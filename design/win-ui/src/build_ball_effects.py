# 把四张宠物立绘裁掉透明边、压成 WebP 内联进原型模板，
# 生成 design/win-ui/ball-effects.html —— 单文件、无外部依赖，浏览器直接打开。
# 用法：python design/win-ui/src/build_ball_effects.py
from pathlib import Path
import base64
import io

from PIL import Image

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
SPRITE_DIR = ROOT / "internal" / "gui" / "frontend" / "assets" / "pokemon"
TEMPLATE = HERE / "ball-effects.template.html"
OUTPUT = HERE.parent / "ball-effects.html"

PETS = ("charmander", "pikachu", "bulbasaur", "squirtle")


def inline_sprite(pet: str) -> str:
    image = Image.open(SPRITE_DIR / f"{pet}.png").convert("RGBA")
    bbox = image.getchannel("A").getbbox()
    if bbox:
        image = image.crop(bbox)
    image.thumbnail((240, 240), Image.LANCZOS)
    buf = io.BytesIO()
    image.save(buf, "WEBP", quality=84, method=6)
    return "data:image/webp;base64," + base64.b64encode(buf.getvalue()).decode("ascii")


def main() -> None:
    html = TEMPLATE.read_text(encoding="utf-8")
    for pet in PETS:
        marker = f"__SPRITE_{pet.upper()}__"
        if marker not in html:
            raise SystemExit(f"模板缺少占位符：{marker}")
        html = html.replace(marker, inline_sprite(pet))
    OUTPUT.write_text(html, encoding="utf-8", newline="\n")
    print(f"生成 {OUTPUT}（{OUTPUT.stat().st_size} 字节）")


if __name__ == "__main__":
    main()
