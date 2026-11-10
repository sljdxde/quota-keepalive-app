package app

import "testing"

// 猫猫旅行被上游风控，默认必须关闭：入口隐藏、自动执行跳过、手动绑定拒绝。
// 恢复时需要同时改这里与 main.html 的 WORKBUDDY_TRAVEL_UI。
func TestWorkBuddyTravelDisabledByDefault(t *testing.T) {
	if workBuddyTravelEnabled {
		t.Fatal("猫猫旅行不应在风控未确认时自动开启")
	}
}
