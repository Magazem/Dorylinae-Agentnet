"""Stars near glyphs: for each check time, ink = |frame - sky-only frame| (text, cards, graphics), and every countable
star of the sky (generator list minus the left-out ones) is mapped to the screen with the coordinate fit.
   python check.py <checkdir> <mapdir> <stars.json> <drop.json|-> [x0,y0,x1,y1 window] -> prints stars whose disc edge is
   within 8 px of ink (excluding stars hidden under opaque ink), plus an image-based peak check."""
import sys, json, os, numpy as np
from PIL import Image
from scipy.ndimage import distance_transform_edt, median_filter, maximum_filter
sys.path.insert(0, os.path.dirname(__file__))
from fit import fit
cdir, mdir, stars_p, drop_p = sys.argv[1:5]
win = [[float(v) for v in w.split(',')] for w in sys.argv[5].split(';')] if len(sys.argv) > 5 else None
stars = json.load(open(stars_p)); drop = set(json.load(open(drop_p))) if drop_p != '-' else set()
left = [s for s in stars if s['k'] not in drop]
tot = 0
for f in sorted(os.listdir(cdir)):
    if not f.startswith('a_'): continue
    t = f[2:-4]
    A = np.asarray(Image.open(f'{cdir}/a_{t}.png').convert('RGB')).astype(int); B = np.asarray(Image.open(f'{cdir}/b_{t}.png').convert('RGB')).astype(int)
    ink = np.abs(A - B).max(2) > 10
    H, Wd = ink.shape
    if win:
        wm = np.zeros_like(ink)
        for w in win: wm[max(int(w[1]), 0):max(int(w[3]), 0), max(int(w[0]), 0):max(int(w[2]), 0)] = True
        ink &= wm
    dist = distance_transform_edt(~ink); depth = distance_transform_edt(ink)
    M, _ = fit(f'{mdir}/cx_{t}.png', f'{mdir}/cy_{t}.png')
    A2 = M[:, :2]; b2 = M[:, 2]; inv = np.linalg.inv(A2); sc = abs(np.linalg.det(A2)) ** 0.5
    near = []
    for s in left:
        sx, sy = inv @ (np.array([s['x'], s['y']]) - b2)
        if not (0 <= sx < Wd and 0 <= sy < H): continue
        xi, yi = int(sx), int(sy)
        cr = s['core'] / sc
        if depth[yi, xi] > cr + 1: continue           # fully under opaque ink (a card or plate)
        d = dist[yi, xi] - cr
        if d < 8: near.append((round(d, 1), round(sx, 1), round(sy, 1), round(cr, 2), s['op'], s['fill']))
    # image check: peaks in the sky-only frame (3x3 max, 21x21 median background) within 8 px of ink
    L = B.mean(2)
    R = L - median_filter(L, 21)
    pk = (R == maximum_filter(R, 5)) & (R > 25) & (dist <= 8) & (depth <= 1)
    ys, xs = np.nonzero(pk)
    print(f'{os.path.basename(cdir)} t={t}: stars within 8 px of ink: {len(near)}', sorted(near)[:12], '| image peaks >25 near ink:', list(zip(xs.tolist(), ys.tolist(), R[ys, xs].round(0).tolist()))[:12])
    tot += len(near)
print('TOTAL', tot)
