package main

import (
	"os"
	"path/filepath"
	"testing"
)

const testWinresJSON = `{
  "RT_GROUP_ICON": { "#3": { "0000": ["icon-256.png"] } },
  "RT_MANIFEST": {
    "#1": {
      "0409": {
        "identity": { "name": "com.qka.agent", "version": "" },
        "dpi-awareness": "per monitor v2"
      }
    }
  },
  "RT_VERSION": { "#1": { "0000": { "fixed": { "file_version": "0.0.0.0" } } } }
}`

func TestIdentityFromWinres(t *testing.T) {
	path := filepath.Join(t.TempDir(), "winres.json")
	if err := os.WriteFile(path, []byte(testWinresJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := identityFromWinres(path)
	if err != nil {
		t.Fatalf("identityFromWinres: %v", err)
	}
	if name != "com.qka.agent" {
		t.Errorf("name = %q, want com.qka.agent", name)
	}
}

func TestIdentityFromWinresUsesRepoConfig(t *testing.T) {
	// 仓库真实配置（单一来源）：期望值必须与 go-winres 生成 syso 时一致。
	name, err := identityFromWinres("../../build/windows/winres.json")
	if err != nil {
		t.Fatalf("identityFromWinres: %v", err)
	}
	if name != "com.qka.agent" {
		t.Errorf("name = %q, want com.qka.agent", name)
	}
}

func TestIdentityFromWinresErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"文件不存在":         filepath.Join(dir, "missing.json"),
		"坏 JSON":        writeTemp(t, dir, "bad.json", "{not json"),
		"无 RT_MANIFEST": writeTemp(t, dir, "nomanifest.json", `{"RT_VERSION": {}}`),
		"缺 identity":    writeTemp(t, dir, "noidentity.json", `{"RT_MANIFEST": {"#1": {"0409": {"dpi-awareness": "system"}}}}`),
	}
	for name, path := range cases {
		if _, err := identityFromWinres(path); err == nil {
			t.Errorf("%s: 期望报错，实际通过", name)
		}
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
