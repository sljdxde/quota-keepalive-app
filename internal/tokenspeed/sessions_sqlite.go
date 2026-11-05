package tokenspeed

import (
	"database/sql"
	"strings"
	"time"
)

// sqliteSessionWindow 是会话元数据的回溯窗口：面板只展示最近 10 分钟活跃的
// 会话，回溯 24 小时足够覆盖跨零点的窗口，又不会让查询随库龄无限变大。
const sqliteSessionWindow = 24 * time.Hour

// sqliteSessionLimit 限制一次读取的会话元数据行数：会话表会积累上千条历史，
// 本轮只需要最近活跃的那些。
const sqliteSessionLimit = 300

// ingestSQLiteSessions 读取 sqlite 数据源（opencode / zcode）的会话标题与
// 工作区目录，让会话行显示真实的会话名而不是模型名。
//
// 这两个 Agent 把会话名放在各自的 session 表里，消息事件本身只带 model：
// 以前只按项目名/模型名兜底，面板上就出现「autoclaw-cn/zai_glm-5.3-flash」
// 这种模型标识当会话名的现象。
//
// 红线：只取 title 与 directory 两列——它们是 Agent 自己展示的会话名与工作区，
// 不读取消息体、提示词或响应正文。titles 由 Agent 生成（首条输入回退或
// LLM 生成），与 DSH 的 session/title 事件同一性质。
func (s *Service) ingestSQLiteSessions(db *sql.DB, source string, now time.Time) {
	if db == nil {
		return
	}
	rows, err := db.Query(`
SELECT id, title, directory
FROM session
WHERE time_updated >= ?
ORDER BY time_updated DESC
LIMIT ?`, now.Add(-sqliteSessionWindow).UnixMilli(), sqliteSessionLimit)
	if err != nil {
		// 老版本库可能没有 session 表：会话名退回项目名/模型名，不影响记账。
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var title, directory sql.NullString
		if rows.Scan(&id, &title, &directory) != nil {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		agentID := source + ":" + id
		if name := normalizeSessionTitle(title.String); name != "" {
			s.sessionTitles[agentID] = name
		}
		if project := projectBaseName(directory.String); project != "" {
			s.projectByAgent[agentID] = project
		}
	}
}
