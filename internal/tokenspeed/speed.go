package tokenspeed

import (
	"math"
	"sort"
	"time"
)

type timedResponse struct {
	responseRecord
	startMS    int64
	durationMS int64
}

type speedEstimate struct {
	output float64
	total  float64
}

type sessionRow struct {
	view     SessionSnapshot
	lastMS   int64
	hasSpeed bool
}

func lessSessionRow(a, b sessionRow) bool {
	if a.view.Generating != b.view.Generating {
		return a.view.Generating
	}
	if a.hasSpeed != b.hasSpeed {
		return a.hasSpeed
	}
	if a.view.OutputTokensPerSecond != b.view.OutputTokensPerSecond {
		return a.view.OutputTokensPerSecond > b.view.OutputTokensPerSecond
	}
	if a.lastMS != b.lastMS {
		return a.lastMS > b.lastMS
	}
	return a.view.ID < b.view.ID
}

func (s *Service) ingest(source, fileID string, state *fileState, line []byte) {
	switch source {
	case "codex":
		s.ingestCodex(fileID, state, line)
	case "claude":
		s.ingestClaudeSource(source, fileID, state, line)
	case "qoder", "qoder-cn":
		s.ingestClaudeSource(source, fileID, state, line)
	case "workbuddy", "codebuddy":
		s.ingestWorkBuddy(source, fileID, state, line)
	}
}

const (
	maxDecodeRate   = 10000.0
	minDecodeMS     = int64(100)
	heldShare       = int64(4)
	thinkPace       = int64(20)
	codexWindowMS   = int64(1000)
	codexBodyTokens = int64(100)
	floorBodyTokens = int64(1000)
	floorWindowMS   = int64(1000)
	maxResponseAge  = 15 * time.Minute
)

type liveRate struct {
	source string
	model  string
	output float64
	total  float64
	endMS  int64
}

func responseRate(response timedResponse) (liveRate, bool) {
	tokens, windowMS, overallMS := responseMeasure(response)
	if tokens <= 0 || windowMS <= 0 || overallMS <= 0 {
		return liveRate{}, false
	}
	seconds := float64(windowMS) / 1000
	output := float64(tokens) / seconds
	input := float64(maxInt64(0, response.usage.input)) /
		(float64(overallMS) / 1000)
	return liveRate{
		source: response.source,
		model:  response.model,
		output: output,
		total:  output + input,
		endMS:  response.endMS,
	}, true
}

func responseMeasure(response timedResponse) (tokens, windowMS, overallMS int64) {
	if response.noMeasuredRate || response.endMS <= 0 || response.startMS <= 0 {
		return 0, 0, 0
	}
	overallMS = response.endMS - response.startMS
	if overallMS <= 0 || overallMS > maxResponseAge.Milliseconds() {
		return 0, 0, 0
	}
	tokens, windowMS = decodedResponse(response.responseRecord)
	if windowMS == 0 {
		tokens, windowMS = floorResponse(response.responseRecord, overallMS)
	}
	if tokens <= 0 || windowMS <= 0 {
		return 0, 0, overallMS
	}
	return tokens, windowMS, overallMS
}

func decodedResponse(record responseRecord) (tokens, windowMS int64) {
	if record.usage.output <= 0 || !record.hasDecodeStart ||
		record.decodeStartMS <= 0 || record.endMS <= record.decodeStartMS {
		return 0, 0
	}
	tokens = record.usage.output
	startMS := record.decodeStartMS
	if record.usage.reasoning > 0 {
		switch {
		case record.hasFirstText && record.firstTextMS >= startMS:
			tokens -= record.usage.reasoning
			startMS = record.firstTextMS
		case record.hasFirstReason && record.firstReasonMS >= startMS &&
			record.flowMS > 0 && record.usage.reasoning <= tokens:
			startMS = record.firstReasonMS
		default:
			return 0, 0
		}
	}
	windowMS = decodeWindow(tokens, record.endMS, startMS)
	if windowMS == 0 {
		return 0, 0
	}
	if record.flowMS > 0 && record.flowMS*heldShare < windowMS {
		return 0, 0
	}
	if record.usage.reasoning > 0 && record.hasFirstText &&
		tokens*(record.firstTextMS-record.decodeStartMS) >
			thinkPace*record.usage.reasoning*windowMS {
		return 0, 0
	}
	if record.source == "codex" &&
		(windowMS < codexWindowMS || tokens < codexBodyTokens) {
		return 0, 0
	}
	return tokens, windowMS
}

func decodeWindow(tokens, endMS, startMS int64) int64 {
	windowMS := endMS - startMS
	if tokens <= 0 || startMS <= 0 || endMS <= startMS || windowMS < minDecodeMS {
		return 0
	}
	if tokens*1000 > int64(maxDecodeRate)*windowMS {
		return 0
	}
	return windowMS
}

func floorResponse(record responseRecord, overallMS int64) (tokens, windowMS int64) {
	if record.noMeasuredRate || overallMS <= 0 || record.usage.output <= 0 {
		return 0, 0
	}
	if record.source == "codex" && record.usage.reasoning > 0 &&
		record.hasDecodeStart && record.decodeStartMS > 0 {
		answer := record.usage.output - record.usage.reasoning
		if answer >= codexBodyTokens {
			if w := decodeWindow(answer, record.endMS, record.decodeStartMS); w >= codexWindowMS {
				return answer, w
			}
		}
	}
	if overallMS < floorWindowMS || record.usage.output < floorBodyTokens {
		return 0, 0
	}
	if w := decodeWindow(record.usage.output, record.endMS, record.startMS); w > 0 {
		return record.usage.output, w
	}
	return 0, 0
}

func (s *Service) computeSnapshot(now time.Time) Snapshot {
	nowMS := now.UnixMilli()
	records := make([]responseRecord, 0, len(s.records))
	for _, record := range s.records {
		// Fold sub-agent sessions into their top-level session so the panel
		// shows one row per conversation, and use the root's project name.
		record.agentID = s.canonicalAgentID(record.agentID)
		if project := s.projectByAgent[record.agentID]; project != "" {
			record.project = project
		}
		records = append(records, record)
	}
	sortedRecent(records)

	measured := make([]timedResponse, 0, len(records))
	unmeasured := make([]timedResponse, 0, len(records))
	for _, record := range records {
		timed, ok := asTimed(record)
		if !ok {
			continue
		}
		if record.final {
			measured = append(measured, timed)
		} else {
			unmeasured = append(unmeasured, timed)
		}
	}

	recentByAgent, fallbackBySource := buildReferences(measured, nowMS)

	// 窗口内贡献：按 agent 累加「token 贡献」（tok/s × 秒），最后统一除以窗口
	// 长度得到 tok/s。已完成响应按区间重叠折算，在飞会话按参考速率估算。
	// 这与 agent-speed 的实时速率口径一致：数字随滑动窗口平滑上升与回落。
	windowFromMS := nowMS - liveRateWindow.Milliseconds()
	outputContrib := make(map[string]float64)
	totalContrib := make(map[string]float64)
	estimatedContrib := make(map[string]float64)
	rateMeta := make(map[string]liveRate)
	generatingBySource := make(map[string]int)
	generatingByAgent := make(map[string]bool)
	lastActiveByAgent := make(map[string]int64)
	// lastResponseEndByAgent 是每个 agent 最近一条已完成响应的结束时间：
	// 速率「保留」要按这个时刻的 3 秒窗口取值，而不是按最后活动时间（工具
	// 事件会把活动时间推到现在，但那一刻并没有新的 token）。
	lastResponseEndByAgent := make(map[string]int64)
	// agentMeta 记录每个 agent 最近响应的来源与模型，供没有实时窗口贡献、
	// 只靠保留速率的 agent 也能生成正确的来源/会话行。
	agentMeta := make(map[string]liveRate)
	// busyByAgent 记录有后台任务在跑（工具执行）的 agent：这段时间没有 token，
	// 但会话确实在忙，保留速率可以一直显示到任务结束；普通闲置只保留 15 秒，
	// 让数字在 agent 真正停下后尽快回到 0（用户反馈「变动非常慢」）。
	busyByAgent := make(map[string]bool)
	generatingAgents := 0

	rememberActive := func(agentID string, atMS int64) {
		if atMS > lastActiveByAgent[agentID] {
			lastActiveByAgent[agentID] = atMS
		}
	}
	rememberResponseEnd := func(agentID string, endMS int64) {
		if endMS > lastResponseEndByAgent[agentID] {
			lastResponseEndByAgent[agentID] = endMS
		}
	}
	rememberMeta := func(agentID, source, model string, atMS int64) {
		current, ok := agentMeta[agentID]
		if !ok || atMS >= current.endMS {
			agentMeta[agentID] = liveRate{source: source, model: model, endMS: atMS}
		}
	}

	// 已完成响应：只统计与窗口有重叠的部分，按实测区间的重叠比例折算 token。
	for _, response := range measured {
		if response.endMS > nowMS {
			continue
		}
		rememberActive(response.agentID, response.endMS)
		rememberResponseEnd(response.agentID, response.endMS)
		rememberMeta(response.agentID, response.source, response.model, response.endMS)
		tokens, windowMS, overallMS := responseMeasure(response)
		if tokens <= 0 || windowMS <= 0 {
			continue
		}
		outTokens := measuredWindowTokens(response.endMS, windowMS, tokens, windowFromMS, nowMS)
		if outTokens <= 0 {
			continue
		}
		inTokens := 0.0
		if overallMS > 0 && response.usage.input > 0 {
			inTokens = inputWindowTokens(response.startMS, response.endMS,
				response.usage.input, windowFromMS, nowMS)
		}
		outputContrib[response.agentID] += outTokens
		totalContrib[response.agentID] += outTokens + inTokens
		if current, ok := rateMeta[response.agentID]; !ok || response.endMS >= current.endMS {
			rateMeta[response.agentID] = liveRate{
				source: response.source,
				model:  response.model,
				output: float64(tokens) / (float64(windowMS) / 1000),
				endMS:  response.endMS,
			}
		}
	}

	// 生成中的会话：参考速率 × 已在飞时长，起点取「在飞开始」与「最近一条
	// 已完成响应结束」的较大者，避免把仍在收尾的响应算两遍。
	estimateInFlight := func(agentID, source, model string, startMS int64) {
		estimate, ok := referenceFor(agentID, source, recentByAgent, fallbackBySource)
		if !ok {
			return
		}
		estimateStart := startMS
		if last := lastActiveByAgent[agentID]; last > estimateStart {
			estimateStart = last
		}
		overlap := windowOverlapMS(estimateStart, nowMS, windowFromMS, nowMS)
		if overlap <= 0 {
			return
		}
		seconds := float64(overlap) / 1000
		outputContrib[agentID] += estimate.output * seconds
		totalContrib[agentID] += estimate.total * seconds
		estimatedContrib[agentID] += estimate.output * seconds
		if _, ok := rateMeta[agentID]; !ok {
			rateMeta[agentID] = liveRate{
				source: source,
				model:  model,
				output: estimate.output,
				total:  estimate.total,
				endMS:  nowMS,
			}
		}
	}

	for _, state := range s.files {
		// s.files 的键是文件路径，会话身份必须用 state.agentID：DSH 一个会话
		// 一个文件，用路径会把同一个会话拆成「路径哈希」的额外一行；子代理
		// 会话再经 canonicalAgentID 折叠回顶层会话。
		agentID := s.canonicalAgentID(state.agentID)
		if agentID == "" {
			continue
		}
		rememberMeta(agentID, state.source, state.model, state.lastLineMS)
		if state.jobOpen > 0 {
			busyByAgent[agentID] = true
		}
		if !state.active || state.lastLineMS == 0 ||
			nowMS-state.lastLineMS > staleAfter.Milliseconds() {
			rememberActive(agentID, state.lastLineMS)
			continue
		}
		start := state.inFlightMS
		if start == 0 {
			start = state.pendingMS
		}
		if start > nowMS {
			rememberActive(agentID, state.lastLineMS)
			continue
		}
		if generatingByAgent[agentID] {
			rememberActive(agentID, state.lastLineMS)
			continue
		}
		generatingAgents++
		generatingBySource[state.source]++
		generatingByAgent[agentID] = true
		estimateInFlight(agentID, state.source, state.model, start)
		rememberActive(agentID, state.lastLineMS)
	}

	for _, response := range unmeasured {
		rememberMeta(response.agentID, response.source, response.model, response.endMS)
		if nowMS-response.endMS > staleAfter.Milliseconds() ||
			generatingByAgent[response.agentID] {
			rememberActive(response.agentID, response.endMS)
			continue
		}
		generatingAgents++
		generatingBySource[response.source]++
		generatingByAgent[response.agentID] = true
		estimateInFlight(response.agentID, response.source, response.model, response.startMS)
		rememberActive(response.agentID, response.endMS)
	}

	// 逐 agent 折算回 tok/s；没有任何贡献的会话不进速度行（会话列表稍后
	// 由 records 兜底补齐，显示 0 速率）。
	ratesByAgent := make(map[string]liveRate)
	estimatedTokens := 0.0
	contributedTokens := 0.0
	for agentID, output := range outputContrib {
		if output <= 0 {
			continue
		}
		meta, ok := rateMeta[agentID]
		if !ok {
			continue
		}
		total := totalContrib[agentID]
		ratesByAgent[agentID] = liveRate{
			source: meta.source,
			model:  meta.model,
			output: liveRateFromContributions(output, liveRateWindow),
			total:  liveRateFromContributions(total, liveRateWindow),
			endMS:  meta.endMS,
		}
		contributedTokens += output
		estimatedTokens += estimatedContrib[agentID]
	}
	// 实时窗口只有 3 秒：DSH 这类按 step 批量落盘的来源，一次工具执行或长思考
	// 可以几分钟没有新的已完成响应，窗口里没有贡献，界面就会一直显示 0（用户
	// 反馈「速度经常是 0」）。对「最近仍有活动」的 agent，保留它最后一条响应
	// 结束时刻按同一 3 秒窗口算出的速率，并与实时值取较大者：数字只在新的实测
	// 响应结束时更新，不在两次响应之间断崖式掉到 0。普通闲置只保留 15 秒，
	// 让 agent 停下后数字尽快回落；工具执行期间（jobOpen）一直保留最后一次
	// 实测值。实时值更大时（流式来源正在生成）仍用实时值，保持灵敏。
	for agentID, endMS := range lastResponseEndByAgent {
		if endMS <= 0 {
			continue
		}
		// 普通闲置只保留 liveWindow（15 秒，与旧版一致），让 agent 真正停下
		// 后数字尽快回落；工具执行期间（jobOpen）没有 token，但会话在忙，
		// 保留到最后一次实测值，直到任务结束。
		if !busyByAgent[agentID] && nowMS-endMS > liveWindow.Milliseconds() {
			continue
		}
		heldOutput, heldTotal, ok := heldLiveRate(measured, agentID, endMS)
		if !ok {
			continue
		}
		if current, ok := ratesByAgent[agentID]; ok {
			if heldOutput > current.output {
				current.output = heldOutput
			}
			if heldTotal > current.total {
				current.total = heldTotal
			}
			ratesByAgent[agentID] = current
			continue
		}
		meta := agentMeta[agentID]
		ratesByAgent[agentID] = liveRate{
			source: meta.source,
			model:  meta.model,
			output: heldOutput,
			total:  heldTotal,
			endMS:  endMS,
		}
	}
	estimatedShare := 0.0
	if contributedTokens > 0 {
		estimatedShare = math.Min(1, math.Max(0, estimatedTokens/contributedTokens))
	}
	outputTokens := 0.0
	totalTokens := 0.0
	ratesBySource := make(map[string]*SourceSnapshot)
	sessionRows := make([]sessionRow, 0, len(ratesByAgent))
	ensureSource := func(source string) *SourceSnapshot {
		rate := ratesBySource[source]
		if rate == nil {
			rate = &SourceSnapshot{Source: source, Label: sourceLabel(source)}
			ratesBySource[source] = rate
		}
		return rate
	}
	for agentID, rate := range ratesByAgent {
		outputTokens += rate.output
		totalTokens += rate.total
		sourceRate := ensureSource(rate.source)
		sourceRate.OutputTokensPerSecond += rate.output
		sourceRate.TotalTokensPerSecond += rate.total
		project := s.projectByAgent[agentID]
		session := SessionSnapshot{
			ID:      sessionID(agentID),
			Project: project,
			// 会话名优先用会话自身的标题；拿不到时回退到项目名、模型名，
			// 最后才是短 ID（避免面板里出现一排含义不明的哈希）。
			Label:                 s.sessionLabel(agentID, project, rate.model),
			Source:                rate.source,
			SourceLabel:           sourceLabel(rate.source),
			Model:                 rate.model,
			OutputTokensPerSecond: roundRate(rate.output),
			TotalTokensPerSecond:  roundRate(rate.total),
		}
		session.InputTokensPerSecond = roundRate(math.Max(0, session.TotalTokensPerSecond-session.OutputTokensPerSecond))
		session.Generating = generatingByAgent[agentID]
		sessionRows = append(sessionRows, sessionRow{
			view:     session,
			lastMS:   lastActiveByAgent[agentID],
			hasSpeed: session.OutputTokensPerSecond > 0 || session.Generating,
		})
	}

	out := Snapshot{
		OutputTokensPerSecond: roundRate(outputTokens),
		TotalTokensPerSecond:  roundRate(totalTokens),
		TodayTotalTokens:      s.todayTotal,
		TodayTokenDate:        s.todayDate,
		GeneratingAgents:      generatingAgents,
		UpdatedAt:             now.Format(time.RFC3339Nano),
		Sources:               make([]SourceSnapshot, 0, len(ratesBySource)),
		Sessions:              make([]SessionSnapshot, 0, len(sessionRows)),
	}
	out.InputTokensPerSecond = roundRate(math.Max(0, out.TotalTokensPerSecond-out.OutputTokensPerSecond))
	out.Active = generatingAgents > 0 || out.OutputTokensPerSecond >= 0.05
	out.TitleThemeID = NormalizeTitleTheme(s.titleTheme)
	out.Title = ThemeTitleAt(out.TitleThemeID, out.OutputTokensPerSecond, out.Active, s.redline())
	out.TitleTheme = TitleThemeLabel(out.TitleThemeID)
	out.TitleCatalog = titleCatalogFor(s.redline())

	// 今天有消耗、但此刻没有速率的来源也要有一行（否则今日总量对的、列表却是空）。
	for source, today := range s.todayBySource {
		if today.total > 0 {
			ensureSource(source)
		}
	}
	// 只保留「真正用过」的来源：今天有消耗、正在生成或还有速率。配置里存在但
	// 今天没用过的来源不再占一行，避免一上来铺满整屏的空 Agent。
	used := make(map[string]struct{}, len(ratesBySource))
	for source, rate := range ratesBySource {
		rate.OutputTokensPerSecond = roundRate(rate.OutputTokensPerSecond)
		rate.TotalTokensPerSecond = roundRate(rate.TotalTokensPerSecond)
		rate.InputTokensPerSecond = roundRate(math.Max(0, rate.TotalTokensPerSecond-rate.OutputTokensPerSecond))
		rate.GeneratingAgents = generatingBySource[source]
		today := s.todayBySource[source]
		rate.TodayInputTokens = today.input
		rate.TodayOutputTokens = today.output
		rate.TodayTotalTokens = today.total
		if rate.GeneratingAgents > 0 || rate.OutputTokensPerSecond > 0 ||
			rate.TotalTokensPerSecond > 0 || today.total > 0 {
			used[source] = struct{}{}
			out.Sources = append(out.Sources, *rate)
		}
	}
	for source := range s.availableSources {
		if _, ok := used[source]; !ok {
			out.IdleSourceCount++
		}
	}
	sort.SliceStable(out.Sources, func(i, j int) bool {
		if (out.Sources[i].GeneratingAgents > 0) != (out.Sources[j].GeneratingAgents > 0) {
			return out.Sources[i].GeneratingAgents > 0
		}
		if out.Sources[i].OutputTokensPerSecond == out.Sources[j].OutputTokensPerSecond {
			if out.Sources[i].TodayTotalTokens == out.Sources[j].TodayTotalTokens {
				return out.Sources[i].Source < out.Sources[j].Source
			}
			return out.Sources[i].TodayTotalTokens > out.Sources[j].TodayTotalTokens
		}
		return out.Sources[i].OutputTokensPerSecond > out.Sources[j].OutputTokensPerSecond
	})
	if len(out.Sources) > 0 {
		out.Agent = out.Sources[0].Label
	}

	seenSession := make(map[string]bool, len(ratesByAgent))
	for agentID := range ratesByAgent {
		seenSession[agentID] = true
	}
	for _, response := range records {
		agentID := response.agentID
		if seenSession[agentID] || nowMS-response.endMS > sessionVisible.Milliseconds() {
			continue
		}
		seenSession[agentID] = true
		project := response.project
		if project == "" {
			project = s.projectByAgent[agentID]
		}
		sessionRows = append(sessionRows, sessionRow{
			view: SessionSnapshot{
				ID:          sessionID(agentID),
				Project:     project,
				Label:       s.sessionLabel(agentID, project, response.model),
				Source:      response.source,
				SourceLabel: sourceLabel(response.source),
				Model:       response.model,
			},
			lastMS: response.endMS,
		})
	}
	// Recent idle sessions remain visible briefly, matching the requested
	// session-level view without retaining prompts or transcript paths.
	// s.files 的键是文件路径，会话身份必须用 state.agentID——否则同一个会话会
	// 以「路径哈希」和「agentID 哈希」出现两次，且名字永远退回短 ID。
	for _, state := range s.files {
		agentID := s.canonicalAgentID(state.agentID)
		if agentID == "" || seenSession[agentID] || state.lastLineMS == 0 ||
			nowMS-state.lastLineMS > sessionVisible.Milliseconds() {
			continue
		}
		seenSession[agentID] = true
		project := state.project
		if project == "" {
			project = s.projectByAgent[agentID]
		}
		sessionRows = append(sessionRows, sessionRow{
			view: SessionSnapshot{
				ID:          sessionID(agentID),
				Project:     project,
				Label:       s.sessionLabel(agentID, project, state.model),
				Source:      state.source,
				SourceLabel: sourceLabel(state.source),
				Model:       state.model,
			},
			lastMS: state.lastLineMS,
		})
	}
	// 同一项目下可能同时有多个会话，名字会完全一样（都叫「临时文件」），
	// 用短 ID 后缀区分，否则用户无法知道哪一行是哪一个。
	labelCount := make(map[string]int, len(sessionRows))
	for _, row := range sessionRows {
		labelCount[row.view.Label]++
	}
	for i := range sessionRows {
		view := &sessionRows[i].view
		if labelCount[view.Label] > 1 {
			view.Label = view.Label + " #" + view.ID[:4]
		}
	}
	sort.SliceStable(sessionRows, func(i, j int) bool {
		return lessSessionRow(sessionRows[i], sessionRows[j])
	})
	if len(sessionRows) > maxSessions {
		sessionRows = sessionRows[:maxSessions]
	}
	for _, row := range sessionRows {
		view := row.view
		if row.lastMS > 0 {
			view.LastActiveAt = time.UnixMilli(row.lastMS).Format(time.RFC3339Nano)
		}
		out.Sessions = append(out.Sessions, view)
	}
	out.TodayOutputTokens = s.todayOutput
	out.TodayInputTokens = s.todayInput
	out.TodayRequests = s.todayRequests
	if s.todayMeasuredMS > 0 {
		out.TodayAverageTokens = roundRate(
			float64(s.todayMeasured) / (float64(s.todayMeasuredMS) / 1000))
	}
	out.EstimatedShare = roundRate(estimatedShare)
	s.trackLiveEvents(&out, nowMS)
	return out
}

// heldLiveRate 计算某个 agent 在 atMS 时刻按 3 秒实时窗口取值会得到多少：
// 只统计该 agent 在 [atMS-3s, atMS] 内仍有重叠的已完成响应，口径与实时速率
// 完全一致（同样的折算函数与分母），因此从「实时」切到「保留」不会跳数。
// 没有可折算的响应时返回 false，调用方保持 0。
func heldLiveRate(measured []timedResponse, agentID string, atMS int64) (output, total float64, ok bool) {
	if atMS <= 0 {
		return 0, 0, false
	}
	fromMS := atMS - liveRateWindow.Milliseconds()
	for _, response := range measured {
		if response.agentID != agentID || response.endMS > atMS {
			continue
		}
		tokens, windowMS, overallMS := responseMeasure(response)
		if tokens <= 0 || windowMS <= 0 {
			continue
		}
		out := measuredWindowTokens(response.endMS, windowMS, tokens, fromMS, atMS)
		if out <= 0 {
			continue
		}
		output += out
		total += out
		if overallMS > 0 && response.usage.input > 0 {
			total += inputWindowTokens(response.startMS, response.endMS,
				response.usage.input, fromMS, atMS)
		}
	}
	if output <= 0 {
		return 0, 0, false
	}
	seconds := liveRateWindow.Seconds()
	return output / seconds, total / seconds, true
}

// trackLiveEvents 维护「今日峰值」与两个轻量事件序号。调用方必须已持有
// s.mu（computeSnapshot 只在持锁时被调用）：这些字段是运行期状态，随快照
// 一起发布，界面靠序号变化播放冲击波与破纪录庆祝。
func (s *Service) trackLiveEvents(out *Snapshot, nowMS int64) {
	if s.peakDate != out.TodayTokenDate {
		s.peakDate = out.TodayTokenDate
		s.peakToday = 0
		s.lastPeakEventMS = 0
	}
	// 响应完成：同一 key 只报一次，且只报两分钟内的实时完成（启动补读
	// 的历史响应不触发庆祝）。
	if s.finalSeen == nil {
		s.finalSeen = make(map[string]bool)
	}
	if len(s.finalSeen) > 8192 {
		s.finalSeen = make(map[string]bool, len(s.records))
	}
	for key, record := range s.records {
		if !record.final || record.endMS <= 0 {
			continue
		}
		if s.finalSeen[key] {
			continue
		}
		s.finalSeen[key] = true
		if nowMS-record.endMS <= responseEventWindow.Milliseconds() {
			s.responseSeq++
		}
	}
	// 今日峰值：运行期间观测到的实时速率最大值；破纪录事件按冷却与底线去抖。
	if live := out.OutputTokensPerSecond; live > s.peakToday {
		growth := live - s.peakToday
		s.peakToday = live
		if growth >= peakEventMinimumGrowth && live >= peakEventMinimum &&
			nowMS-s.lastPeakEventMS >= peakEventCooldown.Milliseconds() {
			s.peakEventSeq++
			s.lastPeakEventMS = nowMS
		}
	}
	out.PeakTodayTokensPerSecond = s.peakToday
	out.ResponseSeq = s.responseSeq
	out.PeakSeq = s.peakEventSeq
	out.Redline = s.redline()
}

func asTimed(record responseRecord) (timedResponse, bool) {
	startMS := int64(0)
	if record.hasStart && record.startMS > 0 {
		startMS = record.startMS
	} else if record.hasDecodeStart && record.decodeStartMS > 0 {
		startMS = record.decodeStartMS
	}
	duration := record.endMS - startMS
	if startMS <= 0 || duration <= 0 || duration > maxResponseAge.Milliseconds() {
		return timedResponse{}, false
	}
	return timedResponse{
		responseRecord: record,
		startMS:        startMS,
		durationMS:     duration,
	}, true
}

func buildReferences(measured []timedResponse, nowMS int64) (map[string]speedEstimate, map[string]speedEstimate) {
	recentByAgent := make(map[string]speedEstimate)
	type sums struct {
		count           int
		outputTokens    int64
		outputDuration  int64
		inputTokens     int64
		overallDuration int64
	}
	agentSums := make(map[string]sums)
	for _, response := range measured {
		if response.endMS < nowMS-recentReference.Milliseconds() {
			continue
		}
		current := agentSums[response.agentID]
		if current.count >= recentCount {
			continue
		}
		tokens, windowMS, overallMS := responseMeasure(response)
		if windowMS == 0 {
			continue
		}
		current.count++
		current.outputTokens += tokens
		current.outputDuration += windowMS
		current.inputTokens += maxInt64(0, response.usage.input)
		current.overallDuration += overallMS
		agentSums[response.agentID] = current
	}
	for agentID, current := range agentSums {
		output := float64(current.outputTokens) /
			(float64(current.outputDuration) / 1000)
		input := float64(current.inputTokens) /
			(float64(current.overallDuration) / 1000)
		recentByAgent[agentID] = speedEstimate{output: output, total: output + input}
	}

	fallbackBySource := make(map[string]speedEstimate)
	sourceSums := make(map[string]sums)
	for _, response := range measured {
		if response.endMS < nowMS-fallback.Milliseconds() {
			continue
		}
		current := sourceSums[response.source]
		if current.count >= fallbackCount {
			continue
		}
		tokens, windowMS, overallMS := responseMeasure(response)
		if windowMS == 0 {
			continue
		}
		current.count++
		current.outputTokens += tokens
		current.outputDuration += windowMS
		current.inputTokens += maxInt64(0, response.usage.input)
		current.overallDuration += overallMS
		sourceSums[response.source] = current
	}
	for source, current := range sourceSums {
		output := float64(current.outputTokens) /
			(float64(current.outputDuration) / 1000)
		input := float64(current.inputTokens) /
			(float64(current.overallDuration) / 1000)
		fallbackBySource[source] = speedEstimate{output: output, total: output + input}
	}
	return recentByAgent, fallbackBySource
}

func referenceFor(agentID, source string, recentByAgent, fallbackBySource map[string]speedEstimate) (speedEstimate, bool) {
	if estimate, ok := recentByAgent[agentID]; ok {
		return estimate, true
	}
	estimate, ok := fallbackBySource[source]
	return estimate, ok
}

func roundRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0
	}
	return math.Round(value*1000) / 1000
}
