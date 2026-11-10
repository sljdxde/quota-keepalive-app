package app

import (
	"context"
	"os"
	"sync"
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
	// 自动执行不再提供用户开关：启动时幂等地把系统计划任务收敛为
	// 「每 2 小时签到 + 每日活跃一次」。异步执行，不拖慢窗口出现。
	go a.ensureDefaultSchedule()
	return gui.New(a.guiDeps())
}

// ensureDefaultSchedule 把内置自动执行收敛到产品默认：始终开启、每 2 小时
// 跑一次签到 + 每日活跃。失败只记日志（下次启动再收敛），绝不阻塞启动。
func (a *App) ensureDefaultSchedule() {
	// GUI() 用 `go a.ensureDefaultSchedule()` 异步收敛计划；零值/测试里
	// `a` 可能是 nil，这里显式兜住，避免 goroutine 把整个测试进程带崩。
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := appExecutable()
	if err != nil {
		applog.Warnf("app", "自动执行默认计划：定位可执行文件失败：%v", err)
		return
	}
	mgr := schedule.NewManager(a.cfg.ConfigDir, exe, scheduler.New(), a.now)
	view, err := mgr.Get(ctx)
	if err != nil {
		applog.Warnf("app", "自动执行默认计划：读取状态失败：%v", err)
		return
	}
	if view.Enabled && view.Installed &&
		view.IntervalMinutes == 120 && view.Action == schedule.ActionAll {
		return
	}
	if _, err := mgr.SetConfig(ctx, 120, schedule.ActionAll); err != nil {
		applog.Warnf("app", "自动执行默认计划：写入间隔失败：%v", err)
		return
	}
	if !view.Enabled {
		if _, err := mgr.SetEnabled(ctx, true); err != nil {
			applog.Warnf("app", "自动执行默认计划：安装系统任务失败：%v", err)
			return
		}
	}
	applog.Infof("app", "自动执行已按默认计划运行：每 2 小时签到并执行每日活跃")
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
	updateService := a.updateService()

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
			return state.afterSaveSettings(st)
		},
		ScheduleGet: scheduleManager.Get,
		ScheduleSetEnabled: func(ctx context.Context, enabled bool) (schedule.View, error) {
			return scheduleManager.SetEnabled(ctx, enabled)
		},
		ScheduleSetConfig: scheduleManager.SetConfig,
		UpdateCheck:       updateService.Check,
		UpdateInstall:     updateService.DownloadAndApply,
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
		ProvidersList:      a.providers.CachedListView,
		ActivityView:       a.providers.ActivityView,
		ActivityRun:        a.RunProviderActivity,
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
	return out, nil
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
	rows := make([]gui.PluginRow, 0, len(s.app.loaded))
	for _, l := range s.app.loaded {
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
