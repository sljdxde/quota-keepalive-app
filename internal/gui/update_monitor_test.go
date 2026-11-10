package gui

import (
	"testing"

	"qka/internal/selfupdate"
)

// updateNotice 只在「发现新版本且与上次提示的版本不同」时返回 true，
// 避免每 6 小时重复弹同一个更新横幅。
func TestUpdateNotice(t *testing.T) {
	available := selfupdate.Status{Available: true, LatestVersion: "0.1.60"}
	notified, notice := updateNotice(available, "")
	if !notice || notified != "0.1.60" {
		t.Fatalf("first notice = %q %v", notified, notice)
	}
	if _, again := updateNotice(available, notified); again {
		t.Fatal("same version must not notify twice")
	}
	next := selfupdate.Status{Available: true, LatestVersion: "0.1.61"}
	if notified, notice := updateNotice(next, notified); !notice || notified != "0.1.61" {
		t.Fatalf("newer version notice = %q %v", notified, notice)
	}
	if _, notice := updateNotice(selfupdate.Status{}, notified); notice {
		t.Fatal("no update must not notify")
	}
}

// 前端要有：后台更新横幅、下载进度、更新说明入口。
func TestUpdateUiShipped(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	requireStrings(t, page, "更新功能 UI",
		`id="update-banner"`,
		`id="update-banner-view"`,
		`id="update-banner-dismiss"`,
		`Events.On("update-available"`,
		`Events.On("update-progress"`,
		`id="settings-update-notes"`,
		`function formatBytes(`,
		`>更新说明</button>`,
	)
}
