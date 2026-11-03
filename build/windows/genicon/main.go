// Command genicon 生成 Windows PE 图标资源（RT_GROUP_ICON）用的 PNG 套图。
//
// 所有尺寸都从 internal/brandicon 的应用图标母图
// internal/brandicon/assets/app-1024.png 派生，和 macOS 的 .app 图标（打包时由
// packaging/genicon 输出 icns）同源同色：深青底、象牙白 Q 环、暖金尾笔。
//
// build/windows/icon-*.png 是生成产物，勿手改。改母图后重新运行：
//
//	go run ./build/windows/genicon
//
// 16/32px 两个小尺寸做「收紧画框」处理：母图四周有约 10% 透明留白，直接缩到
// 小尺寸会把 Q 环压成一小圈灰白，在 Windows 深色任务栏上几乎看不清；裁掉留白后
// 小图标里的 Q 更满也更容易辨认。64px 及以上保持母图原始留白，与 mac 完全一致。
//
// winres.json 里的 RT_GROUP_ICON 引用这些文件名，新增/删除尺寸要同步改
// build/windows/winres.json 与 iconSizes。
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"qka/internal/brandicon"
)

// iconSizes 是写出的 PNG 尺寸，必须覆盖 winres.json 的 RT_GROUP_ICON 列表。
var iconSizes = []int{16, 32, 48, 64, 128, 256}

// smallSizeLimit 及以下尺寸用收紧画框的派生方式（见文件头注释）。
const smallSizeLimit = 32

// smallFramePadding 是小尺寸裁掉母图留白后额外保留的边距比例，避免圆角贴边。
const smallFramePadding = 0.02

// defaultOutputDir 是相对仓库根目录的默认输出目录。
const defaultOutputDir = "build/windows"

var (
	// writeFile / runCommand 与 packaging/genicon 一样做成变量，便于测试。
	writeFile      = os.WriteFile
	runCommand     = run
	applicationPNG = brandicon.Application
	masterPNG      = brandicon.MasterPNG
	encodeImage    = encodePNG
)

func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		panic(err)
	}
}

// run 把全套尺寸写到 dir（默认 build/windows）。
func run(args []string) error {
	dir := defaultOutputDir
	if len(args) > 0 {
		dir = args[0]
	}
	for _, size := range iconSizes {
		data, err := iconPNG(size)
		if err != nil {
			return fmt.Errorf("生成 %dpx 图标: %w", size, err)
		}
		path := filepath.Join(dir, iconName(size))
		if err := writeFile(path, data, 0o644); err != nil {
			return fmt.Errorf("写入 %s: %w", path, err)
		}
	}
	return nil
}

// iconName 是 winres.json 引用的文件名。
func iconName(size int) string {
	return fmt.Sprintf("icon-%d.png", size)
}

// iconPNG 返回指定尺寸的图标 PNG。大尺寸直接用 brandicon 的缩放结果，和 mac
// 图标完全一致；小尺寸换成收紧画框的版本。
func iconPNG(size int) ([]byte, error) {
	if size > smallSizeLimit {
		return applicationPNG(size), nil
	}
	return smallIconPNG(masterPNG(), size)
}

// smallIconPNG 裁掉母图的透明留白（外加极小的边距）后缩放到 size。
func smallIconPNG(master []byte, size int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(master))
	if err != nil {
		return nil, fmt.Errorf("解码母图: %w", err)
	}
	art := artworkBounds(src)
	if art.Empty() {
		return nil, fmt.Errorf("母图没有可见像素")
	}
	pad := int(math.Round(float64(art.Dx()) * smallFramePadding))
	if pad < 1 {
		pad = 1
	}
	region := image.Rect(art.Min.X-pad, art.Min.Y-pad, art.Max.X+pad, art.Max.Y+pad).
		Intersect(src.Bounds())
	cropped := image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	draw.Draw(cropped, cropped.Bounds(), src, region.Min, draw.Src)
	return encodeImage(resize(cropped, size))
}

// artworkBounds 返回 alpha >= 8 的像素包围盒；全透明图返回空矩形。
func artworkBounds(img image.Image) image.Rectangle {
	b := img.Bounds()
	var out image.Rectangle
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a>>8 < 8 {
				continue
			}
			out = out.Union(image.Rect(x, y, x+1, y+1))
		}
	}
	return out
}

// resize 用面积平均把 src 缩放到 size×size。算法与 internal/brandicon 保持一致，
// 避免小尺寸和大尺寸出现两套插值风格。
func resize(src *image.RGBA, size int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	scaleX := float64(b.Dx()) / float64(size)
	scaleY := float64(b.Dy()) / float64(size)
	for y := 0; y < size; y++ {
		y0 := float64(y) * scaleY
		y1 := float64(y+1) * scaleY
		sy0 := max(0, int(math.Floor(y0)))
		sy1 := min(b.Dy(), int(math.Ceil(y1)))
		for x := 0; x < size; x++ {
			x0 := float64(x) * scaleX
			x1 := float64(x+1) * scaleX
			sx0 := max(0, int(math.Floor(x0)))
			sx1 := min(b.Dx(), int(math.Ceil(x1)))
			var r, g, bl, a float64
			for sy := sy0; sy < sy1; sy++ {
				wy := math.Min(float64(sy+1), y1) - math.Max(float64(sy), y0)
				for sx := sx0; sx < sx1; sx++ {
					wx := math.Min(float64(sx+1), x1) - math.Max(float64(sx), x0)
					c := src.RGBAAt(sx+b.Min.X, sy+b.Min.Y)
					weight := wx * wy
					r += float64(c.R) * weight
					g += float64(c.G) * weight
					bl += float64(c.B) * weight
					a += float64(c.A) * weight
				}
			}
			area := (x1 - x0) * (y1 - y0)
			dst.SetRGBA(x, y, color.RGBA{
				R: clamp(r / area),
				G: clamp(g / area),
				B: clamp(bl / area),
				A: clamp(a / area),
			})
		}
	}
	return dst
}

func clamp(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
