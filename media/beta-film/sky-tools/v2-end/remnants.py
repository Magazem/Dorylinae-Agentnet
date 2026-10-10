# remnants.py ORIG.png CLEARED.png : changed regions (clearing footprints) that leave a bright star within their reach
import sys, numpy as np
from PIL import Image
from scipy import ndimage as ndi
A = np.asarray(Image.open(sys.argv[1]).convert('RGB')).astype(np.float32)
B = np.asarray(Image.open(sys.argv[2]).convert('RGB')).astype(np.float32)
ch = np.abs(A - B).max(2) > 3
lab, n = ndi.label(ndi.binary_dilation(ch, iterations=3))
L = B @ np.array([0.2126, 0.7152, 0.0722], np.float32)
bg = ndi.uniform_filter(ndi.grey_opening(L, size=(41, 41)), 9)
r = L - bg
starmask = (r > 40)
sl = ndi.find_objects(lab)
out = []
for i, s in enumerate(sl):
    if s is None: continue
    reg = lab[s] == i + 1
    area = int(reg.sum())
    # a remaining star touching the grown region
    near = ndi.binary_dilation(reg, iterations=24) & starmask[s]
    if near.any() and area > 60:
        ys, xs = np.nonzero(near)
        out.append((s[1].start + int(xs.mean()), s[0].start + int(ys.mean()), area, float(r[s][near].max())))
for o in sorted(out, key=lambda o: -o[3]): print('remnant near star at png (%d,%d) footprint %d px, star peak +%.0f' % o)
print(len(out), 'of', n)
