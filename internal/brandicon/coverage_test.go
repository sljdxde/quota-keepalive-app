package brandicon

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestCoverageHelpers(t *testing.T) {
	if len(MasterPNG()) == 0 {
		t.Fatal("MasterPNG should return the embedded application icon")
	}

	img, err := png.Decode(bytes.NewReader(Application(0)))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("non-positive sizes should clamp to 1x1, got %v", img.Bounds())
	}
	if len(Application(1024)) == 0 {
		t.Fatal("an icon already at the requested size should still be encoded")
	}
}

func TestCoverageInvalidImages(t *testing.T) {
	t.Run("decode panic", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("invalid embedded image data should panic")
			}
		}()
		decodeRGBA([]byte("not a PNG"))
	})

	t.Run("encode panic", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("an empty image should fail PNG encoding")
			}
		}()
		encodePNG(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	})
}

// ApplicationICO 必须产出合法的多尺寸 ICO：ICONDIR + 每档一条目录项，条目
// 数据是完整 PNG（PNG-in-ICO，Vista+），且尺寸与目录声明一致。Windows 托盘
// 依赖「精确尺寸档位」避免 GDI 二次缩放导致的像素感。
func TestApplicationICOMultiSizeEntries(t *testing.T) {
	ico := ApplicationICO()
	if len(ico) < 6 || ico[0] != 0 || ico[1] != 0 || ico[2] != 1 || ico[3] != 0 {
		t.Fatalf("ApplicationICO 缺少 ICONDIR 头: % x", ico[:min(6, len(ico))])
	}
	count := int(ico[4]) | int(ico[5])<<8
	if count != 7 {
		t.Fatalf("ICO 应包含 7 个尺寸档位，实际 %d", count)
	}
	const dirEntry = 16
	offset := 6 + count*dirEntry
	for i := 0; i < count; i++ {
		entry := ico[6+i*dirEntry : 6+(i+1)*dirEntry]
		wantSize := []int{16, 20, 24, 28, 32, 48, 64}[i]
		if got := int(entry[0]); got != wantSize {
			t.Fatalf("档位 %d 宽度应为 %d，实际 %d", i, wantSize, got)
		}
		size := int(entry[8]) | int(entry[9])<<8 | int(entry[10])<<16 | int(entry[11])<<24
		data := ico[offset : offset+size]
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("档位 %d 条目不是合法 PNG: %v", i, err)
		}
		if img.Bounds().Dx() != wantSize || img.Bounds().Dy() != wantSize {
			t.Fatalf("档位 %d PNG 尺寸应为 %d，实际 %d", i, wantSize, img.Bounds().Dx())
		}
		offset += size
	}
	if offset != len(ico) {
		t.Fatalf("ICO 数据长度不符：目录声明到 %d，实际 %d", offset, len(ico))
	}
}
