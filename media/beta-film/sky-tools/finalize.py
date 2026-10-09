"""Check a de-dusted cleared sky against the current asset: every changed pixel must lie inside the footprint (outermost
circle + 2 PNG px) of a newly left-out star. python finalize.py <new.png> <current.png> <stars.json> <newdrop.json> <mask.npy>"""
import sys, json, numpy as np
from PIL import Image
new_p, cur_p, stars_p, drop_p, mask_p = sys.argv[1:6]
N = np.asarray(Image.open(new_p).convert('RGB')).astype(int); C = np.asarray(Image.open(cur_p).convert('RGB')).astype(int)
D = np.abs(N - C).max(2)
stars = {s['k']: s for s in json.load(open(stars_p))}; drop = json.load(open(drop_p))
m = np.load(mask_p)
H, W = D.shape
yy, xx = np.mgrid[0:H, 0:W]
fp = np.zeros_like(D, bool)
for k in drop:
    s = stars[k]; r = s['ext'] + 2
    x0, x1 = max(int(s['x'] - r), 0), min(int(s['x'] + r) + 2, W); y0, y1 = max(int(s['y'] - r), 0), min(int(s['y'] + r) + 2, H)
    fp[y0:y1, x0:x1] |= (xx[y0:y1, x0:x1] + 0.5 - s['x']) ** 2 + (yy[y0:y1, x0:x1] + 0.5 - s['y']) ** 2 <= r * r
ch = D > 0
print(new_p.split('/')[-1], 'changed px', int(ch.sum()), 'max delta', int(D.max()), '| changed outside footprints', int((ch & ~fp).sum()),
      '| changed outside mask', int((ch & ~m).sum()), f'(max delta there {int(D[ch & ~m].max()) if (ch & ~m).any() else 0})')
