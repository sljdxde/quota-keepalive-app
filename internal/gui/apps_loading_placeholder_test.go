package gui

import (
	"strings"
	"testing"
)

// 回归保护：应用管理页首次渲染会先写入「正在加载供应商…」占位框，然后
// appendChild 分组。如果成功拿到数据后不清空 body，占位框会永久留在列表
// 上方——看起来像「一直在加载供应商」，实际数据早就到了。
func TestAppsBodyClearsLoadingPlaceholder(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)

	start := strings.Index(page, "async function renderApps()")
	if start < 0 {
		t.Fatal("找不到 renderApps")
	}
	rest := page[start:]
	end := strings.Index(rest, "function providerRow(")
	if end < 0 {
		t.Fatal("renderApps 结构变化，无法定位函数体")
	}
	body := rest[:end]

	// 占位框必须存在（否则冷启动时用户看到空白，误以为坏了）。
	if !strings.Contains(body, "正在加载供应商") {
		t.Error("首次渲染仍应显示「正在加载供应商…」占位")
	}
	// 成功分支必须清空 body：await 之后、appendChild 之前。
	awaitIdx := strings.Index(body, "await Bound.ProvidersList()")
	if awaitIdx < 0 {
		t.Fatal("找不到 ProvidersList 调用")
	}
	after := body[awaitIdx:]
	clearIdx := strings.Index(after, `body.innerHTML = ""`)
	firstAppend := strings.Index(after, "body.appendChild(")
	if clearIdx < 0 {
		t.Fatal("成功拿到数据后没有清空 apps-body：占位框会永久残留")
	}
	if firstAppend >= 0 && clearIdx > firstAppend {
		t.Fatal("清空必须发生在 appendChild 之前，否则占位框仍会被保留")
	}
	// 清空必须紧跟在成功分支里（catch 之前）。
	catchIdx := strings.Index(after, "} catch (")
	if catchIdx >= 0 && clearIdx > catchIdx {
		t.Fatal("清空应写在 try 成功分支内，而不是 catch 之后")
	}
}
