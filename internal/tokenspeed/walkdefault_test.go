package tokenspeed

import (
	"testing"
	"time"
)

// TestDefaultWalkIntervalIsRelaxed 是 P1-5 的性能回归用例：默认完整扫描间隔
// 必须是 15s 而不是 3s。实测单次重扫约 15.6ms，3s 一次占单核 0.52%，
// 15s 一次降到 0.10%；若有人把它改回 3s，空闲 CPU 会翻上去一个量级。
func TestDefaultWalkIntervalIsRelaxed(t *testing.T) {
	if DefaultWalkInterval != 15*time.Second {
		t.Fatalf("DefaultWalkInterval = %v，want 15s", DefaultWalkInterval)
	}
	if got := DefaultConfig().WalkInterval; got != DefaultWalkInterval {
		t.Fatalf("DefaultConfig().WalkInterval = %v，want %v", got, DefaultWalkInterval)
	}
	// 15s 间隔下每分钟最多扫 4 次；3s 会是 20 次。
	perMinute := int(time.Minute / DefaultWalkInterval)
	if perMinute > 4 {
		t.Fatalf("默认间隔下每分钟扫描 %d 次，超过 4 次预算", perMinute)
	}
}

// TestWalkIntervalRuntimeOverrideAffectsDSHToo 确认运行期改间隔对 DSH 源同样
// 生效（两套计时器共用同一个间隔来源）。
func TestWalkIntervalRuntimeOverrideAffectsDSHToo(t *testing.T) {
	service := New(Config{WalkInterval: 15 * time.Second})
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if !service.dshWalkDue(now) {
		t.Fatal("DSH 首轮应重扫")
	}
	if service.dshWalkDue(now.Add(5 * time.Second)) {
		t.Fatal("5s < 15s 时 DSH 不应重扫")
	}
	service.SetWalkInterval(3 * time.Second)
	if !service.dshWalkDue(now.Add(5 * time.Second)) {
		t.Fatal("改成 3s 后 DSH 在 5s 应重扫")
	}
}
