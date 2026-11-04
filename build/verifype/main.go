// Command verifype 校验 QKA 的 Windows 发行 exe 的 PE 资源是否完整。
//
// 背景（issue #3）：曾发生 go-winres 把 .syso 生成在仓库根、而 Go 只链接
// cmd/qka/ 目录里的 syso，发出了没有图标的 exe。此工具在打包后做回归把关
// （scripts/build-windows-exe.sh、Makefile build-windows 与 CI 各跑一次），
// 任一项不过即 exit 1，并打印是哪项失败：
//
//  1. 段表存在 .rsrc 段；
//  2. .rsrc 里同时有 RT_ICON(3) 与 RT_GROUP_ICON(14)（解析到类型级即可）；
//  3. IMAGE_OPTIONAL_HEADER.Subsystem == 2（Windows GUI）；
//  4. RT_VERSION 里 VS_FIXEDFILEINFO 的 FileVersion/ProductVersion 等于期望
//     值；字符串表带 FileVersion/ProductVersion 时也必须一致；
//  5. RT_MANIFEST 文本包含期望的 identity（AppUserModelID），期望值默认取自
//     build/windows/winres.json 的 identity.name，保持单一来源。
//
// 纯标准库，linux/darwin/windows 均可运行；PE 整体校验由脚本冒烟，解析函数
// 的单测见同目录 *_test.go（用构造的字节切片做夹具，不在仓库里放真实 exe）。
//
// 用法：
//
//	go run ./build/verifype -exe dist/QKA-0.1.23-windows-amd64.exe \
//	    -version 0.1.23 -winres build/windows/winres.json
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	exePath := flag.String("exe", "", "待校验的 PE/exe 路径（必需）")
	version := flag.String("version", "", "期望的 File/ProductVersion（如 0.1.23）；空则跳过版本校验")
	winresPath := flag.String("winres", "", "winres.json 路径，取其 identity.name 作为 manifest 期望值")
	identity := flag.String("identity", "", "manifest 期望的 identity（AppUserModelID）；优先于 -winres")
	requireRuntime := flag.Bool("require-runtime", false,
		"要求 exe 内嵌运行时分包（Bun/adapters/provider-plugins）：GUI 发行物必须带，"+
			"否则安装向导会在解压运行时一步失败")
	flag.Parse()

	if *exePath == "" {
		flag.Usage()
		os.Exit(2)
	}
	expectIdentity := *identity
	if expectIdentity == "" && *winresPath != "" {
		name, err := identityFromWinres(*winresPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "verifype: %v\n", err)
			os.Exit(1)
		}
		expectIdentity = name
	}

	data, err := os.ReadFile(*exePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verifype: 读取 %s: %v\n", *exePath, err)
		os.Exit(1)
	}
	fmt.Printf("verifype: %s\n", filepath.Base(*exePath))
	fails := verifyPE(os.Stdout, data, *version, expectIdentity)
	if *requireRuntime {
		fails = append(fails, checkEmbeddedRuntime(os.Stdout, data)...)
	}
	if len(fails) > 0 {
		fmt.Fprintf(os.Stderr, "verifype: %d 项校验未通过\n", len(fails))
		os.Exit(1)
	}
	fmt.Println("verifype: 全部校验通过")
}
