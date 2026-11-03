//go:build windows

package portsel

import (
	"os/exec"
	"strconv"
	"strings"

	"qka/internal/procattr"
)

// runLsof is named for the shared parser; on Windows netstat supplies the
// listening PID without requiring lsof or administrator privileges.
func runLsof(port int) ([]string, error) {
	cmd := exec.Command("netstat", "-ano", "-p", "tcp")
	// The GUI build of QKA has no console of its own; without this netstat
	// would flash a console window.
	procattr.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") ||
			!strings.HasSuffix(fields[1], suffix) {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		name := fmtPIDName(pid)
		return []string{"p" + strconv.Itoa(pid), "c" + name}, nil
	}
	return []string{}, nil
}

func fmtPIDName(pid int) string {
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid),
		"/FO", "CSV", "/NH")
	procattr.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "pid " + strconv.Itoa(pid)
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "INFO:") {
		return "pid " + strconv.Itoa(pid)
	}
	// CSV first field is the image name.
	name := strings.Trim(strings.SplitN(line, ",", 2)[0], `"`)
	if name == "" {
		return "pid " + strconv.Itoa(pid)
	}
	return name
}
