package providers

import (
	"context"
	"testing"
)

// TestCachedListViewServesSnapshotAndInvalidates 验证页面投影缓存：第二次读取
// 直接拿缓存（不触发运行时往返），写操作 invalidate 后才重新同步。
func TestCachedListViewServesSnapshotAndInvalidates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, err := f.service.CachedListView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.AccountCount != 2 {
		t.Fatalf("首读 accounts = %d, want 2", first.AccountCount)
	}

	// 运行时少了一个账号：缓存期内仍返回旧投影。
	f.runtime.providers[0].Accounts = f.runtime.providers[0].Accounts[:1]
	cached, err := f.service.CachedListView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cached.AccountCount != 2 {
		t.Fatalf("缓存期内应返回旧投影，实际 accounts = %d", cached.AccountCount)
	}

	// 缓存返回的必须是深拷贝：改它不能污染下次读取。
	cached.Providers[0].Accounts = nil
	again, _ := f.service.CachedListView(ctx)
	if again.AccountCount != 2 {
		t.Fatalf("缓存被调用方修改后污染了下次读取: accounts = %d", again.AccountCount)
	}

	// 写操作使缓存失效，下一读重新同步。
	f.service.invalidateView()
	fresh, err := f.service.CachedListView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.AccountCount != 1 {
		t.Fatalf("invalidate 后应重新同步，accounts = %d, want 1", fresh.AccountCount)
	}
}
