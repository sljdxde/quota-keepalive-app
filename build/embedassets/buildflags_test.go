package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// makefileTargetBlock 返回 Makefile 中某个目标名之后的 recipe 块（以 tab
// 开头的连续行），用于精确断言「darwin 目标带了 -s -w」，而不是整个文件
// 任何地方出现 -s -w（Windows CLI 目标本来就有）。
func makefileTargetBlock(t *testing.T, content, target string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == target+":" {
			var block strings.Builder
			for _, recipe := range lines[i+1:] {
				if !strings.HasPrefix(recipe, "\t") {
					break
				}
				block.WriteString(recipe)
				block.WriteString("\n")
			}
			return block.String()
		}
	}
	t.Fatalf("Makefile 缺少目标 %s", target)
	return ""
}

// TestMacOSBuildStripsDebugSymbols 是体积回归用例：darwin 的两个发布目标与
// build-dmg.sh 必须继续带 -s -w。去掉后二进制会从约 24.6MB 涨回 33.7MB
// （-27%），这条用例防止有人在后续改动里把参数删掉。
func TestMacOSBuildStripsDebugSymbols(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"build-darwin-arm64", "build-darwin-amd64"} {
		block := makefileTargetBlock(t, string(makefile), target)
		if !strings.Contains(block, "-s -w") {
			t.Fatalf("%s 目标缺少 -s -w：\n%s", target, block)
		}
	}

	dmg, err := os.ReadFile("../../scripts/build-dmg.sh")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(dmg), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "go build -trimpath") {
			continue
		}
		end := min(i+4, len(lines))
		block := strings.Join(lines[i:end], "\n")
		if !strings.Contains(block, "-s -w") {
			t.Fatalf("build-dmg.sh 的 go build 缺少 -s -w：\n%s", block)
		}
		return
	}
	t.Fatal("build-dmg.sh 未找到 go build -trimpath")
}

// TestWriteZstdUsesBestCompression 是发行物体积回归用例：writeZstd 必须用
// 最高压缩档。用同一份可压缩数据分别走 writeZstd 与 SpeedDefault，前者体积
// 不得更大，并解码校验内容无损。
func TestWriteZstdUsesBestCompression(t *testing.T) {
	line := []byte(`{"role":"assistant","content":"the quick brown fox jumps over the lazy dog","tokens":42}` + "\n")
	data := bytes.Repeat(line, 8192) // 约 640KB

	dir := t.TempDir()
	bestPath := filepath.Join(dir, "best.zst")
	if err := writeZstd(bestPath, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	defaultPath := filepath.Join(dir, "default.zst")
	f, err := os.Create(defaultPath)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	bestInfo, err := os.Stat(bestPath)
	if err != nil {
		t.Fatal(err)
	}
	defaultInfo, err := os.Stat(defaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if bestInfo.Size() > defaultInfo.Size() {
		t.Fatalf("最高档压缩体积应 <= 默认档：best=%d default=%d", bestInfo.Size(), defaultInfo.Size())
	}

	compressed, err := os.Open(bestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	dec, err := zstd.NewReader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("最高档压缩解压后内容不一致")
	}
}
