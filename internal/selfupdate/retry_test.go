package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// 生产环境的重试等待是 800ms 起步；单测里没有意义，统一缩短，
	// 避免失败用例因为多次重试把整个包拖慢。
	downloadRetryDelay = 5 * time.Millisecond
	os.Exit(m.Run())
}

func TestRetryableDownloadClassification(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"stalled", errDownloadStalled, true},
		{"transient status", transientStatusError{resource: "download", code: http.StatusServiceUnavailable}, true},
		{"network", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"plain read error", errors.New("read failed"), false},
		{"hash mismatch", ErrHashMismatch, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableTransfer(tt.err); got != tt.want {
				t.Fatalf("retryableTransfer(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsTransientStatus(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   bool
	}{
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusNotFound, false},
		{http.StatusOK, false},
	} {
		if got := isTransientStatus(tt.status); got != tt.want {
			t.Fatalf("isTransientStatus(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// newDownloadService 构造一个只走假 HTTP 客户端的下载服务。
func newDownloadService(t *testing.T, client HTTPDoer) *Service {
	t.Helper()
	return &Service{StateDir: t.TempDir(), Client: client}
}

func downloadRelease(artifact []byte) Release {
	return Release{
		Version: "2.0.0", URL: "https://download.example/QKA.dmg",
		Name: "QKA.dmg", SHA256: digest(artifact),
	}
}

func TestDownloadRetriesTransientServerError(t *testing.T) {
	artifact := []byte("signed application")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return response(http.StatusServiceUnavailable, strings.NewReader("")), nil
		}
		return response(http.StatusOK, bytes.NewReader(artifact)), nil
	}))

	path, err := svc.download(context.Background(), downloadRelease(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("瞬时 5xx 应重试一次，实际请求 %d 次", calls)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, artifact) {
		t.Fatalf("下载内容 = %q, err = %v", got, err)
	}
}

func TestDownloadRetriesNetworkError(t *testing.T) {
	artifact := []byte("artifact")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
		}
		return response(http.StatusOK, bytes.NewReader(artifact)), nil
	}))

	if _, err := svc.download(context.Background(), downloadRelease(artifact)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("网络错误应重试一次，实际请求 %d 次", calls)
	}
}

func TestDownloadGivesUpAfterAttempts(t *testing.T) {
	artifact := []byte("artifact")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return response(http.StatusBadGateway, strings.NewReader("")), nil
	}))

	_, err := svc.download(context.Background(), downloadRelease(artifact))
	if err == nil {
		t.Fatal("持续 502 应返回错误")
	}
	var statusErr transientStatusError
	if !errors.As(err, &statusErr) || statusErr.code != http.StatusBadGateway {
		t.Fatalf("应返回可重试的状态错误，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("错误文案应带状态码，实际 %q", err.Error())
	}
	if calls != int32(downloadAttempts) {
		t.Fatalf("应尝试 %d 次，实际 %d 次", downloadAttempts, calls)
	}
}

func TestDownloadDoesNotRetryPermanentFailure(t *testing.T) {
	artifact := []byte("artifact")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return response(http.StatusNotFound, strings.NewReader("")), nil
	}))

	if _, err := svc.download(context.Background(), downloadRelease(artifact)); err == nil {
		t.Fatal("404 应返回错误")
	}
	if calls != 1 {
		t.Fatalf("404 不应重试，实际请求 %d 次", calls)
	}
}

// blockingBody 模拟「响应头已到、但身体一直不来数据」的停滞连接：
// 只有请求 context 被取消（停滞看门狗触发）时才返回。
type blockingBody struct{ ctx context.Context }

func (b blockingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func TestDownloadRetriesStalledConnection(t *testing.T) {
	old := downloadStallTimeout
	downloadStallTimeout = 30 * time.Millisecond
	t.Cleanup(func() { downloadStallTimeout = old })

	artifact := []byte("artifact")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(req *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return response(http.StatusOK, blockingBody{ctx: req.Context()}), nil
		}
		return response(http.StatusOK, bytes.NewReader(artifact)), nil
	}))

	if _, err := svc.download(context.Background(), downloadRelease(artifact)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("停滞超时后应重试一次，实际请求 %d 次", calls)
	}
}

func TestDownloadReportsStallWhenAllAttemptsStall(t *testing.T) {
	old := downloadStallTimeout
	downloadStallTimeout = 20 * time.Millisecond
	t.Cleanup(func() { downloadStallTimeout = old })

	artifact := []byte("artifact")
	var calls int32
	svc := newDownloadService(t, doerFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return response(http.StatusOK, blockingBody{ctx: req.Context()}), nil
	}))

	_, err := svc.download(context.Background(), downloadRelease(artifact))
	if !errors.Is(err, errDownloadStalled) {
		t.Fatalf("应返回停滞超时错误，实际 %v", err)
	}
	if calls != int32(downloadAttempts) {
		t.Fatalf("应尝试 %d 次，实际 %d 次", downloadAttempts, calls)
	}
}

func TestDownloadStopsWhenContextCanceledDuringRetry(t *testing.T) {
	artifact := []byte("artifact")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int32
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		cancel() // 首次请求就取消：不进入下一次尝试
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	}))

	if _, err := svc.download(ctx, downloadRelease(artifact)); !errors.Is(err, context.Canceled) {
		t.Fatalf("重试前 context 已取消应返回 context.Canceled，实际 %v", err)
	}
	if calls != 1 {
		t.Fatalf("取消后不应再请求，实际 %d 次", calls)
	}
}

func TestDownloadUnknownContentLengthReportsZeroTotal(t *testing.T) {
	artifact := []byte("artifact")
	var lastTotal int64 = -1
	svc := newDownloadService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		resp := response(http.StatusOK, bytes.NewReader(artifact))
		// 镜像走 chunked 时没有 Content-Length（net/http 用 -1 表示未知）。
		resp.ContentLength = -1
		return resp, nil
	}))
	svc.Progress = func(downloaded, total int64) {
		lastTotal = total
		if downloaded != int64(len(artifact)) {
			t.Errorf("进度字节 = %d，want %d", downloaded, len(artifact))
		}
	}
	if _, err := svc.download(context.Background(), downloadRelease(artifact)); err != nil {
		t.Fatal(err)
	}
	if lastTotal != 0 {
		t.Fatalf("未知总大小时应以 0 上报，实际 %d", lastTotal)
	}
}

func TestFeedCheckTimesOut(t *testing.T) {
	old := feedTimeout
	feedTimeout = 25 * time.Millisecond
	t.Cleanup(func() { feedTimeout = old })

	svc := &Service{
		CurrentVersion: "1.0.0", FeedURL: "https://updates.example/feed",
		StateDir: t.TempDir(),
		Client: doerFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}
	if _, err := svc.Check(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("feed 检查应被 feedTimeout 截止，实际 %v", err)
	}
}

func TestFeedCheckRetriesTransientFailure(t *testing.T) {
	feed := releaseFeed("2.0.0", "test/os", "QKA", "https://download.example/QKA", "hash", true)
	var calls int32
	svc := &Service{
		CurrentVersion: "1.0.0", FeedURL: "https://updates.example/feed",
		StateDir: t.TempDir(), Platform: "test/os",
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			if atomic.AddInt32(&calls, 1) == 1 {
				return response(http.StatusServiceUnavailable, strings.NewReader("")), nil
			}
			return response(http.StatusOK, bytes.NewReader(feed)), nil
		}),
	}
	status, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.LatestVersion != "2.0.0" {
		t.Fatalf("重试后应发现新版本，实际 %+v", status)
	}
	if calls != 2 {
		t.Fatalf("feed 瞬时 5xx 应重试一次，实际请求 %d 次", calls)
	}
}

func TestFeedCheckDoesNotRetryPermanentFailure(t *testing.T) {
	var calls int32
	svc := &Service{
		CurrentVersion: "1.0.0", FeedURL: "https://updates.example/feed",
		StateDir: t.TempDir(),
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return response(http.StatusNotFound, strings.NewReader("")), nil
		}),
	}
	if _, err := svc.Check(context.Background()); err == nil {
		t.Fatal("404 应返回错误")
	}
	if calls != 1 {
		t.Fatalf("feed 的 404 不应重试，实际请求 %d 次", calls)
	}
}
