package main

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
	"testing"
)

// ---- 合成夹具（issue #3 说明：不在仓库放真实 exe，用字节构造覆盖解析链）----

// buildTestResourceDir 构造「类型→名称(id=1)→语言(id=0x409)→数据」的资源
// 目录；rvaBase 是目录（即 .rsrc 段）的起始 RVA，叶子数据项的 RVA 据此折算。
func buildTestResourceDir(rvaBase uint32, items map[uint32][]byte) []byte {
	types := make([]uint32, 0, len(items))
	for t := range items {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })

	// 布局：根目录(16+8n) + 每类型块{名称目录 24、语言目录 24、数据项 16、数据}。
	blockStart := make([]uint32, len(types))
	off := uint32(16 + 8*len(types))
	for i, t := range types {
		blockStart[i] = off
		off += 24 + 24 + 16 + uint32(len(items[t]))
	}

	var buf bytes.Buffer
	root := make([]byte, 16+8*len(types))
	binary.LittleEndian.PutUint16(root[14:], uint16(len(types))) // NumberOfIdEntries
	for i, t := range types {
		e := root[16+8*i:]
		binary.LittleEndian.PutUint32(e, t)
		binary.LittleEndian.PutUint32(e[4:], blockStart[i]|0x80000000)
	}
	buf.Write(root)

	for i, t := range types {
		data := items[t]
		base := blockStart[i]
		// 名称层：ID 条目 1 → 语言层。
		d1 := make([]byte, 24)
		binary.LittleEndian.PutUint16(d1[14:], 1)
		binary.LittleEndian.PutUint32(d1[16:], 1)
		binary.LittleEndian.PutUint32(d1[20:], (base+24)|0x80000000)
		// 语言层：ID 条目 0x409 → 数据项（高位为 0 = 叶子）。
		d2 := make([]byte, 24)
		binary.LittleEndian.PutUint16(d2[14:], 1)
		binary.LittleEndian.PutUint32(d2[16:], 0x409)
		binary.LittleEndian.PutUint32(d2[20:], base+48)
		// IMAGE_RESOURCE_DATA_ENTRY：OffsetToData 是 RVA。
		de := make([]byte, 16)
		binary.LittleEndian.PutUint32(de, rvaBase+base+64)
		binary.LittleEndian.PutUint32(de[4:], uint32(len(data)))
		buf.Write(d1)
		buf.Write(d2)
		buf.Write(de)
		buf.Write(data)
	}
	return buf.Bytes()
}

// testPE 描述一个最小合成 PE：单段 .rsrc（可选不写段、不写数据目录项）。
type testPE struct {
	subsystem   uint16
	rsrc        []byte
	withDir     bool // 数据目录[2] 是否指向资源表
	withSection bool // 是否写 .rsrc 段（false 时测“缺段”路径）
}

// buildTestPE 构造 PE32：DOS 头 + PE 签名 + COFF + optional header(208 字节、
// 14 个数据目录) + 段表 + .rsrc 原始数据。Subsystem 写在 optional header
// 偏移 68，资源表目录项在偏移 96+16。
func buildTestPE(pe testPE) []byte {
	var buf bytes.Buffer
	dos := make([]byte, 0x80)
	dos[0], dos[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(dos[0x3c:], 0x80)
	buf.Write(dos)

	coff := make([]byte, 24)
	copy(coff, "PE\x00\x00")
	binary.LittleEndian.PutUint16(coff[4:], 0x014c)
	numSections := 0
	if pe.withSection {
		numSections = 1
	}
	binary.LittleEndian.PutUint16(coff[6:], uint16(numSections))
	binary.LittleEndian.PutUint16(coff[20:], 208)
	buf.Write(coff)

	opt := make([]byte, 208)
	binary.LittleEndian.PutUint16(opt, 0x10b)
	binary.LittleEndian.PutUint16(opt[68:], pe.subsystem)
	binary.LittleEndian.PutUint32(opt[92:], 14) // NumberOfRvaAndSizes
	if pe.withDir {
		binary.LittleEndian.PutUint32(opt[96+16:], 0x1000)
		binary.LittleEndian.PutUint32(opt[96+20:], uint32(len(pe.rsrc)))
	}
	buf.Write(opt)

	if pe.withSection {
		sec := make([]byte, 40)
		copy(sec, ".rsrc")
		binary.LittleEndian.PutUint32(sec[8:], uint32(len(pe.rsrc)))  // VirtualSize
		binary.LittleEndian.PutUint32(sec[12:], 0x1000)               // VirtualAddress
		binary.LittleEndian.PutUint32(sec[16:], uint32(len(pe.rsrc))) // SizeOfRawData
		// 段数据紧跟在 40 字节段表项之后。
		binary.LittleEndian.PutUint32(sec[20:], uint32(buf.Len()+40)) // PointerToRawData
		buf.Write(sec)
		buf.Write(pe.rsrc)
	}
	return buf.Bytes()
}

// ---- parsePE / rvaToOffset / resources ----

func TestParsePESynthetic(t *testing.T) {
	rsrc := buildTestResourceDir(0x1000, map[uint32][]byte{rtIcon: []byte("icon-bytes")})
	pe, err := parsePE(buildTestPE(testPE{subsystem: 2, rsrc: rsrc, withDir: true, withSection: true}))
	if err != nil {
		t.Fatalf("parsePE: %v", err)
	}
	if pe.subsystem != 2 {
		t.Errorf("subsystem = %d, want 2", pe.subsystem)
	}
	if !pe.hasSection(".rsrc") {
		t.Error("hasSection(.rsrc) = false")
	}
	if pe.resourceDirOff == 0 {
		t.Fatal("resourceDirOff = 0")
	}
	entries, err := pe.resources()
	if err != nil {
		t.Fatalf("resources: %v", err)
	}
	if len(entries) != 1 || entries[0].typ != rtIcon {
		t.Fatalf("entries = %+v, want 1 条 rtIcon", entries)
	}
	if string(entries[0].data) != "icon-bytes" {
		t.Errorf("资源数据 = %q", entries[0].data)
	}
	if entries[0].name != 1 || entries[0].lang != 0x409 {
		t.Errorf("name/lang = %d/%d, want 1/0x409", entries[0].name, entries[0].lang)
	}
}

func TestParsePEFallbackWithoutDataDirectory(t *testing.T) {
	// 数据目录缺失时退回 .rsrc 段头定位资源目录。
	rsrc := buildTestResourceDir(0x1000, map[uint32][]byte{rtVersion: []byte("v")})
	pe, err := parsePE(buildTestPE(testPE{subsystem: 2, rsrc: rsrc, withDir: false, withSection: true}))
	if err != nil {
		t.Fatalf("parsePE: %v", err)
	}
	if pe.resourceDirOff == 0 {
		t.Fatal("fallback 没有找到资源目录")
	}
	if _, err := pe.resources(); err != nil {
		t.Fatalf("resources: %v", err)
	}
}

func TestParsePERejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"空文件":  {},
		"太短":   []byte("MZ\x00\x00"),
		"非 MZ": []byte("ELF-not-a-pe-file........"),
		"e_lfanew 越界": func() []byte {
			b := make([]byte, 0x100)
			b[0], b[1] = 'M', 'Z'
			binary.LittleEndian.PutUint32(b[0x3c:], 0xfffff0)
			return b
		}(),
		"无 PE 签名": func() []byte {
			b := make([]byte, 0x100)
			b[0], b[1] = 'M', 'Z'
			binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
			return b
		}(),
	}
	for name, b := range cases {
		if _, err := parsePE(b); err == nil {
			t.Errorf("%s: 期望报错，实际通过", name)
		}
	}
}

func TestResourcesRejectsBadLeafRVA(t *testing.T) {
	// 叶子数据项的 RVA 不落在任何段内 → 必须报错而不是越界读。
	rsrc := buildTestResourceDir(0x999000, map[uint32][]byte{rtIcon: []byte("x")})
	pe, err := parsePE(buildTestPE(testPE{subsystem: 2, rsrc: rsrc, withDir: true, withSection: true}))
	if err != nil {
		t.Fatalf("parsePE: %v", err)
	}
	_, err = pe.resources()
	if err == nil || !strings.Contains(err.Error(), "不落在任何段内") {
		t.Errorf("err = %v, want 包含“不落在任何段内”", err)
	}
}

func TestResourcesTerminatesOnCycle(t *testing.T) {
	// 根目录的条目指向根目录自身（高位标志=子目录）：必须终止且不报错。
	dir := make([]byte, 24)
	binary.LittleEndian.PutUint16(dir[14:], 1)
	binary.LittleEndian.PutUint32(dir[16:], 3)
	binary.LittleEndian.PutUint32(dir[20:], 0x80000000) // 偏移 0 = 根自身
	pe, err := parsePE(buildTestPE(testPE{subsystem: 2, rsrc: dir, withDir: true, withSection: true}))
	if err != nil {
		t.Fatalf("parsePE: %v", err)
	}
	entries, err := pe.resources()
	if err != nil {
		t.Fatalf("resources: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0", len(entries))
	}
}

func TestResourcesEmptyDir(t *testing.T) {
	// 空资源目录应返回空切片（而非 nil），让 verifyPE 继续报图标缺失。
	pe, err := parsePE(buildTestPE(testPE{subsystem: 2, rsrc: make([]byte, 16), withDir: true, withSection: true}))
	if err != nil {
		t.Fatalf("parsePE: %v", err)
	}
	entries, err := pe.resources()
	if err != nil {
		t.Fatalf("resources: %v", err)
	}
	if entries == nil || len(entries) != 0 {
		t.Errorf("entries = %+v, want 空 slice", entries)
	}
}
