# QKA 发布构建矩阵。
# - macOS 双架构（darwin/arm64、darwin/amd64）GUI 原生构建，需要 CGO。
# - Windows 双架构单文件发行物：一个自包含 exe（Bun/adapters/plugins 内嵌），
#   和 magpie 一样没有 zip、没有安装器；真实 Windows 行为需另机验收。

# 版本号单一来源（issue #2）：仓库根 VERSION 文件。环境变量/命令行可覆盖；
# 发行脚本在 RELEASE=1 或 CI 环境下会拒绝 git describe/dev 兜底。
VERSION ?= $(shell cat VERSION 2>/dev/null)
WINVER  := $(shell echo $(VERSION) | sed -E 's/^v//; s/[^0-9.].*//')
WIN_ARCH ?= all
DIST    ?= dist
PKG     := ./cmd/qka
BUN     := $(shell which bun)
LDFLAGS := -X qka/internal/cli.Version=$(VERSION)
GUI_LDFLAGS := $(LDFLAGS) -X qka/internal/cli.BuildFlavor=gui
# Windows GUI 产物只能在带 embeddedruntime 标签、且先跑过 build/embedassets
# 的构建里使用：否则双击会进安装向导，却在「解压运行时」一步失败
# （runtimebundle: 本次构建未内嵌运行时）。这里不再自己拼这两步，统一交给
# scripts/build-windows-exe.sh（Bun 缓存 + 构建锁 + embedassets + 标签 + verifype）。
ifeq ($(WIN_ARCH),all)
WIN_ARCHES := amd64 arm64
else
WIN_ARCHES := $(WIN_ARCH)
endif

.PHONY: build-darwin-arm64 build-darwin-amd64 build-linux build-windows \
	build-windows-cli release-windows release-macos-arm64 release-macos-amd64 \
	release-macos release-feed release-all release test vet fmt

build-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-darwin-arm64 $(PKG)

build-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-darwin-amd64 $(PKG)

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-linux-amd64 $(PKG)

# Windows GUI 产物：必须是自包含 exe（内嵌 Bun/adapters/provider-plugins）。
# 统一走发行脚本，避免再出现「GUI 版但没嵌运行时」的 exe——那种包能弹安装
# 向导，却必然在最后一步失败，用户侧表现为「安装失败：解压运行时失败」。
build-windows:
	@VERSION="$(VERSION)" DIST="$(abspath $(DIST))" WIN_ARCH="$(WIN_ARCH)" \
		scripts/build-windows-exe.sh
	@mkdir -p "$(DIST)/build"
	@for arch in $(WIN_ARCHES); do \
		cp "$(DIST)/QKA-$(VERSION)-windows-$$arch.exe" \
			"$(DIST)/build/QKA-windows-$$arch.exe" || exit 1; \
		echo "  $(DIST)/build/QKA-windows-$$arch.exe"; \
	done

# Windowless GUI builds cannot print to a terminal. Keep a console binary for
# serve/task-run/doctor and other CLI-only workflows.
build-windows-cli:
	@mkdir -p $(DIST)/build
	@for arch in amd64 arm64; do \
		echo "  windows/$$arch CLI"; \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -trimpath \
			-ldflags "$(LDFLAGS) -s -w" \
			-o $(DIST)/build/QKA-cli-windows-$$arch.exe $(PKG) || exit 1; \
	done

# Windows 单文件发行物：每个架构一个自包含 exe（内嵌 Bun、adapters/、
# provider-plugins/，首次运行解压到配置目录）。没有 zip、没有安装器。
release-windows:
	@VERSION="$(VERSION)" DIST="$(DIST)" WIN_ARCH="$(WIN_ARCH)" \
		scripts/build-windows-exe.sh

# 汇总已生成的 macOS DMG 与 Windows 单文件 exe，生成官方 feed.json 和 SHA256SUMS。
release-feed:
	@VERSION="$(VERSION)" DIST="$(abspath $(DIST))" \
		scripts/build-release-feed.sh

# macOS 发布使用可安装 DMG，不再把裸二进制当作更新制品。
release-macos-arm64:
	@VERSION="$(VERSION)" ARCH=arm64 scripts/build-dmg.sh

release-macos-amd64:
	@VERSION="$(VERSION)" ARCH=amd64 scripts/build-dmg.sh

release-macos: release-macos-arm64 release-macos-amd64

# 完整发布必须同时具备 macOS 双架构与 Windows 双架构制品。
release-all: release-macos release-windows release-feed

release: release-macos

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
