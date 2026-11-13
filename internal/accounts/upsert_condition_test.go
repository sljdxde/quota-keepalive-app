package accounts

import (
	"context"
	"testing"
	"time"
)

type manualClock struct {
	at time.Time
}

func (c *manualClock) Now() time.Time          { return c.at }
func (c *manualClock) Advance(d time.Duration) { c.at = c.at.Add(d) }

func newClockedService(t *testing.T) (*Service, *manualClock) {
	t.Helper()
	svc := newService(t)
	clock := &manualClock{at: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)}
	svc.now = clock.Now
	return svc, clock
}

func accountAndSourceUpdatedAt(
	t *testing.T, svc *Service, target, accountID, sourceID string,
) (string, string) {
	t.Helper()
	var accountTS, sourceTS string
	if err := svc.db.QueryRow(`
SELECT updated_at FROM accounts WHERE target = ? AND id = ?`,
		target, accountID).Scan(&accountTS); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`
SELECT updated_at FROM sources WHERE target = ? AND account_id = ? AND id = ?`,
		target, accountID, sourceID).Scan(&sourceTS); err != nil {
		t.Fatal(err)
	}
	return accountTS, sourceTS
}

func TestDiscoverNoChangeDoesNotTouchUpdatedAt(t *testing.T) {
	svc, clock := newClockedService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}
	accountBefore, sourceBefore := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")

	var changesBefore, changesAfter int64
	if err := svc.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	if err := svc.Discover(ctx, "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("零变化 Discover 不应产生任何写行：total_changes %d -> %d", changesBefore, changesAfter)
	}
	accountAfter, sourceAfter := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")
	if accountAfter != accountBefore || sourceAfter != sourceBefore {
		t.Fatalf("零变化 Discover 不应更新 updated_at: accounts %s->%s, sources %s->%s",
			accountBefore, accountAfter, sourceBefore, sourceAfter)
	}
}

// Discover 上报 healthy 时，CASE 会保留已有的 reauth_required；此时最终值没变，
// 不能因为 excluded.health 不同就重写 updated_at。
func TestDiscoverPreservedReauthHealthDoesNotTouchUpdatedAt(t *testing.T) {
	svc, clock := newClockedService(t)
	ctx := context.Background()
	found := discovered()[:1]
	found[0].Health = HealthReauthRequired
	if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
		t.Fatal(err)
	}
	accountBefore, sourceBefore := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")

	clock.Advance(time.Hour)
	found[0].Health = HealthHealthy
	if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
		t.Fatal(err)
	}
	accountAfter, sourceAfter := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")
	if accountAfter != accountBefore || sourceAfter != sourceBefore {
		t.Fatalf("health 被 CASE 保留时不应写库: accounts %s->%s, sources %s->%s",
			accountBefore, accountAfter, sourceBefore, sourceAfter)
	}
	acc, err := svc.Get(ctx, "qoder-cn", "q_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(acc.Sources) != 1 || acc.Sources[0].Health != HealthReauthRequired {
		t.Fatalf("health 应保持 reauth_required，实际 %+v", acc.Sources)
	}
}

func TestDiscoverWritesWhenAccountFieldsChange(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DiscoveredAccount)
	}{
		{"display_name", func(a *DiscoveredAccount) { a.DisplayName = "Qoder One Renamed" }},
		{"masked_identity", func(a *DiscoveredAccount) { a.MaskedIdentity = "****0000" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock := newClockedService(t)
			ctx := context.Background()
			found := discovered()[:1]
			if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
				t.Fatal(err)
			}
			accountBefore, sourceBefore := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")

			clock.Advance(time.Minute)
			tc.mutate(&found[0])
			if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
				t.Fatal(err)
			}
			accountAfter, sourceAfter := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")
			want := clock.Now().Format(time.RFC3339)
			if accountAfter != want || accountAfter == accountBefore {
				t.Fatalf("%s 变化应写入 accounts.updated_at=%s，实际 %s（原 %s）",
					tc.name, want, accountAfter, accountBefore)
			}
			if sourceAfter != sourceBefore {
				t.Fatalf("仅账号字段变化不应写 sources: %s -> %s", sourceBefore, sourceAfter)
			}
		})
	}
}

func TestDiscoverWritesWhenSourceFieldsChange(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DiscoveredAccount)
	}{
		{"kind", func(a *DiscoveredAccount) { a.SourceKind = KindImported }},
		{"health", func(a *DiscoveredAccount) { a.Health = HealthDisabled }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock := newClockedService(t)
			ctx := context.Background()
			found := discovered()[:1]
			if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
				t.Fatal(err)
			}
			accountBefore, sourceBefore := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")

			clock.Advance(time.Minute)
			tc.mutate(&found[0])
			if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
				t.Fatal(err)
			}
			accountAfter, sourceAfter := accountAndSourceUpdatedAt(t, svc, "qoder-cn", "q_1", "qs_1")
			want := clock.Now().Format(time.RFC3339)
			if sourceAfter != want || sourceAfter == sourceBefore {
				t.Fatalf("%s 变化应写入 sources.updated_at=%s，实际 %s（原 %s）",
					tc.name, want, sourceAfter, sourceBefore)
			}
			if accountAfter != accountBefore {
				t.Fatalf("仅来源字段变化不应写 accounts: %s -> %s", accountBefore, accountAfter)
			}
		})
	}
}
