package app

import (
	"context"
	"os"
	"testing"

	"qka/internal/capabilities"
	"qka/internal/providers"
)

func TestRecoverProviderAuthBranches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := &App{cfg: Config{ConfigDir: dir}}
	authPath := providers.StaleAuthPath(dir)
	ref := capabilities.CredentialRef{
		Target: "qka-provider", AccountID: "runtime", SourceID: "auth", Kind: capabilities.Managed,
	}
	readAuth := func() string {
		data, _ := os.ReadFile(authPath)
		return string(data)
	}

	// No credential service: nothing to rebuild.
	a.credentials = nil
	if err := a.recoverProviderAuth(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatal("nil credential service must not write an auth file")
	}

	// A non-NotFound resolve error must surface.
	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{}, errorSecretStore{},
		capabilities.Grant{"credentials.resolve": true}, nil,
	)
	if err := a.recoverProviderAuth(ctx); err == nil {
		t.Fatal("resolve error must surface")
	}

	// A valid stored snapshot is written back verbatim.
	fileStore := newAppSecretStore()
	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{}, fileStore,
		capabilities.Grant{"credentials.resolve": true, "credentials.managed": true}, nil,
	)
	if err := a.credentials.Persist(ctx, ref, `{"token":"ok"}`); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverProviderAuth(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readAuth(); got != `{"token":"ok"}` {
		t.Fatalf("auth file = %q", got)
	}

	// A non-JSON snapshot is ignored (nothing to materialize).
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	if err := a.credentials.Persist(ctx, ref, "not-json"); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverProviderAuth(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatal("non-JSON snapshot must not write an auth file")
	}

	// A write failure must surface.
	if err := a.credentials.Persist(ctx, ref, `{"token":"ok"}`); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(authPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverProviderAuth(ctx); err == nil {
		t.Fatal("write failure must surface")
	}
}
