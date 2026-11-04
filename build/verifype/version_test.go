package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildTestVersionResource 组一个最小 VS_VERSIONINFO：块头 + UTF-16 键 +
// 补齐 + VS_FIXEDFILEINFO +（可选）StringFileInfo 字符串块。解析器只依赖
// 签名扫描与 StringFileInfo 块头 wLength，这里仍按真实布局组织。
func buildTestVersionResource(file, prod [4]uint16, strs map[string]string) []byte {
	fixed := make([]byte, 52)
	binary.LittleEndian.PutUint32(fixed[0:], vsFixedSignature)
	binary.LittleEndian.PutUint32(fixed[4:], 0x00010000) // StrucVersion
	binary.LittleEndian.PutUint32(fixed[8:], uint32(file[0])<<16|uint32(file[1]))
	binary.LittleEndian.PutUint32(fixed[12:], uint32(file[2])<<16|uint32(file[3]))
	binary.LittleEndian.PutUint32(fixed[16:], uint32(prod[0])<<16|uint32(prod[1]))
	binary.LittleEndian.PutUint32(fixed[20:], uint32(prod[2])<<16|uint32(prod[3]))

	var out bytes.Buffer
	out.Write(make([]byte, 6)) // wLength/wValueLength/wType（解析器不读）
	out.Write(utf16LEBytes("VS_VERSION_INFO"))
	out.Write([]byte{0, 0}) // 键 NUL
	out.Write([]byte{0, 0}) // 对齐补齐
	out.Write(fixed)

	if len(strs) > 0 {
		var body bytes.Buffer
		for _, key := range []string{"FileVersion", "ProductVersion"} {
			val, okv := strs[key]
			if !okv {
				continue
			}
			body.Write(utf16LEBytes(key))
			body.Write([]byte{0, 0}) // 键 NUL
			body.Write([]byte{0, 0}) // 对齐补零
			body.Write(utf16LEBytes(val))
			body.Write([]byte{0, 0}) // 值 NUL
		}
		marker := utf16LEBytes("StringFileInfo")
		header := make([]byte, 6)
		binary.LittleEndian.PutUint16(header, uint16(6+len(marker)+2+2+body.Len()))
		out.Write(header)
		out.Write(marker)
		out.Write([]byte{0, 0})
		out.Write([]byte{0, 0})
		out.Write(body.Bytes())
	}
	return out.Bytes()
}

func TestParseVSFixedInfo(t *testing.T) {
	res := buildTestVersionResource([4]uint16{0, 1, 23, 0}, [4]uint16{0, 1, 23, 4}, nil)
	info, off, err := parseVSFixedInfo(res)
	if err != nil {
		t.Fatalf("parseVSFixedInfo: %v", err)
	}
	if off <= 0 {
		t.Errorf("签名偏移 = %d, want > 0", off)
	}
	if info.FileVersion != [4]uint16{0, 1, 23, 0} {
		t.Errorf("FileVersion = %v", info.FileVersion)
	}
	if info.ProductVersion != [4]uint16{0, 1, 23, 4} {
		t.Errorf("ProductVersion = %v", info.ProductVersion)
	}
}

func TestParseVSFixedInfoMissingSignature(t *testing.T) {
	if _, _, err := parseVSFixedInfo([]byte("no signature anywhere in here")); err == nil {
		t.Error("缺少 0xFEEF04BD 签名时必须报错")
	}
	if _, _, err := parseVSFixedInfo(nil); err == nil {
		t.Error("空输入必须报错")
	}
}

func TestParseVSFixedInfoRobustToPrefix(t *testing.T) {
	// 签名前面有任意噪声（真实资源的块头/键/补齐）也能定位。
	noise := []byte{0x37, 0x01, 0x00, 0x00, 0xff, 0xfe}
	res := append(append([]byte{}, noise...), buildTestVersionResource([4]uint16{1, 0, 0, 0}, [4]uint16{1, 0, 0, 0}, nil)...)
	info, _, err := parseVSFixedInfo(res)
	if err != nil {
		t.Fatalf("parseVSFixedInfo: %v", err)
	}
	if info.FileVersion != [4]uint16{1, 0, 0, 0} {
		t.Errorf("FileVersion = %v", info.FileVersion)
	}
}

func TestParseVersionQuad(t *testing.T) {
	cases := []struct {
		in   string
		want [4]uint16
		ok   bool
	}{
		{"0.1.23", [4]uint16{0, 1, 23, 0}, true},
		{"1.2.3.4", [4]uint16{1, 2, 3, 4}, true},
		{"0.1.23-rc1", [4]uint16{0, 1, 23, 0}, true},
		{"0.1.23.4+build.9", [4]uint16{0, 1, 23, 4}, true},
		{" 0.1.23 ", [4]uint16{0, 1, 23, 0}, true},
		{"dev", [4]uint16{}, false},
		{"", [4]uint16{}, false},
		{"a.b.c", [4]uint16{}, false},
		{"1.2.3.4.5", [4]uint16{}, false},
		{"1..3", [4]uint16{}, false},
		{"70000.1.2", [4]uint16{}, false},
	}
	for _, c := range cases {
		got, ok := parseVersionQuad(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseVersionQuad(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestVersionStringValues(t *testing.T) {
	quad := [4]uint16{0, 1, 23, 0}
	res := buildTestVersionResource(quad, quad, map[string]string{
		"FileVersion":    "0.1.23.0",
		"ProductVersion": "0.1.23.0",
	})
	got := versionStringValues(res)
	if got["FileVersion"] != "0.1.23.0" {
		t.Errorf("FileVersion = %q", got["FileVersion"])
	}
	if got["ProductVersion"] != "0.1.23.0" {
		t.Errorf("ProductVersion = %q", got["ProductVersion"])
	}
}

func TestVersionStringValuesAbsent(t *testing.T) {
	// 无 StringFileInfo 块：安静返回空（定长字段才是权威）。
	quad := [4]uint16{0, 1, 23, 0}
	if got := versionStringValues(buildTestVersionResource(quad, quad, nil)); len(got) != 0 {
		t.Errorf("got = %v, want 空", got)
	}
	if got := versionStringValues([]byte("garbage")); len(got) != 0 {
		t.Errorf("got = %v, want 空", got)
	}
}

func TestVersionStringValuesBoundedByBlock(t *testing.T) {
	// 块范围之外的同名键不应被读到：StringFileInfo 块后面模拟下一个块的
	// 内容，ProductVersion 键必须取不到。
	quad := [4]uint16{0, 1, 23, 0}
	res := buildTestVersionResource(quad, quad, map[string]string{"FileVersion": "0.1.23.0"})
	res = append(res, utf16LEBytes("ProductVersion")...)
	res = append(res, 0, 0)
	res = append(res, utf16LEBytes("9.9.9.9")...)
	res = append(res, 0, 0)
	got := versionStringValues(res)
	if _, present := got["ProductVersion"]; present {
		t.Errorf("块外的 ProductVersion 被读取: %v", got)
	}
	if got["FileVersion"] != "0.1.23.0" {
		t.Errorf("FileVersion = %q, want 0.1.23.0", got["FileVersion"])
	}
}
