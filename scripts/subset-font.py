#!/usr/bin/env python3
"""重新生成内置中文字体子集（internal/gui/frontend/assets/fonts/）。

背景：Windows 系统中文只有 Microsoft YaHei 的 400/700 两档字重，界面里写的
560 / 620 / 650 会被浏览器向上取整成 700，整屏「中等强调」文字全部偏粗——
这是 Windows 版字难读的根因。QKA 因此自带一份 Noto Sans SC 可变字体。

为什么需要这个脚本：完整可变字体 14.4MB，不可能进安装包。这里按界面**实际
用到**的字符做子集，压到约 445KB。界面文案新增汉字后必须重跑，否则新字会
回退到系统字体（不会出方框，但字重会不一致）。

用法：
    python3 scripts/subset-font.py                # 重新生成
    python3 scripts/subset-font.py --check        # 只校验现有子集是否缺字

依赖（一次性）：
    python3 -m venv /tmp/fontenv
    /tmp/fontenv/bin/pip install fonttools brotli -i https://pypi.tuna.tsinghua.edu.cn/simple

字体来源与许可：
    Noto Sans SC Variable（SIL Open Font License 1.1）
    https://github.com/notofonts/noto-cjk
    许可证全文见 internal/gui/frontend/assets/fonts/LICENSE-OFL.txt
"""

from __future__ import annotations

import argparse
import glob
import pathlib
import re
import subprocess
import sys
import tempfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
FRONTEND = ROOT / "internal" / "gui" / "frontend"
OUTPUT = FRONTEND / "assets" / "fonts" / "noto-sc-vf-subset.woff2"

# 可变字体源。走 ghfast.top 镜像：GitHub 直连在国内经常超时。
FONT_URLS = (
    "https://ghfast.top/https://raw.githubusercontent.com/notofonts/noto-cjk/"
    "main/Sans/Variable/OTF/Subset/NotoSansSC-VF.otf",
    "https://raw.githubusercontent.com/notofonts/noto-cjk/"
    "main/Sans/Variable/OTF/Subset/NotoSansSC-VF.otf",
)

# 字符集来源：
#   1. 前端全部 html/js/css —— 界面文案的主来源。
#   2. Go 侧的**字符串字面量** —— 错误提示、状态名等会原样显示在界面上。
#      只取字面量、不取注释：注释里的汉字（实现说明、issue 引用）永远不上屏，
#      收进字库只会让子集变大、并让 --check 因无关改动而误报。
FRONTEND_GLOBS = (
    "internal/gui/frontend/*.html",
    "internal/gui/frontend/assets/*.js",
    "internal/gui/frontend/assets/*.css",
)
GO_GLOBS = ("internal/**/*.go",)

CJK = re.compile(r"[\u4e00-\u9fff]")
# Go 字符串字面量：双引号（含转义）与反引号（raw string）。
GO_STRING_LITERAL = re.compile(r'"((?:[^"\\\n]|\\.)*)"|`([^`]*)`')
# 注释：先剥掉，避免注释里的汉字进字库。
GO_LINE_COMMENT = re.compile(r"//[^\n]*")
GO_BLOCK_COMMENT = re.compile(r"/\*.*?\*/", re.S)


def _read(path: str) -> str:
    try:
        return pathlib.Path(path).read_text(encoding="utf-8", errors="ignore")
    except OSError:
        return ""


def collect_chars() -> set[str]:
    """收集界面可能出现的全部字符。"""
    chars: set[str] = set()
    for pattern in FRONTEND_GLOBS:
        for path in glob.glob(str(ROOT / pattern), recursive=True):
            if "vendor" in path or "node_modules" in path:
                continue
            chars.update(CJK.findall(_read(path)))

    for pattern in GO_GLOBS:
        for path in glob.glob(str(ROOT / pattern), recursive=True):
            if "_test.go" in path or "vendor" in path:
                continue
            src = _read(path)
            if not src:
                continue
            src = GO_BLOCK_COMMENT.sub("", GO_LINE_COMMENT.sub("", src))
            for match in GO_STRING_LITERAL.finditer(src):
                chars.update(CJK.findall(match.group(1) or match.group(2) or ""))

    # ASCII 可打印
    chars.update(chr(c) for c in range(0x20, 0x7F))
    # Latin-1 可见区（含 · ° ± × ÷）：单位、乘号、间隔号都会用到。
    # 注意跳过 0x7F-0x9F 的 C1 控制符：它们没有字形，收进来只会让校验误报。
    chars.update(chr(c) for c in range(0xA0, 0x100))
    # 中文标点与全角符号
    for start, end in ((0x3000, 0x303F), (0xFF00, 0xFFEF)):
        chars.update(chr(c) for c in range(start, end + 1))
    # 箭头、数学、通用标点、货币、杂项符号
    for start, end in (
        (0x2000, 0x206F),
        (0x20A0, 0x20CF),
        (0x2190, 0x21FF),
        (0x2200, 0x22FF),
        (0x2600, 0x27BF),
    ):
        chars.update(chr(c) for c in range(start, end + 1))
    return chars


def download_font(dest: pathlib.Path) -> None:
    """下载完整可变字体；多个源依次尝试。"""
    last_error: Exception | None = None
    for url in FONT_URLS:
        try:
            print(f"  下载 {url.split('/')[2]} ...", flush=True)
            req = urllib.request.Request(url, headers={"User-Agent": "qka-subset-font"})
            with urllib.request.urlopen(req, timeout=180) as resp:
                data = resp.read()
            if len(data) < 1_000_000:
                raise RuntimeError(f"响应过小（{len(data)} 字节），疑似镜像错误页")
            dest.write_bytes(data)
            print(f"  已下载 {len(data) / 1048576:.1f}MB")
            return
        except Exception as exc:  # noqa: BLE001 - 逐个源尝试，全部失败才抛
            last_error = exc
            print(f"  失败：{exc}", file=sys.stderr)
    raise SystemExit(f"所有字体源均不可用：{last_error}")


def subset(font_path: pathlib.Path, chars: set[str], out: pathlib.Path) -> None:
    """调用 pyftsubset 生成 woff2 子集，保留 wght 可变轴。"""
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix=".txt", delete=False) as fh:
        fh.write("".join(sorted(chars)))
        text_file = fh.name
    try:
        subprocess.run(
            [
                sys.executable, "-m", "fontTools.subset", str(font_path),
                f"--text-file={text_file}",
                "--flavor=woff2",
                f"--output-file={out}",
                "--layout-features=*",
                "--no-hinting",
                "--desubroutinize",
            ],
            check=True,
        )
    finally:
        pathlib.Path(text_file).unlink(missing_ok=True)


def check(out: pathlib.Path, chars: set[str]) -> int:
    """校验现有子集是否覆盖全部字符；返回缺失数量。"""
    try:
        from fontTools.ttLib import TTFont
    except ImportError:
        print("需要 fontTools 才能校验：pip install fonttools", file=sys.stderr)
        return 1
    if not out.exists():
        print(f"子集不存在：{out}", file=sys.stderr)
        return 1
    font = TTFont(str(out))
    cmap = font.getBestCmap()
    # 只校验必须渲染的字符：汉字、ASCII 可见字符、以及界面确实用到的符号。
    # 大范围 Unicode 区块（Latin Extended / 箭头 / 数学）里必然有些码位该字体
    # 本来就没有，把那些算成「缺字」会让校验永远失败且没有意义。
    required = {c for c in chars if CJK.match(c)}
    required.update(chr(c) for c in range(0x20, 0x7F))
    # 只列界面确实会渲染的符号。™ 只在 Go 字符串里出现（不显示），
    # 且 Noto Sans SC 本身不含该字形，列进来会让校验永远失败。
    required.update("·°±×÷→←↑↓—–…‰′″€£¥©®§¶†‡•")
    missing = sorted(c for c in required if ord(c) not in cmap)
    if missing:
        print(f"缺 {len(missing)} 个字符：{''.join(missing[:80])}", file=sys.stderr)
        print("请重跑 python3 scripts/subset-font.py", file=sys.stderr)
        return len(missing)
    print(f"子集完整：{len(required)} 个必需字符全部覆盖，{font['maxp'].numGlyphs} 个字形")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="生成内置中文字体子集")
    parser.add_argument("--check", action="store_true", help="只校验现有子集，不重新生成")
    args = parser.parse_args()

    chars = collect_chars()
    cjk = sorted(c for c in chars if CJK.match(c))
    print(f"字符集：{len(chars)} 个（其中汉字 {len(cjk)} 个）")

    if args.check:
        return 0 if check(OUTPUT, chars) == 0 else 1

    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        font = pathlib.Path(tmp) / "NotoSansSC-VF.otf"
        download_font(font)
        subset(font, chars, OUTPUT)
    size = OUTPUT.stat().st_size
    print(f"已生成 {OUTPUT.relative_to(ROOT)}：{size} 字节（{size / 1024:.0f}KB）")
    return 0 if check(OUTPUT, chars) == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
