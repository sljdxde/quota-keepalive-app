package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	return &Service{
		CurrentVersion: currentVersion,
		FeedURL:        strings.TrimSpace(os.Getenv("QKA_UPDATE_FEED_URL")),
		Mirror:         strings.TrimSpace(os.Getenv("QKA_UPDATE_MIRROR")),
		StateDir:       stateDir,
		Client:         &http.Client{Timeout: 30 * time.Second},
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		Executable:     os.Executable,
		StartInstaller: startInstaller,
	}
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
	return &http.Client{Timeout: 30 * time.Second}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.feedURL(), nil)
	if err != nil {
		return status, Release{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return status, Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return status, Release{}, fmt.Errorf("selfupdate: feed HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return status, Release{}, err
	}
	if len(body) > maxFeedBytes {
		return status, Release{}, errors.New("selfupdate: feed is too large")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
	source := io.LimitReader(resp.Body, maxUpdateBytes+1)
	if s.Progress != nil {
		source = &progressReader{reader: source, total: total, report: s.Progress}
	}
	written, copyErr := io.Copy(io.MultiWriter(f, hash), source)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if written > maxUpdateBytes {
		_ = os.Remove(tmp)
		return "", errors.New("selfupdate: update artifact is too large")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(release.SHA256)) {
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
