#!/usr/bin/env bash
# Collect already-built macOS DMGs and Windows single-file EXEs into the self-update feed.
# This script only reads release artifacts and atomically writes feed.json and
# SHA256SUMS; it never rebuilds or deletes application bundles.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${DIST:-$ROOT/dist}"

# 版本号（issue #2）：仓库根 VERSION 文件是单一来源，与两个发行脚本一致。
# 解析顺序：VERSION 环境变量 > VERSION 文件 > git describe（仅本地兜底）；
# RELEASE=1 / CI 环境下不允许静默回退。
VERSION="${VERSION:-}"
if [[ -z "$VERSION" && -f "$ROOT/VERSION" ]]; then
  VERSION="$(head -n1 "$ROOT/VERSION" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
fi
VERSION="${VERSION#v}"
if [[ -z "$VERSION" ]]; then
  if [[ "${RELEASE:-0}" == "1" || "${CI:-}" == "true" || "${GITHUB_ACTIONS:-}" == "true" ]]; then
    echo "release feed 无法确定版本：缺少 VERSION 环境变量与仓库根 VERSION 文件（CI/RELEASE 不允许回退）" >&2
    exit 2
  fi
  VERSION="$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)"
fi
if [[ ! "$VERSION" =~ ^[0-9A-Za-z._+-]+$ ]]; then
  echo "invalid version: $VERSION" >&2
  exit 2
fi
BASE_URL="${QKA_RELEASE_BASE_URL:-https://github.com/sljdxde/quota-keepalive-app/releases/latest/download}"
REQUIRE_PLATFORMS="${QKA_REQUIRE_PLATFORMS:-darwin/arm64,darwin/amd64,windows/amd64,windows/arm64}"

if [[ ! -d "$DIST" ]]; then
  echo "release artifact directory does not exist: $DIST" >&2
  exit 2
fi

args=()
artifacts=()

add_artifact() {
  local platform="$1" path="$2"
  local existing
  args+=(--artifact "$platform=$path")
  if [[ ${#artifacts[@]} -gt 0 ]]; then
    for existing in "${artifacts[@]}"; do
      if [[ "$existing" == "$path" ]]; then
        return
      fi
    done
  fi
  artifacts+=("$path")
}

shopt -s nullglob
for path in "$DIST/dmg/QKA-$VERSION-"*.dmg; do
  [[ -f "$path" ]] || continue
  case "$(basename "$path")" in
    *-universal.dmg)
      add_artifact darwin/arm64 "$path"
      add_artifact darwin/amd64 "$path"
      ;;
    *-arm64.dmg) add_artifact darwin/arm64 "$path" ;;
    *-amd64.dmg) add_artifact darwin/amd64 "$path" ;;
    *)
      echo "unsupported macOS artifact name: $path" >&2
      exit 2
      ;;
  esac
done

# Windows 发行物是单个自包含 exe（和 magpie 一样，没有 zip / 安装器）。
for path in "$DIST/QKA-$VERSION-windows-"*.exe; do
  [[ -f "$path" ]] || continue
  case "$(basename "$path")" in
    *-windows-arm64.exe) add_artifact windows/arm64 "$path" ;;
    *-windows-amd64.exe) add_artifact windows/amd64 "$path" ;;
    *)
      echo "unsupported Windows artifact name: $path" >&2
      exit 2
      ;;
  esac
done

if [[ ${#args[@]} -eq 0 ]]; then
  echo "no QKA $VERSION release artifacts found under $DIST" >&2
  exit 2
fi

(
  cd "$ROOT"
  go run ./cmd/releasefeed \
    --version "$VERSION" \
    --base-url "$BASE_URL" \
    --require-platforms "$REQUIRE_PLATFORMS" \
    --out "$DIST/feed.json" \
    "${args[@]}"
)

shasum -a 256 "${artifacts[@]}" > "$DIST/SHA256SUMS"
echo "SHA256SUMS: $DIST/SHA256SUMS"
echo "feed.json:  $DIST/feed.json"
