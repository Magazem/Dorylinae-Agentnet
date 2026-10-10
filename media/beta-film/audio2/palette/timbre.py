"""Piano timbre in the motif register, measured on rendered notes (-> ../research/piano-timbre.json):
partials 2..8 relative to the fundamental (30..330 ms after the attack), spectral centroid, 1-4 kHz share,
and the share of > 2 kHz energy in the first 30 ms (hammer brightness)."""
import json, sys
from pathlib import Path
import numpy as np
from scipy.signal import butter, sosfilt
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import instruments as I  # noqa
from analysis import midi_to_hz, peak_freq  # noqa
from sampler2 import Instrument, PREROLL, SR  # noqa
stein = Instrument("steinwayB_best", I.T / "Chordophones/Zithers/Grand Piano, Steinway B/Sus", "*.wav",
                   r"JHPiano_Sus_Close_" + I.NOTE + r"_vl(?P<layer>\d)_rr(?P<rr>\d)", layer_db={0: -14.0, 1: -8.0, 2: -3.0},
                   tune="stretch", stereo="best")
C = {"osiris_B (recommended)": I.piano("B"), "kawai v2-v4": I.grand(), "steinwayB (v1's piano, unfiltered)": stein}
out = {}
for name, inst in C.items():
    for midi in (62, 69, 74):
        for v in (0.3, 0.7):
            y = inst.note(midi, v); inst.reset_rr()
            m = y.mean(1); on = int(PREROLL * SR)
            seg = m[on + int(0.03 * SR): on + int(0.33 * SR)]
            f0 = midi_to_hz(midi)
            lv = np.array([peak_freq(seg, SR, h * f0 * 0.985, h * f0 * 1.02)[1] for h in range(1, 9)])
            hp = sosfilt(butter(4, 2000, "high", fs=SR, output="sos"), m)
            att = 10 * np.log10(np.mean(hp[on: on + int(0.03 * SR)] ** 2) / np.mean(m[on: on + int(0.03 * SR)] ** 2))
            P = np.abs(np.fft.rfft(seg * np.hanning(len(seg)))) ** 2
            f = np.fft.rfftfreq(len(seg), 1 / SR)
            out[f"{name} | midi {midi} | vel {v}"] = {
                "layer": inst.layer_for(v), "partials_2_8_db": [round(float(x), 1) for x in (lv[1:] - lv[0])],
                "centroid_hz": round(float(np.sum(f * P) / np.sum(P))),
                "share_1_4k_db": round(float(10 * np.log10(P[(f > 1000) & (f < 4000)].sum() / P.sum())), 1),
                "attack_above_2k_db": round(float(att), 1)}
            print(name, midi, v, out[f"{name} | midi {midi} | vel {v}"])
(HERE.parent / "research" / "piano-timbre.json").write_text(json.dumps(out, indent=1))
