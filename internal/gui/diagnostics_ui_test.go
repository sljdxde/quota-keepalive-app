package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qka/internal/tokenspeed"
)

// readDiagnosticsAssets 读取诊断区块涉及的四个前端文件：两个设置页、共享
// 脚本与生成的绑定。断言只做存在性检查，保持与其它 *_ui_test.go 同一风格。
func readDiagnosticsAssets(t *testing.T) (string, string, string, string) {
	t.Helper()
	sources := map[string]string{}
	for _, name := range []string{
		"frontend/main.html",
		"frontend/settings.html",
		"frontend/assets/diagnostics.js",
		"frontend/bindings/qka/internal/gui/boundservice.js",
	} {
		data, err := frontendFS.ReadFile(name)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", name, err)
		}
		sources[name] = string(data)
	}
	return sources["frontend/main.html"],
		sources["frontend/settings.html"],
		sources["frontend/assets/diagnostics.js"],
		sources["frontend/bindings/qka/internal/gui/boundservice.js"]
}

// 两个设置页（主窗口设置页签 + 独立设置窗口）都要有完整的诊断区块：
// 元数据行、「复制诊断信息」与「打开日志目录」按钮，缺一项用户就取不了证。
func TestSettingsPagesHaveDiagnosticsSection(t *testing.T) {
	mainPage, settingsPage, script, bindings := readDiagnosticsAssets(t)
	for name, page := range map[string]string{
		"main.html":     mainPage,
		"settings.html": settingsPage,
	} {
		requireStrings(t, page, name+" 诊断区块",
			`id="settings-diagnostics-title">诊断</b>`,
			`id="diagnostics-state"`,
			`id="diagnostics-version"`,
			`id="diagnostics-runtime"`,
			`id="diagnostics-renderer"`,
			`id="diagnostics-perf-mode"`,
			`id="diagnostics-pet-fps"`,
			`id="diagnostics-slow-polls"`,
			`id="diagnostics-logdir"`,
			`id="diagnostics-copy">复制诊断信息</button>`,
			`id="diagnostics-open-logdir">打开日志目录</button>`,
			"initDiagnostics",
		)
	}
	// 共享脚本要合并前后端两侧数据：后端绑定 + 前端 perf-mode 检测，
	// 且复制走剪贴板（不弹任何下载/导出框）。
	requireStrings(t, script, "diagnostics.js",
		"Bound.DiagnosticsGet",
		"Bound.DiagnosticsOpenLogDir",
		"lastResolvedPerformanceMode",
		"initPerformanceMode",
		"performanceLow",
		"PERFORMANCE_LOW_FPS",
		"buildDiagnosticsText",
		"writeText",
		"execCommand",
		"不含凭据",
	)
	// 生成的绑定里要有两个新方法（ID 是 Wails 对 FQN 的 FNV-32a）。
	requireStrings(t, bindings, "boundservice.js",
		"function DiagnosticsGet()",
		"$Call.ByID(2606577501)",
		"function DiagnosticsOpenLogDir()",
		"$Call.ByID(290522766)",
	)
}

// 一键复制的纯文本必须覆盖 issue 要求的全部字段：版本/平台、运行时目录与
// 体积、渲染器（含软件渲染）、性能模式、3D 帧率、慢轮询（耗时+阈值）、日志
// 目录；并且文案声明不含凭据。
func TestDiagnosticsCopyTextCoversRequiredFields(t *testing.T) {
	_, _, script, _ := readDiagnosticsAssets(t)
	requireStrings(t, script, "diagnostics.js 复制文本",
		"Quota Keepalive 诊断信息",
		"生成时间",
		"版本: ",
		"平台: ",
		"运行时目录: ",
		"渲染器: ",
		"软件渲染: ",
		"性能模式: ",
		"3D 宠物目标帧率: ",
		"慢轮询",
		"ms",
		"日志目录: ",
	)
	if !strings.Contains(script, "petTargetFPS") {
		t.Error("diagnostics.js 缺少 petTargetFPS（3D 目标帧率来源）")
	}
	if !strings.Contains(script, "formatBytes") {
		t.Error("diagnostics.js 缺少 formatBytes（运行时体积格式化）")
	}
}

// runtimeDirPath 只接受可作目录名的版本串，任何路径片段都拒绝（防逃出
// runtime/ 的展示层注入）。
func TestRuntimeDirPathSanitizesVersion(t *testing.T) {
	if got := runtimeDirPath(`C:\QKA`, "0.9.2"); got != filepath.Join(`C:\QKA`, "runtime", "0.9.2") {
		t.Fatalf("正常版本应拼出 runtime 子目录，实际 %q", got)
	}
	for _, version := range []string{"", " ", ".", "..", "a/b", `a\b`, "a:b", "sub/dir"} {
		if got := runtimeDirPath(`C:\QKA`, version); got != "" {
			t.Fatalf("版本 %q 应被拒绝，实际得到 %q", version, got)
		}
	}
	if got := runtimeDirPath("", "0.9.2"); got != "" {
		t.Fatalf("空配置目录应返回空串，实际 %q", got)
	}
}

// dirUsage 统计常规文件个数与总字节；目录缺失时返回 0 而不是报错。
func TestDirUsageCountsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if files, bytes := dirUsage(filepath.Join(dir, "missing")); files != 0 || bytes != 0 {
		t.Fatalf("目录不存在应返回 0,0，实际 %d,%d", files, bytes)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), bytes10(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "c.bin"), bytes10(), 0o600); err != nil {
		t.Fatal(err)
	}
	files, bytes := dirUsage(dir)
	if files != 3 || bytes != 23 {
		t.Fatalf("应统计 3 个文件共 23 字节，实际 %d 个 %d 字节", files, bytes)
	}
}

func bytes10() []byte { return make([]byte, 10) }

// openLogDirInFileManager：目录为空/不存在/不是目录都拒绝，只有真实存在
// 的目录才会交给启动器（且传的是绝对路径）。
func TestOpenLogDirValidatesBeforeLaunch(t *testing.T) {
	launched := ""
	previous := openLogDirLauncher
	openLogDirLauncher = func(dir string) error { launched = dir; return nil }
	defer func() { openLogDirLauncher = previous }()

	if err := openLogDirInFileManager("  "); err == nil {
		t.Fatal("空日志目录应报错（日志尚未初始化）")
	}
	if err := openLogDirInFileManager(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("不存在的目录应报错")
	}
	file := filepath.Join(t.TempDir(), "qka.log")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := openLogDirInFileManager(file); err == nil {
		t.Fatal("普通文件不是目录，应报错")
	}
	if launched != "" {
		t.Fatalf("非法输入不应触达启动器，实际启动了 %q", launched)
	}

	dir := t.TempDir()
	if err := openLogDirInFileManager(dir); err != nil {
		t.Fatalf("合法目录应打开成功：%v", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if launched != abs {
		t.Fatalf("启动器应收到绝对路径 %q，实际 %q", abs, launched)
	}
}

// buildDiagnosticsInfo 的后端聚合：版本兜底 dev、平台字段来自 runtime、
// 慢轮询来自注入的 tokenspeed 服务；json 输出不含内容性字段名。
func TestBuildDiagnosticsInfoAggregatesMetadata(t *testing.T) {
	fixedAt := time.Date(2026, 11, 4, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	speed := tokenspeed.New(tokenspeed.Config{})
	info := buildDiagnosticsInfo(diagnosticsSource{
		version:    "",
		tokenSpeed: speed,
		now:        func() time.Time { return fixedAt },
	})
	if info.Version != "dev" {
		t.Fatalf("未注入版本应兜底 dev，实际 %q", info.Version)
	}
	if info.GOOS == "" || info.GOARCH == "" || info.GoVersion == "" {
		t.Fatal("平台/架构/Go 版本不应为空")
	}
	if info.SlowPollThresholdMS != 150 {
		t.Fatalf("慢轮询阈值应为 150ms，实际 %d", info.SlowPollThresholdMS)
	}
	if !info.TokenSpeedAvailable || info.SlowPolls == nil {
		t.Fatal("注入 tokenspeed 服务时应标记可用且 SlowPolls 非 nil")
	}
	if info.GeneratedAt != fixedAt.Format(time.RFC3339) {
		t.Fatalf("生成时间应取注入时钟，实际 %q", info.GeneratedAt)
	}

	// 走一遍绑定方法：返回的是 json，且包含诊断区块需要的键。
	service := NewBoundService(Deps{CurrentVersion: "9.9.9", TokenSpeed: speed})
	raw, err := service.DiagnosticsGet()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"version", "go_version", "goos", "goarch", "config_dir", "log_dir",
		"runtime_dir", "runtime_available", "runtime_bytes", "runtime_files",
		"token_speed_available", "slow_poll_threshold_ms", "slow_polls", "generated_at",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("DiagnosticsGet 输出缺少键 %q", key)
		}
	}
	rendered := string(raw)
	for _, banned := range []string{"token_content", "prompt", "credential", "api_key"} {
		if strings.Contains(rendered, banned) {
			t.Errorf("诊断输出不应包含内容性字段 %q", banned)
		}
	}
}
