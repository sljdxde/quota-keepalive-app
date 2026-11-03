package runtimebundle

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/klauspost/compress/zstd"
)

// testEntry 描述一个待打进 tar 的条目。
type testEntry struct {
	name string
	body string
	mode int64
	typ  byte
}

func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawTar 把条目写成未压缩的 tar 字节，便于制造「tar 本身损坏」的资源。
func rawTar(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		typ := entry.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: entry.name, Mode: mode, Size: int64(len(entry.body)), Typeflag: typ}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarZstd(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	return zstdBytes(t, rawTar(t, entries))
}

// standardAssets 是「一切正常」的内嵌资源。
func standardAssets(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		AssetBun: &fstest.MapFile{Data: zstdBytes(t, []byte("bun-binary"))},
		AssetAdapters: &fstest.MapFile{Data: tarZstd(t, []testEntry{
			{name: "adapters/", typ: tar.TypeDir, mode: 0o755},
			{name: "adapters/qoder/", typ: tar.TypeDir, mode: 0o755},
			{name: "adapters/qoder/index.mjs", body: "export const x = 1;", mode: 0o644},
			{name: "adapters/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		})},
		AssetPlugins: &fstest.MapFile{Data: tarZstd(t, []testEntry{
			{name: "provider-plugins/", typ: tar.TypeDir, mode: 0o755},
			{name: "provider-plugins/airush/package.json", body: `{"name":"airush"}`, mode: 0o644},
		})},
	}
}

// withAsset 返回替换了单个资源的标准资源集。
func withAsset(t *testing.T, name string, data []byte) fstest.MapFS {
	t.Helper()
	assets := standardAssets(t)
	assets[name] = &fstest.MapFile{Data: data}
	return assets
}

func mustPaths(t *testing.T, base string) Paths {
	t.Helper()
	return Paths{
		Bun:          filepath.Join(base, bunExecutableName),
		AdaptersRoot: filepath.Join(base, AdaptersDirName),
		PluginsRoot:  filepath.Join(base, PluginsDirName),
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertNoStaging 断言 runtime/ 下没有残留的临时目录。
func assertNoStaging(t *testing.T, configDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(configDir, runtimeDirName))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			t.Fatalf("残留临时目录: %s", entry.Name())
		}
	}
}

func TestEnsureExtractsAndReusesReadyTree(t *testing.T) {
	configDir := t.TempDir()
	paths, err := ensureWith(configDir, "1.2.3", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(configDir, runtimeDirName, "1.2.3")
	if paths != mustPaths(t, base) {
		t.Fatalf("解压路径 = %+v，want %+v", paths, mustPaths(t, base))
	}
	if got := readFile(t, paths.Bun); got != "bun-binary" {
		t.Fatalf("bun 内容 = %q", got)
	}
	if got := readFile(t, filepath.Join(paths.AdaptersRoot, "qoder", "index.mjs")); got != "export const x = 1;" {
		t.Fatalf("adapters 内容 = %q", got)
	}
	if got := readFile(t, filepath.Join(paths.PluginsRoot, "airush", "package.json")); got != `{"name":"airush"}` {
		t.Fatalf("plugins 内容 = %q", got)
	}
	marker := readFile(t, filepath.Join(base, markerName))
	if marker != markerFormat+"\n1.2.3\n" {
		t.Fatalf("就绪标记 = %q", marker)
	}
	assertNoStaging(t, configDir)

	// 幂等：标记就绪后不再解压，手工写入的内容得以保留。
	if err := os.WriteFile(paths.Bun, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := ensureWith(configDir, "1.2.3", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	if again != paths {
		t.Fatalf("复用路径 = %+v，want %+v", again, paths)
	}
	if got := readFile(t, paths.Bun); got != "sentinel" {
		t.Fatalf("就绪后不应重新解压，bun 内容 = %q", got)
	}
}

func TestEnsureLockedReusesTargetReadyWhileWaiting(t *testing.T) {
	configDir := t.TempDir()
	path, err := ensureWith(configDir, "9.9.9", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(path.Bun)
	// 模拟「等锁期间另一进程已经解压完」：锁内复查命中，即使资源为空也不解压。
	got, err := ensureLocked(base, "9.9.9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != mustPaths(t, base) {
		t.Fatalf("锁内复用路径 = %+v", got)
	}
}

func TestEnsureVersionChangeExtractsFreshCopy(t *testing.T) {
	configDir := t.TempDir()
	first, err := ensureWith(configDir, "1.0.0", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureWith(configDir, "1.1.0", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("版本变化后应解压到新目录")
	}
	if _, err := os.Stat(first.Bun); err != nil {
		t.Fatalf("旧版本产物应保留: %v", err)
	}
	if got := readFile(t, second.Bun); got != "bun-binary" {
		t.Fatalf("新版本 bun 内容 = %q", got)
	}
	if !strings.Contains(second.Bun, filepath.Join(runtimeDirName, "1.1.0")) {
		t.Fatalf("新版本路径 = %q", second.Bun)
	}
}

func TestEnsureRepairsIncompleteInstall(t *testing.T) {
	configDir := t.TempDir()
	base := filepath.Join(configDir, runtimeDirName, "2.0.0")
	// 半成品：目录与标记都在，但 bun 缺失（模拟解压中途被杀）。
	if err := os.MkdirAll(filepath.Join(base, AdaptersDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, PluginsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, markerName), []byte(markerFormat+"\n2.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := ensureWith(configDir, "2.0.0", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, paths.Bun); got != "bun-binary" {
		t.Fatalf("修复后 bun 内容 = %q", got)
	}
	assertNoStaging(t, configDir)

	// 目标位置被一个同名文件占据时，也应清理后重建。
	fileTarget := filepath.Join(configDir, runtimeDirName, "2.1.0")
	if err := os.WriteFile(fileTarget, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	repaired, err := ensureWith(configDir, "2.1.0", standardAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repaired.Bun); got != "bun-binary" {
		t.Fatalf("同名文件修复后 bun 内容 = %q", got)
	}
}

func TestEnsureRejectsBadInput(t *testing.T) {
	assets := standardAssets(t)
	for _, tc := range []struct{ name, configDir, version string }{
		{"空配置目录", "", "1.0.0"},
		{"空白配置目录", "   ", "1.0.0"},
		{"空版本", t.TempDir(), ""},
		{"空白版本", t.TempDir(), "  "},
		{"点版本", t.TempDir(), "."},
		{"双点版本", t.TempDir(), ".."},
		{"斜杠版本", t.TempDir(), "v1/v2"},
		{"反斜杠版本", t.TempDir(), `v1\v2`},
		{"冒号版本", t.TempDir(), "v1:v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ensureWith(tc.configDir, tc.version, assets); err == nil {
				t.Fatalf("configDir=%q version=%q 应失败", tc.configDir, tc.version)
			}
		})
	}
}

func TestEnsureConfigDirIsAFile(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.WriteFile(configDir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err == nil {
		t.Fatal("配置目录是文件时应返回错误")
	}
}

func TestEnsureRenameFailures(t *testing.T) {
	oldRename := renamePath
	t.Cleanup(func() { renamePath = oldRename })

	t.Run("无并发发布者时报错并清理临时目录", func(t *testing.T) {
		renamePath = func(string, string) error { return errors.New("rename failed") }
		configDir := t.TempDir()
		if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err == nil {
			t.Fatal("rename 失败应返回错误")
		}
		assertNoStaging(t, configDir)
	})

	t.Run("并发发布者已就绪时复用其产物", func(t *testing.T) {
		configDir := t.TempDir()
		target := filepath.Join(configDir, runtimeDirName, "1.0.0")
		renamePath = func(staging, dest string) error {
			// 另一进程抢先把目标发布完成。
			if err := installReadyTree(t, dest, "1.0.0"); err != nil {
				t.Fatal(err)
			}
			return errors.New("exists")
		}
		paths, err := ensureWith(configDir, "1.0.0", standardAssets(t))
		if err != nil {
			t.Fatal(err)
		}
		if paths != mustPaths(t, target) {
			t.Fatalf("并发发布后路径 = %+v", paths)
		}
		assertNoStaging(t, configDir)
	})
}

func TestEnsureCleanupAndTempFailures(t *testing.T) {
	oldTemp, oldRemove, oldWrite := mkdirTemp, removePath, writeFile
	t.Cleanup(func() { mkdirTemp, removePath, writeFile = oldTemp, oldRemove, oldWrite })

	t.Run("创建临时目录失败", func(t *testing.T) {
		mkdirTemp, removePath, writeFile = oldTemp, oldRemove, oldWrite
		mkdirTemp = func(string, string) (string, error) { return "", errors.New("no temp") }
		if _, err := ensureWith(t.TempDir(), "1.0.0", standardAssets(t)); err == nil {
			t.Fatal("临时目录失败应返回错误")
		}
	})

	t.Run("写就绪标记失败", func(t *testing.T) {
		mkdirTemp, removePath, writeFile = oldTemp, oldRemove, oldWrite
		writeFile = func(string, []byte, os.FileMode) error { return errors.New("readonly") }
		configDir := t.TempDir()
		if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err == nil {
			t.Fatal("标记写入失败应返回错误")
		}
		assertNoStaging(t, configDir)
	})

	t.Run("清理残留目录失败", func(t *testing.T) {
		mkdirTemp, removePath, writeFile = oldTemp, oldRemove, oldWrite
		configDir := t.TempDir()
		target := filepath.Join(configDir, runtimeDirName, "1.0.0")
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		removePath = func(path string) error {
			if path == target {
				return errors.New("busy")
			}
			return nil
		}
		if _, err := ensureWith(configDir, "1.0.0", standardAssets(t)); err == nil {
			t.Fatal("清理残留目录失败应返回错误")
		}
	})
}

func TestEnsureExtractBunFailures(t *testing.T) {
	oldCreate, oldChmod := createFile, chmodPath
	t.Cleanup(func() { createFile, chmodPath = oldCreate, oldChmod })

	validZstd := zstdBytes(t, []byte("bun-binary"))
	truncated := validZstd[:len(validZstd)-4]

	cases := []struct {
		name     string
		assets   fstest.MapFS
		prepare  func()
		wantHint string
	}{
		{
			name: "缺少 bun 资源",
			assets: func() fstest.MapFS {
				assets := standardAssets(t)
				delete(assets, AssetBun)
				return assets
			}(),
			wantHint: AssetBun,
		},
		{
			name:     "bun 资源不是 zstd",
			assets:   withAsset(t, AssetBun, []byte("not zstd")),
			wantHint: "解压",
		},
		{
			name:     "bun 压缩流被截断",
			assets:   withAsset(t, AssetBun, truncated),
			wantHint: "unexpected EOF",
		},
		{
			name:   "创建 bun 文件失败",
			assets: standardAssets(t),
			prepare: func() {
				createFile = func(string, os.FileMode) (sink, error) {
					return nil, errors.New("no space")
				}
			},
			wantHint: "创建",
		},
		{
			name:   "关闭 bun 文件失败",
			assets: standardAssets(t),
			prepare: func() {
				createFile = func(name string, perm os.FileMode) (sink, error) {
					file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
					if err != nil {
						return nil, err
					}
					return closeErrorSink{File: file}, nil
				}
			},
			wantHint: "关闭",
		},
		{
			name:   "设置 bun 权限失败",
			assets: standardAssets(t),
			prepare: func() {
				chmodPath = func(string, os.FileMode) error { return errors.New("chmod") }
			},
			wantHint: "权限",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			createFile, chmodPath = oldCreate, oldChmod
			if tc.prepare != nil {
				tc.prepare()
			}
			configDir := t.TempDir()
			_, err := ensureWith(configDir, "1.0.0", tc.assets)
			if err == nil || !strings.Contains(err.Error(), tc.wantHint) {
				t.Fatalf("错误 = %v，应包含 %q", err, tc.wantHint)
			}
			if _, statErr := os.Stat(filepath.Join(configDir, runtimeDirName, "1.0.0")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("失败后不应发布目标目录: %v", statErr)
			}
			assertNoStaging(t, configDir)
		})
	}
}

// closeErrorSink 让真实文件写入成功，但 Close 返回错误。
type closeErrorSink struct {
	*os.File
}

func (c closeErrorSink) Close() error {
	_ = c.File.Close()
	return errors.New("close failed")
}

// writeErrorSink 让写入失败，用于覆盖写文件失败分支。
type writeErrorSink struct{}

func (writeErrorSink) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (writeErrorSink) Close() error              { return nil }

func TestEnsureAdaptersArchiveFailures(t *testing.T) {
	oldCreate, oldChmod := createFile, chmodPath
	t.Cleanup(func() {
		createFile, chmodPath = oldCreate, oldChmod
	})

	stdTar := rawTar(t, []testEntry{
		{name: "adapters/", typ: tar.TypeDir, mode: 0o755},
		{name: "adapters/a.mjs", body: "x", mode: 0o644},
	})
	shortBody := func() []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		hdr := &tar.Header{Name: "adapters/short.txt", Mode: 0o644, Size: 100, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("short")); err != nil {
			t.Fatal(err)
		}
		// 故意不写完成员：Close 报「少写了字节」，留下截断的 tar。
		_ = tw.Close()
		return buf.Bytes()
	}()

	cases := []struct {
		name     string
		assets   fstest.MapFS
		prepare  func()
		wantHint string
	}{
		{
			name:     "缺少 adapters 资源",
			assets:   withAsset(t, AssetAdapters, nil),
			wantHint: AssetAdapters,
		},
		{
			name:     "adapters 不是 zstd",
			assets:   withAsset(t, AssetAdapters, []byte("nope")),
			wantHint: AssetAdapters,
		},
		{
			name:     "tar 被截断",
			assets:   withAsset(t, AssetAdapters, zstdBytes(t, stdTar[:len(stdTar)-40])),
			wantHint: AssetAdapters,
		},
		{
			name:     "tar 里出现包外条目",
			assets:   withAsset(t, AssetAdapters, tarZstd(t, []testEntry{{name: "evil/x", body: "x"}})),
			wantHint: AssetAdapters,
		},
		{
			name:     "tar 里出现越界条目",
			assets:   withAsset(t, AssetAdapters, tarZstd(t, []testEntry{{name: "adapters/../evil", body: "x"}})),
			wantHint: AssetAdapters,
		},
		{
			name:     "tar 为空",
			assets:   withAsset(t, AssetAdapters, tarZstd(t, nil)),
			wantHint: AssetAdapters,
		},
		{
			name:     "tar 里有符号链接",
			assets:   withAsset(t, AssetAdapters, tarZstd(t, []testEntry{{name: "adapters/link", typ: tar.TypeSymlink}})),
			wantHint: "不支持的条目类型",
		},
		{
			name:     "成员声明长度超过实际内容",
			assets:   withAsset(t, AssetAdapters, zstdBytes(t, shortBody)),
			wantHint: AssetAdapters,
		},
		{
			name: "父路径是普通文件",
			assets: withAsset(t, AssetAdapters, tarZstd(t, []testEntry{
				{name: "adapters", body: "file", mode: 0o644},
				{name: "adapters/a.mjs", body: "x", mode: 0o644},
			})),
			wantHint: AssetAdapters,
		},
		{
			name: "目录父路径是普通文件",
			assets: withAsset(t, AssetAdapters, tarZstd(t, []testEntry{
				{name: "adapters", body: "file", mode: 0o644},
				{name: "adapters/sub/", typ: tar.TypeDir, mode: 0o755},
			})),
			wantHint: AssetAdapters,
		},
		{
			name:   "创建成员文件失败",
			assets: standardAssets(t),
			prepare: func() {
				createFile = func(name string, perm os.FileMode) (sink, error) {
					if strings.HasSuffix(name, "index.mjs") {
						return nil, errors.New("no space")
					}
					return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
				}
			},
			wantHint: "index.mjs",
		},
		{
			name:   "写成员内容失败",
			assets: standardAssets(t),
			prepare: func() {
				createFile = func(name string, perm os.FileMode) (sink, error) {
					if strings.HasSuffix(name, "index.mjs") {
						return writeErrorSink{}, nil
					}
					return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
				}
			},
			wantHint: "index.mjs",
		},
		{
			name:   "关闭成员文件失败",
			assets: standardAssets(t),
			prepare: func() {
				createFile = func(name string, perm os.FileMode) (sink, error) {
					file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
					if err != nil {
						return nil, err
					}
					if strings.HasSuffix(name, "index.mjs") {
						return closeErrorSink{File: file}, nil
					}
					return file, nil
				}
			},
			wantHint: "index.mjs",
		},
		{
			name:   "设置成员权限失败",
			assets: standardAssets(t),
			prepare: func() {
				chmodPath = func(name string, _ os.FileMode) error {
					if strings.HasSuffix(name, "index.mjs") {
						return errors.New("chmod")
					}
					return nil
				}
			},
			wantHint: "index.mjs",
		},
		{
			name:   "设置目录权限失败",
			assets: standardAssets(t),
			prepare: func() {
				chmodPath = func(name string, _ os.FileMode) error {
					if strings.HasSuffix(name, "qoder") {
						return errors.New("chmod dir")
					}
					return nil
				}
			},
			wantHint: "qoder",
		},
		{
			name: "缺少 provider-plugins 资源",
			assets: func() fstest.MapFS {
				assets := standardAssets(t)
				delete(assets, AssetPlugins)
				return assets
			}(),
			wantHint: AssetPlugins,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			createFile, chmodPath = oldCreate, oldChmod
			if tc.prepare != nil {
				tc.prepare()
			}
			configDir := t.TempDir()
			_, err := ensureWith(configDir, "1.0.0", tc.assets)
			if err == nil || !strings.Contains(err.Error(), tc.wantHint) {
				t.Fatalf("错误 = %v，应包含 %q", err, tc.wantHint)
			}
			assertNoStaging(t, configDir)
		})
	}
}

// TestWriteEntryDefaultsZeroMode 覆盖 tar 条目未携带权限位时的默认权限。
func TestWriteEntryDefaultsZeroMode(t *testing.T) {
	dest := t.TempDir()
	dir := filepath.Join(dest, AdaptersDirName)
	if err := writeEntry(dir, &tar.Header{Name: AdaptersDirName, Typeflag: tar.TypeDir}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("目录默认权限 = %o，want 755", got)
	}
	file := filepath.Join(dir, "index.mjs")
	if err := writeEntry(file, &tar.Header{Name: "adapters/index.mjs", Typeflag: tar.TypeReg}, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("文件默认权限 = %o，want 644", got)
	}
}

func TestIsReadyVariants(t *testing.T) {
	base := t.TempDir()
	if isReady(base, "1.0.0") {
		t.Fatal("空目录不应就绪")
	}
	if err := os.WriteFile(filepath.Join(base, markerName), []byte("bad\nlines\nhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReady(base, "1.0.0") {
		t.Fatal("格式不符不应就绪")
	}
	if err := os.WriteFile(filepath.Join(base, markerName), []byte(markerFormat+"\n1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReady(base, "2.0.0") {
		t.Fatal("版本不符不应就绪")
	}
	if err := os.WriteFile(filepath.Join(base, bunExecutableName), []byte("bun"), 0o700); err != nil {
		t.Fatal(err)
	}
	if isReady(base, "1.0.0") {
		t.Fatal("目录不全不应就绪")
	}
	if err := os.MkdirAll(filepath.Join(base, AdaptersDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if isReady(base, "1.0.0") {
		t.Fatal("缺少 plugins 目录不应就绪")
	}
	if err := os.MkdirAll(filepath.Join(base, PluginsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if !isReady(base, "1.0.0") {
		t.Fatal("完整目录应就绪")
	}
	if !fileExists(filepath.Join(base, bunExecutableName)) || fileExists(base) || !isDir(base) {
		t.Fatal("文件/目录判定错误")
	}
}

func TestEnsureConcurrentCallsExtractOnce(t *testing.T) {
	configDir := t.TempDir()
	oldCreate := createFile
	t.Cleanup(func() { createFile = oldCreate })

	var mu sync.Mutex
	bunWrites := 0
	createFile = func(name string, perm os.FileMode) (sink, error) {
		if strings.HasSuffix(name, bunExecutableName) {
			mu.Lock()
			bunWrites++
			mu.Unlock()
		}
		return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	}

	const callers = 8
	results := make([]Paths, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = ensureWith(configDir, "1.0.0", standardAssets(t))
		}(i)
	}
	close(start)
	wg.Wait()

	want := mustPaths(t, filepath.Join(configDir, runtimeDirName, "1.0.0"))
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("第 %d 个调用失败: %v", i, errs[i])
		}
		if results[i] != want {
			t.Fatalf("第 %d 个调用路径 = %+v，want %+v", i, results[i], want)
		}
	}
	if bunWrites != 1 {
		t.Fatalf("bun 应只解压一次，实际 %d 次", bunWrites)
	}
	if got := readFile(t, want.Bun); got != "bun-binary" {
		t.Fatalf("并发解压后 bun 内容 = %q", got)
	}
	assertNoStaging(t, configDir)
}

// installReadyTree 模拟另一个进程已经完成发布。
func installReadyTree(t *testing.T, target, version string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(target, AdaptersDirName), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(target, PluginsDirName), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(target, bunExecutableName), []byte("other"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(target, markerName), []byte(markerFormat+"\n"+version+"\n"), 0o600)
}
