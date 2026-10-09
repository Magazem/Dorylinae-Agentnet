"""Stars to leave out: every countable star whose core disc (or, for a bright star, the inner half of its halo) comes
within 1 PNG px of the mask (the padded text / graphic boxes mapped into the PNG).
   python drops.py <W> <sky-name.png> <stars.json> <out-drop.json>"""
import sys, json, numpy as np
from scipy.ndimage import distance_transform_edt
W, name, stars_p, out_p = sys.argv[1:5]
m = np.load(f'{W}/masks/{name}.npy')
dist = distance_transform_edt(~m)            # PNG px from each pixel to the nearest mask pixel
H, Wd = m.shape
stars = json.load(open(stars_p))
drop, rows = [], []
for s in stars:
    x, y = s['x'], s['y']
    xi, yi = int(np.clip(round(x - 0.5), 0, Wd - 1)), int(np.clip(round(y - 0.5), 0, H - 1))
    reach = s['core'] + 1
    if s['ext'] > s['core']: reach = max(reach, 0.5 * s['ext'])
    if dist[yi, xi] <= reach:
        drop.append(s['k']); rows.append(s)
json.dump(drop, open(out_p, 'w'))
json.dump(rows, open(out_p.replace('.json', '-stars.json'), 'w'))
print(name, 'stars', len(stars), 'dropped', len(drop), 'halo', sum(1 for s in rows if s['ext'] > s['core']),
      'core r', np.percentile([s['core'] for s in rows], [0, 50, 90, 100]).round(2) if rows else '')
