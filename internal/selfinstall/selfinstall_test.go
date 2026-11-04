package selfinstall

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystem 是注入的假操作系统。开始菜单是临时目录里的一个文件夹；
// writeShortcut 模拟真实实现的语义：已经指向 exe 就不写；App Paths 保存在
// 内存里。测试用它验证状态机与调用序列，不需要真的 Windows。
type fakeSystem struct {
	menuDir     string
	menuErr     error
	shortcutErr error

	repairs int      // 真正发生写入的快捷方式修复次数
	seen    []string // writeShortcut 收到的每个 exe（含未写入的检查）

	appExe, appDir string
	appExists      bool
	registered     []string
	registerErr    error
}

func newFake(t *testing.T) (*fakeSystem, system) {
	t.Helper()
	f := &fakeSystem{menuDir: filepath.Join(t.TempDir(), "Start Menu", "Programs")}
	return f, f.system()
}

func (f *fakeSystem) system() system {
	return system{
		startMenuDir: func() (string, error) { return f.menuDir, f.menuErr },
		writeShortcut: func(lnk, exe string) error {
			if f.shortcutErr != nil {
				return f.shortcutErr
			}
			f.seen = append(f.seen, exe)
			if data, err := os.ReadFile(lnk); err == nil && string(data) == exe {
				return nil // 已指向 exe：真实实现不做写操作
			}
			if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
				return err
			}
			f.repairs++
			return os.WriteFile(lnk, []byte(exe), 0o644)
		},
		appPath: func() (string, string, bool) { return f.appExe, f.appDir, f.appExists },
		registerAppPath: func(exe string) error {
			if f.registerErr != nil {
				return f.registerErr
			}
			f.registered = append(f.registered, exe)
			f.appExe, f.appDir, f.appExists = exe, filepath.Dir(exe), true
			return nil
		},
	}
}

func (f *fakeSystem) lnk() string { return filepath.Join(f.menuDir, shortcutName+".lnk") }

// config 返回一个新的配置目录（尚不存在）与一个 exe 路径。
func config(t *testing.T) (configDir, exe string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, "config"), filepath.Join(root, "bin", "QKA.exe")
}

func readState(t *testing.T, configDir string) state {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(configDir, stateName))
	if err != nil {
		t.Fatalf("读取状态文件: %v", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("解析状态文件 %q: %v", data, err)
	}
	return st
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(data)
}

// 第一次双击必须真的把快捷方式和 App Paths 建出来，并落状态文件。
func TestFirstRunInstalls(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)

	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.lnk()); got != exe {
		t.Errorf("快捷方式指向 %q，期望 %q", got, exe)
	}
	if len(f.registered) != 1 || f.registered[0] != exe {
		t.Errorf("App Paths 注册 %v，期望 [%s]", f.registered, exe)
	}
	st := readState(t, configDir)
	if st.Schema != schemaVersion || st.ExePath != exe ||
		st.Version != "1.2.3" || !st.ShortcutCreated {
		t.Errorf("状态文件内容 = %+v", st)
	}
	// 首次安装后记录快捷方式指纹，供后续启动跳过 COM 校验。
	if st.ShortcutModNS == 0 || st.ShortcutSize == 0 {
		t.Errorf("状态文件缺少快捷方式指纹: %+v", st)
	}
	if perm := filePerm(t, filepath.Join(configDir, stateName)); perm != 0o600 {
		t.Errorf("状态文件权限 = %#o，期望 0600", perm)
	}
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// 重复 Ensure 不产生副作用：不重写状态文件、不重复注册 App Paths、
// 不重写已经指向 exe 的快捷方式。
func TestSecondRunIsIdempotent(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	stateBefore := readFile(t, filepath.Join(configDir, stateName))

	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if f.repairs != 1 {
		t.Errorf("快捷方式被重写 %d 次，期望只写首次那 1 次", f.repairs)
	}
	if len(f.registered) != 1 {
		t.Errorf("App Paths 注册 %d 次，期望 1 次", len(f.registered))
	}
	if got := readFile(t, filepath.Join(configDir, stateName)); got != stateBefore {
		t.Errorf("状态文件被重写:\nbefore=%q\nafter =%q", stateBefore, got)
	}
	// 第二次启动时快捷方式指纹未变：应跳过 COM 校验，只做状态比对。
	if len(f.seen) != 1 {
		t.Errorf("快捷方式检查次数 = %d，期望 1（第二次跳过 COM 校验）", len(f.seen))
	}
}

// 快捷方式被外部改动（mtime 变化）后：必须重新走校验，不能一直跳过。
func TestChangedShortcutIsRechecked(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	// 模拟其它程序重写快捷方式：内容与 mtime 都变。
	if err := os.WriteFile(f.lnk(), []byte(exe+"-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if len(f.seen) != 2 {
		t.Errorf("快捷方式检查次数 = %d，期望 2（改动后重新校验）", len(f.seen))
	}
	if got := readFile(t, f.lnk()); got != exe {
		t.Errorf("快捷方式应被指回 %q，实际 %q", exe, got)
	}
}

// 旧版状态文件没有指纹记录：必须校验一次并补上，之后才能走快路径。
func TestLegacyStateIsValidatedOnce(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	// 去掉指纹，模拟旧版本写下的状态文件。
	st := readState(t, configDir)
	st.ShortcutModNS, st.ShortcutSize = 0, 0
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, stateName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if len(f.seen) != 2 {
		t.Errorf("旧状态应触发一次校验，检查次数 = %d，期望 2", len(f.seen))
	}
	if st := readState(t, configDir); st.ShortcutModNS == 0 || st.ShortcutSize == 0 {
		t.Errorf("校验后应补上指纹: %+v", st)
	}
}

// exe 被移动：快捷方式、App Paths 与状态文件都指回新位置。
func TestMovedExeIsRepointed(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "Downloads", "QKA.exe")

	if err := ensure(configDir, moved, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.lnk()); got != moved {
		t.Errorf("快捷方式指向 %q，期望 %q", got, moved)
	}
	if len(f.registered) != 2 || f.registered[1] != moved {
		t.Errorf("App Paths 注册 %v，期望最后一次是 %s", f.registered, moved)
	}
	if st := readState(t, configDir); st.ExePath != moved {
		t.Errorf("状态文件 exe_path = %q，期望 %q", st.ExePath, moved)
	}
	if f.repairs != 2 {
		t.Errorf("快捷方式被写 %d 次，期望 2（首次 + 移动后）", f.repairs)
	}
}

// #508：用户删掉我们建过的快捷方式后，不再重建——重复启动也一样。
func TestDeletedShortcutStaysDeleted(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.lnk()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
			t.Fatal(err)
		}
	}
	if fileExists(f.lnk()) {
		t.Fatal("被用户删掉的快捷方式又回来了")
	}
	if f.repairs != 1 {
		t.Errorf("快捷方式被写 %d 次，期望只有首次 1 次", f.repairs)
	}
	if st := readState(t, configDir); !st.ShortcutCreated {
		t.Error("状态文件应保持 shortcut_created=true")
	}
}

// 用户删掉快捷方式后 exe 又被移动：快捷方式仍保持删除，但 App Paths 与
// 状态文件要跟上新位置。
func TestDeletedShortcutStaysDeletedAfterMove(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.lnk()); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "Moved", "QKA.exe")
	if err := ensure(configDir, moved, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if fileExists(f.lnk()) {
		t.Fatal("被用户删掉的快捷方式又回来了")
	}
	if st := readState(t, configDir); st.ExePath != moved {
		t.Errorf("状态文件 exe_path = %q，期望 %q", st.ExePath, moved)
	}
	if len(f.registered) != 2 || f.registered[1] != moved {
		t.Errorf("App Paths 注册 %v，期望最后一次是 %s", f.registered, moved)
	}
}

// 损坏的状态文件不能让自安装永远失败：按「没装过」处理并重建。
func TestCorruptStateFileIsRebuilt(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt JSON":    `{"schema":1,`,
		"schema mismatch": `{"schema":99,"shortcut_created":true}`,
		"empty":           ``,
	} {
		t.Run(name, func(t *testing.T) {
			f, sys := newFake(t)
			configDir, exe := config(t)
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(configDir, stateName)
			if err := os.WriteFile(statePath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
				t.Fatalf("损坏状态文件应被重建，实际 %v", err)
			}
			if !fileExists(f.lnk()) {
				t.Fatal("状态文件损坏时应重新建立快捷方式")
			}
			if st := readState(t, configDir); st.Schema != schemaVersion || !st.ShortcutCreated {
				t.Errorf("状态文件未重建: %+v", st)
			}
		})
	}
}

// 状态文件读不了（例如同名的目录）返回错误，不 panic。
func TestStateReadErrorIsReturned(t *testing.T) {
	_, sys := newFake(t)
	configDir, exe := config(t)
	if err := os.MkdirAll(filepath.Join(configDir, stateName), 0o700); err != nil {
		t.Fatal(err)
	}
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "读取状态文件") {
		t.Fatalf("期望读取状态文件失败，实际 %v", err)
	}
}

// 配置目录不存在（真正的首次运行）：Ensure 自己建出来。
func TestConfigDirIsCreated(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(configDir, stateName)) {
		t.Fatal("首次运行应在新建的配置目录里写状态文件")
	}
	if !fileExists(f.lnk()) {
		t.Fatal("首次运行应建立快捷方式")
	}
}

// 配置目录建不出来时返回错误。
func TestConfigDirCreationFailure(t *testing.T) {
	_, sys := newFake(t)
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ensure(filepath.Join(blocker, "config"), filepath.Join(root, "QKA.exe"), "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "创建配置目录") {
		t.Fatalf("期望创建配置目录失败，实际 %v", err)
	}
}

// 非 Windows 的 no-op 路径：没有开始菜单时 Ensure 成功返回且不落状态文件。
func TestNoStartMenuIsNoOp(t *testing.T) {
	f, sys := newFake(t)
	f.menuErr = errNoStartMenu
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(configDir, stateName)) {
		t.Error("没有开始菜单时不该写状态文件")
	}
	if len(f.registered) != 0 || f.repairs != 0 {
		t.Error("没有开始菜单时不该注册 App Paths 或写快捷方式")
	}
}

// 定位开始菜单失败（非「没有开始菜单」）要如实返回错误。
func TestStartMenuErrorIsReturned(t *testing.T) {
	f, sys := newFake(t)
	f.menuErr = errors.New("controlled start menu failure")
	configDir, exe := config(t)
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "定位开始菜单") {
		t.Fatalf("期望定位开始菜单失败，实际 %v", err)
	}
}

// 创建快捷方式失败：返回错误，且不写状态文件（下次启动重试）。
func TestShortcutCreationFailure(t *testing.T) {
	f, sys := newFake(t)
	f.shortcutErr = errors.New("controlled shortcut failure")
	configDir, exe := config(t)
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "创建开始菜单快捷方式") {
		t.Fatalf("期望创建快捷方式失败，实际 %v", err)
	}
	if fileExists(filepath.Join(configDir, stateName)) {
		t.Error("快捷方式失败时不该记录已安装")
	}
}

// 修复已有快捷方式失败：返回错误。
func TestShortcutRepairFailure(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	// 先让快捷方式发生变化（mtime/内容都变），确保本轮真的走修复路径；
	// 指纹未变时 ensure 会跳过 COM 校验（见 TestSecondRunIsIdempotent）。
	if err := os.WriteFile(f.lnk(), []byte(exe+"-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.shortcutErr = errors.New("controlled repair failure")
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "修复开始菜单快捷方式") {
		t.Fatalf("期望修复快捷方式失败，实际 %v", err)
	}
}

// 开始菜单目录建不出来：返回错误。
func TestShortcutDirCreationFailure(t *testing.T) {
	f, sys := newFake(t)
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.menuDir = filepath.Join(blocker, "Programs")
	configDir, exe := config(t)
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "创建开始菜单目录") {
		t.Fatalf("期望创建开始菜单目录失败，实际 %v", err)
	}
}

// 注册 App Paths 失败：返回错误，且不建快捷方式。
func TestRegisterAppPathFailure(t *testing.T) {
	f, sys := newFake(t)
	f.registerErr = errors.New("controlled registry failure")
	configDir, exe := config(t)
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "注册 App Paths") {
		t.Fatalf("期望注册 App Paths 失败，实际 %v", err)
	}
	if f.repairs != 0 {
		t.Error("App Paths 失败后不该继续建快捷方式")
	}
}

// 写状态文件失败：返回错误，不 panic。
func TestStateWriteFailure(t *testing.T) {
	old := writeStateFile
	t.Cleanup(func() { writeStateFile = old })
	writeStateFile = func(string, []byte) error { return errors.New("controlled state failure") }

	f, sys := newFake(t)
	configDir, exe := config(t)
	err := ensure(configDir, exe, "1.2.3", sys)
	if err == nil || !strings.Contains(err.Error(), "写状态文件") {
		t.Fatalf("期望写状态文件失败，实际 %v", err)
	}
	if !fileExists(f.lnk()) {
		t.Error("状态写入失败不应回滚已经建好的快捷方式")
	}
}

// 空入参直接报错，不去碰系统。
func TestEmptyInputs(t *testing.T) {
	_, sys := newFake(t)
	if err := ensure("", "C:\\QKA.exe", "1", sys); err == nil {
		t.Error("空配置目录应报错")
	}
	if err := ensure(t.TempDir(), "  ", "1", sys); err == nil {
		t.Error("空 exe 路径应报错")
	}
}

// 快捷方式已经存在但状态文件丢了（例如用户清理过配置）：接管它并补状态，
// 不重复写内容相同的快捷方式。
func TestExistingShortcutWithoutStateIsAdopted(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := os.MkdirAll(f.menuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.lnk(), []byte(exe), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if f.repairs != 0 {
		t.Errorf("已指向 exe 的快捷方式被重写 %d 次", f.repairs)
	}
	if st := readState(t, configDir); !st.ShortcutCreated {
		t.Error("接管已有快捷方式后应记录 shortcut_created=true")
	}
}

// 版本变化只更新状态文件里的版本，不重写快捷方式、不重复注册 App Paths。
func TestVersionChangeOnlyUpdatesState(t *testing.T) {
	f, sys := newFake(t)
	configDir, exe := config(t)
	if err := ensure(configDir, exe, "1.2.3", sys); err != nil {
		t.Fatal(err)
	}
	if err := ensure(configDir, exe, "1.2.4", sys); err != nil {
		t.Fatal(err)
	}
	if f.repairs != 1 || len(f.registered) != 1 {
		t.Errorf("版本变化后 repair=%d registered=%d，期望 1/1", f.repairs, len(f.registered))
	}
	if st := readState(t, configDir); st.Version != "1.2.4" {
		t.Errorf("状态文件 version = %q，期望 1.2.4", st.Version)
	}
}

// portable 判定目前恒为 false（QKA 没有便携发行物），但接缝保留。
func TestPortableIsAlwaysFalseForNow(t *testing.T) {
	for _, exe := range []string{"", `C:\QKA.exe`, "/usr/local/bin/qka"} {
		if Portable(exe) {
			t.Errorf("Portable(%q) = true，QKA 没有便携发行物", exe)
		}
	}
}

func TestStale(t *testing.T) {
	exe := "/tools/QKA.exe"
	for _, c := range []struct {
		target, dir string
		stale       bool
	}{
		{"", "", true},                         // 还没有快捷方式
		{exe, "/tools", false},                 // 已经指向这个 exe
		{"/TOOLS/qka.EXE", "/Tools/", false},   // 大小写与末尾分隔符不同，仍是同一个
		{"/downloads/QKA.exe", "/tools", true}, // exe 被移动
		{exe, "/downloads", true},              // 工作目录变了
		{exe, "", true},                        // 没有工作目录
	} {
		if got := stale(c.target, c.dir, exe); got != c.stale {
			t.Errorf("stale(%q, %q) = %v，期望 %v", c.target, c.dir, got, c.stale)
		}
	}
}
