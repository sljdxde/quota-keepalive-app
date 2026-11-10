package workbuddy

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WorkBuddy 的「每日活跃」不是一次模型对话，而是**计费域的一条 chat 事件**：
// 每个账号每个北京日往 https://www.codebuddy.cn/v2/report 上报一次
// chat_request_send，上游据此给成长中心记一天连登。用通用 provider 的
// 「发一条 hi 给免费模型」不会点亮连登，这就是旧实现的问题。
//
// 口径与 jichie/magpie-workbuddy-tasks 的 engine/daily.mjs 对齐。
const (
	// BillingBaseURL 是计费域（与对话域 copilot.tencent.com 不同）。
	BillingBaseURL = "https://www.codebuddy.cn"
	// BillingReportPath 是事件上报路径。
	BillingReportPath = "/v2/report"

	billingOrigin    = "https://www.codebuddy.cn"
	billingUserAgent = "WorkBuddy/5.8.3"
	dailyModelID     = "deepseek-v4-flash"
	dailyModelName   = "DeepSeek V4 Flash"
)

// DailyActivityEvent 是上报给计费域的单条 chat 事件。字段名与参考实现逐字对齐。
type DailyActivityEvent struct {
	EventCode        string `json:"eventCode"`
	Timestamp        int64  `json:"timestamp"`
	ReportDelay      int    `json:"reportDelay"`
	Mode             string `json:"mode"`
	ConversationID   string `json:"conversationId"`
	RequestID        string `json:"requestId"`
	InputLength      int    `json:"inputLength"`
	RequestModelID   string `json:"requestModelId"`
	RequestModelName string `json:"requestModelName"`
	IsPlan           bool   `json:"isPlan"`
	AgentName        string `json:"agentName"`
	AgentType        string `json:"agentType"`
	UserID           string `json:"userId"`
}

// DailyActivityEventFor 构造某个北京时刻的每日活跃事件。
func DailyActivityEventFor(uid string, now time.Time) DailyActivityEvent {
	ms := now.UnixMilli()
	cid := fmt.Sprintf("wb-task-%d-0", ms)
	return DailyActivityEvent{
		EventCode:        "chat_request_send",
		Timestamp:        ms,
		ReportDelay:      0,
		Mode:             "craft",
		ConversationID:   cid,
		RequestID:        cid + "-req",
		InputLength:      12,
		RequestModelID:   dailyModelID,
		RequestModelName: dailyModelName,
		IsPlan:           false,
		AgentName:        "default",
		AgentType:        "conversation",
		UserID:           uid,
	}
}

// BillingReportURL 返回每日活跃的上报地址。
func BillingReportURL() string {
	return BillingBaseURL + BillingReportPath
}

// DailyActivityHeaders 返回计费域额外头。Authorization / X-User-Id / X-Domain /
// X-Product 由 provider 插件在 host.Fetch 里按账号签名补上，这里只给计费侧
// 自己的头：Origin/Referer 指向 codebuddy.cn，X-CodeBuddy-Request 与设备指纹
// 是计费域要求的一部分（缺失会被当作网页请求拒掉）。
func DailyActivityHeaders(uid string, now time.Time) map[string]string {
	_ = now
	return map[string]string{
		"content-type":        "application/json",
		"accept":              "application/json, text/plain, */*",
		"origin":              billingOrigin,
		"referer":             billingOrigin + "/",
		"x-requested-with":    "XMLHttpRequest",
		"x-codebuddy-request": "1",
		"accept-language":     "zh-CN",
		"x-request-id":        uuidHex(),
		"x-machine-id":        deriveID(uid, "machine"),
		"x-session-id":        deriveID(uid, "session"),
	}
}

// DailyActivityResult 是计费域响应的脱敏解读。
type DailyActivityResult struct {
	OK           bool
	Code         int
	Message      string
	Restricted   bool
	Unauthorized bool
	Retryable    bool
}

// ParseDailyActivityResponse 解析 /v2/report 的信封：code==0 成功；
// HTTP 403 或 code 11140 视为账号级风控（不重试）；401 需要重新登录；
// 429/408/5xx 可重试；其余失败也留到下次重试（不把当天记成失败）。
func ParseDailyActivityResponse(status int, body []byte) DailyActivityResult {
	result := DailyActivityResult{}
	var envelope struct {
		Code json.Number `json:"code"`
		Msg  string      `json:"msg"`
		// 少数网关把错误放在 message 里。
		Message string `json:"message"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &envelope)
	}
	if code, err := envelope.Code.Int64(); err == nil {
		result.Code = int(code)
	} else if text := strings.TrimSpace(envelope.Code.String()); text != "" {
		fmt.Sscanf(text, "%d", &result.Code)
	}
	result.Message = firstNonEmpty(envelope.Msg, envelope.Message)

	switch {
	case status == httpStatusUnauthorized:
		result.Unauthorized = true
	case status == httpStatusForbidden:
		result.Restricted = true
	case result.Code == 11140:
		result.Restricted = true
	case status == httpStatusTooManyRequests, status == httpStatusRequestTimeout, status >= 500:
		result.Retryable = true
	case status >= 200 && status < 300 && result.Code == 0:
		result.OK = true
	default:
		// 上游拒绝但不是明确的鉴权/风控失败：按可重试处理，失败不消耗当天次数。
		result.Retryable = true
	}
	return result
}

// deriveID 与参考实现的 wb_fingerprint.derive_id 一致：md5("<salt>:<uid>") 后
// 改版本位/变体位，再做 8-4-4-4-12 连字符化。同一账号永远得到同一台「设备」。
func deriveID(uid, salt string) string {
	if uid == "" {
		uid = "anonymous"
	}
	sum := md5.Sum([]byte(salt + ":" + uid))
	h := hex.EncodeToString(sum[:])
	h = h[:12] + "4" + h[13:16] + "a" + h[17:]
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// uuidHex 生成 32 位无连字符 hex（请求 id 的形态）。
func uuidHex() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

const (
	httpStatusUnauthorized    = 401
	httpStatusForbidden       = 403
	httpStatusRequestTimeout  = 408
	httpStatusTooManyRequests = 429
)
