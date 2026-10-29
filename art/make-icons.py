#!/usr/bin/env python3
"""art/make-icons.py -- regenerate every Windows icon asset from one PNG.

    pip install pillow
    python3 art/make-icons.py
    python3 art/make-icons.py --src art/omegamaps-icon-1024.png

The source defaults to packaging/linux/omegamaps.png (256x256, the same art the
AppImage uses). A larger master, if one exists, is a better source: the
scale-200 tiles go up to 620px and are upscaled from a 256 source.

Produces, beside this script:

    omegamaps.ico            exe icon, compiled in by app/omegamaps.rc and
                             viewer/omegamaps-viewer.rc
    omegamaps-icon-256.png   runtime window icon (app/resources.qrc)
    msix/                    every asset packaging/msix/AppxManifest.xml names,
                             plus the scale and targetsize variants that
                             resources.pri indexes

All of these are committed: the Windows build machine needs them and should not
need Pillow.
"""

import argparse
import os

from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
DEFAULT_SRC = os.path.join(ROOT, "packaging", "linux", "omegamaps.png")

# The icon's own background, sampled from the art. Used to fill the wide tile
# around a centred icon, so the tile reads as one piece rather than an icon
# floating on Windows' accent colour.
TILE_BG = (6, 14, 26, 255)


def resized(master, px):
    return master.resize((px, px), Image.LANCZOS)


def make_ico(master):
    path = os.path.join(HERE, "omegamaps.ico")
    master.save(path, sizes=[(16, 16), (24, 24), (32, 32), (48, 48),
                             (64, 64), (128, 128), (256, 256)])
    print(f"ico: {path}")


def make_window_icon(master):
    path = os.path.join(HERE, "omegamaps-icon-256.png")
    resized(master, 256).save(path)
    print(f"window icon: {path}")


def make_msix(master):
    out = os.path.join(HERE, "msix")
    os.makedirs(out, exist_ok=True)
    for f in os.listdir(out):
        if f.endswith(".png"):
            os.remove(os.path.join(out, f))

    def square(name, side):
        for scale in (100, 200):
            px = round(side * scale / 100)
            suffix = "" if scale == 100 else f".scale-{scale}"
            resized(master, px).save(os.path.join(out, f"{name}{suffix}.png"))

    square("Square44x44Logo", 44)
    square("Square71x71Logo", 71)
    square("Square150x150Logo", 150)
    square("Square310x310Logo", 310)
    square("StoreLogo", 50)

    # Taskbar, Alt-Tab and Start's app list read these. Unplated means no
    # accent-coloured square behind the icon; without these files (and without
    # resources.pri indexing them) Windows shows a small plated icon.
    for px in (16, 24, 32, 48, 256):
        resized(master, px).save(os.path.join(
            out, f"Square44x44Logo.targetsize-{px}_altform-unplated.png"))

    for scale in (100, 200):
        w, h = round(310 * scale / 100), round(150 * scale / 100)
        tile = Image.new("RGBA", (w, h), TILE_BG)
        side = round(h * 0.8)
        icon = resized(master, side)
        tile.paste(icon, ((w - side) // 2, (h - side) // 2), icon)
        suffix = "" if scale == 100 else f".scale-{scale}"
        tile.save(os.path.join(out, f"Wide310x150Logo{suffix}.png"))

    print(f"msix: {len(os.listdir(out))} assets in {out}")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--src", default=DEFAULT_SRC, help="square source PNG")
    args = ap.parse_args()

    master = Image.open(args.src).convert("RGBA")
    if master.width != master.height:
        raise SystemExit(f"{args.src}: not square ({master.width}x{master.height})")
    print(f"source: {args.src} ({master.width}x{master.height})")

    make_ico(master)
    make_window_icon(master)
    make_msix(master)
    print("icons: done")


if __name__ == "__main__":
    main()
