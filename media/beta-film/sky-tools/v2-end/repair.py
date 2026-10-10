"""Repair partial star removals in a cleared sky with texture-preserving patches.
   python -I repair.py <in.png> <out.png> x,y,R [x,y,R ...]      (PNG px)
Each disc (radius R, feathered over 10 px) is replaced by the local low-frequency sky (grey opening 101 px, smoothed: no
stars, no halos) plus the fine grain (dust) of a nearby star-free donor disc of the same size, so the patch keeps the
sky's texture and tone: the whole star cluster at that place (cores, halos, partial remnants) is left out cleanly."""
import sys, numpy as np
from PIL import Image
from scipy import ndimage as ndi
src, dst = sys.argv[1], sys.argv[2]
sites = [tuple(map(int, a.split(','))) for a in sys.argv[3:]]
I = np.asarray(Image.open(src).convert('RGB')).astype(np.float32)
H, W = I.shape[:2]
Lum = I @ np.array([0.2126, 0.7152, 0.0722], np.float32)
out = I.copy()
FE = 10
for (x, y, R) in sites:
    M = R + FE + 260                                  # working window
    y0, y1, x0, x1 = max(y - M, 0), min(y + M, H), max(x - M, 0), min(x + M, W)
    win = I[y0:y1, x0:x1]
    LF = np.stack([ndi.gaussian_filter(ndi.grey_opening(win[..., c], size=(101, 101)), 18) for c in range(3)], -1)
    Lw = Lum[y0:y1, x0:x1]
    r = ndi.gaussian_filter(Lw, 2.0) - ndi.gaussian_filter(ndi.grey_opening(Lw, size=(31, 31)), 6)   # dust specks average out; star cores stay
    HF = win - LF
    cy, cx = y - y0, x - x0
    yy, xx = np.mgrid[0:y1 - y0, 0:x1 - x0]
    rr = np.hypot(yy - cy, xx - cx)
    alpha = np.clip((R + FE - rr) / FE, 0, 1); alpha = 0.5 - 0.5 * np.cos(np.pi * alpha)
    best = None
    for dist in (2.4 * R + 20, 3.2 * R + 30, 4.2 * R + 40):
        for k in range(24):
            a = 2 * np.pi * k / 24
            dy, dx = int(round(dist * np.sin(a))), int(round(dist * np.cos(a)))
            ty, tx = cy + dy, cx + dx
            if ty - R - FE < 0 or tx - R - FE < 0 or ty + R + FE >= y1 - y0 or tx + R + FE >= x1 - x0: continue
            disc = np.hypot(yy - ty, xx - tx) <= R + FE
            if r[disc].max() > 14: continue            # a star in the donor
            score = float(np.abs(LF[disc] - np.roll(np.roll(LF, -dy, 0), -dx, 1)[disc]).mean())
            if best is None or score < best[0]: best = (score, dy, dx)
    if best is None: print('no donor for', (x, y, R)); continue
    _, dy, dx = best
    hf = np.roll(np.roll(HF, -dy, 0), -dx, 1)         # donor grain moved onto the site
    fill = LF + hf
    out[y0:y1, x0:x1] = win * (1 - alpha[..., None]) + fill * alpha[..., None]
    I[y0:y1, x0:x1] = out[y0:y1, x0:x1]              # later sites see the repaired sky
    print('site', (x, y, R), 'donor offset', (dx, dy), 'LF diff %.2f' % best[0])
Image.fromarray(np.clip(np.rint(out), 0, 255).astype(np.uint8)).save(dst, compress_level=6)
