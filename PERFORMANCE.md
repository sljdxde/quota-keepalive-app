# 性能与资源基线

面向维护者的性能基线、已落地的优化与回归方法。产品功能说明见 [README.md](README.md)。

> 测量环境：macOS（Apple M4，2026-11-13）。Windows 部分为制品与代码推断，
> 无真机采样，按置信度标注（A = 制品/代码硬证据，C = 经验值）。

## 一句话结论

成本不在 Go 业务逻辑，而在三件事：**常驻的外部运行时进程**、**永不销毁的 WebView**、
**每次全量做的周期任务**。Go 侧的热点（快照序列化、状态投影、写库）单次成本都在
微秒级，占比可忽略。

## 基线

| 指标 | 优化前 | 现状 | 备注 |
| --- | --- | --- | --- |
| macOS 空闲 CPU | 3.2%（90s 窗口） | 待复测 | 用户态时间占比，非功耗 |
| macOS 主进程内存 | 65-72MB | 待复测 | 不含 WebContent 等共享 XPC 服务 |
| 常驻 Bun 进程 | 4 个，合计 54-87MB | 0 个（按需启动） | 本机 8 条数据源全走 provider 插件 |
| macOS 二进制 | 33.6MB | 25.1MB | `-s -w` 净 -25.2%（含内嵌字体 +0.41MB） |
| Windows exe amd64 | 80.7MB | 76.7MB | zstd L3→L11，净 -5.0%（含字体 +0.41MB） |
| Windows exe arm64 | 74.2MB | 70.4MB | 净 -5.1% |
| `~/.qka` 磁盘占用 | 64MB（其中 59MB 是历史安装包） | 启动自动清理 | 见 `internal/selfupdate` |

## 三个根因

1. **常驻外部运行时**：随包 4 个 Bun adapter 进程在没有任何调用时也常驻，单个
   RSS 8.2-13.8MB。Windows 的 80MB 里 67.6% 是内嵌 Bun。→ 改为按需启动。
2. **永不销毁的 WebView**：窗口只 `Hide` 不 `Destroy`，每开过一个窗口就多一份
   常驻内存与渲染线程。**已决定不修**（`Destroy` 会破坏恢复语义，收益不抵风险）。
3. **全量周期任务**：每 2s 无条件 open 22 个会话文件、每 3s 全量 `WalkDir`
   1600 个文件、每次刷新 UI 写 22 个事务。→ 分别用 stat 快路径、放宽间隔、
   条件 upsert 解决。

## 已落地的优化

| 改动 | 实测收益 |
| --- | --- |
| adapter 宿主按需启动 | 常驻 4 个 Bun 进程归零，约 -33-55MB |
| orb 动画静止自停 + 隐藏停帧 | 窗口开过后不再 60fps 空转 |
| 3D 资源按需加载 | 首载少约 2.5MB（three.js + GLB + Draco） |
| macOS 构建 `-s -w` | 二进制净 -8.5MB（-25.2%，已扣除字体增量），DMG -7.6MB |
| Windows zstd 提到最高档 | exe 净 -4.0MB（amd64）/ -3.8MB（arm64） |
| 更新残留清理 | 每次更新回收 61-116MB |
| tokenspeed stat 快路径 | 空闲态 22 次/2s 的 open 降到约 0 |
| 目录扫描 3s → 15s（可配置） | 单核 0.52% → 0.10%，省 0.42% |
| providers/accounts 条件 upsert | 零变化不再发 UPDATE，消除 WAL 高水位 |

## 量化后判定「不值得做」

| 项 | 实测 | 结论 |
| --- | --- | --- |
| 快照 `json.Marshal` 换廉价指纹 | 17.3µs / 次，每 500ms 一次 = 单核 0.003% | 不做 |
| 状态投影 | 239µs / 10s = 0.002% | 不做 |
| stat 快路径本身开销 | 67µs / 2s = 0.003% | 已实现，成本可忽略 |

## 明确不做

- **窗口关闭 `Destroy`**：每窗约 +80-120MB，但会破坏「关闭后快速重开」的恢复语义，
  用户已拍板不修。
- **UPX 压缩**：上限约 -15MB，但杀软误报与启动解压开销不划算。
- **adapter 多路复用到一个进程**：省内存，但牺牲崩溃隔离。
- **Bun `-baseline` 变体**：同尺寸，无收益。

## 资源权衡记录

- **内嵌 Noto Sans SC 可变字体（+410KB）**：换取 Windows 上任意字重精确渲染。
  Windows 系统中文只有 400/700 两档，界面里 87 处非标准字重会被向上取整成 700，
  整屏偏粗。字体按界面实际用字子集化，不是完整字库。

## 回归方法

```sh
make bench    # 快照指纹、状态投影、stat 快路径、upsert 零变化
go test ./... # 含性能回归用例（见下）
```

已纳入单测的性能守护：

- `internal/tokenspeed/stat_fastpath_test.go`：未变化的会话文件不 open
- `internal/providers/upsert_condition_test.go`、`internal/accounts/upsert_condition_test.go`：
  零变化时 `total_changes()` 为 0
- `internal/app/adapter_lazy_perf_test.go`：Build 阶段 spawn 次数为 0
- `internal/gui/font_embed_test.go`：字体已 embed、字体栈首位是内置字体
- `internal/gui/frontend-js-tests/orb-agent-speed.test.mjs`：静止判定
- `internal/gui/frontend-js-tests/lazy-3d.test.mjs`：3D 不静态 import

CI 额外校验：`scripts/subset-font.py --check` 确认字体子集覆盖界面全部用字
（见 `.github/workflows/macos.yml`）。

## 测量边界

- 未跑 `powermetrics`（需 sudo）：本文件的 CPU% 是用户态时间占比，**不等于功耗**。
- 未跑 `footprint -p`：WebKit 的 WebContent/GPU/Networking 是共享 XPC 服务，
  无法精确归属，真实占用高于主进程数字。
- **Windows 无真机数据**：内存与首启耗时在拿到真机采样前不要对外承诺数字。
