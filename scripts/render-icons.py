"""Render editable SVG sources. Requires Pillow and resvg-py (build tools only)."""
from pathlib import Path
from io import BytesIO
import sys
import resvg_py
from PIL import Image

root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
sizes = [(n, n) for n in (16, 20, 24, 32, 40, 48, 64, 128, 256)]

def render(path, size):
    return Image.open(BytesIO(resvg_py.svg_to_bytes(svg_path=str(path), width=size, height=size))).convert("RGBA")

for source in (root / "apps/windows/icons").glob("*.svg"):
    render(source, 48).save(source.with_suffix(".png"))
    render(source, 256).save(source.with_suffix(".ico"), sizes=sizes)
app = root / "resources/CommBox.svg"
render(app, 1024).save(root / "resources/logo.png")
render(app, 256).save(app.with_suffix(".ico"), sizes=sizes)
render(app, 1024).save(app.with_suffix(".icns"))
print("Rendered application and action icons")
