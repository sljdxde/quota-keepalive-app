package gui

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// resolveBarDrop 是拖放落点的唯一判定：真实坐标进、真实落点出，表驱动覆盖
// 全部四种落位分支（浮动态→贴边、贴边态→换边、贴边态→拖回浮动态、
// 浮动态→留在浮动态）。

func TestResolveBarDropDocksNearAnyEdge(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	cases := []struct {
		name     string
		x, y     int
		wantSide string
	}{
		{name: "左边缘", x: 4, y: 400, wantSide: ballDockLeft},
		{name: "右边缘", x: area.Width - ballSize - 4, y: 400, wantSide: ballDockRight},
		{name: "上边缘", x: 400, y: 4, wantSide: ballDockTop},
		{name: "下边缘", x: 400, y: area.Height - ballSize - 4, wantSide: ballDockBottom},
	}
	for _, test := range cases {
		drop := resolveBarDrop(area, test.x, test.y, ballSize, "", 0, 0)
		if drop.Action != "dock" {
			t.Errorf("%s：应吸附，实际 %q", test.name, drop.Action)
		}
		if drop.Docked != test.wantSide {
			t.Errorf("%s：贴边侧应为 %q，实际 %q", test.name, test.wantSide, drop.Docked)
		}
		if drop.Size != ballDockSize {
			t.Errorf("%s：贴边后尺寸应为 %d，实际 %d", test.name, ballDockSize, drop.Size)
		}
	}
}

func TestResolveBarDropStaysFloatingInTheMiddle(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	drop := resolveBarDrop(area, 400, 300, ballSize, "", 0, 0)
	if drop.Action != "float" {
		t.Fatalf("中间位置应保持浮动态，实际 %q", drop.Action)
	}
	if drop.Size != ballSize {
		t.Fatalf("浮动态尺寸应为 %d，实际 %d", ballSize, drop.Size)
	}
	if drop.Docked != "" {
		t.Fatalf("浮动态不应带贴边侧，实际 %q", drop.Docked)
	}
	if drop.X != 400 || drop.Y != 300 {
		t.Fatalf("浮动态落点应保持原位，实际 %d,%d", drop.X, drop.Y)
	}
}

func TestResolveBarDropDocksWhenDraggedPastTheEdge(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 拖到右下角之外：按「最近的一条边」吸附，右下角归右边缘
	// （平手时水平边优先，不会突然跑到上边）。
	drop := resolveBarDrop(area, 990, 790, ballSize, "", 0, 0)
	if drop.Action != "dock" {
		t.Fatalf("拖出边界应吸附，实际 %q", drop.Action)
	}
	if drop.Docked != ballDockRight {
		t.Fatalf("右下角应贴到最近的右边缘，实际 %q", drop.Docked)
	}
	// 只越过右边（非角落）：贴右边。
	drop = resolveBarDrop(area, 990, 400, ballSize, "", 0, 0)
	if drop.Docked != ballDockRight {
		t.Fatalf("越过右边界应贴到右边缘，实际 %q", drop.Docked)
	}
}

func TestResolveBarDropSwitchesEdgeWhileDocked(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 已贴左边，拖到右边：换边继续贴，并保留之前的恢复坐标。
	drop := resolveBarDrop(area, area.Width-ballDockSize-2, 300, ballDockSize, ballDockLeft, 120, 240)
	if drop.Action != "dock" || drop.Docked != ballDockRight {
		t.Fatalf("贴边态拖到另一条边应换边贴住，实际 %q/%q", drop.Action, drop.Docked)
	}
	if drop.RestoreX != 120 || drop.RestoreY != 240 {
		t.Fatalf("换边应保留恢复坐标，实际 %d,%d", drop.RestoreX, drop.RestoreY)
	}
}

func TestResolveBarDropLeavesDockWhenDraggedAway(t *testing.T) {
	area := application.Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	// 已贴左边，拖到中间：恢复 64px 浮动态，球心保持在松手位置附近。
	drop := resolveBarDrop(area, 400, 300, ballDockSize, ballDockLeft, 120, 240)
	if drop.Action != "float" {
		t.Fatalf("拖离边缘应恢复浮动态，实际 %q", drop.Action)
	}
	if drop.Size != ballSize {
		t.Fatalf("恢复后尺寸应为 %d，实际 %d", ballSize, drop.Size)
	}
	if drop.Docked != "" {
		t.Fatalf("恢复后不应带贴边侧，实际 %q", drop.Docked)
	}
	// 44 → 64 时多出的一圈平均分到两侧：X 比松手点偏小 10px。
	if drop.X != 390 || drop.Y != 290 {
		t.Fatalf("恢复落点应为 390,290，实际 %d,%d", drop.X, drop.Y)
	}
}

func TestScreenWorkAreaFallsBackToBounds(t *testing.T) {
	// 有工作区就用工作区。
	screen := &application.Screen{
		WorkArea: application.Rect{X: 10, Y: 20, Width: 800, Height: 600},
		Bounds:   application.Rect{X: 0, Y: 0, Width: 1024, Height: 768},
	}
	area := screenWorkArea(screen)
	if area != screen.WorkArea {
		t.Fatalf("应返回工作区，实际 %+v", area)
	}
	// 工作区为空（某些虚拟屏）时退回屏幕边界。
	screen.WorkArea = application.Rect{}
	area = screenWorkArea(screen)
	if area != screen.Bounds {
		t.Fatalf("应回退到屏幕边界，实际 %+v", area)
	}
	// nil 屏幕返回零矩形。
	if area := screenWorkArea(nil); area != (application.Rect{}) {
		t.Fatalf("nil 屏幕应返回零矩形，实际 %+v", area)
	}
}
