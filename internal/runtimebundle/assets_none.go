//go:build !embeddedruntime

package runtimebundle

import "io/fs"

// embeddedAssets 在没有 embeddedruntime 标签的普通构建里恒为空：
// Available 返回 false，Ensure 返回 ErrUnavailable，不会碰文件系统。
func embeddedAssets() fs.FS {
	return nil
}
