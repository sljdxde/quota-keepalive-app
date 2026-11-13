//go:build windows

package scheduler

import (
	"context"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16TaskXML(command string) []byte {
	xml := `<?xml version="1.0" encoding="UTF-16"?><Task><Actions><Exec><Command>` + command + `</Command></Exec></Actions></Task>`
	units := utf16.Encode([]rune(xml))
	out := []byte{0xFF, 0xFE}
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func TestWindowsPruneOrphans(t *testing.T) {
	keepDir := `C:\Users\test\.qka`
	keep := WindowsTaskName(keepDir)
	stale := "QKA Auto Run 22222222"
	livePath := t.TempDir() + `\qka.exe`
	if err := os.WriteFile(livePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := "QKA Auto Run 33333333"
	var deleted []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "schtasks" && args[0] == "/Query" && args[1] == "/FO":
			return []byte(`"` + stale + `","N/A","Ready"` + "\r\n" +
				`"` + live + `","N/A","Ready"` + "\r\n" +
				`"` + keep + `","N/A","Ready"` + "\r\n" +
				`"Other Task","N/A","Ready"` + "\r\n"), nil
		case name == "schtasks" && args[0] == "/Query" && args[1] == "/TN":
			if strings.HasSuffix(args[2], "33333333") {
				return utf16TaskXML(livePath), nil
			}
			return utf16TaskXML(`C:\gone\app.test.exe`), nil
		case name == "schtasks" && args[0] == "/Delete":
			deleted = append(deleted, args[2])
			return nil, nil
		}
		return nil, nil
	}
	s := newWindowsScheduler(run, nil)
	removed, err := s.PruneOrphans(context.Background(), Spec{ConfigDir: keepDir})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(deleted) != 1 || deleted[0] != stale {
		t.Fatalf("只应删除程序已不存在的陈旧任务，removed=%d deleted=%v", removed, deleted)
	}
}
