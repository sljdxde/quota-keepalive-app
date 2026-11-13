package accounts

import (
	"context"
	"testing"
	"time"

	"qka/internal/store"
)

// BenchmarkDiscoverUnchanged 衡量刷新页面时「账号与来源都没变化」的一次
// Discover 成本：一个事务 + 每账号两条条件 upsert，应只做无行更新。
func BenchmarkDiscoverUnchanged(b *testing.B) {
	st, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	svc := NewService(st.DB())
	svc.now = func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) }
	ctx := context.Background()
	found := discovered()
	if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := svc.Discover(ctx, "qoder-cn", found); err != nil {
			b.Fatal(err)
		}
	}
}
