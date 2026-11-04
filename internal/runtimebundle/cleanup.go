// cleanup.go 处理 runtime/ 下历史版本目录的清理：升级会不断留下新的
// <version>/ 完整副本（单份约 100MB），旧目录若永不删除，磁盘占用会随
// 升级次数线性增长（见 issue #4）。
//
// 清理发生在每次解压完成后、以及启动命中缓存时（均由 ensureWith 触发），
// 规则是按版本号排序最多保留「当前 + 上一个版本」两份，给回滚留一条后路。
// 清理永远服从主流程：任何失败（目录被占用、读取失败）都只记日志并跳过，
// 下次启动自然重试，绝不向上返回错误、绝不阻塞启动。

package runtimebundle

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"qka/internal/applog"
)

// compareVersions 按点分段比较两个版本号：数字段比数值大小（0.1.9 < 0.1.10），
// 非数字段比字典序，且数字段视为大于非数字段（对齐语义化版本里
// 0.1.23-beta < 0.1.23 的习惯）；一方缺失的段按空串处理。
//
// 这里只求同一批目录名的稳定排序，不追求完整 semver 语义：目录名来自
// 注入的 cli.Version（形如 0.1.23 或 dev），排序结果只影响保留哪一份
// 旧副本，即使遇到不认识的名字也只是排序保守，不会误删当前版本。
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := "", ""
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aerr := strconv.Atoi(av)
		bn, berr := strconv.Atoi(bv)
		aok, bok := aerr == nil, berr == nil
		switch {
		case aok && bok:
			if an != bn {
				return cmp.Compare(an, bn)
			}
		case aok:
			return 1
		case bok:
			return -1
		default:
			if c := strings.Compare(av, bv); c != 0 {
				return c
			}
		}
	}
	return 0
}

// cleanupOldVersions 删除 runtimeRoot 下除 keepVersion 以外的历史版本目录，
// 按版本号从新到旧最多保留两份：keepVersion 本身 + 最近的一个旧版本。
//
// 容错约定（issue #4）：该函数没有返回值，任何失败都不影响调用方——
//   - runtimeRoot 不存在（首次安装）或读不出来：静默返回；
//   - 单个目录删除失败：Windows 上旧 bun.exe 可能仍被残留子进程占用，
//     记一条 warn 后继续处理其余目录，下次启动再试；
//   - .staging- 前缀的临时目录可能正被并发进程写入，一律跳过；散落的
//     普通文件不占体积，也不动。
func cleanupOldVersions(runtimeRoot, keepVersion string) {
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		// 首次安装时 runtime/ 尚不存在属正常；其余读取失败也只留 debug 线索。
		if !errors.Is(err, os.ErrNotExist) {
			applog.Debugf("runtimebundle", "读取运行时目录失败，跳过清理 path=%s: %v", runtimeRoot, err)
		}
		return
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), stagingPrefix) {
			continue
		}
		dirs = append(dirs, entry.Name())
	}
	// 版本号从新到旧排序：keepVersion 之外的第一个即「上一个版本」。
	sort.Slice(dirs, func(i, j int) bool { return compareVersions(dirs[i], dirs[j]) > 0 })

	var kept, stale []string
	previousKept := false
	for _, name := range dirs {
		switch {
		case name == keepVersion:
			kept = append(kept, name) // 当前版本始终保留
		case !previousKept:
			previousKept = true
			kept = append(kept, name) // 最近一个旧版本保留，回滚需要
		default:
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		applog.Debugf("runtimebundle", "运行时目录无需清理 version=%s", keepVersion)
		return
	}
	applog.Infof("runtimebundle", "清理旧版本运行时目录：保留=%s 待删=%s",
		strings.Join(kept, ","), strings.Join(stale, ","))
	for _, name := range stale {
		dir := filepath.Join(runtimeRoot, name)
		if err := removePath(dir); err != nil {
			// 典型场景是旧 bun.exe 被占用：跳过本次，下次启动再试。
			applog.Warnf("runtimebundle", "删除旧版本运行时目录失败（下次启动重试）dir=%s: %v", name, err)
			continue
		}
		applog.Infof("runtimebundle", "已删除旧版本运行时目录 dir=%s", name)
	}
}
