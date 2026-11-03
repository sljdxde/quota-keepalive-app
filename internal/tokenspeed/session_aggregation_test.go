package tokenspeed

import (
	"fmt"
	"path/filepath"
	"testing"
)

// dshSubagentPath 构造子代理会话的路径：目录名是裸 UUID（无 session- 前缀），
// 与真实 DSH 会话布局一致。
func dshSubagentPath(root, session string) string {
	return filepath.Join(root, "--Users-test-workspace--", session, "session.v4.jsonl.zstd")
}

// TestDSHFoldsSubagentSessionsIntoParentWithTitle 验证：一个顶层会话孵化出的
// 子代理会话不会各占一行，而是折叠回顶层会话；会话行显示会话自身的标题，
// 而不是项目名加短 ID。
func TestDSHFoldsSubagentSessionsIntoParentWithTitle(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	parent := "paren0-0000-4000-8000-0000000000a1"
	child := "child0-0000-4000-8000-0000000000b2"
	cwd := "/Users/me/004个人代码仓库/机架感知功能"

	dshWriteFile(t, dshSessionPath(root, parent), dshFrame(t,
		fmt.Sprintf(`{"type":"session","version":4,"id":"paren0","cwd":%q}`, cwd),
		`{"type":"session/title","seq":1,"time":1,"data":{"title":"这是我最新版本的设计文档，","source":{"kind":"fallback"}}}`,
		`{"type":"session/title","seq":2,"time":2,"data":{"title":"设计文档评审与类设计分析","source":{"kind":"provider","provider":"session-title-first-prompt-llm","model":{"provider":"bangsun","model":"deepseek-v4-flash"}}}}`,
		dshAssistantEvent("msg-parent", now, 10, 1500, 1510, 0),
	))
	dshWriteFile(t, dshSubagentPath(root, child), dshFrame(t,
		fmt.Sprintf(`{"type":"session","version":4,"id":%q,"cwd":%q,"parentSession":%q,"delegationDepth":1,"origin":"subagent"}`,
			child, cwd, "session-"+parent),
		`{"type":"session/title","seq":1,"time":1,"data":{"title":"You are an experienced backend","source":{"kind":"fallback"}}}`,
		dshAssistantEvent("msg-child", now, 10, 1500, 1510, 0),
	))

	service := dshNewService(root, now)
	service.Poll()
	snapshot := service.Snapshot()

	if len(snapshot.Sessions) != 1 {
		t.Fatalf("子代理会话应折叠进顶层会话，实际 %d 行: %+v",
			len(snapshot.Sessions), snapshot.Sessions)
	}
	session := snapshot.Sessions[0]
	if session.Label != "设计文档评审与类设计分析" {
		t.Fatalf("会话行应显示会话标题，实际 %q", session.Label)
	}
	if session.Project != "机架感知功能" {
		t.Fatalf("project 字段应保持工作区名，实际 %q", session.Project)
	}
	// 父子两次响应的总量都要计入（跨会话折叠不丢账）。
	if snapshot.TodayTotalTokens != 3020 {
		t.Fatalf("今日总量 = %d, want 3020", snapshot.TodayTotalTokens)
	}
}

// TestDSHGeneratingSessionUsesSingleRow 验证进行中的 DSH 会话只出现一行：
// 生成态来自 s.files（键是文件路径），必须用 state.agentID 而不是路径做身份，
// 否则会与已完成响应的会话行重复。
func TestDSHGeneratingSessionUsesSingleRow(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	endBase := now.UnixMilli()
	session := "gen00000-0000-4000-8000-0000000000c3"
	dshWriteFile(t, dshSessionPath(root, session), dshFrame(t,
		`{"type":"session","version":4,"id":"gen00000","cwd":"/Users/me/机架感知功能"}`,
		dshStepStart(1, 1, endBase-4000),
		dshTimedAssistantEvent("msg-done", endBase-3000,
			endBase-4000, endBase-4000, endBase-3500, endBase-3459,
			50, 30, 100, 150),
		dshStepStart(2, 1, endBase-800),
	))

	service := dshNewService(root, now)
	service.Poll()
	snapshot := service.Snapshot()

	if snapshot.GeneratingAgents != 1 {
		t.Fatalf("进行中的 DSH 会话应计为 1 个正在生成，实际 %d", snapshot.GeneratingAgents)
	}
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("进行中的 DSH 会话只应有一行，实际 %d 行: %+v",
			len(snapshot.Sessions), snapshot.Sessions)
	}
}

// TestRootSessionIDHandlesCyclesAndMissingLinks 保证父子链损坏（环）时不会死循环。
func TestRootSessionIDHandlesCyclesAndMissingLinks(t *testing.T) {
	service := New(Config{Now: dshFixedNow})
	if got := service.rootSessionID("missing"); got != "missing" {
		t.Fatalf("missing link should self-resolve, got %q", got)
	}
	service.sessionParent["a"] = "b"
	service.sessionParent["b"] = "a"
	if got := service.rootSessionID("a"); got != "a" && got != "b" {
		t.Fatalf("cycle should stop on a member, got %q", got)
	}
	service.sessionParent["self"] = "self"
	if got := service.rootSessionID("self"); got != "self" {
		t.Fatalf("self-parent should self-resolve, got %q", got)
	}
}
