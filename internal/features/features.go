// Package features holds product-level automation vocabulary shared by the
// backend, scheduler, and UI projections.
package features

// 自动执行动作：all 同时跑签到与每日活跃；checkin / activity 只跑其中一项。
// activity 对外是「每日活跃」，内部 operation id 沿用 "keepalive"，以免迁移
// 旧的 daily_ops 与 run_items 历史。
const (
	ActionAll      = "all"
	ActionCheckin  = "checkin"
	ActionActivity = "activity"

	// OperationActivity 是持久化层使用的 operation id（历史原因仍是 keepalive）。
	OperationActivity = "keepalive"
	// OperationCheckin 是签到 operation id。
	OperationCheckin = "checkin"
)

// NormalizeAction 把旧数据与入口参数收敛到 all/checkin/activity：
// 空值与 "all" 都是全量；旧版 "keepalive" 迁移为 "activity"；未知值保留，
// 交给上层校验拒绝。
func NormalizeAction(action string) string {
	switch action {
	case "", ActionAll:
		return ActionAll
	case "keepalive", ActionActivity:
		return ActionActivity
	default:
		return action
	}
}

// ValidAction 报告动作是否属于 all/checkin/activity。
func ValidAction(action string) bool {
	switch NormalizeAction(action) {
	case ActionAll, ActionCheckin, ActionActivity:
		return true
	default:
		return false
	}
}

// OperationAction maps a persisted operation id back to its automation action.
func OperationAction(operation string) string {
	switch operation {
	case OperationCheckin:
		return ActionCheckin
	case OperationActivity:
		return ActionActivity
	default:
		return operation
	}
}
