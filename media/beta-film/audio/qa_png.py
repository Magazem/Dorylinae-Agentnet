"""QA picture of a mix: waveform (peak, dBFS) over a log-frequency spectrogram, sections and cue onsets marked.
No plotting library needed (numpy + zlib PNG writer).    python -I qa_png.py out/mix-16x9.wav out/report.json 16x9 out/qa-16x9.png
"""
import json, struct, sys, zlib
import numpy as np, soundfile as sf
from scipy.signal import stft

def png(path, rgb):
    h, w, _ = rgb.shape
    raw = b"".join(b"\x00" + rgb[y].astype(np.uint8).tobytes() for y in range(h))
    chunk = lambda t, d: struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
    open(path, "wb").write(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
                           + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))

def cmap(v):                                   # 0..1 -> dark blue / violet / orange / pale yellow
    stops = np.array([[8, 8, 20], [60, 20, 110], [170, 50, 120], [245, 140, 50], [252, 240, 180]], float)
    x = np.clip(v, 0, 1) * (len(stops) - 1); i = np.minimum(x.astype(int), len(stops) - 2); f = (x - i)[..., None]
    return stops[i] * (1 - f) + stops[i + 1] * f

wav, rep, fmt, out = sys.argv[1:5]
t0, t1 = (float(sys.argv[5]), float(sys.argv[6])) if len(sys.argv) > 6 else (0.0, None)   # optional zoom window (s)
x, sr = sf.read(wav)
x = x[int(t0 * sr): int(t1 * sr) if t1 else None]; m = x.mean(1); dur = len(m) / sr
W, HS, HW = 1890, 360, 110                     # 30 px per second for the full film
hop = max(16, int(len(m) / W))
f, t, Z = stft(m, fs=sr, nperseg=4096 if hop >= 1024 else 1024, noverlap=(4096 if hop >= 1024 else 1024) - hop, boundary=None)
P = 20 * np.log10(np.abs(Z) + 1e-9)
fl = np.geomspace(30, 16000, HS)[::-1]
img = np.stack([np.interp(fl, f, P[:, min(j, P.shape[1] - 1)]) for j in range(W)], 1)
img = cmap((img - (img.max() - 90)) / 90)
# waveform strip: per-column peak in dBFS, -60..0
col = np.array_split(np.abs(x).max(1), W)
pk = np.array([20 * np.log10(c.max() + 1e-9) for c in col])
wave = np.full((HW, W, 3), 14.0)
for j, v in enumerate(pk):
    hgt = int(np.clip((v + 60) / 60, 0, 1) * (HW - 10))
    wave[HW - hgt:, j] = [150, 120, 230]
for db_ in (-1, -6, -20, -40):                 # guide lines
    y = HW - int((db_ + 60) / 60 * (HW - 10)); wave[y, ::4] = [90, 90, 90]
canvas = np.concatenate([wave, np.full((4, W, 3), 0.0), img], 0)
r = json.load(open(rep))[fmt]
for s in r["sections"]:                        # section starts: white ticks across both strips
    j = int((s["start"] - t0) * W / dur)
    if 0 <= j < W: canvas[:, j, :] = canvas[:, j, :] * 0.4 + 160
for e in r["events_placed"]:                   # cue onsets: small green marks above the spectrogram
    j = int((e["t_placed"] - t0) * W / dur)
    if 0 <= j < W: canvas[HW: HW + 4, j] = [80, 230, 120]
grid = [100, 1000, 10000]                      # frequency guides
for fq in grid:
    y = HW + 4 + int(np.argmin(np.abs(fl - fq))); canvas[y, ::3] = [200, 200, 200]
png(out, canvas)
print(out, canvas.shape)
