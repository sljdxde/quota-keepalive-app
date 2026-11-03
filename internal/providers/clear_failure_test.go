package providers

import (
	"context"
	"testing"

	"qka/internal/engine"
)

// TestClearStaleFailureDropsFailedRowsOnly verifies that a fresh sign-in clears
// only today's failure projection (which is stale once the credential is
// renewed) while keeping real progress such as "already".
func TestClearStaleFailureDropsFailedRowsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := engine.BeijingDay(f.service.now())
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops (target, account_id, operation, day_key, state, updated_at)
VALUES ('fixture','a1','checkin','`+day+`','failed_final','ts'),
       ('fixture','a2','checkin','`+day+`','already','ts')`)

	if err := f.service.clearStaleFailure(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	var failed, kept int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM daily_ops WHERE target='fixture' AND account_id='a1'`).
		Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM daily_ops WHERE target='fixture' AND account_id='a2'`).
		Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("failed row should be cleared, got %d", failed)
	}
	if kept != 1 {
		t.Fatalf("successful row must survive, got %d", kept)
	}
	// An empty account is a no-op (defensive; new sign-ins resolve later).
	if err := f.service.clearStaleFailure(ctx, "fixture", "  "); err != nil {
		t.Fatal(err)
	}
}

// TestClearAuthFailureOnlyDropsAuthErrors verifies a successful usage refresh
// clears a stale auth failure but leaves a non-auth check-in failure visible.
func TestClearAuthFailureOnlyDropsAuthErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := engine.BeijingDay(f.service.now())
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops (target, account_id, operation, day_key, state, message, updated_at)
VALUES ('fixture','a1','checkin','`+day+`','failed_final','活动列表读取失败：HTTP 401: APIG.0301 Incorrect IAM authentication','ts'),
       ('fixture','a2','checkin','`+day+`','failed_final','不符合活动资格','ts')`)
	if err := f.service.clearAuthFailure(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.clearAuthFailure(ctx, "fixture", "a2"); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, id := range []string{"a1", "a2"} {
		var n int
		if err := f.store.DB().QueryRowContext(ctx,
			`SELECT count(*) FROM daily_ops WHERE account_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[id] = n
	}
	if counts["a1"] != 0 || counts["a2"] != 1 {
		t.Fatalf("auth row should clear, non-auth stay: %+v", counts)
	}
}
