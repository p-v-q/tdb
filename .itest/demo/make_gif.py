#!/usr/bin/env python3
"""Build the README demo GIF from recorded terminal frames.

usage: make_gif.py FRAMES_DIR MANIFEST OUT_DIR OUTPUT_GIF

FRAMES_DIR holds 00000.png ... frames rendered by the demo recorder; MANIFEST
holds "<name> <delay-ms>" lines. The frames are downscaled, quantized to one
shared palette, near-identical consecutive frames are merged (their delays add
up), and the result is written as a looping GIF.
"""

import os
import sys

from PIL import Image, ImageChops

WIDTH = int(os.environ.get("DEMO_GIF_WIDTH", "980"))
COLORS = int(os.environ.get("DEMO_GIF_COLORS", "128"))
MAX_FRAMES = int(os.environ.get("DEMO_GIF_MAX_FRAMES", "120"))
SAME_PIXEL_DIFF = int(os.environ.get("DEMO_GIF_SAME_DIFF", "0"))


def read_manifest(path):
    entries = []
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            name, delay = line.split()
            entries.append((name, int(delay)))
    return entries


def palette_image(frames):
    # One palette for every frame keeps the GIF small and the colors stable:
    # a sample of the recording is enough to describe its colors.
    sample = frames[:: max(1, len(frames) // 12)]
    sheet = Image.new("RGB", (WIDTH, sum(f.height for f in sample)))
    y = 0
    for f in sample:
        sheet.paste(f, (0, y))
        y += f.height
    return sheet.quantize(colors=COLORS, method=Image.Quantize.MEDIANCUT)


def frames_differ(a, b):
    diff = ImageChops.difference(a.convert("RGB"), b.convert("RGB"))
    box = diff.getbbox()
    if box is None:
        return False
    return diff.crop(box).getextrema()[0][1] > SAME_PIXEL_DIFF


def main():
    if len(sys.argv) != 5:
        sys.exit(__doc__.strip())
    frames_dir, manifest_path, out_dir, out_gif = sys.argv[1:]
    entries = read_manifest(manifest_path)

    images = []
    total = 0
    for name, delay in entries:
        path = os.path.join(frames_dir, name + ".png")
        if not os.path.exists(path):
            print(f"skip missing frame {path}")
            continue
        img = Image.open(path).convert("RGB")
        width, height = img.size
        img = img.resize((WIDTH, round(height * WIDTH / width)), Image.LANCZOS)
        images.append([img, delay])
        total += delay
    if not images:
        sys.exit("no frames")

    palette = palette_image([img for img, _ in images])
    quantized = []
    for img, delay in images:
        quantized.append([img.quantize(palette=palette, dither=Image.Dither.NONE), delay])

    # Merge frames that did not change: a static screen costs one frame with a
    # longer delay instead of a dozen identical ones.
    merged = [quantized[0]]
    for img, delay in quantized[1:]:
        if not frames_differ(merged[-1][0], img):
            merged[-1][1] += delay
            continue
        merged.append([img, delay])

    # Keep the GIF bounded: if there are too many frames, keep every n-th one
    # and carry its delay over.
    if len(merged) > MAX_FRAMES:
        step = len(merged) / MAX_FRAMES
        kept = []
        i = 0.0
        while int(i) < len(merged):
            frame = merged[int(i)]
            delay = frame[1]
            for j in range(int(i) + 1, min(int(i) + int(step), len(merged))):
                delay += merged[j][1]
            kept.append([frame[0], delay])
            i += step
        merged = kept

    frames = [img for img, _ in merged]
    durations = [delay for _, delay in merged]
    os.makedirs(out_dir, exist_ok=True)
    frames[0].save(
        out_gif,
        save_all=True,
        append_images=frames[1:],
        duration=durations,
        loop=0,
        optimize=True,
        disposal=2,
    )
    size = os.path.getsize(out_gif)
    print(f"gif: {out_gif}  frames={len(frames)}  source_frames={len(images)}  "
          f"duration={sum(durations)/1000:.1f}s  size={size/1024/1024:.2f} MiB")


if __name__ == "__main__":
    main()
