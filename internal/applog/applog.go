// Package applog 提供落盘、带保留期的文件日志，供桌面形态事后排查。
//
// Windows 上 QKA.exe 是 windowsgui 程序：没有控制台，启动失败、panic 或行为
// 异常时用户看不到任何输出。这里把启动、关键流程、失败与 panic 写进
// <configDir>/logs/qka-YYYYMMDD.log（QKA_LOG_DIR 可覆盖目录），按天轮转、
// 保留最近 7 天，单文件超过 32MB 切到 -1/-2 后缀；panic 额外写
// crash-<ts>.log，并在 Windows 上弹原生提示告知日志目录。
//
// 日志是排障辅助，不是启动前提：初始化失败只返回错误、绝不 panic；未 Init
// （或 Init 失败）时所有写口静默丢弃。所有自由文本在写盘前统一过
// internal/privacy 的脱敏规则，日志里不会出现令牌、账号或提示词明文。
package applog

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"qka/internal/privacy"
)

const (
	// KeepFiles 是默认保留的日志文件天数（一天一个文件）。
	KeepFiles = 7
	// MaxFileBytes 是单个日志文件的默认上限（32MB），超过后切到 -1/-2 后缀。
	MaxFileBytes int64 = 32 << 20
	// PanicExitCode 是 panic 被捕获后的进程退出码，与 CLI 启动失败保持一致。
	PanicExitCode = 2

	// dirName 是日志目录相对配置目录的名字。
	dirName = "logs"
	// filePrefix/fileSuffix 是日志文件名：qka-YYYYMMDD[-N].log。
	filePrefix = "qka-"
	fileSuffix = ".log"
	// crashPrefix/crashSuffix 是崩溃文件名：crash-YYYYMMDD-HHMMSS.log。
	crashPrefix = "crash-"
	crashSuffix = ".log"

	// dayLayout/stampLayout 分别是日志文件与崩溃文件的时间格式。
	dayLayout   = "20060102"
	stampLayout = "20060102-150405"
	// levelEnv 覆盖日志级别；dirEnv 覆盖日志目录。
	levelEnv = "QKA_LOG_LEVEL"
	dirEnv   = "QKA_LOG_DIR"
)

// 测试接缝：真实环境只有 os.Getenv/time.Now。测试用临时目录、假时钟与这些
// 变量覆盖轮转、保留期和失败分支，不必真的等一天或制造磁盘故障。
var (
	now        = time.Now
	glob       = filepath.Glob
	removeFile = os.Remove
	mkdirAll   = os.MkdirAll
	openFile   = os.OpenFile
	writeCrash = os.WriteFile
	keepFiles  = KeepFiles
	maxBytes   = MaxFileBytes
)

// discardLogger 是未 Init 时的写口。slog 会先问 Enabled，丢弃处理器直接拒绝，
// 没有格式化开销，也不会让调用方到处判空。
var discardLogger = slog.New(slog.DiscardHandler)

// core 是一次 Init 的落盘状态。
type core struct {
	writer *fileWriter
	logger *slog.Logger
	dir    string
	// configDir 是 Init 收到的配置目录（日志目录的父级，QKA_LOG_DIR 覆盖时
	// 与 dir 无派生关系）；诊断区块用它定位运行时等兄弟目录。
	configDir string
	closed    atomic.Bool
}

// current 是进程级当前日志状态。用原子指针让 Logger() 无锁、Init 可替换。
var current atomic.Pointer[core]

// Init 初始化落盘日志：创建 <configDir>/logs/（QKA_LOG_DIR 可覆盖），打开并
// 续写当天的 qka-YYYYMMDD.log，并清理过旧文件。返回的关闭函数可重复调用；
// 重复 Init 会先换上新日志再关闭上一份，所以测试与多次启动都安全。
//
// 失败只返回错误、不 panic；此时 Logger() 返回丢弃日志器，程序照常运行。
// 返回的关闭函数在失败时也是非 nil 的空操作，调用方可以无条件 defer。
func Init(configDir, buildVersion string) (func() error, error) {
	dir, err := resolveDir(configDir)
	if err != nil {
		return noopClose, err
	}
	if err := mkdirAll(dir, 0o700); err != nil {
		return noopClose, fmt.Errorf("applog: 创建日志目录 %s: %w", dir, err)
	}
	writer, err := newFileWriter(dir, now())
	if err != nil {
		return noopClose, err
	}
	level := parseLevel(os.Getenv(levelEnv))
	c := &core{
		writer:    writer,
		logger:    slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})),
		dir:       dir,
		configDir: strings.TrimSpace(configDir),
	}
	// 先换上新状态再关闭旧的：关闭函数只清自己，不碰后来的日志。
	if old := current.Swap(c); old != nil {
		_ = old.close()
	}
	c.logger.Info("日志已初始化",
		"module", "applog", "version", buildVersion, "dir", dir, "min_level", level.String())
	return c.close, nil
}

// noopClose 是 Init 失败时返回的空关闭函数。
func noopClose() error { return nil }

// resolveDir 决定日志目录：QKA_LOG_DIR > <configDir>/logs。
func resolveDir(configDir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(dirEnv)); override != "" {
		return override, nil
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "", fmt.Errorf("applog: 配置目录为空，无法定位日志目录")
	}
	return filepath.Join(configDir, dirName), nil
}

// parseLevel 解析 QKA_LOG_LEVEL；缺省或无法识别时用 info（不因笔误静默丢日志）。
func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Logger 返回当前落盘日志器；未 Init 或 Init 失败时返回丢弃日志器，永不 nil。
// 长期持有日志器的调用方（如 Wails）应使用 Module 带上模块标签。
func Logger() *slog.Logger {
	if c := current.Load(); c != nil {
		return c.logger
	}
	return discardLogger
}

// Module 返回带模块标签的日志器，供需要长期持有日志器的地方使用
// （如 Wails 内部 warning/error）。未 Init 时同样返回丢弃日志器。
func Module(name string) *slog.Logger {
	if c := current.Load(); c != nil {
		return c.logger.With("module", name)
	}
	return discardLogger
}

// Dir 返回当前日志目录；未 Init 或 Init 失败时返回空串。设置页的诊断区块
// 用它展示日志位置并提供「打开日志目录」入口。
func Dir() string {
	if c := current.Load(); c != nil {
		return c.dir
	}
	return ""
}

// ConfigDir 返回 Init 收到的配置目录；未 Init 或 Init 失败时返回空串。
// 诊断区块用它定位运行时解压目录等配置目录下的兄弟目录。
func ConfigDir() string {
	if c := current.Load(); c != nil {
		return c.configDir
	}
	return ""
}

// Infof 写一行 info 日志。module 是模块标签（如 runtimebundle），format 后的
// 自由文本会随整行统一脱敏。
func Infof(module, format string, args ...any) { logf(slog.LevelInfo, module, format, args...) }

// Debugf 写一行 debug 日志（QKA_LOG_LEVEL=debug 时可见）。
func Debugf(module, format string, args ...any) { logf(slog.LevelDebug, module, format, args...) }

// Warnf 写一行 warn 日志。
func Warnf(module, format string, args ...any) { logf(slog.LevelWarn, module, format, args...) }

// Errorf 写一行 error 日志。
func Errorf(module, format string, args ...any) { logf(slog.LevelError, module, format, args...) }

// logf 是便利函数的共同实现：未 Init 时直接返回（静默），否则按级别落盘。
func logf(level slog.Level, module, format string, args ...any) {
	c := current.Load()
	if c == nil {
		return
	}
	c.logger.Log(context.Background(), level, fmt.Sprintf(format, args...), "module", module)
}

// RecoverPanic 捕获当前 goroutine 的 panic：写 error 日志与崩溃文件，并在
// Windows 上弹原生提示告知日志目录。
//
// recover 只能由被 defer 的函数直接调用，因此用法固定为
//
//	defer applog.RecoverPanic()
//
// 需要把 panic 映射成退出码时用 Run，不要在闭包里再包一层调用。
func RecoverPanic() {
	if r := recover(); r != nil {
		reportCrash(r)
	}
}

// Run 用 RecoverPanic 的同一套处理包住 fn：正常返回时透传 fn 的退出码，
// panic 时写日志与崩溃文件并返回 PanicExitCode，避免 windowsgui 进程
// 「无输出地消失」。
func Run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			reportCrash(r)
			code = PanicExitCode
		}
	}()
	return fn()
}

// reportCrash 落盘一次 panic。自身出现任何异常都吞掉：已经是最坏情况，
// 提示失败也绝不能再 panic 一次。
func reportCrash(r any) {
	defer func() { _ = recover() }()

	message := privacy.SanitizeText(fmt.Sprint(r), 0)
	stack := privacy.SanitizeText(string(debug.Stack()), 0)
	Errorf("panic", "捕获到未处理 panic：%s", message)

	c := current.Load()
	if c == nil {
		return // 没有日志目录：不落文件、不弹窗，只保证不 panic。
	}
	t := now()
	body := fmt.Sprintf("time: %s\nlog_dir: %s\npanic: %s\n\n%s\n",
		t.Format("2006-01-02 15:04:05"), c.dir, message, stack)
	path := filepath.Join(c.dir, crashPrefix+t.Format(stampLayout)+crashSuffix)
	_ = writeCrash(path, []byte(body), 0o600)
	pruneCrashes(c.dir)
	showCrashNotice(c.dir)
}

// close 关闭文件写口；重复调用安全，且不会影响后来 Init 的新日志。
func (c *core) close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	current.CompareAndSwap(c, nil)
	return c.writer.Close()
}
