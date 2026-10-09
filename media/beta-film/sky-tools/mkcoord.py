# Coordinate-encoding PNGs: pass X has R = x & 255, G = x >> 8; pass Y the same for y; B = 200 marks a sky pixel.
import sys, numpy as np
from PIL import Image
out = sys.argv[1]
for w, h in [(4032, 2268), (2268, 2836), (4992, 2808), (4608, 2592)]:
    xs = np.arange(w)[None, :].repeat(h, 0); ys = np.arange(h)[:, None].repeat(w, 1)
    for nm, v in (('x', xs), ('y', ys)):
        a = np.zeros((h, w, 3), np.uint8); a[..., 0] = v & 255; a[..., 1] = v >> 8; a[..., 2] = 200
        Image.fromarray(a).save(f'{out}/coord-{w}x{h}-{nm}.png', compress_level=1)
print('ok')
