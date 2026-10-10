# bites.py ORIG CLEARED : stars whose core or halo was changed only in part by a clearing (partial removals)
import sys, numpy as np
from PIL import Image
from scipy import ndimage as ndi
A = np.asarray(Image.open(sys.argv[1]).convert('RGB')).astype(np.float32)
B = np.asarray(Image.open(sys.argv[2]).convert('RGB')).astype(np.float32)
W = np.array([0.2126, 0.7152, 0.0722], np.float32)
LA = A @ W
bg = ndi.uniform_filter(ndi.grey_opening(LA, size=(41, 41)), 9)
r = LA - bg
d = np.abs(A - B).max(2)
lab, n = ndi.label(r > 40)
for i, s in enumerate(ndi.find_objects(lab)):
    core = lab[s] == i + 1
    sz = int(core.sum())
    if sz < 12: continue
    cy, cx = [int(v.mean()) + o.start for v, o in zip(np.nonzero(core), s)]
    fc = float((d[s][core] > 8).mean())
    # halo ring: 1..3 core radii around the core
    rad = max(3, int(np.sqrt(sz / np.pi)))
    H_, W_ = LA.shape; y0, y1, x0, x1 = max(cy - 4 * rad, 0), min(cy + 4 * rad + 1, H_), max(cx - 4 * rad, 0), min(cx + 4 * rad + 1, W_)
    yy, xx = np.mgrid[y0:y1, x0:x1]; rr = np.hypot(yy - cy, xx - cx)
    ring = (rr > 1.3 * rad) & (rr < 3.5 * rad)
    dh = d[y0:y1, x0:x1][ring]; hal = r[y0:y1, x0:x1][ring]
    fh = float(((dh > 3) & (hal > 3)).mean())
    if (0.04 < fc < 0.9) or (fc < 0.04 and fh > 0.08):
        print(f'star ({cx},{cy}) size {sz} core changed {fc:.2f} halo changed {fh:.2f}')
