import subprocess, io, os, sys
from PIL import Image, ImageDraw, ImageFont

NAVY = (25, 33, 71, 255)
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
HERE = os.path.dirname(os.path.abspath(__file__))

def mark(size, mono=None):
    svg = os.path.join(HERE, "mark.svg" if mono is None else "mark-mono.svg")
    png = subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size), svg], capture_output=True, check=True).stdout
    im = Image.open(io.BytesIO(png)).convert("RGBA")
    if mono is not None:  # recolor the glyph
        px = im.load()
        for y in range(im.height):
            for x in range(im.width):
                a = px[x, y][3]
                px[x, y] = (*mono, a)
    return im

def wordmark(w, h, text_color=NAVY):
    im = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    m = int(round(h * 0.80))
    pad = (h - m) // 2
    im.alpha_composite(mark(m), (pad, pad))
    x0 = pad + m + max(2, h // 8)
    avail = w - x0 - pad
    d = ImageDraw.Draw(im)
    size = h
    while size > 6:
        f = ImageFont.truetype(FONT, size)
        l, t, r, b = d.textbbox((0, 0), "AutoMDM", font=f)
        if r - l <= avail and b - t <= m * 0.62:
            break
        size -= 1
    y = (h - (b - t)) // 2 - t
    d.text((x0 - l, y), "AutoMDM", font=f, fill=text_color)
    return im

def save(im, path, fmt=None):
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    im.save(path, fmt) if fmt else im.save(path)

if __name__ == "__main__":
    out = sys.argv[1]
    save(wordmark(236, 82), os.path.join(out, "wordmark-236x82.png"))
    save(mark(256), os.path.join(out, "mark-256.png"))
