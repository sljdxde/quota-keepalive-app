# Quota Keepalive（桌面应用）

本地 Agent 的额度签到与 Token 速度仪表：一个 Go + Wails v3 的桌面应用，macOS 常驻菜单栏、Windows 常驻托盘。

> 供应商插件（Magpie 用的 npm 包 `quota-keepalive`）是另一个项目：
> [sljdxde/quota-keepalive-magpie-plugin](https://github.com/sljdxde/quota-keepalive-magpie-plugin)。
> 本仓库只包含桌面应用本体，两者独立发版。

## 功能

- **签到管理**：供应商 → 账号两级折叠；按账号控制自动签到、查看今日状态与额度；WorkBuddy CN 走官方浏览器登录，账号只在只读验证通过后创建
- **Token 速度**：实时吞吐与今日总量，覆盖 9 个本地 Agent 数据源（Codex / Claude / Qoder / Qoder CN / WorkBuddy / CodeBuddy / ZCode / OpenCode / DeepSeek Harness）；会话行显示项目名而不是哈希
- **模型测试**：批量多选 + 全选/反选，结论以绿/红点标在模型名上，失败原因在悬停提示里
- **3D 宠物**：材质归一与骨骼跟随，短按放小技能、长按蓄力放大招
- **日志**：滚动保留 48 小时，写入口统一脱敏，不落盘密码、令牌与提示词
- **自动执行**：内置 launchd / 任务计划程序调度，按间隔巡检

## 安装

从 [Releases](https://github.com/sljdxde/quota-keepalive-app/releases) 下载：

| 平台 | 产物 | 说明 |
| --- | --- | --- |
| macOS（Apple Silicon） | `QKA-<version>-arm64.dmg` | 拖入「应用程序」即可（已签名并公证） |
| Windows x64（Intel / AMD） | `QKA-<version>-windows-amd64.exe` | 单个 exe：双击进入安装向导，选安装路径后自动装完 |
| Windows ARM64（骁龙 X 等） | `QKA-<version>-windows-arm64.exe` | 同上，仅架构不同 |

Windows 版是**单文件自包含安装程序**：下载到的就是一个 exe，双击进入安装向导——选择安装路径（默认 `%LOCALAPPDATA%\Programs\Quota Keepalive`，不需要管理员权限），之后自动完成复制程序、解压运行时（Bun / `adapters/` / `provider-plugins/` 都内嵌在 exe 里，解压到 `%LOCALAPPDATA%\QKA\runtime\<version>\`）、创建开始菜单快捷方式与 `HKCU\...\App Paths\QKA.exe`（Win+R 输 `QKA` 可直接启动）。安装完成后可以立即启动 QKA，下次双击安装目录里的 QKA.exe 直接进托盘，不再弹向导。

Windows 上双击启动会直接打开主窗口（macOS 保持菜单栏形态、不弹窗）；关闭主窗口只是隐藏，之后左键点托盘出面板、右键菜单「打开主窗口」都能再打开。重复双击 exe 不会起第二个实例，只会把已有实例的主窗口唤到前台。

Windows 上有一个**常驻刘海**：主屏左上角的独立置顶小条，等价于 macOS 菜单栏上的 QKA 图标，只显示 Q 图标和实时 tok/s。左键点它弹出和 mac 上同一个吸附面板（面板贴在刘海正下方），**右键出菜单**（打开主窗口 / 设置 / 退出）。刘海随应用常驻，关闭主窗口也不消失。

主窗口保留系统原生标题栏（关闭/最小化/最大化都交给 Windows）。

Windows 需要 WebView2 运行时：Win11 自带，Win10 可能没有；缺失时程序会弹窗说明并引导安装，而不是静默退出。选错架构会提示「此应用无法在你的电脑上运行」——Intel / AMD 机器必须用 **amd64**，只有 ARM 机器才用 **arm64**。

自更新同样是单文件语义：检查到新版本后下载新的 exe，退出旧进程、把旧 exe 改名备份、放入新 exe 并重启；新版本 5 秒内没起来就回滚到旧版。解压出来的运行时放在 `%LOCALAPPDATA%\QKA\runtime\<version>\`，删掉 `%LOCALAPPDATA%\QKA` 等于重置配置与运行时缓存。

## 日志与排障

两个平台的日志都落在配置目录下，默认 info 级别、按天轮转、保留 7 天：

- Windows：`%LOCALAPPDATA%\QKA\logs\qka-YYYYMMDD.log`
- macOS / Linux：`~/.qka/logs/qka-YYYYMMDD.log`

排查时用 `QKA_LOG_LEVEL=debug` 启动（日志目录可用 `QKA_LOG_DIR` 覆盖）。程序 panic 会额外写 `crash-<时间>.log`（含 goroutine 栈），Windows 上同时弹窗显示日志目录。日志写入口统一脱敏，不落令牌、账号与提示词明文。

## 从源码构建

需要 Go 1.27+；macOS 原生构建需要 CGO 与 Xcode 命令行工具。

```sh
make build-darwin-arm64     # macOS GUI
make release-macos          # macOS DMG
make release-windows        # Windows 单文件 exe（amd64 + arm64，内嵌运行时）
```

`make release-windows` 会先用 `go run ./build/embedassets` 把 Bun、`adapters/` 与
`provider-plugins/` 压成 `internal/runtimebundle/assets/` 下的 zstd 资源，再用
`-tags embeddedruntime` 交叉编译出 `dist/QKA-<version>-windows-<arch>.exe`；资源目录
是构建产物，不进版本库。

版本号来自 `git describe --tags`，发布时也可显式指定，例如
`VERSION=0.1.16 make release-windows`。

## 开发

```sh
go test ./...               # 单元测试
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
node --test internal/gui/frontend-js-tests/*.test.mjs   # 前端 JS 单测
go vet ./... && gofmt -l .
```

仓库保持 100% 语句覆盖率。测试优先使用真实行为（真实 SQLite、真实 zstd 会话文件、真实子进程），不为覆盖率造 mock；确实不可达的分支直接删除而不是硬凑。

目录速览：

| 路径 | 内容 |
| --- | --- |
| `cmd/qka` | 程序入口（GUI / CLI 同一二进制） |
| `internal/gui` | Wails 绑定、窗口与内嵌前端（`frontend/`） |
| `internal/providers` | 供应商、账号、模型测试与请求风控 |
| `internal/tokenspeed` | Token 统计与速率测量 |
| `internal/workbuddy` | WorkBuddy CN 官方登录协议移植 |
| `internal/providerruntime` | bun 托管的适配器运行时 |
| `adapters/`、`provider-plugins/` | 运行时适配器，打包时必须随程序一起发布 |

## 发布

打 tag 后由 `.github/workflows/windows.yml` 与 `feedback-endpoint.yml` 自动校验（Windows 全量编译 + CLI 冒烟、反馈端点连通性）；DMG 与 Windows 包由 `scripts/` 下脚本在本机产出，再手工上传到 Release。

## 已知限制

- **Windows GUI 未经真机验收**：CI 覆盖「全部包与测试在 Windows 编译通过」与「CLI 真实可执行」，WebView2 渲染、托盘、任务计划程序仍需实机确认
- **反馈入口默认隐藏**：内置反馈服务端尚未部署，提交会失败；功能代码保留，服务可用后由 `FEEDBACK_AVAILABLE` 开关恢复
- Qoder 上游 token 字段恒为 0；OpenCode 无首 token 时间戳，不出实测速率
- 保活（真实对话续期）功能全局停用

## 许可

见 [LICENSE](LICENSE)。