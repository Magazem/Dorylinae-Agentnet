"""Build PNG-space masks (where stars must go) from map/<shot>-<fmt>/ (items.json + coordinate screenshots).
   python masks.py <W> -> masks/<sky>.npy and masks/<sky>-boxes.json (every PNG box with its source)"""
import sys, json, os, glob, numpy as np
sys.path.insert(0, os.path.dirname(__file__))
from fit import fit
W = sys.argv[1]
PAD = 14.0
SW = {'169': (1920, 1080), '45': (1080, 1350)}

def offscreen(r, fw, fh):
    return r[2] < -20 or r[0] > fw + 20 or r[3] < -20 or r[1] > fh + 20

def select(shot, fmt, items, t):
    fw, fh = SW[fmt]
    out = []
    if shot in ('S9', 'S10'):
        for it in items:
            r = it['rect']
            if offscreen(r, fw, fh): continue
            if it['kind'].startswith('svg') and abs(r[0] + r[2]) < 1 and abs(r[1] + r[3]) < 1: continue  # parked at origin
            out.append((it['kind'] + ':' + it['label'][:24], r, PAD))
        if shot == 'S10' and fmt == '169':
            out.append(('tag-new-row (x 1380-1830, y 160-215)', [1380, 160, 1830, 215], 4))
    elif shot == 'S5':
        card = [it for it in items if it['kind'] == 'box' and it['label'] == 'Who'][0]['rect']
        out.append(('Who card (ring)', card, PAD))
        for it in items:
            r = it['rect']
            if it['kind'] == 'svg:line' and card[0] <= r[0] <= card[2] and abs(r[1] - card[3]) < 2:
                out.append(('Who leader', r, PAD))
    elif shot == 'S6':
        for it in items:
            r = it['rect']
            if not it['kind'].startswith('svg'): continue
            cy = (r[1] + r[3]) / 2
            if 600 <= cy <= 625 and r[2] < 260:
                out.append(('trail ' + it['kind'], r, PAD))
    return out

masks, boxes = {}, {}
for d in sorted(glob.glob(f'{W}/map/*-*')):
    shot, fmt = os.path.basename(d).split('-')[:2]
    j = json.load(open(f'{d}/items.json')); sky = json.load(open(f'{d}/sky.json'))
    name = sky['src'].split('/')[-1]
    m = masks.setdefault(name, np.zeros((sky['h'], sky['w']), bool))
    for t in j['times']:
        key = str(int(t)) if float(t).is_integer() else str(t)
        M, (rx, ry, frac) = fit(f'{d}/cx_{t:.3f}.png', f'{d}/cy_{t:.3f}.png')
        assert rx < 0.75 and ry < 0.75, (d, t, rx, ry)
        for label, r, pad in select(shot, fmt, j['items'][key], t):
            xs = [r[0] - pad, r[2] + pad]; ys = [r[1] - pad, r[3] + pad]
            P = np.array([[M[0] @ [x, y, 1], M[1] @ [x, y, 1]] for x in xs for y in ys])
            x0, y0 = np.floor(P.min(0)).astype(int); x1, y1 = np.ceil(P.max(0)).astype(int)
            x0, y0 = max(x0, 0), max(y0, 0); x1, y1 = min(x1, sky['w']), min(y1, sky['h'])
            if x1 <= x0 or y1 <= y0: continue
            box = np.ones((y1 - y0, x1 - x0), bool)
            if label == 'Who card (ring)':
                # stars fully under the opaque card are invisible in S5 and stay for S2 / S8: keep the card's inside
                # (per time: the card locks in, so its inside at one time is its edge at another)
                xs = [r[0] + 3, r[2] - 3]; ys = [r[1] + 3, r[3] - 3]
                P = np.array([[M[0] @ [x, y, 1], M[1] @ [x, y, 1]] for x in xs for y in ys])
                a0, b0 = np.ceil(P.min(0)).astype(int); a1, b1 = np.floor(P.max(0)).astype(int)
                box[max(b0 - y0, 0):max(b1 - y0, 0), max(a0 - x0, 0):max(a1 - x0, 0)] = False
            m[y0:y1, x0:x1] |= box
            boxes.setdefault(name, []).append({'shot': shot, 'fmt': fmt, 't': t, 'label': label, 'screen': r, 'png': [int(x0), int(y0), int(x1), int(y1)]})
os.makedirs(f'{W}/masks', exist_ok=True)
for name in masks:
    m = masks[name]
    np.save(f'{W}/masks/{name}.npy', m)
    json.dump(boxes[name], open(f'{W}/masks/{name}-boxes.json', 'w'))
    print(name, 'mask px', int(m.sum()), f'{100*m.mean():.2f} %', 'boxes', len(boxes[name]))
