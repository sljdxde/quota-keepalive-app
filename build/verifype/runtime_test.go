package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCheckEmbeddedRuntime(t *testing.T) {
	all := []byte("prefix assets/bundle.json assets/bun.zst " +
		"assets/adapters.tar.zst assets/provider-plugins.tar.zst suffix")
	var buf bytes.Buffer
	if fails := checkEmbeddedRuntime(&buf, all); len(fails) != 0 {
		t.Fatalf("齐全时不应失败: %v", fails)
	}
	if !strings.Contains(buf.String(), "内嵌运行时资源齐全") {
		t.Fatalf("应打印通过项: %q", buf.String())
	}

	buf.Reset()
	// 只有部分资源：bun 在，但适配器/插件缺失（例如 embedassets 参数不全）。
	partial := []byte("assets/bundle.json assets/bun.zst")
	fails := checkEmbeddedRuntime(&buf, partial)
	if len(fails) != 1 {
		t.Fatalf("缺资源时必须失败: %v", fails)
	}
	msg := fails[0]
	for _, want := range []string{"adapters.tar.zst", "provider-plugins.tar.zst"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("失败项应点名缺失的资源 %q: %v", want, msg)
		}
	}
	if strings.Contains(msg, "bun.zst") {
		t.Fatalf("已存在的 bun.zst 不应被列为缺失: %v", msg)
	}
	if !strings.Contains(buf.String(), "安装时必然在") {
		t.Fatalf("失败提示应说明后果: %q", buf.String())
	}

	buf.Reset()
	// 完全没内嵌（普通 go build）：正是用户遇到的坏包。
	if fails := checkEmbeddedRuntime(&buf, []byte("MZ ordinary build")); len(fails) != 1 {
		t.Fatalf("未内嵌时必须失败: %v", fails)
	}
}
