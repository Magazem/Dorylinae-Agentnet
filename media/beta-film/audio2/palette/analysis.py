"""Per-sample measurements for instrument candidates (nobody can listen, so everything is measured).

    measure_file(path, expected_midi=None) -> dict

Metrics (all on the file as delivered, before any processing of ours):
  format     sr, bit depth (subtype), channels, duration
  level      peak dBFS (sample peak), max 50 ms RMS dBFS
  onset      leading silence before the note (first point where the 1 ms envelope reaches peak - 30 dB),
             first-sample magnitude (a non-zero start is a click), DC offset
  attack     10 % -> 90 % rise time of the 1 ms peak envelope
  noise      floor = 5th percentile of the 50 ms RMS over non-silent windows (dBFS), the pre-onset RMS when there
             are >= 10 ms of pre-roll, and SNR = max 50 ms RMS - floor
  tail       T30 decay estimate (time from peak RMS to -30 dB, x2 = T60 estimate), level of the last 20 ms re peak
             (> -50 dB without a fade = truncated), whether the file ends in digital silence
  tuning     octave decided by YIN, then the fundamental partial is tracked frame by frame (Hann, 4x zero-padded FFT,
             parabolic interpolation on log magnitude); median deviation from 12-TET (A4 = 440 Hz) in cents, and the
             10-90 % spread (vibrato / drift)
  stereo     zero-lag L/R correlation over the active part, side/mid energy (dB), L-R balance (dB), mono-sum loss
             (dB, energy of (L+R)/2 vs the mean channel energy), inter-channel delay (cross-correlation peak, ms)
  colour     spectral centroid over the first 400 ms after onset, share of energy above 4 kHz in the first 150 ms
"""
from __future__ import annotations

import re

import numpy as np
import soundfile as sf

NOTE = {"C": 0, "C#": 1, "DB": 1, "D": 2, "D#": 3, "EB": 3, "E": 4, "F": 5, "F#": 6, "GB": 6, "G": 7, "G#": 8,
        "AB": 8, "A": 9, "A#": 10, "BB": 10, "B": 11}


def name_to_midi(name: str) -> int:
    m = re.fullmatch(r"([A-Ga-g])([#b]?)(-?\d)", name)
    if not m:
        raise ValueError(name)
    key = m.group(1).upper() + ("#" if m.group(2) == "#" else ("B" if m.group(2) == "b" else ""))
    return 12 * (int(m.group(3)) + 1) + NOTE[key]


def midi_to_hz(m: float) -> float:
    return 440.0 * 2 ** ((m - 69) / 12)


def db(x: float) -> float:
    return float(20 * np.log10(max(float(x), 1e-12)))


def frame_rms(m: np.ndarray, n: int) -> np.ndarray:
    k = len(m) // n
    if k == 0:
        return np.array([np.sqrt(np.mean(m ** 2))])
    return np.sqrt(np.mean(m[: k * n].reshape(k, n) ** 2, axis=1))


def envelope_1ms(m: np.ndarray, sr: int) -> np.ndarray:
    from scipy.ndimage import maximum_filter1d
    return maximum_filter1d(np.abs(m), size=max(1, sr // 1000), mode="nearest")


def yin_f0(x: np.ndarray, sr: int, fmin: float = 25.0, fmax: float = 4200.0, thresh: float = 0.15) -> float | None:
    """Cumulative-mean-normalised difference (de Cheveigne & Kawahara 2002), FFT autocorrelation."""
    n = len(x)
    tmax = min(int(sr / fmin), n // 2)
    tmin = max(2, int(sr / fmax))
    if tmax <= tmin + 2:
        return None
    x = x - x.mean()
    w = n - tmax
    nfft = 1 << int(np.ceil(np.log2(n + w)))
    X = np.fft.rfft(x, nfft)
    Y = np.fft.rfft(x[:w][::-1], nfft)
    corr = np.fft.irfft(X * Y, nfft)[w - 1: w - 1 + tmax + 1]
    cs = np.concatenate([[0.0], np.cumsum(x ** 2)])
    e0 = cs[w] - cs[0]
    et = cs[np.arange(tmax + 1) + w] - cs[np.arange(tmax + 1)]
    d = e0 + et - 2 * corr
    d[0] = 0
    cm = np.ones_like(d)
    run = np.cumsum(d[1:])
    cm[1:] = d[1:] * np.arange(1, len(d)) / np.maximum(run, 1e-20)
    cand = np.where(cm[tmin:tmax] < thresh)[0]
    if len(cand):
        t = cand[0] + tmin
        while t + 1 < tmax and cm[t + 1] < cm[t]:
            t += 1
    else:
        t = int(np.argmin(cm[tmin:tmax]) + tmin)
        if cm[t] > 0.45:
            return None
    if 1 <= t < len(cm) - 1:
        a, b, c = cm[t - 1], cm[t], cm[t + 1]
        den = a - 2 * b + c
        t = t + (0.5 * (a - c) / den if den != 0 else 0)
    return sr / t


def peak_freq(seg: np.ndarray, sr: int, f_lo: float, f_hi: float, pad: int = 4) -> tuple[float, float]:
    """Frequency and level (dB) of the largest spectral peak in [f_lo, f_hi], parabolic interpolation (log mag)."""
    n = len(seg)
    nfft = 1 << int(np.ceil(np.log2(n * pad)))
    S = np.abs(np.fft.rfft(seg * np.hanning(n), nfft))
    f = np.fft.rfftfreq(nfft, 1 / sr)
    i0, i1 = np.searchsorted(f, [f_lo, f_hi])
    if i1 - i0 < 3:
        return float("nan"), -200.0
    i = i0 + int(np.argmax(S[i0:i1]))
    if 1 <= i < len(S) - 1:
        a, b, c = np.log(S[i - 1] + 1e-20), np.log(S[i] + 1e-20), np.log(S[i + 1] + 1e-20)
        den = a - 2 * b + c
        p = 0.5 * (a - c) / den if den != 0 else 0.0
    else:
        p = 0.0
    return float((i + p) * sr / nfft), db(S[i] / (np.sum(np.hanning(n)) / 2))


def partial_fit(seg: np.ndarray, sr: int, f_t: float, hmax: int = 10) -> dict | None:
    """Fit f_h = h f0 sqrt(1 + B h^2) to the partial peaks (piano inharmonicity). Robust when the fundamental is
    weak (piano bass). Least squares on f_h^2 = f0^2 h^2 + f0^2 B h^4, weighted by partial level."""
    n = len(seg)
    nfft = 1 << int(np.ceil(np.log2(n * 4)))
    S = np.abs(np.fft.rfft(seg * np.hanning(n), nfft))
    f = np.fft.rfftfreq(nfft, 1 / sr)
    Sdb = 20 * np.log10(S + 1e-20)
    top = Sdb[(f > f_t * 0.5) & (f < min(sr / 2 - 100, f_t * (hmax + 0.5)))].max()
    B = 0.0
    pts = []
    for it in range(2):
        pts = []
        for h in range(1, hmax + 1):
            fc = h * f_t * np.sqrt(1 + B * h * h)
            if fc > min(16000, sr / 2 - 200):
                break
            lo, hi = fc * 2 ** (-45 / 1200), fc * 2 ** (45 / 1200)
            i0, i1 = np.searchsorted(f, [lo, hi])
            if i1 - i0 < 3:
                continue
            i = i0 + int(np.argmax(S[i0:i1]))
            if i in (i0, i1 - 1) or Sdb[i] < top - 45:
                continue
            a_, b_, c_ = np.log(S[i - 1] + 1e-20), np.log(S[i] + 1e-20), np.log(S[i + 1] + 1e-20)
            den = a_ - 2 * b_ + c_
            p = 0.5 * (a_ - c_) / den if den != 0 else 0.0
            pts.append((h, (i + p) * sr / nfft, Sdb[i]))
        if len(pts) < 3:
            return None
        H = np.array([q[0] for q in pts], float)
        F = np.array([q[1] for q in pts], float)
        W = 10 ** ((np.array([q[2] for q in pts]) - top) / 40)
        X = np.stack([H ** 2, H ** 4], 1) * W[:, None]
        sol, *_ = np.linalg.lstsq(X, F ** 2 * W, rcond=None)
        if sol[0] <= 0:
            return None
        f0 = float(np.sqrt(sol[0]))
        B = float(max(0.0, sol[1] / sol[0]))
    return {"f0": f0, "B": B, "partials": len(pts)}


def measure_tuning(m: np.ndarray, sr: int, onset: int, expected_midi: float | None, short: bool = False,
                   force_octave: int | None = None) -> dict:
    """Octave by YIN (or forced: the library's own octave convention), then frame-wise fundamental-partial tracking
    around the 12-TET target; an inharmonic partial fit takes over when the fundamental is weak (piano bass).
    A file whose pitch is more than 55 cents from its label is re-labelled to the nearest semitone."""
    out: dict = {}
    start = onset + int((0.015 if short else 0.08) * sr)
    if start >= len(m) - int(0.05 * sr):
        start = onset
    win = m[start: start + int(0.2 * sr)]
    f_yin = yin_f0(win, sr) if len(win) > 512 else None
    out["f_yin"] = f_yin
    if expected_midi is None:
        if not f_yin:
            return out
        expected_midi = round(69 + 12 * np.log2(f_yin / 440))
        out["named"] = False
    f_exp = midi_to_hz(expected_midi)
    semis = 0
    if force_octave is not None:
        k = int(force_octave)
    elif f_yin:
        k = int(np.clip(np.round(np.log2(f_yin / f_exp)), -3, 3))
    else:                                   # no periodicity found: strongest peak among the octave candidates
        lv = []
        seg = win * 1.0
        for kk in range(-2, 3):
            fc = f_exp * 2 ** kk
            if fc > sr / 2 - 500 or fc < 20:
                lv.append(-300.0)
                continue
            lv.append(peak_freq(seg, sr, fc * 2 ** (-0.5 / 12), fc * 2 ** (0.5 / 12))[1])
        k = int(np.argmax(lv)) - 2
        out["octave_by"] = "spectrum"
    # analysis region: from start, up to 1.6 s (0.5 s for short sounds), stop where the envelope fell 45 dB
    region_end = len(m)
    rms = frame_rms(m[start:], max(1, int(0.01 * sr)))
    if len(rms) and rms.max() > 0:
        below = np.where(rms < rms.max() * 10 ** (-45 / 20))[0]
        if len(below):
            region_end = min(region_end, start + int(below[0] * 0.01 * sr))
    region_end = min(region_end, start + int((0.5 if short else 1.6) * sr))

    def track(f_t, half=0.9):
        N = int(max(0.085 * sr, 24 * sr / f_t))
        N = min(N, 1 << 16)
        if region_end - start < N:
            N = max(2048, region_end - start)
        hop = max(N // 2, 1)
        fs = []
        for s0 in range(start, max(start + 1, region_end - N + 1), hop):
            seg = m[s0: s0 + N]
            if len(seg) < N:
                break
            fpk, _ = peak_freq(seg, sr, f_t * 2 ** (-half / 12), f_t * 2 ** (half / 12))
            if np.isfinite(fpk):
                fs.append(fpk)
            if len(fs) >= 40:
                break
        return np.array(fs), N

    target_midi = expected_midi + 12 * k
    f_t = midi_to_hz(target_midi)
    fs, N = track(f_t, 1.45)
    if len(fs):
        c0 = float(np.median(1200 * np.log2(fs / f_t)))
        seg0 = m[start: start + max(N, min(region_end - start, int(0.5 * sr)))]
        strong = (peak_freq(seg0, sr, f_t * 2 ** (-1.45 / 12), f_t * 2 ** (1.45 / 12))[1]
                  - peak_freq(seg0, sr, f_t * 0.5, min(sr / 2 - 1, f_t * 8.5))[1]) > -12
        if abs(c0) > 55 and strong:             # labelled with the wrong note (only judged on a clear fundamental)
            semis = int(np.round(c0 / 100))
            target_midi += semis
            f_t = midi_to_hz(target_midi)
        fs, N = track(f_t)
    out["octave_offset"] = k
    out["label_offset_semitones"] = semis
    out["target_midi"] = int(target_midi)
    if not len(fs):
        return out
    cents = 1200 * np.log2(fs / f_t)
    seg = m[start: start + max(N, min(region_end - start, int(0.5 * sr)))]
    _, l1 = peak_freq(seg, sr, f_t * 0.97, f_t * 1.03)
    _, lmax = peak_freq(seg, sr, f_t * 0.5, min(sr / 2 - 1, f_t * 8.5))
    out["fund_rel_db"] = float(l1 - lmax)
    out["frames"] = int(len(fs))
    out["cents_fund"] = float(np.median(cents))
    out["cents_spread"] = float(np.percentile(cents, 90) - np.percentile(cents, 10)) if len(cents) > 2 else 0.0
    out["cents"] = out["cents_fund"]
    out["method"] = "fundamental"
    if not short:
        pf = partial_fit(seg, sr, f_t)
        if pf:
            out["cents_fit"] = float(1200 * np.log2(pf["f0"] / f_t))
            out["B"] = pf["B"]
            out["fit_partials"] = pf["partials"]
            if out["fund_rel_db"] < -12:
                out["cents"] = out["cents_fit"]
                out["method"] = "partial-fit"
    out["f0"] = float(f_t * 2 ** (out["cents"] / 1200))
    return out


def measure_file(path, expected_midi: float | None = None, short: bool = False, pitched: bool = True,
                 force_octave: int | None = None) -> dict:
    info = sf.info(str(path))
    x, sr = sf.read(str(path), dtype="float64", always_2d=True)
    ch = x.shape[1]
    m = x.mean(axis=1)
    r: dict = {"file": str(path), "sr": sr, "subtype": info.subtype, "channels": ch, "dur": len(x) / sr}
    peak = float(np.abs(x).max())
    r["peak_dbfs"] = db(peak)
    if peak <= 0:
        r["empty"] = True
        return r
    env = envelope_1ms(m, sr)
    epk = env.max()
    onset = int(np.argmax(env >= epk * 10 ** (-30 / 20)))
    r["onset_ms"] = 1000 * onset / sr
    r["first_sample_db"] = db(np.abs(x[0]).max()) - r["peak_dbfs"] if np.abs(x[0]).max() > 0 else -200.0
    r["dc_db"] = db(np.abs(x.mean(axis=0)).max())
    i_pk = int(np.argmax(env))
    seg = env[: i_pk + 1]
    i10 = int(np.argmax(seg >= 0.1 * epk))
    i90 = int(np.argmax(seg >= 0.9 * epk))
    r["rise_ms"] = 1000 * (i90 - i10) / sr
    r["peak_at_ms"] = 1000 * i_pk / sr
    w = max(1, int(0.05 * sr))
    rms = frame_rms(m, w)
    nz = rms[rms > 10 ** (-140 / 20)]
    r["max_rms_dbfs"] = db(rms.max())
    r["floor_dbfs"] = db(np.percentile(nz, 5)) if len(nz) else -200.0
    r["snr_db"] = r["max_rms_dbfs"] - r["floor_dbfs"]
    pre = m[: max(0, onset - int(0.002 * sr))]
    r["preroll_ms"] = 1000 * len(pre) / sr
    r["pre_rms_dbfs"] = db(np.sqrt(np.mean(pre ** 2))) if len(pre) >= int(0.01 * sr) else None
    # tail
    i_rms_pk = int(np.argmax(rms))
    after = rms[i_rms_pk:]
    t30 = np.where(after < rms.max() * 10 ** (-30 / 20))[0]
    r["t30_s"] = float(t30[0] * w / sr) if len(t30) else None
    t60 = np.where(after < rms.max() * 10 ** (-60 / 20))[0]
    r["t60_s"] = float(t60[0] * w / sr) if len(t60) else None
    last = x[-max(1, int(0.02 * sr)):]
    r["end_db_re_peak"] = db(np.sqrt(np.mean(last ** 2))) - r["max_rms_dbfs"] if np.any(last) else -200.0
    r["ends_in_zero"] = bool(np.all(np.abs(x[-8:]) < 1e-7))
    # stereo
    if ch >= 2:
        act = slice(onset, min(len(x), onset + int(1.5 * sr)))
        L, R = x[act, 0], x[act, 1]
        eL, eR = np.sum(L ** 2), np.sum(R ** 2)
        if eL > 0 and eR > 0:
            r["lr_corr"] = float(np.sum(L * R) / np.sqrt(eL * eR))
            M, S = (L + R) / 2, (L - R) / 2
            r["side_mid_db"] = 10 * np.log10(max(np.sum(S ** 2), 1e-30) / max(np.sum(M ** 2), 1e-30))
            r["balance_db"] = 10 * np.log10(eL / eR)
            r["mono_loss_db"] = 10 * np.log10(np.sum(M ** 2) / ((eL + eR) / 2))
            # inter-channel delay within +/- 3 ms
            n = min(len(L), int(0.3 * sr))
            lag = int(0.003 * sr)
            a, b = L[:n], R[:n]
            nfft = 1 << int(np.ceil(np.log2(2 * n)))
            cc = np.fft.irfft(np.fft.rfft(a, nfft) * np.conj(np.fft.rfft(b, nfft)), nfft)
            cc = np.concatenate([cc[-lag:], cc[: lag + 1]])
            r["ic_delay_ms"] = 1000 * (int(np.argmax(np.abs(cc))) - lag) / sr
    # colour
    s = m[onset: onset + int(0.4 * sr)]
    if len(s) > 256:
        S = np.abs(np.fft.rfft(s * np.hanning(len(s)))) ** 2
        f = np.fft.rfftfreq(len(s), 1 / sr)
        r["centroid_hz"] = float(np.sum(f * S) / max(np.sum(S), 1e-30))
    s = m[onset: onset + int(0.15 * sr)]
    if len(s) > 256:
        S = np.abs(np.fft.rfft(s * np.hanning(len(s)))) ** 2
        f = np.fft.rfftfreq(len(s), 1 / sr)
        r["hf4k_share_db"] = 10 * np.log10(max(np.sum(S[f > 4000]), 1e-30) / max(np.sum(S), 1e-30))
    if pitched:
        r["tuning"] = measure_tuning(m, sr, onset, expected_midi, short=short, force_octave=force_octave)
    return r
