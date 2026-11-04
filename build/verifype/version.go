package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// VS_VERSIONINFO 解析。标准库没有现成的，这里做最小实现：定长字段
// （VS_FIXEDFILEINFO）直接扫 0xFEEF04BD 签名定位——不按块头长度逐层走，
// 避开对齐细节的脆弱性；字符串表（StringFileInfo）先定位
// "StringFileInfo" UTF-16 键与其块头的 wLength 圈定范围，再在范围内搜键。
const vsFixedSignature = 0xfeef04bd

type vsFixedInfo struct {
	FileVersion    [4]uint16
	ProductVersion [4]uint16
}

// parseVSFixedInfo 在 RT_VERSION 资源数据里定位 VS_FIXEDFILEINFO 并读出
// 版本四元组。签名之后的 +8..+24 是 FileVersion 与 ProductVersion 两个
// DWORD（MS 在前）。返回签名所在偏移便于测试定位。
func parseVSFixedInfo(res []byte) (vsFixedInfo, int, error) {
	for i := 0; i+24 <= len(res); i++ {
		if le32(res[i:i+4]) != vsFixedSignature {
			continue
		}
		return vsFixedInfo{
			FileVersion:    quadFromDWords(le32(res[i+8:i+12]), le32(res[i+12:i+16])),
			ProductVersion: quadFromDWords(le32(res[i+16:i+20]), le32(res[i+20:i+24])),
		}, i, nil
	}
	return vsFixedInfo{}, -1, fmt.Errorf("版本资源里找不到 VS_FIXEDFILEINFO（0xFEEF04BD）")
}

// quadFromDWords 把 MS/LS 两个 DWORD 转成人类顺序四元组：MS 高 16 位是
// 第 1 段版本号。
func quadFromDWords(ms, ls uint32) [4]uint16 {
	return [4]uint16{uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls)}
}

func formatQuad(q [4]uint16) string {
	return fmt.Sprintf("%d.%d.%d.%d", q[0], q[1], q[2], q[3])
}

// parseVersionQuad 把 "0.1.23"、"0.1.23.4-rc1" 解析成四元组：截掉后缀、
// 不足 4 段补 0；解析不出返回 ok=false。
func parseVersionQuad(s string) ([4]uint16, bool) {
	var q [4]uint16
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return q, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return q, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 5 {
			return q, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 0xffff {
			return q, false
		}
		q[i] = uint16(n)
	}
	return q, true
}

func utf16LEBytes(s string) []byte {
	b := make([]byte, len(s)*2)
	for i := 0; i < len(s); i++ {
		b[i*2] = s[i]
		b[i*2+1] = 0
	}
	return b
}

func indexOfBytes(haystack, needle []byte) int {
	return bytes.Index(haystack, needle)
}

// versionStringValues 提取 StringFileInfo 里的字符串键值（只关心
// FileVersion / ProductVersion；定长字段才是权威，字符串缺键不算错）。
// 键 NUL 之后跳过对齐补零，读 UTF-16 值到 NUL 为止。
func versionStringValues(res []byte) map[string]string {
	out := map[string]string{}
	marker := utf16LEBytes("StringFileInfo")
	idx := indexOfBytes(res, marker)
	if idx < 6 {
		return out
	}
	start := idx - 6 // 块头（wLength/wValueLength/wType）紧贴在键之前
	end := len(res)
	if blockLen := int(le16(res[start : start+2])); blockLen > 0 && start+blockLen <= len(res) {
		end = start + blockLen
	}
	block := res[start:end]
	for _, key := range []string{"FileVersion", "ProductVersion"} {
		kb := utf16LEBytes(key)
		i := indexOfBytes(block, kb)
		if i < 0 {
			continue
		}
		j := i + len(kb)
		for j+1 < len(block) && le16(block[j:j+2]) == 0 {
			j += 2 // 键的 NUL 终止 + 4 字节对齐补零
		}
		var sb strings.Builder
		for j+1 < len(block) {
			u := le16(block[j : j+2])
			if u == 0 {
				break
			}
			sb.WriteRune(rune(u))
			j += 2
			if sb.Len() > 128 {
				break // 防御：版本字符串不该这么长，多半是解析跑偏了
			}
		}
		if sb.Len() > 0 {
			out[key] = sb.String()
		}
	}
	return out
}
