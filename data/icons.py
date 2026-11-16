"""Draws Anyscale's pixel-art icons on a 24x24 grid, scaled to 256x256.

Four tailnet nodes sit in a diamond around a center node, which is this
machine. The cyan network links the top node to the others, the pink
one links the bottom node, and both meet in the center: several tailnets
on one machine.
"""
import sys
from PIL import Image

N = 24
OUT = 256

BG = (27, 21, 48, 255)
BG2 = (36, 28, 64, 255)
BORDER = (74, 58, 128, 255)
WHITE = (244, 244, 244, 255)
YELLOW = (255, 210, 63, 255)
CYAN = (63, 224, 208, 255)
PINK = (255, 95, 162, 255)

TOP, BOTTOM, LEFT, RIGHT, CENTER = (11, 3), (11, 19), (3, 11), (19, 11), (11, 11)


def line(px, a, b, color):
    """Bresenham line between grid points a and b."""
    (x0, y0), (x1, y1) = a, b
    dx, dy = abs(x1 - x0), -abs(y1 - y0)
    sx, sy = (1 if x0 < x1 else -1), (1 if y0 < y1 else -1)
    err = dx + dy
    while True:
        px[x0, y0] = color
        if (x0, y0) == (x1, y1):
            return
        e2 = 2 * err
        if e2 >= dy:
            err += dy
            x0 += sx
        if e2 <= dx:
            err += dx
            y0 += sy


def node(px, at, color, size=3):
    x, y = at
    r = size // 2
    for dx in range(-r, r + 1):
        for dy in range(-r, r + 1):
            px[x + dx, y + dy] = color


def glyph(px, nodes, center, a, b, bus):
    line(px, TOP, LEFT, a)
    line(px, TOP, RIGHT, a)
    line(px, TOP, CENTER, a)
    line(px, BOTTOM, LEFT, b)
    line(px, BOTTOM, RIGHT, b)
    line(px, BOTTOM, CENTER, b)
    line(px, LEFT, CENTER, bus)
    line(px, RIGHT, CENTER, bus)
    for at in (TOP, BOTTOM, LEFT, RIGHT):
        node(px, at, nodes)
    node(px, CENTER, center)


def tile(px):
    """The app icon's rounded tile, with a lighter upper half."""
    for y in range(N):
        for x in range(N):
            cx, cy = min(x, N - 1 - x), min(y, N - 1 - y)
            if cx + cy < 3:
                continue
            if cx == 0 or cy == 0 or cx + cy == 3:
                px[x, y] = BORDER
            else:
                px[x, y] = BG2 if y < N // 2 else BG


def make(kind):
    img = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    px = img.load()
    if kind == "app":
        tile(px)
        glyph(px, WHITE, YELLOW, CYAN, PINK, WHITE)
    elif kind == "active":
        glyph(px, WHITE, WHITE, WHITE, WHITE, WHITE)
    elif kind == "inactive":
        g = (140, 140, 140, 255)
        glyph(px, g, g, g, g, g)
    elif kind == "exit":
        glyph(px, WHITE, YELLOW, WHITE, WHITE, WHITE)
    # 24 doesn't divide 256, so scale up by whole pixels and center.
    k = OUT // N
    big = img.resize((N * k, N * k), Image.NEAREST)
    out = Image.new("RGBA", (OUT, OUT), (0, 0, 0, 0))
    off = (OUT - N * k) // 2
    out.paste(big, (off, off))
    return out


if __name__ == "__main__":
    dest = sys.argv[1]
    for kind in ("app", "active", "inactive", "exit"):
        make(kind).save(f"{dest}/{kind}.png")
