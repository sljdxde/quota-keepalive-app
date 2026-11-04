#!/usr/bin/env bash
# 构建 QKA 的 Windows 单文件发行物：每个架构一个自包含 exe，行为和 magpie 一致
# ——没有 zip、没有安装器，双击即用；Bun、adapters/ 与 provider-plugins/ 压进
# exe，首次运行时由 internal/runtimebundle 解压到配置目录。
#
# 用法：
#   scripts/build-windows-exe.sh
#   WIN_ARCH=amd64 scripts/build-windows-exe.sh
#
# 环境变量：
#   VERSION                         版本号，默认读仓库根 VERSION 文件（单一来源，
#                                   见 issue #2）；都没有时本地退 git describe，
#                                   RELEASE=1 / CI 环境下直接失败
#   ARCH / WIN_ARCH                 amd64 | arm64 | all（默认 all）
#   DIST                            默认 <repo>/dist
#   BUN_VERSION                     默认 1.4.4
#   BUN_WINDOWS_AMD64/ARM64         可选：已有 bun.exe 路径，跳过下载
#   QKA_CACHE_HOME                  缓存根目录（Bun 下载与构建锁），默认按平台
#                                   选用户缓存区，绝不在 dist/ 里（见下）
#   QKA_LOCK_STALE_SECONDS          构建锁心跳超时，默认 1800
#   RELEASE=1                       发行构建：版本无法确定或不形如 x.y.z 时失败
#
# 产物：dist/QKA-<version>-windows-<arch>.exe 与 dist/SHA256SUMS-windows-<version>.txt
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

die() { echo "error: $*" >&2; exit 2; }

# sha256_file 输出与 `sha256sum` 相同的「<hex>  <name>」格式。
# 不同环境自带工具不一：Linux/Git Bash 有 sha256sum，macOS 用 shasum，
# 两者都没有时退回 openssl。CI（Git Bash）与 macOS 都要能跑。
sha256_file() {
  local f="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$f"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$f"
  else
    printf '%s  %s\n' "$(openssl dgst -sha256 -r "$f" | awk '{print $1}')" "$f"
  fi
}

# 发行/CI 语境：版本不允许静默回退到 git describe / dev。
release_build() {
  [[ "${RELEASE:-0}" == "1" || "${CI:-}" == "true" || "${GITHUB_ACTIONS:-}" == "true" ]]
}

# ---- 版本号（issue #2）：仓库根 VERSION 文件是单一来源 ----
# 解析顺序：VERSION 环境变量 > VERSION 文件 > git describe（仅本地兜底）。
# 本脚本与 build-dmg.sh、Makefile 共用同一来源，消除 mac/Windows 版本漂移。
VERSION="${VERSION:-}"
if [[ -z "$VERSION" && -f "$ROOT/VERSION" ]]; then
  VERSION="$(head -n1 "$ROOT/VERSION" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
fi
VERSION="${VERSION#v}"
if [[ -z "$VERSION" ]]; then
  if release_build; then
    die "发行/CI 构建无法确定版本：缺少 VERSION 环境变量与仓库根 VERSION 文件"
  fi
  VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
fi
[[ "$VERSION" =~ ^[0-9A-Za-z._+-]+$ ]] || die "invalid version: $VERSION"
if release_build; then
  [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || die "发行/CI 构建版本号必须形如 x.y.z：$VERSION"
fi

ARCH="${WIN_ARCH:-${ARCH:-all}}"
DIST="${DIST:-$ROOT/dist}"
BUN_VERSION="${BUN_VERSION:-1.4.4}"
ASSETS="$ROOT/internal/runtimebundle/assets"
SUMMARY="$DIST/SHA256SUMS-windows-$VERSION.txt"
WINVER="$(printf '%s' "$VERSION" | sed -E 's/^v//; s/[^0-9.].*//')"
WINVER="${WINVER:-0.0.0}"

# ---- 缓存目录（issue #1）：移出仓库与产物区 ----
# Bun 下载缓存原来放 dist/cache/，与产物同区：git clean -fdx、并行构建都会把它
# 删掉，导致每次重新下载。现在放用户缓存区：macOS 用 ~/Library/Caches/qka/，
# 其余（Linux、Windows Git Bash）用 $XDG_CACHE_HOME/qka/ 或 ~/.cache/qka/。
cache_home() {
  if [[ -n "${QKA_CACHE_HOME:-}" ]]; then
    printf '%s/qka' "$QKA_CACHE_HOME"
    return
  fi
  case "$(uname -s)" in
    Darwin) printf '%s/Library/Caches/qka' "$HOME" ;;
    *) printf '%s/qka' "${XDG_CACHE_HOME:-$HOME/.cache}" ;;
  esac
}
CACHE_HOME="$(cache_home)"
BUN_CACHE="$CACHE_HOME/bun-v$BUN_VERSION"

# ---- 构建锁（issue #1）：并发构建互斥，纯 POSIX 可移植 ----
# 两个发行脚本共用 dist/、cmd/qka/*.syso 和 runtimebundle 资源，并发跑会互相
# 覆盖/删除产物。锁方案：原子 mkdir 抢锁 + 心跳续期 + 陈旧检测（不用 flock：
# macOS 与 Linux 行为有差异，Git Bash 也未必自带）。
#   - 锁目录内写 pid 与 ts（持有期间每 60 秒续写一次心跳）；
#   - 持锁进程已退出、或心跳超过 QKA_LOCK_STALE_SECONDS（默认 30 分钟）未更新
#     时视为陈旧可抢占——长构建靠心跳保护，不会被误抢；
#   - 抢占与释放都先 mv 原子改名再删，避免两个等待者先后 rm 误删新持有者的锁。
# 锁放在用户缓存区而非 dist/：git clean -fdx 删不到，也不随产物清理消失。
LOCK_STALE_SECONDS="${QKA_LOCK_STALE_SECONDS:-1800}"
LOCK_WAIT_MAX="${QKA_LOCK_WAIT_MAX:-1800}"
[[ "$LOCK_STALE_SECONDS" =~ ^[0-9]+$ ]] || LOCK_STALE_SECONDS=1800
[[ "$LOCK_WAIT_MAX" =~ ^[0-9]+$ ]] || LOCK_WAIT_MAX=1800
LOCK_DIR="$CACHE_HOME/locks/build-$(printf '%s' "$ROOT" | cksum | awk '{print $1}')"
LOCK_HEARTBEAT_PID=""
BUN_TMP=""

lock_steal() { # $1 = 抢占原因
  echo "==> 抢占构建锁：$1"
  if mv "$LOCK_DIR" "$LOCK_DIR.stale.$$" 2>/dev/null; then
    rm -rf "$LOCK_DIR.stale.$$"
  fi
}

lock_acquire() {
  local waited=0 pid ts
  mkdir -p "$(dirname "$LOCK_DIR")" || die "无法创建缓存目录：$(dirname "$LOCK_DIR")"
  while true; do
    if mkdir "$LOCK_DIR" 2>/dev/null; then
      printf '%s\n' "$$" > "$LOCK_DIR/pid"
      date +%s > "$LOCK_DIR/ts"
      # 心跳：长构建（超过陈旧阈值）也持续刷新 ts，不会被误判陈旧。
      (
        while :; do
          sleep 60
          date +%s > "$LOCK_DIR/ts" 2>/dev/null || exit 0
        done
      ) &
      LOCK_HEARTBEAT_PID=$!
      return 0
    fi
    pid="$(head -n1 "$LOCK_DIR/pid" 2>/dev/null | tr -cd '0-9')"
    ts="$(head -n1 "$LOCK_DIR/ts" 2>/dev/null | tr -cd '0-9')"
    if [[ -z "$pid" && -z "$ts" ]]; then
      # 锁目录存在但没有 pid/ts：多半是持有者刚 mkdir 就死了；给 5 分钟宽限。
      if (( waited >= 300 )); then
        lock_steal "锁目录无 pid/ts（异常残留）"
        continue
      fi
    elif [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
      lock_steal "持有进程 $pid 已退出"
      continue
    elif [[ -n "$ts" ]] && (( $(date +%s) - ts > LOCK_STALE_SECONDS )); then
      lock_steal "心跳超过 $((LOCK_STALE_SECONDS / 60)) 分钟未更新"
      continue
    fi
    if (( waited >= LOCK_WAIT_MAX )); then
      die "等待构建锁超时（$((LOCK_WAIT_MAX / 60)) 分钟）：$LOCK_DIR"
    fi
    sleep 5
    waited=$((waited + 5))
  done
}

lock_release() {
  if [[ -n "$LOCK_HEARTBEAT_PID" ]]; then
    kill "$LOCK_HEARTBEAT_PID" 2>/dev/null || true
    LOCK_HEARTBEAT_PID=""
  fi
  if [[ -n "${LOCK_DIR:-}" ]] && mv "$LOCK_DIR" "$LOCK_DIR.release.$$" 2>/dev/null; then
    rm -rf "$LOCK_DIR.release.$$"
  fi
  return 0
}

cleanup() {
  # 只清理本次构建的可再生临时产物；绝不删 dist/ 里已成功的发行物（issue #1）：
  # 某个架构失败时，之前已成功的 exe 与校验和原样保留。
  rm -f "$ROOT"/cmd/qka/rsrc_windows_*.syso "$ROOT"/rsrc_windows_*.syso
  # 生成的内嵌资源不进版本库；每次构建重新生成，避免旧架构的资源被误嵌。
  find "$ASSETS" -maxdepth 1 -type f ! -name '.gitignore' ! -name '.gitkeep' -delete 2>/dev/null || true
  if [[ -n "$BUN_TMP" ]]; then
    rm -f "$BUN_TMP"
    BUN_TMP=""
  fi
  lock_release
}
trap cleanup EXIT

[[ -f "$ROOT/go.mod" ]] || die "请在仓库根目录运行"
[[ -d "$ROOT/adapters" ]] || die "缺少 adapters 目录"
[[ -d "$ROOT/provider-plugins" ]] || die "缺少 provider-plugins 目录"

echo "==> 版本 $VERSION 架构 $ARCH"
lock_acquire
mkdir -p "$DIST" "$ASSETS"

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
  BUN_TMP="$bun"
  prepare_bun "$arch" "$bun"

  go run ./build/embedassets \
    --arch "$arch" \
    --bun "$bun" \
    --adapters "$ROOT/adapters" \
    --plugins "$ROOT/provider-plugins"
  rm -f "$bun"
  BUN_TMP=""

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

  # 打包后自检（issue #2/#3）：产物文件名带本次版本；PE 资源（.rsrc、图标、
  # GUI 子系统、版本号、manifest identity）由 verifype 逐项校验，任一不满足
  # 即失败，防止再次发出无图标/版本漂移的 exe。
  # -require-runtime 额外确认内嵌运行时分包真的进了 exe：漏 -tags
  # embeddedruntime 的 GUI 包能弹安装向导，却在解压运行时一步必失败。
  [[ "$(basename "$out")" == "QKA-$VERSION-windows-$arch.exe" ]] \
    || die "产物文件名与版本不一致：$(basename "$out") 应为 QKA-$VERSION-windows-$arch.exe"
  echo "    校验 PE 资源与内嵌运行时（verifype）"
  go run ./build/verifype \
    -exe "$out" \
    -version "$WINVER" \
    -winres "$ROOT/build/windows/winres.json" \
    -require-runtime \
    || die "$(basename "$out") PE 资源校验失败"

  (
    cd "$DIST"
    sha256_file "$(basename "$out")"
  ) >> "$SUMMARY"
done

echo ""
echo "==> Windows 单文件发行物完成"
cat "$SUMMARY"
