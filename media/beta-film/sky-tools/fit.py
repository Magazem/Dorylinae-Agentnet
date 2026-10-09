import sys, json, numpy as np
from PIL import Image
def decode(cx, cy):
    a = np.asarray(Image.open(cx).convert('RGB')).astype(np.int64); b = np.asarray(Image.open(cy).convert('RGB')).astype(np.int64)
    valid = (a[..., 2] == 200) & (b[..., 2] == 200)
    X = a[..., 0] + 256 * a[..., 1]; Y = b[..., 0] + 256 * b[..., 1]
    return X, Y, valid
def fit(cx, cy, step=3):
    X, Y, v = decode(cx, cy)
    H, W = v.shape
    yy, xx = np.mgrid[0:H:step, 0:W:step]
    m = v[::step, ::step]
    sx = xx[m] + 0.5; sy = yy[m] + 0.5
    A = np.stack([sx, sy, np.ones_like(sx)], 1)
    px = X[::step, ::step][m] + 0.5; py = Y[::step, ::step][m] + 0.5
    cxv, *_ = np.linalg.lstsq(A, px, rcond=None); cyv, *_ = np.linalg.lstsq(A, py, rcond=None)
    rx = A @ cxv - px; ry = A @ cyv - py
    return np.stack([cxv, cyv]), (np.abs(rx).max(), np.abs(ry).max(), m.mean())
if __name__ == '__main__':
    M, info = fit(sys.argv[1], sys.argv[2])
    print(M, info)
