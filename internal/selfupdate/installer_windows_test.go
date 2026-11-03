//go:build windows

package selfupdate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsInstallerScriptIsUTF8WithBOM guards the encoding Windows
// PowerShell 5.1 needs to read the script's Chinese diagnostics correctly.
func TestWindowsInstallerScriptIsUTF8WithBOM(t *testing.T) {
	if !bytes.HasPrefix(windowsInstallerScriptBytes(), []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("installer script must be UTF-8 with a byte order mark")
	}
}

func TestWindowsInstallerScriptReplacesSingleExecutable(t *testing.T) {
	for _, required := range []string{
		// Waits for the replaced process, then keeps the existing path checks.
		"while (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)",
		"Get-TargetProcesses",
		"$_.ProcessName -ne $ProcessName",
		"$_.Path",
		"[IO.Path]::GetPathRoot($TargetDir)",
		"Split-Path -Leaf $Executable",
		"更新包不存在",
		// Single-file swap: rename the running exe aside under the current
		// executable name, then move the downloaded exe into its place.
		`"${Executable}.old"`,
		`Move-WithRetries $Executable $BackupPath`,
		`Move-WithRetries $Package $Executable`,
		`Start-Process -FilePath $Executable`,
		// The new build must stay up for a few seconds, else roll back.
		"Start-Sleep -Seconds 5",
		"更新后的 QKA 启动后立即退出",
		`Move-WithRetries $BackupPath $Executable`,
		`Remove-Item -LiteralPath $BackupPath`,
	} {
		if !strings.Contains(windowsInstallerScript, required) {
			t.Errorf("installer script missing %q", required)
		}
	}
}

func TestWindowsInstallerScriptNoLongerUnpacksZIP(t *testing.T) {
	for _, forbidden := range []string{
		"Expand-Archive",
		"QKA-cli.exe",
		"README.txt",
		"bun.exe",
		"provider-plugins",
		"managed item",
	} {
		if strings.Contains(windowsInstallerScript, forbidden) {
			t.Errorf("installer script still contains ZIP-era marker %q", forbidden)
		}
	}
}

func TestWindowsInstallerRejectsNonExeBeforeLaunch(t *testing.T) {
	for _, packagePath := range []string{
		`C:\updates\QKA-1.2.3-windows-amd64.zip`,
		`C:\updates\QKA.dmg`,
		`C:\updates\QKA-1.2.3-windows-amd64`,
	} {
		err := startInstaller(context.Background(), `C:\QKA\QKA.exe`, packagePath)
		if err == nil {
			t.Fatalf("non-.exe Windows update %q must be rejected", packagePath)
		}
		if !strings.Contains(err.Error(), ".exe") {
			t.Fatalf("error for %q should name the required .exe package: %v", packagePath, err)
		}
	}
}

func TestWindowsInstallerRejectsMissingPackage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "QKA-1.2.3-windows-amd64.exe")
	err := startInstaller(context.Background(), `C:\QKA\QKA.exe`, missing)
	if err == nil {
		t.Fatal("missing Windows .exe package must be rejected")
	}
	if !strings.Contains(err.Error(), "更新包") {
		t.Fatalf("missing package error = %v", err)
	}
}

// TestWindowsInstallerScriptParses catches a broken replacement script before
// it can be dropped next to a real install: Windows CI only compiles test
// binaries, so this runs on a Windows developer machine or during acceptance.
func TestWindowsInstallerScriptParses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "qka-install.ps1"), windowsInstallerScriptBytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := `$tokens = $null; $errors = $null; ` +
		`[void][System.Management.Automation.Language.Parser]::ParseFile(` +
		`'qka-install.ps1', [ref]$tokens, [ref]$errors); ` +
		`if (@($errors).Count -gt 0) { $errors | ForEach-Object { Write-Output $_.ToString() }; exit 1 }`
	cmd := exec.Command("powershell.exe",
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", parse)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer script does not parse: %v\n%s", err, out)
	}
}
