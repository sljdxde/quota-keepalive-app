package gui

import (
	"strings"
	"testing"
)

func accountPage(t *testing.T) string {
	t.Helper()
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	// Windows 上的 checkout 可能把 LF 转成 CRLF（autocrlf），而不少断言
	// 直接匹配源码里的多行片段；统一成 LF 再比对，避免平台差异假失败。
	// 仓库根另有 .gitattributes 把前端文本固定为 LF，这里是第二道保险。
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func TestCheckinManagementUsesGenericProviderLifecycle(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		"Bound.ProvidersList()",
		"Bound.ProvidersSetAccountEnabled",
		"Bound.ProvidersSetTaskEnabled",
		"Bound.ProvidersRun",
		"Bound.ProvidersTestModel",
		"Bound.ProvidersHistory",
		"Bound.ProvidersDeleteAccount",
		"account.masked_identity",
		"account.enabled",
		`taskEnabledFor(account, "checkin")`,
		"账号已从 Quota Keepalive 删除，供应商数据未被修改",
		"账号备注保存失败，请重试。",
		"任务偏好保存失败，请重试。",
		"删除失败，请稍后重试。",
		"再点一次「确认删除？」才会删除该账号",
		"flashAccountResult(",
		"openAccountRemarkModal(",
		"openAccountLog(",
		"acctOdometer(",
		"throw err",
		"let accountsRenderGeneration = 0;",
		"providerModelPanel(provider)",
		"function fillAgentModal(provider)",
		"agentModalSection(",
		`free.textContent = "免费"`,
		"provider-model-chip",
		`test.textContent = "测试模型"`,
		"model-test-result",
		`const head = document.createElement("button")`,
		`head.type = "button"`,
		`head.setAttribute("aria-expanded"`,
		`head.setAttribute("aria-label"`,
		`cursor: pointer; user-select: none`,
		`headSummary.textContent = badCount`,
		`dot.className = "model-test-dot"`,
		`chip.insertBefore(dot, label)`,
		`function applyModelStatus(modelId, status, detail)`,
		`entry.dot.dataset.testState = state`,
		`applyModelStatus(model.id, tested.ok ? "ok" : "fail", detail)`,
		`applyModelStatus(model.id, "fail", detail)`,
		`function renderTestSummary(rows)`,
		`悬停模型名查看原因`,
		`title.className = "acct-title"`,
		`meta.className = "acct-meta"`,
		`status.className = "acct-status"`,
		`links.className = "acct-links"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("签到管理页缺少通用供应商行为 %q", want)
		}
	}
}

// TestAccountCardLeadsWithCheckinAndQuota 锁定账号卡 v2 的行式结构：
// 名称行（acct-title，含自动开关）→ 额度行（acct-meta，含小圆圈刷新）→
// 状态行（acct-status：今日签到/今日活跃/执行日志）→ 文字链操作（acct-links）；
// 旧的「点账号头部展开」实现已移除。
func TestAccountCardLeadsWithCheckinAndQuota(t *testing.T) {
	page := accountPage(t)
	order := []string{
		`title.className = "acct-title"`,
		`meta.className = "acct-meta"`,
		`status.className = "acct-status"`,
		`links.className = "acct-links"`,
	}
	last := -1
	for _, want := range order {
		at := strings.Index(page, want)
		if at < 0 {
			t.Fatalf("账号卡缺少 %q", want)
		}
		if at <= last {
			t.Errorf("账号卡顺序错误：%q 应出现在上一段之后", want)
		}
		last = at
	}
	// mac 的轻量语言：额度内联在额度行、操作是文字链；名称行右侧一枚自动
	// 开关，额度行尾一枚小圆圈刷新，不再有 chip 与 mini-action。
	for _, want := range []string{
		`.account-card .acct-meta .account-quota {`,
		`.acct-link {`,
		`.acct-status {`,
		`.acct-auto {`,
		`.acct-quota-refresh {`,
		// 控件尺度对齐主窗口字号阶梯：名称与额度数字 12.5px（--fs-md），
		// 状态行与文字链 11.5px（--fs-sm），不再有半像素手工值。
		`font-size: var(--fs-md); font-weight: 600;`,
		`font-size: var(--fs-sm); font-weight: 600;`,
		`line-height: 1.45; cursor: pointer;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号卡缺少 mac 风格样式 %q", want)
		}
	}
	for _, gone := range []string{
		`account-collapse-head`,
		`account-collapse`,
		`box.classList.toggle("collapsed"`,
		`shouldCollapseAccount`,
		`nextAccountExpansion`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("账号卡不应再依赖折叠 %q（签到结果与额度需常驻可见）", gone)
		}
	}
}

// TestModelTestUsesChipDotsNotResultList 锁住展示方式：模型测试结果只体现在
// chip 的状态点上，不允许再退回「每个模型一行结果」的长列表。
func TestModelTestUsesChipDotsNotResultList(t *testing.T) {
	page := accountPage(t)
	for _, gone := range []string{
		`model-test-rows`,
		`model-test-row `,
		`renderBatchResults`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("模型测试不应再使用结果列表 %q", gone)
		}
	}
	if !strings.Contains(page, `.model-test-dot[data-test-state="ok"]`) ||
		!strings.Contains(page, `.model-test-dot[data-test-state="fail"]`) {
		t.Error("缺少模型测试状态点的绿/红样式")
	}
}

func TestCheckinManagementDoesNotExposeUnsafeCredentialInputs(t *testing.T) {
	page := accountPage(t)
	for _, forbidden := range []string{
		`id="target-select"`,
		`value="qoder-cn"`,
		`value="doubao-work"`,
		"导入来源",
		"桌面登录",
		`type="password"`,
		`placeholder="密码"`,
		`placeholder="验证码"`,
		`placeholder="扫码`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("签到管理页不应出现凭据输入或固定目标入口 %q", forbidden)
		}
	}
}

func TestCheckinManagementHasPerTaskActionsAndQuotaHistory(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`checkinBtn.textContent = "手动签到"`,
		"手动签到",
		"手动活跃",
		"改备注",
		"确认删除？",
		"今日无需签到",
		`label: "今日签到"`,
		"今日签到 未完成",
		"已启用 自动",
		"empty-add-account",
		`class="btn accent"`,
		"account-quota",
		"ProvidersHistory(",
		"quota-bar",
		"供应商今天没有可领取的签到活动",
		// 额度口径统一走 account-quota.js（与面板同一套纯函数）：主窗口按
		// 窗口取视图，没有窗口时才带 reading 兜底。
		`from "./assets/account-quota.js"`,
		"quotaViewForWindow(w, null)",
		"quotaViewForWindow(null, reading)",
		"function claimGrowth(op)",
		`"✓ 签到成功，额度 +" + formatQuota(gain)`,
		"Math.abs(value - Number(op.balance)) > 0.000001",
		// 执行日志：原「历史 / 额度」面板升级为居中弹层，只记签到与活跃；
		// 额度改为 30 分钟自动刷新 + 卡片小圆圈手动刷新，观测不进日志。
		"执行日志",
		"function openAccountLog(",
		"acctlog-day",
		"仅记录签到与每日活跃",
		"scheduleQuotaAutoRefresh",
		"30 * 60 * 1000",
		"自动刷新 · 上次 ",
		"overflow-x: hidden",
		"background: var(--bg)",
		`document.documentElement.dataset.theme = theme`,
		`Events.On("theme-changed"`,
		"自动执行：已启用",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("签到管理页缺少状态或操作 %q", want)
		}
	}
}

func TestProviderLoginSupportsPromptOAuthAndAPIKey(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`id="provider-login-dialog"`,
		"Bound.ProvidersBeginLogin",
		"Bound.ProvidersFinishLogin",
		"Bound.ProvidersLoginAPIKey",
		`view.status === "prompt"`,
		`view.status === "api_key"`,
		`view.status === "authorized"`,
		`view.status === "verified"`,
		"登录完成，账号已添加并默认启用",
		"Quota Keepalive 不接收密码、验证码或扫码内容",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("供应商登录弹窗缺少 %q", want)
		}
	}
}

func TestProviderLoginHasInteractivePikachu(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`id="provider-login-pokemon"`,
		`id="provider-login-pokemon-art"`,
		`id="provider-login-pokemon-bubble"`,
		`id="provider-login-pokemon-particles"`,
		`pokemonById,`,
		// 3D 资源按需加载：皮卡丘艺术图只在打开登录对话框时才创建，
		// 因此这里断言的是 loader 而不是 pokemon-3d.js 的静态导入。
		`import { loadPokemon3D } from "./assets/pokemon-3d-loader.js"`,
		`function ensureProviderLoginPokemonViewer()`,
		`const providerLoginPikachu = pokemonById("pikachu")`,
		`class="pokemon-3d-canvas"`,
		`models: [providerLoginPikachu.stages[0].model]`,
		`modelRotations: [providerLoginPikachu.stages[0].rotation || null]`,
		`createPokemon3D(`,
		`providerLoginPokemonViewer.setPointer(nx, ny)`,
		`providerLoginPokemonViewer.playSkill("electric", 3)`,
		`providerLoginPokemonArt.classList.add("is-3d")`,
		`function aimProviderLoginPokemon(`,
		`setProperty("--login-look-x"`,
		`.provider-login-pokemon-art .pokemon-stage-image`,
		`[data-stage="1"]`,
		`function spawnProviderLoginPokemonSparks(`,
		`function triggerProviderLoginPokemonPlay(`,
		`classList.add("zapping")`,
		`provider-login-spark`,
		`document.visibilityState !== "hidden"`,
		`@media (prefers-reduced-motion: reduce)`,
		`function finishAuthorizedProviderLogin(`,
		`Bound.ProvidersFinishLogin(session, "")`,
		`完成后会自动验证并关闭登录窗口`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("供应商登录弹窗缺少皮卡丘交互 %q", want)
		}
	}
	for _, forbidden := range []string{
		`id="provider-login-cat"`,
		`import("./assets/cat-3d.js")`,
		`createCat3D`,
		`登录成功，喵`,
		`provider-login-wand`,
		`wand-sway`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("供应商登录弹窗不应保留旧橘猫实现 %q", forbidden)
		}
	}
}

func TestFrontendProviderCallsExistInGeneratedBindings(t *testing.T) {
	page := accountPage(t)
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	bindings := string(data)
	for _, method := range []string{
		"ProvidersList",
		"ProvidersBeginLogin",
		"ProvidersFinishLogin",
		"ProvidersLoginAPIKey",
		"ProvidersSetAccountEnabled",
		"ProvidersSetTaskEnabled",
		"ProvidersRun",
		"ProvidersTestModel",
		"ProvidersDeleteAccount",
		"ProvidersHistory",
		"ScheduleGet",
		"ScheduleSetEnabled",
		"ScheduleSetConfig",
		"UpdateCheck",
		"UpdateInstall",
	} {
		if strings.Contains(page, "Bound."+method+"(") &&
			!strings.Contains(bindings, "export function "+method+"(") {
			t.Errorf("前端调用 Bound.%s 但生成绑定中不存在该方法", method)
		}
	}
}
