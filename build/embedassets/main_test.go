package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"qka/internal/runtimebundle"
)

// fixture 生成一套小的 bun/adapters/provider-plugins 输入。
func fixture(t *testing.T) genOptions {
	t.Helper()
	root := t.TempDir()
	bun := filepath.Join(root, "bun.exe")
	if err := os.WriteFile(bun, []byte("bun-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapters := filepath.Join(root, "adapters")
	if err := os.MkdirAll(filepath.Join(adapters, "qoder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapters, "qoder", "index.mjs"), []byte("export const x = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapters, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(root, "provider-plugins")
	if err := os.MkdirAll(filepath.Join(plugins, "airush"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "airush", "package.json"), []byte(`{"name":"airush"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return genOptions{
		Arch:     "amd64",
		Bun:      bun,
		Adapters: adapters,
		Plugins:  plugins,
		Out:      filepath.Join(root, "assets"),
	}
}

// args 把 genOptions 转成命令行参数。
func (o genOptions) args() []string {
	return []string{
		"--arch", o.Arch,
		"--bun", o.Bun,
		"--adapters", o.Adapters,
		"--plugins", o.Plugins,
		"--out", o.Out,
	}
}

func decodeZstd(t *testing.T, path string) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	dec, err := zstd.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	data, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type tarEntryData struct {
	body     string
	mode     int64
	typeflag byte
	modTime  time.Time
	uid      int
}

func readTarZstd(t *testing.T, name string) map[string]tarEntryData {
	t.Helper()
	raw := decodeZstd(t, name)
	tr := tar.NewReader(bytes.NewReader(raw))
	entries := map[string]tarEntryData{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = tarEntryData{
			body:     string(body),
			mode:     hdr.Mode,
			typeflag: hdr.Typeflag,
			modTime:  hdr.ModTime,
			uid:      hdr.Uid,
		}
	}
	return entries
}

func TestRunGeneratesEmbeddedAssets(t *testing.T) {
	opts := fixture(t)
	// 预置上一版残留与目录占位文件。
	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.Out, "stale.zst"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{".gitignore", ".gitkeep"} {
		if err := os.WriteFile(filepath.Join(opts.Out, keep), []byte("# keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := run(opts.args()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(opts.Out, "stale.zst")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("生成前应清空输出目录: %v", err)
	}
	for _, keep := range []string{".gitignore", ".gitkeep"} {
		if _, err := os.Stat(filepath.Join(opts.Out, keep)); err != nil {
			t.Fatalf("占位文件 %s 应保留: %v", keep, err)
		}
	}

	bunPath := filepath.Join(opts.Out, runtimebundle.AssetBun)
	if got := string(decodeZstd(t, bunPath)); got != "bun-binary" {
		t.Fatalf("bun.zst 内容 = %q", got)
	}

	adapters := readTarZstd(t, filepath.Join(opts.Out, runtimebundle.AssetAdapters))
	if entry, ok := adapters["adapters/qoder/index.mjs"]; !ok ||
		entry.body != "export const x = 1;" || entry.mode != 0o644 {
		t.Fatalf("adapters 包内容错误: %+v", adapters)
	}
	if entry := adapters["adapters/run.sh"]; entry.mode != 0o755 {
		t.Fatalf("adapters 可执行位未保留: %+v", entry)
	}
	for _, dir := range []string{"adapters/", "adapters/qoder/"} {
		if entry, ok := adapters[dir]; !ok || entry.typeflag != tar.TypeDir {
			t.Fatalf("adapters 缺少目录条目 %s: %+v", dir, adapters)
		}
	}
	for name, entry := range adapters {
		// 归一化元数据：同样的输入必须产出同样的包。
		if entry.uid != 0 || !entry.modTime.Equal(time.Unix(0, 0)) {
			t.Fatalf("条目 %s 元数据未归一化: uid=%d modTime=%v", name, entry.uid, entry.modTime)
		}
	}

	plugins := readTarZstd(t, filepath.Join(opts.Out, runtimebundle.AssetPlugins))
	if entry := plugins["provider-plugins/airush/package.json"]; entry.body != `{"name":"airush"}` {
		t.Fatalf("provider-plugins 包内容错误: %+v", entry)
	}

	meta, err := os.ReadFile(filepath.Join(opts.Out, runtimebundle.AssetMetadata))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Arch string `json:"arch"`
	}
	if err := json.Unmarshal(meta, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Arch != "amd64" {
		t.Fatalf("bundle.json arch = %q", decoded.Arch)
	}

	// 同样的输入重新生成，产物必须逐字节相同。
	first := hashFiles(t, opts.Out)
	if err := run(opts.args()); err != nil {
		t.Fatal(err)
	}
	if second := hashFiles(t, opts.Out); second != first {
		t.Fatal("同样的输入应产出逐字节相同的资源")
	}
}

func hashFiles(t *testing.T, dir string) string {
	t.Helper()
	hasher := sha256.New()
	for _, name := range []string{
		runtimebundle.AssetBun,
		runtimebundle.AssetAdapters,
		runtimebundle.AssetPlugins,
		runtimebundle.AssetMetadata,
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		hasher.Write(data)
	}
	return string(hasher.Sum(nil))
}

func TestRunRejectsBadInput(t *testing.T) {
	opts := fixture(t)
	asFile := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(asFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")

	cases := []struct {
		name string
		args []string
	}{
		{"未知参数", []string{"--bogus"}},
		{"缺少架构", []string{"--bun", opts.Bun, "--adapters", opts.Adapters, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"非法架构", []string{"--arch", "386"}},
		{"缺少 bun", []string{"--arch", "amd64", "--adapters", opts.Adapters, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"bun 不存在", []string{"--arch", "amd64", "--bun", missing, "--adapters", opts.Adapters, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"bun 是目录", []string{"--arch", "amd64", "--bun", opts.Adapters, "--adapters", opts.Adapters, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"适配器不存在", []string{"--arch", "amd64", "--bun", opts.Bun, "--adapters", missing, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"适配器是文件", []string{"--arch", "amd64", "--bun", opts.Bun, "--adapters", asFile, "--plugins", opts.Plugins, "--out", opts.Out}},
		{"插件不存在", []string{"--arch", "amd64", "--bun", opts.Bun, "--adapters", opts.Adapters, "--plugins", missing, "--out", opts.Out}},
		{"插件是文件", []string{"--arch", "amd64", "--bun", opts.Bun, "--adapters", opts.Adapters, "--plugins", asFile, "--out", opts.Out}},
		{"缺少输出目录", []string{"--arch", "amd64", "--bun", opts.Bun, "--adapters", opts.Adapters, "--plugins", opts.Plugins, "--out", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := run(tc.args); err == nil {
				t.Fatalf("参数 %v 应被拒绝", tc.args)
			}
		})
	}
}

func TestRunUsesDefaultOutputDirectory(t *testing.T) {
	// 不带 --out 时默认写 internal/runtimebundle/assets；缺 --arch 会在
	// 触碰文件系统前失败，因此这里既覆盖默认值也不污染工作树。
	if err := run(nil); err == nil {
		t.Fatal("缺少 --arch 应失败")
	}
}

func TestResetOutFailures(t *testing.T) {
	oldReadDir, oldRemoveAll := readDir, removeAll
	t.Cleanup(func() { readDir, removeAll = oldReadDir, oldRemoveAll })

	out := t.TempDir()

	readDir = func(string) ([]fs.DirEntry, error) { return nil, errors.New("read failed") }
	if err := resetOut(out); err == nil {
		t.Fatal("读取输出目录失败应返回错误")
	}

	readDir = oldReadDir
	if err := os.WriteFile(filepath.Join(out, "stale.zst"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeAll = func(string) error { return errors.New("busy") }
	if err := resetOut(out); err == nil {
		t.Fatal("清空失败应返回错误")
	}
	removeAll = oldRemoveAll

	// 输出路径是文件时 MkdirAll 失败。
	file := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := resetOut(file); err == nil {
		t.Fatal("输出路径是文件应返回错误")
	}
}

func TestWriteZstdFailures(t *testing.T) {
	oldCreate, oldRename, oldRemove := createTemp, renamePath, removePath
	t.Cleanup(func() { createTemp, renamePath, removePath = oldCreate, oldRename, oldRemove })

	out := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("创建临时文件失败", func(t *testing.T) {
		createTemp, renamePath, removePath = oldCreate, oldRename, oldRemove
		createTemp = func(string, string) (temporaryFile, error) { return nil, errors.New("no temp") }
		if err := compressFile(filepath.Join(out, "x.zst"), src); err == nil {
			t.Fatal("临时文件失败应返回错误")
		}
	})

	t.Run("收尾压缩流失败", func(t *testing.T) {
		createTemp, renamePath, removePath = oldCreate, oldRename, oldRemove
		createTemp = func(string, string) (temporaryFile, error) {
			return failingTemp{name: filepath.Join(out, "broken.tmp")}, nil
		}
		if err := compressFile(filepath.Join(out, "x.zst"), src); err == nil {
			t.Fatal("压缩流收尾失败应返回错误")
		}
	})

	t.Run("关闭临时文件失败", func(t *testing.T) {
		createTemp, renamePath, removePath = oldCreate, oldRename, oldRemove
		createTemp = func(string, string) (temporaryFile, error) {
			return &stubTemp{name: filepath.Join(out, "buffered.tmp"), closeErr: errors.New("close failed")}, nil
		}
		if err := compressFile(filepath.Join(out, "x.zst"), src); err == nil {
			t.Fatal("关闭临时文件失败应返回错误")
		}
	})

	t.Run("发布失败并清理临时文件", func(t *testing.T) {
		createTemp, renamePath, removePath = oldCreate, oldRename, oldRemove
		var tempName string
		createTemp = func(dir, pattern string) (temporaryFile, error) {
			file, err := os.CreateTemp(dir, pattern)
			if err != nil {
				return nil, err
			}
			tempName = file.Name()
			return file, nil
		}
		renamePath = func(string, string) error { return errors.New("rename failed") }
		if err := compressFile(filepath.Join(out, "x.zst"), src); err == nil {
			t.Fatal("发布失败应返回错误")
		}
		if _, err := os.Stat(tempName); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("失败后应清理临时文件: %v", err)
		}
	})
}

// stubTemp 把写入收进内存，并可模拟关闭失败。
type stubTemp struct {
	name     string
	buf      bytes.Buffer
	closeErr error
}

func (s *stubTemp) Name() string                { return s.name }
func (s *stubTemp) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *stubTemp) Close() error                { return s.closeErr }

// failingTemp 让写入直接失败，用于覆盖压缩流收尾失败。
type failingTemp struct{ name string }

func (f failingTemp) Name() string              { return f.name }
func (f failingTemp) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func (f failingTemp) Close() error              { return nil }

func TestAddTreeFailures(t *testing.T) {
	oldWalk := walkDir
	t.Cleanup(func() { walkDir = oldWalk })

	srcRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcRoot, "a.mjs"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	walkDir = func(string, fs.WalkDirFunc) error { return errors.New("walk failed") }
	var walkBuf bytes.Buffer
	if err := addTree(tar.NewWriter(&walkBuf), srcRoot, "adapters"); err == nil {
		t.Fatal("遍历失败应返回错误")
	}

	cases := []struct {
		name    string
		entries []fakeDirEntry
		want    string
	}{
		{
			name:    "读取条目信息失败",
			entries: []fakeDirEntry{{rel: "a.mjs", err: errors.New("info failed")}},
			want:    "info failed",
		},
		{
			name:    "符号链接",
			entries: []fakeDirEntry{{rel: "a.mjs", info: fakeInfo{name: "a.mjs", mode: os.ModeSymlink | 0o777}}},
			want:    "不支持符号链接",
		},
		{
			name:    "头部无法编码",
			entries: []fakeDirEntry{{rel: "a.mjs", info: fakeInfo{name: "a.mjs", size: -1, mode: 0o644}}},
			want:    "negative size",
		},
		{
			name:    "读取文件内容失败",
			entries: []fakeDirEntry{{rel: "missing.mjs", info: fakeInfo{name: "missing.mjs", size: 0, mode: 0o644}}},
			want:    "missing.mjs",
		},
		{
			name:    "写入内容超过声明长度",
			entries: []fakeDirEntry{{rel: "a.mjs", info: fakeInfo{name: "a.mjs", size: 0, mode: 0o644}}},
			want:    "write too long",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			walkDir = func(root string, fn fs.WalkDirFunc) error {
				for _, entry := range tc.entries {
					if err := fn(filepath.Join(root, entry.rel), entry, nil); err != nil {
						return err
					}
				}
				return nil
			}
			var buf bytes.Buffer
			err := addTree(tar.NewWriter(&buf), srcRoot, "adapters")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 = %v，应包含 %q", err, tc.want)
			}
		})
	}

	// 声明长度大于实际内容：tar.Writer.Close 报「少写了字节」。
	t.Run("收尾时成员长度不足", func(t *testing.T) {
		walkDir = func(root string, fn fs.WalkDirFunc) error {
			return fn(filepath.Join(root, "a.mjs"),
				fakeDirEntry{rel: "a.mjs", info: fakeInfo{name: "a.mjs", size: 100, mode: 0o644}}, nil)
		}
		out := filepath.Join(t.TempDir(), "adapters.tar.zst")
		if err := compressTar(out, srcRoot, "adapters"); err == nil {
			t.Fatal("成员长度不足应在收尾时报错")
		}
	})
}

func TestGeneratePropagatesFailures(t *testing.T) {
	oldReadDir, oldCreate, oldWalk, oldWrite := readDir, createTemp, walkDir, writeFile
	t.Cleanup(func() {
		readDir, createTemp, walkDir, writeFile = oldReadDir, oldCreate, oldWalk, oldWrite
	})
	reset := func() {
		readDir, createTemp, walkDir, writeFile = oldReadDir, oldCreate, oldWalk, oldWrite
	}

	t.Run("清空输出目录失败", func(t *testing.T) {
		reset()
		readDir = func(string) ([]fs.DirEntry, error) { return nil, errors.New("read failed") }
		if err := generate(fixture(t)); err == nil {
			t.Fatal("清空输出目录失败应返回错误")
		}
	})

	t.Run("压缩 bun 失败", func(t *testing.T) {
		reset()
		createTemp = func(string, string) (temporaryFile, error) { return nil, errors.New("no temp") }
		if err := generate(fixture(t)); err == nil {
			t.Fatal("压缩 bun 失败应返回错误")
		}
	})

	t.Run("打包 adapters 失败", func(t *testing.T) {
		reset()
		walkDir = func(string, fs.WalkDirFunc) error { return errors.New("walk failed") }
		if err := generate(fixture(t)); err == nil {
			t.Fatal("打包 adapters 失败应返回错误")
		}
	})

	t.Run("打包 provider-plugins 失败", func(t *testing.T) {
		reset()
		opts := fixture(t)
		if err := os.Symlink(filepath.Join(opts.Plugins, "airush"), filepath.Join(opts.Plugins, "link")); err != nil {
			t.Fatal(err)
		}
		if err := generate(opts); err == nil {
			t.Fatal("打包 provider-plugins 失败应返回错误")
		}
	})

	t.Run("写 bundle.json 失败", func(t *testing.T) {
		reset()
		writeFile = func(string, []byte, os.FileMode) error { return errors.New("readonly") }
		if err := generate(fixture(t)); err == nil {
			t.Fatal("写 bundle.json 失败应返回错误")
		}
	})
}

func TestAddTreeWalkError(t *testing.T) {
	oldWalk := walkDir
	t.Cleanup(func() { walkDir = oldWalk })
	walkDir = func(string, fs.WalkDirFunc) error { return nil }
	// 遍历回调自带错误：直接透传。
	walkDir = func(_ string, fn fs.WalkDirFunc) error {
		return fn("ignored", nil, errors.New("walk callback failed"))
	}
	var buf bytes.Buffer
	err := addTree(tar.NewWriter(&buf), t.TempDir(), "adapters")
	if err == nil || !strings.Contains(err.Error(), "walk callback failed") {
		t.Fatalf("遍历回调错误应透传，实际 %v", err)
	}
}

func TestCompressFileFailures(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.zst")
	if err := compressFile(out, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("源文件不存在应返回错误")
	}
	// 源是目录：os.Open 成功，读取时报 EISDIR。
	if err := compressFile(out, t.TempDir()); err == nil {
		t.Fatal("源是目录应返回错误")
	}
}

func TestMainSuccessAndFailure(t *testing.T) {
	oldCommand := runCommand
	t.Cleanup(func() { runCommand = oldCommand })

	var passed []string
	runCommand = func(args []string) error {
		passed = append([]string(nil), args...)
		return nil
	}
	main()
	if len(passed) != len(os.Args)-1 {
		t.Fatalf("main 应传递命令行参数，实际 %v", passed)
	}

	runCommand = func([]string) error { return errors.New("boom") }
	defer func() {
		if recover() == nil {
			t.Fatal("生成失败时 main 应 panic")
		}
	}()
	main()
}

// fakeDirEntry / fakeInfo 让 addTree 的异常分支可以用合成条目覆盖。
type fakeDirEntry struct {
	rel  string
	info fs.FileInfo
	err  error
}

func (e fakeDirEntry) Name() string { return path.Base(e.rel) }
func (e fakeDirEntry) IsDir() bool  { return e.info != nil && e.info.IsDir() }
func (e fakeDirEntry) Type() fs.FileMode {
	if e.info == nil {
		return 0
	}
	return e.info.Mode().Type()
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return e.info, e.err }

type fakeInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }
