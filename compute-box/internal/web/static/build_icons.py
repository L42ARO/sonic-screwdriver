"""Regenerates the bitmap icons from logo.svg. Run after editing logo.svg:

    python internal/web/static/build_icons.py

logo.svg is the only source: every page shows it (sidebar, sign-in) and browsers use it
as the favicon. Two bitmap copies exist only for clients that can't use SVG:

    favicon.ico           16, 32 and 48 px, for browsers and tools that ask for /favicon.ico
    apple-touch-icon.png  180 px on a full green square (iOS rounds the corners itself)

Needs Microsoft Edge or Google Chrome (to draw the SVG) and Pillow (pip install pillow).
"""
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

HERE = Path(__file__).resolve().parent
GREEN = (0x22, 0xC5, 0x5E, 255)  # the logo's background, from logo.svg
RENDER = 1024


def browser() -> str:
    for candidate in (
        r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        shutil.which("msedge"), shutil.which("google-chrome"), shutil.which("chromium"),
    ):
        if candidate and Path(candidate).exists():
            return candidate
    sys.exit("Need Microsoft Edge or Google Chrome to draw logo.svg.")


def render(svg: Path) -> Image.Image:
    """Draws the SVG at RENDER x RENDER on a transparent background."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        page = tmp / "logo.html"
        page.write_text(
            "<!doctype html><html><body style='margin:0;background:transparent'>"
            f"<img src='{svg.as_uri()}' width='{RENDER}' height='{RENDER}' style='display:block'>"
            "</body></html>", encoding="utf-8")
        shot = tmp / "logo.png"
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
        sys.exit("The render came out empty; is logo.svg valid?")
    return image


def main():
    logo = render(HERE / "logo.svg")
    logo.save(HERE / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)])
    touch = Image.new("RGBA", logo.size, GREEN)
    touch.alpha_composite(logo)
    touch.convert("RGB").resize((180, 180), Image.LANCZOS).save(HERE / "apple-touch-icon.png", optimize=True)
    print("wrote favicon.ico and apple-touch-icon.png from logo.svg")


if __name__ == "__main__":
    main()
