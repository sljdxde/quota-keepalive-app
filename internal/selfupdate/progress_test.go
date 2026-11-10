package selfupdate

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestReleasePageURL(t *testing.T) {
	if got := releasePageURL("v0.1.59"); got != releasePagePrefix+"v0.1.59" {
		t.Fatalf("releasePageURL = %q", got)
	}
	if got := releasePageURL(" 0.1.60 "); got != releasePagePrefix+"v0.1.60" {
		t.Fatalf("trimmed releasePageURL = %q", got)
	}
	if releasePageURL("") != "" {
		t.Fatal("empty version must have no release page")
	}
}

// 下载时按字节汇报进度：GUI 设置页用它显示百分比，而不是只写「下载中」。
func TestDownloadReportsProgress(t *testing.T) {
	artifact := bytes.Repeat([]byte("qka-update-payload"), 4096)
	sum := digest(artifact)
	stateDir := t.TempDir()
	var gotDownloaded, gotTotal int64
	svc := &Service{
		CurrentVersion: "0.1.58",
		StateDir:       stateDir,
		Platform:       "darwin/arm64",
		Progress: func(downloaded, total int64) {
			gotDownloaded, gotTotal = downloaded, total
		},
	}
	svc.Client = doerFunc(func(req *http.Request) (*http.Response, error) {
		resp := response(200, bytes.NewReader(artifact))
		resp.ContentLength = int64(len(artifact))
		return resp, nil
	})
	release := Release{Version: "0.1.59", Name: "QKA-0.1.59-arm64.zip",
		URL: "https://updates.example/QKA-0.1.59-arm64.zip", SHA256: sum}
	path, err := svc.download(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("download did not return a staged path")
	}
	if gotDownloaded != int64(len(artifact)) || gotTotal != int64(len(artifact)) {
		t.Fatalf("progress = %d/%d, want %d/%d", gotDownloaded, gotTotal, len(artifact), len(artifact))
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(artifact)) {
		t.Fatalf("staged artifact = %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "staged.json")); err != nil {
		t.Fatalf("staged metadata missing: %v", err)
	}
}

// Check 返回的 Status 带上 release 页地址，前端据此给「更新说明」入口。
func TestCheckCarriesReleaseURL(t *testing.T) {
	artifact := []byte("payload")
	sum := digest(artifact)
	feed := releaseFeed("0.1.59", "darwin/arm64", "QKA-0.1.59-arm64.zip",
		"https://updates.example/QKA-0.1.59-arm64.zip", sum, true)
	svc := &Service{
		CurrentVersion: "0.1.58",
		FeedURL:        "https://updates.example/feed.json",
		StateDir:       t.TempDir(),
		Platform:       "darwin/arm64",
		Client: doerFunc(func(req *http.Request) (*http.Response, error) {
			return response(200, bytes.NewReader(feed)), nil
		}),
	}
	status, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.LatestVersion != "0.1.59" {
		t.Fatalf("status = %+v", status)
	}
	if status.ReleaseURL != releasePagePrefix+"v0.1.59" {
		t.Fatalf("release url = %q", status.ReleaseURL)
	}
}
