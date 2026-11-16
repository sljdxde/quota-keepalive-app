//go:build darwin && !ios

package gui

/*
#cgo CFLAGS: -mmacosx-version-min=10.13 -x objective-c
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
#import <Cocoa/Cocoa.h>

// menuBarStringWidth 返回 text 在菜单栏字体下的宽度（点）。
static double menuBarStringWidth(const char *text, NSFont *font) {
	if (text == NULL || font == NULL) {
		return 0;
	}
	NSString *value = [NSString stringWithUTF8String:text];
	if (value == nil) {
		return 0;
	}
	return [value sizeWithAttributes:@{NSFontAttributeName: font}].width;
}

// menuBarMeasure 量出 visible 这条文案的「文字宽度」与按钮固有开销
// （图标 + 内边距 + 图文间距）。开销不写死：临时把状态项恢复成自适应宽度、
// 设一个没有补白的标题，读回按钮真实 frame。全过程在同一个主线程 turn 内
// 完成，用户看不到中间态，也不依赖特定 macOS 版本的内边距常数。
static int menuBarMeasure(void *item, const char *visible,
                          double *textWidth, double *overhead) {
	if (item == NULL || visible == NULL) {
		return 0;
	}
	__block int ok = 0;
	void (^measure)(void) = ^{
		NSStatusItem *statusItem = (NSStatusItem *)item;
		NSStatusBarButton *button = statusItem.button;
		if (button == nil) {
			return;
		}
		NSFont *font = button.font ?: [NSFont menuBarFontOfSize:0];
		NSString *text = [NSString stringWithUTF8String:visible];
		if (text == nil) {
			return;
		}
		double textW = menuBarStringWidth(visible, font);

		// 彩色标签走 attributedTitle、普通标签走 title；两者都要还原，
		// 否则量宽会把用户正在看的文案与颜色冲掉。
		NSAttributedString *savedAttr = nil;
		if (button.attributedTitle.length > 0) {
			savedAttr = [[button.attributedTitle copy] autorelease];
		}
		NSString *savedTitle = [[button.title copy] autorelease];
		double savedLength = statusItem.length;

		statusItem.length = NSVariableStatusItemLength;
		button.title = text;
		[button layoutSubtreeIfNeeded];
		double needed = button.frame.size.width;

		if (savedAttr != nil) {
			button.attributedTitle = savedAttr;
		} else {
			button.title = savedTitle;
		}
		statusItem.length = savedLength;

		double extra = needed - textW;
		if (extra < 0) {
			extra = 0;
		}
		*textWidth = textW;
		*overhead = extra;
		ok = 1;
	};
	if ([NSThread isMainThread]) {
		measure();
	} else {
		dispatch_sync(dispatch_get_main_queue(), measure);
	}
	return ok;
}

// menuBarApply 把状态项宽度收紧到 width，并把 label 补到 width - overhead
// 的可见文字宽度：数字左缘固定，右侧最多留一个量化步长的空白。
// 返回 malloc 的字符串，调用方负责释放。
static char *menuBarApply(void *item, const char *label, double width,
                          double textWidth, double overhead) {
	if (item == NULL || label == NULL || width <= 0) {
		return NULL;
	}
	__block char *result = NULL;
	void (^apply)(void) = ^{
		NSStatusItem *statusItem = (NSStatusItem *)item;
		NSStatusBarButton *button = statusItem.button;
		if (button == nil) {
			return;
		}
		NSFont *font = button.font ?: [NSFont menuBarFontOfSize:0];
		double need = (width - overhead) - textWidth;
		if (need < 0) {
			need = 0;
		}
		// 空格 / thin space / hair space 三种宽度组合，误差控制在 0.5pt 以内。
		double spaceWidth = menuBarStringWidth(" ", font);
		double thinWidth = menuBarStringWidth("\xE2\x80\x89", font);
		double hairWidth = menuBarStringWidth("\xE2\x80\x8A", font);
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
			return;
		}
		memcpy(out, label, labelLength);
		memcpy(out + labelLength, pad, (size_t)padLength);
		out[labelLength + (size_t)padLength] = '\0';

		if (statusItem.length != width) {
			statusItem.length = width;
		}
		button.alignment = NSTextAlignmentLeft;
		result = out;
	};
	if ([NSThread isMainThread]) {
		apply();
	} else {
		dispatch_sync(dispatch_get_main_queue(), apply);
	}
	return result;
}
*/
import "C"

import (
	"reflect"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/applog"
)

// applyMenuBarLayout 让状态项宽度跟着当前标签走，并把标签补到刚好占满它。
//
// 旧实现按「所有样式里最宽的文案（1.00k tok/s）」永久定宽，再把每条标签
// 补满到那个宽度：空闲的「0 tok/s」右侧就挂着几十 pt 看不见的空白，
// 「只显示数字」也省不下空间。现在宽度按当前可见文案实测、按步长量化，
// 只保留一个步长以内的补白，样式切换与伙伴图标变化都自动跟随。
//
// 文案不变时直接复用上轮补白标签，不重复触发 AppKit 排版。
func applyMenuBarLayout(tray *application.SystemTray, label, visible string, now time.Time) string {
	if tray == nil || label == "" {
		return label
	}
	item := statusItemPointer(tray)
	if item == nil {
		// 状态项还没创建（或 Wails 内部结构变化）时保持自适应宽度、不补白，
		// 本身就是最省空间的状态；下一轮刷新会再试。
		return label
	}

	menuBarWidthState.mu.Lock()
	if menuBarWidthState.ready && menuBarWidthState.padded != "" &&
		menuBarWidthState.visible == visible && menuBarWidthState.label == label {
		width, narrowSince := menuBarDecideWidth(
			menuBarWidthState.width, menuBarWidthState.target, menuBarWidthState.narrowSince, now)
		menuBarWidthState.narrowSince = narrowSince
		if width == menuBarWidthState.width {
			padded := menuBarWidthState.padded
			menuBarWidthState.mu.Unlock()
			return padded
		}
		// 变窄观察期到期：沿用上轮实测数据，只重设宽度并重新补白。
		menuBarWidthState.width = width
		textWidth := menuBarWidthState.textWidth
		overhead := menuBarWidthState.overhead
		menuBarWidthState.mu.Unlock()
		return menuBarApplyLabel(item, label, visible, width, textWidth, overhead)
	}
	menuBarWidthState.mu.Unlock()

	textWidth, overhead, ok := menuBarMeasureLabel(item, visible)
	if !ok {
		return label
	}
	target := menuBarQuantizedWidth(textWidth + overhead)

	menuBarWidthState.mu.Lock()
	firstLayout := !menuBarWidthState.ready
	width, narrowSince := menuBarDecideWidth(
		menuBarWidthState.width, target, menuBarWidthState.narrowSince, now)
	menuBarWidthState.width = width
	menuBarWidthState.target = target
	menuBarWidthState.narrowSince = narrowSince
	menuBarWidthState.ready = true
	menuBarWidthState.visible = visible
	menuBarWidthState.label = label
	menuBarWidthState.textWidth = textWidth
	menuBarWidthState.overhead = overhead
	menuBarWidthState.padded = "" // 新文案不能复用旧补白结果
	menuBarWidthState.mu.Unlock()

	if firstLayout {
		applog.Infof("gui", "菜单栏速度条按当前文案收紧宽度 %.0fpt（可见文案 %q，图标与内边距 %.0fpt）", width, visible, overhead)
	}
	return menuBarApplyLabel(item, label, visible, width, textWidth, overhead)
}

// menuBarMeasureLabel 调用 C 侧实测文字宽度与按钮固有开销。
func menuBarMeasureLabel(item unsafe.Pointer, visible string) (textWidth, overhead float64, ok bool) {
	cVisible := C.CString(visible)
	defer C.free(unsafe.Pointer(cVisible))
	var text, extra C.double
	if C.menuBarMeasure(item, cVisible, &text, &extra) == 0 {
		return 0, 0, false
	}
	return float64(text), float64(extra), true
}

// menuBarApplyLabel 把宽度与补白写回状态项，并缓存结果供后续刷新复用。
func menuBarApplyLabel(item unsafe.Pointer, label, visible string, width, textWidth, overhead float64) string {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	padded := C.menuBarApply(item, cLabel, C.double(width), C.double(textWidth), C.double(overhead))
	if padded == nil {
		// 写回失败就作废缓存：下一轮重新量，别复用旧文案的补白结果。
		menuBarWidthState.mu.Lock()
		menuBarWidthState.ready = false
		menuBarWidthState.mu.Unlock()
		return label
	}
	result := C.GoString(padded)
	C.free(unsafe.Pointer(padded))

	menuBarWidthState.mu.Lock()
	if menuBarWidthState.ready && menuBarWidthState.visible == visible && menuBarWidthState.label == label {
		menuBarWidthState.padded = result
	}
	menuBarWidthState.mu.Unlock()
	return result
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
