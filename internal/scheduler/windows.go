//go:build windows

package scheduler

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"qka/internal/procattr"
)

type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type windowsScheduler struct {
	run    commandRunner
	now    func() time.Time
	userID string
}

func realRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// The GUI build of QKA has no console of its own; without this schtasks
	// (and its children) would each flash a console window.
	procattr.Hide(cmd)
	return cmd.CombinedOutput()
}

// New returns the current-user Task Scheduler backend.
func New() Scheduler {
	userID := ""
	if current, err := user.Current(); err == nil {
		userID = current.Username
	}
	return &windowsScheduler{run: realRunner, now: time.Now, userID: userID}
}

func newWindowsScheduler(run commandRunner, now func() time.Time) *windowsScheduler {
	return &windowsScheduler{run: run, now: now}
}

func encodeUTF16LE(data []byte) []byte {
	units := utf16.Encode([]rune(string(data)))
	out := make([]byte, 2, 2+len(units)*2)
	out[0], out[1] = 0xff, 0xfe
	for _, unit := range units {
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], unit)
		out = append(out, buf[:]...)
	}
	return out
}

func (s *windowsScheduler) Install(ctx context.Context, spec Spec) error {
	data, err := WindowsTaskXML(spec, s.now(), s.userID)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "qka-task-*.xml")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(encodeUTF16LE(data)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := s.run(ctx, "schtasks", "/Create", "/TN",
		WindowsTaskName(spec.ConfigDir), "/XML", path, "/F")
	if err != nil {
		return fmt.Errorf("scheduler: schtasks create: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *windowsScheduler) Remove(ctx context.Context, spec Spec) error {
	name := WindowsTaskName(spec.ConfigDir)
	out, err := s.run(ctx, "schtasks", "/Delete", "/TN", name, "/F")
	if err != nil && !taskMissing(out) {
		return fmt.Errorf("scheduler: schtasks delete: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *windowsScheduler) Status(ctx context.Context, spec Spec) (Status, error) {
	out, err := s.run(ctx, "schtasks", "/Query", "/TN", WindowsTaskName(spec.ConfigDir))
	if err != nil {
		if !taskMissing(out) {
			return Status{}, fmt.Errorf("scheduler: schtasks query: %w (%s)",
				err, strings.TrimSpace(string(out)))
		}
		return Status{
			Installed:   false,
			PowerPolicy: "当前用户登录时运行；允许电池供电并支持睡眠唤醒后补跑",
		}, nil
	}
	return Status{
		Installed:   true,
		PowerPolicy: "当前用户登录时运行；允许电池供电并支持睡眠唤醒后补跑",
	}, nil
}

func taskMissing(output []byte) bool {
	text := strings.ToLower(string(output))
	for _, marker := range []string{
		"cannot find", "does not exist", "not found", "找不到", "不存在",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// pruneTaskPattern 只匹配「自动执行」任务（QKA Auto Run <8 位 hash>）。
var pruneTaskPattern = regexp.MustCompile(`^QKA Auto Run [0-9a-f]{8}$`)

// PruneOrphans 清理指向已不存在程序的 QKA 自动执行任务（测试二进制或已删除
// 的配置目录遗留）。当前配置目录的任务、以及其它程序仍存在的任务都保留。
func (s *windowsScheduler) PruneOrphans(ctx context.Context, current Spec) (int, error) {
	out, err := s.run(ctx, "schtasks", "/Query", "/FO", "CSV", "/NH")
	if err != nil {
		return 0, fmt.Errorf("scheduler: schtasks query: %w", err)
	}
	keep := WindowsTaskName(current.ConfigDir)
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.FieldsPerRecord = -1
	removed := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return removed, fmt.Errorf("scheduler: parse schtasks output: %w", readErr)
		}
		if len(record) == 0 {
			continue
		}
		name := strings.TrimSpace(record[0])
		if name == keep || !pruneTaskPattern.MatchString(name) {
			continue
		}
		command, queryErr := s.taskCommand(ctx, name)
		if queryErr != nil || command == "" {
			// 读不到命令就保守跳过，绝不误删仍可运行的任务。
			continue
		}
		if _, statErr := os.Stat(command); statErr == nil {
			continue
		}
		if _, delErr := s.run(ctx, "schtasks", "/Delete", "/TN", name, "/F"); delErr != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// taskCommand 读取一个任务的 <Command>（可执行文件路径）。schtasks 的 XML
// 输出可能是 UTF-16LE（带 BOM），统一转成 UTF-8 再解析。
func (s *windowsScheduler) taskCommand(ctx context.Context, name string) (string, error) {
	out, err := s.run(ctx, "schtasks", "/Query", "/TN", name, "/XML")
	if err != nil {
		return "", err
	}
	data := decodeScheduledTaskXML(out)
	var task struct {
		Actions struct {
			Exec struct {
				Command string `xml:"Command"`
			} `xml:"Exec"`
		} `xml:"Actions"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// 数据已在上一步转成 UTF-8；XML 声明里仍写着 UTF-16，直接放行。
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	if err := decoder.Decode(&task); err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(task.Actions.Exec.Command), `"`), nil
}

// decodeScheduledTaskXML 把 schtasks 的 UTF-16LE（BOM）或 UTF-8 BOM 输出转成
// 无 BOM 的 UTF-8；其它编码原样返回，交给 xml.Unmarshal 失败后保守跳过。
func decodeScheduledTaskXML(data []byte) []byte {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		units := make([]uint16, 0, (len(data)-2)/2)
		for i := 2; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
		}
		return []byte(string(utf16.Decode(units)))
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}
