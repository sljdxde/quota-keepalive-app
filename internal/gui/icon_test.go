package gui

import (
	"bytes"
	"image"
	"image/png"
	"reflect"
	"testing"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestTemplateIconHasNoRed(t *testing.T) {
	img := decode(t, templateIcon())
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r != g || g != b {
				t.Fatal("模板图标应为单色，不含彩色（红）像素")
			}
		}
	}
}

// trayIconFields 读出 Wails SystemTray 里已写入的图标与模板标记（字段未导出）。
func trayIconFields(t *testing.T, tray *application.SystemTray) (icon []byte, template bool) {
	t.Helper()
	value := reflect.ValueOf(tray).Elem()
	iconField := value.FieldByName("icon")
	templateField := value.FieldByName("isTemplateIcon")
	if !iconField.IsValid() || !templateField.IsValid() {
		t.Fatal("Wails SystemTray 字段已变化，测试需要更新")
	}
	icon = reflect.NewAt(iconField.Type(), unsafe.Pointer(iconField.UnsafeAddr())).
		Elem().Interface().([]byte)
	template = reflect.NewAt(templateField.Type(), unsafe.Pointer(templateField.UnsafeAddr())).
		Elem().Bool()
	return icon, template
}

// macOS 用模板图标让系统按菜单栏深浅色着色；Windows 必须用彩色应用图标，
// 否则深色单色 glyph 会直接画在深色任务栏上，看起来漆黑一片。
func TestTrayIconFollowsPlatform(t *testing.T) {
	icon, template := trayIcon("darwin")
	if !template {
		t.Error("macOS 托盘图标应为模板图标")
	}
	if !bytes.Equal(icon, templateIcon()) {
		t.Error("macOS 托盘图标应使用单色模板资源")
	}

	icon, template = trayIcon("windows")
	if template {
		t.Error("Windows 托盘图标不应是模板图标")
	}
	if len(icon) == 0 || bytes.Equal(icon, templateIcon()) {
		t.Error("Windows 托盘图标应使用彩色应用图标，而不是模板 glyph")
	}
}

func TestApplyTrayIconWritesPlatformChoice(t *testing.T) {
	app := New(Deps{})
	// 用一条全新的托盘验证写入行为：New() 里的 app.tray 已经装过模板图标，
	// 而 Wails 的 SetIcon 不会清掉 isTemplateIcon 标记，直接复用会误判。
	tray := app.wails.SystemTray.New()

	applyTrayIcon(tray, "windows")
	icon, template := trayIconFields(t, tray)
	if template || !bytes.Equal(icon, applicationIcon(64)) {
		t.Fatalf("Windows 应写入彩色图标: template=%v len=%d", template, len(icon))
	}

	applyTrayIcon(tray, "darwin")
	icon, template = trayIconFields(t, tray)
	if !template || !bytes.Equal(icon, templateIcon()) {
		t.Fatalf("macOS 应写入模板图标: template=%v len=%d", template, len(icon))
	}
}
