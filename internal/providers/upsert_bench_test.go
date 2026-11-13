package providers

import (
	"context"
	"testing"
	"time"

	"qka/internal/store"
)

// BenchmarkUpsertProviderUnchanged 衡量「刷新页面时 provider 元数据未变化」
// 的单次写路径成本。条件 upsert 命中 WHERE 全 false 时只做一次无行更新的
// UPDATE，应保持低 ns/op 与 0 allocs/op（除 SQL 驱动自身缓冲）。
func BenchmarkUpsertProviderUnchanged(b *testing.B) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	svc := NewService(st.DB(), nil, nil, nil, nil, func() time.Time { return now })
	provider := providerFixture()
	if err := svc.upsertProvider(context.Background(), provider); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := svc.upsertProvider(context.Background(), provider); err != nil {
			b.Fatal(err)
		}
	}
}
