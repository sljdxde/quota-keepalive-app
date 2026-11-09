package freetokens

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serverPayload(t *testing.T) []byte {
	t.Helper()
	return flattenJSON(t, validFlattenTable())
}

func TestFetchScoreHonestUAAndDecodes(t *testing.T) {
	var gotUA, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(serverPayload(t))
	}))
	defer server.Close()

	snapshot, err := Fetch(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snapshot.Deals) != 1 || snapshot.Deals[0].ID != "minimax-daily-400" {
		t.Fatalf("解码结果异常: %+v", snapshot.Deals)
	}
	if snapshot.FetchedAt.IsZero() {
		t.Fatal("FetchedAt 未设置")
	}
	if !strings.Contains(gotUA, "QuotaKeepalive") || strings.Contains(strings.ToLower(gotUA), "mozilla") {
		t.Fatalf("User-Agent 应为诚实 UA，实际 %q", gotUA)
	}
	if gotAccept != "text/plain" {
		t.Fatalf("Accept = %q，want text/plain", gotAccept)
	}
}

func TestFetchReportsUpstreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := Fetch(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v，want 502", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not flatten</html>"))
	}))
	defer bad.Close()
	if _, err := Fetch(context.Background(), bad.Client(), bad.URL); err == nil {
		t.Fatal("非法数据应报错")
	}
}

func TestServiceCachesAndRefreshes(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(serverPayload(t))
	}))
	defer server.Close()

	service := &Service{URL: server.URL, TTL: time.Hour, Client: server.Client()}
	for i := 0; i < 3; i++ {
		snapshot, stale, err := service.Get(context.Background())
		if err != nil || stale || len(snapshot.Deals) != 1 {
			t.Fatalf("第 %d 次 Get: snapshot=%+v stale=%v err=%v", i, snapshot, stale, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("TTL 内应只打一次上游，实际 %d 次", got)
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("Refresh 应强制打上游，实际 %d 次", got)
	}
}

func TestServiceFallsBackToStaleSnapshot(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(serverPayload(t))
	}))
	defer server.Close()

	service := &Service{URL: server.URL, TTL: 0, Client: server.Client()}
	first, stale, err := service.Get(context.Background())
	if err != nil || stale || len(first.Deals) != 1 {
		t.Fatalf("首次 Get 失败: %v stale=%v", err, stale)
	}
	fail.Store(true)
	// TTL=0 会走默认值；把 fetched 时间调旧，模拟缓存过期。
	service.mu.Lock()
	service.fetched = time.Now().Add(-time.Hour)
	service.mu.Unlock()
	second, stale, err := service.Get(context.Background())
	if err == nil || !stale {
		t.Fatalf("上游失败时应返回 stale 快照: stale=%v err=%v", stale, err)
	}
	if second.Deals[0].ID != first.Deals[0].ID {
		t.Fatalf("stale 快照内容不一致: %+v", second.Deals)
	}
}

func TestDecodeSnapshotRejectsNonObjectRoot(t *testing.T) {
	raw, _ := json.Marshal([]any{"not-an-object"})
	_, err := decodeSnapshot(raw)
	if err == nil {
		t.Fatal("根节点不是对象时应报错")
	}
}
