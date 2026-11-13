package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"qka/internal/applog"
	"qka/internal/atomicfile"
)

// tray-state.json 记住菜单栏当前伙伴与「已捕获」状态。前端把选择存在
// WebView 的 localStorage 里，后端启动时读不到；不落一份到配置目录的话，
// 每次重启/升级后到首次打开主窗口或面板之前，菜单栏都会退回默认 Q 图标
// （用户反馈「出现非宠物的图标」）。
const (
	trayStateFile  = "tray-state.json"
	defaultTrayPet = "bulbasaur" // 与前端 readMainPetId 的兜底一致
)

type trayState struct {
	Pet    string `json:"pet"`
	Caught bool   `json:"caught"`
}

// trayStatePath 返回状态文件路径；配置目录未知（单测/未初始化日志）时为空。
func trayStatePath() string {
	dir := strings.TrimSpace(applog.ConfigDir())
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, trayStateFile)
}

// loadTrayState 读取上次保存的伙伴状态；文件缺失或损坏时返回零值。
func loadTrayState() trayState {
	path := trayStatePath()
	if path == "" {
		return trayState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return trayState{}
	}
	var state trayState
	if json.Unmarshal(data, &state) != nil {
		return trayState{}
	}
	state.Pet = strings.TrimSpace(state.Pet)
	return state
}

// saveTrayState 原子写入伙伴状态；失败只记一行警告，不影响托盘交互。
func saveTrayState(state trayState) {
	path := trayStatePath()
	if path == "" {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		applog.Warnf("gui", "保存菜单栏伙伴状态失败：%v", err)
	}
}
