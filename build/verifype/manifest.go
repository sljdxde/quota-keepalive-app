package main

import (
	"bytes"
	"strings"
	"unicode/utf16"
)

// decodeManifestText 把 RT_MANIFEST 数据解成文本：先去 BOM（UTF-8/UTF-16LE/
// UTF-16BE），无 BOM 但字节分布呈 UTF-16LE 特征时也按 UTF-16 解，否则当
// UTF-8。go-winres 生成的是 UTF-8 XML，但手工改过的资源可能是 UTF-16。
func decodeManifestText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xef, 0xbb, 0xbf}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xff, 0xfe}):
		return decodeUTF16LE(b[2:])
	case bytes.HasPrefix(b, []byte{0xfe, 0xff}):
		swapped := make([]byte, len(b)-2)
		for i := 2; i+1 < len(b); i += 2 {
			swapped[i-2], swapped[i-1] = b[i+1], b[i]
		}
		return decodeUTF16LE(swapped)
	case looksUTF16LE(b):
		return decodeUTF16LE(b)
	default:
		return string(b)
	}
}

func decodeUTF16LE(b []byte) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = le16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(units))
}

// looksUTF16LE：偶数长度且过半字符单元高字节为 0，判为 UTF-16LE。UTF-8 的
// XML 里几乎不会有 NUL 字节；纯 ASCII 的 UTF-16 文本则每个单元高字节都是 0。
func looksUTF16LE(b []byte) bool {
	if len(b) < 16 || len(b)%2 != 0 {
		return false
	}
	hiZero := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			hiZero++
		}
	}
	return hiZero*2 > len(b)/2
}

// containsIdentity 检查 manifest 文本里是否出现期望的 identity。go-winres 会
// 把 winres.json 的 identity.name 写进 manifest 的 assemblyIdentity，做子串
// 比对即可，不解析 XML（真实 manifest 带各种命名空间与属性顺序）。
func containsIdentity(text, identity string) bool {
	return strings.Contains(text, identity)
}
