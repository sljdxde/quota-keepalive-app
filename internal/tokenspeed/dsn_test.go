package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 回归保护：Windows 绝对路径（C:\Users\...）必须拼成 file:///C:/Users/...。
// 曾经的写法 url.URL{Scheme:"file", Path:"C:/Users/..."} 生成 file:C:/Users/...，
// sqlite 把 "C:" 当成 URI authority，报「invalid uri authority: C:」——zcode 与
// opencode 两个数据源在 Windows 上因此从未读到任何记录，Token 速度恒为 0。
func TestOpenCodeReadOnlyDSNIsValidFileURI(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		want        string
		windowsOnly bool
	}{
		{"windows 绝对路径", `C:\Users\me\.zcode\cli\db\db.sqlite`, "file:///C:/Users/me/.zcode/cli/db/db.sqlite", true},
		{"windows 小写盘符", `d:\data\opencode.db`, "file:///d:/data/opencode.db", true},
		{"posix 绝对路径", "/home/me/.local/share/opencode/opencode.db", "file:///home/me/.local/share/opencode/opencode.db", false},
		{"相对路径", filepath.Join("cache", "db.sqlite"), "file:cache/db.sqlite", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 盘符路径在 POSIX 上 filepath 不把 "\" 当分隔符，不在本条用例的
			// 覆盖范围（Windows 那两例由 Windows CI 覆盖）。
			if tc.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("Windows 盘符路径仅在 Windows 上是绝对路径")
			}
			dsn := openCodeReadOnlyDSN(tc.path)
			base := dsn
			if i := strings.IndexByte(dsn, '?'); i >= 0 {
				base = dsn[:i]
			}
			if base != tc.want {
				t.Fatalf("DSN = %q，want 前缀 %q", dsn, tc.want)
			}
			// 必须是 sqlite 能解析的 URI：mode=ro 只读、busy_timeout 兜住锁竞争。
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("DSN 无法解析为 URI: %v", err)
			}
			if parsed.Scheme != "file" {
				t.Fatalf("scheme = %q，want file", parsed.Scheme)
			}
			if parsed.Host != "" {
				t.Fatalf("不应有 authority（Host=%q）：Windows 盘符会被误判成主机名", parsed.Host)
			}
			if got := parsed.Query().Get("mode"); got != "ro" {
				t.Fatalf("mode = %q，want ro", got)
			}
			if got := parsed.Query().Get("_pragma"); !strings.Contains(got, "busy_timeout") {
				t.Fatalf("_pragma = %q，应包含 busy_timeout", got)
			}
		})
	}
}

// 端到端回归：在绝对路径（Windows 上是 C:\...）建一个真实 sqlite 库，走生产
// 读取路径必须拿到记录。DSN 拼错时这里会返回 0 条——正是「速度恒为 0」的现场。
func TestZCodeReaderReadsAbsolutePathDatabase(t *testing.T) {
	now := time.Now()
	start := now.Add(-2 * time.Second)
	path := filepath.Join(t.TempDir(), "db.sqlite")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE message (
  id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT
);
CREATE TABLE part (
  id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT,
  time_created INTEGER, time_updated INTEGER, data TEXT
);`); err != nil {
		t.Fatal(err)
	}
	msgID := "msg_1"
	created := start.UnixMilli()
	updated := now.UnixMilli()
	data, err := json.Marshal(map[string]any{
		"role":    "assistant",
		"modelId": "deepseek-v4-flash",
		"time":    map[string]any{"created": created},
		"tokens": map[string]any{
			"input": 437888, "output": 835, "reasoning": 266,
			"cache": map[string]any{"read": 437888, "write": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES(?,?,?,?,?)`,
		msgID, "sess_1", created, updated, string(data)); err != nil {
		t.Fatal(err)
	}
	part, err := json.Marshal(map[string]any{"type": "text", "text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES(?,?,?,?,?,?)`,
		"part_1", msgID, "sess_1", start.Add(500*time.Millisecond).UnixMilli(),
		updated, string(part)); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Roots = []Root{{Source: "zcode", Path: path}}
	svc := New(cfg)
	svc.Poll()

	if len(svc.records) == 0 {
		t.Fatalf("绝对路径的 sqlite 库应读到记录（DSN 是否又退化成 file:C:/...？）")
	}
	var out float64
	for _, rec := range svc.records {
		if rec.usage.output > 0 {
			out += float64(rec.usage.output)
		}
	}
	if out <= 0 {
		t.Fatalf("应聚合计入输出 token，实际 %v", out)
	}
	snap := svc.Snapshot()
	if len(snap.Sources) == 0 || snap.Sources[0].Source != "zcode" {
		t.Fatalf("快照应包含 zcode 数据源，实际 %+v", snap.Sources)
	}
	t.Logf("读到的输出 token 总量=%.0f，快照输出速度=%.2f", out, snap.OutputTokensPerSecond)
	if runtime.GOOS == "windows" {
		// Windows 上这条日志能直接对上「盘符被当成 authority」的历史故障。
		t.Logf("DSN=%s", openCodeReadOnlyDSN(path))
	}
}

// 读取失败必须留下可诊断的痕迹（lastErr），并且不能把失败当成「无变化」缓存
// 起来——否则数据源会永远静默为 0（Windows 上 DSN 拼错的真实后果）。
func TestOpenCodeSourceFailureIsRecordedAndRetried(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-a-db.sqlite")
	if err := os.WriteFile(bad, []byte("this is not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Roots = []Root{{Source: "zcode", Path: bad}}
	svc := New(cfg)
	svc.Poll()

	state := svc.opencode[bad]
	if state == nil {
		t.Fatal("应记录数据源状态")
	}
	if state.lastErr == "" {
		t.Fatal("读取失败应留下 lastErr，供日志定位")
	}
	if state.sigSet {
		t.Fatal("失败不能记住文件指纹，否则下轮不会再试")
	}
}
