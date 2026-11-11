package gui

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/features"
	"qka/internal/freetokens"
	"qka/internal/providers"
	"qka/internal/schedule"
	"qka/internal/selfupdate"
	"qka/internal/settings"
	"qka/internal/status"
	"qka/internal/tokenspeed"
	"qka/internal/workbuddy"
)

// Deps 是 GUI 所需的后端能力（由应用装配注入）。
type Deps struct {
	// CurrentVersion is the running application version shown by the updater.
	CurrentVersion string
	// Targets 是受支持目标应用的有序清单（应用管理页始终列出，即使 0 账号）。
	Targets []string
	// Status 返回当前状态视图。
	Status func(ctx context.Context) (status.View, error)
	// Plan 返回只读预演。
	Plan func(ctx context.Context) (status.Plan, error)
	// Run 执行一次（立即执行）。
	Run func(ctx context.Context) error
	// Settings 读取当前设置。
	Settings func() *settings.Settings
	// SaveSettings 保存设置。
	SaveSettings func(*settings.Settings) error
	// AfterSaveSettings 在保存后应用副作用（如 launch_at_login → autostart）。
	AfterSaveSettings func(*settings.Settings) error
	// DockIconVisible 在 macOS 上即时显示或隐藏 Dock 图标。
	DockIconVisible func(bool)
	// ScheduleGet 返回 QKA 内置周期自动执行与系统调度状态。
	ScheduleGet func(ctx context.Context) (schedule.View, error)
	// ScheduleSetEnabled 安装或移除系统调度，并与 schedule.json 同步。
	ScheduleSetEnabled func(ctx context.Context, enabled bool) (schedule.View, error)
	// ScheduleSetConfig 更新自动执行间隔与执行动作。
	ScheduleSetConfig func(ctx context.Context, intervalMinutes int, action string) (schedule.View, error)
	// UpdateCheck reads the official feed without downloading an artifact.
	UpdateCheck func(ctx context.Context) (selfupdate.Status, error)
	// UpdateInstall downloads, verifies and starts the platform update handoff.
	UpdateInstall func(ctx context.Context) (selfupdate.Status, error)
	// QuitApp exits QKA after the update helper can safely replace the bundle.
	QuitApp func()
	// PanelToggle 切换 392px 吸附面板的显示状态。macOS 菜单栏点击走这里；
	// Windows 悬浮球左键在球内与宠物交互，不再开面板。
	PanelToggle func()
	// PanelToggleAt 在指定屏幕坐标（悬浮球所在位置）下方切换 392px 面板。
	PanelToggleAt func(x, y int)
	// BarToggle 是 Windows 悬浮球左键的旧入口：页面已改为球内宠物交互，
	// 不再调用；保留给贴边弹回与未来入口使用。
	BarToggle func()
	// BarStartDrag 让悬浮球进入系统原生移动循环：页面在「按住且移动超过
	// 阈值」时调用，实现「移动前不吞点击、移动后才拖动」的手势。
	BarStartDrag func()
	// BarMenu 是 Windows 悬浮球右键点击：在球左侧弹出自绘菜单（含「退出」）。
	BarMenu func()
	// BallVisible 应用「显示悬浮球」设置：true 显示（尊重贴边状态），false 隐藏。
	// 仅 Windows 悬浮球使用；macOS 无此概念。
	BallVisible func(visible bool)
	// TokenSpeedGet 返回本机 Agent 日志的实时 token 吞吐快照。
	TokenSpeedGet func(ctx context.Context) (tokenspeed.Snapshot, error)
	// FreeTokens 返回 AIHOT 免费额度（Token 羊毛）的三档视图；为空时页面不可用。
	// force=true 时绕过内存缓存强制拉取。
	FreeTokens func(ctx context.Context, force bool) (freetokens.View, error)
	// OpenURL 用系统浏览器打开一个已校验的 http(s) 链接。
	OpenURL func(url string) error
	// TokenSpeed 是本机 Agent 日志速度服务；为空时不启动菜单栏实时标签。
	TokenSpeed *tokenspeed.Service
	// Accounts 是账号管理桥接。
	Accounts AccountBridge
	// ActivityView 返回「每日活跃」投影（逐账号今日状态、连续天数与汇总）。
	ActivityView func(ctx context.Context) (providers.ActivityView, error)
	// ActivityRun 立即为单个账号执行一次每日活跃。
	ActivityRun func(ctx context.Context, provider, account string) error
	// ActivityHistory 返回单个账号最近的每日活跃执行记录。
	ActivityHistory func(ctx context.Context, provider, account string, limit int) ([]providers.ActivityRecord, error)
	// WorkBuddyTravelStatus 查询 WorkBuddy 猫猫旅行状态。
	WorkBuddyTravelStatus func(ctx context.Context, account string) (workbuddy.TravelStatus, error)
	// WorkBuddyTravelRun 手动推进一步 WorkBuddy 猫猫旅行（派出或领奖）。
	WorkBuddyTravelRun func(ctx context.Context, account string) (workbuddy.TravelResult, error)
	// ActivitySetEnabled 开关单个账号的每日活跃任务。
	ActivitySetEnabled func(ctx context.Context, provider, account string, enabled bool) error
	// Providers 是随包供应商插件的统一管理投影。
	ProvidersList func(ctx context.Context) (providers.View, error)
	// ProvidersWarm 在启动时预热供应商运行时与模型目录，避免首次打开页面
	// 现场冷启动 Bun 进程导致首屏空白。
	ProvidersWarm func(ctx context.Context) error
	// ProviderBeginLogin starts or advances a provider login.
	ProviderBeginLogin func(ctx context.Context, provider string, method int, inputs map[string]string) (providers.LoginView, error)
	// ProviderFinishLogin completes an OAuth callback.
	ProviderFinishLogin func(ctx context.Context, session, code string) (providers.LoginView, error)
	// ProviderLoginAPIKey stores an API key through the provider plugin.
	ProviderLoginAPIKey       func(ctx context.Context, provider string, method int, inputs map[string]string, key string) (providers.LoginView, error)
	ProviderSetEnabled        func(ctx context.Context, provider string, enabled bool) error
	ProviderSetAccountEnabled func(ctx context.Context, provider, account string, enabled bool) error
	// ProviderSetAccountLabel 保存账号备注（界面上的账号名），空值表示清除。
	ProviderSetAccountLabel func(ctx context.Context, provider, account, label string) error
	ProviderSetTaskEnabled  func(ctx context.Context, provider, account, operation string, enabled bool) error
	ProviderRun             func(ctx context.Context, provider, account, operation string) error
	ProviderTestModel       func(ctx context.Context, provider, account, model string) (providers.ModelTestView, error)
	ProviderDeleteAccount   func(ctx context.Context, provider, account string) error
	ProviderHistory         func(ctx context.Context, provider, account string, limit int) ([]providers.RunItemView, []providers.QuotaReadingView, error)
	// Logs 按筛选条件查询日志（logtail）。
	Logs func(ctx context.Context, filter LogFilter, limit int, cursor int64) (LogPage, error)
	// AgentStatus 返回宿主接入信息（配对/连通/最近调用/计划指引）。
	AgentStatus func(ctx context.Context) (AgentView, error)
	// Plugins 返回插件页视图（市场/签名/更新/审计）。
	Plugins func(ctx context.Context) (PluginsView, error)
	// SetDevMode 切换开发者模式。
	SetDevMode func(enabled bool) error
}

// BoundService 是暴露给前端的服务。
type BoundService struct {
	deps Deps
}

// NewBoundService 组装绑定服务。
func NewBoundService(deps Deps) *BoundService {
	return &BoundService{deps: deps}
}

// Status 返回当前状态（序列化为 JSON）。
func (s *BoundService) Status() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// PlanValidate 返回只读预演。
func (s *BoundService) PlanValidate() (json.RawMessage, error) {
	plan, err := s.deps.Plan(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(plan)
}

// Run 立即执行：先取预演（同一服务链），再执行。
func (s *BoundService) Run() error {
	if _, err := s.deps.Plan(context.Background()); err != nil {
		return err
	}
	return s.deps.Run(context.Background())
}

// SettingsSchema 返回声明式设置 schema（用于生成表单）。按当前平台过滤：
// 「显示悬浮球」这类只在特定平台有意义的项不会出现在其它平台。
func (s *BoundService) SettingsSchema() (json.RawMessage, error) {
	return json.Marshal(settings.FieldsForPlatform(goos))
}

// SettingsGet 返回当前设置值。
func (s *BoundService) SettingsGet() (json.RawMessage, error) {
	values := s.deps.Settings().Values()
	// This metadata key is not in the settings schema, so the generic form
	// renderer ignores it while the updater can still show the running version.
	values["__qka_version"] = s.deps.CurrentVersion
	return json.Marshal(values)
}

// SettingsSet 更新一个设置项并保存，随后应用副作用（如 launch_at_login）。
func (s *BoundService) SettingsSet(key string, value bool) error {
	cur := s.deps.Settings()
	if err := cur.Set(key, value); err != nil {
		return err
	}
	if err := s.deps.SaveSettings(cur); err != nil {
		return err
	}
	var sideEffectErr error
	if s.deps.AfterSaveSettings != nil {
		sideEffectErr = s.deps.AfterSaveSettings(cur)
	}
	if key == "show_dock_icon" && s.deps.DockIconVisible != nil {
		s.deps.DockIconVisible(value)
	}
	if key == "show_ball" && s.deps.BallVisible != nil {
		s.deps.BallVisible(value)
	}
	return sideEffectErr
}

// SettingsSetString 更新一个有限选项设置并保存，随后广播外观变化。
func (s *BoundService) SettingsSetString(key, value string) error {
	cur := s.deps.Settings()
	if err := cur.SetString(key, value); err != nil {
		return err
	}
	if err := s.deps.SaveSettings(cur); err != nil {
		return err
	}
	if s.deps.AfterSaveSettings != nil {
		if err := s.deps.AfterSaveSettings(cur); err != nil {
			return err
		}
	}
	if key == "theme" {
		if app := application.Get(); app != nil {
			app.Event.Emit("theme-changed", value)
		}
	}
	if key == "background" || key == "glass" {
		if app := application.Get(); app != nil {
			app.Event.Emit("appearance-changed", map[string]string{"key": key, "value": value})
		}
	}
	if key == "performance_mode" {
		// 主窗口与设置窗口各自持有页面级检测结果，切换后广播让所有窗口即时生效。
		if app := application.Get(); app != nil {
			app.Event.Emit("performance-changed", value)
		}
	}
	if key == "title_theme" {
		if app := application.Get(); app != nil {
			app.Event.Emit("title-theme-changed", value)
		}
		if s.deps.TokenSpeedGet != nil {
			if snapshot, err := s.deps.TokenSpeedGet(context.Background()); err == nil {
				if app := application.Get(); app != nil {
					app.Event.Emit("tokenspeed-changed", snapshot)
				}
			}
		}
	}
	return nil
}

// ScheduleGet 返回周期自动执行的启用状态、间隔与最近结果。
func (s *BoundService) ScheduleGet() (json.RawMessage, error) {
	if s.deps.ScheduleGet == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleGet(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ScheduleSetEnabled 同步安装或删除系统调度，并返回最新状态。
func (s *BoundService) ScheduleSetEnabled(enabled bool) (json.RawMessage, error) {
	if s.deps.ScheduleSetEnabled == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleSetEnabled(context.Background(), enabled)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ScheduleSetConfig updates the interval and action and reinstalls the native
// task when auto-run is enabled.
func (s *BoundService) ScheduleSetConfig(
	intervalMinutes int, action string,
) (json.RawMessage, error) {
	if s.deps.ScheduleSetConfig == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleSetConfig(context.Background(), intervalMinutes, action)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// UpdateCheck checks the official update feed without downloading an artifact.
func (s *BoundService) UpdateCheck() (json.RawMessage, error) {
	if s.deps.UpdateCheck == nil {
		return nil, errNotWired
	}
	status, err := s.deps.UpdateCheck(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(status)
}

// UpdateInstall downloads, verifies and starts the platform update handoff.
func (s *BoundService) UpdateInstall() (json.RawMessage, error) {
	if s.deps.UpdateInstall == nil {
		return nil, errNotWired
	}
	status, err := s.deps.UpdateInstall(context.Background())
	if err != nil {
		return nil, err
	}
	if s.deps.QuitApp != nil {
		go func() {
			time.Sleep(600 * time.Millisecond)
			s.deps.QuitApp()
		}()
	}
	return json.Marshal(status)
}

// PanelToggle 切换吸附面板：macOS 菜单栏图标点击时调用，面板内容即
// panel.html。Windows 悬浮球不使用这条路径（见 BarToggle）。
func (s *BoundService) PanelToggle() {
	if s.deps.PanelToggle != nil {
		s.deps.PanelToggle()
	}
}

// BarToggle 是 Windows 悬浮球左键点击的入口。
func (s *BoundService) BarToggle() {
	if s.deps.BarToggle != nil {
		s.deps.BarToggle()
		return
	}
	s.PanelToggle()
}

// BarStartDrag 让悬浮球进入系统原生移动循环（见 Deps.BarStartDrag）。
func (s *BoundService) BarStartDrag() {
	if s.deps.BarStartDrag != nil {
		s.deps.BarStartDrag()
	}
}

// BarMenu 是 Windows 悬浮球右键点击的入口：弹出含「退出」的菜单。
func (s *BoundService) BarMenu() {
	if s.deps.BarMenu != nil {
		s.deps.BarMenu()
	}
}

// PanelToggleAt 在悬浮球所在的屏幕坐标下方切换 392px 面板。x/y 是点击位置
// 的 CSS 像素坐标（DIP）；没有该能力时退回普通 PanelToggle。
func (s *BoundService) PanelToggleAt(x int, y int) {
	if s.deps.PanelToggleAt != nil {
		s.deps.PanelToggleAt(x, y)
		return
	}
	s.PanelToggle()
}

// TokenSpeedGet 返回本机 Agent 日志的实时 token 吞吐快照。
func (s *BoundService) TokenSpeedGet() (json.RawMessage, error) {
	if s.deps.TokenSpeedGet == nil {
		return nil, errNotWired
	}
	snapshot, err := s.deps.TokenSpeedGet(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

// FreeTokensGet 返回 AIHOT 免费额度（Token 羊毛）与三档分类：
// 已配置可用 / 有插件待配置 / 暂无插件。
func (s *BoundService) FreeTokensGet(force bool) (json.RawMessage, error) {
	if s.deps.FreeTokens == nil {
		return nil, errNotWired
	}
	view, err := s.deps.FreeTokens(context.Background(), force)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// OpenURL 用系统浏览器打开外部链接（仅允许 http/https，非法链接返回错误）。
func (s *BoundService) OpenURL(raw string) error {
	if s.deps.OpenURL == nil {
		return errNotWired
	}
	return s.deps.OpenURL(raw)
}

// ActivityView 返回「每日活跃」视图：逐账号今日状态、连续天数与汇总。
func (s *BoundService) ActivityView() (json.RawMessage, error) {
	if s.deps.ActivityView == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ActivityView(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ActivityHistory 返回单个账号最近的每日活跃执行记录（已脱敏）。
func (s *BoundService) ActivityHistory(provider, account string, limit int) (json.RawMessage, error) {
	if s.deps.ActivityHistory == nil {
		return nil, errNotWired
	}
	records, err := s.deps.ActivityHistory(context.Background(), provider, account, limit)
	if err != nil {
		return nil, err
	}
	if records == nil {
		records = []providers.ActivityRecord{}
	}
	return json.Marshal(records)
}

// WorkBuddyTravelStatus 查询 WorkBuddy 猫猫旅行状态（只读）。
func (s *BoundService) WorkBuddyTravelStatus(account string) (json.RawMessage, error) {
	if s.deps.WorkBuddyTravelStatus == nil {
		return nil, errNotWired
	}
	status, err := s.deps.WorkBuddyTravelStatus(context.Background(), account)
	if err != nil {
		return nil, err
	}
	return json.Marshal(status)
}

// WorkBuddyTravelRun 手动推进一步猫猫旅行：在家派出、归来领奖。
func (s *BoundService) WorkBuddyTravelRun(account string) (json.RawMessage, error) {
	if s.deps.WorkBuddyTravelRun == nil {
		return nil, errNotWired
	}
	result, err := s.deps.WorkBuddyTravelRun(context.Background(), account)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// ActivityRun 立即为单个账号执行一次每日活跃；仍受当天幂等与冷却约束。
func (s *BoundService) ActivityRun(provider, account string) error {
	if s.deps.ActivityRun == nil {
		return errNotWired
	}
	return s.deps.ActivityRun(context.Background(), provider, account)
}

// ActivitySetEnabled 开关单个账号的每日活跃任务。
func (s *BoundService) ActivitySetEnabled(provider, account string, enabled bool) error {
	if s.deps.ActivitySetEnabled == nil {
		return errNotWired
	}
	return s.deps.ActivitySetEnabled(context.Background(), provider, account, enabled)
}

// AppsList 返回应用管理页聚合行。
func (s *BoundService) AppsList() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(AggregateApps(view, s.deps.Targets))
}

// ProvidersList returns the bundled provider plugins and their accounts.
func (s *BoundService) ProvidersList() (json.RawMessage, error) {
	if s.deps.ProvidersList == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProvidersList(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersBeginLogin starts or advances a provider sign-in.
func (s *BoundService) ProvidersBeginLogin(
	provider string, method int, inputs map[string]string,
) (json.RawMessage, error) {
	if s.deps.ProviderBeginLogin == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderBeginLogin(context.Background(), provider, method, inputs)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersFinishLogin completes OAuth or desktop sign-in.
func (s *BoundService) ProvidersFinishLogin(session, code string) (json.RawMessage, error) {
	if s.deps.ProviderFinishLogin == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderFinishLogin(context.Background(), session, code)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersLoginAPIKey stores an API-key sign-in.
func (s *BoundService) ProvidersLoginAPIKey(
	provider string, method int, inputs map[string]string, key string,
) (json.RawMessage, error) {
	if s.deps.ProviderLoginAPIKey == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderLoginAPIKey(context.Background(), provider, method, inputs, key)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersSetEnabled toggles one provider.
func (s *BoundService) ProvidersSetEnabled(provider string, enabled bool) error {
	if s.deps.ProviderSetEnabled == nil {
		return errNotWired
	}
	return s.deps.ProviderSetEnabled(context.Background(), provider, enabled)
}

// ProvidersSetAccountEnabled toggles one provider account.
func (s *BoundService) ProvidersSetAccountEnabled(provider, account string, enabled bool) error {
	if s.deps.ProviderSetAccountEnabled == nil {
		return errNotWired
	}
	return s.deps.ProviderSetAccountEnabled(context.Background(), provider, account, enabled)
}

// ProvidersSetAccountLabel saves one account's display label (备注).
func (s *BoundService) ProvidersSetAccountLabel(provider, account, label string) error {
	if s.deps.ProviderSetAccountLabel == nil {
		return errNotWired
	}
	return s.deps.ProviderSetAccountLabel(context.Background(), provider, account, label)
}

// ProvidersSetTaskEnabled toggles check-in automation.
func (s *BoundService) ProvidersSetTaskEnabled(
	provider, account, operation string, enabled bool,
) error {
	if s.deps.ProviderSetTaskEnabled == nil {
		return errNotWired
	}
	switch operation {
	case features.OperationCheckin, features.OperationActivity:
	default:
		return errUnsupportedAccountAction
	}
	return s.deps.ProviderSetTaskEnabled(context.Background(), provider, account, operation, enabled)
}

// ProvidersRun executes one provider account action.
func (s *BoundService) ProvidersRun(provider, account, operation string) error {
	if s.deps.ProviderRun == nil {
		return errNotWired
	}
	switch operation {
	case "checkin", "refresh":
	default:
		return errUnsupportedAccountAction
	}
	return s.deps.ProviderRun(context.Background(), provider, account, operation)
}

// ProvidersTestModel checks one provider model through its signed-in account.
func (s *BoundService) ProvidersTestModel(
	provider, account, model string,
) (json.RawMessage, error) {
	if s.deps.ProviderTestModel == nil {
		return nil, errNotWired
	}
	result, err := s.deps.ProviderTestModel(
		context.Background(), provider, account, model)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// ProvidersDeleteAccount removes QKA's provider account and credentials.
func (s *BoundService) ProvidersDeleteAccount(provider, account string) error {
	if s.deps.ProviderDeleteAccount == nil {
		return errNotWired
	}
	return s.deps.ProviderDeleteAccount(context.Background(), provider, account)
}

// ProvidersHistory returns recent runs and quota observations.
func (s *BoundService) ProvidersHistory(
	provider, account string, limit int,
) (json.RawMessage, error) {
	if s.deps.ProviderHistory == nil {
		return nil, errNotWired
	}
	items, readings, err := s.deps.ProviderHistory(
		context.Background(), provider, account, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"items": items, "quota": readings,
	})
}

// AccountsList 返回所有账号（含来源）。
func (s *BoundService) AccountsList() (json.RawMessage, error) {
	list, err := s.deps.Accounts.List(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

// AccountsRun 对单个账号执行签到、只读复查或额度刷新。
func (s *BoundService) AccountsRun(target, accountID, operation string) error {
	switch operation {
	case "checkin", "refresh":
	default:
		return errUnsupportedAccountAction
	}
	return s.deps.Accounts.Run(context.Background(), target, accountID, operation)
}

// AccountsSetTaskEnabled 设置自动执行是否包含该账号的某一任务。
func (s *BoundService) AccountsSetTaskEnabled(
	target, accountID, operation string,
	enabled bool,
) error {
	switch operation {
	case features.OperationCheckin, features.OperationActivity:
	default:
		return errUnsupportedAccountAction
	}
	return s.deps.Accounts.SetTaskEnabled(
		context.Background(), target, accountID, operation, enabled)
}

// AccountsSetEnabled 逐账号启停。
func (s *BoundService) AccountsSetEnabled(target, accountID string, enabled bool) error {
	return s.deps.Accounts.SetEnabled(context.Background(), target, accountID, enabled)
}

// AccountsDelete 删除 QKA 映射。
func (s *BoundService) AccountsDelete(target, accountID string) error {
	return s.deps.Accounts.Delete(context.Background(), target, accountID)
}

// AccountsPinSource 设置来源优先级。
func (s *BoundService) AccountsPinSource(target, accountID, sourceID string, pinned bool) error {
	return s.deps.Accounts.PinSource(context.Background(), target, accountID, sourceID, pinned)
}

// AccountsDiscover 经适配器发现目标应用账号。
func (s *BoundService) AccountsDiscover(target string) error {
	return s.deps.Accounts.Discover(context.Background(), target)
}

// AccountsBeginLogin 发起官方登录并返回可见状态。
func (s *BoundService) AccountsBeginLogin(target string) (json.RawMessage, error) {
	view, err := s.deps.Accounts.BeginLogin(context.Background(), target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// AccountsLoginStatus 返回当前或最近一次官方登录状态。
func (s *BoundService) AccountsLoginStatus(target string) (json.RawMessage, error) {
	return json.Marshal(s.deps.Accounts.LoginStatus(context.Background(), target))
}

// AccountsCancelLogin 取消进行中的官方登录。
func (s *BoundService) AccountsCancelLogin(target string) (json.RawMessage, error) {
	view, err := s.deps.Accounts.CancelLogin(context.Background(), target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// AccountsImportSource 导入只读来源。
func (s *BoundService) AccountsImportSource(target, accountID, sourceID string) error {
	return s.deps.Accounts.ImportSource(context.Background(), target, accountID, sourceID)
}

// LogsTail 按四类筛选查询日志（有界、游标）。
func (s *BoundService) LogsTail(target, accountID, operation, state string,
	limit int, cursor int64) (json.RawMessage, error) {
	if s.deps.Logs == nil {
		return nil, errNotWired
	}
	page, err := s.deps.Logs(context.Background(),
		LogFilter{Target: target, AccountID: accountID, Operation: operation, State: state},
		limit, cursor)
	if err != nil {
		return nil, err
	}
	return json.Marshal(page)
}

// AgentStatus 返回宿主接入信息。
func (s *BoundService) AgentStatus() (json.RawMessage, error) {
	if s.deps.AgentStatus == nil {
		return nil, errNotWired
	}
	view, err := s.deps.AgentStatus(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// PluginsList 返回插件页视图（市场条目、版本、签名、更新状态与审计）。
func (s *BoundService) PluginsList() (json.RawMessage, error) {
	if s.deps.Plugins == nil {
		return nil, errNotWired
	}
	view, err := s.deps.Plugins(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// SetDevMode 切换开发者模式。
func (s *BoundService) SetDevMode(enabled bool) error {
	if s.deps.SetDevMode == nil {
		return errNotWired
	}
	return s.deps.SetDevMode(enabled)
}
