package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/applog"
	"qka/internal/autostart"
	"qka/internal/capabilities"
	"qka/internal/freetokens"
	"qka/internal/gui"
	"qka/internal/install"
	"qka/internal/logtail"
	"qka/internal/openurl"
	"qka/internal/providers"
	"qka/internal/runlock"
	"qka/internal/schedule"
	"qka/internal/scheduler"
	"qka/internal/selfupdate"
	"qka/internal/settings"
	"qka/internal/tokenspeed"
	"qka/internal/workbuddy"
)

// targetOrder 是 MVP 当前实际接通的目标应用。
// 其余 V1 目标在适配器、账号和验收闭环完成前不在界面中展示。
var targetOrder = []string{"workbuddy-cn"}

var (
	appExecutable = os.Executable
	userHomeDir   = os.UserHomeDir
	openBrowser   = openurl.Open
)

// guiCurrentApp 返回当前正在运行的 gui.App；GUI 未启动时返回 nil。
// 通过 gui 包里的包级指针拿，避免把 gui.App 实例再穿一层依赖。
var guiCurrentApp = gui.Current

// guiState 持有 GUI 运行期可变状态（设置、开发者模式、受限加载审计）。
type guiState struct {
	app *App

	mu       sync.Mutex
	settings *settings.Settings
	devMode  bool
	audit    []gui.AuditLine
}

// GUI 装配并返回菜单栏 GUI（托盘 + 吸附面板 + 五页签主窗口）。
// 调用方负责通过 Run() 进入事件循环。
func (a *App) GUI() *gui.App {
	// 首次启动安装默认自动执行计划（每 2 小时签到 + 每日活跃）；已有配置的
	// 用户设置优先，界面可改间隔/内容或关闭。异步执行，不拖慢窗口出现。
	go a.ensureDefaultSchedule()
	return gui.New(a.guiDeps())
}

// ensureDefaultSchedule 首次启动时安装默认自动执行计划；已有配置的用户
// 设置优先，启动只做「系统任务缺失时按现有计划修复」，不会把间隔/动作改回
// 默认值，也不会把用户关掉的自动执行重新打开。失败只记日志，绝不阻塞启动。
func (a *App) ensureDefaultSchedule() {
	// GUI() 用 `go a.ensureDefaultSchedule()` 异步收敛计划；零值与测试里
	// `a` 可能是 nil。测试二进制更不能碰真实系统调度器：此前 go test 用临时
	// 配置目录启动 GUI，会在开发机 launchd 里留下大量 com.qka.agent.* 任务。
	if a == nil || testing.Testing() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := appExecutable()
	if err != nil {
		applog.Warnf("app", "自动执行：定位可执行文件失败：%v", err)
		return
	}
	mgr := schedule.NewManager(a.cfg.ConfigDir, exe, scheduler.New(), a.now)
	if err := ensureSchedulePlan(ctx, mgr, a.cfg.ConfigDir); err != nil {
		applog.Warnf("app", "自动执行：初始化计划失败：%v", err)
	}
}

// ensureSchedulePlan 是 ensureDefaultSchedule 的可测核心：
//   - 没有 schedule.json（首次启动）：安装默认计划（启用、120 分钟、all）；
//   - 已有 schedule.json：保留用户选择，只在「启用但系统任务缺失」时重装；
//   - 最后清理指向已删除程序/配置目录的陈旧系统任务。
func ensureSchedulePlan(ctx context.Context, mgr *schedule.Manager, configDir string) error {
	if mgr == nil {
		return fmt.Errorf("自动执行：缺少计划管理器")
	}
	if schedule.Exists(configDir) {
		view, err := mgr.Get(ctx)
		if err != nil {
			return err
		}
		if view.Enabled && !view.Installed {
			if _, err := mgr.SetEnabled(ctx, true); err != nil {
				return err
			}
			applog.Infof("app", "自动执行：系统任务缺失，已按现有计划重新安装")
		}
	} else {
		if _, err := mgr.SetConfig(ctx, schedule.DefaultIntervalMinutes, schedule.ActionAll); err != nil {
			return err
		}
		if _, err := mgr.SetEnabled(ctx, true); err != nil {
			return err
		}
		applog.Infof("app", "首次启动：已启用默认自动执行（每 2 小时签到 + 每日活跃）")
	}
	pruneStaleSchedules(ctx, mgr)
	return nil
}

// pruneStaleSchedules 清理系统调度器里已经没有对应程序/配置目录的旧任务。
// 调度器未实现 Pruner（例如测试桩）时静默跳过。
func pruneStaleSchedules(ctx context.Context, mgr *schedule.Manager) {
	pruner, ok := mgr.Scheduler.(scheduler.Pruner)
	if !ok {
		return
	}
	removed, err := pruner.PruneOrphans(ctx, scheduler.Spec{
		Executable:      mgr.Executable,
		ConfigDir:       mgr.ConfigDir,
		IntervalMinutes: schedule.DefaultIntervalMinutes,
	})
	if err != nil {
		applog.Warnf("app", "自动执行：清理陈旧任务失败：%v", err)
		return
	}
	if removed > 0 {
		applog.Infof("app", "自动执行：已清理 %d 个陈旧的系统任务", removed)
	}
}

func (a *App) guiDeps() gui.Deps {
	st, err := settings.Load(a.cfg.ConfigDir)
	if err != nil {
		st = settings.Default()
	}

	state := &guiState{
		app:      a,
		settings: st,
		audit:    []gui.AuditLine{},
	}
	exe, _ := appExecutable()
	scheduleManager := schedule.NewManager(a.cfg.ConfigDir, exe, scheduler.New(), a.now)
	tokenSpeed := tokenspeed.New(tokenspeed.DefaultConfig())
	freeTokensService := freetokens.NewService()
	tokenSpeed.SetTitleTheme(st.GetString("title_theme"))
	// 采样频率来自设置（默认 500ms）；设置页改动后由 AfterSaveSettings 继续更新。
	tokenSpeed.SetPollInterval(st.SpeedPollInterval())
	updateService := a.updateService()
	// 下载进度推给前端：设置页在安装时显示百分比，不再只是一个「下载中」。
	updateService.Progress = func(downloaded, total int64) {
		if app := application.Get(); app != nil {
			app.Event.Emit("update-progress", map[string]any{
				"downloaded": downloaded, "total": total,
			})
		}
	}

	bridge := gui.NewAccountBridge(
		a.accounts,
		a, // app 实现 gui.HostDiscoverer
		a.workBuddyLoginService(),
	).WithRuntime(a.status.Status, a).WithTaskCapabilities(a.capabilitiesFor)

	deps := gui.Deps{
		CurrentVersion: a.cfg.Version,
		Targets:        targetOrder,
		Status:         a.status.Status,
		Plan:           a.status.PlanValidate,
		Run:            a.RunAll,
		Settings:       state.currentSettings,
		SaveSettings:   state.saveSettings,
		AfterSaveSettings: func(st *settings.Settings) error {
			tokenSpeed.SetTitleTheme(st.GetString("title_theme"))
			// 采样频率是运行时可调项：改完下一次轮询就按新间隔调度。
			tokenSpeed.SetPollInterval(st.SpeedPollInterval())
			// 完整目录扫描间隔同理：改完下一次 walkDue 判定就用新值。
			tokenSpeed.SetWalkInterval(st.SpeedWalkInterval())
			// 红线控制热谱与称号阶梯的缩放，改完立即重算快照。
			tokenSpeed.SetRedline(st.SpeedRedline())
			return state.afterSaveSettings(st)
		},
		ScheduleGet: scheduleManager.Get,
		ScheduleSetEnabled: func(ctx context.Context, enabled bool) (schedule.View, error) {
			return scheduleManager.SetEnabled(ctx, enabled)
		},
		ScheduleSetConfig: scheduleManager.SetConfig,
		UpdateCheck: func(ctx context.Context) (selfupdate.Status, error) {
			applyUpdateMirror(updateService, state.currentSettings())
			return updateService.Check(ctx)
		},
		UpdateInstall: func(ctx context.Context) (selfupdate.Status, error) {
			applyUpdateMirror(updateService, state.currentSettings())
			return updateService.DownloadAndApply(ctx)
		},
		QuitApp: func() {
			if app := application.Get(); app != nil {
				app.Quit()
			}
		},
		// PanelToggle 是旧入口：macOS 菜单栏点击切换 392px 吸附面板。
		// gui.App 在事件循环启动后才存在，这里用 application.Get() 现取。
		PanelToggle: func() {
			if app := guiCurrentApp(); app != nil {
				app.PanelToggle()
			}
		},
		PanelToggleAt: func(x, y int) {
			if app := guiCurrentApp(); app != nil {
				app.PanelToggleAt(x, y)
			}
		},
		// Windows 悬浮球（菜单栏状态项）：左键球内宠物交互、右键出菜单（含退出）。
		BarToggle: func() {
			if app := guiCurrentApp(); app != nil {
				app.BarToggle()
			}
		},
		BarStartDrag: func() {
			if app := guiCurrentApp(); app != nil {
				app.BarStartDrag()
			}
		},
		BarMenu: func() {
			if app := guiCurrentApp(); app != nil {
				app.BarMenu()
			}
		},
		TokenSpeedGet: func(context.Context) (tokenspeed.Snapshot, error) {
			return tokenSpeed.Snapshot(), nil
		},
		TokenSpeed: tokenSpeed,
		// AIHOT 免费额度：按需拉一次（10 分钟内存缓存），再与 QKA 现场
		// provider 状态合成三档；拉取失败时返回上次快照并带错误给页面。
		FreeTokens: func(ctx context.Context, force bool) (freetokens.View, error) {
			var snapshot freetokens.Snapshot
			var fetchErr error
			if force {
				snapshot, fetchErr = freeTokensService.Refresh(ctx)
			} else {
				snapshot, _, fetchErr = freeTokensService.Get(ctx)
			}
			states := make([]freetokens.ProviderState, 0, 16)
			if view, err := a.providers.CachedListView(ctx); err == nil {
				for _, provider := range view.Providers {
					states = append(states, freetokens.ProviderState{
						ID:       provider.ID,
						Name:     provider.Name,
						Icon:     provider.Icon,
						Enabled:  provider.Enabled,
						SignedIn: provider.SignedIn,
						Accounts: len(provider.Accounts),
						Checkin:  provider.Checkin,
					})
				}
			}
			return freetokens.Classify(snapshot, states, fetchErr), nil
		},
		OpenURL:  openBrowser,
		Accounts: bridge,
		// 页面打开走缓存投影（秒开），过期后在后台刷新；写操作会立即失效缓存。
		ProvidersList:   a.providers.CachedListView,
		ActivityView:    a.providers.ActivityView,
		ActivityRun:     a.RunProviderActivity,
		ActivityHistory: a.providers.ActivityHistory,
		// 猫猫旅行被上游风控（403/11140），暂时关闭：界面入口隐藏、自动执行
		// 跳过、绑定调用也直接拒绝，避免误触发账号级限制。
		WorkBuddyTravelStatus: func(ctx context.Context, account string) (workbuddy.TravelStatus, error) {
			if !workBuddyTravelEnabled {
				return workbuddy.TravelStatus{}, errors.New("猫猫旅行暂时关闭")
			}
			return a.providers.WorkBuddyTravelStatus(ctx, account)
		},
		WorkBuddyTravelRun: func(ctx context.Context, account string) (workbuddy.TravelResult, error) {
			if !workBuddyTravelEnabled {
				return workbuddy.TravelResult{}, errors.New("猫猫旅行暂时关闭")
			}
			return a.providers.RunWorkBuddyTravel(ctx, account)
		},
		ActivitySetEnabled: a.providers.SetActivityEnabled,
		ProvidersWarm: func(ctx context.Context) error {
			_, err := a.providers.List(ctx)
			return err
		},
		ProviderBeginLogin: func(ctx context.Context, provider string, method int, inputs map[string]string) (providers.LoginView, error) {
			view, err := a.providers.BeginLogin(ctx, provider, method, inputs)
			if err == nil && view.Status == "authorized" && view.URL != "" {
				_ = openBrowser(view.URL)
			}
			return view, err
		},
		ProviderFinishLogin:       a.providers.FinishLogin,
		ProviderLoginAPIKey:       a.providers.LoginAPIKey,
		ProviderSetEnabled:        a.providers.SetEnabled,
		ProviderSetAccountEnabled: a.providers.SetAccountEnabled,
		ProviderSetAccountLabel:   a.providers.SetAccountLabel,
		ProviderSetTaskEnabled:    a.providers.SetTaskEnabled,
		ProviderRun: func(ctx context.Context, provider, account, operation string) error {
			return a.RunProviderAccount(ctx, provider, account, operation)
		},
		ProviderTestModel:     a.providers.TestModel,
		ProviderDeleteAccount: a.providers.DeleteAccount,
		ProviderHistory:       a.providers.History,
		Logs:                  state.logs,
		AgentStatus:           state.agentStatus,
		Plugins:               state.plugins,
		SetDevMode:            state.setDevMode,
	}
	return deps
}

// RunProviderAccount sends one provider action through the config-directory
// lock so manual and scheduled execution cannot overlap.
func (a *App) RunProviderAccount(
	ctx context.Context, provider, account, operation string,
) error {
	unlock, err := a.acquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()
	return a.providers.Run(ctx, provider, account, operation)
}

// RunProviderActivity runs one account's daily activity heartbeat through the
// config-directory lock so manual and scheduled runs cannot overlap.
func (a *App) RunProviderActivity(
	ctx context.Context, provider, account string,
) error {
	unlock, err := a.acquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()
	return a.providers.RunActivity(ctx, provider, account)
}

func (a *App) workBuddyLoginService() *workbuddy.LoginService {
	baseURL := workBuddyLoginBaseURL(a.cfg.BaseURLs)
	pendingStore := a.pendingCredentials
	if pendingStore == nil {
		pendingStore = capabilities.NewLoginCredentialStore(
			capabilities.NewKeychainStore(),
			capabilities.NewFileSecretStore(a.cfg.ConfigDir),
		)
	}
	credentials := a.credentials
	if credentials == nil {
		credentials = capabilities.NewCredentialService(
			capabilities.NewKeychainStore(),
			capabilities.NewFileSecretStore(a.cfg.ConfigDir),
			capabilities.Grant{
				"credentials.managed": true,
				"credentials.resolve": true,
			},
			a.now,
		)
	}
	verifier := workbuddy.NewVerificationService(workbuddy.VerificationConfig{
		BaseURL:      baseURL,
		PendingStore: pendingStore,
		Sink:         newWorkBuddyVerificationSink(a.accounts, credentials),
	})
	return workbuddy.NewLoginService(workbuddy.LoginConfig{
		BaseURL:      baseURL,
		PendingStore: pendingStore,
		Verifier:     verifier,
		OpenBrowser:  openBrowser,
		Now:          a.now,
	})
}

// RunAccount 实现 gui.AccountRunner：复用宿主调用的同一单账号执行链。
func (a *App) RunAccount(ctx context.Context, target, accountID, operation string) error {
	unlock, err := a.acquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()

	if target == "workbuddy-cn" && operation == "refresh" {
		return a.refreshWorkBuddyUsage(ctx, accountID)
	}
	_, err = a.runOperation(ctx, target, accountID, operation)
	return err
}

func (a *App) acquireRunLock() (func(), error) {
	if a.cfg.ConfigDir == "" {
		return func() {}, nil
	}
	lock, err := runlock.Acquire(a.cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Close() }, nil
}

func workBuddyLoginBaseURL(overrides map[string]string) string {
	if baseURL := overrides["workbuddy-cn"]; baseURL != "" {
		return baseURL
	}
	return workbuddy.DefaultBaseURL
}

func (s *guiState) currentSettings() *settings.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *guiState) saveSettings(st *settings.Settings) error {
	if err := st.Save(s.app.cfg.ConfigDir); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	return nil
}

// afterSaveSettings 应用设置副作用：launch_at_login → LaunchAgent 幂等 on/off。
func (s *guiState) afterSaveSettings(st *settings.Settings) error {
	exe, err := appExecutable()
	if err != nil {
		return err
	}
	home, err := userHomeDir()
	if err != nil {
		return err
	}
	if st.Get("launch_at_login") {
		return autostart.On(home, exe, autostart.DefaultLabel)
	}
	return autostart.Off(home, autostart.DefaultLabel)
}

func (s *guiState) logs(ctx context.Context, filter gui.LogFilter, limit int, cursor int64) (gui.LogPage, error) {
	// 过期日志由 startLogPruner 的定时任务清理，翻页不再触发同步 DELETE。
	page, err := logtail.Tail(ctx, s.app.store.DB(), logtail.Filter{
		Target:    filter.Target,
		AccountID: filter.AccountID,
		Operation: filter.Operation,
		State:     filter.State,
	}, limit, cursor)
	if err != nil {
		return gui.LogPage{}, err
	}

	out := gui.LogPage{Entries: []gui.LogEntry{}, NextCursor: page.NextCursor}
	for _, e := range page.Entries {
		out.Entries = append(out.Entries, gui.LogEntry{
			ID: e.ID, TS: e.TS, Target: e.Target, AccountID: e.AccountID,
			Operation: e.Operation, State: e.State, Level: e.Level, Message: e.Message,
		})
	}
	// 日志里存的是供应商 key（如 codearts#u90nyn）；用已缓存的供应商投影把
	// 它换成真实账号标识与供应商展示名，缓存为空就保留原值（不为此启动运行时）。
	if s.app.providers != nil {
		if view, ok := s.app.providers.PeekView(); ok {
			out.Entries = enrichLogIdentities(out.Entries, view)
		}
	}
	return out, nil
}

// enrichLogIdentities 把日志行的供应商 id / 账号 key 换成展示名与真实标识；
// 供应商投影里没有的条目保持原值，界面上再退回 key。
func enrichLogIdentities(entries []gui.LogEntry, view providers.View) []gui.LogEntry {
	names := map[string]string{}
	type pair struct{ identity, masked string }
	identities := map[string]pair{}
	for _, provider := range view.Providers {
		if provider.ID != "" && provider.Name != "" {
			names[provider.ID] = provider.Name
		}
		for _, account := range provider.Accounts {
			identity := account.Identity
			if identity == "" {
				identity = account.MaskedIdentity
			}
			if identity == "" {
				continue
			}
			identities[provider.ID+"|"+account.Key] = pair{
				identity: identity, masked: account.MaskedIdentity,
			}
		}
	}
	for i := range entries {
		entry := &entries[i]
		entry.TargetName = names[entry.Target]
		if hit, ok := identities[entry.Target+"|"+entry.AccountID]; ok {
			entry.AccountIdentity, entry.AccountMasked = hit.identity, hit.masked
		}
	}
	return entries
}

func (s *guiState) agentStatus(context.Context) (gui.AgentView, error) {
	rows := make([]gui.HostRow, 0, 5)
	for _, p := range install.Profiles() {
		rows = append(rows, gui.HostRow{
			HostID:        p.HostID,
			Label:         p.Label,
			Paired:        false, // 新安装尚无宿主配对；配对由安装 skill 写入后反映
			Connected:     false,
			ScheduleGuide: p.ScheduleGuide,
		})
	}
	return gui.NewAgentView(rows), nil
}

func (s *guiState) plugins(context.Context) (gui.PluginsView, error) {
	loaded := s.app.LoadedAdapters()
	rows := make([]gui.PluginRow, 0, len(loaded))
	for _, l := range loaded {
		rows = append(rows, gui.PluginRow{
			ID:      l.Manifest.ID,
			Name:    l.Manifest.Name,
			Version: l.Manifest.Version,
			Source:  "local",
			Signed:  false, // 随包开发适配器未签名，固定标注未验证
		})
	}

	s.mu.Lock()
	devMode := s.devMode
	audit := append([]gui.AuditLine{}, s.audit...)
	s.mu.Unlock()

	return gui.NewPluginsView(rows, devMode, audit), nil
}

func (s *guiState) setDevMode(enabled bool) error {
	s.mu.Lock()
	s.devMode = enabled
	s.mu.Unlock()
	return nil
}
