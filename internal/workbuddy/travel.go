package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 猫猫旅行（WorkBuddy 国内版成长中心的 Buddy 旅行）：
// 状态查询 → 归来领奖 / 在家派出 / 旅行中跳过。没有 Buddy 实例时先完成并
// 领取 first_buddy（auto 任务，奖励即 Buddy），再重试一次派出。
//
// 口径与 jichie/magpie-workbuddy-tasks 的 engine/travel.mjs 对齐；所有请求由
// provider 插件在 host.Fetch 里签名，本包不接触 token。

const (
	travelStatusPath = "/activity/growth/buddy/travel/status"
	travelConfigPath = "/activity/growth/buddy/travel/config"
	travelDepartPath = "/activity/growth/buddy/travel/depart"
	travelClaimPath  = "/activity/growth/buddy/travel/claim"
	tasksPath        = "/v2/activity/growth/tasks"
	tasksClaimFormat = "/activity/growth/tasks/%s/claim"
	tasksReportPath  = "/v2/report"
	firstBuddyCode   = "first_buddy"

	// upstreamGap 是任意两次上游调用之间的最小间隔（参考实现的防风控铁律 1s）。
	upstreamGap = time.Second
	// settleWait 是上报事件后等待上游落账的时间。
	settleWait = 1500 * time.Millisecond
	// progressWait 是进度已经在涨时的再等间隔。
	progressWait = 2500 * time.Millisecond
)

// TravelFetcher 执行一次签名后的 HTTP 请求，返回状态码与响应体。
type TravelFetcher func(ctx context.Context, method, url string, body []byte) (int, []byte, error)

// TravelConfig 注入 HTTP 通道、时钟与等待（测试可替换）。
type TravelConfig struct {
	BaseURL string
	UID     string
	Fetch   TravelFetcher
	Now     func() time.Time
	Sleep   func(ctx context.Context, d time.Duration) error
}

// TravelResult 是一次旅行推进的脱敏结果。
type TravelResult struct {
	OK         bool    `json:"ok"`
	Action     string  `json:"action,omitempty"` // claim/depart/traveling/idle/skip
	Message    string  `json:"message,omitempty"`
	Credit     float64 `json:"credit,omitempty"`
	Restricted bool    `json:"restricted,omitempty"`
}

// TravelStatus 是猫猫旅行的当前状态（供界面展示与手动操作）。
type TravelStatus struct {
	OK                bool    `json:"ok"`
	State             string  `json:"state,omitempty"`
	DailyLimitReached bool    `json:"daily_limit_reached,omitempty"`
	Location          string  `json:"location,omitempty"`
	DepartAt          int64   `json:"depart_at,omitempty"`
	ArriveAt          int64   `json:"arrive_at,omitempty"`
	DurationHours     float64 `json:"duration_hours,omitempty"`
	RewardCredit      float64 `json:"reward_credit,omitempty"`
	Restricted        bool    `json:"restricted,omitempty"`
	Message           string  `json:"message,omitempty"`
}

// TravelRunner 运行一个 WorkBuddy 账号的旅行闭环。
type TravelRunner struct {
	base  string
	uid   string
	fetch TravelFetcher
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// NewTravelRunner 构造旅行执行器。
func NewTravelRunner(cfg TravelConfig) *TravelRunner {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return nil
			}
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &TravelRunner{base: base, uid: cfg.UID, fetch: cfg.Fetch, now: now, sleep: sleep}
}

type travelEnvelope struct {
	Code json.Number     `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// call 发一次请求并解析信封；返回 code、msg、data 与是否受限。
func (r *TravelRunner) call(ctx context.Context, method, path string, body []byte) (int, int, string, json.RawMessage, bool, error) {
	status, raw, err := r.fetch(ctx, method, r.base+path, body)
	if err != nil {
		return 0, 0, "", nil, false, err
	}
	restricted := status == 403
	var envelope travelEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &envelope)
	}
	code := 0
	if n, err := envelope.Code.Int64(); err == nil {
		code = int(n)
	}
	if code == 11140 {
		restricted = true
	}
	return status, code, envelope.Msg, envelope.Data, restricted, nil
}

// paced 先等够防风控间隔再发请求：任意两次上游调用之间至少 upstreamGap。
func (r *TravelRunner) paced(
	ctx context.Context, method, path string, body []byte,
) (int, int, string, json.RawMessage, bool, error) {
	if err := r.sleep(ctx, upstreamGap); err != nil {
		return 0, 0, "", nil, false, err
	}
	return r.call(ctx, method, path, body)
}

func okEnvelope(status, code int) bool {
	return status >= 200 && status < 300 && code == 0
}

// Run 执行一次旅行推进：归来领奖 / 在家派出 / 旅行中报告状态。
func (r *TravelRunner) Run(ctx context.Context) (TravelResult, error) {
	status, err := r.Status(ctx)
	if err != nil {
		return TravelResult{}, err
	}
	if status.Restricted {
		return TravelResult{OK: false, Restricted: true, Message: "账号已被风控限制"}, nil
	}
	if !status.OK {
		return TravelResult{OK: false, Message: "查询旅行状态失败：" + status.Message}, nil
	}
	switch status.State {
	case "arrived":
		return r.claim(ctx)
	case "idle":
		if status.DailyLimitReached {
			return TravelResult{OK: true, Action: "idle",
				Message: "猫猫今日已完成旅行，明天 00:00 刷新"}, nil
		}
		return r.depart(ctx)
	case "traveling":
		return TravelResult{OK: true, Action: "traveling",
			Message: "猫猫正在旅行途中，归来后会自动领奖并再次派出"}, nil
	default:
		return TravelResult{OK: true, Action: status.State,
			Message: "当前旅行状态：" + status.State}, nil
	}
}

// Status 查询旅行状态；用于界面展示与「旅行回来领奖」的检测。
func (r *TravelRunner) Status(ctx context.Context) (TravelStatus, error) {
	httpStatus, code, msg, data, restricted, err := r.paced(ctx, "GET", travelStatusPath, nil)
	if err != nil {
		return TravelStatus{}, err
	}
	if restricted {
		return TravelStatus{Restricted: true, Message: "账号已被风控限制"}, nil
	}
	if !okEnvelope(httpStatus, code) {
		message := msg
		if message == "" {
			message = fmt.Sprintf("HTTP %d", httpStatus)
		}
		return TravelStatus{Message: message}, nil
	}
	var payload struct {
		State             string  `json:"state"`
		DailyLimitReached bool    `json:"daily_limit_reached"`
		DepartAt          int64   `json:"depart_at"`
		ArriveAt          int64   `json:"arrive_at"`
		DurationHours     float64 `json:"duration_hours"`
		RewardCredit      float64 `json:"reward_credit"`
		Location          *struct {
			Name string `json:"name"`
		} `json:"location"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return TravelStatus{Message: "旅行状态格式无法识别"}, nil
	}
	out := TravelStatus{
		OK: true, State: payload.State,
		DailyLimitReached: payload.DailyLimitReached,
		DepartAt:          payload.DepartAt, ArriveAt: payload.ArriveAt,
		DurationHours: payload.DurationHours, RewardCredit: payload.RewardCredit,
	}
	if payload.Location != nil {
		out.Location = payload.Location.Name
	}
	return out, nil
}

func (r *TravelRunner) claim(ctx context.Context) (TravelResult, error) {
	status, code, msg, data, restricted, err := r.paced(ctx, "POST", travelClaimPath, []byte("{}"))
	if err != nil {
		return TravelResult{}, err
	}
	if restricted {
		return TravelResult{OK: false, Restricted: true, Message: "领奖失败：账号已被风控限制"}, nil
	}
	if !okEnvelope(status, code) {
		reason := msg
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", status)
		}
		return TravelResult{OK: false, Message: "旅行归来领奖失败：" + reason}, nil
	}
	var claim struct {
		Credit float64 `json:"reward_credit"`
	}
	_ = json.Unmarshal(data, &claim)
	message := "旅行归来领奖成功！"
	if claim.Credit > 0 {
		message = fmt.Sprintf("旅行归来领奖成功！获得 %g 积分", claim.Credit)
	}
	return TravelResult{OK: true, Action: "claim", Credit: claim.Credit, Message: message}, nil
}

func (r *TravelRunner) depart(ctx context.Context) (TravelResult, error) {
	// 目的地清单：官方前端要求 body 带 location_id，空 body 会被 400 拒绝。
	lid := 1
	status, code, _, data, _, err := r.paced(ctx, "GET", travelConfigPath, nil)
	if err == nil && okEnvelope(status, code) {
		var config struct {
			Locations []struct {
				ID json.Number `json:"id"`
			} `json:"locations"`
		}
		if json.Unmarshal(data, &config) == nil && len(config.Locations) > 0 {
			if id, convErr := config.Locations[0].ID.Int64(); convErr == nil && id > 0 {
				lid = int(id)
			}
		}
	}
	dep, err := r.departOnce(ctx, lid)
	if err != nil {
		return TravelResult{}, err
	}
	if dep.Restricted {
		return dep, nil
	}
	if dep.OK {
		return dep, nil
	}
	// 没有 Buddy 实例：先完成并领取 first_buddy，再重试一次派出。
	if !strings.Contains(strings.ToLower(dep.Message), "no active buddy") {
		return dep, nil
	}
	ready, err := r.ensureBuddy(ctx)
	if err != nil {
		return TravelResult{}, err
	}
	if !ready {
		return TravelResult{OK: false,
			Message: "还没有 Buddy：需要先完成并领取「领取一只 Buddy」任务"}, nil
	}
	return r.departOnce(ctx, lid)
}

func (r *TravelRunner) departOnce(ctx context.Context, locationID int) (TravelResult, error) {
	body, _ := json.Marshal(map[string]int{"location_id": locationID})
	status, code, msg, data, restricted, err := r.paced(ctx, "POST", travelDepartPath, body)
	if err != nil {
		return TravelResult{}, err
	}
	if restricted {
		return TravelResult{OK: false, Restricted: true, Message: "派出旅行失败：账号已被风控限制"}, nil
	}
	if !okEnvelope(status, code) {
		reason := msg
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", status)
		}
		return TravelResult{OK: false, Message: "派出旅行失败：" + reason}, nil
	}
	var depart struct {
		Location struct {
			Name string `json:"name"`
		} `json:"location"`
	}
	_ = json.Unmarshal(data, &depart)
	where := strings.TrimSpace(depart.Location.Name)
	if where == "" {
		return TravelResult{OK: true, Action: "depart", Message: "猫猫已经出发旅行啦"}, nil
	}
	return TravelResult{OK: true, Action: "depart",
		Message: fmt.Sprintf("猫猫已出发前往「%s」，预计数小时后归来！", where)}, nil
}

type travelTask struct {
	Code         string `json:"task_code"`
	Type         string `json:"task_type"`
	AcceptStatus string `json:"accept_status"`
	Progress     struct {
		Current int `json:"current"`
		Target  int `json:"target"`
	} `json:"progress"`
}

func (r *TravelRunner) tasks(ctx context.Context) ([]travelTask, bool, error) {
	status, code, _, data, restricted, err := r.paced(ctx, "GET", tasksPath, nil)
	if err != nil {
		return nil, false, err
	}
	if restricted {
		return nil, true, nil
	}
	if !okEnvelope(status, code) {
		return []travelTask{}, false, nil
	}
	var payload struct {
		Tasks []travelTask `json:"tasks"`
	}
	_ = json.Unmarshal(data, &payload)
	return payload.Tasks, false, nil
}

// ensureBuddy 确保账号有 Buddy 实例：first_buddy 是 auto 任务，上报一次对话
// 事件点亮进度，再领奖；返回是否已 ready。
func (r *TravelRunner) ensureBuddy(ctx context.Context) (bool, error) {
	tasks, restricted, err := r.tasks(ctx)
	if err != nil {
		return false, err
	}
	if restricted {
		return false, nil
	}
	fb := findTask(tasks, firstBuddyCode)
	if fb == nil {
		return false, nil
	}
	if fb.AcceptStatus == "claimed" {
		return true, nil
	}
	if fb.Progress.Current < maxInt(fb.Progress.Target, 1) &&
		fb.AcceptStatus != "completed" && fb.AcceptStatus != "claimed" {
		// auto 任务不接受接取；直接上报一条 chat 事件点亮进度。
		event := DailyActivityEventFor(r.uid, r.now())
		body, _ := json.Marshal([]DailyActivityEvent{event})
		status, code, _, _, restricted, err := r.paced(ctx, "POST", tasksReportPath, body)
		if err != nil {
			return false, err
		}
		if restricted || !okEnvelope(status, code) {
			return false, nil
		}
		// 等上游落账，快查几次进度。
		if err := r.waitForProgress(ctx, firstBuddyCode); err != nil {
			return false, err
		}
		fresh, restricted, err := r.tasks(ctx)
		if err != nil {
			return false, err
		}
		if restricted {
			return false, nil
		}
		fb = findTask(fresh, firstBuddyCode)
		if fb == nil || (fb.Progress.Current < maxInt(fb.Progress.Target, 1) &&
			fb.AcceptStatus != "completed" && fb.AcceptStatus != "claimed") {
			return false, nil
		}
	}
	// 领奖（first_buddy 的奖励就是 Buddy 实例）。
	path := fmt.Sprintf(tasksClaimFormat, firstBuddyCode)
	status, code, _, _, restricted, err := r.paced(ctx, "POST", path, []byte("{}"))
	if err != nil {
		return false, err
	}
	if restricted || !okEnvelope(status, code) {
		return false, nil
	}
	fresh, restricted, err := r.tasks(ctx)
	if err != nil {
		return false, err
	}
	if restricted {
		return false, nil
	}
	fb = findTask(fresh, firstBuddyCode)
	return fb != nil && fb.AcceptStatus == "claimed", nil
}

// waitForProgress 上报后轮询任务进度，最多 4 次；已达成即返回。
func (r *TravelRunner) waitForProgress(ctx context.Context, code string) error {
	for attempt := 0; attempt < 4; attempt++ {
		wait := settleWait
		if attempt >= 2 {
			wait = progressWait
		}
		if err := r.sleep(ctx, wait); err != nil {
			return err
		}
		tasks, restricted, err := r.tasks(ctx)
		if err != nil {
			return err
		}
		if restricted {
			return nil
		}
		task := findTask(tasks, code)
		if task != nil && (task.AcceptStatus == "completed" || task.AcceptStatus == "claimed" ||
			task.Progress.Current >= maxInt(task.Progress.Target, 1)) {
			return nil
		}
	}
	return nil
}

func findTask(tasks []travelTask, code string) *travelTask {
	for i := range tasks {
		if tasks[i].Code == code {
			return &tasks[i]
		}
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
