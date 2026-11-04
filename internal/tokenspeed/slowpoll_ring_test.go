package tokenspeed

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordSlowPoll 的环形记录：容量内追加、超容量覆盖最旧、RecentSlowPolls
// 始终按旧→新返回拷贝（修改返回值不影响内部状态）。
func TestSlowPollRingKeepsNewestOrdered(t *testing.T) {
	s := New(Config{})
	base := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	for i := 1; i <= slowPollCapacity+4; i++ {
		s.recordSlowPoll(time.Duration(150+i)*time.Millisecond, base.Add(time.Duration(i)*time.Second))
	}
	got := s.RecentSlowPolls()
	if len(got) != slowPollCapacity {
		t.Fatalf("环形记录应保留 %d 条，实际 %d", slowPollCapacity, len(got))
	}
	// 覆盖 4 条后剩下的应是第 5..14 次（毫秒数 155..164 递增）。
	for i, sample := range got {
		wantMS := int64(150 + 5 + i)
		if sample.ElapsedMS != wantMS {
			t.Fatalf("第 %d 条耗时应为 %dms，实际 %dms", i, wantMS, sample.ElapsedMS)
		}
		wantAt := base.Add(time.Duration(5+i) * time.Second).Format(time.RFC3339)
		if sample.At != wantAt {
			t.Fatalf("第 %d 条时间应为 %s，实际 %s", i, wantAt, sample.At)
		}
	}
	// 返回值是拷贝：外部改动不得污染内部记录。
	got[0].ElapsedMS = 999999
	if again := s.RecentSlowPolls(); again[0].ElapsedMS == 999999 {
		t.Fatal("RecentSlowPolls 应返回拷贝，外部修改不应影响内部记录")
	}
}

// 未记录过时 RecentSlowPolls 返回空且不 panic；阈值以内的轮询不该被记录。
func TestSlowPollRingEmptyAndThreshold(t *testing.T) {
	s := New(Config{})
	if got := s.RecentSlowPolls(); len(got) != 0 {
		t.Fatalf("未记录时应为空，实际 %d 条", len(got))
	}
	if _, ok := slowPollWarning(slowPollThreshold); ok {
		t.Fatal("恰好等于阈值不应算慢轮询")
	}
	if _, ok := slowPollWarning(slowPollThreshold - 1); ok {
		t.Fatal("阈值以内不应算慢轮询")
	}
	if got := SlowPollThresholdMS(); got != 150 {
		t.Fatalf("阈值应为 150ms，实际 %d", got)
	}
}

// 记录与查询并发安全：多 goroutine 同时写、读不竞态（-race 下验证）。
func TestSlowPollRingConcurrent(t *testing.T) {
	s := New(Config{})
	base := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.recordSlowPoll(time.Duration(200+j)*time.Millisecond,
					base.Add(time.Duration(i*1000+j)*time.Millisecond))
				_ = s.RecentSlowPolls()
			}
		}(i)
	}
	wg.Wait()
	if got := len(s.RecentSlowPolls()); got != slowPollCapacity {
		t.Fatalf("并发写完应仍保留 %d 条，实际 %d", slowPollCapacity, got)
	}
}

// 慢轮询样本只含耗时与时间戳：字段序列化后不出现任何内容性字段。
func TestSlowPollSampleFieldsAreMetadataOnly(t *testing.T) {
	sample := SlowPollSample{ElapsedMS: 213, At: "2026-11-04T12:00:00+08:00"}
	rendered := fmt.Sprintf("%+v", sample)
	for _, secret := range []string{"token", "prompt", "content", "account"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("样本渲染不应包含 %q：%s", secret, rendered)
		}
	}
}
