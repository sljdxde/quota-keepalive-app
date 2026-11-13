package providers

import (
	"context"
	"testing"
	"time"

	"qka/internal/providerruntime"
	"qka/internal/store"
)

func newUpsertTestService(t *testing.T, clock *time.Time) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := NewService(st.DB(), nil, nil, nil, nil, func() time.Time { return *clock })
	return svc, st
}

func TestUpsertProviderSkipsUnchangedRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, st := newUpsertTestService(t, &now)
	provider := providerFixture()
	if err := svc.upsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}

	var created, first string
	if err := st.DB().QueryRowContext(ctx, `
SELECT created_at, updated_at FROM providers WHERE id = ?`, provider.ID).
		Scan(&created, &first); err != nil {
		t.Fatal(err)
	}
	// 模拟用户手动启用：重复 upsert 不得覆盖 enabled。
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE providers SET enabled = 1 WHERE id = ?`, provider.ID); err != nil {
		t.Fatal(err)
	}

	var changesBefore, changesAfter int64
	if err := st.DB().QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := svc.upsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("零变化 upsert 不应产生任何写行：total_changes %d -> %d", changesBefore, changesAfter)
	}
	var enabled int
	var gotCreated, gotUpdated string
	if err := st.DB().QueryRowContext(ctx, `
SELECT enabled, created_at, updated_at FROM providers WHERE id = ?`, provider.ID).
		Scan(&enabled, &gotCreated, &gotUpdated); err != nil {
		t.Fatal(err)
	}
	if gotUpdated != first {
		t.Fatalf("零变化 upsert 不应更新 updated_at: first=%s got=%s", first, gotUpdated)
	}
	if gotCreated != created {
		t.Fatalf("零变化 upsert 不应改动 created_at: first=%s got=%s", created, gotCreated)
	}
	if enabled != 1 {
		t.Fatalf("upsert 不得覆盖用户设置的 enabled，实际 %d", enabled)
	}
}

func TestUpsertProviderWritesWhenAnyTrackedFieldChanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*providerruntime.Provider)
	}{
		{"name", func(p *providerruntime.Provider) { p.Name = "Fixture Renamed" }},
		{"plugin_spec", func(p *providerruntime.Provider) {
			p.Spec = "@magpie-community/opencode-fixture-auth-v2"
		}},
		{"icon", func(p *providerruntime.Provider) { p.Icon = "data:image/png;base64,BB==" }},
		{"usage", func(p *providerruntime.Provider) { p.Usage = false }},
		{"checkin", func(p *providerruntime.Provider) { p.Checkin = false }},
		{"keepalive", func(p *providerruntime.Provider) { p.Fetch = false }},
		{"models_json", func(p *providerruntime.Provider) {
			p.Models = append(p.Models, providerruntime.Model{ID: "model-b", Name: "Model B"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			svc, st := newUpsertTestService(t, &now)
			provider := providerFixture()
			if err := svc.upsertProvider(ctx, provider); err != nil {
				t.Fatal(err)
			}

			now = now.Add(time.Minute)
			tc.mutate(&provider)
			if err := svc.upsertProvider(ctx, provider); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := st.DB().QueryRowContext(ctx,
				`SELECT updated_at FROM providers WHERE id = ?`, provider.ID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if want := now.Format(time.RFC3339); got != want {
				t.Fatalf("%s 变化应写入 updated_at=%s，实际 %s", tc.name, want, got)
			}
		})
	}
}
