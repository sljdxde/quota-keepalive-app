package app

import (
	"qka/internal/selfupdate"
	"qka/internal/settings"
)

func (a *App) updateService() *selfupdate.Service {
	return selfupdate.New(a.cfg.Version, a.cfg.ConfigDir)
}

// applyUpdateMirror 按设置里的「国内镜像源」开关切换更新前缀。每次检查/安装
// 前都重读设置，保证改完开关立即生效，不需要重启。
func applyUpdateMirror(svc *selfupdate.Service, st *settings.Settings) {
	if svc == nil {
		return
	}
	svc.MirrorPrefix = ""
	if st != nil && st.Get("update_domestic_mirror") {
		svc.MirrorPrefix = selfupdate.DomesticMirror
	}
}
