package main

import (
	"strings"
	"testing"
)

const testManifestXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><assembly xmlns="urn:schemas-microsoft-com:asm.v1"><assemblyIdentity name="com.qka.agent" version="0.1.23.0"/></assembly>`

func utf16LEEncode(s string) []byte {
	return utf16LEBytes(s)
}

func TestDecodeManifestTextUTF8(t *testing.T) {
	for name, b := range map[string][]byte{
		"裸 UTF-8":   []byte(testManifestXML),
		"UTF-8 BOM": append([]byte{0xef, 0xbb, 0xbf}, []byte(testManifestXML)...),
	} {
		got := decodeManifestText(b)
		if !strings.Contains(got, "com.qka.agent") {
			t.Errorf("%s: 解码结果里找不到 identity: %q", name, got)
		}
	}
}

func TestDecodeManifestTextUTF16(t *testing.T) {
	encoded := utf16LEEncode(testManifestXML)
	// 奇数补一个 NUL 单元不影响 UTF-16 判定。
	if len(encoded)%4 != 0 {
		encoded = append(encoded, 0, 0)
	}
	cases := map[string][]byte{
		"UTF-16LE BOM": append([]byte{0xff, 0xfe}, encoded...),
		"UTF-16BE BOM": func() []byte {
			be := make([]byte, len(encoded))
			for i := 0; i+1 < len(encoded); i += 2 {
				be[i], be[i+1] = encoded[i+1], encoded[i]
			}
			return append([]byte{0xfe, 0xff}, be...)
		}(),
		"无 BOM UTF-16LE": encoded,
	}
	for name, b := range cases {
		got := decodeManifestText(b)
		if !strings.Contains(got, "com.qka.agent") {
			t.Errorf("%s: 解码结果里找不到 identity: %q", name, got)
		}
	}
}

func TestDecodeManifestTextPlainASCIIXMLStaysUTF8(t *testing.T) {
	// 纯 ASCII 的 UTF-8 XML 不应被误判为 UTF-16。
	if got := decodeManifestText([]byte(testManifestXML)); got != testManifestXML {
		t.Errorf("纯 ASCII XML 被改写: %q", got)
	}
}

func TestContainsIdentity(t *testing.T) {
	if !containsIdentity(testManifestXML, "com.qka.agent") {
		t.Error("包含时应返回 true")
	}
	if containsIdentity(testManifestXML, "com.other.app") {
		t.Error("不包含时应返回 false")
	}
}
