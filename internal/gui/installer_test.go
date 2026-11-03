package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/installer"
)

// resetAppStarting 把 Wails 应用的 starting 标志恢复为 false；forceAppStarting
// 用它覆盖 Run 的「已在运行」错误分支，测完必须还原，避免影响同包其它测试。
func resetAppStarting(t *testing.T, app *application.App) {
	t.Helper()
	field := reflect.ValueOf(app).Elem().FieldByName("starting")
	if !field.IsValid() {
		t.Fatal("wails application has no starting field")
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetBool(false)
}

func TestInstallerWindowOptions(t *testing.T) {
	options := installerWindowOptions()
	if options.Title != "Quota Keepalive 安装程序" {
		t.Errorf("安装向导标题 = %q", options.Title)
	}
	if options.Width != 560 || options.Height != 430 {
		t.Errorf("安装向导尺寸 = %dx%d，want 560x430", options.Width, options.Height)
	}
	if !options.Hidden {
		t.Error("安装向导应初始隐藏，等 ApplicationStarted 后再显示")
	}
	if options.URL != "/installer.html" {
		t.Errorf("安装向导 URL = %q", options.URL)
	}
}

func TestNewInstallerWiresWindowAndService(t *testing.T) {
	deps := InstallerDeps{ConfigDir: t.TempDir(), Version: "0.1.16", SelfPath: "/tmp/QKA.exe"}
	inst := NewInstaller(deps)
	if inst.wails == nil || inst.window == nil || inst.service == nil {
		t.Fatal("NewInstaller 未装配完整")
	}
	if inst.service.configDir != deps.ConfigDir || inst.service.version != deps.Version || inst.service.selfPath != deps.SelfPath {
		t.Fatalf("服务未收到依赖: %+v", inst.service)
	}
	if inst.service.install == nil || inst.service.upgrade == nil || inst.service.defaultDir == nil ||
		inst.service.launch == nil || inst.service.browse == nil ||
		inst.service.quit == nil || inst.service.emit == nil {
		t.Fatal("服务的默认实现未接全")
	}
	if got := inst.service.Status(); got.Mode != "install" || got.Version != "0.1.16" {
		t.Fatalf("没有安装记录时 Status = %+v，want install 模式", got)
	}

	inst.onApplicationStarted(nil)

	if dir := inst.service.DefaultDir(); dir == "" {
		t.Fatal("默认安装目录不应为空（测试机 HOME 一定存在）")
	}
	if _, err := inst.service.BrowseDir(""); err == nil || !strings.Contains(err.Error(), "不支持目录选择") {
		t.Fatalf("非 Windows 平台的目录选择应给出清晰错误，err=%v", err)
	}
	// 默认 quit/事件实现也不应 panic。
	inst.service.QuitInstaller()
	inst.service.report(installer.StepCheck, "检查中")
}

func TestInstallerRunDelegatesToWails(t *testing.T) {
	inst := NewInstaller(InstallerDeps{ConfigDir: t.TempDir(), Version: "0.1.16"})
	forceAppStarting(t, inst.wails)
	t.Cleanup(func() { resetAppStarting(t, inst.wails) })
	if err := inst.Run(); err == nil {
		t.Fatal("应用已在启动中时 Run 应回传底层错误")
	}
}

func TestInstallerServiceDefaultDir(t *testing.T) {
	if got := (&InstallerService{}).DefaultDir(); got != "" {
		t.Fatalf("没有默认目录实现时应返回空串，实际 %q", got)
	}
	svc := &InstallerService{defaultDir: func() string { return "/opt/QKA" }}
	if got := svc.DefaultDir(); got != "/opt/QKA" {
		t.Fatalf("DefaultDir = %q", got)
	}
}

func TestInstallerServiceBrowseDir(t *testing.T) {
	if _, err := (&InstallerService{}).BrowseDir("/x"); err == nil {
		t.Fatal("没有目录选择实现时应报错")
	}
	svc := &InstallerService{browse: func(current string) (string, error) {
		return current + "/picked", nil
	}}
	got, err := svc.BrowseDir("/opt")
	if err != nil || got != "/opt/picked" {
		t.Fatalf("BrowseDir = %q/%v", got, err)
	}
}

func TestInstallerServiceStartInstallSuccess(t *testing.T) {
	var mu sync.Mutex
	var emitted []InstallerProgress
	svc := &InstallerService{
		configDir: "/config",
		version:   "0.1.16",
		selfPath:  "/downloads/QKA.exe",
		emit: func(name string, data any) {
			mu.Lock()
			defer mu.Unlock()
			if name != installerProgressEvent {
				t.Errorf("事件名 = %q", name)
			}
			progress, ok := data.(InstallerProgress)
			if !ok {
				t.Errorf("事件数据 = %T", data)
				return
			}
			emitted = append(emitted, progress)
		},
	}
	var gotOptions installer.Options
	svc.install = func(_ context.Context, opts installer.Options) (installer.Result, error) {
		gotOptions = opts
		opts.Progress(installer.StepCheck, "检查中")
		opts.Progress(installer.StepCopy, "复制中")
		opts.Progress(installer.StepFinish, "完成")
		return installer.Result{InstallDir: "/opt/QKA", InstalledExe: "/opt/QKA/QKA.exe"}, nil
	}
	if err := svc.StartInstall("/opt/QKA"); err != nil {
		t.Fatalf("StartInstall 应立即返回 nil: %v", err)
	}
	waitInstallerIdle(t, svc)

	if gotOptions.ConfigDir != "/config" || gotOptions.Version != "0.1.16" ||
		gotOptions.InstallDir != "/opt/QKA" || gotOptions.SelfPath != "/downloads/QKA.exe" {
		t.Fatalf("安装参数 = %+v", gotOptions)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 3 || emitted[0].Step != "check" || emitted[1].Step != "copy" || emitted[2].Step != "finish" {
		t.Fatalf("进度事件 = %+v", emitted)
	}
	if emitted[0].Detail != "检查中" {
		t.Fatalf("进度 detail = %+v", emitted[0])
	}
}

func TestInstallerServiceStartInstallLeavesInstalledExeForLaunch(t *testing.T) {
	var launchedExe string
	var launchedArgs []string
	var quit bool
	svc := &InstallerService{
		install: func(context.Context, installer.Options) (installer.Result, error) {
			return installer.Result{InstalledExe: "/opt/QKA/QKA.exe"}, nil
		},
		launch: func(exe string, args []string) error {
			launchedExe, launchedArgs = exe, args
			return nil
		},
		quit: func() { quit = true },
	}
	if err := svc.StartInstall("/opt/QKA"); err != nil {
		t.Fatal(err)
	}
	waitInstallerIdle(t, svc)
	if err := svc.LaunchInstalled(); err != nil {
		t.Fatalf("安装完成后启动应成功: %v", err)
	}
	if launchedExe != "/opt/QKA/QKA.exe" || launchedArgs != nil {
		t.Fatalf("启动参数 = %q/%v", launchedExe, launchedArgs)
	}
	if !quit {
		t.Fatal("启动已安装的程序后应退出安装向导")
	}
}

func TestInstallerServiceStartInstallRunningGuard(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	svc := &InstallerService{install: func(context.Context, installer.Options) (installer.Result, error) {
		close(started)
		<-release
		return installer.Result{}, nil
	}}
	if err := svc.StartInstall("/opt/QKA"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := svc.StartInstall("/opt/QKA"); err == nil || !strings.Contains(err.Error(), "正在进行") {
		t.Fatalf("重复启动应被拒绝，err=%v", err)
	}
	close(release)
	waitInstallerIdle(t, svc)
}

func TestInstallerServiceStartInstallFailure(t *testing.T) {
	svc := &InstallerService{install: func(context.Context, installer.Options) (installer.Result, error) {
		return installer.Result{}, errors.New("boom")
	}}
	if err := svc.StartInstall("/opt/QKA"); err != nil {
		t.Fatal(err)
	}
	waitInstallerIdle(t, svc)
	if svc.installedExe != "" {
		t.Fatal("安装失败不应记住已安装路径")
	}
	if err := svc.LaunchInstalled(); err == nil || !strings.Contains(err.Error(), "还没有完成安装") {
		t.Fatalf("未安装时启动应报错，err=%v", err)
	}
}

func TestInstallerServiceLaunchInstalledBranches(t *testing.T) {
	if err := (&InstallerService{installedExe: "/opt/QKA/QKA.exe"}).LaunchInstalled(); err == nil ||
		!strings.Contains(err.Error(), "不支持启动") {
		t.Fatalf("没有启动实现时应报错，err=%v", err)
	}

	svc := &InstallerService{
		installedExe: "/opt/QKA/QKA.exe",
		launch:       func(string, []string) error { return errors.New("denied") },
	}
	if err := svc.LaunchInstalled(); err == nil || !strings.Contains(err.Error(), "启动已安装的 QKA 失败") {
		t.Fatalf("启动失败应回传错误，err=%v", err)
	}

	(&InstallerService{}).QuitInstaller()
}

func TestInstallerReportWithoutEmitter(t *testing.T) {
	(&InstallerService{}).report(installer.StepFailed, "失败")
}

func TestLaunchInstalledRunsProgram(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > \"$QKA_INSTALLER_LAUNCH_MARKER\"\n"
	exe := filepath.Join(dir, "QKA.exe")
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QKA_INSTALLER_LAUNCH_MARKER", marker)
	if err := launchInstalled(exe, nil); err != nil {
		t.Fatalf("启动已安装程序失败: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("启动的程序没有运行")
		}
		time.Sleep(time.Millisecond)
	}
	if err := launchInstalled(filepath.Join(dir, "missing.exe"), nil); err == nil {
		t.Fatal("启动不存在的程序应报错")
	}
}

// waitInstallerIdle 等信息装 goroutine 结束，避免用固定 sleep 拉长测试时间。
func waitInstallerIdle(t *testing.T, svc *InstallerService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		running := svc.running
		svc.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("安装在超时内没有结束")
}

func TestInstallerPageIsEmbedded(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/installer.html", nil)
	rec := httptest.NewRecorder()
	assetHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("嵌入的 installer.html 状态码 = %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{
		"Quota Keepalive 安装程序",
		"/wails/runtime.js",
		"./bindings/qka/internal/gui/installerservice.js",
		`id="install-dir"`,
		`id="start"`,
		"installer-progress",
		`id="head-sub"`,
		`id="dir-field"`,
		`id="done-title"`,
		`id="error-title"`,
		`id="check-copy"`,
		"Installer.Status()",
		"err.message",
		`data-step="check"`,
		`data-step="copy"`,
		`data-step="runtime"`,
		`data-step="shortcut"`,
		`data-step="finish"`,
		"不需要管理员权限",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("installer.html 缺少 %q", want)
		}
	}
	if strings.Contains(page, "font-size: 13px") {
		t.Error("installer.html 不应写死字号，统一用 --fs-* 阶梯")
	}
}

// TestInstallerBindingsMatchServiceMethods 校验手写的 Wails 绑定与方法表一致：
// 每个导出方法的调用 ID 必须等于 Wails 的 FNV(package.Type.Method)，否则页面
// 调用会在运行时 404，而 Go 侧测试完全发现不了。
func TestInstallerBindingsMatchServiceMethods(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/installerservice.js")
	if err != nil {
		t.Fatalf("读取安装向导绑定失败: %v", err)
	}
	calls := regexp.MustCompile(`export function (\w+)\([^)]*\) \{\s*return \$Call\.ByID\((\d+)`).
		FindAllStringSubmatch(string(data), -1)
	if len(calls) == 0 {
		t.Fatal("安装向导绑定里没有找到任何 $Call.ByID 调用")
	}

	serviceType := reflect.TypeOf(&InstallerService{})
	bound := map[string]uint32{}
	for _, call := range calls {
		method := call[1]
		if _, ok := serviceType.MethodByName(method); !ok {
			t.Errorf("绑定里的 %s 不是 *InstallerService 的方法", method)
		}
		var want uint32
		_, _ = fmt.Sscanf(call[2], "%d", &want)
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte("qka/internal/gui.InstallerService." + method))
		if got := hasher.Sum32(); got != want {
			t.Errorf("%s 的绑定 ID = %d，want %d", method, want, got)
		}
		bound[method] = want
	}

	methods := 0
	for i := 0; i < serviceType.NumMethod(); i++ {
		method := serviceType.Method(i).Name
		methods++
		if _, ok := bound[method]; !ok {
			t.Errorf("服务方法 %s 缺少前端绑定", method)
		}
	}
	if len(bound) != methods {
		t.Errorf("绑定数量 = %d，服务方法数量 = %d", len(bound), methods)
	}
}

func TestResolveStartDir(t *testing.T) {
	existing := map[string]bool{
		"/home/me":               true,
		"/home/me/AppData/Local": true,
		"/home/me/AppData/Local/Programs/Quota Keepalive": false,
	}
	exists := func(path string) bool { return existing[path] }

	if got := resolveStartDir("  ", exists); got != "" {
		t.Fatalf("空初始目录应返回空串，实际 %q", got)
	}
	if got := resolveStartDir("/home/me/AppData/Local/Programs/Quota Keepalive", exists); got != "/home/me/AppData/Local" {
		t.Fatalf("不存在的目录应向上收敛到最近存在的父目录，实际 %q", got)
	}
	if got := resolveStartDir("/home/me/AppData/Local/Programs/Quota Keepalive/", exists); got != "/home/me/AppData/Local" {
		t.Fatalf("末尾分隔符不应影响结果，实际 %q", got)
	}
	if got := resolveStartDir("/home/me", exists); got != "/home/me" {
		t.Fatalf("已存在的目录应原样返回，实际 %q", got)
	}
	if got := resolveStartDir("/nowhere/at/all", exists); got != "" {
		t.Fatalf("整条路径都不存在时应返回空串，实际 %q", got)
	}
}

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if !dirExists(dir) {
		t.Fatal("临时目录应判定为存在")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirExists(file) {
		t.Fatal("普通文件不应被当成初始目录")
	}
	if dirExists(filepath.Join(dir, "missing")) {
		t.Fatal("不存在的路径不应判定为存在")
	}
}

func TestInstallerStatusFor(t *testing.T) {
	configDir := t.TempDir()
	status := installerStatusFor(InstallerDeps{ConfigDir: configDir, Version: "0.1.16", SelfPath: "/downloads/QKA.exe"})
	if status.Mode != "install" || status.Version != "0.1.16" || status.InstalledVersion != "" || status.InstallDir != "" {
		t.Fatalf("无记录时 Status = %+v", status)
	}

	installedExe := filepath.Join(configDir, "Programs", "Quota Keepalive", "QKA.exe")
	seedInstallerRecord(t, configDir, installer.Record{
		Schema:      installer.SchemaVersion,
		Version:     "0.1.15",
		InstallDir:  filepath.Dir(installedExe),
		ExePath:     installedExe,
		InstalledAt: "2026-01-01T00:00:00Z",
	})
	status = installerStatusFor(InstallerDeps{ConfigDir: configDir, Version: "0.1.16", SelfPath: "/downloads/QKA.exe"})
	if status.Mode != "upgrade" || status.InstalledVersion != "0.1.15" || status.InstallDir != filepath.Dir(installedExe) {
		t.Fatalf("下载副本应判定为升级，Status = %+v", status)
	}
	status = installerStatusFor(InstallerDeps{ConfigDir: configDir, Version: "0.1.16", SelfPath: installedExe})
	if status.Mode != "none" {
		t.Fatalf("已安装的 exe 应判定为 none，Status = %+v", status)
	}
}

// seedInstallerRecord 把一条安装记录写进配置目录。
func seedInstallerRecord(t *testing.T, configDir string, record installer.Record) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "install.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewInstallerUpgradeMode(t *testing.T) {
	configDir := t.TempDir()
	installedExe := filepath.Join(configDir, "Programs", "Quota Keepalive", "QKA.exe")
	seedInstallerRecord(t, configDir, installer.Record{
		Schema:      installer.SchemaVersion,
		Version:     "0.1.15",
		InstallDir:  filepath.Dir(installedExe),
		ExePath:     installedExe,
		InstalledAt: "2026-01-01T00:00:00Z",
	})
	inst := NewInstaller(InstallerDeps{ConfigDir: configDir, Version: "0.1.16", SelfPath: "/downloads/QKA-0.1.16.exe"})
	status := inst.service.Status()
	if status.Mode != "upgrade" || status.InstalledVersion != "0.1.15" || status.InstallDir != filepath.Dir(installedExe) {
		t.Fatalf("升级模式 Status = %+v", status)
	}
	inst.onApplicationStarted(nil)
}

func TestInstallerServiceStartInstallUpgrade(t *testing.T) {
	var gotOptions installer.Options
	installCalled := false
	svc := &InstallerService{
		configDir: "/config",
		version:   "0.1.16",
		selfPath:  "/downloads/QKA-0.1.16.exe",
		status:    InstallerStatus{Mode: "upgrade", Version: "0.1.16", InstalledVersion: "0.1.15", InstallDir: "/opt/QKA"},
		install: func(context.Context, installer.Options) (installer.Result, error) {
			installCalled = true
			return installer.Result{}, nil
		},
		upgrade: func(_ context.Context, opts installer.Options) (installer.Result, error) {
			gotOptions = opts
			return installer.Result{InstalledExe: "/opt/QKA/QKA.exe"}, nil
		},
	}
	// 升级模式忽略页面传来的目录。
	if err := svc.StartInstall("/ignored"); err != nil {
		t.Fatal(err)
	}
	waitInstallerIdle(t, svc)
	if installCalled {
		t.Fatal("升级模式不应调用首次安装")
	}
	if gotOptions.ConfigDir != "/config" || gotOptions.Version != "0.1.16" ||
		gotOptions.SelfPath != "/downloads/QKA-0.1.16.exe" || gotOptions.InstallDir != "" {
		t.Fatalf("升级参数 = %+v", gotOptions)
	}
	if svc.installedExe != "/opt/QKA/QKA.exe" {
		t.Fatalf("升级后应记住已安装路径，实际 %q", svc.installedExe)
	}
}
