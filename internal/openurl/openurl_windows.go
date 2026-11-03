//go:build windows

package openurl

import (
	"os/exec"

	"qka/internal/procattr"
)

// Open opens raw through the shell URL handler. The URL is passed as a single
// argv entry; no shell command string is constructed.
func Open(raw string) error {
	if err := validate(raw); err != nil {
		return err
	}
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", raw)
	// The GUI build of QKA has no console of its own; without this the helper
	// would be given one, and a window, before the browser takes over.
	procattr.Hide(cmd)
	return cmd.Run()
}
