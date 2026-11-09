// Package freetokens 接入 AIHOT 的免费额度（Token 羊毛）数据：
// https://aihot.news/free-tokens.data
//
// 设计边界（参考 free-token-monitor 的 magpie 页面插件）：
//   - 只展示厂商/平台自己的官方额度，不展示第三方中转站；
//   - 数据在打开页面时按需拉一次，不在后台轮询，不落盘、不镜像；
//   - 只读公开数据，不读取任何密钥；对 AIHOT 使用诚实 UA。
package freetokens

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DataURL 是 AIHOT 公开的免费额度数据地址（turbo-stream flatten 编码）。
const DataURL = "https://aihot.news/free-tokens.data"

// userAgent 使用诚实 UA，不伪装成浏览器（与参考实现一致）。
const userAgent = "QuotaKeepalive/0.1 (+local desktop app; free-tokens)"

// defaultTTL 是内存缓存时长：AIHOT 一天更新数次，10 分钟内复用即可，
// 避免用户来回切页反复打上游。
const defaultTTL = 10 * time.Minute

// Model 是 deal 关联的模型条目。
type Model struct {
	Slug  string  `json:"slug"`
	Name  string  `json:"name"`
	Score float64 `json:"score,omitempty"`
}

// Deal 是一条免费额度（羊毛）记录，字段与 AIHOT 数据对齐。
type Deal struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Provider    string   `json:"provider"`
	Detail      string   `json:"detail"`
	URL         string   `json:"url"`
	Evidence    string   `json:"evidence,omitempty"`
	AppliesTo   string   `json:"appliesTo,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	EndsAt      *string  `json:"endsAt,omitempty"`
	Validity    string   `json:"validity,omitempty"`
	VerifiedAt  string   `json:"verifiedAt,omitempty"`
	Yuan        *float64 `json:"yuan,omitempty"`
	Label       string   `json:"label,omitempty"`
	Face        string   `json:"face,omitempty"`
	Counted     bool     `json:"counted,omitempty"`
	ClientOnly  bool     `json:"clientOnly,omitempty"`
	StudentOnly bool     `json:"studentOnly,omitempty"`
	RealName    bool     `json:"realName,omitempty"`
	AutoRenews  bool     `json:"autoRenews,omitempty"`
	Models      []Model  `json:"models,omitempty"`
}

// Basis 是 AIHOT 给出的折算基准（汇率是活值，不可硬编码）。
type Basis struct {
	Mix    string  `json:"mix"`
	Ratio  string  `json:"ratio"`
	USDCNY float64 `json:"usdCny"`
}

// Snapshot 是一次完整的 AIHOT 快照。
type Snapshot struct {
	Deals     []Deal    `json:"deals"`
	Ended     []Deal    `json:"ended,omitempty"`
	Basis     Basis     `json:"basis"`
	ServerNow string    `json:"serverNow"`
	ExpiresAt string    `json:"expiresAt"`
	FetchedAt time.Time `json:"-"`
}

// Fetch 拉取并解码 AIHOT 数据。client 为空时用带 15s 超时的默认 client。
func Fetch(ctx context.Context, client *http.Client, url string) (Snapshot, error) {
	if url == "" {
		url = DataURL
	}
	if client == nil {
		client = NewHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snapshot{}, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "text/plain")
	response, err := client.Do(request)
	if err != nil {
		return Snapshot{}, fmt.Errorf("拉取 AIHOT 免费额度失败: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("AIHOT %d %s", response.StatusCode, http.StatusText(response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return Snapshot{}, fmt.Errorf("读取 AIHOT 数据失败: %w", err)
	}
	snapshot, err := decodeSnapshot(body)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.FetchedAt = time.Now()
	return snapshot, nil
}

// NewHTTPClient 返回带超时的 client；Go 的 Transport 会自动协商并解压 gzip，
// 因此这里不手写 Accept-Encoding（手写会拿到 br 而无法解压）。
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// Service 是带内存缓存与并发保护的 AIHOT 拉取服务。
type Service struct {
	URL    string
	TTL    time.Duration
	Client *http.Client

	mu       sync.Mutex
	snapshot Snapshot
	fetched  time.Time
	lastErr  error
}

// NewService 返回使用默认地址与缓存的 service。
func NewService() *Service {
	return &Service{URL: DataURL, TTL: defaultTTL, Client: NewHTTPClient()}
}

// Get 返回缓存快照；缓存过期或不存在时拉取一次。拉取失败时返回上次的
// 快照（若存在）并附带错误，让 UI 能显示「旧数据 + 错误提示」。
func (s *Service) Get(ctx context.Context) (Snapshot, bool, error) {
	s.mu.Lock()
	cached := s.snapshot
	age := time.Since(s.fetched)
	s.mu.Unlock()
	ttl := s.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if !cached.FetchedAt.IsZero() && age < ttl {
		return cached, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := Fetch(ctx, s.Client, s.URL)
	if err != nil {
		s.lastErr = err
		if !s.snapshot.FetchedAt.IsZero() {
			return s.snapshot, true, err
		}
		return Snapshot{}, false, err
	}
	s.snapshot = snapshot
	s.fetched = time.Now()
	s.lastErr = nil
	return snapshot, false, nil
}

// Refresh 强制重新拉取（用于页面的「刷新」按钮）。
func (s *Service) Refresh(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := Fetch(ctx, s.Client, s.URL)
	if err != nil {
		s.lastErr = err
		return s.snapshot, err
	}
	s.snapshot = snapshot
	s.fetched = time.Now()
	s.lastErr = nil
	return snapshot, nil
}

// ErrUnavailable 是没有任何可用快照时的稳定错误。
var ErrUnavailable = errors.New("free-tokens: 数据暂不可用")
