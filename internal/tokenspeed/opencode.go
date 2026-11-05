package tokenspeed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"qka/internal/applog"
)

const opencodeEventLimit = 20000

type opencodeSourceState struct {
	lastRowID     int64
	lastUpdatedMS int64
	// sig/sigSet 记录上次成功读取时的库文件指纹，用于跳过没有变化的轮询。
	sig    sourceSignature
	sigSet bool
	// lastErr 是上次读取失败的错误文本，用来「同一错误只记一次日志」。
	lastErr string
}

type opencodeEvent struct {
	Info opencodeInfo `json:"info"`
}

type opencodeInfo struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Role      string          `json:"role"`
	ModelID   string          `json:"modelID"`
	Model     opencodeModel   `json:"model"`
	Time      opencodeTime    `json:"time"`
	Tokens    *opencodeTokens `json:"tokens"`
}

type opencodeModel struct {
	ModelID string `json:"modelID"`
}

type opencodeTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type opencodeTokens struct {
	Input     json.Number        `json:"input"`
	Output    json.Number        `json:"output"`
	Reasoning json.Number        `json:"reasoning"`
	Total     json.Number        `json:"total"`
	Cache     opencodeTokenCache `json:"cache"`
}

type opencodeTokenCache struct {
	Read  json.Number `json:"read"`
	Write json.Number `json:"write"`
}

func (s *Service) readOpenCodeSources(ctx context.Context, now time.Time) {
	for _, root := range s.cfg.Roots {
		if root.Source != "opencode" && root.Source != "zcode" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		info, err := os.Stat(root.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		s.availableSources[root.Source] = true
		state := s.opencode[root.Path]
		if state == nil {
			state = &opencodeSourceState{}
			s.opencode[root.Path] = state
		}
		// 快路径：主库与 -wal 都没变化说明没有新提交，跳过本轮 sql.Open +
		// 查询。zcode 的 session 库常在 GB 级，750ms 一次打开代价不小。
		sig := sqliteSignature(root.Path)
		if state.sigSet && sig == state.sig {
			continue
		}
		var readErr error
		if root.Source == "zcode" {
			readErr = s.readZCodeMessages(root.Path, state, now)
		} else {
			readErr = s.readOpenCodeEvents(root.Path, state, now)
		}
		// 只有读成功才记住签名：锁冲突等临时失败下轮仍会重试。
		if readErr == nil {
			state.sig = sig
			state.sigSet = true
			state.lastErr = ""
			continue
		}
		// 读取失败必须留痕：这类数据源以前是静默失败的（DSN 拼错导致 Windows
		// 上永远读不到记录、速度恒为 0，日志里一个字都没有）。同一条错误只记
		// 一次（状态恢复后才重置），既能看到问题又不会每 750ms 刷屏。
		if msg := readErr.Error(); msg != state.lastErr {
			state.lastErr = msg
			applog.Warnf("tokenspeed", "读取 %s 数据源失败 path=%s: %v", root.Source, root.Path, readErr)
		}
	}
}

// sourceSignature 是 sqlite 数据文件的「有无变化」指纹（含 WAL）。
type sourceSignature struct {
	size     int64
	modNS    int64
	walSize  int64
	walModNS int64
}

// sqliteSignature 读取主库与 -wal 的大小/mtime；文件缺失按零值处理。
func sqliteSignature(path string) sourceSignature {
	var sig sourceSignature
	if info, err := os.Stat(path); err == nil {
		sig.size = info.Size()
		sig.modNS = info.ModTime().UnixNano()
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		sig.walSize = info.Size()
		sig.walModNS = info.ModTime().UnixNano()
	}
	return sig
}

type zcodeMessage struct {
	Role       string       `json:"role"`
	ModelID    string       `json:"modelId"`
	ModelIDAlt string       `json:"modelID"`
	Tokens     *zcodeTokens `json:"tokens"`
	Time       opencodeTime `json:"time"`
}

type zcodeTokens struct {
	Input     json.Number        `json:"input"`
	Output    json.Number        `json:"output"`
	Reasoning json.Number        `json:"reasoning"`
	Total     json.Number        `json:"total"`
	Cache     opencodeTokenCache `json:"cache"`
}

// readZCodeMessages 读取 zcode 的 sqlite 会话库；返回值用于决定是否记住
// 本轮文件指纹（失败时下轮必须重试，不能当作「无变化」跳过）。
func (s *Service) readZCodeMessages(path string, state *opencodeSourceState, now time.Time) error {
	// sqlite 驱动在包初始化时注册；sql.Open 只在驱动名未知时失败，这里的常量
	// 驱动名不会失败，错误分支属于死代码，已删除。
	db, _ := sql.Open("sqlite", openCodeReadOnlyDSN(path))
	defer db.Close()
	db.SetMaxOpenConns(1)

	dayStartMS := beijingDayStart(now).UnixMilli()
	if state.lastUpdatedMS < dayStartMS-1 {
		state.lastUpdatedMS = dayStartMS - 1
	}
	rows, err := db.Query(`
SELECT m.id, m.session_id, m.time_created, m.time_updated, m.data,
       MIN(CASE WHEN json_extract(p.data, '$.type') = 'text'
                THEN p.time_created END),
       MIN(CASE WHEN json_extract(p.data, '$.type') = 'reasoning'
                THEN p.time_created END)
FROM message m
LEFT JOIN part p ON p.message_id = m.id
WHERE m.time_updated >= ?
GROUP BY m.id
ORDER BY m.time_updated, m.id
LIMIT ?`, state.lastUpdatedMS, opencodeEventLimit)
	if err != nil {
		return err
	}
	defer rows.Close()

	nowMS := now.UnixMilli()
	for rows.Next() {
		var id, sessionID string
		var createdMS, updatedMS int64
		var data []byte
		var firstText, firstReason sql.NullInt64
		if err := rows.Scan(
			&id,
			&sessionID,
			&createdMS,
			&updatedMS,
			&data,
			&firstText,
			&firstReason,
		); err != nil {
			return err
		}
		if updatedMS > state.lastUpdatedMS {
			state.lastUpdatedMS = updatedMS
		}
		s.ingestZCodeMessage(id, sessionID, createdMS, updatedMS, data,
			firstText, firstReason, nowMS)
	}
	// 先关闭消息游标再查会话元数据：连接池只有 1 条连接，游标未关时第二次
	// Query 会一直等连接。
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	s.ingestSQLiteSessions(db, "zcode", now)
	return nil
}

func (s *Service) ingestZCodeMessage(
	id, sessionID string,
	createdMS, updatedMS int64,
	data []byte,
	firstText, firstReason sql.NullInt64,
	nowMS int64,
) {
	var message zcodeMessage
	if json.Unmarshal(data, &message) != nil ||
		message.Role != "assistant" || id == "" || message.Tokens == nil {
		return
	}
	endMS := updatedMS
	final := endMS > createdMS
	if !final && nowMS-endMS <= staleAfter.Milliseconds() {
		endMS = nowMS
	}
	record := responseRecord{
		key:       "zcode:" + id,
		source:    "zcode",
		agentID:   "zcode:" + firstNonEmpty(sessionID, id),
		model:     firstNonEmpty(message.ModelID, message.ModelIDAlt),
		startMS:   createdMS,
		hasStart:  createdMS > 0,
		endMS:     endMS,
		final:     final,
		usage:     message.Tokens.normalized(),
		updatedAt: time.UnixMilli(endMS),
	}
	if firstText.Valid && firstText.Int64 >= createdMS {
		record.decodeStartMS = firstText.Int64
		record.hasDecodeStart = true
		record.firstTextMS = firstText.Int64
		record.hasFirstText = true
	}
	if firstReason.Valid && firstReason.Int64 >= createdMS {
		if !record.hasDecodeStart || firstReason.Int64 < record.decodeStartMS {
			record.decodeStartMS = firstReason.Int64
			record.hasDecodeStart = true
		}
		record.firstReasonMS = firstReason.Int64
		record.hasFirstReason = true
	}
	s.upsertRecord(record)
}

func (u *zcodeTokens) normalized() usage {
	// ZCode follows the AI SDK: input already contains cache reads/writes.
	input := numberTokens(u.Input)
	output := numberTokens(u.Output)
	total := numberTokens(u.Total)
	if total < input+output {
		total = input + output
	}
	return usage{
		input:     input,
		cached:    numberTokens(u.Cache.Read),
		cacheNew:  numberTokens(u.Cache.Write),
		output:    output,
		reasoning: numberTokens(u.Reasoning),
		total:     total,
	}
}

// readOpenCodeEvents 读取 opencode 的 event 表；返回值含义同 readZCodeMessages。
func (s *Service) readOpenCodeEvents(path string, state *opencodeSourceState, now time.Time) error {
	// 同上：驱动恒注册，sql.Open 不会失败。
	db, _ := sql.Open("sqlite", openCodeReadOnlyDSN(path))
	defer db.Close()
	db.SetMaxOpenConns(1)

	var maxRowID int64
	if db.QueryRow(`SELECT COALESCE(MAX(rowid), 0) FROM event`).Scan(&maxRowID) != nil {
		return errors.New("opencode: max rowid 查询失败")
	}
	if maxRowID < state.lastRowID {
		state.lastRowID = 0
	}

	rows, err := db.Query(`
SELECT rowid, data
FROM event
WHERE type = 'message.updated.1' AND rowid > ?
ORDER BY rowid
LIMIT ?`, state.lastRowID, opencodeEventLimit)
	if err != nil {
		return err
	}
	defer rows.Close()

	nowMS := now.UnixMilli()
	for rows.Next() {
		var rowID int64
		var data []byte
		// SQLite 驱动把 rowid 写回 int64、data 转 []byte，正常 schema 下 Scan
		// 不会失败；这里不再保留不可达的错误分支。
		_ = rows.Scan(&rowID, &data)
		state.lastRowID = rowID
		s.ingestOpenCodeEvent(data, nowMS)
	}
	// 同上：先释放游标，再复用这条连接读会话元数据。
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	s.ingestSQLiteSessions(db, "opencode", now)
	return nil
}

func (s *Service) ingestOpenCodeEvent(data []byte, nowMS int64) {
	var event opencodeEvent
	if json.Unmarshal(data, &event) != nil {
		return
	}
	info := event.Info
	if info.Role != "assistant" || info.ID == "" || info.Time.Created <= 0 || info.Tokens == nil {
		return
	}
	endMS := info.Time.Completed
	final := endMS > 0
	if endMS <= 0 {
		endMS = info.Time.Created
		if nowMS-endMS <= staleAfter.Milliseconds() {
			endMS = nowMS
		}
	}
	model := firstNonEmpty(info.ModelID, info.Model.ModelID)
	sessionID := firstNonEmpty(info.SessionID, info.ID)
	s.upsertRecord(responseRecord{
		key:            "opencode:" + info.ID,
		source:         "opencode",
		agentID:        "opencode:" + sessionID,
		model:          model,
		startMS:        info.Time.Created,
		hasStart:       true,
		endMS:          endMS,
		final:          final,
		usage:          info.Tokens.normalized(),
		noMeasuredRate: true,
		updatedAt:      time.UnixMilli(endMS),
	})
}

func (u *opencodeTokens) normalized() usage {
	cacheRead := numberTokens(u.Cache.Read)
	cacheWrite := numberTokens(u.Cache.Write)
	input := numberTokens(u.Input) + cacheRead + cacheWrite
	output := numberTokens(u.Output)
	total := numberTokens(u.Total)
	if total < input+output {
		total = input + output
	}
	return usage{
		input:     input,
		cached:    cacheRead,
		cacheNew:  cacheWrite,
		output:    output,
		reasoning: numberTokens(u.Reasoning),
		total:     total,
	}
}

// openCodeReadOnlyDSN 构造 sqlite 只读 DSN。
//
// 必须用「file:///C:/...」这种带三斜杠的文件 URI：Windows 的绝对路径是
// C:\Users\...（ToSlash 后 C:/Users/...），直接塞进 url.URL{Path:...} 会得到
// file:C:/Users/...，sqlite 的 URI 解析器把 "C:" 当成 authority，报
// 「invalid uri authority: C:」——整个 zcode/opencode 数据源在 Windows 上
// 因此从未读到过任何记录（Token 速度恒为 0）。
//
// 相对路径不能走 url.URL.String()：Scheme 非空且 Path 不以 "/" 开头时，Go 会
// 补出「//」，把第一段目录当成主机名（file://cache/db.sqlite），库就打不开了。
// 所以这里手工拼接：绝对路径是 file:// + 前导斜杠路径（共三斜杠），相对路径是
// file: + 路径（无斜杠）。
func openCodeReadOnlyDSN(path string) string {
	slashPath := filepath.ToSlash(path)
	absolute := isAbsoluteSlashPath(slashPath)
	if absolute && !strings.HasPrefix(slashPath, "/") {
		// Windows 盘符：C:/x → /C:/x，才能得到 file:///C:/x。
		slashPath = "/" + slashPath
	}
	escaped := (&url.URL{Path: slashPath}).EscapedPath()

	query := url.Values{}
	query.Set("mode", "ro")
	query.Set("_pragma", "busy_timeout(2000)")

	var builder strings.Builder
	builder.WriteString("file:")
	if absolute {
		builder.WriteString("//") // 与下面的前导 "/" 合成三斜杠
	}
	builder.WriteString(escaped)
	builder.WriteByte('?')
	builder.WriteString(query.Encode())
	return builder.String()
}

// isAbsoluteSlashPath 报告 ToSlash 后的路径是否绝对：POSIX 的 "/x" 或 Windows
// 的 "C:/x"。
func isAbsoluteSlashPath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && p[2] == '/'
}
