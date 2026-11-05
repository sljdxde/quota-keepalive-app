package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
)

func load(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	return img
}

type mask struct {
	w, h int
	data []bool
}

func newMask(w, h int) *mask { return &mask{w: w, h: h, data: make([]bool, w*h)} }
func (m *mask) at(x, y int) bool {
	if x < 0 || y < 0 || x >= m.w || y >= m.h {
		return false
	}
	return m.data[y*m.w+x]
}
func (m *mask) set(x, y int, v bool) { m.data[y*m.w+x] = v }
func inkBounds(src *mask) image.Rectangle {
	var box image.Rectangle
	seen := false
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			if !src.at(x, y) {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if !seen {
				box, seen = p, true
			} else {
				box = box.Union(p)
			}
		}
	}
	return box
}

func place(src *mask, size, margin int) *image.RGBA {
	box := inkBounds(src)
	w, h := box.Dx(), box.Dy()
	inner := size - margin*2
	scale := math.Min(float64(inner)/float64(w), float64(inner)/float64(h))
	dw, dh := int(math.Round(float64(w)*scale)), int(math.Round(float64(h)*scale))
	offX, offY := (size-dw)/2, (size-dh)/2
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			x0 := float64(box.Min.X) + float64(x)*float64(w)/float64(dw)
			x1 := float64(box.Min.X) + float64(x+1)*float64(w)/float64(dw)
			y0 := float64(box.Min.Y) + float64(y)*float64(h)/float64(dh)
			y1 := float64(box.Min.Y) + float64(y+1)*float64(h)/float64(dh)
			var covered, total float64
			for yy := int(math.Floor(y0)); yy < int(math.Ceil(y1)); yy++ {
				wy := math.Min(float64(yy+1), y1) - math.Max(float64(yy), y0)
				for xx := int(math.Floor(x0)); xx < int(math.Ceil(x1)); xx++ {
					wx := math.Min(float64(xx+1), x1) - math.Max(float64(xx), x0)
					total += wx * wy
					if src.at(xx, yy) {
						covered += wx * wy
					}
				}
			}
			a := 0.0
			if total > 0 {
				a = covered / total
			}
			dst.SetRGBA(offX+x, offY+y, color.RGBA{0, 0, 0, uint8(math.Round(a * 255))})
		}
	}
	return dst
}

type blob struct {
	area                           int
	cx, cy, minx, miny, maxx, maxy int
}

func components(src *mask) []blob {
	seen := make([]bool, src.w*src.h)
	out := []blob{}
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			if !src.at(x, y) || seen[y*src.w+x] {
				continue
			}
			stack := [][2]int{{x, y}}
			seen[y*src.w+x] = true
			b := blob{minx: x, miny: y, maxx: x, maxy: y}
			var sx, sy int
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				px, py := p[0], p[1]
				b.area++
				sx += px
				sy += py
				if px < b.minx {
					b.minx = px
				}
				if px > b.maxx {
					b.maxx = px
				}
				if py < b.miny {
					b.miny = py
				}
				if py > b.maxy {
					b.maxy = py
				}
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := px+d[0], py+d[1]
					if src.at(nx, ny) && !seen[ny*src.w+nx] {
						seen[ny*src.w+nx] = true
						stack = append(stack, [2]int{nx, ny})
					}
				}
			}
			b.cx, b.cy = sx/b.area, sy/b.area
			out = append(out, b)
		}
	}
	return out
}

// Command petgen derives the macOS menu bar template icons from the shipped 2D
// pet sprites (frontend/assets/pokemon/*.png). The sprite alpha becomes a bold
// silhouette (the outline is what reads at 22pt), with the eyes punched out so
// the icon still looks like the actual Pokémon. macOS recolours the template.
//
// Usage (from the repository root):
//
//	go run ./internal/brandicon/petgen
//
// drawBall renders the Poké Ball menu bar template directly in Go (4× super
// sampled, then box filtered) so the asset is reproducible without an SVG
// rasterizer: black ball, transparent band, black button with a white ring.
func drawBall() *image.RGBA {
	const ss = 4
	const size = 44
	big := ss * size
	img := image.NewRGBA(image.Rect(0, 0, big, big))
	px := func(unit float64) float64 { return unit / 24.0 * float64(big) }
	cx, cy := px(12), px(12)
	r := px(9.6) * 0.94
	for y := 0; y < big; y++ {
		for x := 0; x < big; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy > r*r {
				continue
			}
			img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	clearCircle := func(radius float64) {
		for y := 0; y < big; y++ {
			for x := 0; x < big; x++ {
				dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
				if dx*dx+dy*dy <= radius*radius {
					img.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
				}
			}
		}
	}
	clearRect := func(y0, y1 float64) {
		for y := int(y0); y < int(y1); y++ {
			for x := 0; x < big; x++ {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}
	clearRect(px(10.9), px(13.1))
	clearCircle(px(3.8))
	fillCircle := func(radius float64, alpha uint8) {
		for y := 0; y < big; y++ {
			for x := 0; x < big; x++ {
				dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
				if dx*dx+dy*dy <= radius*radius {
					img.SetRGBA(x, y, color.RGBA{0, 0, 0, alpha})
				}
			}
		}
	}
	fillCircle(px(2.4), 255)
	clearCircle(px(1.1))
	// 4× box downscale.
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sum float64
			for yy := 0; yy < ss; yy++ {
				for xx := 0; xx < ss; xx++ {
					sum += float64(img.RGBAAt(x*ss+xx, y*ss+yy).A)
				}
			}
			dst.SetRGBA(x, y, color.RGBA{0, 0, 0, uint8(math.Round(sum / (ss * ss)))})
		}
	}
	return dst
}

func main() {
	spriteDir := flag.String("sprites", "internal/gui/frontend/assets/pokemon", "directory with 2D pet sprites")
	outDir := flag.String("out", "internal/brandicon/assets/pets", "output directory for 44px template PNGs")
	flag.Parse()
	ballPath := filepath.Join(filepath.Dir(*outDir), "poke-ball-44.png")
	if f, err := os.Create(ballPath); err == nil {
		png.Encode(f, drawBall())
		f.Close()
	}
	for _, name := range []string{"bulbasaur", "charmander", "squirtle", "pikachu"} {
		img := load(filepath.Join(*spriteDir, name+".png"))
		b := img.Bounds()
		W, H := b.Dx(), b.Dy()
		body := newMask(W, H)
		dark := newMask(W, H)
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				r, g, bl, a := img.At(x+b.Min.X, y+b.Min.Y).RGBA()
				sa := float64(a) / 65535.0
				if sa < 0.25 {
					continue
				}
				body.set(x, y, true)
				rr, gg, bb := float64(r)/257, float64(g)/257, float64(bl)/257
				lum := (0.299*rr+0.587*gg+0.114*bb)*sa + 255*(1-sa)
				if lum < 120 {
					dark.set(x, y, true)
				}
			}
		}
		blobs := components(dark)
		cands := []blob{}
		for _, c := range blobs {
			if c.area < 80 || c.area > 3500 {
				continue
			}
			if c.cy > H*3/5 {
				continue
			}
			if c.cx < W/6 || c.cx > W*5/6 {
				continue
			}
			if c.maxx-c.minx+1 > 150 || c.maxy-c.miny+1 > 150 {
				continue
			}
			cands = append(cands, c)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].area > cands[j].area })
		if len(cands) > 2 {
			cands = cands[:2]
		}
		holes := newMask(W, H)
		for _, c := range cands {
			// 稍微放大眼睛洞口，22px 下才看得见。
			for y := c.miny - 3; y <= c.maxy+3; y++ {
				for x := c.minx - 3; x <= c.maxx+3; x++ {
					if dark.at(x, y) {
						holes.set(x, y, true)
					}
				}
			}
		}
		icon := newMask(W, H)
		for i := range icon.data {
			icon.data[i] = body.data[i] && !holes.data[i]
		}
		tpl := place(icon, 44, 3)
		fmt.Printf("%s eyes=%d bbox=%v\n", name, len(cands), inkBounds(maskFromAlpha(tpl)))
		f, _ := os.Create(filepath.Join(*outDir, name+".png"))
		png.Encode(f, tpl)
		f.Close()
	}
}
func maskFromAlpha(img *image.RGBA) *mask {
	m := newMask(img.Bounds().Dx(), img.Bounds().Dy())
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			if img.RGBAAt(x, y).A > 0 {
				m.set(x, y, true)
			}
		}
	}
	return m
}
