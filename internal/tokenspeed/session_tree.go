package tokenspeed

import (
	"strings"
	"unicode/utf8"
)

// normalizeSessionRef strips the on-disk "session-" directory prefix from a
// parent session reference so it matches dshSessionID's normalized form.
func normalizeSessionRef(ref string) string {
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "session-")
}

// rootSessionID follows parentSession links to the top-level conversation a
// sub-agent session belongs to. Cycles or missing links stop at the current id.
func (s *Service) rootSessionID(session string) string {
	current := session
	for i := 0; i < 64; i++ {
		parent := s.sessionParent[current]
		if parent == "" || parent == current {
			return current
		}
		current = parent
	}
	return current
}

// canonicalAgentID folds a DSH sub-agent session into its top-level session so
// the token panel shows one row per conversation instead of one per sub-agent.
// Other sources keep their existing identity.
func (s *Service) canonicalAgentID(agentID string) string {
	if !strings.HasPrefix(agentID, "dsh:") {
		return agentID
	}
	session := strings.TrimPrefix(agentID, "dsh:")
	root := s.rootSessionID(session)
	if root == session {
		return agentID
	}
	return "dsh:" + root
}

// sessionLabel prefers the conversation's own title; otherwise it falls back to
// project, then model, then a short id.
//
// sessionTitles is keyed by agentID（"dsh:<session>"、"zcode:<id>" 等）。标题来源
// 不止 DSH：opencode/zcode 的 session 表也给会话起了名字，缺了它面板只能拿
// 模型名充当会话名。
func (s *Service) sessionLabel(agentID, project, model string) string {
	if title := strings.TrimSpace(s.sessionTitles[agentID]); title != "" {
		return title
	}
	return sessionDisplayLabel(project, model, agentID)
}

// normalizeSessionTitle 把会话标题收敛成面板可用的单行文本。
//
// Agent 生成的标题可能带换行与多余空白，首条输入回退出来的标题还可能很长；
// 面板是单行显示 + 工具提示，这里折叠空白并截断到可读长度，避免把整段输入
// 原样塞进界面。
func normalizeSessionTitle(raw string) string {
	title := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if title == "" {
		return ""
	}
	const maxTitleRunes = 60
	if utf8.RuneCountInString(title) > maxTitleRunes {
		title = string([]rune(title)[:maxTitleRunes]) + "…"
	}
	return title
}
