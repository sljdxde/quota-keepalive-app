package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"qka/internal/applog"
	"qka/internal/procattr"
	"qka/internal/tokenspeed"
)

// 本文件实现设置页「诊断」区块的后端能力（issue：用户反馈 Windows 卡时
// 拿不到任何信息）。数据分两半：后端负责版本/平台/运行时目录/慢轮询/日志
// 目录；渲染器与性能模式解析结果在前端内存里（perf-mode.js），复制时由前端
// 合并。红线：这里只收集性能与版本元数据，不碰凭据、提示词或 token 内容。
//
// 方法挂在 BoundService 上（同包新文件，不占 app.go/bound.go）：Wails 注册
// 服务时用反射枚举指针类型的全部导出方法，diagnostics.go 里新增的方法会被
// 自动绑定，无需改动注册入口。

// DiagnosticsInfo 是「诊断」区块的后端数据视图：全部为本地路径、版本与
// 性能数字，可安全复制进 issue。
type DiagnosticsInfo struct {
	// Version 是当前运行版本（cli.Version 经 deps.CurrentVersion 注入）。
	Version string `json:"version"`
	// GoVersion 是编译用的 Go 版本（runtime.Version）。
	GoVersion string `json:"go_version"`
	// GOOS/GOARCH 是运行平台与架构。
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	// ConfigDir 是当前生效的配置目录（日志、运行时都在它下面）。
	ConfigDir string `json:"config_dir"`
	// LogDir 是日志目录（applog.Init 解析的结果，可能为空=未初始化）。
	LogDir string `json:"log_dir"`
	// RuntimeDir 是内嵌运行时的解压目录（runtimebundle 布局
	// <configDir>/runtime/<version>）。开发构建未解压时 RuntimeAvailable=false。
	RuntimeDir       string `json:"runtime_dir"`
	RuntimeAvailable bool   `json:"runtime_available"`
	// RuntimeBytes/RuntimeFiles 是解压目录 walk 统计的占用体积与文件数。
	RuntimeBytes int64 `json:"runtime_bytes"`
	RuntimeFiles int   `json:"runtime_files"`
	// TokenSpeedAvailable 表示本进程是否运行了 tokenspeed 服务；false 时
	// SlowPolls 恒为空（如测试装配）。
	TokenSpeedAvailable bool `json:"token_speed_available"`
	// SlowPollThresholdMS 是慢轮询告警阈值（毫秒），与样本一起展示。
	SlowPollThresholdMS int64 `json:"slow_poll_threshold_ms"`
	// SlowPolls 是最近几次慢轮询（旧→新），只含耗时与时间戳。
	SlowPolls []tokenspeed.SlowPollSample `json:"slow_polls"`
	// GeneratedAt 是本视图的生成时刻（RFC3339）。
	GeneratedAt string `json:"generated_at"`
}

// openLogDirLauncher 用系统文件管理器打开目录；包级变量便于测试替换。
var openLogDirLauncher = func(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// explorer 成功打开时也可能返回退出码 1（历史行为），因此只 Start
		// 不等待退出码，避免把成功当失败。
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	// windowsgui 进程直接 spawn 控制台程序会弹黑窗；explorer/open 无控制台，
	// Hide 在这里是无害的保险。
	procattr.Hide(cmd)
	return cmd.Start()
}

// DiagnosticsGet 返回诊断区块所需的后端元数据（不含前端渲染器检测）。
func (s *BoundService) DiagnosticsGet() (json.RawMessage, error) {
	info := buildDiagnosticsInfo(diagnosticsSource{
		version:    s.deps.CurrentVersion,
		tokenSpeed: s.deps.TokenSpeed,
		now:        time.Now,
	})
	return json.Marshal(info)
}

// DiagnosticsOpenLogDir 用系统文件管理器打开日志目录。路径来自 applog 自己
// 解析的目录（配置目录/logs，或 QKA_LOG_DIR 覆盖值），以独立 argv 交给
// explorer/open/xdg-open，全程不做 shell 字符串拼接。
func (s *BoundService) DiagnosticsOpenLogDir() error {
	return openLogDirInFileManager(applog.Dir())
}

// diagnosticsSource 是 buildDiagnosticsInfo 的输入接缝，测试可注入假时钟
// 与无 tokenspeed 的装配。
type diagnosticsSource struct {
	version    string
	tokenSpeed *tokenspeed.Service
	now        func() time.Time
}

// buildDiagnosticsInfo 聚合诊断视图：路径与体积统计是纯本地元数据，
// 慢轮询来自 tokenspeed 的进程内环形记录。configDir 取 applog 在启动时收到
// 的配置目录（gui 不能反向 import cli 取 ResolveConfigDir，那会成环；
// applog.Init 拿到的正是 cli.ResolveConfigDir 的结果）。
func buildDiagnosticsInfo(src diagnosticsSource) DiagnosticsInfo {
	now := src.now
	if now == nil {
		now = time.Now
	}
	version := strings.TrimSpace(src.version)
	if version == "" {
		version = "dev"
	}
	configDir := applog.ConfigDir()
	info := DiagnosticsInfo{
		Version:             version,
		GoVersion:           runtime.Version(),
		GOOS:                runtime.GOOS,
		GOARCH:              runtime.GOARCH,
		ConfigDir:           configDir,
		LogDir:              applog.Dir(),
		RuntimeDir:          runtimeDirPath(configDir, version),
		SlowPollThresholdMS: tokenspeed.SlowPollThresholdMS(),
		SlowPolls:           []tokenspeed.SlowPollSample{},
		GeneratedAt:         now().Format(time.RFC3339),
	}
	if info.RuntimeDir != "" {
		info.RuntimeFiles, info.RuntimeBytes = dirUsage(info.RuntimeDir)
		info.RuntimeAvailable = info.RuntimeFiles > 0
	}
	if src.tokenSpeed != nil {
		info.TokenSpeedAvailable = true
		if polls := src.tokenSpeed.RecentSlowPolls(); len(polls) > 0 {
			info.SlowPolls = polls
		}
	}
	return info
}

// runtimeDirPath 按 runtimebundle 的解压布局（<configDir>/runtime/<version>）
// 给出当前版本的运行时目录。runtimebundle 未提供只算路径的查询，这里复刻
// 布局常量；version 沿用 runtimebundle.validVersion 的约束（无路径分隔符），
// 保证 Join 不会逃出 runtime/。
func runtimeDirPath(configDir, version string) string {
	configDir = strings.TrimSpace(configDir)
	version = strings.TrimSpace(version)
	if configDir == "" || version == "" ||
		version == "." || version == ".." ||
		strings.ContainsAny(version, `/\:`) ||
		filepath.Base(version) != version {
		return ""
	}
	return filepath.Join(configDir, "runtime", version)
}

// dirUsage 走一遍 dir 统计常规文件的个数与总字节。目录不可读或不存在时
// 返回 0,0；诊断是尽力而为，不因目录缺失报错。
func dirUsage(dir string) (files int, bytes int64) {
	_ = filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil // 走错就跳过，统计不求全
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes
}

// openLogDirInFileManager 校验并打开日志目录：先确认是已存在的目录，再把
// 绝对路径作为单个参数交给平台启动器（无 shell 参与）。
func openLogDirInFileManager(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("日志尚未初始化，无法定位日志目录")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("日志目录不存在：%s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("日志路径不是目录：%s", dir)
	}
	return openLogDirLauncher(dir)
}
