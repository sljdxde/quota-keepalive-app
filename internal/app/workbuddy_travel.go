package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"qka/internal/capabilities"
	"qka/internal/workbuddy"
)

// workBuddyTravelEnabled 是猫猫旅行的总开关。上游对旅行请求做了账号级风控
// （403 / 11140），在确认安全口径前保持关闭：不自动执行、界面不显示入口、
// 手动绑定也直接拒绝。代码保留，改回 true 即可恢复。
const workBuddyTravelEnabled = false

// workBuddyTravelCooldown 防止「巡检」被频繁点击时反复打上游：同一账号
// 30 分钟内最多推进一次猫猫旅行（状态查询本身也算一次上游调用）。
const workBuddyTravelCooldown = 30 * time.Minute

// runWorkBuddyTravel 在定时/手动巡检的每日活跃阶段之后，为每个已启用且
// 开启每日活跃的 WorkBuddy 账号推进一次猫猫旅行：归来领奖、在家派出、
// 旅行中跳过。失败只记日志，不阻断其它账号。
func (a *App) runWorkBuddyTravel(ctx context.Context) (RunSummary, error) {
	summary := RunSummary{}
	if !workBuddyTravelEnabled || a.providers == nil {
		return summary, nil
	}
	view, err := a.providers.ActivityView(ctx)
	if err != nil {
		return summary, err
	}
	var failures []error
	for _, account := range view.Accounts {
		if account.Provider != "workbuddy" || !account.Enabled || !account.TaskEnabled {
			continue
		}
		if a.workBuddyTravelCooling(ctx, account.Account) {
			summary.Skipped++
			continue
		}
		result, runErr := a.providers.RunWorkBuddyTravel(ctx, account.Account)
		if runErr != nil {
			summary.Attempted++
			summary.NeedsAttention++
			failures = append(failures, runErr)
			a.writeWorkBuddyTravelLog(ctx, account.Account,
				workbuddy.TravelResult{OK: false, Message: "猫猫旅行执行失败：" + runErr.Error()})
			continue
		}
		summary.Attempted++
		if result.OK {
			summary.Succeeded++
		} else {
			summary.NeedsAttention++
		}
		a.writeWorkBuddyTravelLog(ctx, account.Account, result)
	}
	return summary, errors.Join(failures...)
}

// workBuddyTravelCooling reports whether this account's travel was attempted
// within the cooldown window (based on the last travel log row).
func (a *App) workBuddyTravelCooling(ctx context.Context, account string) bool {
	if a.store == nil {
		return false
	}
	var ts string
	err := a.store.DB().QueryRowContext(ctx, `
SELECT ts FROM logs
WHERE target = 'workbuddy' AND account_id = ? AND operation = 'travel'
ORDER BY id DESC LIMIT 1`, account).Scan(&ts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		return false
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(ts))
	if err != nil {
		return false
	}
	return a.now().Sub(parsed) < workBuddyTravelCooldown
}

// writeWorkBuddyTravelLog records one redacted travel step; log failures never
// turn a successful upstream step into a retry.
func (a *App) writeWorkBuddyTravelLog(
	ctx context.Context, account string, result workbuddy.TravelResult,
) {
	if a.store == nil {
		return
	}
	level := "info"
	state := "completed"
	switch {
	case result.Restricted:
		state = "failed_final"
		level = "error"
	case !result.OK:
		state = "failed_retryable"
		level = "warn"
	}
	svc := capabilities.NewLogService(
		a.store.DB(),
		capabilities.Grant{"log.write": true},
		a.now,
	)
	message := strings.TrimSpace(result.Message)
	if message == "" {
		message = "猫猫旅行已推进"
	}
	_ = svc.WriteWithState(ctx, capabilities.Namespace{
		Target: "workbuddy", AccountID: account, Operation: "travel",
	}, state, level, message)
}
