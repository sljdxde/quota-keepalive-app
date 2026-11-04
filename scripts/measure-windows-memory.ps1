# ============================================================================
# measure-windows-memory.ps1 —— QKA 三窗口(WebView2)内存/CPU 量化采样脚本
#
# 用途:GitHub issue #7 性能调查。启动一个 QKA 实例,周期采样:
#   - QKA 主进程的 WorkingSet / PrivateMemory / CPU
#   - 全部归属于 QKA 的 msedgewebview2 子进程(沿父进程链归属)的内存与 CPU
#   - 尽力按创建顺序把渲染进程对应到窗口(bar / main / panel),归属不确定时
#     会明确标注
# 输出:每进程明细表 + 汇总(均值/峰值)+ 可直接粘贴到 GitHub issue 的表格。
#
# 兼容性:PowerShell 5.1(Windows 10 自带),无需管理员权限。
#
# 用法示例:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\measure-windows-memory.ps1
#   powershell -NoProfile -File scripts\measure-windows-memory.ps1 -Duration 30 -Interval 2
#   powershell -NoProfile -File scripts\measure-windows-memory.ps1 -LaunchArgs tray
#       (tray 形态只创建常驻刘海 bar,可单独测「常驻底噪」)
#
# 清理:只 Kill QKA 主进程(WebView2 子进程会随之退出),随后核对并兜底
#       清理残留的归属子进程,绝不动其它应用的 msedgewebview2。
# ============================================================================

param(
    # exe 路径:默认依次找 dist\build\QKA-windows-amd64.exe(开发机构建产物)、
    # %LOCALAPPDATA%\Programs\Quota Keepalive\QKA.exe(安装版)。也可显式传入。
    [string]$ExePath = "",
    # 采样总时长(秒)
    [double]$Duration = 60,
    # 采样间隔(秒)
    [double]$Interval = 2,
    # 传给 QKA 的启动参数,如 "tray"(托盘形态:只有常驻刘海,不弹主窗口)
    [string]$LaunchArgs = "",
    # 显式指定 QKA 的工作目录(默认:仓库 dist\build 产物→仓库根;已安装版→exe 所在目录)
    [string]$WorkingDir = "",
    # 启动后等待窗口/子进程就绪后的静置时间(秒),让首屏渲染、资源解压完成
    [int]$SettleSeconds = 6,
    # 只打印表格,不在结尾重复输出 markdown 块
    [switch]$SkipMarkdown
)

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# 1. 定位 QKA exe
# ---------------------------------------------------------------------------
function Resolve-QkaExe {
    param([string]$Requested)

    # 显式传入的路径优先,直接校验
    if ($Requested -ne "") {
        if (-not (Test-Path -LiteralPath $Requested)) {
            throw "指定的 ExePath 不存在: $Requested"
        }
        return (Resolve-Path -LiteralPath $Requested).Path
    }
    # 依次尝试:仓库内构建产物 → 安装版
    $candidates = @()
    $repoRoot = Split-Path -Parent $PSScriptRoot
    $candidates += (Join-Path $repoRoot "dist\build\QKA-windows-amd64.exe")
    $candidates += (Join-Path $repoRoot "dist\build\QKA-windows-arm64.exe")
    $installed = Join-Path $env:LOCALAPPDATA "Programs\Quota Keepalive\QKA.exe"
    $candidates += $installed
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { return (Resolve-Path -LiteralPath $c).Path }
    }
    throw "未找到 QKA exe。请先 make build-windows,或用 -ExePath 显式指定。尝试过:`n" + ($candidates -join "`n")
}

# ---------------------------------------------------------------------------
# 2. 检测是否已有 QKA 实例在运行
#    依据 1:GUI 单实例命名互斥体 Local\QuotaKeepalive.GUI.Instance(最权威,
#            见 internal/gui/instance_windows.go)
#    依据 2:进程名兜底(QKA.exe / QKA-windows-*.exe 等开发构建名)
# ---------------------------------------------------------------------------
function Test-QkaRunning {
    $running = $false
    # 2.1 互斥体存在 == 有实例持有单实例锁
    $mutex = $null
    try {
        $mutex = [System.Threading.Mutex]::OpenExisting("Local\QuotaKeepalive.GUI.Instance")
        $running = $true
    } catch [System.Threading.WaitHandleCannotBeOpenedException] {
        # 不存在 → 没有实例在跑,正常
    } catch {
        # 其它异常(权限等)不误判,留给进程名兜底
    } finally {
        if ($mutex) { $mutex.Close() }
    }
    # 2.2 进程名兜底:覆盖互斥体检测不到的异常情况
    if (-not $running) {
        $procs = Get-Process -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -eq "QKA" -or $_.Name -like "QKA-windows-*" -or $_.Name -eq "qka" }
        if ($procs) { $running = $true }
    }
    return $running
}

# ---------------------------------------------------------------------------
# 3. 进程树快照:全量查询一次 Win32_Process,返回
#    @{ Procs = pid→CIM对象映射; Descendants = QKA 全部后代的 pid 集合 }
#    归属方式:沿 ParentProcessId 逐级上溯,能在 8 层内到达 QKA 主进程即算后代
#    (QKA→browser→renderer 链通常 2 层;8 层上限同时防 PID 复用成环)
# ---------------------------------------------------------------------------
function Get-ProcessTreeSnapshot {
    param([int]$RootPid)

    $all = Get-CimInstance Win32_Process |
        Where-Object { $_.ProcessId -and $_.ProcessId -ne 0 }
    $byPid = @{}
    foreach ($p in $all) { $byPid[[int]$p.ProcessId] = $p }

    $descendants = New-Object System.Collections.Generic.HashSet[int]
    if (-not $byPid.ContainsKey($RootPid)) {
        return @{ Procs = $byPid; Descendants = $descendants }
    }
    foreach ($p in $all) {
        $cur = $p
        $depth = 0
        while ($null -ne $cur -and $depth -lt 8) {
            $parentPid = 0
            if ($cur.ParentProcessId) { $parentPid = [int]$cur.ParentProcessId }
            if ($parentPid -eq $RootPid) {
                [void]$descendants.Add([int]$p.ProcessId)
                break
            }
            if (-not $byPid.ContainsKey($parentPid)) { break }
            $cur = $byPid[$parentPid]
            $depth++
        }
    }
    return @{ Procs = $byPid; Descendants = $descendants }
}

# ---------------------------------------------------------------------------
# 4. WebView2 子进程角色分类(依据命令行参数,Chromium 惯例)
# ---------------------------------------------------------------------------
function Get-WebviewRole {
    param([string]$CommandLine)
    if ([string]::IsNullOrEmpty($CommandLine)) { return "未知(无命令行)" }
    if ($CommandLine -match '--type=renderer') {
        # 渲染进程:每个 Wails WebviewWindow 一个,对应一个窗口页面
        return "renderer(渲染页面)"
    }
    if ($CommandLine -match '--type=gpu-process')   { return "gpu(合成/光栅)" }
    if ($CommandLine -match '--type=utility')       { return "utility(网络服务等)" }
    if ($CommandLine -match '--type=crashpad')      { return "crashpad(崩溃报告)" }
    if ($CommandLine -match '--type=')              { return "其他type子进程" }
    # 没有 --type= 的是浏览器主进程(每个 WebView2 环境一个,窗口间共享)
    return "browser(WebView2主进程)"
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
$exe = Resolve-QkaExe $ExePath
$procName = [System.IO.Path]::GetFileNameWithoutExtension($exe)
$launchArgsDesc = $LaunchArgs
if ($launchArgsDesc -eq "") { $launchArgsDesc = "<无,弹出主窗口>" }
Write-Host "=== QKA 窗口内存/CPU 采样 ===" -ForegroundColor Cyan
Write-Host ("exe       : {0}" -f $exe)
Write-Host ("启动参数  : {0}" -f $launchArgsDesc)
Write-Host ("采样      : 时长 {0}s,间隔 {1}s,静置 {2}s" -f $Duration, $Interval, $SettleSeconds)

# —— 已有实例在运行则拒绝执行:另起实例会被单实例互斥体弹掉,采样也会污染
#    用户正在用的实例的数据。
if (Test-QkaRunning) {
    Write-Host "" 
    Write-Host "检测到 QKA 已在运行(单实例互斥体/进程),拒绝启动第二份实例。" -ForegroundColor Yellow
    Write-Host "采样会干扰正在使用的实例;请先退出当前 QKA(托盘右键→退出)再运行本脚本。" -ForegroundColor Yellow
    exit 1
}

$qkaProc = $null
$samples = @()          # 扁平的采样记录列表(每条记录带 Sample 序号)
$firstSeenOrder = @()   # 首次发现顺序的 pid 列表(用于明细表排序/renderer 编号)

try {
    # -----------------------------------------------------------------------
    # 启动 QKA。
    # 工作目录说明:开发构建的 QKA 按「相对当前目录的 adapters/」解析适配器,
    # exe 放在仓库 dist\build 下时必须以仓库根为工作目录,否则启动即以退出码
    # 2 失败(open adapters: 系统找不到指定的文件)。已安装的自包含单文件按
    # exe 所在目录启动即可;可用 -WorkingDir 覆盖默认推断。
    # -----------------------------------------------------------------------
    $workDir = Split-Path -Parent $exe
    $repoRoot = Split-Path -Parent $PSScriptRoot
    if ($exe.StartsWith((Join-Path $repoRoot "dist\build"), [System.StringComparison]::OrdinalIgnoreCase)) {
        $workDir = $repoRoot
    }
    if ($WorkingDir -ne "") { $workDir = $WorkingDir }
    Write-Host ("工作目录  : {0}" -f $workDir)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    if ($LaunchArgs -ne "") { $psi.Arguments = $LaunchArgs }
    $psi.WorkingDirectory = $workDir
    $psi.UseShellExecute = $true
    $qkaProc = [System.Diagnostics.Process]::Start($psi)
    Write-Host ("已启动 QKA,PID = {0}" -f $qkaProc.Id)

    # -----------------------------------------------------------------------
    # 就绪等待:出现至少 1 个 renderer(说明页面真正加载起来了),超时 45s
    # -----------------------------------------------------------------------
    Write-Host "等待窗口/WebView2 子进程就绪..." -NoNewline
    $ready = $false
    $deadline = (Get-Date).AddSeconds(45)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 700
        Write-Host "." -NoNewline
        if ($qkaProc.HasExited) { throw "QKA 启动后提前退出,退出码 $($qkaProc.ExitCode)(缺 WebView2 运行时?)" }
        $snap = Get-ProcessTreeSnapshot -RootPid $qkaProc.Id
        $wv = @($snap.Descendants | ForEach-Object { $snap.Procs[$_] } |
            Where-Object { $_.Name -eq "msedgewebview2.exe" })
        $renderers = @($wv | Where-Object { $_.CommandLine -match '--type=renderer' })
        if ($renderers.Count -ge 1) { $ready = $true; break }
    }
    Write-Host ""
    if (-not $ready) {
        Write-Host "警告:45s 内未见 renderer 子进程,仍继续采样(窗口可能仍在加载)。" -ForegroundColor Yellow
    } else {
        Write-Host ("就绪:发现 {0} 个 WebView2 子进程。" -f (@($wv).Count))
    }

    # -----------------------------------------------------------------------
    # 静置:让首屏渲染/内嵌资源解压完成,再开始正式采样
    # -----------------------------------------------------------------------
    Write-Host ("静置 {0}s 后开始采样..." -f $SettleSeconds)
    Start-Sleep -Seconds $SettleSeconds

    # -----------------------------------------------------------------------
    # 周期采样
    # CPU 口径:平均占单核百分比((Δ累计CPU时间)/Δ墙上时间*100)。机器 N 核
    # 时,占整机百分比为该值/N(结尾会注明核数)。
    # -----------------------------------------------------------------------
    $prevCpu = @{}     # pid → 上次累计 CPU 秒
    $prevWall = $null  # 上次采样时间
    $sampleCount = [math]::Ceiling($Duration / $Interval)
    $cpuCores = [Environment]::ProcessorCount

    for ($i = 0; $i -lt $sampleCount; $i++) {
        $now = Get-Date
        $snapshot = Get-ProcessTreeSnapshot -RootPid $qkaProc.Id
        $records = @()

        # —— QKA 主进程 ——
        $mainPerf = Get-Process -Id $qkaProc.Id -ErrorAction SilentlyContinue
        if ($mainPerf) {
            $cpu = $mainPerf.TotalProcessorTime.TotalSeconds
            $cpuPct = $null
            if ($prevWall -and $prevCpu.ContainsKey($qkaProc.Id)) {
                $cpuPct = ($cpu - $prevCpu[$qkaProc.Id]) / ($now - $prevWall).TotalSeconds * 100
            }
            $prevCpu[$qkaProc.Id] = $cpu
            $records += [pscustomobject]@{
                Sample    = $i
                Time      = $now
                Pid       = $qkaProc.Id
                Kind      = "qka"
                Name      = "$procName.exe"
                Role      = "QKA 主进程"
                Window    = "-"
                WorkingSetMB   = $mainPerf.WorkingSet64 / 1MB
                PrivateMB      = $mainPerf.PrivateMemorySize64 / 1MB
                CpuPctSingleCore = $cpuPct
            }
        }

        # —— 全部后代进程(msedgewebview2 + 其它如内嵌运行时) ——
        foreach ($pid2 in ($snapshot.Descendants | Sort-Object)) {
            $cim = $snapshot.Procs[$pid2]
            $perf = Get-Process -Id $pid2 -ErrorAction SilentlyContinue
            if (-not $perf) { continue }   # 采样瞬间退出,跳过
            if ($firstSeenOrder -notcontains $pid2) { $firstSeenOrder += $pid2 }

            $kind = "other"
            $role = "QKA 其它子进程"
            if ($cim.Name -eq "msedgewebview2.exe") {
                $kind = "webview2"
                $role = Get-WebviewRole $cim.CommandLine
            }
            $cpu = $perf.TotalProcessorTime.TotalSeconds
            $cpuPct = $null
            if ($prevWall -and $prevCpu.ContainsKey($pid2)) {
                $cpuPct = ($cpu - $prevCpu[$pid2]) / ($now - $prevWall).TotalSeconds * 100
            }
            $prevCpu[$pid2] = $cpu
            $records += [pscustomobject]@{
                Sample    = $i
                Time      = $now
                Pid       = $pid2
                Kind      = $kind
                Name      = $cim.Name
                Role      = $role
                Window    = ""    # 稍后统一按创建时间编号填充
                # CreationDate 用于给 renderer 按真实创建顺序编号(首见顺序
                # 不等于创建顺序:首次采样时进程已全部存在,枚举序是乱的)
                CreationDate   = $cim.CreationDate
                WorkingSetMB   = $perf.WorkingSet64 / 1MB
                PrivateMB      = $perf.PrivateMemorySize64 / 1MB
                CpuPctSingleCore = $cpuPct
            }
        }
        $samples += $records
        $prevWall = $now

        $totalWs = ($records | Measure-Object WorkingSetMB -Sum).Sum
        Write-Host ("  [{0:d2}/{1}] {2:HH:mm:ss}  进程 {3} 个,合计 WS {4:N1} MB" -f `
            ($i + 1), $sampleCount, $now, $records.Count, $totalWs)
        if ($i -lt $sampleCount - 1) { Start-Sleep -Seconds $Interval }
    }

    # -----------------------------------------------------------------------
    # 窗口归属推断(尽力而为):
    # renderer 按 Win32_Process 的 CreationDate(真实创建时间)排序编号。
    # QKA 的窗口创建顺序是确定的事件驱动序列:
    #   ensureBar(ApplicationStarted 后立即)→ showMain(仅无参数启动形态)
    # 所以无参数形态:最早创建的 renderer≈bar,其次≈main;tray 形态只有 bar。
    # 注意这仍是启发式(WebView2 也可能为同窗口派生额外 renderer),且可用
    # 内存量交叉验证:bar 页面极小(约 50-60MB WS),main 五页签页面大得多
    # (百 MB 级)。归属不确定都会在输出中标注。panel 不点刘海不会创建,
    # 无法自动覆盖。
    # -----------------------------------------------------------------------
    $rendererSeq = @($samples | Where-Object { $_.Kind -eq "webview2" -and $_.Role -eq "renderer(渲染页面)" } |
        Group-Object Pid | ForEach-Object { $_.Group[0] } | Sort-Object CreationDate, Time)
    $modeNote = if ($LaunchArgs -match 'tray') { "tray" } else { "normal" }
    for ($r = 0; $r -lt $rendererSeq.Count; $r++) {
        $guess = ""
        if ($modeNote -eq "tray") {
            $guess = if ($r -eq 0) { "推测=bar(唯一窗口)" } else { "归属不确定(意外多出的渲染进程)" }
        } else {
            switch ($r) {
                0 { $guess = "推测=bar(最先创建)" }
                1 { $guess = "推测=main(其次创建)" }
                default { $guess = "归属不确定(超出预期窗口数)" }
            }
        }
        $rp = $rendererSeq[$r].Pid
        foreach ($rec in @($samples | Where-Object { $_.Pid -eq $rp })) { $rec.Window = $guess }
    }

    # -----------------------------------------------------------------------
    # 输出 1:每进程明细(按 pid 聚合出均值/峰值)
    # -----------------------------------------------------------------------
    Write-Host ""
    Write-Host "=== 每进程明细(按采样聚合) ===" -ForegroundColor Cyan
    $detail = $samples | Group-Object Pid | ForEach-Object {
        $g = $_.Group
        $cpuVals = @($g | Where-Object { $null -ne $_.CpuPctSingleCore } | ForEach-Object { $_.CpuPctSingleCore })
        [pscustomobject]@{
            "PID"     = $_.Name
            "进程"    = $g[0].Name
            "角色"    = $g[0].Role
            "窗口归属"= $(if ($g[0].Window) { $g[0].Window } else { "-" })
            "WS均值MB"   = [math]::Round(($g | Measure-Object WorkingSetMB -Average).Average, 1)
            "WS峰值MB"   = [math]::Round(($g | Measure-Object WorkingSetMB -Maximum).Maximum, 1)
            "Priv均值MB" = [math]::Round(($g | Measure-Object PrivateMB -Average).Average, 1)
            "Priv峰值MB" = [math]::Round(($g | Measure-Object PrivateMB -Maximum).Maximum, 1)
            "CPU均值%(单核)" = $(if ($cpuVals.Count) { [math]::Round(($cpuVals | Measure-Object -Average).Average, 2) } else { "-" })
        }
    }
    # 按进程首次出现顺序排列(主进程最前,其后按子进程创建先后)
    $detail = @($detail | Sort-Object { [array]::IndexOf($firstSeenOrder, [int]$_.PID) })
    $detail | Format-Table -AutoSize | Out-String -Width 240 | Write-Host

    # -----------------------------------------------------------------------
    # 输出 2:汇总
    # -----------------------------------------------------------------------
    $perSampleTotals = $samples | Group-Object -Property Sample | ForEach-Object {
        $recs = $_.Group
        $cpuVals = @($recs | Where-Object { $null -ne $_.CpuPctSingleCore } | ForEach-Object { $_.CpuPctSingleCore })
        [pscustomobject]@{
            采样     = ($recs[0].Sample + 1)
            时间     = $recs[0].Time.ToString("HH:mm:ss")
            "总WS(MB)"   = [math]::Round(($recs | Measure-Object WorkingSetMB -Sum).Sum, 1)
            "总Priv(MB)" = [math]::Round(($recs | Measure-Object PrivateMB -Sum).Sum, 1)
            "总CPU%(单核)" = [math]::Round(($cpuVals | Measure-Object -Sum).Sum, 2)
        }
    }
    Write-Host "=== 每次采样总量 ===" -ForegroundColor Cyan
    $perSampleTotals | Format-Table -AutoSize | Out-String -Width 200 | Write-Host

    $grandWsMean  = ($perSampleTotals | Measure-Object "总WS(MB)" -Average).Average
    $grandWsPeak  = ($perSampleTotals | Measure-Object "总WS(MB)" -Maximum).Maximum
    $grandPrivMean = ($perSampleTotals | Measure-Object "总Priv(MB)" -Average).Average
    $grandPrivPeak = ($perSampleTotals | Measure-Object "总Priv(MB)" -Maximum).Maximum
    $grandCpuMean = ($perSampleTotals | Measure-Object "总CPU%(单核)" -Average).Average
    Write-Host ("汇总:全部进程合计 WS 均值 {0:N1} MB / 峰值 {1:N1} MB;Priv 均值 {2:N1} MB / 峰值 {3:N1} MB;CPU 均值 {4:N2}%(单核,{5} 核)" -f `
        $grandWsMean, $grandWsPeak, $grandPrivMean, $grandPrivPeak, $grandCpuMean, $cpuCores)

    # -----------------------------------------------------------------------
    # 输出 3:可直接粘贴到 GitHub issue 的 markdown 表格
    # -----------------------------------------------------------------------
    if (-not $SkipMarkdown) {
        Write-Host ""
        Write-Host "=== GitHub issue 粘贴用 markdown(复制下面整块) ===" -ForegroundColor Green
        $md = @()
        $md += "**QKA Windows 窗口内存/CPU 实测**(`$LaunchArgs=$modeNote` 形态,采样 ${Duration}s / 间隔 ${Interval}s,${cpuCores} 核 Win10)"
        $md += ""
        $md += "| 进程 | 角色/窗口归属 | WS均值 | WS峰值 | Priv均值 | Priv峰值 | CPU%(单核) |"
        $md += "|---|---|---:|---:|---:|---:|---:|"
        foreach ($d in $detail) {
            $cpuCell = $d."CPU均值%(单核)"
            $md += ("| {0} (PID {1}) | {2}{3} | {4} | {5} | {6} | {7} | {8} |" -f `
                $d.进程, $d.PID, $d.角色, $(if ($d.窗口归属 -ne "-") { " / " + $d.窗口归属 } else { "" }), `
                $d."WS均值MB", $d."WS峰值MB", $d."Priv均值MB", $d."Priv峰值MB", $cpuCell)
        }
        $md += ("| **合计** | 全部进程 | **{0:N1}** | **{1:N1}** | **{2:N1}** | **{3:N1}** | **{4:N2}** |" -f `
            $grandWsMean, $grandWsPeak, $grandPrivMean, $grandPrivPeak, $grandCpuMean)
        $md += ""
        $md += "注:CPU% 为占单个核心的百分比(整机口径除以 $cpuCores);窗口归属按渲染进程创建顺序推断,标注「推测」;金额单位 MB。"
        $md -join "`n" | Write-Host
    }
}
finally {
    # -----------------------------------------------------------------------
    # 清理:温和关闭。只 Kill QKA 主进程,WebView2 子进程监测到宿主退出会
    # 自行退出;等待后仍有残留(归属 QKA 的)才兜底强杀。绝不碰其它应用的
    # msedgewebview2。
    # -----------------------------------------------------------------------
    Write-Host ""
    if ($qkaProc -and -not $qkaProc.HasExited) {
        Write-Host "清理:结束 QKA 主进程(子进程随之退出)..." -ForegroundColor Yellow
        try { $qkaProc.Kill() } catch { Write-Host "  Kill 失败: $($_.Exception.Message)" -ForegroundColor Red }
        try { $qkaProc.WaitForExit(10000) | Out-Null } catch {}
    }
    # 兜底:核对归属 QKA 树的残留子进程并结束(先按名字复核,防 PID 复用误杀)
    try {
        if ($qkaProc) {
            $snap = Get-ProcessTreeSnapshot -RootPid $qkaProc.Id
        } else {
            $snap = @{ Procs = @{}; Descendants = (New-Object System.Collections.Generic.HashSet[int]) }
        }
        foreach ($spid in $snap.Descendants) {
            $p = Get-Process -Id $spid -ErrorAction SilentlyContinue
            if ($p -and ($p.Name -eq "msedgewebview2" -or $p.Name -like "$procName*")) {
                Write-Host ("  兜底结束残留子进程 {0} (PID {1})" -f $p.Name, $spid) -ForegroundColor Yellow
                try { Stop-Process -Id $spid -Force -ErrorAction SilentlyContinue } catch {}
            }
        }
    } catch {}
    Start-Sleep -Seconds 2
    # 最终核对:本机不允许留下任何 QKA 主进程
    $left = Get-Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -eq "QKA" -or $_.Name -like "QKA-windows-*" -or $_.Name -like "$procName*" }
    if ($left) {
        Write-Host "警告:仍有 QKA 相关进程残留,强制结束:" -ForegroundColor Red
        $left | ForEach-Object { Write-Host ("  {0} (PID {1})" -f $_.Name, $_.Id); try { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue } catch {} }
    } else {
        Write-Host "清理完成:无 QKA 进程残留。" -ForegroundColor Green
    }
}
