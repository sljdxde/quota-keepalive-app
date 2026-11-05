# Quota Keepalive（桌面应用）

本地 Agent 的额度签到与 Token 速度仪表：一个 Go + Wails v3 的桌面应用，macOS 常驻菜单栏，Windows 常驻托盘并提供屏幕顶栏入口。

> 供应商插件（Magpie 用的 npm 包 `quota-keepalive`）是另一个项目：
> [sljdxde/quota-keepalive-magpie-plugin](https://github.com/sljdxde/quota-keepalive-magpie-plugin)。
> 本仓库只包含桌面应用本体，两者独立发版。

## 功能

- **签到管理**：随包 7 个供应商插件（WorkBuddy / CodeArts / MiniMax / Qoder / Trae / ZCode / OpenCode Zen Free，共 13 个供应商目标）→ 账号两级折叠；按账号控制自动签到、查看今日状态与额度；官方浏览器登录，账号只在只读验证通过后创建，签到结果写入后页面自动刷新
- **应用管理**：按供应商查看账号与模型数量、启用状态；开关只影响该供应商下的全部账号
- **Token 速度**：实时吞吐与今日总量，覆盖 9 个本地 Agent 数据源（Codex / Claude / Qoder / Qoder CN / WorkBuddy / CodeBuddy / ZCode / OpenCode / DeepSeek Harness）；按对话聚合，子代理会话并入所属会话，会话行显示会话标题
- **性能模式**：自动识别软件渲染并按需降级毛玻璃、阴影与 3D 宠物开销，也可在设置里强制开启/关闭
- **模型测试**：批量多选 + 全选/反选，结论以绿/红点标在模型名上，失败原因在悬停提示里
- **3D 宠物**：头颈跟随鼠标；点击或按住蓄力投出精灵球捕获伙伴（蓄到甜点区命中率最高、宠物会躲避），捕获后点精灵球放出；技能按 Token 档位自动释放，档位越高粒子/冲击环/镜头冲击越强
- **日志**：滚动保留 48 小时，写入口统一脱敏，不落盘密码、令牌与提示词
- **自动执行**：内置 launchd / 任务计划程序调度，按间隔巡检

## 安装

从 [Releases](https://github.com/sljdxde/quota-keepalive-app/releases) 下载：

| 平台 | 产物 | 说明 |
| --- | --- | --- |
| macOS（Apple Silicon） | `QKA-<version>-arm64.dmg` | 拖入「应用程序」即可（ad-hoc 签名；若系统拦截，右键「打开」或在「隐私与安全性」里放行） |
| Windows x64（Intel / AMD） | `QKA-<version>-windows-amd64.exe` | 单个 exe：双击进入安装向导，选安装路径后自动装完 |
| Windows ARM64（骁龙 X 等） | `QKA-<version>-windows-arm64.exe` | 同上，仅架构不同 |

Windows 版是**单文件自包含安装程序**：下载到的就是一个 exe，双击进入安装向导——选择安装路径（默认 `%LOCALAPPDATA%\Programs\Quota Keepalive`，不需要管理员权限），之后自动完成复制程序、解压运行时（Bun / `adapters/` / `provider-plugins/` 都内嵌在 exe 里，解压到 `%LOCALAPPDATA%\QKA\runtime\<version>\`）、创建开始菜单快捷方式与 `HKCU\...\App Paths\QKA.exe`（Win+R 输 `QKA` 可直接启动）。安装完成后可以立即启动 QKA，下次双击安装目录里的 QKA.exe 直接进托盘，不再弹向导。

Windows 上双击启动会直接打开主窗口（macOS 保持菜单栏形态、不弹窗）；关闭主窗口只是隐藏，之后左键点托盘出面板、右键菜单「打开主窗口」都能再打开。重复双击 exe 不会起第二个实例，只会把已有实例的主窗口唤到前台。

Windows 上有一个**常驻刘海**：主屏左上角的独立置顶小条，等价于 macOS 菜单栏上的 QKA 图标，只显示 Q 图标和实时 tok/s。左键点它弹出和 mac 上同一个吸附面板（面板贴在刘海正下方），**右键出菜单**（打开主窗口 / 设置 / 退出）。刘海随应用常驻，关闭主窗口也不消失。

主窗口保留系统原生标题栏（关闭/最小化/最大化都交给 Windows）。

Windows 需要 WebView2 运行时：Win11 自带，Win10 可能没有；缺失时程序会弹窗说明并引导安装，而不是静默退出。选错架构会提示「此应用无法在你的电脑上运行」——Intel / AMD 机器必须用 **amd64**，只有 ARM 机器才用 **arm64**。

自更新同样是单文件语义：检查到新版本后下载新的 exe，退出旧进程、把旧 exe 改名备份、放入新 exe 并重启；新版本 5 秒内没起来就回滚到旧版。解压出来的运行时放在 `%LOCALAPPDATA%\QKA\runtime\<version>\`，删掉 `%LOCALAPPDATA%\QKA` 等于重置配置与运行时缓存。

## 首次使用

1. **启动**：Windows 装完即启动、直接进主窗口（之后双击 exe 直达托盘）；macOS 常驻菜单栏，点 Q 图标出面板
2. **添加账号**：在「签到管理」里选一个供应商目标，走官方浏览器登录；账号只在只读验证通过后才会创建
3. **开启自动签到**：按账号单独控制，今日状态与剩余额度直接显示在账号行上
4. **Token 速度**：无需配置，自动读取本机 Codex / Claude / ZCode 等本地 Agent 的会话记录并按对话聚合

## 日志与排障

各平台的日志都落在配置目录下，默认 info 级别、按天轮转、保留 7 天：

- Windows：`%LOCALAPPDATA%\QKA\logs\qka-YYYYMMDD.log`
- macOS / Linux：`~/.qka/logs/qka-YYYYMMDD.log`

排查时用 `QKA_LOG_LEVEL=debug` 启动（日志目录可用 `QKA_LOG_DIR` 覆盖）。程序 panic 会额外写 `crash-<时间>.log`（含 goroutine 栈），Windows 上同时弹窗显示日志目录。日志写入口统一脱敏，不落令牌、账号与提示词明文。

## 从源码构建

需要 Go 1.27+；macOS 原生构建需要 CGO 与 Xcode 命令行工具。

```sh
make build-darwin-arm64     # macOS GUI
make release-macos          # macOS DMG
make build-windows          # Windows 单文件 exe（内嵌运行时，可直接安装/运行）
make release-windows        # Windows 发行物 + SHA256SUMS（amd64 + arm64）
```

Windows 的 GUI 产物必须是自包含的：构建时先用 `go run ./build/embedassets` 把 Bun、
`adapters/` 与 `provider-plugins/` 压成 `internal/runtimebundle/assets/` 下的 zstd 资源，
再用 `-tags embeddedruntime` 交叉编译。漏掉任何一步都会得到「能弹安装向导、却在
解压运行时一步必然失败」的 exe，所以 `make build-windows` 与 `make release-windows`
统一走 `scripts/build-windows-exe.sh`（Bun 缓存 + 构建锁 + 内嵌 + `build/verifype`）。
资源目录是构建产物，不进版本库；首次构建会下载一次 Bun（约 53MB/架构），之后复用缓存。

版本号来自仓库根 `VERSION` 文件（单一来源，`Makefile` 与两个发行脚本共用）；
发布时也可显式指定，例如 `VERSION=0.1.24 make release-windows`。

## 开发

```sh
go test ./...               # 单元测试
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
node --test internal/gui/frontend-js-tests/*.test.mjs   # 前端 JS 单测
go vet ./... && gofmt -l .
```

目标保持 100% 语句覆盖率，当前全量约 98.6%：缺口集中在仅 Windows 生效的分支（安装器、WebView2、窗口 chrome）与少量平台桩。测试优先使用真实行为（真实 SQLite、真实 zstd 会话文件、真实子进程），不为覆盖率造 mock；确实不可达的分支直接删除而不是硬凑。

目录速览：

| 路径 | 内容 |
| --- | --- |
| `cmd/qka` | 程序入口（GUI / CLI 同一二进制） |
| `internal/gui` | Wails 绑定、窗口与内嵌前端（`frontend/`） |
| `internal/providers` | 供应商、账号、模型测试与请求风控 |
| `internal/tokenspeed` | Token 统计与速率测量 |
| `internal/workbuddy` | WorkBuddy CN 官方登录协议移植 |
| `internal/providerruntime` | bun 托管的适配器运行时 |
| `internal/applog` | 落盘日志：按天轮转、写入统一脱敏 |
| `internal/installer`、`internal/runtimebundle`、`internal/selfinstall` | Windows 单文件安装器与内嵌运行时 |
| `internal/settings`、`internal/schedule`、`internal/scheduler` | 设置项与自动巡检调度 |
| `adapters/`、`provider-plugins/` | 运行时适配器，打包时必须随程序一起发布 |

## 发布

打 `v*` tag 后由 CI 自动发布：`.github/workflows/windows.yml` 做 Windows 全量编译 + CLI 冒烟，`.github/workflows/release.yml` 打包 Windows 单文件 exe（amd64/arm64）与 macOS DMG（arm64/amd64），生成 feed.json 与 SHA256SUMS，并自动创建 GitHub Release。

## 已知限制

- **Windows GUI 未经真机验收**：CI 覆盖「全部包与测试在 Windows 编译通过」与「CLI 真实可执行」；WebView2 渲染、托盘、常驻刘海、安装向导与自更新仍需实机确认
- **反馈功能已移除**：内置反馈服务端（Cloudflare Worker）不可达，且 `*.workers.dev` 在部分地区被网络阻断；相关代码与 CI 探活已整体摘除，需要时从历史提交恢复
- **macOS 包未公证**：DMG 使用 ad-hoc 签名，换机或从网上下载后首次打开可能被 Gatekeeper 拦截，需右键「打开」或在「隐私与安全性」放行
- Qoder 上游 token 字段恒为 0；OpenCode 无首 token 时间戳，不出实测速率
- 保活（真实对话续期）功能全局停用

## 许可

见 [LICENSE](LICENSE)。