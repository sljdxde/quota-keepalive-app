package gui

import (
	"testing"
	"time"

	"qka/internal/settings"
	"qka/internal/tokenspeed"
)

// 广播节奏必须跟随采样频率：采样调密了却仍按 750ms 广播，等于把设置抹掉。
func TestTokenSpeedPublishIntervalFollowsSampling(t *testing.T) {
	tests := []struct {
		name   string
		sample time.Duration
		want   time.Duration
	}{
		{name: "200ms 采样按 200ms 广播", sample: 200 * time.Millisecond, want: 200 * time.Millisecond},
		{name: "300ms 采样按 300ms 广播", sample: 300 * time.Millisecond, want: 300 * time.Millisecond},
		{name: "500ms 采样按 500ms 广播", sample: 500 * time.Millisecond, want: 500 * time.Millisecond},
		{name: "750ms 采样按 750ms 广播", sample: 750 * time.Millisecond, want: 750 * time.Millisecond},
		// 上限：再密也不给 WebView 制造空转。
		{name: "50ms 采样压到 200ms", sample: 50 * time.Millisecond, want: 200 * time.Millisecond},
		// 下限：设置被写坏时不要变成每秒几十次。
		{name: "5s 采样压到 1s", sample: 5 * time.Second, want: time.Second},
		{name: "零值回退 200ms", sample: 0, want: 200 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenSpeedPublishInterval(tc.sample); got != tc.want {
				t.Fatalf("tokenSpeedPublishInterval(%v) = %v, want %v", tc.sample, got, tc.want)
			}
		})
	}
}

// 采样间隔从服务读：设置页改完立即影响广播节奏。
func TestTokenSpeedSampleIntervalReadsService(t *testing.T) {
	service := tokenspeed.New(tokenspeed.Config{PollInterval: time.Second})
	app := &App{tokenSpeed: service}
	if got := app.tokenSpeedSampleInterval(); got != time.Second {
		t.Fatalf("初始采样间隔 = %v，want 1s", got)
	}
	service.SetPollInterval(200 * time.Millisecond)
	if got := app.tokenSpeedSampleInterval(); got != 200*time.Millisecond {
		t.Fatalf("调整后采样间隔 = %v，want 200ms", got)
	}
}

// 没有服务时退回设置值；两者都没有时给默认 500ms（不能是 0，否则 ticker 会 panic）。
func TestTokenSpeedSampleIntervalFallsBackToSettings(t *testing.T) {
	st := settings.Default()
	if err := st.SetString(settings.KeyTokenSpeedPoll, "300"); err != nil {
		t.Fatal(err)
	}
	app := &App{settings: func() *settings.Settings { return st }}
	if got := app.tokenSpeedSampleInterval(); got != 300*time.Millisecond {
		t.Fatalf("无服务时应读设置，实际 %v", got)
	}

	empty := &App{}
	if got := empty.tokenSpeedSampleInterval(); got != 500*time.Millisecond {
		t.Fatalf("无服务无设置时应回退 500ms，实际 %v", got)
	}
}

// 菜单栏样式从设置读；没有设置来源时用纯文本，不能 panic。
func TestMenuBarSpeedStyleFallsBack(t *testing.T) {
	if got := (&App{}).menuBarSpeedStyle(); got != settings.MenuBarSpeedLabel {
		t.Fatalf("无设置来源时应回退纯文本，实际 %q", got)
	}
	st := settings.Default()
	if err := st.SetString(settings.KeyTokenSpeedMenuBar, settings.MenuBarSpeedTinted); err != nil {
		t.Fatal(err)
	}
	app := &App{settings: func() *settings.Settings { return st }}
	if got := app.menuBarSpeedStyle(); got != settings.MenuBarSpeedTinted {
		t.Fatalf("应读到分段彩色，实际 %q", got)
	}
}

// 启动时把设置里的采样间隔应用给服务。
func TestApplyTokenSpeedPollAppliesSetting(t *testing.T) {
	st := settings.Default()
	if err := st.SetString(settings.KeyTokenSpeedPoll, "200"); err != nil {
		t.Fatal(err)
	}
	service := tokenspeed.New(tokenspeed.Config{PollInterval: time.Second})
	applyTokenSpeedPoll(service, func() *settings.Settings { return st })
	if got := service.PollInterval(); got != 200*time.Millisecond {
		t.Fatalf("应用后采样间隔 = %v，want 200ms", got)
	}

	// 缺服务或缺设置来源时必须安全跳过。
	applyTokenSpeedPoll(nil, func() *settings.Settings { return st })
	applyTokenSpeedPoll(service, nil)
}
