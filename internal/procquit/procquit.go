// Package procquit 提供「让正在运行的 QKA 退出」的跨进程通道。
//
// 升级场景：用户在下载目录双击新版 exe，安装目录里的旧版 QKA 可能还开着。
// Windows 不允许覆盖运行中的 exe，但允许改名——旧版自更新就是这么做的。
// 安装向导的做法是：先礼貌地请旧实例自己退出（命名事件），等它释放文件；
// 对没有实现该通道的旧构建，再按可执行文件路径找到进程并结束它。
package procquit

// QuitEventName 是 GUI 实例监听「请退出」的命名事件。Local\ 前缀让它按登录
// 会话隔离：只影响当前用户会话里的 QKA。
const QuitEventName = `Local\QuotaKeepalive.GUI.Quit`
