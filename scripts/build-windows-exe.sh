#!/usr/bin/env bash
# 构建 QKA 的 Windows 单文件发行物：每个架构一个自包含 exe，行为和 magpie 一致
# ——没有 zip、没有安装器，双击即用；Bun、adapters/ 与 provider-plugins/ 压进
# exe，首次运行时由 internal/runtimebundle 解压到配置目录。
#
# 用法：
#   VERSION=0.1.16 scripts/build-windows-exe.sh
#   VERSION=0.1.16 WIN_ARCH=amd64 scripts/build-windows-exe.sh
#
# 环境变量：
#   VERSION                         默认 git describe；产物命名用（去掉前导 v）
#   ARCH / WIN_ARCH                 amd64 | arm64 | all（默认 all）
#   DIST                            默认 <repo>/dist
#   BUN_VERSION                     默认 1.4.4
#   BUN_WINDOWS_AMD64/ARM64         可选：已有 bun.exe 路径，跳过下载
#
# 产物：dist/QKA-<version>-windows-<arch>.exe 与 dist/SHA256SUMS-windows-<version>.txt
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
VERSION="$(printf '%s' "$VERSION" | tr -cd 'A-Za-z0-9._+-')"
ARCH="${WIN_ARCH:-${ARCH:-all}}"
DIST="${DIST:-$ROOT/dist}"
BUN_VERSION="${BUN_VERSION:-1.4.4}"
BUN_CACHE="$DIST/cache/bun-v$BUN_VERSION"
ASSETS="$ROOT/internal/runtimebundle/assets"
SUMMARY="$DIST/SHA256SUMS-windows-$VERSION.txt"
WINVER="$(printf '%s' "$VERSION" | sed -E 's/^v//; s/[^0-9.].*//')"
WINVER="${WINVER:-0.0.0}"

die() { echo "error: $*" >&2; exit 2; }

cleanup() {
  rm -f "$ROOT"/cmd/qka/rsrc_windows_*.syso "$ROOT"/rsrc_windows_*.syso
  # 生成的内嵌资源不进版本库；每次构建重新生成，避免旧架构的资源被误嵌。
  find "$ASSETS" -maxdepth 1 -type f ! -name '.gitignore' ! -name '.gitkeep' -delete 2>/dev/null || true
}
trap cleanup EXIT

[[ -f "$ROOT/go.mod" ]] || die "请在仓库根目录运行"
[[ -d "$ROOT/adapters" ]] || die "缺少 adapters 目录"
[[ -d "$ROOT/provider-plugins" ]] || die "缺少 provider-plugins 目录"

archs=()
case "$ARCH" in
  all) archs=(amd64 arm64) ;;
  amd64|arm64) archs=("$ARCH") ;;
  *) die "unsupported ARCH=$ARCH (want amd64, arm64, or all)" ;;
esac

package_for() {
  case "$1" in
    amd64) echo "@oven/bun-windows-x64" ;;
    arm64) echo "@oven/bun-windows-aarch64" ;;
  esac
}

integrity_for() {
  case "$1" in
    amd64) echo "sha512-a3LNctBVO8PjePsFZfwDleF2JXYf2BfQ1u/PLRnBDLnC2Riml+4+LgJpKj4xRGhB92KweGyQI5PXaTkP9qypMQ==" ;;
    arm64) echo "sha512-n0ep5ZRPCZY1+dqFJpnnRUZB1wy4GYU6At8RF+pyzySCLjkN9R4rFh2PtBHAgF/lg1vOl3/CeER3pDM+p40ATA==" ;;
  esac
}

override_for() {
  case "$1" in
    amd64) printf '%s' "${BUN_WINDOWS_AMD64:-}" ;;
    arm64) printf '%s' "${BUN_WINDOWS_ARM64:-}" ;;
  esac
}

# prepare_bun 把对应架构的 bun.exe 放到 $2，已缓存则直接复用。
prepare_bun() {
  local arch="$1" out="$2" override
  override="$(override_for "$arch")"
  if [[ -n "$override" ]]; then
    [[ -f "$override" ]] || die "BUN_WINDOWS_$arch 指向的文件不存在：$override"
    cp "$override" "$out"
    return
  fi

  if [[ ! -f "$BUN_CACHE/$arch/bun.exe" ]]; then
    local package expected actual tarball
    package="$(package_for "$arch")"
    expected="$(integrity_for "$arch")"
    tarball="$BUN_CACHE/oven-bun-windows-$arch-$BUN_VERSION.tgz"
    mkdir -p "$BUN_CACHE"
    if [[ ! -f "$tarball" ]]; then
      echo "==> 下载 Bun ${BUN_VERSION}（windows/${arch}）"
      curl -fL --retry 3 --retry-delay 2 --connect-timeout 20 --max-time 600 \
        "https://registry.npmjs.org/$package/-/$(basename "$package")-$BUN_VERSION.tgz" \
        -o "$tarball"
    fi
    actual="sha512-$(openssl dgst -sha512 -binary "$tarball" | openssl base64 -A)"
    [[ "$actual" == "$expected" ]] || die "Bun 包完整性校验失败：$package@$BUN_VERSION"
    mkdir -p "$BUN_CACHE/$arch"
    tar -xOf "$tarball" package/bin/bun.exe > "$BUN_CACHE/$arch/bun.exe"
  fi
  cp "$BUN_CACHE/$arch/bun.exe" "$out"
}

mkdir -p "$DIST" "$ASSETS"

echo "==> go-winres（图标 / 版本 / DPI 清单）"
# go-winres 的 --out 只是文件名前缀，写到当前工作目录；Go 只识别「被编译包所在
# 目录」里的 *.syso。主包在 ./cmd/qka，所以必须在那个目录里生成，否则图标、
# 版本资源和 DPI 清单都不会被链接进 exe（Windows 上表现为没有图标、任务栏
# 无法固定应用）。
(
  cd cmd/qka
  go run github.com/tc-hib/go-winres@v0.3.3 make \
    --in ../../build/windows/winres.json --arch amd64,arm64 --out rsrc \
    --file-version "$WINVER" --product-version "$WINVER"
)

: > "$SUMMARY"
for arch in "${archs[@]}"; do
  echo "==> windows/${arch}（内嵌运行时）"
  bun="$(mktemp -t qka-bun-XXXXXX)"
  prepare_bun "$arch" "$bun"

  go run ./build/embedassets \
    --arch "$arch" \
    --bun "$bun" \
    --adapters "$ROOT/adapters" \
    --plugins "$ROOT/provider-plugins"
  rm -f "$bun"

  out="$DIST/QKA-$VERSION-windows-$arch.exe"
  rm -f "$out"
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" go build \
    -tags embeddedruntime -trimpath \
    -ldflags "-s -w -H windowsgui -X qka/internal/cli.Version=$VERSION -X qka/internal/cli.BuildFlavor=gui" \
    -o "$out" ./cmd/qka

  bytes="$(wc -c < "$out" | tr -d ' ')"
  # 明显偏小说明资源没嵌进去（bun 压缩后也在数十 MB 量级）。
  (( bytes > 40 * 1024 * 1024 )) || die "$(basename "$out") 只有 $((bytes / 1048576))MB，内嵌资源可能缺失"
  echo "    $(basename "$out")  $((bytes / 1048576))MB"
  (
    cd "$DIST"
    shasum -a 256 "$(basename "$out")"
  ) >> "$SUMMARY"
done

echo ""
echo "==> Windows 单文件发行物完成"
cat "$SUMMARY"
