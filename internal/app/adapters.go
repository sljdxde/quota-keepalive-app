package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"qka/internal/adapterhost"
	"qka/internal/adapterwiring"
	"qka/internal/capabilities"
	"qka/internal/registry"
)

// discoverAdapters 发现 AdaptersRoot 下的适配器目录并只加载 manifest，不启动进程。
// 缺 manifest 的目录（如 _template、node_modules）跳过；进程在首次使用时按需启动。
func (a *App) discoverAdapters() error {
	entries, err := os.ReadDir(a.cfg.AdaptersRoot)
	if err != nil {
		return err
	}

	var dirs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && !strings.HasPrefix(name, "_") && name != "node_modules" {
			dirs = append(dirs, filepath.Join(a.cfg.AdaptersRoot, name))
		}
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		manifest, err := adapterhost.LoadManifest(dir)
		if err != nil {
			continue // 不是有效适配器目录，跳过
		}
		a.loaded = append(a.loaded, Loaded{Manifest: manifest, Dir: dir})
	}
	return nil
}

// hostFor 返回 target 对应的适配器宿主。首次调用时按记录目录启动并缓存；
// 后续调用复用同一实例。启动失败记录到 Loaded.LoadErr 并直接返回错误。
func (a *App) hostFor(ctx context.Context, target string) (*adapterhost.Host, error) {
	a.hostsMu.Lock()
	defer a.hostsMu.Unlock()

	if host, ok := a.hosts[target]; ok && host != nil {
		return host, nil
	}

	idx := -1
	for i := range a.loaded {
		if a.loaded[i].Manifest.ID == target {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("目标适配器未运行: %s", target)
	}
	if err := a.loaded[idx].LoadErr; err != nil {
		return nil, err
	}

	host, err := a.launchOne(ctx, a.loaded[idx].Dir, a.loaded[idx].Manifest)
	if err != nil {
		a.loaded[idx].LoadErr = err
		return nil, err
	}
	if a.hosts == nil {
		a.hosts = map[string]*adapterhost.Host{}
	}
	a.hosts[target] = host
	a.loaded[idx].Host = host
	return host, nil
}

// launchOne 为单个适配器装配反向钩子（http.fetch / log.write）并完成握手。
func (a *App) launchOne(ctx context.Context, dir string, manifest adapterhost.Manifest) (*adapterhost.Host, error) {
	mux := adapterwiring.NewHookMux()

	baseURL := a.cfg.BaseURLs[manifest.ID]
	if baseURL != "" {
		mux.Init["baseURL"] = baseURL
	}

	// 出站白名单：显式 AllowedHosts + BaseURLs 的主机。
	hosts := append([]string{}, a.cfg.AllowedHosts...)
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	allowed := capabilities.AllowedHostsFromManifest(hosts)
	grant := capabilities.GrantFromNeeds(manifest.Needs)
	fetch := capabilities.NewHTTPFetch(allowed, grant)

	adapterwiring.RegisterHTTPFetch(mux, fetch)
	if a.store != nil {
		adapterwiring.RegisterLogWrite(mux, a.store.DB())
	}

	// 进程生命周期不跟随触发它的请求：仅 initialize 握手沿用调用方超时预算。
	return adapterhost.LaunchWith(context.WithoutCancel(ctx), a.cfg.BunPath, dir, mux)
}

// registryAdapters 把加载结果转为 registry 视图输入；仅启动失败的适配器标记为禁用。
// Host 为空属于正常的懒加载状态，不代表禁用。
func (a *App) registryAdapters() []registry.LoadedAdapter {
	loaded := a.loadedSnapshot()
	out := make([]registry.LoadedAdapter, 0, len(loaded))
	for _, l := range loaded {
		la := registry.LoadedAdapter{Manifest: l.Manifest}
		if l.LoadErr != nil {
			la.Disabled = true
		}
		out = append(out, la)
	}
	return out
}

// capabilitiesFor 从适配器声明的能力推导目标操作（不硬编码产品清单）。
// 「每日活跃」只走 provider 插件通道，不依赖本机 Agent 客户端，所以适配器
// 路径永远只暴露 checkin。
func (a *App) capabilitiesFor(string) []string {
	return []string{"checkin"}
}
