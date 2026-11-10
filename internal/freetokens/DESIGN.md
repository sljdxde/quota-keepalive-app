# 免费额度（AIHOT · Token 羊毛）接入设计

## 目标
把 [AIHOT · Token 羊毛](https://aihot.news/free-tokens) 的官方免费额度接进 QKA 主窗口，
并按本机供应商/账号状态给出**可行动的**三档分类，而不是一张静态清单。

## 数据
- 来源：`https://aihot.news/free-tokens.data`（AIHOT 公开数据）。
- 编码：turbo-stream v2 flatten（JSON 数组、下标即引用 id、负数常量当 null）。
  `internal/freetokens/turbo.go` 是零依赖解码器；**不要**换 turbo-stream v3（会静默返回扁平数组）。
- 线上 `serverNow` / `expiresAt` 是毫秒时间戳，解码时统一归一化成 RFC3339。
- 折算：`today_yuan = round(Σ yuan where counted && yuan != null)`；`yuan` 为 null 的额度
  （多为"不折钱"）不计入。

## 分档
对每条 deal：
1. 厂商不在 `internal/freetokens/mapping.go`（= QKA 没有内置插件）→ **暂无插件**。
2. 映射命中但运行时没有注册该 provider（插件版本差异/加载失败）→ **有插件待配置**。
3. 映射命中、但 provider 在「应用管理」里**未启用** → **有插件待配置**（按钮去应用管理）。
   即使插件已发现出账号，也不能算已配置可用——页面口径必须与「应用管理」的启用开关一致。
4. 映射命中、provider 已启用且已登录/有账号 → **已配置可用**；插件支持签到时按钮为
   「去签到」。
5. 映射命中、已启用但没账号 → **有插件待配置**（按钮去登录）。

图标优先取随包内置的品牌 SVG（`assets/provider-icons/`），其次取插件声明的
`magpie.icon`（data: URI 或 https 地址），两者都没有才回落厂商首字。

映射只登记 QKA **已 vendor 的插件**（`provider-plugins/`），不宣称无法执行的安装路径；
`mapping_test.go` 会在 CI 里核对每个映射的 npm 包确实内置。

## 服务与缓存
`internal/freetokens.Service`：10 分钟内存缓存 + 并发保护；拉取失败时返回上次快照并带
`stale/error`，页面显示"上一次数据 + 刷新失败"而不是空白。绑定入口
`BoundService.FreeTokensGet(force)`，`force=true` 绕过缓存（页面「刷新」按钮）。

## 隐私与合规
- 只读公开数据；不读取任何密钥；诚实 UA（`QuotaKeepalive/...`）。
- 不在后台轮询；不落盘、不镜像、不做可导出数据集。
- 页面注明数据来自 AIHOT，额度与截止时间以官方为准；个人使用，公开分发前按 AIHOT
  条款取得授权。

## 前端
- 主窗口第六个页签「免费额度」：三档筛选 + 搜索 + 今日折算 + 行内行动按钮。
- 纯展示逻辑在 `internal/gui/frontend/assets/free-tokens.js`，由 `node --test` 覆盖
  （分档/kind/金额/时间/筛选/摘要文案）。
- 外部链接经 `BoundService.OpenURL`（仅 http/https）交给系统浏览器打开。
