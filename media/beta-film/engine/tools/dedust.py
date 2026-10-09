"""Reduce the deep field's fine dust in a sky PNG, keeping nebula, stars and vignette.

    python3 -I dedust.py <sky.png> <dust-only.png> <out.png> [keep=0.35]

dust-only.png is the same sky's dust layers rendered white on black (brand-gen film-gen/dust-only.ts, same seed and
size). Dust is composited as white with alpha a = D/255 over the sky below it, so the sky under the dust is
U = (S - D) / (1 - a); the output keeps `keep` of the dust: O = U + keep * (S - U).
It first checks that the dust layer really belongs to this sky (correlation of D with the sky's fine detail), so a
wrong seed can never stamp a foreign pattern into the image.
"""
import sys
import numpy as np
from PIL import Image

sky_p, dust_p, out_p = sys.argv[1:4]
keep = float(sys.argv[4]) if len(sys.argv) > 4 else 0.35
S = np.asarray(Image.open(sky_p).convert('RGB'), dtype=np.float64)
D = np.asarray(Image.open(dust_p).convert('L'), dtype=np.float64)
assert S.shape[:2] == D.shape, (S.shape, D.shape)
# match check: fine detail of the sky (pixel minus its 3x3 box mean) against the dust
L = S.mean(axis=2)
pad = np.pad(L, 1, mode='edge')
box = sum(pad[1 + dy:1 + dy + L.shape[0], 1 + dx:1 + dx + L.shape[1]] for dy in (-1, 0, 1) for dx in (-1, 0, 1)) / 9
fine = L - box
m = D > 8
corr = np.corrcoef(fine[m].ravel()[::7], D[m].ravel()[::7])[0, 1] if m.any() else 0
print(f'{sky_p.split("/")[-1]}: dust pixels {m.mean()*100:.1f} %, correlation {corr:.3f}')
if corr < 0.3:
    sys.exit(f'dust layer does not match this sky (corr {corr:.3f}); refusing')
a = np.clip(D / 255.0, 0, 0.98)[..., None]
U = np.clip((S - D[..., None]) / (1 - a), 0, 255)
O = U + keep * (S - U)
Image.fromarray(np.clip(np.round(O), 0, 255).astype(np.uint8)).save(out_p, compress_level=6)
print('wrote', out_p)
