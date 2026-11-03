package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"qka/internal/atomicfile"
	"qka/internal/capabilities"
	"qka/internal/providerruntime"
	"qka/internal/providers"
)

var (
	discoverProviderPlugins = providerruntime.DiscoverPlugins
	launchProviderRuntime   = providerruntime.Launch
)

func (a *App) providerRuntimeFactory() providers.RuntimeFactory {
	return func(ctx context.Context) (providers.Runtime, error) {
		root := a.cfg.ProviderPluginsRoot
		if root == "" {
			return nil, errors.New("provider plugins are not bundled")
		}
		plugins, err := discoverProviderPlugins(root)
		if err != nil {
			return nil, err
		}
		if len(plugins) == 0 {
			return nil, errors.New("no provider plugins were found")
		}
		authPath := providers.StaleAuthPath(a.cfg.ConfigDir)
		if err := providerruntime.RemoveAuthFiles(authPath); err != nil {
			return nil, err
		}
		auth := []byte("{}")
		if data, authErr := a.providerAuthBytes(ctx); authErr != nil {
			return nil, authErr
		} else if data != nil {
			auth = data
		}
		if err := atomicfile.WriteFile(authPath, auth, 0o600); err != nil {
			return nil, err
		}
		host, err := launchProviderRuntime(ctx, providerruntime.LaunchConfig{
			Bun:        a.cfg.BunPath,
			ConfigDir:  a.cfg.ConfigDir,
			AuthPath:   authPath,
			Directory:  a.cfg.ConfigDir,
			Plugins:    plugins,
			LoadMillis: 30_000,
			ListMillis: 30_000,
		})
		if err != nil {
			_ = providerruntime.RemoveAuthFiles(authPath)
			return nil, fmt.Errorf("provider runtime: %w", err)
		}
		return host, nil
	}
}

// providerAuthBytes returns the durable provider-runtime auth snapshot, or nil
// when none is stored. This snapshot is the source of truth the volatile
// runtime auth view file is materialized from.
func (a *App) providerAuthBytes(ctx context.Context) ([]byte, error) {
	if a.credentials == nil {
		return nil, nil
	}
	credential, err := a.credentials.Resolve(ctx, capabilities.CredentialRef{
		Target: "qka-provider", AccountID: "runtime", SourceID: "auth",
		Kind: capabilities.Managed,
	})
	switch {
	case err == nil:
		if !json.Valid([]byte(credential.Secret)) {
			return nil, nil
		}
		return []byte(credential.Secret), nil
	case errors.Is(err, capabilities.ErrSecretNotFound):
		return nil, nil
	default:
		return nil, err
	}
}

// recoverProviderAuth rewrites the transient runtime auth view file from the
// durable credential store. providers.Service invokes it when the runtime
// reports zero accounts for a provider that still has stored accounts, i.e.
// when the auth view file went missing rather than the accounts being removed.
func (a *App) recoverProviderAuth(ctx context.Context) error {
	data, err := a.providerAuthBytes(ctx)
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	return atomicfile.WriteFile(providers.StaleAuthPath(a.cfg.ConfigDir), data, 0o600)
}
