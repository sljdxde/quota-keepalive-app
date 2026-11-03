package applog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// reset 在用例结束时把进程级日志状态归零，避免用例之间互相污染。
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if c := current.Load(); c != nil {
			_ = c.close()
		}
	})
}

// freeze 把「现在」固定到 at，跨天/保留期用例不必真的等一天。
func freeze(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { return at }
}

// initTemp 在临时目录里初始化日志，返回 <configDir>/logs。
func initTemp(t *testing.T) string {
	t.Helper()
	reset(t)
	configDir := t.TempDir()
	closeFn, err := Init(configDir, "test-version")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	return filepath.Join(configDir, dirName)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dayFile 返回 dir 下 time 当天的日志文件路径。
func dayFile(dir string, at time.Time) string {
	return filepath.Join(dir, filePrefix+at.Format(dayLayout)+fileSuffix)
}

// boom 在一个函数里直接 defer RecoverPanic 再 panic：recover 只能被被 defer
// 的函数直接调用，所以这里不能包在闭包里。
func boom(msg string) {
	func() {
		defer RecoverPanic()
		panic(msg)
	}()
}

func TestInitCreatesDailyFileAndWritesStructuredLines(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	configDir := t.TempDir()

	closeFn, err := Init(configDir, "1.2.3")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if closeFn == nil {
		t.Fatal("Init 应返回非 nil 关闭函数")
	}
	Infof("main", "hello %s", "world")
	if err := closeFn(); err != nil {
		t.Fatalf("关闭日志: %v", err)
	}

	dir := filepath.Join(configDir, dirName)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("日志目录应存在: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("日志目录权限应为 0700，实际 %o", perm)
	}

	path := dayFile(dir, at)
	data := readFile(t, path)
	for _, want := range []string{
		"time=", "level=INFO", "module=applog",
		"version=1.2.3", "dir=", "hello world", "module=main",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 日志文件: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("日志文件权限应为 0600，实际 %o", perm)
	}
}

func TestInitAppendsAcrossRestart(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	configDir := t.TempDir()

	for i, msg := range []string{"first-run", "second-run"} {
		closeFn, err := Init(configDir, "test-version")
		if err != nil {
			t.Fatalf("第 %d 次 Init: %v", i+1, err)
		}
		Infof("main", "%s", msg)
		if err := closeFn(); err != nil {
			t.Fatalf("第 %d 次关闭: %v", i+1, err)
		}
	}

	data := readFile(t, dayFile(filepath.Join(configDir, dirName), at))
	if !strings.Contains(data, "first-run") || !strings.Contains(data, "second-run") {
		t.Fatalf("重新打开应追加而不是截断，实际：\n%s", data)
	}
	if n := strings.Count(data, "日志已初始化"); n != 2 {
		t.Errorf("两次 Init 应各留一行初始化记录，实际 %d 行", n)
	}
}

func TestInitRepeatedSwitchesDirAndClosesOld(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	dirA, dirB := t.TempDir(), t.TempDir()

	closeA, err := Init(dirA, "v1")
	if err != nil {
		t.Fatalf("Init A: %v", err)
	}
	Infof("main", "in-dir-a")

	closeB, err := Init(dirB, "v2")
	if err != nil {
		t.Fatalf("Init B: %v", err)
	}
	Infof("main", "in-dir-b")

	// 旧关闭函数在重复 Init 后已经关闭，再调用仍是安全 no-op。
	if err := closeA(); err != nil {
		t.Errorf("旧关闭函数应幂等，实际 %v", err)
	}
	if err := closeA(); err != nil {
		t.Errorf("旧关闭函数重复调用应幂等，实际 %v", err)
	}
	if err := closeB(); err != nil {
		t.Fatalf("关闭 B: %v", err)
	}

	a := readFile(t, dayFile(filepath.Join(dirA, dirName), at))
	if !strings.Contains(a, "in-dir-a") || strings.Contains(a, "in-dir-b") {
		t.Errorf("目录 A 只应有 A 的日志，实际：\n%s", a)
	}
	b := readFile(t, dayFile(filepath.Join(dirB, dirName), at))
	if !strings.Contains(b, "in-dir-b") {
		t.Errorf("目录 B 应有 B 的日志，实际：\n%s", b)
	}
}

func TestInitEmptyConfigDirFailsWithoutPanic(t *testing.T) {
	reset(t)
	closeFn, err := Init("   ", "test-version")
	if err == nil {
		t.Fatal("空配置目录应返回错误")
	}
	if closeFn == nil {
		t.Fatal("失败时也应返回非 nil 空关闭函数")
	}
	if err := closeFn(); err != nil {
		t.Errorf("空关闭函数应返回 nil，实际 %v", err)
	}
	if Logger() != discardLogger {
		t.Error("未初始化时 Logger 应返回丢弃日志器")
	}
	Infof("main", "不应 panic")
	Debugf("main", "不应 panic")
	Warnf("main", "不应 panic")
	Errorf("main", "不应 panic")
}

func TestInitMkdirFailureDegradesToDiscard(t *testing.T) {
	reset(t)
	configDir := t.TempDir()
	// 用普通文件占住 logs 路径，让 MkdirAll 失败。
	if err := os.WriteFile(filepath.Join(configDir, dirName), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	closeFn, err := Init(configDir, "test-version")
	if err == nil {
		t.Fatal("日志目录被文件占住时应返回错误")
	}
	if closeFn == nil {
		t.Fatal("失败时应返回非 nil 空关闭函数")
	}
	if Logger() != discardLogger {
		t.Error("Init 失败后应退回丢弃日志器")
	}
}

func TestInitOpenFailureDegradesToDiscard(t *testing.T) {
	reset(t)
	oldOpen := openFile
	t.Cleanup(func() { openFile = oldOpen })
	openFile = func(string, int, fs.FileMode) (*os.File, error) {
		return nil, errors.New("open failed")
	}
	_, err := Init(t.TempDir(), "test-version")
	if err == nil || !strings.Contains(err.Error(), "打开日志文件") {
		t.Fatalf("打开失败应返回带路径的错误，实际 %v", err)
	}
	if Logger() != discardLogger {
		t.Error("Init 失败后应退回丢弃日志器")
	}
}

func TestQKALogDirOverride(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	override := filepath.Join(t.TempDir(), "custom-logs")
	t.Setenv(dirEnv, override)
	configDir := t.TempDir()

	closeFn, err := Init(configDir, "test-version")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	Infof("main", "override-dir")
	if err := closeFn(); err != nil {
		t.Fatalf("关闭: %v", err)
	}

	data := readFile(t, dayFile(override, at))
	if !strings.Contains(data, "override-dir") {
		t.Errorf("日志应写在 QKA_LOG_DIR，实际：\n%s", data)
	}
	if exists(filepath.Join(configDir, dirName)) {
		t.Error("QKA_LOG_DIR 覆盖后不应再创建 <configDir>/logs")
	}
}

func TestLogLevelFiltering(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
		wantError bool
	}{
		{"debug", "debug", true, true, true, true},
		{"info", "info", false, true, true, true},
		{"warn", "warn", false, false, true, true},
		{"warning-alias", "warning", false, false, true, true},
		{"error", "error", false, false, false, true},
		{"unknown-falls-back-to-info", "verbose", false, true, true, true},
		{"empty-is-info", "", false, true, true, true},
		{"trimmed", "  DEBUG  ", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
			freeze(t, at)
			t.Setenv(levelEnv, tc.env)
			dir := initTemp(t)

			Debugf("filter", "marker-debug")
			Infof("filter", "marker-info")
			Warnf("filter", "marker-warn")
			Errorf("filter", "marker-error")
			if c := current.Load(); c != nil {
				_ = c.close()
			}

			data := readFile(t, dayFile(dir, at))
			for marker, want := range map[string]bool{
				"marker-debug": tc.wantDebug,
				"marker-info":  tc.wantInfo,
				"marker-warn":  tc.wantWarn,
				"marker-error": tc.wantError,
			} {
				if got := strings.Contains(data, marker); got != want {
					t.Errorf("QKA_LOG_LEVEL=%q 时 %s 是否写入 = %v，期望 %v；日志：\n%s",
						tc.env, marker, got, want, data)
				}
			}
		})
	}
}

func TestModuleAndLogger(t *testing.T) {
	reset(t)
	if Logger() != discardLogger {
		t.Error("未初始化时 Logger 应返回丢弃日志器")
	}
	if Module("wails") != discardLogger {
		t.Error("未初始化时 Module 应返回丢弃日志器")
	}

	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	dir := initTemp(t)
	Module("wails").Info("wails-internal")
	Logger().Info("base-logger")
	if c := current.Load(); c != nil {
		_ = c.close()
	}

	data := readFile(t, dayFile(dir, at))
	for _, want := range []string{"module=wails", "wails-internal", "base-logger"} {
		if !strings.Contains(data, want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, data)
		}
	}
}

func TestWritesAreSanitized(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	dir := initTemp(t)
	Infof("privacy", "Authorization: Bearer abcdefghijklmnop token=supersecret123 uid:1234567890 email alice@example.com")
	if c := current.Load(); c != nil {
		_ = c.close()
	}

	data := readFile(t, dayFile(dir, at))
	for _, forbidden := range []string{"abcdefghijklmnop", "supersecret123", "alice@example.com", "1234567890"} {
		if strings.Contains(data, forbidden) {
			t.Errorf("日志不应出现明文 %q，实际：\n%s", forbidden, data)
		}
	}
	for _, want := range []string{"[redacted]", "a***@example.com"} {
		if !strings.Contains(data, want) {
			t.Errorf("日志应包含脱敏标记 %q，实际：\n%s", want, data)
		}
	}
}

func TestDayRotation(t *testing.T) {
	reset(t)
	day := time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)
	oldNow := now
	t.Cleanup(func() { now = oldNow })
	now = func() time.Time { return day }
	dir := initTemp(t)

	Infof("main", "day-one")
	day = day.Add(24 * time.Hour)
	Infof("main", "day-two")
	if c := current.Load(); c != nil {
		_ = c.close()
	}

	first := dayFile(dir, time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local))
	second := dayFile(dir, time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local))
	if !exists(first) || !exists(second) {
		t.Fatalf("跨天应切成两个文件，实际目录：%v", listNames(t, dir))
	}
	if data := readFile(t, first); !strings.Contains(data, "day-one") {
		t.Errorf("前一天文件应保留 day-one，实际：\n%s", data)
	}
	if data := readFile(t, second); !strings.Contains(data, "day-two") {
		t.Errorf("新一天文件应有 day-two，实际：\n%s", data)
	}
}

func TestSizeRotation(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	oldMax := maxBytes
	t.Cleanup(func() { maxBytes = oldMax })
	// 只要文件已有内容就切新文件：验证 -1/-2 后缀命名，不必真的写 32MB。
	maxBytes = 1
	dir := initTemp(t)

	Infof("main", "rotation-1")
	Infof("main", "rotation-2")
	if c := current.Load(); c != nil {
		_ = c.close()
	}

	base := dayFile(dir, at)
	rotated1 := filepath.Join(dir, filePrefix+at.Format(dayLayout)+"-1"+fileSuffix)
	rotated2 := filepath.Join(dir, filePrefix+at.Format(dayLayout)+"-2"+fileSuffix)
	names := listNames(t, dir)
	for _, want := range []string{filepath.Base(base), filepath.Base(rotated1), filepath.Base(rotated2)} {
		if !containsName(names, want) {
			t.Errorf("应有日志文件 %s，实际 %v", want, names)
		}
	}
	if data := readFile(t, rotated1); !strings.Contains(data, "rotation-1") {
		t.Errorf("-1 文件应有 rotation-1，实际：\n%s", data)
	}
	if data := readFile(t, rotated2); !strings.Contains(data, "rotation-2") {
		t.Errorf("-2 文件应有 rotation-2，实际：\n%s", data)
	}
}

func TestWriteRotationFailuresDoNotPanic(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	oldOpen := openFile
	t.Cleanup(func() { openFile = oldOpen })

	t.Run("day-change-open-fails", func(t *testing.T) {
		dir := initTemp(t)
		openFile = func(string, int, fs.FileMode) (*os.File, error) {
			return nil, errors.New("open failed")
		}
		now = func() time.Time { return at.Add(24 * time.Hour) }
		Infof("main", "will-fail") // 跨天重新打开失败：写入报错被 slog 忽略，不 panic
		openFile = oldOpen
		now = func() time.Time { return at }
		if exists(dayFile(dir, at.Add(24*time.Hour))) {
			t.Error("打开失败时不应创建新一天文件")
		}
	})

	t.Run("size-rotation-open-fails", func(t *testing.T) {
		oldMax := maxBytes
		t.Cleanup(func() { maxBytes = oldMax })
		maxBytes = 1
		dir := initTemp(t)
		openFile = func(string, int, fs.FileMode) (*os.File, error) {
			return nil, errors.New("open failed")
		}
		Infof("main", "will-fail") // 超限切文件失败：不 panic
		openFile = oldOpen
		if names := listNames(t, dir); containsName(names, filePrefix+at.Format(dayLayout)+"-1"+fileSuffix) {
			t.Errorf("切文件失败时不应产生 -1 文件，实际 %v", names)
		}
	})
}

func TestRetentionKeepsNewestDays(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	oldKeep := keepFiles
	t.Cleanup(func() { keepFiles = oldKeep })
	keepFiles = 3
	configDir := t.TempDir()
	dir := filepath.Join(configDir, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for day := 1; day <= 5; day++ {
		path := filepath.Join(dir, filePrefix+time.Date(2026, 11, day, 0, 0, 0, 0, time.Local).Format(dayLayout)+fileSuffix)
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 不合命名规则的文件不参与保留策略，也不该被误删。
	for _, name := range []string{"qka-bad.log", "qka-2026x105.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	closeFn, err := Init(configDir, "test-version")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	_ = closeFn()

	for _, day := range []int{4, 5, 6} {
		path := filepath.Join(dir, filePrefix+time.Date(2026, 11, day, 0, 0, 0, 0, time.Local).Format(dayLayout)+fileSuffix)
		if !exists(path) {
			t.Errorf("保留期应保留最近的 2026-11-%02d，实际 %v", day, listNames(t, dir))
		}
	}
	for _, day := range []int{1, 2, 3} {
		path := filepath.Join(dir, filePrefix+time.Date(2026, 11, day, 0, 0, 0, 0, time.Local).Format(dayLayout)+fileSuffix)
		if exists(path) {
			t.Errorf("保留期应删除 2026-11-%02d，实际 %v", day, listNames(t, dir))
		}
	}
	for _, name := range []string{"qka-bad.log", "qka-2026x105.log", "notes.txt"} {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("不应删除不合命名规则的文件 %s", name)
		}
	}
}

func TestPruneGlobErrorsAreIgnored(t *testing.T) {
	// 目录名含 '[' 会让 filepath.Glob 返回 ErrBadPattern；保留策略必须静默跳过。
	pruneLogs("[")
	pruneCrashes("[")
}

func TestDayOfParsing(t *testing.T) {
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"qka-20261105.log", "20261105", true},
		{"qka-20261105-1.log", "20261105", true},
		{"qka-bad.log", "", false},
		{"qka-2026110.log", "", false},
		{"qka-2026x105.log", "", false},
		{"other.txt", "", false},
	}
	for _, tc := range cases {
		got, ok := dayOf(tc.name)
		if got != tc.want || ok != tc.ok {
			t.Errorf("dayOf(%q) = %q,%v，期望 %q,%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRecoverPanicWritesLogAndCrashFile(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 16, 16, 54, 0, time.Local)
	freeze(t, at)
	dir := initTemp(t)

	boom("boom token=abc123")

	logData := readFile(t, dayFile(dir, at))
	for _, want := range []string{"level=ERROR", "module=panic", "捕获到未处理 panic"} {
		if !strings.Contains(logData, want) {
			t.Errorf("日志应包含 %q，实际：\n%s", want, logData)
		}
	}
	if strings.Contains(logData, "abc123") {
		t.Errorf("panic 文本应脱敏，实际：\n%s", logData)
	}

	crashPath := filepath.Join(dir, crashPrefix+at.Format(stampLayout)+crashSuffix)
	crashData := readFile(t, crashPath)
	for _, want := range []string{"panic: boom", "goroutine", "log_dir:"} {
		if !strings.Contains(crashData, want) {
			t.Errorf("崩溃文件应包含 %q，实际：\n%s", want, crashData)
		}
	}
	if strings.Contains(crashData, "abc123") {
		t.Errorf("崩溃文件应脱敏，实际：\n%s", crashData)
	}
	info, err := os.Stat(crashPath)
	if err != nil {
		t.Fatalf("stat 崩溃文件: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("崩溃文件权限应为 0600，实际 %o", perm)
	}
}

func TestCrashFilesRespectRetention(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 16, 16, 54, 0, time.Local)
	freeze(t, at)
	oldKeep := keepFiles
	t.Cleanup(func() { keepFiles = oldKeep })
	keepFiles = 1
	dir := initTemp(t)

	boom("first")
	at = at.Add(2 * time.Second)
	now = func() time.Time { return at }
	boom("second")

	names := listNames(t, dir)
	var crashes []string
	for _, name := range names {
		if strings.HasPrefix(name, crashPrefix) && strings.HasSuffix(name, crashSuffix) {
			crashes = append(crashes, name)
		}
	}
	if len(crashes) != 1 {
		t.Fatalf("保留 1 个崩溃文件时只应剩 1 个，实际 %v", crashes)
	}
	want := crashPrefix + at.Format(stampLayout) + crashSuffix
	if crashes[0] != want {
		t.Errorf("应保留最新崩溃文件 %s，实际 %s", want, crashes[0])
	}
}

func TestRecoverPanicWithoutInitIsSafe(t *testing.T) {
	reset(t)
	boom("no logger") // 未初始化：只吞掉 panic，不落文件、不再 panic
	if Logger() != discardLogger {
		t.Error("未初始化时 Logger 仍应是丢弃日志器")
	}
}

func TestRecoverPanicWithoutPanicIsNoop(t *testing.T) {
	reset(t)
	func() {
		defer RecoverPanic()
	}()
	if Logger() != discardLogger {
		t.Error("没有 panic 时不应改变日志状态")
	}
}

func TestRunPassesThroughExitCode(t *testing.T) {
	reset(t)
	if code := Run(func() int { return 7 }); code != 7 {
		t.Fatalf("正常返回应透传退出码 7，实际 %d", code)
	}
}

func TestRunMapsPanicToExitCode(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 16, 16, 54, 0, time.Local)
	freeze(t, at)
	dir := initTemp(t)

	var code int
	func() {
		// Run 自己 recover，panic 不会逃出这里。
		code = Run(func() int { panic("run-boom") })
	}()
	if code != PanicExitCode {
		t.Fatalf("panic 应映射成退出码 %d，实际 %d", PanicExitCode, code)
	}
	crash := filepath.Join(dir, crashPrefix+at.Format(stampLayout)+crashSuffix)
	if !exists(crash) {
		t.Fatalf("Run 捕获 panic 后应写崩溃文件，实际 %v", listNames(t, dir))
	}
	if data := readFile(t, crash); !strings.Contains(data, "run-boom") {
		t.Errorf("崩溃文件应记录 panic 文本，实际：\n%s", data)
	}
}

func TestCloseIsIdempotentAndWriteAfterCloseFails(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	initTemp(t)
	c := current.Load()
	if c == nil {
		t.Fatal("Init 后应有当前日志状态")
	}
	if err := c.close(); err != nil {
		t.Fatalf("第一次关闭: %v", err)
	}
	if err := c.close(); err != nil {
		t.Fatalf("重复关闭应幂等: %v", err)
	}
	if _, err := c.writer.Write([]byte("after-close\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("关闭后写入应返回 os.ErrClosed，实际 %v", err)
	}
	if Logger() != discardLogger {
		t.Error("关闭后 Logger 应退回丢弃日志器")
	}
	// writer 自己也幂等：core 已关过，再直接关一次是空操作。
	if err := c.writer.Close(); err != nil {
		t.Errorf("writer 重复关闭应幂等，实际 %v", err)
	}
}

func TestWriteFailureSurfacesError(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	initTemp(t)
	c := current.Load()

	// 绕过 writer 直接关掉底层文件，制造写入失败（磁盘错误无法稳定复现）。
	if err := c.writer.file.Close(); err != nil {
		t.Fatalf("关闭底层文件: %v", err)
	}
	if _, err := c.writer.Write([]byte("disk-error\n")); err == nil {
		t.Fatal("底层文件不可写时应返回错误而不是 panic")
	}
}

func TestConcurrentWritesStayLineIntact(t *testing.T) {
	reset(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	dir := initTemp(t)

	const workers, perWorker = 8, 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				Infof("concurrent", "concurrent-%d-%d", w, i)
			}
		}(w)
	}
	wg.Wait()
	if c := current.Load(); c != nil {
		_ = c.close()
	}

	data := readFile(t, dayFile(dir, at))
	if got := strings.Count(data, "concurrent-"); got != workers*perWorker {
		t.Errorf("并发写入应保留全部 %d 行，实际 %d", workers*perWorker, got)
	}
}

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录 %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
