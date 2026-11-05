package brandicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestApplicationIconScalesToRequestedSize(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Application(64)))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("应用图标尺寸 = %v，期望 64×64", img.Bounds())
	}
}

func TestTemplateIconIsMonochrome(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Template()))
	if err != nil {
		t.Fatal(err)
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r != g || g != b {
				t.Fatalf("模板图标 (%d,%d) 不是单色", x, y)
			}
		}
	}
}

func TestTemplateIconKeepsOpticalMargin(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Template()))
	if err != nil {
		t.Fatal(err)
	}
	bbox := image.Rectangle{}
	seen := false
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			point := image.Rect(x, y, x+1, y+1)
			if !seen {
				bbox = point
				seen = true
				continue
			}
			bbox = bbox.Union(point)
		}
	}
	if !seen {
		t.Fatal("菜单栏模板图标为空")
	}
	// The glyph must read as a Q in the menu bar (about 16-18pt at 2x), not
	// collapse into a dot, while still keeping an optical margin on all sides.
	if bbox.Dx() < 28 || bbox.Dy() < 26 || bbox.Dx() > 36 || bbox.Dy() > 36 {
		t.Fatalf("菜单栏 Q 尺寸应在 28-36px 区间，实际 bbox=%v", bbox)
	}
	if bbox.Min.X < 4 || bbox.Min.Y < 4 || bbox.Max.X > 40 || bbox.Max.Y > 40 {
		t.Fatalf("菜单栏 Q 应保留光学边距，实际 bbox=%v", bbox)
	}
}

func TestApplicationIconHasVisibleOuterRim(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Application(64)))
	if err != nil {
		t.Fatal(err)
	}

	rim := color.RGBAModel.Convert(img.At(4, 32)).(color.RGBA)
	interior := color.RGBAModel.Convert(img.At(7, 32)).(color.RGBA)
	rimLuma := luminance(rim)
	interiorLuma := luminance(interior)
	if rimLuma < 95 || rimLuma < interiorLuma+30 {
		t.Fatalf("应用图标外缘不够清晰：rim=%.1f interior=%.1f", rimLuma, interiorLuma)
	}
}

func luminance(c color.RGBA) float64 {
	return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
}

func TestPetTemplatesAreCleanMonochromeMarks(t *testing.T) {
	ids := PetTemplateIDs()
	if len(ids) != 4 {
		t.Fatalf("宠物模板数量 = %d，期望 4", len(ids))
	}
	seen := make(map[string]string, len(ids))
	for _, id := range ids {
		data, ok := PetTemplate(id)
		if !ok || len(data) == 0 {
			t.Fatalf("宠物 %s 缺少模板图标", id)
		}
		if previous, dup := seen[string(data)]; dup {
			t.Fatalf("宠物 %s 与 %s 模板完全相同", id, previous)
		}
		seen[string(data)] = id

		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 44 || img.Bounds().Dy() != 44 {
			t.Fatalf("宠物 %s 模板尺寸 = %v，期望 44×44", id, img.Bounds())
		}
		bbox := image.Rectangle{}
		seenInk := false
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if r != g || g != b {
					t.Fatalf("宠物 %s 模板 (%d,%d) 不是单色", id, x, y)
				}
				if a == 0 {
					continue
				}
				point := image.Rect(x, y, x+1, y+1)
				if !seenInk {
					bbox, seenInk = point, true
				} else {
					bbox = bbox.Union(point)
				}
			}
		}
		if !seenInk {
			t.Fatalf("宠物 %s 模板为空", id)
		}
		// 菜单栏 22pt 图标：字形要够大，又不能顶到边缘（系统会裁切/贴边）。
		if bbox.Dx() < 26 || bbox.Dy() < 26 || bbox.Dx() > 38 || bbox.Dy() > 38 {
			t.Fatalf("宠物 %s 字形尺寸越界: bbox=%v", id, bbox)
		}
		if bbox.Min.X < 2 || bbox.Min.Y < 2 || bbox.Max.X > 42 || bbox.Max.Y > 42 {
			t.Fatalf("宠物 %s 字形缺少光学边距: bbox=%v", id, bbox)
		}
	}
	if _, ok := PetTemplate("missingno"); ok {
		t.Fatal("未知宠物不应返回模板图标")
	}
}
