package gui

import (
	"context"
	"errors"
	"testing"
	"time"

	"qka/internal/status"
)

func TestStatusNeedsAttention(t *testing.T) {
	tests := []struct {
		name string
		view status.View
		want bool
	}{
		{name: "empty", view: status.View{}, want: false},
		{
			name: "reauth",
			view: status.View{Accounts: []status.AccountStatus{{
				SourceHealth: "reauth_required",
			}}},
			want: true,
		},
		{
			name: "manual",
			view: status.View{Accounts: []status.AccountStatus{{
				Operations: []status.OpStatus{{State: "manual_required"}},
			}}},
			want: true,
		},
		{
			name: "retryable",
			view: status.View{Accounts: []status.AccountStatus{{
				Operations: []status.OpStatus{{State: "failed_retryable"}},
			}}},
			want: true,
		},
		{
			name: "final",
			view: status.View{Accounts: []status.AccountStatus{{
				Operations: []status.OpStatus{{State: "failed_final"}},
			}}},
			want: true,
		},
		{
			name: "healthy",
			view: status.View{Accounts: []status.AccountStatus{{
				SourceHealth: "healthy",
				Operations:   []status.OpStatus{{State: "already"}},
			}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusNeedsAttention(tt.view); got != tt.want {
				t.Fatalf("statusNeedsAttention = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMonitorStatusRefreshesTrayFailureState(t *testing.T) {
	oldInterval := statusPollInterval
	statusPollInterval = time.Millisecond
	t.Cleanup(func() { statusPollInterval = oldInterval })

	calls := make(chan struct{}, 4)
	app := New(Deps{Status: func(context.Context) (status.View, error) {
		calls <- struct{}{}
		return status.View{Accounts: []status.AccountStatus{{
			Operations: []status.OpStatus{{State: "failed_final"}},
		}}}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.monitorStatus(ctx)
	}()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("status monitor did not run immediately")
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("status monitor did not poll on its interval")
	}
	if !app.failureOn.Load() {
		t.Fatal("failed status should set the tray failure icon")
	}
	cancel()
	<-done
}

func TestRefreshFailureStateHandlesUnavailableStatus(t *testing.T) {
	app := &App{}
	app.refreshFailureState(context.Background())

	app.status = func(context.Context) (status.View, error) {
		return status.View{}, errors.New("status unavailable")
	}
	app.refreshFailureState(context.Background())
	if app.failureOn.Load() {
		t.Fatal("status error must not change failure state")
	}
}

func TestRunStartsStatusMonitor(t *testing.T) {
	called := make(chan struct{}, 1)
	app := New(Deps{Status: func(context.Context) (status.View, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return status.View{}, nil
	}})
	forceAppStarting(t, app.wails)
	_ = app.Run()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("Run did not start the status monitor")
	}
}

func TestSetFailureStateDeduplicatesRepeatedState(t *testing.T) {
	app := New(Deps{})
	app.SetFailureState(true)
	app.SetFailureState(true)
	app.SetFailureState(false)
	app.SetFailureState(false)
	if app.failureOn.Load() {
		t.Fatal("failure state should be false at the end")
	}
}

// TestStatusSignatureChangesWithCheckinState 保证指纹在签到状态/额度变化时改变，
// 未变化时稳定 —— 后端据此决定是否推送「结果已更新」。
func TestStatusSignatureChangesWithCheckinState(t *testing.T) {
	credits := 100.0
	balance := 2000.0
	base := status.View{
		LastInvocation: "2026-11-04T00:38:00Z",
		Accounts: []status.AccountStatus{{
			Target: "trae-cn", AccountID: "trae-cn", Enabled: true,
			SourceHealth: "healthy",
			Operations: []status.OpStatus{{
				Operation: "checkin", State: "signed_provisional",
				Credits: &credits, Balance: &balance,
			}},
		}},
	}
	if statusSignature(base) != statusSignature(base) {
		t.Fatal("同一状态指纹必须稳定")
	}
	changed := base
	changed.Accounts = []status.AccountStatus{{
		Target: "trae-cn", AccountID: "trae-cn", Enabled: true,
		SourceHealth: "healthy",
		Operations: []status.OpStatus{{
			Operation: "checkin", State: "signed_confirmed",
			Credits: &credits, Balance: &balance,
		}},
	}}
	if statusSignature(changed) == statusSignature(base) {
		t.Fatal("状态从待复核变为已确认时指纹必须变化")
	}
}
