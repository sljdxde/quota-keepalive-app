package providers

import (
	"context"
	"errors"
	"testing"

	"qka/internal/providerruntime"
)

// TestListKeepsStoredAccountsWhenRuntimeReportsNone reproduces the data-loss
// bug: the provider runtime's account list is derived from a volatile auth
// view file. When that file is missing or unreadable the runtime reports zero
// accounts, and a plain sync must not treat that as "every account was removed"
// and delete the durable account projection.
func TestListKeepsStoredAccountsWhenRuntimeReportsNone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := f.account.List(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("setup: want 2 stored accounts, got %d", len(stored))
	}

	// Simulate the transient auth-view outage: same providers, zero accounts.
	f.runtime.providers[0].Accounts = nil
	f.runtime.providers[0].SignedIn = false

	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err = f.account.List(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("accounts vanished after an empty runtime view: got %d, want 2", len(stored))
	}
}

// TestListRecoversStaleRuntimeAuth verifies that when the runtime reports zero
// accounts but the projection still has some, List asks the installed recovery
// hook to rebuild the auth view and retries once, so accounts reappear without
// an app restart.
func TestListRecoversStaleRuntimeAuth(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	f.runtime.providers[0].Accounts = nil
	f.runtime.providers[0].SignedIn = false
	recoveries := 0
	f.service.SetRuntimeAuthRecovery(func(context.Context) error {
		recoveries++
		f.runtime.providers[0] = providerFixture()
		return nil
	})

	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recoveries != 1 {
		t.Fatalf("recovery hook calls = %d, want 1", recoveries)
	}
	if view.AccountCount != 2 {
		t.Fatalf("accounts not recovered into view: %d", view.AccountCount)
	}
}

// TestListSurvivesFailedAuthRecovery verifies a failing recovery hook does not
// delete accounts and does not abort the list.
func TestListSurvivesFailedAuthRecovery(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	f.runtime.providers[0].Accounts = nil
	f.runtime.providers[0].SignedIn = false
	f.service.SetRuntimeAuthRecovery(func(context.Context) error {
		return errors.New("keychain locked")
	})

	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountCount != 0 {
		t.Fatalf("expected empty view while recovery fails, got %d", view.AccountCount)
	}
	stored, err := f.account.List(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("accounts must survive a failed recovery: got %d", len(stored))
	}
}

// TestListPrunesPerAccountWhenProviderStillReportsAccounts keeps the intended
// cleanup: when the runtime does report accounts, an account that is genuinely
// gone is removed. This guards against over-broadening the empty-view safety.
func TestListPrunesPerAccountWhenProviderStillReportsAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	f.runtime.providers[0].Accounts = []providerruntime.Account{
		{Key: "a1", Type: "oauth", AccountID: "user@example.com", Hint: "first"},
	}
	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountCount != 1 {
		t.Fatalf("view accounts = %d, want 1", view.AccountCount)
	}
	if _, err := f.account.Get(ctx, "fixture", "a2"); err == nil {
		t.Fatal("gone per-account plugin source should be pruned when others remain")
	}
}

// TestRuntimeOmitsStoredAccountsBranches pins the helper's decisions: a
// provider that reports accounts is authoritative (false), a genuinely empty
// provider with no stored rows is fine (false), an empty view over stored rows
// is stale (true), and a projection read error is never mistaken for a signal.
func TestRuntimeOmitsStoredAccountsBranches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.service.runtimeOmitsStoredAccounts(ctx, f.runtime.providers) {
		t.Fatal("a provider reporting accounts is not stale")
	}
	if f.service.runtimeOmitsStoredAccounts(ctx, []providerruntime.Provider{{ID: "none"}}) {
		t.Fatal("a provider with no stored accounts is not stale")
	}

	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if !f.service.runtimeOmitsStoredAccounts(ctx, []providerruntime.Provider{{ID: "fixture"}}) {
		t.Fatal("an empty view over stored accounts must be treated as stale")
	}

	mustExec(t, f.store.DB(), `DROP TABLE accounts`)
	if f.service.runtimeOmitsStoredAccounts(ctx, []providerruntime.Provider{{ID: "fixture"}}) {
		t.Fatal("a projection read error must not be treated as stale")
	}
}
