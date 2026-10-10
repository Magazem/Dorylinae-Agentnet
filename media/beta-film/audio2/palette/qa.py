"""Measure the palette demo (nobody can listen): loudness, true peak, clicks, onsets, tuning, mono, spectrum, balance.

    $PY -I qa.py [out/palette-demo.wav]      # -> out/demo-qa.json, printed summary
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

import numpy as np
import pyloudnorm as pyln
import soundfile as sf
from scipy.ndimage import maximum_filter1d
from scipy.signal import butter, sosfilt, sosfiltfilt

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from analysis import midi_to_hz, peak_freq  # noqa: E402
import instruments as I  # noqa: E402

OUT = HERE / "out"


def ebur128(path: Path) -> dict:
    p = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-af", "ebur128=peak=true",
                        "-f", "null", "-"], capture_output=True, text=True)
    s = p.stderr[p.stderr.rfind("Summary:"):]
    g = lambda k: float(re.search(k + r":\s+(-?[\d.]+)", s).group(1))
    return {"I_LUFS": g("I"), "LRA_LU": g("LRA"), "true_peak_dbtp": g("Peak")}


def momentary(x: np.ndarray, sr: int, win: float = 0.4, hop: float = 0.1) -> tuple[np.ndarray, np.ndarray]:
    """K-weighted momentary loudness (LUFS) per window (pyloudnorm's K filter, no gating)."""
    meter = pyln.Meter(sr)
    y = x.copy()
    for f in meter._filters.values():
        y = f.apply_filter(y) if hasattr(f, "apply_filter") else y
    w, h = int(win * sr), int(hop * sr)
    t, L = [], []
    for s0 in range(0, len(y) - w, h):
        ms = np.mean(y[s0: s0 + w] ** 2, axis=0).sum()
        t.append((s0 + w / 2) / sr)
        L.append(-0.691 + 10 * np.log10(ms + 1e-20))
    return np.array(t), np.array(L)


def clicks(x: np.ndarray, sr: int, onsets: list[float]) -> list[dict]:
    """Discontinuities: the 2nd difference of a 6 kHz high-passed signal against its local (50 ms) median level.
    A struck note's own attack is expected in [t - 5 ms, t + 30 ms] and is not reported."""
    hp = sosfilt(butter(4, 6000, "high", fs=sr, output="sos"), x.mean(1))
    d2 = np.abs(np.diff(hp, 2))
    w = int(0.05 * sr)
    k = len(d2) // w
    loc = np.median(d2[: k * w].reshape(k, w), axis=1)
    loc = np.repeat(loc, w)
    loc = np.concatenate([loc, np.full(len(d2) - len(loc), loc[-1] if len(loc) else 1e-9)])
    peak = np.abs(x).max()
    hits = np.where((d2 > 30 * (loc + 1e-9)) & (d2 > peak * 10 ** (-70 / 20)))[0]
    # a struck note's own attack (with its pre-noise) is expected in [t - 5 ms, t + 30 ms]
    out, last = [], -1
    on = np.array(onsets)
    for i in hits:
        if last >= 0 and i - last < int(0.01 * sr):
            continue
        last = i
        t = i / sr
        if len(on) and np.any((t > on - 0.005) & (t < on + 0.030)):
            continue
        out.append({"t": round(t, 4), "ratio_db": round(20 * np.log10(d2[i] / (loc[i] + 1e-12)), 1)})
    return out


def detect_onsets(x: np.ndarray, sr: int, events: list[float], all_times: list[float]) -> list[dict]:
    """Attack-point check on isolated events (no other event in the stem within -0.15/+0.25 s) whose stem is
    quiet just before (level 25..5 ms before t at least 20 dB under the event's own peak): the first crossing of
    peak - 15 dB (the sampler's alignment point) of the 1 ms envelope in [t - 25 ms, t + 300 ms]. Error in ms."""
    m = x.mean(1)
    env = maximum_filter1d(np.abs(m), size=max(1, sr // 1000))
    res = []
    at = np.array(sorted(all_times))
    for t in events:
        others = at[(at != t) & (at > t - 0.15) & (at < t + 0.25)]
        if len(others) or t < 0.05:
            continue
        a, b = int((t - 0.025) * sr), int((t + 0.3) * sr)
        pk = env[a:b].max()
        before = env[a: int((t - 0.005) * sr)].max()
        if before > pk * 10 ** (-20 / 20):
            continue
        j = a + int(np.argmax(env[a:b] >= pk * 10 ** (-15 / 20)))
        res.append({"t": t, "detected": j / sr, "err_ms": 1000 * (j / sr - t)})
    return res


def main():
    mix_p = Path(sys.argv[1]) if len(sys.argv) > 1 else OUT / "palette-demo.wav"
    rep = json.loads((OUT / "demo-events.json").read_text())
    x, sr = sf.read(mix_p, dtype="float64", always_2d=True)
    stems = {p.stem: sf.read(p, dtype="float64", always_2d=True)[0] for p in sorted((OUT / "stems").glob("*.wav"))}
    q: dict = {"file": str(mix_p), "duration_s": len(x) / sr, "sr": sr}
    q["ebur128"] = ebur128(mix_p)
    q["pyloudnorm_I"] = pyln.Meter(sr).integrated_loudness(x)
    q["sample_peak_dbfs"] = float(20 * np.log10(np.abs(x).max()))
    q["first_last_sample"] = [float(np.abs(x[0]).max()), float(np.abs(x[-1]).max())]
    q["dc_dbfs"] = float(20 * np.log10(np.abs(x.mean(0)).max() + 1e-15))
    ev = rep["events"]
    onsets = sorted({e["t"] for e in ev})
    q["clicks_mix"] = clicks(x, sr, onsets)
    q["clicks_stems"] = {k: clicks(v, sr, sorted({e["t"] for e in ev if e["stem"] == k}))
                         for k, v in stems.items() if not k.startswith("rev_")}
    # onsets: transient stems, isolated events only
    on = {}
    for k in ("piano", "mallets", "low"):
        ts = sorted({e["t"] for e in ev if e["stem"] == k and e.get("inst") != "sub"})
        on[k] = detect_onsets(stems[k], sr, ts, ts)
    q["onsets"] = on
    errs = [abs(o["err_ms"]) for v in on.values() for o in v]
    q["onset_abs_err_ms"] = {"median": float(np.median(errs)), "max": float(np.max(errs)), "n": len(errs)}
    # tuning of melodic struck notes (>= C4, fundamental resolvable in 0.3 s) in their stem: fundamental near the
    # target 80..380 ms after the attack; chord notes below C4 are proven in isolation by test_sampler.py
    pno = I.piano()
    tun = []
    for e in ev:
        if e.get("midi") is None or e["inst"] in ("pad", "sub", "bassdrum", "bowed", "cellos", "violas", "grand"):
            continue
        if e["midi"] < 60:
            continue
        stem = stems[e["stem"]]
        f_t = midi_to_hz(e["midi"])
        expect = 0.0
        if e["inst"] == "piano" and pno._smooth is not None:
            expect = float(np.polyval(pno._smooth, e["midi"]) - np.polyval(pno._smooth, 69))
        a = int((e["t"] + 0.08) * sr)
        b = int((e["t"] + 0.38) * sr)
        seg = stem[a:b].mean(1)
        f, lv = peak_freq(seg, sr, f_t * 2 ** (-0.6 / 12), f_t * 2 ** (0.6 / 12), pad=8)
        c = 1200 * np.log2(f / f_t)
        tun.append({"t": e["t"], "inst": e["inst"], "midi": e["midi"], "cents": round(float(c), 2),
                    "expect": round(expect, 2), "err": round(float(c - expect), 2)})
    q["tuning"] = tun
    te = [abs(t["err"]) for t in tun]
    q["tuning_abs_err_cents"] = {"median": float(np.median(te)), "p90": float(np.percentile(te, 90)),
                                 "max": float(np.max(te)), "n": len(te)}
    # mono compatibility
    M = x.mean(1, keepdims=True)
    mono = np.repeat(M, 2, 1) / np.sqrt(2) * np.sqrt(2)          # (L+R)/2 on both channels
    meter = pyln.Meter(sr)
    q["mono_minus_stereo_LU"] = float(meter.integrated_loudness(mono) - meter.integrated_loudness(x))
    bands = {"<200": (20, 200), "200-2k": (200, 2000), "2k-8k": (2000, 8000), ">8k": (8000, 20000)}
    loss = {}
    for k, (lo, hi) in bands.items():
        sos = butter(4, [lo, hi], "band", fs=sr, output="sos")
        xs = sosfiltfilt(sos, x, axis=0)
        e_st = np.mean(xs ** 2)
        e_mo = np.mean(xs.mean(1) ** 2)
        loss[k] = float(10 * np.log10(e_mo / e_st + 1e-20))
    q["mono_loss_db_by_band"] = loss                              # 0 dB = fully correlated, -3 dB = uncorrelated
    w = int(0.4 * sr)
    cors = []
    for s0 in range(0, len(x) - w, w // 2):
        L, R = x[s0: s0 + w, 0], x[s0: s0 + w, 1]
        if np.sum(L ** 2) > 1e-9:
            cors.append(float(np.sum(L * R) / np.sqrt(np.sum(L ** 2) * np.sum(R ** 2))))
    q["lr_correlation_400ms"] = {"min": min(cors), "median": float(np.median(cors))}
    S = (x[:, 0] - x[:, 1]) / 2
    q["side_to_mid_db"] = float(10 * np.log10(np.mean(S ** 2) / np.mean(M ** 2)))
    # long-term spectrum shares
    m = x.mean(1)
    P = np.abs(np.fft.rfft(m)) ** 2
    f = np.fft.rfftfreq(len(m), 1 / sr)
    tot = P[(f > 20)].sum()
    q["spectrum_share_db"] = {k: float(10 * np.log10(P[(f >= lo) & (f < hi)].sum() / tot))
                              for k, (lo, hi) in {"sub <60": (20, 60), "low 60-250": (60, 250),
                                                  "lowmid 250-1k": (250, 1000), "mid 1-4k": (1000, 4000),
                                                  "high 4-10k": (4000, 10000), "air >10k": (10000, 24000)}.items()}
    q["spectral_centroid_hz"] = float(np.sum(f * P) / np.sum(P))
    # balance: momentary loudness per stem; the piano's max and the others at their own events
    ml = {k: momentary(v, sr) for k, v in stems.items()}
    t0, Lp = ml["piano"]
    bal = {}
    for k, (tt, L) in ml.items():
        good = L > -70
        bal[k] = {"max_M": float(L.max()), "median_M_active": float(np.median(L[good])) if good.any() else None}
    q["stem_momentary"] = bal
    # per bar: piano max M vs bed (pad+bowed+strings) median M
    bed = stems["bed"] + stems.get("bowed", 0) + stems.get("strings", 0)
    tb, Lb = momentary(bed, sr)
    rows = []
    for bar in range(1, 6):
        a, b = (bar - 1) * rep["bar"], bar * rep["bar"]
        sel = (t0 >= a) & (t0 < b)
        rows.append({"bar": bar, "piano_max_M": float(Lp[sel].max()), "bed_median_M": float(np.median(Lb[sel])),
                     "bed_under_piano_LU": float(Lp[sel].max() - np.median(Lb[sel]))})
    # brightness per bar of the piano stem (1-4 kHz share of its energy; the lift should open it)
    for r in rows:
        a, b = int((r["bar"] - 1) * rep["bar"] * sr), int(r["bar"] * rep["bar"] * sr)
        seg = stems["piano"][a:b].mean(1)
        P = np.abs(np.fft.rfft(seg)) ** 2
        f = np.fft.rfftfreq(len(seg), 1 / sr)
        r["piano_1_4k_share_db"] = float(10 * np.log10(P[(f >= 1000) & (f < 4000)].sum() / P[f > 20].sum()))
        r["piano_centroid_hz"] = float(np.sum(f * P) / np.sum(P))
    q["bars"] = rows
    # accents: each mallet / heartbeat event's momentary level vs the piano max in +/- 1 beat
    acc = []
    for e in ev:
        if e["stem"] not in ("mallets", "low") or e.get("inst") == "sub":
            continue
        tt, L = ml[e["stem"]]
        sel = (tt >= e["t"]) & (tt < e["t"] + 0.4)
        selp = (t0 >= e["t"] - rep["beat"]) & (t0 < e["t"] + rep["beat"])
        acc.append({"t": e["t"], "inst": e["inst"], "kind": e.get("kind"), "M": round(float(L[sel].max()), 1),
                    "piano_M_near": round(float(Lp[selp].max()), 1),
                    "under_piano_LU": round(float(Lp[selp].max() - L[sel].max()), 1)})
    q["accents"] = acc
    # phone translation: a small speaker modelled as a 2nd-order high-pass at 350 Hz and low-pass at 9 kHz.
    # Loudness lost by the mix, and the motif's lead over everything else through that speaker.
    sos_ph = np.vstack([butter(2, 350, "high", fs=sr, output="sos"), butter(2, 9000, "low", fs=sr, output="sos")])
    ph = lambda v: sosfilt(sos_ph, v, axis=0)
    q["phone_loudness_drop_LU"] = float(pyln.Meter(sr).integrated_loudness(ph(x)) - q["pyloudnorm_I"])
    rest = sum(v for k, v in stems.items() if k != "piano")
    tp, Lp_ph = momentary(ph(stems["piano"]), sr)
    _, Lr_ph = momentary(ph(rest), sr)
    lead = []
    for e in ev:
        if e["inst"] == "piano" and e.get("midi", 0) >= 60:
            sel = (tp >= e["t"]) & (tp < e["t"] + 0.4)
            if sel.any():
                lead.append({"t": e["t"], "midi": e["midi"], "piano_minus_rest_LU": round(float(
                    Lp_ph[sel].max() - Lr_ph[sel].max()), 1)})
    q["phone_motif_lead"] = lead
    (OUT / "demo-qa.json").write_text(json.dumps(q, indent=1, default=float))
    brief = {k: q[k] for k in ("ebur128", "sample_peak_dbfs", "first_last_sample", "dc_dbfs", "onset_abs_err_ms",
                                "tuning_abs_err_cents", "mono_minus_stereo_LU", "mono_loss_db_by_band",
                                "lr_correlation_400ms", "side_to_mid_db", "spectrum_share_db", "spectral_centroid_hz",
                                "stem_momentary", "bars", "phone_loudness_drop_LU", "phone_motif_lead")}
    brief["clicks_mix"] = q["clicks_mix"]
    brief["clicks_stems"] = {k: len(v) for k, v in q["clicks_stems"].items()}
    print(json.dumps(brief, indent=1, default=float))
    print("accents:", json.dumps(acc))


if __name__ == "__main__":
    main()
