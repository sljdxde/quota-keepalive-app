//go:build darwin && !ios

package gui

/*
#cgo CFLAGS: -mmacosx-version-min=10.13 -x objective-c
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
#import <Cocoa/Cocoa.h>

// menuBarWidthSamples 是菜单栏需要同时容纳的几种文案形态：纯文本与分段彩色
// 带「tok/s」，只显示数字时最窄。按最宽者定宽，切换样式或位数变化时都不会
// 改变条目宽度。
static const char *kMenuBarWidthSamples[] = {
	"0 tok/s",
	"88.8 tok/s",
	"999 tok/s",
	"1.00k tok/s",
	"888",
};

// menuBarWidestLabelWidth 返回容纳所有样例文案所需的状态项宽度（点）：
// 图标宽度 + 文字宽度 + 内边距。读 AppKit，必须在主线程执行；非主线程时
// 同步切到主线程再量（只在安装时调用一次）。
static double menuBarWidestLabelWidth(void *item) {
	if (item == NULL) {
		return 0;
	}
	__block double widest = 0;
	void (^measure)(void) = ^{
		NSStatusItem *statusItem = (NSStatusItem *)item;
		NSStatusBarButton *button = statusItem.button;
		if (button == nil) {
			return;
		}
		NSFont *font = button.font ?: [NSFont menuBarFontOfSize:0];
		// Wails 会把模板图缩到菜单栏厚度；图标还没装上时按厚度预留，宁宽勿窄。
		CGFloat iconWidth = button.image != nil
			? button.image.size.width
			: [NSStatusBar systemStatusBar].thickness;
		// 图标与文字间距（约 4pt）加按钮左右内边距；窄了标题会被截断成「…」。
		CGFloat extra = iconWidth + 14;
		int count = (int)(sizeof(kMenuBarWidthSamples) / sizeof(kMenuBarWidthSamples[0]));
		for (int i = 0; i < count; i++) {
			NSString *text = [NSString stringWithUTF8String:kMenuBarWidthSamples[i]];
			if (text == nil) {
				continue;
			}
			CGFloat width = [text sizeWithAttributes:@{NSFontAttributeName: font}].width + extra;
			if (width > widest) {
				widest = width;
			}
		}
	};
	if ([NSThread isMainThread]) {
		measure();
	} else {
		dispatch_sync(dispatch_get_main_queue(), measure);
	}
	return widest;
}

// menuBarStringWidth 返回 text 在菜单栏字体下的宽度（点）。
static double menuBarStringWidth(const char *text) {
	if (text == NULL) {
		return 0;
	}
	NSString *value = [NSString stringWithUTF8String:text];
	if (value == nil) {
		return 0;
	}
	NSFont *font = [NSFont menuBarFontOfSize:0];
	return [value sizeWithAttributes:@{NSFontAttributeName: font}].width;
}

// menuBarPaddedLabel 在 label 末尾补空白，使它的可见文字宽度与最宽样例一致。
// 状态项会把「图标 + 标题」整体居中，标题宽度每变一位数字就整体左右挪几像素；
// 把每条标签补到同一宽度后，内容宽度恒定，数字左缘就固定在同一个位置。
// visible 是不含 ANSI 转义码的可见文字，只用于量宽；返回 malloc 的字符串。
static char *menuBarPaddedLabel(const char *label, const char *visible) {
	if (label == NULL || visible == NULL) {
		return NULL;
	}
	double target = 0;
	int count = (int)(sizeof(kMenuBarWidthSamples) / sizeof(kMenuBarWidthSamples[0]));
	for (int i = 0; i < count; i++) {
		double width = menuBarStringWidth(kMenuBarWidthSamples[i]);
		if (width > target) {
			target = width;
		}
	}
	double need = target - menuBarStringWidth(visible);
	// 空格 / thin space / hair space 三种宽度组合，误差控制在 0.5pt 以内。
	double spaceWidth = menuBarStringWidth(" ");
	double thinWidth = menuBarStringWidth("\xE2\x80\x89");
	double hairWidth = menuBarStringWidth("\xE2\x80\x8A");
	char pad[128];
	int padLength = 0;
	while (need >= spaceWidth && padLength < (int)sizeof(pad) - 4) {
		pad[padLength++] = ' ';
		need -= spaceWidth;
	}
	while (need >= thinWidth && padLength < (int)sizeof(pad) - 4) {
		pad[padLength++] = '\xE2';
		pad[padLength++] = '\x80';
		pad[padLength++] = '\x89';
		need -= thinWidth;
	}
	while (need >= hairWidth && padLength < (int)sizeof(pad) - 4) {
		pad[padLength++] = '\xE2';
		pad[padLength++] = '\x80';
		pad[padLength++] = '\x8A';
		need -= hairWidth;
	}
	size_t labelLength = strlen(label);
	char *out = malloc(labelLength + (size_t)padLength + 1);
	if (out == NULL) {
		return NULL;
	}
	memcpy(out, label, labelLength);
	memcpy(out + labelLength, pad, (size_t)padLength);
	out[labelLength + (size_t)padLength] = '\0';
	return out;
}

// menuBarApplyFixedWidth 固定状态项宽度并让内容左对齐：
// 宽度固定后，数字从 2 位变 3 位、切到「1.00k」或只显示数字时，整个菜单栏
// 条目都不会再把左右相邻的状态项挤开；左对齐让数字左缘也保持不动。
static void menuBarApplyFixedWidth(void *item, double width) {
	if (item == NULL || width <= 0) {
		return;
	}
	dispatch_async(dispatch_get_main_queue(), ^{
		NSStatusItem *statusItem = (NSStatusItem *)item;
		if (statusItem == nil) {
			return;
		}
		statusItem.length = width;
		NSStatusBarButton *button = statusItem.button;
		if (button != nil) {
			button.alignment = NSTextAlignmentLeft;
		}
	});
}
*/
import "C"

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/applog"
)

var menuBarFixedWidth struct {
	mu   sync.Mutex
	done bool
}

// installMenuBarFixedWidth 给 macOS 菜单栏速度项设置固定宽度。
//
// 状态项由 Wails 在事件循环启动后创建，这里可能还拿不到原生指针：拿不到就
// 保持原样并返回，下一次标签刷新会再试（成功一次后不再重复）。
// 非 darwin 平台是空实现。
func installMenuBarFixedWidth(tray *application.SystemTray) {
	if tray == nil {
		return
	}
	menuBarFixedWidth.mu.Lock()
	if menuBarFixedWidth.done {
		menuBarFixedWidth.mu.Unlock()
		return
	}
	menuBarFixedWidth.mu.Unlock()

	item := statusItemPointer(tray)
	if item == nil {
		return
	}
	// 量宽度要读 AppKit：C 侧会同步切到主线程，调用方在哪个 goroutine 都可以。
	width := float64(C.menuBarWidestLabelWidth(item))
	if width <= 0 {
		return
	}
	C.menuBarApplyFixedWidth(item, C.double(width))
	menuBarFixedWidth.mu.Lock()
	menuBarFixedWidth.done = true
	menuBarFixedWidth.mu.Unlock()
	applog.Infof("gui", "菜单栏速度条固定宽度 %.0fpt（数字位数变化不再挤动相邻状态项）", width)
}

// padMenuBarLabel 把标签补到固定宽度的可见文字，避免数字位数变化时整条
// 内容在固定宽度里左右挪动。visible 是去掉 ANSI 转义码后的可见文字。
func padMenuBarLabel(label, visible string) string {
	if label == "" {
		return label
	}
	cLabel := C.CString(label)
	cVisible := C.CString(visible)
	defer C.free(unsafe.Pointer(cLabel))
	defer C.free(unsafe.Pointer(cVisible))
	padded := C.menuBarPaddedLabel(cLabel, cVisible)
	if padded == nil {
		return label
	}
	defer C.free(unsafe.Pointer(padded))
	return C.GoString(padded)
}

// statusItemPointer 从 Wails 的 SystemTray 里取出 NSStatusItem 指针。
//
// Wails v3 没暴露状态项宽度 API，macOS 侧只能通过私有字段拿到原生对象：
// SystemTray.impl（*macosSystemTray）→ nsStatusItem。这里用反射读字段而不是
// 断言具体类型名，字段缺失（未来 Wails 改结构）时返回 nil，调用方保持原样，
// 不会崩。go.mod 锁定了 v3.0.0，字段名与类型见 systemtray_darwin.go。
func statusItemPointer(tray *application.SystemTray) unsafe.Pointer {
	if tray == nil {
		return nil
	}
	value := reflect.ValueOf(tray)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	impl := value.Elem().FieldByName("impl")
	if !impl.IsValid() {
		return nil
	}
	// impl 是未导出字段：reflect.NewAt + unsafe 取得可读地址，不修改它。
	impl = reflect.NewAt(impl.Type(), unsafe.Pointer(impl.UnsafeAddr())).Elem()
	if impl.Kind() == reflect.Interface {
		if impl.IsNil() {
			return nil
		}
		impl = impl.Elem()
	}
	if impl.Kind() != reflect.Pointer || impl.IsNil() {
		return nil
	}
	field := impl.Elem().FieldByName("nsStatusItem")
	if !field.IsValid() || field.Kind() != reflect.UnsafePointer {
		return nil
	}
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	return unsafe.Pointer(field.Pointer())
}
