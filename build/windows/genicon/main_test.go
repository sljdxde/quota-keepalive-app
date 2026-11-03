package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// solidPNG 生成纯色 PNG，用于构造可预测的测试母图。
func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("生成的 PNG 不能解码: %v", err)
	}
	return img
}

// visibleStats 统计可见像素占比，以及象牙白/暖金像素数，用来断言配色没有丢。
func visibleStats(img image.Image) (visible, ivory, warm int) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a>>8 < 8 {
				continue
			}
			visible++
			R, G, B := float64(r>>8), float64(g>>8), float64(bl>>8)
			luma := 0.2126*R + 0.7152*G + 0.0722*B
			// 象牙白：高亮度、近中性（母图 Q 环）。
			if luma > 190 && R-G < 25 && G-B < 45 {
				ivory++
			}
			// 暖金：红高、蓝低（母图尾笔）。
			if R > 190 && G > 130 && B < 140 && R-B > 70 {
				warm++
			}
		}
	}
	return visible, ivory, warm
}

func TestIconSizesMatchWinresJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "winres.json"))
	if err != nil {
		t.Fatalf("读取 winres.json: %v", err)
	}
	var cfg struct {
		GroupIcon map[string]map[string][]string `json:"RT_GROUP_ICON"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("解析 winres.json: %v", err)
	}
	want := map[string]bool{}
	for _, languages := range cfg.GroupIcon {
		for _, names := range languages {
			for _, name := range names {
				want[name] = true
			}
		}
	}
	for _, size := range iconSizes {
		name := iconName(size)
		if !want[name] {
			t.Errorf("winres.json 没有引用 %s，生成的图标不会被注入 exe", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("winres.json 引用了不存在的尺寸 %s（iconSizes 未生成）", name)
	}
}

func TestRunWritesEverySize(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{dir}); err != nil {
		t.Fatal(err)
	}
	for _, size := range iconSizes {
		path := filepath.Join(dir, iconName(size))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("缺少 %s: %v", iconName(size), err)
		}
		img := decodePNG(t, data)
		if got := img.Bounds().Dx(); got != size {
			t.Fatalf("%s 宽度 = %d，期望 %d", iconName(size), got, size)
		}
		if got := img.Bounds().Dy(); got != size {
			t.Fatalf("%s 高度 = %d，期望 %d", iconName(size), got, size)
		}
		visible, ivory, warm := visibleStats(img)
		total := size * size
		if visible*4 < total {
			t.Fatalf("%s 可见像素太少（%d/%d），图标可能是空的", iconName(size), visible, total)
		}
		// 与母图同色：象牙白 Q 环与暖金尾笔都要能抽到。
		if ivory == 0 || warm == 0 {
			t.Fatalf("%s 缺少母图配色（象牙白=%d 暖金=%d）", iconName(size), ivory, warm)
		}
	}
}

func TestRunDefaultOutputDir(t *testing.T) {
	oldWrite := writeFile
	t.Cleanup(func() { writeFile = oldWrite })

	var paths []string
	writeFile = func(path string, data []byte, mode os.FileMode) error {
		paths = append(paths, path)
		return nil
	}
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(iconSizes) {
		t.Fatalf("写出文件数 = %d，期望 %d", len(paths), len(iconSizes))
	}
	for i, size := range iconSizes {
		want := filepath.Join(defaultOutputDir, iconName(size))
		if paths[i] != want {
			t.Fatalf("第 %d 个输出路径 = %q，期望 %q", i, paths[i], want)
		}
	}
}

func TestRunWriteFailure(t *testing.T) {
	oldWrite := writeFile
	t.Cleanup(func() { writeFile = oldWrite })

	writeFile = func(path string, data []byte, mode os.FileMode) error {
		return errors.New("disk full")
	}
	err := run([]string{t.TempDir()})
	if err == nil {
		t.Fatal("写入失败应返回错误")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("错误信息应包含写入原因，实际 %v", err)
	}
}

func TestRunIconFailure(t *testing.T) {
	oldMaster := masterPNG
	t.Cleanup(func() { masterPNG = oldMaster })

	masterPNG = func() []byte { return []byte("not a png") }
	if err := run([]string{t.TempDir()}); err == nil {
		t.Fatal("母图损坏时 run 应返回错误")
	}
}

func TestEncodePNGFailure(t *testing.T) {
	if _, err := encodePNG(image.NewRGBA(image.Rect(0, 0, 0, 0))); err == nil {
		t.Fatal("空图像编码应返回错误")
	}
}

func TestLargeSizesAreBrandiconOutput(t *testing.T) {
	// 48px 起保持母图原始留白，字节级与 brandicon 一致，保证和 mac 图标同源。
	for _, size := range []int{48, 64, 128, 256} {
		got, err := iconPNG(size)
		if err != nil {
			t.Fatal(err)
		}
		want := applicationPNG(size)
		if !bytes.Equal(got, want) {
			t.Fatalf("%dpx 图标应与 brandicon.Application 输出一致", size)
		}
	}
}

func TestSmallIconsUseTighterFrame(t *testing.T) {
	// 小尺寸裁掉母图留白，可见内容在画布里的占比应比大尺寸更大。
	coverage := func(size int) float64 {
		data, err := iconPNG(size)
		if err != nil {
			t.Fatal(err)
		}
		img := decodePNG(t, data)
		art := artworkBounds(img)
		return float64(art.Dx()) / float64(size)
	}
	small := coverage(16)
	large := coverage(64)
	if small <= large {
		t.Fatalf("16px 收紧画框后的填充率 %.2f 应大于 64px 的 %.2f", small, large)
	}
	if small < 0.95 {
		t.Fatalf("16px 填充率只有 %.2f，Q 环可能仍太小", small)
	}
}

func TestSmallIconErrors(t *testing.T) {
	t.Run("母图不是 PNG", func(t *testing.T) {
		if _, err := smallIconPNG([]byte("not a png"), 16); err == nil {
			t.Fatal("非法母图应返回错误")
		}
	})
	t.Run("母图全透明", func(t *testing.T) {
		if _, err := smallIconPNG(solidPNG(t, 8, 8, color.RGBA{0, 0, 0, 0}), 16); err == nil {
			t.Fatal("全透明母图应返回错误")
		}
	})
	t.Run("编码失败", func(t *testing.T) {
		oldEncode := encodeImage
		t.Cleanup(func() { encodeImage = oldEncode })
		encodeImage = func(image.Image) ([]byte, error) { return nil, errors.New("encode") }
		if _, err := smallIconPNG(solidPNG(t, 8, 8, color.RGBA{255, 255, 255, 255}), 16); err == nil {
			t.Fatal("编码失败应返回错误")
		}
	})
	t.Run("极小母图走最小边距", func(t *testing.T) {
		data, err := smallIconPNG(solidPNG(t, 2, 2, color.RGBA{250, 240, 220, 255}), 16)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodePNG(t, data).Bounds().Dx(); got != 16 {
			t.Fatalf("宽度 = %d，期望 16", got)
		}
	})
}

func TestArtworkBoundsEmpty(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if !artworkBounds(img).Empty() {
		t.Fatal("全透明图像应返回空包围盒")
	}
}

func TestClamp(t *testing.T) {
	cases := []struct {
		in   float64
		want uint8
	}{
		{-1, 0}, {0, 0}, {127.4, 127}, {127.5, 128}, {255, 255}, {256, 255},
	}
	for _, c := range cases {
		if got := clamp(c.in); got != c.want {
			t.Fatalf("clamp(%v) = %d，期望 %d", c.in, got, c.want)
		}
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
			t.Fatal("run 失败时 main 应 panic")
		}
	}()
	main()
}
