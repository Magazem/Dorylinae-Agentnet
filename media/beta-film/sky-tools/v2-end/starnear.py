# starnear.py WITH.png WITHOUT.png [radius=40]: stars (bright local peaks of the bare sky) near the text (pixels that differ)
import sys, numpy as np
from PIL import Image
from scipy import ndimage as nd
a = np.asarray(Image.open(sys.argv[1]).convert('RGB'), dtype=np.float64)
b = np.asarray(Image.open(sys.argv[2]).convert('RGB'), dtype=np.float64)
R = int(sys.argv[3]) if len(sys.argv) > 3 else 40
txt = np.abs(a - b).max(2) > 12
lum = b.mean(2)
bg = nd.median_filter(lum, size=9)
peak = (lum - bg > float(sys.argv[4]) if len(sys.argv) > 4 else lum - bg > 40) & (lum == nd.maximum_filter(lum, size=5))
dist = nd.distance_transform_edt(~txt)
ys, xs = np.nonzero(peak & (dist <= R))
for y, x in sorted(zip(ys, xs), key=lambda p: dist[p]):
    print(f'star at ({x},{y}) dist {dist[y,x]:.1f}px  lum {lum[y,x]:.0f} (+{lum[y,x]-bg[y,x]:.0f} over local)')
print('text px', int(txt.sum()), 'stars within', R, ':', len(ys))
