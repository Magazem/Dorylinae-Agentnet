# edge.py DIR y0 y1 x0 x1 [axis=y] : sub-pixel position of the strongest light/dark edge per frame (50 % crossing of the
# crop's averaged profile), and its per-frame step
import sys, glob, numpy as np
from PIL import Image
d, y0, y1, x0, x1 = sys.argv[1], *map(int, sys.argv[2:6]); axis = sys.argv[6] if len(sys.argv) > 6 else 'y'
fs = sorted(glob.glob(d + '/*.png'))
pos = []
for f in fs:
    a = np.asarray(Image.open(f).convert('L'), dtype=np.float64)[y0:y1, x0:x1]
    p = a.mean(1) if axis == 'y' else a.mean(0)
    lo, hi = np.percentile(p, 5), np.percentile(p, 95); mid = (lo + hi) / 2
    g = np.abs(np.diff(p)); i = int(g.argmax())          # strongest step
    # crossing between i and i+1
    a0, a1 = p[i], p[i + 1]; fr = (mid - a0) / (a1 - a0) if a1 != a0 else 0.5
    pos.append(i + fr)
pos = np.array(pos); st = np.diff(pos)
print(f'n={len(pos)} pos {pos[0]+ (y0 if axis=="y" else x0):.2f} → {pos[-1]+(y0 if axis=="y" else x0):.2f}  step mean {st.mean():+.3f} std {st.std():.3f} min {st.min():+.3f} max {st.max():+.3f}')
print('steps', ' '.join(f'{v:+.2f}' for v in st))
