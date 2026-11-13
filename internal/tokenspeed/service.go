package tokenspeed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"qka/internal/applog"
)

type usage struct {
	input     int64
	cached    int64
	cacheNew  int64
	output    int64
	reasoning int64
	total     int64
}

type responseRecord struct {
	key            string
	source         string
	agentID        string
	model          string
	startMS        int64
	hasStart       bool
	decodeStartMS  int64
	hasDecodeStart bool
	firstTextMS    int64
	hasFirstText   bool
	firstReasonMS  int64
	hasFirstReason bool
	flowMS         int64
	noMeasuredRate bool
	endMS          int64
	final          bool
	usage          usage
	updatedAt      time.Time
	project        string
}

type fileState struct {
	source  string
	agentID string
	offset  int64
	// size/modNS 记录上次读完时的文件大小与 mtime：下一轮发现文件没变化时
	// 直接跳过 open/seek/read（Windows 上每次 open 都要付一次杀软扫描成本）。
	size          int64
	modNS         int64
	createdMS     int64
	subagentOrd   *int64
	pendingMS     int64
	hasPending    bool
	inFlightMS    int64
	active        bool
	streaming     bool
	firstOutputMS int64
	firstTextMS   int64
	firstReasonMS int64
	lastContentMS int64
	modernUsage   bool
	lastLineMS    int64
	// jobOpen 是尚未 settled 的后台任务数（当前只有 DSH 会写这类事件）：
	// 工具执行期间没有 token，但会话仍在活动，保留速率不应过期。
	jobOpen    int
	model      string
	project    string
	prevTotals *usage
	claude     claudeFileState
	workbuddy  workbuddyFileState
	// backfill 非空时表示正在补读实时尾部之前的历史字节。首次打开超大会话
	// 文件时实时读取只保留尾部，历史补读用于还原当日 token 总量；它使用独立
	// 的 fileState，避免历史顺序打乱实时计时状态。
	backfill *backfillState
}

// backfillState 是一次历史补读的游标；offset/end 是文件字节区间 [offset,end)。
type backfillState struct {
	offset int64
	end    int64
	state  *fileState
}

func resetOutputTiming(state *fileState) {
	state.firstOutputMS = 0
	state.firstTextMS = 0
	state.firstReasonMS = 0
	state.lastContentMS = 0
}

func markOutputTiming(state *fileState, ts int64, text, reasoning bool) {
	if state.firstOutputMS == 0 {
		state.firstOutputMS = ts
	}
	if text && state.firstTextMS == 0 {
		state.firstTextMS = ts
	}
	if reasoning && state.firstReasonMS == 0 {
		state.firstReasonMS = ts
	}
	if ts > state.lastContentMS {
		state.lastContentMS = ts
	}
}

type dedupClaim int

const (
	dedupNew dedupClaim = iota
	dedupSameFile
	dedupOtherFile
)

type transcriptFile interface {
	io.Reader
	io.Seeker
	Stat() (os.FileInfo, error)
	Close() error
}

var (
	openTranscript  = func(path string) (transcriptFile, error) { return os.Open(path) }
	maxFilesPerRoot = 20000
	walkDir         = filepath.WalkDir
)

// Service incrementally reads transcript files and maintains a live speed snapshot.
type Service struct {
	cfg Config

	mu sync.RWMutex
	// snapshotMu 只保护已算好的快照。Poll 扫描会话文件要持 mu 写锁上百毫秒，
	// 若 Snapshot 也读 mu，窗口初始化/刷新的绑定调用会被扫描整段挡住。
	snapshotMu sync.RWMutex
	files      map[string]*fileState
	records    map[string]responseRecord
	deduper    map[string]string
	dedupRing  []string
	titleTheme string
	todayDate  string
	todayTotal int64
	// 今日的输入/输出/请求数与「实测输出 ÷ 实测时长」，速度页今日网格要用。
	todayOutput        int64
	todayInput         int64
	todayRequests      int64
	todayMeasured      int64 // Σ 实测输出的 token
	todayMeasuredMS    int64 // Σ 实测时长（毫秒）
	todayContributions map[string]usage
	todayBySource      map[string]usage
	availableSources   map[string]bool
	opencode           map[string]*opencodeSourceState
	// projectByAgent 记住每个 agent 的项目名，供会话行展示（不使用提示词内容）。
	projectByAgent map[string]string
	// sessionParent / sessionTitles 来自 DSH 会话自身的元数据事件：
	// parentSession 把子代理会话折叠回顶层会话，session/title 提供会话名。
	sessionParent map[string]string
	sessionTitles map[string]string
	dsh           map[string]*dshFileState
	dshDec        *dshDecoder
	snapshot      Snapshot
	// 峰值与事件序号：只在 computeSnapshot（已持 mu）里更新。
	peakToday       float64
	peakDate        string
	peakEventSeq    int64
	lastPeakEventMS int64
	responseSeq     int64
	finalSeen       map[string]bool
	// lastWalk / lastDSHWalk 记录上一次完整目录扫描的时间，用于按
	// WalkInterval 节流（普通日志源与 DSH 源各自计时）。由 Poll 在 s.mu 内读写。
	lastWalk    time.Time
	lastDSHWalk time.Time

	// slowMu 保护慢轮询环形记录；独立于 mu，避免诊断查询被扫描整段挡住。
	slowMu   sync.Mutex
	slowRing []SlowPollSample
	// slowNext 是环形写指针：slowRing 写满后从这里开始覆盖最旧样本。
	slowNext int

	started bool
	done    chan struct{}

	// livePollNanos 是「活跃时」轮询间隔的纳秒值。设置页允许在运行中调整
	// 采样频率，无需重启服务；0 表示沿用 cfg.PollInterval。用 atomic 是因为
	// 写入来自 GUI 线程、读取来自轮询 goroutine。
	livePollNanos atomic.Int64
}

// New creates a service. Call Start to begin polling.
func New(cfg Config) *Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 750 * time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	now := cfg.Now()
	todayDate := beijingDay(now)
	return &Service{
		cfg:                cfg,
		files:              make(map[string]*fileState),
		records:            make(map[string]responseRecord),
		finalSeen:          make(map[string]bool),
		deduper:            make(map[string]string),
		titleTheme:         TitleThemeVehicles,
		todayDate:          todayDate,
		todayContributions: make(map[string]usage),
		todayBySource:      make(map[string]usage),
		availableSources:   make(map[string]bool),
		projectByAgent:     make(map[string]string),
		sessionParent:      make(map[string]string),
		sessionTitles:      make(map[string]string),
		opencode:           make(map[string]*opencodeSourceState),
		dsh:                make(map[string]*dshFileState),
		snapshot: Snapshot{
			TitleTheme:     TitleThemeLabel(TitleThemeVehicles),
			TitleThemeID:   TitleThemeVehicles,
			TodayTokenDate: todayDate,
			Sources:        []SourceSnapshot{},
			Sessions:       []SessionSnapshot{},
			UpdatedAt:      now.Format(time.RFC3339Nano),
		},
	}
}

// Start polls immediately and then at the configured interval until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	go func() {
		defer close(done)
		// 首轮扫描放到后台：会话目录文件很多时，同步首跑会把首窗创建
		// 拖后数秒（用户感知为「双击后卡半天」）。
		s.poll(ctx)
		for {
			// 间隔按上一轮结果自适应：有会话在生成才用 750ms，空闲时放宽，
			// 避免在 Windows 上持续全盘扫描抢占 CPU（虚拟机里尤其明显）。
			timer := time.NewTimer(s.nextPollInterval())
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.poll(ctx)
			}
		}
	}()
}

// nextPollInterval 返回下一轮轮询的间隔：上一轮有生成中的会话就保持快速
// 刷新，否则用空闲间隔（未配置时沿用 PollInterval）。
func (s *Service) nextPollInterval() time.Duration {
	s.snapshotMu.RLock()
	active := s.snapshot.Active || s.snapshot.GeneratingAgents > 0
	s.snapshotMu.RUnlock()
	if active {
		return s.livePollInterval()
	}
	// 有后台任务在跑（工具执行）也按活跃节奏轮询：工具结束到下一步开始之间
	// 会写 step/end + step/start，2 秒的空闲间隔会让「生成中」和估算值晚
	// 一个空闲周期才出现，手感上就是「速度变化很慢」。
	s.mu.RLock()
	for _, state := range s.files {
		if state != nil && state.jobOpen > 0 {
			active = true
			break
		}
	}
	s.mu.RUnlock()
	if active {
		return s.livePollInterval()
	}
	if s.cfg.IdlePollInterval > 0 {
		return s.cfg.IdlePollInterval
	}
	return s.livePollInterval()
}

// livePollInterval 返回当前生效的活跃轮询间隔：设置页改过就用新值，
// 否则用启动时的配置。
func (s *Service) livePollInterval() time.Duration {
	if nanos := s.livePollNanos.Load(); nanos > 0 {
		return time.Duration(nanos)
	}
	return s.cfg.PollInterval
}

// SetPollInterval 在运行中调整活跃轮询间隔，下一次轮询立即按新值调度。
// 非正值表示忽略（保留当前值），避免设置项被清空时把轮询打成忙等。
func (s *Service) SetPollInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.livePollNanos.Store(int64(interval))
}

// PollInterval 返回当前生效的活跃轮询间隔，供设置页回显与诊断使用。
func (s *Service) PollInterval() time.Duration {
	return s.livePollInterval()
}

// Wait blocks until the polling goroutine exits. It is safe to call after Start.
func (s *Service) Wait() {
	s.mu.RLock()
	done := s.done
	started := s.started
	s.mu.RUnlock()
	if started && done != nil {
		<-done
	}
}

// WaitWithin 最多等 d：退出流程用它兜底，避免网络盘等异常文件系统上的
// 单轮扫描把进程退出拖到用户以为「关不掉」。
func (s *Service) WaitWithin(d time.Duration) {
	s.mu.RLock()
	done := s.done
	started := s.started
	s.mu.RUnlock()
	if !started || done == nil {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// Snapshot returns a copy of the latest credential-free view.
func (s *Service) Snapshot() Snapshot {
	s.snapshotMu.RLock()
	snap := s.snapshot
	s.snapshotMu.RUnlock()
	out := snap
	out.Sources = append([]SourceSnapshot(nil), snap.Sources...)
	out.Sessions = append([]SessionSnapshot(nil), snap.Sessions...)
	return out
}

// storeSnapshot publishes a computed snapshot without holding the scan lock.
func (s *Service) storeSnapshot(snap Snapshot) {
	s.snapshotMu.Lock()
	s.snapshot = snap
	s.snapshotMu.Unlock()
}

// redline 返回当前红线；未配置时用默认值。调用方持 mu。
func (s *Service) redline() float64 {
	if s.cfg.Redline > 0 {
		return s.cfg.Redline
	}
	return DefaultRedline
}

// SetRedline 更新红线速率并立即重算快照（称号阈值随它缩放）。
func (s *Service) SetRedline(value float64) {
	if value <= 0 {
		value = DefaultRedline
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Redline = value
	s.storeSnapshot(s.computeSnapshot(s.cfg.Now()))
}

// SetTitleTheme switches the built-in title theme and refreshes the derived snapshot.
func (s *Service) SetTitleTheme(theme string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titleTheme = NormalizeTitleTheme(theme)
	s.storeSnapshot(s.computeSnapshot(s.cfg.Now()))
}

// slowPollThreshold 是单次轮询的告警阈值：超过它说明扫描本机日志偏慢，
// 每 750ms 一次的界面刷新可能被拖慢，写一条 perf 告警便于事后排查。
const slowPollThreshold = 150 * time.Millisecond

// slowPollCapacity 是慢轮询环形记录的容量：只保留最近少数几次，供设置页
// 诊断区块展示；记录只有耗时毫秒数与时间戳，不含任何会话内容。
const slowPollCapacity = 10

// SlowPollSample 是一次慢轮询的耗时记录。红线：不落提示词、token 或账号，
// 只有性能数字与时间。
type SlowPollSample struct {
	// ElapsedMS 是该次轮询的实测耗时（毫秒）。
	ElapsedMS int64 `json:"elapsed_ms"`
	// At 是该次轮询结束时刻（服务时钟，RFC3339）。
	At string `json:"at"`
}

// SlowPollThresholdMS 返回慢轮询告警阈值（毫秒），诊断页展示「耗时+阈值」用。
func SlowPollThresholdMS() int64 {
	return slowPollThreshold.Milliseconds()
}

// slowPollWarning 返回需要记录的性能告警；未超过阈值时 ok 为 false。
func slowPollWarning(elapsed time.Duration) (message string, ok bool) {
	if elapsed <= slowPollThreshold {
		return "", false
	}
	return fmt.Sprintf("单次轮询耗时 %s（阈值 %s），界面刷新可能被拖慢",
		elapsed.Round(time.Millisecond), slowPollThreshold), true
}

// recordSlowPoll 把一次慢轮询写入环形记录；容量小、加锁独立，不会拖慢轮询。
func (s *Service) recordSlowPoll(elapsed time.Duration, at time.Time) {
	sample := SlowPollSample{
		ElapsedMS: elapsed.Milliseconds(),
		At:        at.Format(time.RFC3339),
	}
	s.slowMu.Lock()
	defer s.slowMu.Unlock()
	if len(s.slowRing) < slowPollCapacity {
		s.slowRing = append(s.slowRing, sample)
		return
	}
	s.slowRing[s.slowNext] = sample
	s.slowNext = (s.slowNext + 1) % slowPollCapacity
}

// RecentSlowPolls 返回最近记录的慢轮询（旧→新）拷贝；未记录过时为空切片。
func (s *Service) RecentSlowPolls() []SlowPollSample {
	s.slowMu.Lock()
	defer s.slowMu.Unlock()
	if len(s.slowRing) < slowPollCapacity || s.slowNext == 0 {
		return append([]SlowPollSample(nil), s.slowRing...)
	}
	// 环形已绕回：写指针处是最旧样本，从它开始按时间顺序拼接。
	out := make([]SlowPollSample, 0, len(s.slowRing))
	out = append(out, s.slowRing[s.slowNext:]...)
	out = append(out, s.slowRing[:s.slowNext]...)
	return out
}

// Poll performs one discovery, incremental read, and recomputation pass.
func (s *Service) Poll() {
	s.poll(context.Background())
}

// poll 是 Poll 的可取消版本：退出时取消 ctx，扫描在下一个检查点放弃，
// 不再白等一轮完整目录遍历。
func (s *Service) poll(ctx context.Context) {
	started := time.Now()
	s.pollLocked(ctx)
	elapsed := time.Since(started)
	if message, ok := slowPollWarning(elapsed); ok {
		applog.Warnf("perf", "tokenspeed: %s", message)
		// 日志之外再留一份进程内环形记录：设置页诊断区块可以直接展示
		// 最近几次慢轮询，用户反馈「卡」时不用翻日志文件。
		s.recordSlowPoll(elapsed, s.cfg.Now())
	}
}

// walkDue 判断本轮是否需要重扫目录树；需要时顺带记下这次扫描时间。
// WalkInterval 为 0 时每轮都扫（测试默认行为，新建文件立刻可见）。
func (s *Service) walkDue(now time.Time) bool {
	if s.cfg.WalkInterval <= 0 {
		return true
	}
	if s.lastWalk.IsZero() || now.Sub(s.lastWalk) >= s.cfg.WalkInterval {
		s.lastWalk = now
		return true
	}
	return false
}

// dshWalkDue 与 walkDue 同理，但单独计时：普通日志源和 DSH 源的扫描互不影响。
func (s *Service) dshWalkDue(now time.Time) bool {
	if s.cfg.WalkInterval <= 0 {
		return true
	}
	if s.lastDSHWalk.IsZero() || now.Sub(s.lastDSHWalk) >= s.cfg.WalkInterval {
		s.lastDSHWalk = now
		return true
	}
	return false
}

// pollLocked 是 Poll 的加锁主体：耗时的统计放在锁外记录，避免日志写盘时
// 继续持有服务锁。
func (s *Service) pollLocked(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Now()
	s.ensureToday(now)
	s.discoverAndRead(ctx, now)
	s.readBackfills(ctx)
	s.readOpenCodeSources(ctx, now)
	s.readDSHSources(ctx, now)
	if ctx.Err() != nil {
		// 已取消：不发布半成品快照，进程正在退出。
		return
	}
	s.prune(now)
	s.storeSnapshot(s.computeSnapshot(now))
}

func (s *Service) discoverAndRead(ctx context.Context, now time.Time) {
	seen := make(map[string]struct{})
	s.availableSources = make(map[string]bool)
	dayStart := beijingDayStart(now)
	// 目录树重扫是轮询里最贵的一步（Windows 上尤其明显），按 WalkInterval
	// 节流；两次重扫之间只对已发现的文件做增量读取，新文件最多延迟一个
	// WalkInterval 出现。
	walk := s.walkDue(now)
	for _, root := range s.cfg.Roots {
		if root.Source == "opencode" || root.Source == "dsh" {
			continue
		}
		info, err := os.Stat(root.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		s.availableSources[root.Source] = true
		if !walk {
			// 不重扫：已知文件照常增量读，seen 不参与裁剪判断（见下方 prune）。
			for path, state := range s.files {
				if ctx.Err() != nil {
					return
				}
				if state == nil || state.source != root.Source {
					continue
				}
				seen[path] = struct{}{}
				// 快路径与重扫分支一致：先 stat 比较上次读完时记下的
				// size/mtime，没变化就不 open。否则空闲时每轮仍会对每个
				// 已知会话文件付一次 open（Windows 上还有杀软扫描）。
				if info, err := os.Stat(path); err == nil &&
					state.size == info.Size() && state.modNS == info.ModTime().UnixNano() {
					continue
				}
				s.readFile(path, root.Source, now)
			}
			continue
		}
		count := 0
		_ = walkDir(root.Path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if count >= maxFilesPerRoot {
				return filepath.SkipAll
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			count++
			stat, err := entry.Info()
			if err != nil || stat.ModTime().Before(dayStart) {
				return nil
			}
			seen[path] = struct{}{}
			// 快路径：大小与 mtime 都没变说明没有新增内容，跳过 open——
			// 每轮给每个会话文件付一次 Windows CreateFile + 杀软扫描不值得。
			if state := s.files[path]; state != nil &&
				state.size == stat.Size() && state.modNS == stat.ModTime().UnixNano() {
				return nil
			}
			s.readFile(path, root.Source, now)
			return nil
		})
	}
	for path, state := range s.files {
		// DSH 的会话文件（.jsonl.zstd）由 readDSHSources 单独扫描，不会出现在
		// 这一轮的 seen 里；这里若按「没扫到」删掉，DSH 的实时状态就永远从
		// s.files 消失（active/lastLineMS 全丢），速率也会退回只能在响应结束
		// 瞬间亮一下。保留 DSH 条目，让后面的 readDSHSources 继续增量更新。
		if state != nil && state.source == "dsh" {
			continue
		}
		if _, ok := seen[path]; !ok {
			delete(s.files, path)
		}
	}
}

func (s *Service) readFile(path, source string, now time.Time) {
	file, err := openTranscript(path)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	state := s.files[path]
	fresh := state == nil
	if fresh || info.Size() < state.offset {
		state = &fileState{source: source, agentID: path,
			project: projectName(source, path)}
		s.files[path] = state
		fresh = true
	}
	// 无论下面是否真正读到数据，都记下这次看到的大小/mtime：下轮快路径
	// 依据它们跳过没有变化的文件。
	defer func() {
		state.size = info.Size()
		state.modNS = info.ModTime().UnixNano()
	}()
	if state.project != "" {
		s.projectByAgent[state.agentID] = state.project
	}

	offset := state.offset
	if offset == 0 && info.Size() > maxInitialTail {
		offset = alignToNextLine(file, info.Size()-maxInitialTail)
		state.offset = offset
	}
	if fresh && offset > 0 {
		// 首次打开超大会话文件时只读尾部，历史区间通过 readBackfills 分片
		// 补读，避免启动时解析数百 MB 会话阻塞实时速率；当日总量在补读
		// 完成后收敛。
		state.backfill = &backfillState{
			end:   offset,
			state: &fileState{source: source, agentID: path},
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return
	}
	remaining := info.Size() - offset
	if remaining <= 0 {
		return
	}
	if remaining > maxReadChunk {
		remaining = maxReadChunk
	}
	data := make([]byte, remaining)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		return
	}
	data = data[:n]
	consumed, skippedOverlong := processLines(data, func(line []byte) {
		s.ingest(source, path, state, line)
	})
	state.offset += int64(consumed)
	if skippedOverlong > 0 {
		state.offset += int64(skippedOverlong)
	}
	_ = now
}

func alignToNextLine(file io.ReadSeeker, offset int64) int64 {
	if offset <= 0 {
		return 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			if index := strings.IndexByte(string(buf[:n]), '\n'); index >= 0 {
				return offset + int64(index+1)
			}
			offset += int64(n)
		}
		if err != nil {
			return offset
		}
	}
}

// processLines returns complete bytes consumed and the bytes skipped for one
// overlong line that exceeded the read chunk without a newline.
func processLines(data []byte, consume func([]byte)) (int, int) {
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				consume(line)
			}
			start = i + 1
		}
	}
	if start == 0 && len(data) == maxReadChunk {
		return 0, len(data)
	}
	return start, 0
}

// readBackfills advances bounded historical reads for files whose live offset
// starts past the beginning of the file. Polling keeps the total work per pass
// bounded so the UI snapshot never waits on a whole-file parse.
func (s *Service) readBackfills(ctx context.Context) {
	budget := backfillBudget
	for path, state := range s.files {
		if state.backfill == nil {
			continue
		}
		if budget <= 0 || ctx.Err() != nil {
			return
		}
		budget -= s.readBackfill(path, state, budget)
	}
}

// readBackfill reads at most one bounded chunk of the not-yet-read history and
// feeds complete lines through the normal per-source parser with a dedicated
// fileState. It returns the bytes consumed from the backfill budget.
func (s *Service) readBackfill(path string, state *fileState, budget int64) int64 {
	backfill := state.backfill
	if backfill == nil {
		return 0
	}
	remaining := backfill.end - backfill.offset
	if remaining <= 0 {
		state.backfill = nil
		return 0
	}
	limit := backfillChunk
	if limit > remaining {
		limit = remaining
	}
	if limit > budget {
		limit = budget
	}
	if limit <= 0 {
		return 0
	}

	file, err := openTranscript(path)
	if err != nil {
		state.backfill = nil
		return 0
	}
	defer file.Close()
	if _, err := file.Seek(backfill.offset, io.SeekStart); err != nil {
		state.backfill = nil
		return 0
	}
	data := make([]byte, limit)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		state.backfill = nil
		return 0
	}
	data = data[:n]

	consumed := int64(0)
	if index := bytes.LastIndexByte(data, '\n'); index >= 0 {
		consumed = int64(index + 1)
		processLines(data[:consumed], func(line []byte) {
			s.ingest(state.source, path, backfill.state, line)
		})
	} else {
		// 整块都没有换行：按超长行跳过，保证补读一定前进。
		consumed = int64(n)
	}
	backfill.offset += consumed
	if backfill.offset >= backfill.end {
		state.backfill = nil
	}
	return consumed
}

func (s *Service) claim(key, owner string) dedupClaim {
	existing, ok := s.deduper[key]
	if ok {
		if existing == owner {
			return dedupSameFile
		}
		return dedupOtherFile
	}
	s.deduper[key] = owner
	s.dedupRing = append(s.dedupRing, key)
	if len(s.dedupRing) > deduperSize {
		evict := len(s.dedupRing) - deduperSize
		for _, old := range s.dedupRing[:evict] {
			delete(s.deduper, old)
		}
		s.dedupRing = append([]string(nil), s.dedupRing[evict:]...)
	}
	return dedupNew
}

func (s *Service) upsertRecord(record responseRecord) {
	if record.endMS <= 0 {
		return
	}
	if record.project == "" {
		record.project = s.projectByAgent[record.agentID]
	}
	s.records[record.key] = record
	s.trackTodayUsage(record)
}

func (s *Service) ensureToday(now time.Time) {
	date := beijingDay(now)
	if date == s.todayDate {
		return
	}
	s.todayDate = date
	s.todayTotal = 0
	s.todayOutput = 0
	s.todayInput = 0
	s.todayRequests = 0
	s.todayMeasured = 0
	s.todayMeasuredMS = 0
	s.todayContributions = make(map[string]usage)
	s.todayBySource = make(map[string]usage)
}

func (s *Service) trackTodayUsage(record responseRecord) {
	if beijingDay(time.UnixMilli(record.endMS)) != s.todayDate {
		return
	}
	contribution := nonNegativeUsage(record.usage)
	previous := s.todayContributions[record.key]
	if contribution == previous {
		return
	}
	s.todayContributions[record.key] = contribution
	delta := subtractNonNegative(contribution, previous)
	// 请求数只在首次见到这条响应时 +1（后续流式更新不算新请求）。
	if previous == (usage{}) && contribution != (usage{}) {
		s.todayRequests++
		// 实测速率：完成且有有效区间时计入「今日平均速度」。
		if record.final {
			if timed, ok := asTimed(record); ok {
				if tokens, windowMS, _ := responseMeasure(timed); tokens > 0 && windowMS > 0 {
					s.todayMeasured += tokens
					s.todayMeasuredMS += windowMS
				}
			}
		}
	}
	s.todayTotal += delta.total
	s.todayOutput += delta.output
	s.todayInput += delta.input
	source := record.source
	if source == "" {
		source = "unknown"
	}
	total := s.todayBySource[source]
	total.input += delta.input
	total.output += delta.output
	total.total += delta.total
	s.todayBySource[source] = total
}

func nonNegativeUsage(value usage) usage {
	return usage{
		input:     maxInt64(0, value.input),
		cached:    maxInt64(0, value.cached),
		cacheNew:  maxInt64(0, value.cacheNew),
		output:    maxInt64(0, value.output),
		reasoning: maxInt64(0, value.reasoning),
		total:     maxInt64(0, value.total),
	}
}

func subtractNonNegative(current, previous usage) usage {
	return usage{
		input:     maxInt64(0, current.input-previous.input),
		cached:    maxInt64(0, current.cached-previous.cached),
		cacheNew:  maxInt64(0, current.cacheNew-previous.cacheNew),
		output:    maxInt64(0, current.output-previous.output),
		reasoning: maxInt64(0, current.reasoning-previous.reasoning),
		total:     maxInt64(0, current.total-previous.total),
	}
}

var beijingZone = time.FixedZone("Asia/Shanghai", 8*60*60)

func beijingDay(now time.Time) string {
	return now.In(beijingZone).Format("2006-01-02")
}

func beijingDayStart(now time.Time) time.Time {
	local := now.In(beijingZone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, beijingZone)
}

func (s *Service) prune(now time.Time) {
	cutoff := now.Add(-maxRecordAge).UnixMilli()
	for key, record := range s.records {
		if record.endMS < cutoff {
			delete(s.records, key)
		}
	}
}

func sortedRecent(records []responseRecord) []responseRecord {
	sort.Slice(records, func(i, j int) bool {
		return records[i].endMS > records[j].endMS
	})
	return records
}
