package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"qka/internal/privacy"
)

// fileWriter 是 slog 的写口：按天轮转、单文件超大时加序号后缀、写入前统一脱敏。
// 所有落盘写入都经过它，因此 Wails 等外部持有 Logger() 的写入同样被脱敏。
type fileWriter struct {
	mu     sync.Mutex
	dir    string
	day    string
	seq    int
	file   *os.File
	size   int64
	closed bool
}

// newFileWriter 打开 dir 下当前时间对应的日志文件（追加）。
func newFileWriter(dir string, t time.Time) (*fileWriter, error) {
	w := &fileWriter{dir: dir}
	if err := w.rotate(t); err != nil {
		return nil, err
	}
	return w, nil
}

// Write 实现 io.Writer：脱敏后按需轮转再追加。成功时返回 len(p)（表示整条
// 已被消费，实际落盘的是脱敏后的等义内容），失败时返回错误但不 panic。
func (w *fileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	line := privacy.Redact(string(p))
	t := now()
	// 跨天：切到新一天的文件，旧文件留着交给保留策略处理。
	if t.Format(dayLayout) != w.day {
		if err := w.rotate(t); err != nil {
			return 0, err
		}
	}
	// 单文件超限：切到 -1/-2 后缀的新文件，避免一个文件无限增长。
	if w.size > 0 && w.size+int64(len(line)) > maxBytes {
		if err := w.nextFile(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.WriteString(line)
	if err != nil {
		return 0, err
	}
	w.size += int64(n)
	return len(p), nil
}

// Close 关闭当前文件；重复调用安全。关闭后的写入返回 os.ErrClosed。
func (w *fileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	err := w.file.Close()
	w.file = nil
	return err
}

// rotate 切到 t 当天的文件（序号从 0 重新开始），并清理过旧的日志。
func (w *fileWriter) rotate(t time.Time) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	w.day = t.Format(dayLayout)
	w.seq = 0
	return w.open()
}

// nextFile 在当前天切到下一个序号文件（单文件超过 maxBytes 时使用）。
func (w *fileWriter) nextFile() error {
	_ = w.file.Close()
	w.file = nil
	w.seq++
	return w.open()
}

// open 以追加方式打开当前目标文件，并顺带清理过旧文件。
// 数字文件用 O_APPEND：进程重启或重复 Init 都是续写而不是截断。
func (w *fileWriter) open() error {
	path := w.pathFor(w.seq)
	f, err := openFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("applog: 打开日志文件 %s: %w", path, err)
	}
	size := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	w.file = f
	w.size = size
	pruneLogs(w.dir)
	return nil
}

// pathFor 返回当前天第 seq 个文件：qka-YYYYMMDD.log、qka-YYYYMMDD-1.log…
func (w *fileWriter) pathFor(seq int) string {
	name := filePrefix + w.day
	if seq > 0 {
		name += fmt.Sprintf("-%d", seq)
	}
	return filepath.Join(w.dir, name+fileSuffix)
}

// pruneLogs 按天保留最近 keepFiles 天的日志，更旧的 best-effort 删除。
// 删除失败不影响写入：保留策略是磁盘卫生，不是正确性要求。
func pruneLogs(dir string) {
	paths, err := glob(filepath.Join(dir, filePrefix+"*"+fileSuffix))
	if err != nil {
		return
	}
	byDay := map[string][]string{}
	var days []string
	for _, path := range paths {
		day, ok := dayOf(path)
		if !ok {
			continue
		}
		if _, seen := byDay[day]; !seen {
			days = append(days, day)
		}
		byDay[day] = append(byDay[day], path)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	for i, day := range days {
		if i < keepFiles {
			continue
		}
		for _, path := range byDay[day] {
			_ = removeFile(path)
		}
	}
}

// pruneCrashes 只保留最近 keepFiles 个崩溃文件：崩溃日志同属长期存储，
// 不该无限堆积。文件名里的时间戳可直接按字典序排序。
func pruneCrashes(dir string) {
	paths, err := glob(filepath.Join(dir, crashPrefix+"*"+crashSuffix))
	if err != nil {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for i, path := range paths {
		if i < keepFiles {
			continue
		}
		_ = removeFile(path)
	}
}

// dayOf 从 qka-YYYYMMDD[-N].log 里取出 8 位日期；不符合命名的文件不参与
// 保留策略（例如用户手工放进来的文件不会被误删）。
func dayOf(path string) (string, bool) {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return "", false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix)
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) != 8 {
		return "", false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return rest, true
}
