//go:build windows

package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"qka/internal/procattr"
)

// windowsInstallerScript replaces the running QKA executable in place.
//
// Windows 发行物是单个自包含 .exe，更新包就是新版本本体，因此不再解压任何
// 归档：Windows 不允许删除或覆盖正在运行的 .exe，但允许重命名它。脚本等待
// 旧进程退出后把旧 exe 改名为 <name>.exe.old，把下载的新 exe 移到原路径，
// 启动并观察数秒；任何一步失败都回滚改名、重启旧版本并记录原因。
const windowsInstallerScript = `param(
    [Parameter(Mandatory=$true)][string]$Package,
    [Parameter(Mandatory=$true)][string]$Executable,
    [Parameter(Mandatory=$true)][int]$ProcessId
)

$ErrorActionPreference = "Stop"
$LogPath = Join-Path (Split-Path -Parent $PSCommandPath) "qka-install-error.log"

function Get-TargetProcesses([string]$Executable, [string]$Directory) {
    # 等待当前 exe 的进程名（用户改名安装时进程名随之改变），并用目录过滤
    # 掉另一个目录里的同名 QKA；目录里别的辅助进程（历史发行物残留的
    # bun.exe 等）不锁目标文件，也不该拖住更新。
    $ProcessName = [IO.Path]::GetFileNameWithoutExtension($Executable)
    $Canonical = [IO.Path]::GetFullPath($Directory) -replace '[\\/]+$', ''
    return Get-Process -ErrorAction SilentlyContinue | Where-Object {
        try {
            if ($_.ProcessName -ne $ProcessName) { return $false }
            if ($null -eq $_.Path) { return $false }
            $EntryDir = [IO.Path]::GetFullPath((Split-Path -Parent $_.Path)) -replace '[\\/]+$', ''
            return ($EntryDir -eq $Canonical)
        } catch {
            return $false
        }
    }
}

function Stop-Update([string]$Message) {
    try {
        Add-Content -LiteralPath $LogPath -Value ((Get-Date).ToString("o") + " " + $Message) -Encoding UTF8
    } catch {
    }
    exit 1
}

function Move-WithRetries([string]$Source, [string]$Destination) {
    # 杀毒软件或同步盘可能短暂持有文件句柄，Windows 会拒绝改名；重试几秒
    # 通常就能成功，仍失败则返回最后一次错误。
    $Last = $null
    for ($Attempt = 1; $Attempt -le 10; $Attempt++) {
        try {
            Move-Item -LiteralPath $Source -Destination $Destination -Force -ErrorAction Stop
            return $null
        } catch {
            $Last = $_.Exception.Message
            Start-Sleep -Milliseconds 500
        }
    }
    return $Last
}

function Clear-PreviousSwaps([string]$Directory, [string]$ExecutableName) {
    # 之前的更新留下的 .old/.failed 只在确认旧进程已退出后清理，且始终是
    # 尽力而为：清理不掉也不能影响本次更新。
    foreach ($Pattern in @(
        ("${ExecutableName}.old"),
        ("${ExecutableName}.old-*"),
        ("${ExecutableName}.failed")
    )) {
        Get-ChildItem -LiteralPath $Directory -Filter $Pattern -File -ErrorAction SilentlyContinue |
            Remove-Item -Force -ErrorAction SilentlyContinue
    }
}

function Get-BackupPath([string]$Executable) {
    $Path = "${Executable}.old"
    for ($Suffix = 2; Test-Path -LiteralPath $Path; $Suffix++) {
        $Path = "${Executable}.old-${Suffix}"
    }
    return $Path
}

try {
    # a. 等当前 QKA 进程退出。
    while (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) {
        Start-Sleep -Seconds 1
    }

    # b. 校验更新包是单个 Windows 可执行文件。
    if ([IO.Path]::GetExtension($Package) -ne ".exe") {
        throw "更新包不是 .exe 单文件包"
    }
    if (-not (Test-Path -LiteralPath $Package -PathType Leaf)) {
        throw "更新包不存在"
    }
    $PackageItem = Get-Item -LiteralPath $Package
    if ($PackageItem.Length -le 0) {
        throw "更新包为空"
    }
    $Header = [byte[]](0, 0)
    $Stream = $null
    try {
        $Stream = [IO.File]::OpenRead($Package)
        $null = $Stream.Read($Header, 0, 2)
    } finally {
        if ($null -ne $Stream) { $Stream.Dispose() }
    }
    if ($Header[0] -ne 0x4D -or $Header[1] -ne 0x5A) {
        throw "更新包不是有效的 Windows 可执行文件"
    }
    $TargetDir = Split-Path -Parent $Executable
    $ExecutableName = Split-Path -Leaf $Executable
    if ([string]::IsNullOrWhiteSpace($TargetDir) -or
        $TargetDir -eq [IO.Path]::GetPathRoot($TargetDir)) {
        throw "目标目录无效（不能是盘符根目录）"
    }
    if ([string]::IsNullOrWhiteSpace((Split-Path -Parent $TargetDir))) {
        throw "目标目录的上级目录无效"
    }
    if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
        throw "当前可执行文件不存在"
    }
    if ([IO.Path]::GetFullPath($Package) -eq [IO.Path]::GetFullPath($Executable)) {
        throw "更新包不能是当前正在运行的可执行文件"
    }

    # 目标目录里可能还有旧实例（改名安装时进程名随 exe 名变化）；它们完全
    # 退出前无法安全改名 exe。
    $Deadline = (Get-Date).AddMinutes(2)
    while (Get-TargetProcesses $Executable $TargetDir) {
        if ((Get-Date) -gt $Deadline) {
            throw "QKA 仍在运行，请退出后重试"
        }
        Start-Sleep -Seconds 1
    }

    Clear-PreviousSwaps $TargetDir $ExecutableName
    $BackupPath = Get-BackupPath $Executable

    # c. 把旧 exe 改名到同目录的 <name>.exe.old（运行时允许改名）。
    $MoveError = Move-WithRetries $Executable $BackupPath
    if ($null -ne $MoveError) {
        throw "无法把 ${ExecutableName} 改名为备份文件：$MoveError"
    }

    $Failure = $null
    try {
        # 把新 exe 移到原路径：同卷时是改名，跨卷时复制后删除源文件。
        $MoveError = Move-WithRetries $Package $Executable
        if ($null -ne $MoveError) {
            throw "无法安装新的 ${ExecutableName}：$MoveError"
        }
        $NewProcess = Start-Process -FilePath $Executable -WorkingDirectory $TargetDir -PassThru
        Start-Sleep -Seconds 5
        if ($NewProcess.HasExited) {
            throw "更新后的 QKA 启动后立即退出（退出码 $($NewProcess.ExitCode)）"
        }
    } catch {
        # d. 回滚：把失败的新版本挪到 .failed，把旧 exe 移回原路径并重启。
        $Failure = $_.Exception.Message
        if (Test-Path -LiteralPath $Executable) {
            $null = Move-WithRetries $Executable "${Executable}.failed"
        }
        if (Test-Path -LiteralPath $BackupPath) {
            $null = Move-WithRetries $BackupPath $Executable
        }
        if (Test-Path -LiteralPath $BackupPath -PathType Leaf) {
            # 原路径没能恢复（新 exe 仍被占用）：直接在备份位置启动旧版本，
            # 至少不让 QKA 处于不可用状态。
            Start-Process -FilePath $BackupPath -WorkingDirectory $TargetDir | Out-Null
        } elseif (Test-Path -LiteralPath $Executable -PathType Leaf) {
            Start-Process -FilePath $Executable -WorkingDirectory $TargetDir | Out-Null
        }
    }
    if ($null -ne $Failure) {
        throw $Failure
    }

    # e. 新版本已在运行，尽力清理备份。
    Remove-Item -LiteralPath $BackupPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath "${Executable}.failed" -Force -ErrorAction SilentlyContinue
} catch {
    Stop-Update $_.Exception.Message
}
`

// windowsInstallerScriptPrefix marks the script as UTF-8. Windows PowerShell
// 5.1 reads a .ps1 without a byte order mark using the system code page, which
// turns the Chinese diagnostics in the script into mojibake on English systems.
const windowsInstallerScriptPrefix = "\xEF\xBB\xBF"

// windowsInstallerScriptBytes is exactly what is written to qka-install.ps1.
func windowsInstallerScriptBytes() []byte {
	return append([]byte(windowsInstallerScriptPrefix), windowsInstallerScript...)
}

func startInstaller(ctx context.Context, executable, packagePath string) error {
	if executable == "" {
		return errors.New("selfupdate: 可执行文件路径不可用")
	}
	if !strings.EqualFold(filepath.Ext(packagePath), ".exe") {
		return fmt.Errorf(
			"selfupdate: Windows 自更新只接受 .exe 单文件包，收到 %q",
			filepath.Ext(packagePath),
		)
	}
	info, err := os.Stat(packagePath)
	if err != nil {
		return fmt.Errorf("selfupdate: 更新包不存在或不可读: %s: %w", packagePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("selfupdate: 更新包不是文件: %s", packagePath)
	}
	scriptPath := filepath.Join(filepath.Dir(packagePath), "qka-install.ps1")
	if err := os.WriteFile(scriptPath, windowsInstallerScriptBytes(), 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath, packagePath, executable, strconv.Itoa(updateProcessID()),
	)
	// 更新脚本在后台跑，GUI 版 QKA 没有控制台，不隐藏会弹一个 powershell 黑窗口。
	procattr.Hide(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (s *Service) installer() (string, error) {
	exe := s.Executable
	if exe == nil {
		exe = os.Executable
	}
	return exe()
}
