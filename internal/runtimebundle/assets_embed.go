//go:build embeddedruntime

package runtimebundle

import (
	"embed"
	"io/fs"
)

// assetsFS 是 build/embedassets 生成的 bun.zst / adapters.tar.zst /
// provider-plugins.tar.zst / bundle.json。生成资源后再用
// -tags embeddedruntime 构建，这些文件就会随 exe 一起发布。
//
//go:embed assets/*
var assetsFS embed.FS

// embeddedAssets 让 Available 与 Ensure 看到内嵌资源。embed.FS 的路径带
// assets/ 前缀，Sub 之后资源名就是 bun.zst 这类裸文件名。
func embeddedAssets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return nil
	}
	return sub
}
