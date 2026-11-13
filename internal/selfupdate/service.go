package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"qka/internal/applog"
	"qka/internal/atomicfile"
)

const (
	// DefaultFeedURL is updated by the release pipeline. A local override keeps
	// development and controlled acceptance tests away from the network.
	DefaultFeedURL = "https://github.com/sljdxde/quota-keepalive-app/releases/latest/download/feed.json"
	// DomesticMirror 是国内镜像前缀：打开「国内镜像源」后 feed 与安装包都走
	// 这个 GitHub 代理，版本与 SHA-256 仍按官方 feed 校验。
	DomesticMirror = "https://ghfast.top/"
	maxFeedBytes   = 1 << 20
	// releasePagePrefix 是更新说明的官方地址前缀。
	releasePagePrefix = "https://github.com/sljdxde/quota-keepalive-app/releases/tag/"
	// downloadAttempts / feedAttempts 是镜像抖动的最大尝试次数（含首次）：
	// 连接被掐断、服务端 408/429/5xx 或长时间无数据都会自动重试，避免一次
	// 抖动就让检查或下载失败。
	downloadAttempts = 3
	feedAttempts     = 3
	// downloadDialTimeout 限制建连与 TLS 握手，downloadHeaderTimeout 限制
	// 等响应头的时间（镜像可能先回源再开始转发，给足余量）。
	downloadDialTimeout   = 15 * time.Second
	downloadHeaderTimeout = 120 * time.Second
)

var (
	// feedTimeout 限制一次 feed 检查的总时长。走国内镜像时首字节可能要等
	// 十几秒；旧实现把所有请求都塞进 30s 的 Client.Timeout，慢速检查与
	// 大文件下载都会被误杀。
	feedTimeout = 60 * time.Second
	// downloadStallTimeout 是「连续多久没有收到任何数据」才判超时。下载不再
	// 设总时长上限：61-81 MB 的安装包经镜像下载常要几分钟，按总时长计时会
	// 误杀正常下载，只惩罚真正停滞的连接。
	downloadStallTimeout = 60 * time.Second
	// downloadRetryDelay 是重试前的等待基线，按尝试次数线性递增。
	downloadRetryDelay = 800 * time.Millisecond
)

// releasePageURL 返回某版本对应的 release 页面；版本为空时返回空串。
func releasePageURL(version string) string {
	value := normalizeVersion(version)
	if value == "" {
		return ""
	}
	return releasePagePrefix + "v" + value
}

var (
	// ErrNoArtifact reports a feed without a compatible release artifact.
	ErrNoArtifact = errors.New("selfupdate: no compatible release artifact")
	// ErrHashMismatch reports a downloaded artifact that failed SHA-256.
	ErrHashMismatch = errors.New("selfupdate: downloaded artifact checksum mismatch")
	// ErrNotStaged reports an install request before a verified download.
	ErrNotStaged = errors.New("selfupdate: update has not been downloaded")
	// errDownloadStalled 表示下载连续 downloadStallTimeout 没有任何数据，
	// 换一次连接可能恢复，所以归入可重试错误。
	errDownloadStalled = errors.New("selfupdate: 下载停滞超时，网络长时间无数据")
	// errUpdateTooLarge 表示安装包超过 maxUpdateBytes，重试没有意义。
	errUpdateTooLarge = errors.New("selfupdate: update artifact is too large")
)

var (
	updateProcessID = os.Getpid
	openUpdateFile  = func(path string) (io.WriteCloser, error) {
		return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	}
	absUpdatePath        = filepath.Abs
	maxUpdateBytes int64 = 512 << 20
)

// Release is the normalized update metadata used by the service.
type Release = ReleaseInfo

// HTTPDoer is the minimal HTTP client used by update checks.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Artifact is one platform release file.
type Artifact struct {
	Platform string `json:"platform,omitempty"`
	Name     string `json:"name,omitempty"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

// Status is the credential-free update projection shown by the GUI.
type Status struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	Available      bool   `json:"available"`
	Downloaded     bool   `json:"downloaded"`
	Ready          bool   `json:"ready"`
	Artifact       string `json:"artifact,omitempty"`
	// ReleaseURL 指向这次版本在 GitHub 上的 release 页（更新说明）。
	ReleaseURL string `json:"release_url,omitempty"`
	Message    string `json:"message,omitempty"`
}

type stagedRelease struct {
	Release
	Path string `json:"path"`
}

// Service coordinates a verified, user-local application update.
type Service struct {
	CurrentVersion string
	FeedURL        string
	// Mirror 是旧的环境变量镜像：只替换协议+主机，保留路径（反向代理）。
	Mirror string
	// MirrorPrefix 是前缀式镜像（如 https://ghfast.top/）：拼在完整 GitHub
	// 地址前面，同时作用于 feed 与安装包。
	MirrorPrefix   string
	StateDir       string
	Client         HTTPDoer
	Platform       string
	Executable     func() (string, error)
	StartInstaller func(context.Context, string, string) error
	// Progress 在下载过程中汇报进度（已下载字节, 总字节；总字节未知时为 0）。
	// 只用于 GUI 展示，不参与校验。
	Progress func(downloaded, total int64)
}

// New constructs a production update service.
func New(currentVersion, configDir string) *Service {
	stateDir := filepath.Join(configDir, "updates")
	service := &Service{
		CurrentVersion: currentVersion,
		FeedURL:        strings.TrimSpace(os.Getenv("QKA_UPDATE_FEED_URL")),
		Mirror:         strings.TrimSpace(os.Getenv("QKA_UPDATE_MIRROR")),
		StateDir:       stateDir,
		Client:         newHTTPClient(),
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		Executable:     os.Executable,
		StartInstaller: startInstaller,
	}
	// 启动即回收上次更新留下的安装包：安装成功后安装脚本只删除自身，
	// staged.json 与 60MB+ 的 dmg/exe 会一直留在 updates/ 里。清理是
	// best-effort，失败只记日志，绝不影响启动。
	service.CleanupStale()
	return service
}

func (s *Service) feedURL() string {
	if value := strings.TrimSpace(s.FeedURL); value != "" {
		return value
	}
	if prefix := strings.TrimSpace(s.MirrorPrefix); prefix != "" {
		if mirrored, err := prefixMirror(DefaultFeedURL, prefix); err == nil {
			return mirrored
		}
	}
	return DefaultFeedURL
}

func (s *Service) client() HTTPDoer {
	if s.Client != nil {
		return s.Client
	}
	return newHTTPClient()
}

// newHTTPClient 构造更新检查/下载共用的客户端：不设 Client.Timeout（它的口径
// 是「含响应体在内的整次请求上限」，61-81 MB 的安装包必然超过），只限制建连、
// TLS 与等响应头的时长；下载过程由停滞看门狗兜底，feed 检查由 feedTimeout 兜底。
func newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: downloadDialTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   downloadDialTimeout,
			ResponseHeaderTimeout: downloadHeaderTimeout,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

func (s *Service) platform() string {
	if value := strings.TrimSpace(s.Platform); value != "" {
		return value
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

// Check downloads the small official feed and selects this platform's update.
func (s *Service) Check(ctx context.Context) (Status, error) {
	status, _, err := s.fetchRelease(ctx)
	return status, err
}

func (s *Service) fetchRelease(ctx context.Context) (Status, Release, error) {
	status := s.status()
	// A previously verified update is already usable offline. Do not make
	// installation depend on the feed being reachable again after a restart.
	if status.Ready {
		return status, Release{}, nil
	}
	// feed 体积很小，但走镜像时首字节可能要等十几秒；用单次检查的总超时
	// 兜底，避免界面一直停在「检查中」。网络抖动由 readFeed 内部重试。
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	body, err := s.readFeed(ctx)
	if err != nil {
		return status, Release{}, err
	}
	release, err := selectRelease(body, s.platform(), s.CurrentVersion, s.Mirror)
	if err == nil && strings.TrimSpace(s.MirrorPrefix) != "" {
		if mirrored, mirrorErr := prefixMirror(release.URL, s.MirrorPrefix); mirrorErr == nil {
			release.URL = mirrored
		}
	}
	if errors.Is(err, ErrNoUpdate) {
		status.Message = "当前已是最新版本"
		return status, Release{}, nil
	}
	if err != nil {
		return status, Release{}, err
	}
	status.LatestVersion = release.Version
	status.Available = true
	status.Artifact = release.Name
	status.ReleaseURL = releasePageURL(release.Version)
	status.Message = "发现新版本，可下载安装"
	return status, release, nil
}

// readFeed 带重试地拉取 feed：镜像偶发 DNS/连接失败或 5xx 时自动再试，
// 避免一次网络抖动就让「检查更新」直接失败。
func (s *Service) readFeed(ctx context.Context) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		body, err := s.readFeedOnce(ctx)
		if err == nil {
			return body, nil
		}
		if attempt >= feedAttempts || !retryableTransfer(err) {
			return nil, err
		}
		if err := waitRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

// readFeedOnce 读取一次 feed，并在这里校验状态码与体积上限。
func (s *Service) readFeedOnce(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.feedURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isTransientStatus(resp.StatusCode) {
			return nil, transientStatusError{resource: "feed", code: resp.StatusCode}
		}
		return nil, fmt.Errorf("selfupdate: feed HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, errors.New("selfupdate: feed is too large")
	}
	return body, nil
}

// DownloadAndApply downloads and verifies the update, then starts the
// platform-specific background handoff. Current process exit remains the GUI's
// responsibility so it can finish closing windows and release the run lock.
func (s *Service) DownloadAndApply(ctx context.Context) (Status, error) {
	status := s.status()
	if status.Ready {
		return s.applyStaged(ctx, status)
	}
	status, release, err := s.fetchRelease(ctx)
	if err != nil {
		return status, err
	}
	if !status.Available {
		return status, ErrNoUpdate
	}
	path, err := s.download(ctx, release)
	if err != nil {
		return status, err
	}
	return s.applyDownloaded(ctx, status, path)
}

func (s *Service) applyStaged(ctx context.Context, status Status) (Status, error) {
	staged, ok := s.stagedRelease()
	if !ok {
		return status, ErrNotStaged
	}
	return s.applyDownloaded(ctx, status, staged.Path)
}

func (s *Service) applyDownloaded(
	ctx context.Context, status Status, path string,
) (Status, error) {
	if s.StartInstaller == nil {
		return status, errors.New("selfupdate: installer is unavailable")
	}
	target, err := s.installer()
	if err != nil {
		return status, err
	}
	if err := s.StartInstaller(ctx, target, path); err != nil {
		return status, err
	}
	status.Downloaded = true
	status.Ready = true
	status.Message = "更新已就绪；QKA 退出后会自动替换并重新启动"
	return status, nil
}

// download 下载并校验安装包。大文件不设总超时：只在「连续无数据」时判停滞；
// 对网络抖动、408/429/5xx 自动重试，避免镜像偶发超时让整个更新失败。
func (s *Service) download(ctx context.Context, release Release) (string, error) {
	if strings.TrimSpace(release.URL) == "" || strings.TrimSpace(release.SHA256) == "" {
		return "", errors.New("selfupdate: artifact URL and SHA-256 are required")
	}
	if strings.TrimSpace(s.StateDir) == "" {
		return "", errors.New("selfupdate: state directory is unavailable")
	}
	if err := atomicfile.EnsureDir(s.StateDir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(release.Name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "qka-update"
	}
	path := filepath.Join(s.StateDir, name)
	tmp := path + ".part"
	expected := strings.TrimSpace(release.SHA256)
	for attempt := 1; ; attempt++ {
		sum, err := s.downloadOnce(ctx, release.URL, tmp)
		if err == nil {
			if !strings.EqualFold(sum, expected) {
				_ = os.Remove(tmp)
				return "", ErrHashMismatch
			}
			if err := os.Rename(tmp, path); err != nil {
				_ = os.Remove(tmp)
				return "", err
			}
			meta, _ := json.Marshal(stagedRelease{Release: release, Path: path})
			if err := atomicfile.WriteFile(filepath.Join(s.StateDir, "staged.json"), meta, 0o600); err != nil {
				return "", err
			}
			return path, nil
		}
		if attempt >= downloadAttempts || !retryableTransfer(err) {
			return "", err
		}
		if err := waitRetry(ctx, attempt); err != nil {
			return "", err
		}
	}
}

// downloadOnce 是完整的一次下载尝试：写完临时文件并返回内容哈希，大小与
// SHA-256 的最终判定由调用方统一做（重试时重新完整写一遍，不做断点续传）。
func (s *Service) downloadOnce(ctx context.Context, url, tmp string) (string, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := newStallWatch(downloadStallTimeout, cancel)
	defer watch.stop()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isTransientStatus(resp.StatusCode) {
			return "", transientStatusError{resource: "download", code: resp.StatusCode}
		}
		return "", fmt.Errorf("selfupdate: download HTTP %d", resp.StatusCode)
	}
	f, err := openUpdateFile(tmp)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	var source io.Reader = io.LimitReader(&stallReader{reader: resp.Body, watch: watch}, maxUpdateBytes+1)
	if s.Progress != nil {
		source = &progressReader{reader: source, total: total, report: s.Progress}
	}
	written, copyErr := io.Copy(io.MultiWriter(f, hash), source)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		if watch.stalled.Load() {
			return "", errDownloadStalled
		}
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if written > maxUpdateBytes {
		_ = os.Remove(tmp)
		return "", errUpdateTooLarge
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// retryableTransfer 判断一次下载失败是否值得重试：网络错误、停滞超时、服务端
// 临时错误与连接被提前掐断都可重试；HTTP 4xx、写盘/重命名失败、哈希不符与
// 超大文件重试没有意义。
func retryableTransfer(err error) bool {
	if errors.Is(err, errDownloadStalled) {
		return true
	}
	var statusErr transientStatusError
	if errors.As(err, &statusErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// isTransientStatus 报告 HTTP 状态是否是「换一次连接可能成功」的临时错误。
func isTransientStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= 500
}

// waitRetry 在两次尝试之间等待；父 context 取消时立即返回，保证用户
// 关掉应用或取消操作时不会卡在重试等待里。
func waitRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(downloadRetryDelay * time.Duration(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// transientStatusError 表示服务端返回的临时错误（408/429/5xx），可以重试。
type transientStatusError struct {
	resource string
	code     int
}

func (e transientStatusError) Error() string {
	return fmt.Sprintf("selfupdate: %s HTTP %d", e.resource, e.code)
}

// stallWatch 在连续 timeout 没有任何读取进度时取消请求，避免下载无限挂起。
// 每读到数据就重置计时器，所以只惩罚真正停滞的连接。
type stallWatch struct {
	timeout time.Duration
	timer   *time.Timer
	stalled atomic.Bool
}

func newStallWatch(timeout time.Duration, cancel context.CancelFunc) *stallWatch {
	watch := &stallWatch{timeout: timeout}
	watch.timer = time.AfterFunc(timeout, func() {
		watch.stalled.Store(true)
		cancel()
	})
	return watch
}

func (w *stallWatch) touch() { w.timer.Reset(w.timeout) }
func (w *stallWatch) stop()  { w.timer.Stop() }

// stallReader 把每次成功读取的进度喂给 stallWatch。
type stallReader struct {
	reader io.Reader
	watch  *stallWatch
}

func (r *stallReader) Read(buf []byte) (int, error) {
	n, err := r.reader.Read(buf)
	if n > 0 {
		r.watch.touch()
	}
	return n, err
}

// progressReader 按约 200ms 的节流频率汇报下载进度，避免每个 32KB chunk
// 都推一次事件把前端刷爆。
type progressReader struct {
	reader io.Reader
	total  int64
	done   int64
	last   time.Time
	report func(downloaded, total int64)
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	p.done += int64(n)
	if p.report != nil && (err != nil || time.Since(p.last) >= 200*time.Millisecond) {
		p.last = time.Now()
		p.report(p.done, p.total)
	}
	return n, err
}

func (s *Service) status() Status {
	current := s.CurrentVersion
	if strings.TrimSpace(current) == "" {
		current = "dev"
	}
	status := Status{CurrentVersion: current, Message: "尚未检查更新"}
	staged, ok := s.stagedRelease()
	if !ok || !newer(staged.Version, current) {
		return status
	}
	status.LatestVersion = staged.Version
	status.Available = true
	status.Downloaded = true
	status.Ready = true
	status.Artifact = staged.Name
	status.ReleaseURL = releasePageURL(staged.Version)
	status.Message = "已验证的更新已暂存"
	return status
}

func (s *Service) stagedRelease() (stagedRelease, bool) {
	if strings.TrimSpace(s.StateDir) == "" {
		return stagedRelease{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.StateDir, "staged.json"))
	if err != nil {
		return stagedRelease{}, false
	}
	var staged stagedRelease
	if json.Unmarshal(data, &staged) != nil || staged.Path == "" || staged.Version == "" {
		return stagedRelease{}, false
	}
	if !pathWithin(s.StateDir, staged.Path) {
		return stagedRelease{}, false
	}
	info, err := os.Stat(staged.Path)
	if err != nil || info.IsDir() {
		return stagedRelease{}, false
	}
	return staged, true
}

func pathWithin(root, candidate string) bool {
	absRoot, err := absUpdatePath(root)
	if err != nil {
		return false
	}
	absCandidate, err := absUpdatePath(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absRoot, absCandidate)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// CleanupStale 回收 updates/ 目录里不再需要的更新残留，best-effort：
//   - staged.json 指向的版本不高于当前版本（更新已安装成功，或回滚/降级后
//     失效）：删除该安装包与 staged.json；
//   - 下载中断留下的 *.part：一律删除；
//   - 目录里本服务下载的旧安装包（QKA-*.dmg/.pkg/.zip/.exe）：除当前仍待
//     安装的那一份外全部删除；
//   - 其它文件（安装脚本、错误日志、非本服务命名的文件）一律不动。
//
// 正在等待安装的更新（staged 版本 > 当前版本）必须保留。任何失败只记日志，
// 不向调用方返回错误：清理只是启动辅助动作，不能因为目录只读、文件被占用
// 而影响应用启动。
func (s *Service) CleanupStale() {
	stateDir := strings.TrimSpace(s.StateDir)
	if stateDir == "" {
		return
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			applog.Debugf("selfupdate", "读取更新目录失败，跳过清理 dir=%s: %v", stateDir, err)
		}
		return
	}

	keepPath := ""
	staged, ok := s.stagedRelease()
	current := strings.TrimSpace(s.CurrentVersion)
	if current == "" {
		current = "dev"
	}
	if ok && newer(staged.Version, current) {
		keepPath = staged.Path
	} else {
		// staged 已失效或元数据损坏：先删安装包再删元数据，避免留下无人
		// 引用的孤儿包。staged.Path 已经过 pathWithin 校验，不会删到
		// StateDir 之外。
		if ok {
			s.removeUpdateFile(staged.Path)
		}
		s.removeUpdateFile(filepath.Join(stateDir, "staged.json"))
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	for _, path := range staleUpdateFiles(stateDir, names, keepPath) {
		s.removeUpdateFile(path)
	}
}

// removeUpdateFile 删除一个更新残留文件；文件已不存在视为清理完成，
// 其它失败只记 warn，留给下次启动重试。
func (s *Service) removeUpdateFile(path string) {
	if err := os.Remove(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			applog.Warnf("selfupdate", "清理更新残留失败 path=%s: %v", path, err)
		}
		return
	}
	applog.Infof("selfupdate", "已清理更新残留 file=%s", filepath.Base(path))
}

// staleUpdateFiles 是清理规则里的纯函数部分：根据 StateDir 下的普通文件名
// 计算应删除的文件路径。keepPath 是仍待安装的安装包路径（没有则为空）。
//
// 只处理两类文件：*.part（下载中断残留）与 isUpdateArtifactName 认可的
// 安装包；staged.json、安装脚本、日志及其它文件都不在此列。
func staleUpdateFiles(stateDir string, names []string, keepPath string) []string {
	keep := filepath.Clean(keepPath)
	stale := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(stateDir, name)
		if strings.HasSuffix(strings.ToLower(name), ".part") {
			stale = append(stale, path)
			continue
		}
		if !isUpdateArtifactName(name) {
			continue
		}
		if keepPath != "" && filepath.Clean(path) == keep {
			continue
		}
		stale = append(stale, path)
	}
	return stale
}

// isUpdateArtifactName 判断文件名是否本服务下载的安装包：QKA 前缀加发行
// 流水线使用的安装包扩展名（QKA-<version>-<platform>.dmg/.exe/.zip）。
// 收紧到固定命名模式，避免误删 updates/ 下其它文件（如 qka-install.sh、
// qka-install-error.log 或用户自己的 QKA.backup.zip）。
func isUpdateArtifactName(name string) bool {
	ext := filepath.Ext(name)
	switch strings.ToLower(ext) {
	case ".dmg", ".pkg", ".zip", ".exe":
	default:
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	if strings.EqualFold(stem, "QKA") {
		return true
	}
	return len(stem) > 4 && strings.EqualFold(stem[:4], "QKA-")
}

func selectRelease(data []byte, platform, current, mirror string) (Release, error) {
	var raw struct {
		Stable struct {
			Version   string     `json:"version"`
			URL       string     `json:"url"`
			Name      string     `json:"name"`
			SHA256    string     `json:"sha256"`
			Artifacts []Artifact `json:"artifacts"`
		} `json:"stable"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Release{}, err
	}
	stable := raw.Stable
	selected := Artifact{URL: stable.URL, SHA256: stable.SHA256, Name: stable.Name}
	if len(stable.Artifacts) > 0 {
		found := false
		for _, artifact := range stable.Artifacts {
			if artifact.Platform == platform {
				selected = artifact
				found = true
				break
			}
		}
		if !found {
			return Release{}, ErrNoArtifact
		}
	}
	release := Release{
		Version: stable.Version, URL: selected.URL,
		SHA256: selected.SHA256, Name: selected.Name,
	}
	if !newer(release.Version, current) {
		return Release{}, ErrNoUpdate
	}
	if mirror != "" {
		url, err := applyMirror(release.URL, mirror)
		if err != nil {
			return Release{}, err
		}
		release.URL = url
	}
	return release, nil
}
