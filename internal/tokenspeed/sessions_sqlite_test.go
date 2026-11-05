package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// sqliteSessionFixture 建一份最小 sqlite 库，含 session 表（会话名）与消息表。
func sqliteSessionFixture(t *testing.T, source, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`CREATE TABLE session (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			directory TEXT NOT NULL,
			time_updated INTEGER NOT NULL
		)`,
	}
	if source == "zcode" {
		statements = append(statements,
			`CREATE TABLE message (
				id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
				time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE part (
				id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
				time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
				data TEXT NOT NULL
			)`,
		)
	} else {
		statements = append(statements, `CREATE TABLE event (
			id TEXT PRIMARY KEY, aggregate_id TEXT NOT NULL, seq INTEGER NOT NULL,
			type TEXT NOT NULL, data TEXT NOT NULL
		)`)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func insertSQLiteSession(t *testing.T, db *sql.DB, id, title, directory string, updated time.Time) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO session (id, title, directory, time_updated) VALUES (?, ?, ?, ?)`,
		id, title, directory, updated.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func insertZCodeAssistantMessage(t *testing.T, db *sql.DB, messageID, sessionID string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"role":    "assistant",
		"modelId": "autoclaw-cn/zai_glm-5.3-flash",
		"time":    map[string]any{"created": at.UnixMilli(), "completed": at.Add(2 * time.Second).UnixMilli()},
		"tokens":  map[string]any{"total": 400, "input": 100, "output": 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?)`,
		messageID, sessionID, at.UnixMilli(), at.Add(2*time.Second).UnixMilli(), string(data)); err != nil {
		t.Fatal(err)
	}
}

func insertOpenCodeAssistantEvent(t *testing.T, db *sql.DB, eventID, sessionID, model string, at time.Time) {
	t.Helper()
	info := map[string]any{
		"id":        eventID,
		"sessionID": sessionID,
		"role":      "assistant",
		"modelID":   model,
		"time":      map[string]any{"created": at.UnixMilli(), "completed": at.Add(2 * time.Second).UnixMilli()},
		"tokens":    map[string]any{"total": 400, "input": 100, "output": 300},
	}
	data, err := json.Marshal(map[string]any{"sessionID": sessionID, "info": info})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		eventID, sessionID, at.UnixMilli(), "message.updated.1", string(data)); err != nil {
		t.Fatal(err)
	}
}

// TestZCodeSessionTitleBecomesLabel 复现面板把模型名当会话名的现场：
// zcode 的会话名在 session 表里（title），修复前只按模型名兜底，会话行显示
// 「autoclaw-cn/zai_glm-5.3-flash」。修复后应显示会话标题。
func TestZCodeSessionTitleBecomesLabel(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db := sqliteSessionFixture(t, "zcode", path)
	insertSQLiteSession(t, db, "sess-zcode-1", "修复最新版本Bug",
		"/Users/me/004个人代码仓库/quota-keepalive-agent", start)
	insertZCodeAssistantMessage(t, db, "msg-zcode-1", "sess-zcode-1", start)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "zcode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("会话数 = %d: %+v", len(snapshot.Sessions), snapshot.Sessions)
	}
	session := snapshot.Sessions[0]
	if session.Label != "修复最新版本Bug" {
		t.Fatalf("会话名 = %q，want 会话标题「修复最新版本Bug」", session.Label)
	}
	if session.Model != "autoclaw-cn/zai_glm-5.3-flash" {
		t.Fatalf("模型名 = %q（模型仍应保留在小字里）", session.Model)
	}
	if session.Project != "quota-keepalive-agent" {
		t.Fatalf("项目名 = %q，want quota-keepalive-agent", session.Project)
	}
}

// TestOpenCodeSessionTitleBecomesLabel 覆盖 opencode 的同一问题。
func TestOpenCodeSessionTitleBecomesLabel(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "opencode.db")
	db := sqliteSessionFixture(t, "opencode", path)
	insertSQLiteSession(t, db, "ses-open-1", "会话结构重做",
		"/Users/me/projects/qka", start)
	insertOpenCodeAssistantEvent(t, db, "msg-open-1", "ses-open-1", "deepseek-v4-flash", start)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "opencode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("会话数 = %d: %+v", len(snapshot.Sessions), snapshot.Sessions)
	}
	if got := snapshot.Sessions[0].Label; got != "会话结构重做" {
		t.Fatalf("会话名 = %q，want 会话标题「会话结构重做」", got)
	}
}

// TestSQLiteSessionMissingTableFallsBackToModel 老版本库没有 session 表时不能
// 报错、也不能让数据源读取失败：会话名退回项目名/模型名即可。
func TestSQLiteSessionMissingTableFallsBackToModel(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE message (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE part (
			id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
			data TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	insertZCodeAssistantMessage(t, db, "msg-old-1", "sess-old-1", start)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "zcode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("会话数 = %d: %+v", len(snapshot.Sessions), snapshot.Sessions)
	}
	if got := snapshot.Sessions[0].Label; got != "autoclaw-cn/zai_glm-5.3-flash" {
		t.Fatalf("缺少 session 表时应回落到模型名，实际 %q", got)
	}
	if service.opencode[path].lastErr != "" {
		t.Fatalf("缺少 session 表不应记为数据源读取失败: %q", service.opencode[path].lastErr)
	}
}

// TestSQLiteSessionTitleFeedsOnlyFreshSessions 24 小时窗口外的历史会话不该被
// 读进来（会话表会无限增长）；窗口内的新会话要能拿到名字。
func TestSQLiteSessionTitleFeedsOnlyFreshSessions(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db := sqliteSessionFixture(t, "zcode", path)
	insertSQLiteSession(t, db, "sess-old", "上周的旧会话",
		"/Users/me/old", start.Add(-72*time.Hour))
	insertSQLiteSession(t, db, "sess-new", "今天的会话",
		"/Users/me/new", start)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "zcode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	if _, ok := service.sessionTitles["zcode:sess-old"]; ok {
		t.Fatal("24 小时窗口外的历史会话名不应被读取")
	}
	if got := service.sessionTitles["zcode:sess-new"]; got != "今天的会话" {
		t.Fatalf("窗口内会话名 = %q，want 今天的会话", got)
	}
}

// TestNormalizeSessionTitleCollapsesAndTruncates 标题可能来自首条输入：折叠
// 换行/多余空白，并截断到面板可读长度。
func TestNormalizeSessionTitleCollapsesAndTruncates(t *testing.T) {
	if got := normalizeSessionTitle("  多行\n标题 \t 带空白 "); got != "多行 标题 带空白" {
		t.Fatalf("normalizeSessionTitle = %q", got)
	}
	if got := normalizeSessionTitle("   "); got != "" {
		t.Fatalf("空白标题应归一为空串，实际 %q", got)
	}
	long := make([]rune, 80)
	for i := range long {
		long[i] = '好'
	}
	got := normalizeSessionTitle(string(long))
	if runes := []rune(got); len(runes) != 61 || runes[60] != '…' {
		t.Fatalf("超长标题应截断为 60 字符 + 省略号，实际 %q（%d 字符）", got, len(runes))
	}
}
