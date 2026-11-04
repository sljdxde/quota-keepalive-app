package main

import (
	"io"
	"strings"
	"testing"
)

// buildGoodTestPE 构造一个五项校验全部满足的合成 exe（版本 0.1.23）。
func buildGoodTestPE() []byte {
	quad := [4]uint16{0, 1, 23, 0}
	rsrc := buildTestResourceDir(0x1000, map[uint32][]byte{
		rtIcon:      {0x00, 0x00, 0x01, 0x00},
		rtGroupIcon: {0x00, 0x00, 0x01, 0x00, 0x01, 0x00},
		rtVersion: buildTestVersionResource(quad, quad, map[string]string{
			"FileVersion":    "0.1.23.0",
			"ProductVersion": "0.1.23.0",
		}),
		rtManifest: []byte(testManifestXML),
	})
	return buildTestPE(testPE{subsystem: 2, rsrc: rsrc, withDir: true, withSection: true})
}

func TestVerifyPEAllPass(t *testing.T) {
	var sb strings.Builder
	fails := verifyPE(&sb, buildGoodTestPE(), "0.1.23", "com.qka.agent")
	if len(fails) != 0 {
		t.Fatalf("全部满足时不应有失败项：%v\n输出:\n%s", fails, sb.String())
	}
	for _, want := range []string{".rsrc", "RT_ICON", "Subsystem", "FileVersion", "manifest"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("输出里缺少 %q 项:\n%s", want, sb.String())
		}
	}
}

func TestVerifyPEVersionEquivalentForms(t *testing.T) {
	// "0.1.23" 与资源里的 "0.1.23.0" 四元组归一化后视为一致。
	for _, expect := range []string{"0.1.23", "0.1.23.0"} {
		if fails := verifyPE(io.Discard, buildGoodTestPE(), expect, "com.qka.agent"); len(fails) != 0 {
			t.Errorf("期望版本 %q 不应失败：%v", expect, fails)
		}
	}
}

func TestVerifyPESkipsOptionalChecks(t *testing.T) {
	// 未传版本与 identity：只做 .rsrc/图标/子系统三项，缺版本资源也不算失败。
	rsrc := buildTestResourceDir(0x1000, map[uint32][]byte{
		rtIcon:      {1},
		rtGroupIcon: {2},
	})
	data := buildTestPE(testPE{subsystem: 2, rsrc: rsrc, withDir: true, withSection: true})
	var sb strings.Builder
	if fails := verifyPE(&sb, data, "", ""); len(fails) != 0 {
		t.Fatalf("fails = %v", fails)
	}
	if !strings.Contains(sb.String(), "跳过") {
		t.Errorf("应打印跳过说明:\n%s", sb.String())
	}
}

func TestVerifyPEFailures(t *testing.T) {
	good := func(mut func(*testPE, map[uint32][]byte)) []byte {
		quad := [4]uint16{0, 1, 23, 0}
		items := map[uint32][]byte{
			rtIcon:      {1},
			rtGroupIcon: {2},
			rtVersion:   buildTestVersionResource(quad, quad, nil),
			rtManifest:  []byte(testManifestXML),
		}
		pe := testPE{subsystem: 2, rsrc: nil, withDir: true, withSection: true}
		mut(&pe, items)
		pe.rsrc = buildTestResourceDir(0x1000, items)
		return buildTestPE(pe)
	}

	cases := []struct {
		name      string
		data      []byte
		expectVer string
		wantMsg   string
	}{
		{
			name:    "缺少 .rsrc 段",
			data:    buildTestPE(testPE{subsystem: 2, withDir: false, withSection: false}),
			wantMsg: ".rsrc",
		},
		{
			name:    "子系统不是 GUI",
			data:    buildTestPE(testPE{subsystem: 3, rsrc: buildTestResourceDir(0x1000, map[uint32][]byte{rtIcon: {1}, rtGroupIcon: {2}}), withDir: true, withSection: true}),
			wantMsg: "Subsystem",
		},
		{
			name: "缺 RT_GROUP_ICON",
			data: good(func(_ *testPE, items map[uint32][]byte) {
				delete(items, rtGroupIcon)
			}),
			wantMsg: "RT_GROUP_ICON",
		},
		{
			name:      "版本不符",
			data:      good(func(_ *testPE, items map[uint32][]byte) {}),
			expectVer: "9.9.9",
			wantMsg:   "版本不一致",
		},
		{
			name: "缺 RT_VERSION",
			data: good(func(_ *testPE, items map[uint32][]byte) {
				delete(items, rtVersion)
			}),
			wantMsg: "RT_VERSION",
		},
		{
			name: "字符串版本不符",
			data: good(func(_ *testPE, items map[uint32][]byte) {
				quad := [4]uint16{0, 1, 23, 0}
				items[rtVersion] = buildTestVersionResource(quad, quad, map[string]string{
					"ProductVersion": "9.9.9.0",
				})
			}),
			wantMsg: "ProductVersion",
		},
		{
			name: "manifest identity 不符",
			data: good(func(_ *testPE, items map[uint32][]byte) {
				items[rtManifest] = []byte(`<?xml version="1.0"?><assembly><assemblyIdentity name="com.other.app"/></assembly>`)
			}),
			wantMsg: "identity",
		},
		{
			name: "缺 RT_MANIFEST",
			data: good(func(_ *testPE, items map[uint32][]byte) {
				delete(items, rtManifest)
			}),
			wantMsg: "RT_MANIFEST",
		},
		{
			name:    "坏 PE",
			data:    []byte("this is not a PE at all, just bytes"),
			wantMsg: "PE 解析",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectVer := c.expectVer
			if expectVer == "" {
				expectVer = "0.1.23"
			}
			var sb strings.Builder
			fails := verifyPE(&sb, c.data, expectVer, "com.qka.agent")
			if len(fails) == 0 {
				t.Fatalf("期望失败，实际通过。输出:\n%s", sb.String())
			}
			if !strings.Contains(sb.String(), c.wantMsg) {
				t.Errorf("输出里缺少 %q:\n%s", c.wantMsg, sb.String())
			}
		})
	}
}
