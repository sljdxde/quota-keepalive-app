package main

import (
	"bytes"
	"fmt"
	"io"
)

// embeddedAssetMarkers 是 runtimebundle 里 //go:embed assets/* 的表项名。
// 这些字符串会被打进带 -tags embeddedruntime 的 exe：Go 会把 embed 文件的
// 相对路径写进二进制。反之，漏掉标签或漏跑 build/embedassets 的构建里不会有
// 它们——那正是「GUI 版能弹安装向导、却在解压运行时一步必然失败」的坏包。
//
// 四个都查：bun.zst 是主体（约 53MB），其余是适配器与供应商插件；缺任何一个
// 装出来的程序都不完整。
var embeddedAssetMarkers = []string{
	"assets/bundle.json",
	"assets/bun.zst",
	"assets/adapters.tar.zst",
	"assets/provider-plugins.tar.zst",
}

// checkEmbeddedRuntime 校验 exe 是否真的内嵌了运行时分包，返回失败项描述。
// data 是整个 exe 的字节：运行时资源是压缩后的二进制，但 embed 的路径名
// 以明文存在于二进制里，足以判断资源有没有被链接进来。
func checkEmbeddedRuntime(w io.Writer, data []byte) []string {
	var missing []string
	for _, marker := range embeddedAssetMarkers {
		if !bytes.Contains(data, []byte(marker)) {
			missing = append(missing, marker)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "  FAIL 未内嵌运行时资源（缺 %v）：这个 exe 安装时必然在"+
			"「解压运行时」一步失败；请用 make build-windows 或 "+
			"scripts/build-windows-exe.sh 构建（内嵌 + -tags embeddedruntime）\n", missing)
		return []string{fmt.Sprintf("未内嵌运行时资源：缺 %v", missing)}
	}
	fmt.Fprintf(w, "  ok   内嵌运行时资源齐全（%d 项）\n", len(embeddedAssetMarkers))
	return nil
}
