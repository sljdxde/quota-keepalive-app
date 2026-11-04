package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"qka/internal/runtimebundle"
)

// fakeSink 是 copySink 的测试实现，用来逐项触发写入/同步/权限/关闭失败。
type fakeSink struct {
	name     string
	writeErr error
	syncErr  error
	chmodErr error
	closeErr error
	data     []byte
}

func (f *fakeSink) Name() string { return f.name }

func (f *fakeSink) Chmod(fs.FileMode) error { return f.chmodErr }

func (f *fakeSink) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.data = append(f.data, p...)
	return len(p), nil
}

func (f *fakeSink) Sync() error  { return f.syncErr }
func (f *fakeSink) Close() error { return f.closeErr }

// withEnv 临时替换环境接缝，cleanup 时恢复。
func withEnv(t *testing.T, _ string, env map[string]string) {
	t.Helper()
	oldLookup := lookupEnv
	t.Cleanup(func() { lookupEnv = oldLookup })
	lookupEnv = func(key string) string { return env[key] }
}

func writeFile(t *testing.T, path string, data []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDir(t *testing.T) {
	t.Run("LocalAppData 优先于 exe 目录", func(t *testing.T) {
		withEnv(t, "windows", map[string]string{"LOCALAPPDATA": `/Users/me/AppData/Local`})
		got := DefaultDir(t.TempDir(), "/Users/me/Downloads/QKA.exe")
		want := filepath.Join(`/Users/me/AppData/Local`, "Programs", defaultDirName)
		if got != want {
			t.Fatalf("默认目录 = %q，want %q", got, want)
		}
	})

	t.Run("没有 LOCALAPPDATA 时退回 exe 目录", func(t *testing.T) {
		withEnv(t, "windows", nil)
		exe := filepath.Join(t.TempDir(), "QKA.exe")
		if got, want := DefaultDir(t.TempDir(), exe), filepath.Dir(exe); got != want {
			t.Fatalf("默认目录 = %q，want %q", got, want)
		}
	})

	t.Run("都没有时返回空串", func(t *testing.T) {
		withEnv(t, "windows", nil)
		if got := DefaultDir(t.TempDir(), ""); got != "" {
			t.Fatalf("默认目录 = %q，want 空串", got)
		}
	})

	t.Run("已有安装记录且目录存在时沿用它", func(t *testing.T) {
		withEnv(t, "windows", map[string]string{"LOCALAPPDATA": `/Users/me/AppData/Local`})
		configDir := t.TempDir()
		installed := filepath.Join(t.TempDir(), "Custom QKA")
		if err := os.MkdirAll(installed, 0o755); err != nil {
			t.Fatal(err)
		}
		seedRecord(t, configDir, Record{
			Schema: SchemaVersion, Version: "0.1.15",
			InstallDir: installed, ExePath: filepath.Join(installed, ExeName),
			InstalledAt: "2026-01-02T03:04:05Z",
		})
		if got := DefaultDir(configDir, "/Users/me/Downloads/QKA.exe"); got != installed {
			t.Fatalf("应沿用已安装目录 %q，实际 %q", installed, got)
		}
	})

	t.Run("安装记录目录已消失时回到默认位置", func(t *testing.T) {
		withEnv(t, "windows", map[string]string{"LOCALAPPDATA": `/Users/me/AppData/Local`})
		configDir := t.TempDir()
		gone := filepath.Join(t.TempDir(), "Deleted QKA")
		seedRecord(t, configDir, Record{
			Schema: SchemaVersion, Version: "0.1.15",
			InstallDir: gone, ExePath: filepath.Join(gone, ExeName),
			InstalledAt: "2026-01-02T03:04:05Z",
		})
		want := filepath.Join(`/Users/me/AppData/Local`, "Programs", defaultDirName)
		if got := DefaultDir(configDir, ""); got != want {
			t.Fatalf("默认目录 = %q，want %q", got, want)
		}
	})
}

func TestNeedsInstallAndLoad(t *testing.T) {
	configDir := t.TempDir()
	if !NeedsInstall(configDir) {
		t.Fatal("没有 install.json 时应需要安装")
	}
	if _, ok := Load("  "); ok {
		t.Fatal("空配置目录不应读出安装记录")
	}

	valid := Record{
		Schema:      SchemaVersion,
		Version:     "0.1.16",
		InstallDir:  filepath.Join(t.TempDir(), "installed"),
		ExePath:     filepath.Join(t.TempDir(), "installed", ExeName),
		InstalledAt: "2026-01-02T03:04:05Z",
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(configDir, stateName), data, 0o600)
	if NeedsInstall(configDir) {
		t.Fatal("合法 install.json 应视为已安装")
	}
	got, ok := Load(configDir)
	if !ok || got != valid {
		t.Fatalf("Load = %+v/%v，want %+v", got, ok, valid)
	}

	cases := map[string]string{
		"损坏 JSON":   "{not-json",
		"格式号不认识":    `{"schema":99,"install_dir":"C:\\x","exe_path":"C:\\x\\QKA.exe"}`,
		"安装目录为空":    `{"schema":1,"install_dir":"  ","exe_path":"C:\\x\\QKA.exe"}`,
		"可执行文件路径为空": `{"schema":1,"install_dir":"C:\\x","exe_path":""}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			writeFile(t, filepath.Join(configDir, stateName), []byte(content), 0o600)
			if !NeedsInstall(configDir) {
				t.Fatalf("%s 的 install.json 应视为未安装", name)
			}
		})
	}

	// 配置目录本身是文件时读不到记录（ReadFile 返回 ENOTDIR）。
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, fileAsDir, []byte("x"), 0o600)
	if !NeedsInstall(fileAsDir) {
		t.Fatal("配置目录不可读时应视为未安装")
	}
}

func TestPrepareInstallDir(t *testing.T) {
	if _, err := prepareInstallDir("   "); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("空白路径应被拒绝，err=%v", err)
	}
	if _, err := prepareInstallDir("relative/dir"); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径应被拒绝，err=%v", err)
	}
	if _, err := prepareInstallDir(string(filepath.Separator)); err == nil || !strings.Contains(err.Error(), "根目录") {
		t.Fatalf("卷根应被拒绝，err=%v", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, []byte("x"), 0o600)
	if _, err := prepareInstallDir(filepath.Join(file, "sub")); err == nil || !strings.Contains(err.Error(), "无法创建安装目录") {
		t.Fatalf("父级是文件时应报创建失败，err=%v", err)
	}

	oldCreate := createTempFile
	t.Cleanup(func() { createTempFile = oldCreate })
	createTempFile = func(string, string) (copySink, error) { return nil, errors.New("disk full") }
	if _, err := prepareInstallDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "不可写") {
		t.Fatalf("建探测文件失败应报不可写，err=%v", err)
	}
	createTempFile = func(dir, pattern string) (copySink, error) {
		return &fakeSink{name: filepath.Join(dir, "probe"), closeErr: errors.New("close failed")}, nil
	}
	if _, err := prepareInstallDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "不可写") {
		t.Fatalf("关闭探测文件失败应报不可写，err=%v", err)
	}
	createTempFile = oldCreate

	// 正常情况下创建成功，并返回清理后的绝对路径（去掉末尾分隔符）。
	dir := t.TempDir()
	got, err := prepareInstallDir(dir + string(filepath.Separator))
	if err != nil {
		t.Fatalf("合法目录应创建成功: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("返回值 = %q，want %q", got, filepath.Clean(dir))
	}
}

func TestIsVolumeRoot(t *testing.T) {
	if !isVolumeRoot("/") {
		t.Fatal("/ 应是卷根")
	}
	if isVolumeRoot(filepath.Join(t.TempDir(), "sub")) {
		t.Fatal("普通子目录不应是卷根")
	}
}

func TestCopyExecutableSuccessAndSkip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "QKA-src.exe")
	writeFile(t, src, []byte("program-bytes"), 0o700)
	dst := filepath.Join(dir, "install", ExeName)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(src, dst); err != nil {
		t.Fatalf("复制程序失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "program-bytes" {
		t.Fatalf("复制结果 = %q/%v", got, err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("安装后的程序应可执行，权限 = %v", info.Mode().Perm())
	}

	// 源与目标同一文件（从安装目录启动后的重装）：直接跳过，不复制。
	if err := copyExecutable(dst, dst); err != nil {
		t.Fatalf("同一路径应跳过复制: %v", err)
	}
}

func TestCopyExecutableFailures(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.exe")
	writeFile(t, src, []byte("data"), 0o600)
	dst := filepath.Join(dir, "out", ExeName)

	if err := copyExecutable(filepath.Join(dir, "missing.exe"), dst); err == nil || !strings.Contains(err.Error(), "读取当前程序") {
		t.Fatalf("源不存在应报读取失败，err=%v", err)
	}
	if err := copyExecutable(dir, dst); err == nil || !strings.Contains(err.Error(), "不是文件") {
		t.Fatalf("源是目录应被拒绝，err=%v", err)
	}

	oldOpen := openFile
	t.Cleanup(func() { openFile = oldOpen })
	openFile = func(string) (*os.File, error) { return nil, errors.New("denied") }
	if err := copyExecutable(src, dst); err == nil || !strings.Contains(err.Error(), "打开当前程序") {
		t.Fatalf("打开源失败应报错，err=%v", err)
	}
	openFile = oldOpen

	if err := copyExecutable(src, filepath.Join(dir, "missing-dir", ExeName)); err == nil || !strings.Contains(err.Error(), "临时文件") {
		t.Fatalf("安装目录不存在应报建临时文件失败，err=%v", err)
	}

	oldCreate := createTempFile
	t.Cleanup(func() { createTempFile = oldCreate })

	failing := func(sink *fakeSink) {
		createTempFile = func(string, string) (copySink, error) { return sink, nil }
	}
	cases := []struct {
		name string
		sink *fakeSink
		want string
	}{
		{"写入失败", &fakeSink{name: filepath.Join(dir, "out", ".QKA-tmp"), writeErr: errors.New("full")}, "复制程序数据"},
		{"同步失败", &fakeSink{name: filepath.Join(dir, "out", ".QKA-tmp"), syncErr: errors.New("sync")}, "同步程序数据"},
		{"权限失败", &fakeSink{name: filepath.Join(dir, "out", ".QKA-tmp"), chmodErr: errors.New("chmod")}, "设置程序权限"},
		{"关闭失败", &fakeSink{name: filepath.Join(dir, "out", ".QKA-tmp"), closeErr: errors.New("close")}, "关闭程序临时文件"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(dir, "out"), 0o755); err != nil {
				t.Fatal(err)
			}
			failing(test.sink)
			err := copyExecutable(src, dst)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v，want 含 %q", err, test.want)
			}
		})
	}

	// rename 失败：目标是一个非空目录（Windows 上等价于「目标正在运行」）。
	createTempFile = oldCreate
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(src, blocked); err == nil || !strings.Contains(err.Error(), "无法写入") {
		t.Fatalf("rename 到目录应失败，err=%v", err)
	}
}

func TestWriteRecordEnsureDirFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, []byte("x"), 0o600)
	if err := writeRecord(file, Record{}); err == nil {
		t.Fatal("配置目录是文件时应写记录失败")
	}
}

func TestInstallSuccess(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	installDir := filepath.Join(root, "Programs", "Quota Keepalive")
	selfPath := filepath.Join(root, "download", "QKA-0.1.16-windows-amd64.exe")
	writeFile(t, selfPath, []byte("self-exe"), 0o700)

	oldRuntime, oldSelfInstall, oldNow := ensureRuntime, ensureSelfInstall, now
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall, now = oldRuntime, oldSelfInstall, oldNow })
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now = func() time.Time { return fixed }

	var runtimeConfigDir, runtimeVersion string
	ensureRuntime = func(configDir, version string) (runtimebundle.Paths, error) {
		runtimeConfigDir, runtimeVersion = configDir, version
		return runtimebundle.Paths{}, nil
	}
	var selfConfigDir, selfExe, selfVersion string
	ensureSelfInstall = func(configDir, exe, version string) error {
		selfConfigDir, selfExe, selfVersion = configDir, exe, version
		return nil
	}

	var steps []Step
	var details []string
	result, err := Install(context.Background(), Options{
		ConfigDir:  configDir,
		Version:    "0.1.16",
		InstallDir: installDir,
		SelfPath:   selfPath,
		Progress: func(step Step, detail string) {
			steps = append(steps, step)
			details = append(details, detail)
		},
	})
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	// StepCopy 出现两次：先「请旧版本退出」，再「替换程序」。
	wantSteps := []Step{StepCheck, StepCopy, StepRuntime, StepShortcut, StepFinish}
	if len(steps) != len(wantSteps) {
		t.Fatalf("进度步骤 = %v，want %v", steps, wantSteps)
	}
	for i := range wantSteps {
		if steps[i] != wantSteps[i] {
			t.Fatalf("进度步骤 = %v，want %v", steps, wantSteps)
		}
		if details[i] == "" {
			t.Fatalf("步骤 %v 缺少状态文字", steps[i])
		}
	}
	if runtimeConfigDir != configDir || runtimeVersion != "0.1.16" {
		t.Fatalf("解压运行时参数 = %q/%q", runtimeConfigDir, runtimeVersion)
	}
	wantExe := filepath.Join(installDir, ExeName)
	if selfConfigDir != configDir || selfExe != wantExe || selfVersion != "0.1.16" {
		t.Fatalf("自安装参数 = %q/%q/%q", selfConfigDir, selfExe, selfVersion)
	}
	if result.ConfigDir != configDir || result.InstallDir != installDir || result.InstalledExe != wantExe {
		t.Fatalf("Result = %+v", result)
	}
	if result.LaunchArgs != nil {
		t.Fatalf("LaunchArgs = %v，want nil", result.LaunchArgs)
	}
	if data, err := os.ReadFile(wantExe); err != nil || string(data) != "self-exe" {
		t.Fatalf("安装目录里的程序 = %q/%v", data, err)
	}

	record, ok := Load(configDir)
	if !ok {
		t.Fatal("安装成功后应能读出 install.json")
	}
	if record.Schema != SchemaVersion || record.Version != "0.1.16" ||
		record.InstallDir != installDir || record.ExePath != wantExe {
		t.Fatalf("install.json = %+v", record)
	}
	if record.InstalledAt != fixed.Format(time.RFC3339) {
		t.Fatalf("installed_at = %q，want %q", record.InstalledAt, fixed.Format(time.RFC3339))
	}
	if NeedsInstall(configDir) {
		t.Fatal("安装成功后再跑安装向导应视为已安装")
	}
}

func TestInstallFailureLeavesRetryableState(t *testing.T) {
	newSetup := func(t *testing.T) (Options, string, string) {
		t.Helper()
		root := t.TempDir()
		configDir := filepath.Join(root, "config")
		installDir := filepath.Join(root, "install")
		selfPath := filepath.Join(root, "self.exe")
		writeFile(t, selfPath, []byte("self-exe"), 0o700)
		return Options{ConfigDir: configDir, Version: "0.1.16", InstallDir: installDir, SelfPath: selfPath},
			configDir, installDir
	}

	oldRuntime, oldSelfInstall, oldWrite, oldNow := ensureRuntime, ensureSelfInstall, writeStateFile, now
	t.Cleanup(func() {
		ensureRuntime, ensureSelfInstall, writeStateFile, now = oldRuntime, oldSelfInstall, oldWrite, oldNow
	})
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }
	now = time.Now

	t.Run("运行时失败", func(t *testing.T) {
		opts, configDir, _ := newSetup(t)
		ensureRuntime = func(string, string) (runtimebundle.Paths, error) {
			return runtimebundle.Paths{}, errors.New("bad zip")
		}
		assertInstallFails(t, opts, "解压运行时失败", configDir)
	})

	// 构建没内嵌运行时（漏了 embeddedruntime 标签）是最常见的一类失败：
	// 错误消息必须带可执行的修法，而不是只有一句技术描述。
	t.Run("未内嵌运行时应给出修法", func(t *testing.T) {
		opts, configDir, _ := newSetup(t)
		ensureRuntime = func(string, string) (runtimebundle.Paths, error) {
			return runtimebundle.Paths{}, runtimebundle.ErrUnavailable
		}
		_, err := Install(context.Background(), opts)
		if err == nil {
			t.Fatal("未内嵌运行时必须失败")
		}
		for _, want := range []string{"解压运行时失败", "没有内嵌运行时", "build-windows"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误消息缺少 %q: %v", want, err)
			}
		}
		if record, ok := Load(configDir); ok {
			t.Fatalf("失败后不应写安装记录: %+v", record)
		}
	})

	t.Run("快捷方式失败", func(t *testing.T) {
		opts, configDir, _ := newSetup(t)
		ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
		ensureSelfInstall = func(string, string, string) error { return errors.New("no menu") }
		assertInstallFails(t, opts, "创建快捷方式失败", configDir)
	})

	t.Run("写记录失败", func(t *testing.T) {
		opts, configDir, _ := newSetup(t)
		ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
		ensureSelfInstall = func(string, string, string) error { return nil }
		writeStateFile = func(string, []byte) error { return errors.New("disk full") }
		assertInstallFails(t, opts, "写入安装记录失败", configDir)
		// 记录写失败后重试（写恢复正常）应当成功。
		writeStateFile = func(path string, data []byte) error { return oldWrite(path, data) }
		if _, err := Install(context.Background(), opts); err != nil {
			t.Fatalf("重试应成功: %v", err)
		}
		if NeedsInstall(configDir) {
			t.Fatal("重试成功后应视为已安装")
		}
	})
}

func assertInstallFails(t *testing.T, opts Options, want string, configDir string) {
	t.Helper()
	var failedDetail string
	opts.Progress = func(step Step, detail string) {
		if step == StepFailed {
			failedDetail = detail
		}
	}
	_, err := Install(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v，want 含 %q", err, want)
	}
	if !strings.Contains(failedDetail, want) {
		t.Fatalf("失败进度 detail = %q，want 含 %q", failedDetail, want)
	}
	if !NeedsInstall(configDir) {
		t.Fatal("安装失败不应写 install.json")
	}
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".state-") || strings.HasSuffix(entry.Name(), ".tmp") {
				t.Fatalf("失败后不应残留临时文件: %s", entry.Name())
			}
		}
	}
}

func TestInstallInputFailures(t *testing.T) {
	root := t.TempDir()
	selfPath := filepath.Join(root, "self.exe")
	writeFile(t, selfPath, []byte("x"), 0o700)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Install(cancelled, Options{ConfigDir: filepath.Join(root, "c"), InstallDir: filepath.Join(root, "i"), SelfPath: selfPath}); err == nil ||
		!strings.Contains(err.Error(), "安装已取消") {
		t.Fatalf("取消的 ctx 应失败，err=%v", err)
	}

	if _, err := Install(context.Background(), Options{ConfigDir: filepath.Join(root, "c"), InstallDir: filepath.Join(root, "i")}); err == nil ||
		!strings.Contains(err.Error(), "当前程序路径") {
		t.Fatalf("SelfPath 为空应失败，err=%v", err)
	}

	if _, err := Install(context.Background(), Options{ConfigDir: filepath.Join(root, "c"), SelfPath: selfPath}); err == nil ||
		!strings.Contains(err.Error(), "安装位置不能为空") {
		t.Fatalf("InstallDir 为空应失败，err=%v", err)
	}

	// 复制失败：SelfPath 指向不存在的文件。
	if _, err := Install(context.Background(), Options{
		ConfigDir:  filepath.Join(root, "c"),
		InstallDir: filepath.Join(root, "i"),
		SelfPath:   filepath.Join(root, "missing.exe"),
	}); err == nil || !strings.Contains(err.Error(), "复制程序失败") {
		t.Fatalf("复制失败应报错，err=%v", err)
	}
}

func TestInstallWithoutProgressCallback(t *testing.T) {
	root := t.TempDir()
	selfPath := filepath.Join(root, "self.exe")
	writeFile(t, selfPath, []byte("x"), 0o700)

	oldRuntime, oldSelfInstall := ensureRuntime, ensureSelfInstall
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall = oldRuntime, oldSelfInstall })
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }

	if _, err := Install(context.Background(), Options{
		ConfigDir:  filepath.Join(root, "c"),
		InstallDir: filepath.Join(root, "i"),
		SelfPath:   selfPath,
	}); err != nil {
		t.Fatalf("Progress 为 nil 时安装也应成功: %v", err)
	}
}

func TestPlatformSeamsAreRuntimeDefaults(t *testing.T) {
	// 默认接缝必须指向真实实现：lookupEnv 是可用的取环境变量实现。
	if runtime.GOOS == "" {
		t.Fatal("runtime.GOOS 不应为空")
	}
	if os.Getenv("QKA_INSTALLER_TEST_ENV") == "" {
		t.Setenv("QKA_INSTALLER_TEST_ENV", "1")
		if got := lookupEnv("QKA_INSTALLER_TEST_ENV"); got != "1" {
			t.Fatalf("lookupEnv 默认实现应读环境变量，实际 %q", got)
		}
	}
}

// seedRecord 把一条安装记录写进配置目录（绕过 install.json 的写入接缝，直接
// 造出「上次安装过」的状态）。
func seedRecord(t *testing.T, configDir string, record Record) {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(configDir, stateName), data, 0o600)
}

func TestModeString(t *testing.T) {
	cases := map[Mode]string{
		ModeNone:    "none",
		ModeInstall: "install",
		ModeUpgrade: "upgrade",
		Mode(99):    "none",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("Mode(%d).String() = %q，want %q", mode, got, want)
		}
	}
}

func TestPlan(t *testing.T) {
	// 记录缺失分支现在会探测真机残留安装；固定接缝保证测试不受
	// 运行测试的机器是否恰好装过 QKA 影响。
	withDetect(t, func() string { return "" })

	configDir := t.TempDir()
	installedExe := filepath.Join(configDir, "Programs", "Quota Keepalive", ExeName)
	record := Record{
		Schema:      SchemaVersion,
		Version:     "0.1.15",
		InstallDir:  filepath.Dir(installedExe),
		ExePath:     installedExe,
		InstalledAt: "2026-01-01T00:00:00Z",
	}

	if got := Plan(configDir, filepath.Join(configDir, "Downloads", "QKA-0.1.16.exe")); got != ModeInstall {
		t.Fatalf("没有安装记录时 Plan = %v，want install", got)
	}
	if got := Plan("  ", installedExe); got != ModeInstall {
		t.Fatalf("空配置目录时 Plan = %v，want install", got)
	}

	// 记录缺失但探测到残留安装（用户手删配置目录后重跑）→ 升级/修复，
	// 而不是弹全新安装向导让用户装出第二份。
	detectedExe := filepath.Join(t.TempDir(), "Quota Keepalive", ExeName)
	withDetect(t, func() string { return detectedExe })
	if got := Plan(configDir, filepath.Join(configDir, "Downloads", "QKA-0.1.16.exe")); got != ModeUpgrade {
		t.Fatalf("记录缺失但探测到残留安装时 Plan = %v，want upgrade", got)
	}
	// 但若探测到的就是正在运行的这份（用户双击的就是已安装副本，只是记录丢了），
	// 必须直接运行：否则每次启动都弹一次升级向导。
	if got := Plan(configDir, detectedExe); got != ModeNone {
		t.Fatalf("记录丢失但运行的正是已安装副本时 Plan = %v，want none", got)
	}
	if got := Plan(configDir, strings.ToUpper(detectedExe)); got != ModeNone {
		t.Fatalf("同一个 exe 的大小写差异也应判 none，Plan = %v", got)
	}
	withDetect(t, func() string { return "" })

	seedRecord(t, configDir, record)

	if got := Plan(configDir, installedExe); got != ModeNone {
		t.Fatalf("当前就是已安装的 exe 时 Plan = %v，want none", got)
	}
	// 路径大小写/分隔符差异不应被当成新副本（Windows 路径大小写不敏感）。
	if got := Plan(configDir, strings.ToUpper(installedExe)); got != ModeNone {
		t.Fatalf("同一路径的大小写差异应视为已安装，Plan = %v", got)
	}
	if got := Plan(configDir, filepath.Join(configDir, "Downloads", "QKA-0.1.16.exe")); got != ModeUpgrade {
		t.Fatalf("下载目录里的新 exe 应触发升级，Plan = %v", got)
	}
	if got := Plan(configDir, "  "); got != ModeUpgrade {
		t.Fatalf("已有记录但拿不到当前 exe 路径时按升级处理，Plan = %v", got)
	}

	// 记录损坏、探测也未命中 → 当作没装过。
	writeFile(t, filepath.Join(configDir, stateName), []byte("{broken"), 0o600)
	if got := Plan(configDir, installedExe); got != ModeInstall {
		t.Fatalf("损坏记录时 Plan = %v，want install", got)
	}
	// 记录损坏但探测到残留安装 → 修复，而不是全新安装。
	withDetect(t, func() string { return detectedExe })
	if got := Plan(configDir, installedExe); got != ModeUpgrade {
		t.Fatalf("损坏记录但探测到残留安装时 Plan = %v，want upgrade", got)
	}
	withDetect(t, func() string { return "" })
}

// upgradeSetup 造出「已安装旧版本 + 下载目录里有新版本」的现场。
func upgradeSetup(t *testing.T) (opts Options, configDir, installedExe string) {
	t.Helper()
	root := t.TempDir()
	configDir = filepath.Join(root, "config")
	installDir := filepath.Join(root, "Programs", "Quota Keepalive")
	installedExe = filepath.Join(installDir, ExeName)
	writeFile(t, installedExe, []byte("old-version-binary"), 0o700)
	seedRecord(t, configDir, Record{
		Schema:      SchemaVersion,
		Version:     "0.1.15",
		InstallDir:  installDir,
		ExePath:     installedExe,
		InstalledAt: "2026-01-01T00:00:00Z",
	})
	selfPath := filepath.Join(root, "Downloads", "QKA-0.1.16-windows-amd64.exe")
	writeFile(t, selfPath, []byte("new-version-binary"), 0o700)
	return Options{
		ConfigDir:  configDir,
		Version:    "0.1.16",
		InstallDir: filepath.Join(root, "ignored"),
		SelfPath:   selfPath,
	}, configDir, installedExe
}

func TestUpgradeSuccess(t *testing.T) {
	opts, configDir, installedExe := upgradeSetup(t)

	oldRuntime, oldSelfInstall, oldNow := ensureRuntime, ensureSelfInstall, now
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall, now = oldRuntime, oldSelfInstall, oldNow })
	fixed := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	now = func() time.Time { return fixed }
	var runtimeVersion, selfExe, selfVersion string
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(_, exe, version string) error {
		selfExe, selfVersion = exe, version
		return nil
	}

	var steps []Step
	result, err := Upgrade(context.Background(), Options{
		ConfigDir:  opts.ConfigDir,
		Version:    opts.Version,
		InstallDir: opts.InstallDir, // 升级模式忽略页面传来的目录
		SelfPath:   opts.SelfPath,
		Progress: func(step Step, detail string) {
			steps = append(steps, step)
			if detail == "" {
				t.Fatalf("步骤 %v 缺少状态文字", step)
			}
		},
	})
	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	_ = runtimeVersion

	// StepCopy 出现两次：先「请旧版本退出」，再「替换程序」。
	wantSteps := []Step{StepCheck, StepCopy, StepCopy, StepRuntime, StepShortcut, StepFinish}
	if len(steps) != len(wantSteps) {
		t.Fatalf("进度步骤 = %v，want %v", steps, wantSteps)
	}
	for i := range wantSteps {
		if steps[i] != wantSteps[i] {
			t.Fatalf("进度步骤 = %v，want %v", steps, wantSteps)
		}
	}

	if data, err := os.ReadFile(installedExe); err != nil || string(data) != "new-version-binary" {
		t.Fatalf("安装目录里的程序 = %q/%v，应已被新 exe 替换", data, err)
	}
	if result.ConfigDir != opts.ConfigDir || result.InstallDir != filepath.Dir(installedExe) || result.InstalledExe != installedExe {
		t.Fatalf("Result = %+v", result)
	}
	if selfExe != installedExe || selfVersion != "0.1.16" {
		t.Fatalf("快捷方式刷新参数 = %q/%q", selfExe, selfVersion)
	}

	record, ok := Load(configDir)
	if !ok {
		t.Fatal("升级后应能读出 install.json")
	}
	if record.Version != "0.1.16" || record.ExePath != installedExe || record.InstallDir != filepath.Dir(installedExe) {
		t.Fatalf("升级后的记录 = %+v", record)
	}
	if record.InstalledAt != fixed.Format(time.RFC3339) {
		t.Fatalf("installed_at = %q", record.InstalledAt)
	}
	if Plan(configDir, installedExe) != ModeNone {
		t.Fatal("升级完成后，已安装的 exe 启动应直接进托盘")
	}
	if Plan(configDir, opts.SelfPath) != ModeUpgrade {
		t.Fatal("升级完成后，下载目录的 exe 再次启动仍应提示升级（避免跑成第二个应用）")
	}
}

func TestUpgradeInPlaceWhenCurrentExeIsInstalled(t *testing.T) {
	_, configDir, installedExe := upgradeSetup(t)
	oldRuntime, oldSelfInstall := ensureRuntime, ensureSelfInstall
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall = oldRuntime, oldSelfInstall })
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }

	// 当前 exe 不是下载副本，而是安装目录里的那份（记录丢失/改坏后的原地重装）。
	if _, err := Upgrade(context.Background(), Options{
		ConfigDir: configDir,
		Version:   "0.1.16",
		SelfPath:  installedExe,
	}); err != nil {
		t.Fatalf("原地刷新应成功: %v", err)
	}
	if data, err := os.ReadFile(installedExe); err != nil || string(data) != "old-version-binary" {
		t.Fatalf("原地刷新不应复制自己：%q/%v", data, err)
	}
	record, _ := Load(configDir)
	if record.Version != "0.1.16" {
		t.Fatalf("版本未更新: %+v", record)
	}
}

func TestUpgradeRebuildsMissingInstallDir(t *testing.T) {
	opts, configDir, installedExe := upgradeSetup(t)
	// 模拟用户手动删掉安装目录：记录还在，程序没了。
	if err := os.RemoveAll(filepath.Dir(installedExe)); err != nil {
		t.Fatal(err)
	}
	oldRuntime, oldSelfInstall := ensureRuntime, ensureSelfInstall
	t.Cleanup(func() { ensureRuntime, ensureSelfInstall = oldRuntime, oldSelfInstall })
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }

	if _, err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("安装目录被删后升级应能重建: %v", err)
	}
	if data, err := os.ReadFile(installedExe); err != nil || string(data) != "new-version-binary" {
		t.Fatalf("重建后的程序 = %q/%v", data, err)
	}
	if _, ok := Load(configDir); !ok {
		t.Fatal("重建后应写入安装记录")
	}
}

func TestCheckReplaceable(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "QKA.exe")
	if err := checkReplaceable(missing); err != nil {
		t.Fatalf("程序不存在时应可直接替换: %v", err)
	}
	file := filepath.Join(dir, "file.exe")
	writeFile(t, file, []byte("x"), 0o700)
	if err := checkReplaceable(file); err != nil {
		t.Fatalf("普通文件应可替换: %v", err)
	}
	if err := checkReplaceable(dir); err == nil || !strings.Contains(err.Error(), "无法替换已安装的程序") {
		t.Fatalf("目录/被占用路径应报不能替换，err=%v", err)
	}
	// 父级是文件（ENOTDIR）：Stat 失败后仍要走「不能替换」的错误分支。
	if err := checkReplaceable(filepath.Join(file, "QKA.exe")); err == nil || !strings.Contains(err.Error(), "无法替换已安装的程序") {
		t.Fatalf("ENOTDIR 路径应报不能替换，err=%v", err)
	}
}

func TestUpgradeFailures(t *testing.T) {
	oldRuntime, oldSelfInstall, oldWrite, oldNow := ensureRuntime, ensureSelfInstall, writeStateFile, now
	t.Cleanup(func() {
		ensureRuntime, ensureSelfInstall, writeStateFile, now = oldRuntime, oldSelfInstall, oldWrite, oldNow
	})
	ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
	ensureSelfInstall = func(string, string, string) error { return nil }
	now = time.Now

	t.Run("没有安装记录", func(t *testing.T) {
		// 记录缺失时 Upgrade 会兜底探测；固定为未命中，隔离真机状态。
		withDetect(t, func() string { return "" })
		root := t.TempDir()
		selfPath := filepath.Join(root, "new.exe")
		writeFile(t, selfPath, []byte("x"), 0o700)
		_, err := Upgrade(context.Background(), Options{ConfigDir: filepath.Join(root, "config"), Version: "0.1.16", SelfPath: selfPath})
		if err == nil || !strings.Contains(err.Error(), "没有找到已安装版本的记录") {
			t.Fatalf("没有记录应报错，err=%v", err)
		}
	})

	t.Run("当前程序路径为空", func(t *testing.T) {
		_, configDir, _ := upgradeSetup(t)
		_, err := Upgrade(context.Background(), Options{ConfigDir: configDir, Version: "0.1.16"})
		if err == nil || !strings.Contains(err.Error(), "无法定位当前程序路径") {
			t.Fatalf("SelfPath 为空应报错，err=%v", err)
		}
	})

	t.Run("上下文取消", func(t *testing.T) {
		opts, _, _ := upgradeSetup(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Upgrade(ctx, opts)
		if err == nil || !strings.Contains(err.Error(), "升级已取消") {
			t.Fatalf("取消的 ctx 应报错，err=%v", err)
		}
	})

	t.Run("安装目录不合法", func(t *testing.T) {
		root := t.TempDir()
		configDir := filepath.Join(root, "config")
		seedRecord(t, configDir, Record{
			Schema: SchemaVersion, Version: "0.1.15",
			InstallDir: string(filepath.Separator), ExePath: filepath.Join(string(filepath.Separator), ExeName),
		})
		selfPath := filepath.Join(root, "new.exe")
		writeFile(t, selfPath, []byte("x"), 0o700)
		_, err := Upgrade(context.Background(), Options{ConfigDir: configDir, Version: "0.1.16", SelfPath: selfPath})
		if err == nil || !strings.Contains(err.Error(), "根目录") {
			t.Fatalf("卷根应被拒绝，err=%v", err)
		}
	})

	t.Run("旧版本仍在运行", func(t *testing.T) {
		opts, _, installedExe := upgradeSetup(t)
		if err := os.Remove(installedExe); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(installedExe, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Upgrade(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "无法替换已安装的程序") {
			t.Fatalf("旧版被占用应提示先退出，err=%v", err)
		}
	})

	t.Run("替换程序失败", func(t *testing.T) {
		opts, configDir, _ := upgradeSetup(t)
		opts.SelfPath = filepath.Join(t.TempDir(), "missing.exe")
		_, err := Upgrade(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "替换程序失败") {
			t.Fatalf("复制失败应报错，err=%v", err)
		}
		record, _ := Load(configDir)
		if record.Version != "0.1.15" {
			t.Fatalf("失败后记录不应升级: %+v", record)
		}
	})

	t.Run("解压运行时失败", func(t *testing.T) {
		opts, configDir, _ := upgradeSetup(t)
		ensureRuntime = func(string, string) (runtimebundle.Paths, error) {
			return runtimebundle.Paths{}, errors.New("bad bundle")
		}
		t.Cleanup(func() {
			ensureRuntime = func(string, string) (runtimebundle.Paths, error) { return runtimebundle.Paths{}, nil }
		})
		_, err := Upgrade(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "解压运行时失败") {
			t.Fatalf("运行时失败应报错，err=%v", err)
		}
		record, _ := Load(configDir)
		if record.Version != "0.1.15" {
			t.Fatalf("失败后记录不应升级: %+v", record)
		}
	})

	t.Run("刷新快捷方式失败", func(t *testing.T) {
		opts, configDir, _ := upgradeSetup(t)
		ensureSelfInstall = func(string, string, string) error { return errors.New("no menu") }
		t.Cleanup(func() { ensureSelfInstall = func(string, string, string) error { return nil } })
		_, err := Upgrade(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "刷新快捷方式失败") {
			t.Fatalf("快捷方式失败应报错，err=%v", err)
		}
		record, _ := Load(configDir)
		if record.Version != "0.1.15" {
			t.Fatalf("失败后记录不应升级: %+v", record)
		}
	})

	t.Run("更新记录失败", func(t *testing.T) {
		opts, configDir, _ := upgradeSetup(t)
		writeStateFile = func(string, []byte) error { return errors.New("disk full") }
		t.Cleanup(func() { writeStateFile = oldWrite })
		_, err := Upgrade(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "更新安装记录失败") {
			t.Fatalf("写记录失败应报错，err=%v", err)
		}
		record, _ := Load(configDir)
		if record.Version != "0.1.15" {
			t.Fatalf("失败后记录不应升级: %+v", record)
		}
	})
}
