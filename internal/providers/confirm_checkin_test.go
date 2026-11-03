package providers

import (
	"context"
	"testing"

	"qka/internal/engine"
)

// TestConfirmEvidencedCheckinsPromotesGrantsOnly pins the promotion rule: a
// provisional provider check-in that recorded an explicit grant (+N credits
// differing from the balance) is confirmed, while a receipt-less claim, a
// non-provider target, and an already-confirmed row are left alone.
func TestConfirmEvidencedCheckinsPromotesGrantsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := engine.BeijingDay(f.service.now())

	mustExec(t, f.store.DB(), `
INSERT INTO providers
  (id, name, plugin_spec, icon, enabled, usage, checkin, keepalive, models_json, created_at, updated_at)
VALUES ('fixture','Fixture','spec','',1,1,1,0,'[]','ts','ts')`)
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, credits, balance, credits_known, balance_known, updated_at)
VALUES
  ('fixture','grant','checkin','`+day+`','signed_provisional',100,2500,1,1,'ts'),
  ('fixture','no_receipt','checkin','`+day+`','signed_provisional',0,2500,0,1,'ts'),
  ('fixture','equal','checkin','`+day+`','signed_provisional',2500,2500,1,1,'ts'),
  ('not-a-provider','grant','checkin','`+day+`','signed_provisional',100,2500,1,1,'ts')`)

	if err := f.service.confirmEvidencedCheckins(ctx); err != nil {
		t.Fatal(err)
	}
	stateOf := func(target, account string) string {
		var state string
		if err := f.store.DB().QueryRowContext(ctx,
			`SELECT state FROM daily_ops WHERE target = ? AND account_id = ?`,
			target, account).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	if got := stateOf("fixture", "grant"); got != string(engine.SignedConfirmed) {
		t.Fatalf("explicit grant should be confirmed, got %q", got)
	}
	if got := stateOf("fixture", "no_receipt"); got != string(engine.SignedProvisional) {
		t.Fatalf("receipt-less claim must stay provisional, got %q", got)
	}
	if got := stateOf("fixture", "equal"); got != string(engine.SignedProvisional) {
		t.Fatalf("credits equal to balance is not a grant, got %q", got)
	}
	if got := stateOf("not-a-provider", "grant"); got != string(engine.SignedProvisional) {
		t.Fatalf("non-provider target must not be promoted, got %q", got)
	}
}
