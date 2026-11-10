package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"qka/internal/accounts"
	"qka/internal/engine"
	"qka/internal/features"
	"qka/internal/providers"
	"qka/internal/runlock"
)

var executeRunOperation = (*App).runOperation

// RunSummary is a redacted aggregate of one RunAll invocation. It deliberately
// omits account IDs and upstream messages so it is safe for CLI JSON, schedule
// history and UI status.
type RunSummary struct {
	Attempted      int `json:"attempted"`
	Succeeded      int `json:"succeeded"`
	NeedsAttention int `json:"needs_attention"`
	Skipped        int `json:"skipped"`
}

// RunAll 是「立即全部执行」的后端：与宿主调用同一条执行链，按预演（plan_validate）
// 中可执行的账号×操作逐个运行，遵守当天幂等与每日活跃冷却；单个失败不拖垮其余，
// 最终返回聚合错误（若有）。
func (a *App) RunAll(ctx context.Context) error {
	_, err := a.RunAllGuarded(ctx)
	return err
}

// RunAllGuarded serializes manual and scheduled execution for this config
// directory before entering the same RunAllSummary core.
func (a *App) RunAllGuarded(ctx context.Context) (RunSummary, error) {
	if a.cfg.ConfigDir == "" {
		return a.RunAllSummary(ctx)
	}
	lock, err := runlock.Acquire(a.cfg.ConfigDir)
	if err != nil {
		return RunSummary{}, err
	}
	defer lock.Close()
	return a.RunAllSummary(ctx)
}

// RunOperationGuarded serializes a single account operation with RunAll and
// task-run. Callers inside RunAllSummary already hold the lock and must use
// runOperation directly.
func (a *App) RunOperationGuarded(
	ctx context.Context, target, accountID, operation string,
) (any, error) {
	if a.cfg.ConfigDir == "" {
		return a.runOperation(ctx, target, accountID, operation)
	}
	lock, err := runlock.Acquire(a.cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return a.runOperation(ctx, target, accountID, operation)
}

// RunAllSummary executes the shared business chain. Callers that already hold
// the config-directory run lock (task-run) use this directly; GUI/manual callers
// use RunAll or RunAllGuarded.
func (a *App) RunAllSummary(ctx context.Context) (RunSummary, error) {
	return a.RunAllActionSummary(ctx, "all")
}

// RunAllActionSummary executes only the requested operation family. The
// scheduler uses all/checkin/activity while manual "run all" keeps the same
// shared state machine and Beijing-day idempotency rules.
func (a *App) RunAllActionSummary(ctx context.Context, action string) (RunSummary, error) {
	if !features.ValidAction(action) {
		return RunSummary{}, fmt.Errorf("unsupported run action %q", action)
	}
	action = features.NormalizeAction(action)
	providerView, providerAccounts, providerErr := a.providerExecutionView(ctx)
	providerActive := a.providers != nil && a.cfg.ProviderPluginsRoot != ""

	plan, err := a.status.PlanValidate(ctx)
	if err != nil {
		return RunSummary{}, err
	}

	summary := RunSummary{}
	var failures []error
	if providerErr != nil {
		failures = append(failures, providerErr)
	}
	for _, item := range plan.Items {
		// 适配器路径只有 checkin；每日活跃由 provider 插件通道执行。
		// 先排除非 checkin 计划项，再按 action 过滤，避免 activity 误跑签到。
		if item.Operation != features.OperationCheckin ||
			!actionRunsOperation(action, item.Operation) {
			continue
		}
		if providerAccounts[item.Target][item.AccountID] {
			// Provider accounts have their own lifecycle and history. They are
			// executed only by providers.RunAllView below.
			continue
		}
		// 预演标注 no_change（冲突/冷却/已终态）的项跳过，不重复发起。
		// review 只允许 manual_required 的只读复查，不能触发新的领取。
		if item.Transition == "no_change" {
			summary.Skipped++
			continue
		}
		result, err := executeRunOperation(a, ctx, item.Target, item.AccountID, item.Operation)
		if err != nil {
			summary.Attempted++
			failures = append(failures, fmt.Errorf("%s/%s/%s: %w",
				item.Target, item.AccountID, item.Operation, err))
			summary.NeedsAttention++
			continue
		}
		if obs, ok := result.(engine.Observation); ok {
			if !obs.Executed {
				summary.Skipped++
				continue
			}
			summary.Attempted++
			switch obs.State {
			case engine.FailedRetryable, engine.FailedFinal, engine.ManualRequired:
				summary.NeedsAttention++
			default:
				summary.Succeeded++
			}
			continue
		}
		summary.Attempted++
		summary.Succeeded++
	}

	if providerActive && providerErr == nil {
		providerSummary, runErr := a.providers.RunAllView(ctx, providerView, action, nil)
		summary.Attempted += providerSummary.Attempted
		summary.Succeeded += providerSummary.Succeeded
		summary.NeedsAttention += providerSummary.NeedsAttention
		summary.Skipped += providerSummary.Skipped
		if runErr != nil {
			failures = append(failures, runErr)
		}
	}
	// WorkBuddy 猫猫旅行：每日活跃阶段之后推进一次（归来领奖 / 在家派出 /
	// 旅行中跳过）。失败只记日志，不阻断其它账号；30 分钟冷却避免频繁打上游。
	if workBuddyTravelEnabled && providerActive && providerErr == nil &&
		actionRunsOperation(action, features.OperationActivity) {
		travelSummary, travelErr := a.runWorkBuddyTravel(ctx)
		summary.Attempted += travelSummary.Attempted
		summary.Succeeded += travelSummary.Succeeded
		summary.NeedsAttention += travelSummary.NeedsAttention
		summary.Skipped += travelSummary.Skipped
		if travelErr != nil {
			failures = append(failures, travelErr)
		}
	}
	return summary, errors.Join(failures...)
}

// actionRunsOperation reports whether one adapter operation belongs to the
// requested automation action. Daily activity never runs through adapters.
func actionRunsOperation(action, operation string) bool {
	switch features.NormalizeAction(action) {
	case features.ActionAll:
		return true
	case features.ActionCheckin:
		return operation == features.OperationCheckin
	case features.ActionActivity:
		return operation == features.OperationActivity
	default:
		return false
	}
}

func (a *App) providerExecutionView(
	ctx context.Context,
) (providers.View, map[string]map[string]bool, error) {
	accountsByProvider := map[string]map[string]bool{}
	if a.providers == nil || a.cfg.ProviderPluginsRoot == "" {
		return providers.View{}, accountsByProvider, nil
	}
	view, err := a.providers.List(ctx)
	if err != nil {
		return providers.View{}, accountsByProvider, err
	}
	for _, provider := range view.Providers {
		keys := map[string]bool{}
		for _, account := range provider.Accounts {
			keys[account.Key] = true
		}
		accountsByProvider[provider.ID] = keys
	}
	return view, accountsByProvider, nil
}

// discoverViaHost 调用目标适配器的 accounts.discover，把脱敏结果解析为发现账号。
func (a *App) discoverViaHost(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error) {
	host, ok := a.hosts[target]
	if !ok {
		return nil, fmt.Errorf("目标适配器未运行: %s", target)
	}

	raw, err := host.Call(ctx, "accounts.discover")
	if err != nil {
		return nil, err
	}

	var result struct {
		Status   string `json:"status"`
		Accounts []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"displayName"`
			MaskedIdentity string `json:"maskedIdentity"`
			Source         struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Health string `json:"health"`
			} `json:"source"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	disc := make([]accounts.DiscoveredAccount, 0, len(result.Accounts))
	for _, ac := range result.Accounts {
		disc = append(disc, accounts.DiscoveredAccount{
			ID:             ac.ID,
			DisplayName:    ac.DisplayName,
			MaskedIdentity: ac.MaskedIdentity,
			SourceID:       ac.Source.ID,
			SourceKind:     accounts.SourceKind(ac.Source.Kind),
			Health:         accounts.SourceHealth(ac.Source.Health),
		})
	}
	return disc, nil
}

// Discover 实现 gui.HostDiscoverer：经适配器发现目标账号（脱敏、无凭据）。
func (a *App) Discover(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error) {
	return a.discoverViaHost(ctx, target)
}
