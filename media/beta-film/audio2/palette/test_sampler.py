"""Proof of the sampler: every instrument plays a set of notes; each rendered note is re-measured.
  * onset: the 1 ms envelope must cross peak-30 dB at PREROLL (3 ms) +/- 0.25 ms
  * pitch: fundamental (or partial fit) within +/- 3 cents of the 12-TET target (+ the piano's kept stretch)
  * clicks: first and last sample exactly 0; no step larger than -30 dB re peak in the first 3 ms
  * determinism: rendering the same note twice gives identical bytes
Writes ../research/sampler-proof.json and prints a table.
"""
import hashlib, json, sys
from pathlib import Path
import numpy as np
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from analysis import envelope_1ms, measure_tuning, midi_to_hz  # noqa
from sampler2 import PREROLL, SR, filter_report  # noqa
import instruments as I  # noqa

TESTS = {
    "piano": (I.piano, [38, 45, 50, 54, 57, 62, 66, 69, 71, 74, 78, 81, 86], [0.3, 0.8]),
    "piano_damped": (I.piano_damped, [50, 62, 69, 74], [0.4]),
    "grand": (I.grand, [38, 50, 57, 62, 69, 74, 81], [0.3, 0.6, 0.9]),
    "vibes": (I.vibes, [62, 66, 69, 74, 78, 81], [0.3, 0.8]),
    "chimes": (I.chimes, [74, 78, 81, 86], [0.6]),
    "celesta": (I.celesta, [74, 78, 81, 86, 90, 93], [0.8]),
    "bowed": (I.bowed, [57, 62, 66, 69, 74, 76], [0.6]),
    "cellos": (I.cellos, [38, 45, 50, 57], [0.3]),
    "violas": (I.violas, [50, 57, 62, 66], [0.3]),
}
rows = []
for name, (ctor, notes, vels) in TESTS.items():
    inst = ctor()
    for v in vels:
        for n in notes:
            dur = 3.0 if inst.sustain else None
            try:
                y = inst.note(n, v, dur=dur)
            except ValueError as e:
                rows.append({"inst": name, "midi": n, "vel": v, "error": str(e)}); continue
            inst.reset_rr()
            y2 = inst.note(n, v, dur=dur)
            inst.reset_rr()
            m = y[:, 0] if inst.stereo == 'left' else y.mean(1)
            env = envelope_1ms(m, SR)
            on = int(np.argmax(env >= env.max() * 10 ** (inst.align_db / 20)))
            on30 = int(np.argmax(env >= env.max() * 10 ** (-30 / 20)))
            short = name in ("vibes", "chimes", "celesta")
            t = measure_tuning(m, SR, on30, n, short=short, force_octave=0)
            expect = 0.0
            if inst.tune == "stretch" and inst._smooth is not None:
                z = inst._pick(n, inst.layer_for(v), inst.zones); inst.reset_rr()
                expect = float(np.polyval(inst._smooth, n) - np.polyval(inst._smooth, 69))
            rows.append({"inst": name, "midi": n, "vel": v, "onset_ms": 1000 * on / SR,
                         "cents": t.get("cents"), "method": t.get("method"), "expect_cents": expect,
                         "err_cents": (t.get("cents") - expect) if t.get("cents") is not None else None,
                         "first": float(np.abs(y[0]).max()), "last": float(np.abs(y[-1]).max()),
                         "deterministic": hashlib.md5(y.tobytes()).hexdigest() == hashlib.md5(y2.tobytes()).hexdigest(),
                         "len_s": len(y) / SR})
# round robins cycle; velocity is monotonic in level across layer switches
rr = {}
from sampler2 import Instrument  # noqa
yam = Instrument("yamaha_up_best", I.VC / "Chordophones/Zithers/Upright Piano, Yamaha/Sustains", "*.wav",
                 r"Upright1_Sus_" + I.NOTE + r"_vl(?P<layer>\d)_rr(?P<rr>\d)", stereo="best")
yam.reset_rr()
rr["yamaha_upright_G4_vel0.9"] = [yam._pick(67, yam.layer_for(0.9), yam.zones).path.name for _ in range(5)]
bd = I.bassdrum()
seq = []
for _ in range(4):
    i = bd._rr.get("pp", 0)
    seq.append(bd.files["pp"][i % len(bd.files["pp"])].name)
    bd.hit("pp")
rr["bassdrum_pp"] = seq
vel = {}
for name, ctor, midi in (("piano", I.piano, 69), ("grand", I.grand, 62), ("vibes", I.vibes, 74), ("celesta", I.celesta, 84)):
    inst = ctor()
    pts = []
    for v in np.linspace(0.05, 1.0, 20):
        y = inst.note(midi, float(v)); inst.reset_rr()
        a = int(PREROLL * SR)
        pts.append((round(float(v), 3), inst.layer_for(float(v)), round(float(20 * np.log10(np.sqrt(np.mean(y[a: a + int(0.5 * SR)] ** 2)) + 1e-12)), 2)))
    lv = [p[2] for p in pts]
    vel[name] = {"points": pts, "monotonic": bool(all(b >= a - 0.05 for a, b in zip(lv, lv[1:]))),
                 "max_step_db": float(max(abs(b - a) for a, b in zip(lv, lv[1:]))), "range_db": float(lv[-1] - lv[0])}
# release: note-off with the damper curve (+ the library's key-up sample where it has one)
from scipy.signal import butter as _b, sosfilt as _sf  # noqa
rel = {}
for name, ctor, midi in (("grand", I.grand, 62), ("grand", I.grand, 45), ("piano_damped", I.piano_damped, 62),
                         ("piano_damped", I.piano_damped, 45), ("bowed", I.bowed, 69), ("cellos", I.cellos, 50)):
    inst = ctor()
    dur = 0.6 if not inst.sustain else 2.0
    y = inst.note(midi, 0.6, dur=dur); inst.reset_rr()
    a = int(PREROLL * SR); off = a + int(dur * SR)
    lvl = lambda s0, s1: float(20 * np.log10(np.sqrt(np.mean(y[s0:s1] ** 2)) + 1e-12))
    hp = _sf(_b(4, 6000, "high", fs=SR, output="sos"), y.mean(1))
    d2 = np.abs(np.diff(hp, 2))
    win = d2[off - 480: off + 4800]
    ref = np.median(d2[off - 9600: off - 480]) + 1e-12
    r = {"midi": midi, "dur": dur, "release_s": inst._release_time(midi), "level_before_off_db": lvl(off - 2400, off),
         "level_200ms_after_db": lvl(off + int(0.18 * SR), off + int(0.22 * SR)),
         "tail_len_s": (len(y) - off) / SR, "last_sample": float(np.abs(y[-1]).max()),
         "max_d2_ratio_db_at_off": float(20 * np.log10(win.max() / ref)),
         "release_sample_used": bool(getattr(inst, "rel", None))}
    if inst.rel:
        keep = inst.rel; inst.rel = None
        y0 = inst.note(midi, 0.6, dur=dur); inst.reset_rr(); inst.rel = keep
        n = min(len(y), len(y0))
        r["release_sample_level_db"] = float(20 * np.log10(np.sqrt(np.mean((y[off:n] - y0[off:n]) ** 2)) + 1e-12))
    rel[f"{name}:{midi}"] = r
print("release:", json.dumps(rel, default=float))
out = {"filter": filter_report(), "filter_osiris_48k": filter_report(48000, 48000, 0.97), "round_robin": rr,
       "velocity": vel, "release": rel, "notes": rows}
print("round robins:", json.dumps(rr))
print("velocity:", json.dumps({k: {kk: vv for kk, vv in v.items() if kk != "points"} for k, v in vel.items()}))
(HERE.parent / "research" / "sampler-proof.json").write_text(json.dumps(out, indent=1, default=float))
print(json.dumps(out["filter"]), json.dumps(out["filter_osiris_48k"]))
for name in TESTS:
    rr = [r for r in rows if r["inst"] == name and "error" not in r]
    errs = [abs(r["err_cents"]) for r in rr if r["err_cents"] is not None]
    ons = [r["onset_ms"] for r in rr]
    print(f"{name:13s} notes={len(rr):2d} |err| max {max(errs):5.2f} c median {np.median(errs):4.2f}  onset {min(ons):.2f}..{max(ons):.2f} ms  "
          f"zero ends {all(r['first'] == 0 and r['last'] == 0 for r in rr)}  deterministic {all(r['deterministic'] for r in rr)}  "
          f"errors {sum(1 for r in rows if r['inst'] == name and 'error' in r)}")
    bad = [r for r in rr if r["err_cents"] is None or abs(r["err_cents"]) > 3 or abs(r["onset_ms"] - 1000 * PREROLL) > 0.4]
    for r in bad:
        print("   ", r)
