"""Generated stereo impulse responses (no third-party IR, so no licence question) and their measurement.

    ir = make_ir(**ROOMS["chamber"])          # (n, 2) float64 at 48 kHz, peak-normalised
    m  = measure_ir(ir)                       # per-octave EDT / T30, IACC, C80, echo density, spectrum

Construction:
  * early reflections: image sources of a shoebox room up to order 3, seen by a near-coincident pair of cardioids
    (ORTF-like: 17 cm apart, +/-55 deg), each reflection attenuated by sqrt(1 - alpha) per bounce and 1/r,
    and darkened per bounce (one-pole low-pass, cutoff falls with order), high-passed at 250 Hz (discrete
    reflections only where a room has them): a real room's first 80 ms
  * late tail: Gaussian noise in octave bands (63 Hz .. 16 kHz, 4th-order Butterworth band-passes run forward and
    backward, so the bands sum flat with zero phase), each band decaying with its own T60, with a band-dependent
    common L/R component so the interaural coherence falls with frequency like a real hall (high in the bass,
    low above 1 kHz): mono-compatible bass and a wide top. The tail fades in over the mixing time.
  * high-pass 25 Hz, exact zero at both ends, deterministic seed
"""
from __future__ import annotations

import numpy as np
from scipy.signal import butter, sosfilt, sosfiltfilt

SR = 48000
BANDS = [63, 125, 250, 500, 1000, 2000, 4000, 8000, 16000]

ROOMS = {
    # warm small hall / scoring room: for the piano and mallets (presence, not wash)
    "chamber": dict(dims=(14.0, 9.5, 6.0), src=(4.2, 3.1, 1.6), mic=(9.8, 5.2, 2.2), alpha=0.22,
                    t60={63: 1.75, 125: 1.7, 250: 1.6, 500: 1.45, 1000: 1.35, 2000: 1.15, 4000: 0.9, 8000: 0.6,
                         16000: 0.35},
                    coh={63: 0.85, 125: 0.7, 250: 0.45, 500: 0.25, 1000: 0.12, 2000: 0.06, 4000: 0.04,
                         8000: 0.03, 16000: 0.03},
                    predelay=0.008, mix_time=0.075, er_db=-2.0, length=3.2, seed=84),
    # large hall: for the bed and long tails (send only)
    "hall": dict(dims=(32.0, 21.0, 13.0), src=(9.0, 7.5, 2.0), mic=(21.0, 10.5, 3.5), alpha=0.16,
                 t60={63: 3.3, 125: 3.2, 250: 3.0, 500: 2.8, 1000: 2.6, 2000: 2.2, 4000: 1.7, 8000: 1.1,
                      16000: 0.6},
                 coh={63: 0.8, 125: 0.6, 250: 0.35, 500: 0.2, 1000: 0.1, 2000: 0.05, 4000: 0.03, 8000: 0.03,
                      16000: 0.03},
                 predelay=0.022, mix_time=0.12, er_db=-6.0, length=6.0, seed=57),
}


def _band_sos(fc: float, sr: int):
    lo, hi = fc / np.sqrt(2), min(fc * np.sqrt(2), sr / 2 * 0.98)
    if fc == BANDS[0]:
        return butter(4, hi, "low", fs=sr, output="sos")
    if fc == BANDS[-1]:
        return butter(4, lo, "high", fs=sr, output="sos")
    return butter(4, [lo, hi], "band", fs=sr, output="sos")


def early_reflections(dims, src, mic, alpha, sr=SR, order=3, length=0.12):
    """Image-source reflections into an ORTF-like cardioid pair. Returns (n, 2)."""
    Lx, Ly, Lz = dims
    n = int(length * sr)
    out = np.zeros((n, 2))
    caps = np.array(mic, float)
    spacing = 0.17
    ang = np.deg2rad(55)
    # the pair faces the source (in the x-y plane)
    d0 = np.array(src, float) - caps
    face = np.arctan2(d0[1], d0[0])
    perp = np.array([-np.sin(face), np.cos(face), 0.0])
    pos = [caps + perp * spacing / 2, caps - perp * spacing / 2]
    aims = [face + ang, face - ang]
    c = 343.0
    refl = np.sqrt(1 - alpha)
    per_order = {k: [] for k in range(order + 1)}
    rng = range(-order, order + 1)
    for i in rng:
        for j in rng:
            for k in rng:
                o = abs(i) + abs(j) + abs(k)
                if o > order:
                    continue
                x = (i * Lx + (src[0] if i % 2 == 0 else Lx - src[0]))
                y = (j * Ly + (src[1] if j % 2 == 0 else Ly - src[1]))
                z = (k * Lz + (src[2] if k % 2 == 0 else Lz - src[2]))
                per_order[o].append((np.array([x, y, z]), refl ** o))
    t_direct = np.linalg.norm(np.array(src) - caps) / c
    for o, lst in per_order.items():
        buf = np.zeros((n, 2))
        for p, g in lst:
            for ch in range(2):
                v = p - pos[ch]
                r = np.linalg.norm(v)
                t = r / c - t_direct                      # direct sound at t = 0
                idx = int(round(t * sr))
                if idx < 0 or idx >= n:
                    continue
                az = np.arctan2(v[1], v[0])
                el = np.arcsin(v[2] / r)
                cosang = np.cos(el) * np.cos(az - aims[ch])
                card = 0.5 + 0.5 * cosang
                buf[idx, ch] += g * card / max(r, 1.0)
        if o >= 1:                                         # each bounce darker
            fc = max(1500.0, 14000.0 / (1.6 ** (o - 1)))
            buf = sosfilt(butter(1, fc, "low", fs=sr, output="sos"), buf, axis=0)
        out += buf
    return out


def make_ir(dims, src, mic, alpha, t60, coh, predelay, mix_time, er_db, length, seed, sr=SR, direct=False):
    rng = np.random.default_rng(seed)
    n = int(length * sr)
    t = np.arange(n) / sr
    tail = np.zeros((n, 2))
    for fc in BANDS:
        sos = _band_sos(fc, sr)
        common = rng.standard_normal(n)
        u1, u2 = rng.standard_normal(n), rng.standard_normal(n)
        rho = coh[fc]
        a, b = np.sqrt(rho), np.sqrt(1 - rho)
        nl, nr = a * common + b * u1, a * common + b * u2
        band = sosfiltfilt(sos, np.stack([nl, nr], 1), axis=0)
        band *= np.exp(-6.91 * t / t60[fc])[:, None]
        tail += band
    # density builds over the mixing time
    ramp = np.clip(t / mix_time, 0, 1)
    tail *= (0.5 - 0.5 * np.cos(np.pi * ramp))[:, None]
    er = early_reflections(dims, src, mic, alpha, sr=sr)
    # discrete reflections only above ~250 Hz (below that a room's early field is modal, not discrete; full-band
    # spikes made the bass EDT collapse to 10-50 ms in the first measurement)
    er = sosfiltfilt(butter(2, 250, "high", fs=sr, output="sos"), er, axis=0)
    if not direct:
        er[:int(0.0015 * sr)] = 0.0                       # wet-only IR: no direct sound
    e_tail = np.sqrt(np.mean(tail[: int(0.2 * sr)] ** 2))
    e_er = np.sqrt(np.mean(er[: int(0.2 * sr)] ** 2)) + 1e-20
    er *= (e_tail / e_er) * 10 ** (er_db / 20)
    ir = tail.copy()
    ir[: len(er)] += er
    pd = int(predelay * sr)
    ir = np.vstack([np.zeros((pd, 2)), ir])[:n]
    ir = sosfiltfilt(butter(2, 25, "high", fs=sr, output="sos"), ir, axis=0)
    f = int(0.25 * sr)
    ir[-f:] *= (0.5 + 0.5 * np.cos(np.linspace(0, np.pi, f)))[:, None]
    ir[0] = 0.0
    ir[-1] = 0.0
    return ir / np.abs(ir).max()


# ------------------------------------------------------------------------------------------- measurement
def _schroeder_db(h2: np.ndarray) -> np.ndarray:
    e = np.cumsum(h2[::-1])[::-1]
    return 10 * np.log10(e / e[0] + 1e-30)


def _decay_time(edc: np.ndarray, sr: int, lo: float, hi: float) -> float | None:
    i0 = np.argmax(edc <= lo)
    i1 = np.argmax(edc <= hi)
    if i1 <= i0 or edc[i1] > hi:
        return None
    tt = np.arange(i0, i1) / sr
    slope, _ = np.polyfit(tt, edc[i0:i1], 1)
    return float(-60.0 / slope) if slope < 0 else None


def measure_ir(ir: np.ndarray, sr: int = SR) -> dict:
    out = {"length_s": len(ir) / sr}
    onset = int(np.argmax(np.abs(ir).max(1) > np.abs(ir).max() * 10 ** (-40 / 20)))
    out["predelay_ms"] = 1000 * onset / sr
    h = ir[onset:]
    bands = {}
    for fc in BANDS[:-1]:
        sos = _band_sos(fc, sr)
        b = sosfiltfilt(sos, h, axis=0)
        h2 = np.sum(b ** 2, axis=1)
        edc = _schroeder_db(h2)
        L, R = b[int(0.08 * sr):, 0], b[int(0.08 * sr):, 1]
        iacc = float(np.sum(L * R) / np.sqrt(np.sum(L ** 2) * np.sum(R ** 2)))
        bands[fc] = {"EDT": _decay_time(edc, sr, -0.1, -10), "T20": _decay_time(edc, sr, -5, -25),
                     "T30": _decay_time(edc, sr, -5, -35), "late_coherence": iacc}
    out["bands"] = bands
    h2 = np.sum(h ** 2, axis=1)
    n80 = int(0.08 * sr)
    out["C80_dB"] = float(10 * np.log10(h2[:n80].sum() / h2[n80:].sum()))
    # normalised echo density (Abel & Huang 2006): fraction of samples outside 1 std in a 20 ms window / erfc(1/sqrt2)
    w = int(0.02 * sr)
    m = h.mean(1)
    ned = []
    for s0 in range(0, min(len(m) - w, int(0.5 * sr)), w // 2):
        seg = m[s0: s0 + w]
        sd = np.sqrt(np.mean(seg ** 2))
        ned.append(np.mean(np.abs(seg) > sd) / 0.3173)
    ned = np.array(ned)
    out["echo_density_at_ms"] = {int(1000 * i * (w // 2) / sr): round(float(v), 2) for i, v in enumerate(ned)
                                 if i % 4 == 0}
    out["echo_density_time_to_0.9_ms"] = float(1000 * np.argmax(ned > 0.9) * (w // 2) / sr)
    # tail spectrum, 1/3 octave smoothness (std of dB across 100 Hz..10 kHz after removing a linear tilt)
    seg = m[int(0.1 * sr): int(0.6 * sr)]
    S = 20 * np.log10(np.abs(np.fft.rfft(seg * np.hanning(len(seg)))) + 1e-12)
    f = np.fft.rfftfreq(len(seg), 1 / sr)
    edges = np.geomspace(100, 10000, 21)
    lv = np.array([S[(f >= a) & (f < b)].mean() for a, b in zip(edges[:-1], edges[1:])])
    x = np.log2(np.sqrt(edges[:-1] * edges[1:]))
    p = np.polyfit(x, lv, 1)
    out["tail_tilt_db_per_oct"] = float(p[0])
    out["tail_third_oct_ripple_db"] = float(np.std(lv - np.polyval(p, x)))
    out["dc"] = float(np.abs(ir.mean(0)).max())
    out["ends_zero"] = bool(np.all(ir[-1] == 0) and np.all(ir[0] == 0))
    return out
