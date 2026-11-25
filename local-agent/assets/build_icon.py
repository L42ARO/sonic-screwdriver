"""Regenerates icon.ico from icon.svg. Run after editing icon.svg, then rebuild the agent:

    python assets/build_icon.py
    cargo build --release

icon.svg is the agent's logo: the dashboard logo (compute-box/internal/web/static/logo.svg)
in black and white. icon.ico holds every size Windows asks for (Explorer, taskbar, tray,
Alt+Tab); build.rs embeds it in local-agent.exe.

Needs Microsoft Edge or Google Chrome (to draw the SVG) and Pillow (pip install pillow).
"""
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

HERE = Path(__file__).resolve().parent
RENDER = 1024
SIZES = [16, 20, 24, 32, 40, 48, 64, 256]


def browser() -> str:
    for candidate in (
        r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        shutil.which("msedge"), shutil.which("google-chrome"), shutil.which("chromium"),
    ):
        if candidate and Path(candidate).exists():
            return candidate
    sys.exit("Need Microsoft Edge or Google Chrome to draw icon.svg.")


def render(svg: Path) -> Image.Image:
    """Draws the SVG at RENDER x RENDER on a transparent background."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        page = tmp / "icon.html"
        page.write_text(
            "<!doctype html><html><body style='margin:0;background:transparent'>"
            f"<img src='{svg.as_uri()}' width='{RENDER}' height='{RENDER}' style='display:block'>"
            "</body></html>", encoding="utf-8")
        shot = tmp / "icon.png"
        subprocess.run([
            browser(), "--headless=new", "--disable-gpu", "--hide-scrollbars",
            f"--user-data-dir={tmp / 'profile'}", "--default-background-color=00000000",
            "--allow-file-access-from-files", f"--window-size={RENDER},{RENDER}",
            f"--screenshot={shot}", page.as_uri(),
        ], check=True, capture_output=True, timeout=60)
        image = Image.open(shot).convert("RGBA")
        image.load()
    if image.size != (RENDER, RENDER):
        image = image.crop((0, 0, RENDER, RENDER))
    if image.getpixel((RENDER // 2, RENDER // 2))[3] == 0:
        sys.exit("The render came out empty; is icon.svg valid?")
    return image


def main():
    icon = render(HERE / "icon.svg")
    icon.save(HERE / "icon.ico", sizes=[(s, s) for s in SIZES])
    print(f"wrote icon.ico ({', '.join(map(str, SIZES))} px) from icon.svg")


if __name__ == "__main__":
    main()
