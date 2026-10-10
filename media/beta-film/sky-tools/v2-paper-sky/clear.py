"""Leave out the stars that touch the v2 zones (paper rest edges + 10 px, trail lanes + dot radius + 10 px) in a sky PNG.
   python -I clear.py <sky.png> <zones.json> <sky name> <out.png> [peak=50] [min size=5]
A star is a compact bright blob over the local background (grey opening 21 px, smoothed); every blob of at least `min size`
px whose peak stands >= peak levels above it and whose pixels reach into a zone is replaced, over its footprint (core and
soft halo + 2 px, feathered), by that background (per channel). Nebula, fine dust and every other star are untouched."""
import sys, json, numpy as np
from PIL import Image
from scipy import ndimage as ndi
sky_p, zones_p, name, out_p = sys.argv[1:5]
PEAK = float(sys.argv[5]) if len(sys.argv) > 5 else 50.0
MINSZ = int(sys.argv[6]) if len(sys.argv) > 6 else 5
S = np.asarray(Image.open(sky_p).convert('RGB')).astype(np.float32)
Hh, Ww = S.shape[:2]
mask = np.zeros((Hh, Ww), bool)
for z in json.load(open(zones_p)):
    if z['sky'] != name: continue
    x0, y0, x1, y1 = z['outer']
    xa, ya, xb, yb = max(int(np.floor(x0)), 0), max(int(np.floor(y0)), 0), min(int(np.ceil(x1)), Ww), min(int(np.ceil(y1)), Hh)
    if xa >= xb or ya >= yb: continue
    if z['kind'] == 'band': mask[ya:yb, xa:xb] = True; continue
    sub = np.ones((yb - ya, xb - xa), bool)
    ix0, iy0, ix1, iy1 = z['inner']
    ia, ja, ib, jb = int(np.ceil(ix0)) - xa, int(np.ceil(iy0)) - ya, int(np.floor(ix1)) - xa, int(np.floor(iy1)) - ya
    if ia < ib and ja < jb: sub[max(ja, 0):max(jb, 0), max(ia, 0):max(ib, 0)] = False
    mask[ya:yb, xa:xb] |= sub
L = S @ np.array([0.2126, 0.7152, 0.0722], np.float32)
bg = ndi.uniform_filter(ndi.grey_opening(L, size=(21, 21)), 7)
r = L - bg
lab, n = ndi.label(r > 6)
peaks = ndi.maximum(r, lab, index=np.arange(1, n + 1))
inzone = ndi.maximum(mask.astype(np.uint8), lab, index=np.arange(1, n + 1)) > 0
sizes = ndi.sum(np.ones_like(r), lab, index=np.arange(1, n + 1))
keep_ids = np.nonzero((peaks >= PEAK) & inzone & (sizes >= MINSZ) & (sizes <= 900))[0] + 1
seeds = np.isin(lab, keep_ids)
big = np.isin(lab, keep_ids[sizes[keep_ids - 1] >= 30])
# a removed star's footprint: its core grown through its soft halo, plus 2 px; the replacement is feathered over ~1.5 px
# so no ghost disc or hard edge is left. Small stars: halo r > 1 over the 21 px background, at most 14 px. Big (halo)
# stars: their glow is wider than 21 px, so they use an 81 px background and grow through r > 1.5 up to 48 px.
L2 = ndi.uniform_filter(ndi.grey_opening(L, size=(81, 81)), 15)
fp_s = ndi.binary_dilation(seeds & ~big, iterations=14, mask=r > 1.0) | ndi.binary_dilation(seeds & ~big, iterations=2)
fp_b = ndi.binary_dilation(big, iterations=48, mask=(L - L2) > 1.5) | ndi.binary_dilation(big, iterations=3)
out = S.copy()
for fp, size, sm in ((fp_s, 21, 7), (fp_b, 81, 15)):
    alpha = np.clip(ndi.gaussian_filter(fp.astype(np.float32), 1.2 if size == 21 else 3.0) * 1.6, 0, 1)
    ys, xs = np.nonzero(alpha > 0.01)
    if not len(ys): continue
    y0, y1, x0, x1 = max(ys.min() - size, 0), ys.max() + 1 + size, max(xs.min() - size, 0), xs.max() + 1 + size
    al = alpha[y0:y1, x0:x1]
    for ch in range(3):
        src = out[y0:y1, x0:x1, ch]
        bgc = np.minimum(src, ndi.uniform_filter(ndi.grey_opening(src, size=(size, size)), sm))
        out[y0:y1, x0:x1, ch] = src * (1 - al) + bgc * al
Image.fromarray(np.clip(np.rint(out), 0, 255).astype(np.uint8)).save(out_p, compress_level=6)
print(name, 'zone px', int(mask.sum()), 'stars left out', len(keep_ids), 'changed px', int((np.abs(out - S).max(2) > 0).sum()))
