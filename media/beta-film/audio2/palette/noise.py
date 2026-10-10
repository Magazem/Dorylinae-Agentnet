"""Noise detail for sustained/bed candidates: pre-roll (or quiet tail) noise vs the sustain level, and its spectrum
in bands (rumble/hum < 120 Hz, 120 Hz-1 kHz, 1-5 kHz, hiss > 5 kHz), all in dB relative to the sustain RMS."""
import json, sys
from pathlib import Path
import numpy as np, soundfile as sf
HERE = Path(__file__).resolve().parent
M = HERE.parent / "research" / "measurements"
out = {}
for cid in sys.argv[1:]:
    d = json.loads((M / f"{cid}.json").read_text())
    rows = []
    for r in d["samples"]:
        x, sr = sf.read(r["file"], dtype="float64", always_2d=True)
        m = x.mean(1)
        on = int(r["onset_ms"] * sr / 1000)
        pre = m[: max(0, on - int(0.005 * sr))]
        w = int(0.05 * sr); k = len(m) // w
        rms = np.sqrt(np.mean(m[: k * w].reshape(k, w) ** 2, 1))
        sus = np.median(rms[rms > rms.max() * 10 ** (-12 / 20)])
        src = None
        if len(pre) >= int(0.03 * sr):
            nz, src = pre, "pre-roll"
        else:
            q = np.where(rms < rms.max() * 10 ** (-55 / 20))[0]
            q = q[q > np.argmax(rms)]
            if len(q) >= 3:
                nz, src = m[q[0] * w: (q[0] + len(q)) * w], "tail"
        if src is None:
            continue
        nr = np.sqrt(np.mean(nz ** 2)) + 1e-15
        S = np.abs(np.fft.rfft(nz * np.hanning(len(nz)))) ** 2
        f = np.fft.rfftfreq(len(nz), 1 / sr)
        tot = S.sum() + 1e-30
        b = lambda lo, hi: 10 * np.log10(S[(f >= lo) & (f < hi)].sum() / tot + 1e-12)
        rows.append({"file": Path(r["file"]).name, "src": src, "noise_re_sustain_db": 20 * np.log10(nr / sus),
                     "lt120": b(0, 120), "120-1k": b(120, 1000), "1k-5k": b(1000, 5000), "gt5k": b(5000, sr / 2),
                     "layer": r.get("layer")})
    if rows:
        v = np.array([q["noise_re_sustain_db"] for q in rows])
        out[cid] = {"files": len(rows), "noise_re_sustain_db_median": float(np.median(v)),
                    "worst": float(v.max()), "by_layer": {}}
        for L in sorted({str(q["layer"]) for q in rows}):
            vv = [q["noise_re_sustain_db"] for q in rows if str(q["layer"]) == L]
            out[cid]["by_layer"][L] = float(np.median(vv))
        for key in ("lt120", "120-1k", "1k-5k", "gt5k"):
            out[cid]["share_" + key] = float(np.median([q[key] for q in rows]))
    print(cid, json.dumps(out.get(cid), default=float))
p = M / "noise.json"
allv = json.loads(p.read_text()) if p.exists() else {}
allv.update(out)
p.write_text(json.dumps(allv, indent=1))
