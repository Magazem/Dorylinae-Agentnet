"""15-second palette demo at 84 BPM (beat 0.714286 s, bar 2.857143 s), D major, rendered entirely in code.

    PY=../../tools/.venv/bin/python
    $PY -I demo.py            # -> out/palette-demo.wav (master), out/stems/*.wav, out/demo-events.json
    $PY -I qa.py              # measurements + out/palette-demo-qa.png

It is a miniature of the film's first five scene bars, so every palette role is heard in context:
  bar 1  0.000  bed enters (designed pad, Dadd9), heartbeat LUB/DUB (felt bass drum pp + tuned sub D1),
                Osiris low D2/A2
  bar 2  2.857  the v1 motif on the Osiris piano (one mic of pair B per note, pitch-panned): A4 (beat 1) F#4 (2.5)
                D4 (4), over a rolled D3-A3-E4
  bar 3  5.714  Bm7; the 'paper' colour: vibraphone soft mallets F#5, celesta A6 / F#6 (one channel, high-passed)
  bar 4  8.571  Gmaj9 -> Asus4 (beat 3); bowed vibraphone A4+E5 swells into the lift; cello and viola sections
                enter on beat 3 (soft layer, slow attack)
  bar 5 11.429  THE LIFT / the seal: felt bass drum mp + sub, hand chime D6 + vibraphone D5, rolled Dmaj9 on the
                piano with the Kawai grand layered 6 dB under ('tre corde': the una corda opens), the pad's spectral
                envelope opens 600 -> 2200 Hz over 1.6 s, strings bloom;
                the motif answers D5 (beat 3) A4 (beat 4); master fades 14.2 -> 15.0 s to exact zero
Space: two generated IRs (reverb.py), 'chamber' for presence and 'hall' for the wash, as sends (high-passed 160 Hz).
Master: stems summed, 25 Hz high-pass, the toolchain's 4x-oversampled true-peak limiter (ceiling -1.5 dBTP),
then a static gain to -16 LUFS integrated (a demo of timbre, not a loudness master).
"""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

import numpy as np
import soundfile as sf
from pedalboard import (HighpassFilter, HighShelfFilter, LowpassFilter, LowShelfFilter, PeakFilter, Pedalboard)
from scipy.signal import butter, fftconvolve, sosfiltfilt

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent.parent / "tools" / "audio-poc"))
sys.dont_write_bytecode = True
import instruments as I  # noqa: E402
from master import limit, lufs  # noqa: E402
from reverb import ROOMS, make_ir  # noqa: E402
from sampler2 import PREROLL, SR, place  # noqa: E402
from synth import pad, sub  # noqa: E402

BPM = 84
BEAT = 60 / BPM
BAR = 4 * BEAT
DUR = 15.0
TARGET_LUFS = -16.0
OUT = HERE / "out"


def T(bar: int, beat: float = 1.0) -> float:
    return (bar - 1) * BAR + (beat - 1) * BEAT


def pre(y: np.ndarray) -> np.ndarray:
    """Synth clips start at their onset: give them the sampler's PREROLL so place() treats them alike."""
    return np.vstack([np.zeros((int(round(PREROLL * SR)), 2)), y])


class Stems:
    def __init__(self):
        self.n = int(DUR * SR) + int(8 * SR)              # room for tails; cut at the end
        self.s: dict[str, np.ndarray] = {}
        self.events: list[dict] = []

    def add(self, stem: str, clip: np.ndarray, t: float, **meta):
        buf = self.s.setdefault(stem, np.zeros((self.n, 2)))
        place(buf, clip, t)
        self.events.append({"t": round(t, 6), "stem": stem, **meta})


def render() -> dict:
    P = I.palette()
    st = Stems()
    piano, grand, vib, chi, cel, bow, vc, vla, bd = (P[k] for k in ("piano", "grand", "vibes", "chimes", "celesta",
                                                                   "bowed", "cellos", "violas", "bassdrum"))

    def pn(t, midi, vel, **kw):
        st.add("piano", piano.note(midi, vel, **kw), t, inst="piano", midi=midi, vel=vel)

    def gn(t, midi, vel, gain_db=-6.0):
        st.add("piano", grand.note(midi, vel, gain_db=gain_db), t, inst="grand", midi=midi, vel=vel)

    def roll(t, notes, vel, gap=0.035, with_grand=False):
        for i, m in enumerate(notes):              # bass notes voiced softer: a low Osiris note carries 10x the energy
            v = vel * (1 - 0.04 * i) * (0.7 if m < 48 else 1.0)
            pn(t + i * gap, m, v)
            if with_grand and m >= 45:
                gn(t + i * gap, m, v)

    def heart(t, strong=False, trim=0.0):
        g = (-5.0 if strong else -10.0) + trim
        st.add("low", bd.hit("mp" if strong else "pp", gain_db=g), t, inst="bassdrum", kind="LUB")
        st.add("low", pre(sub(26, level_db=-18 + (3 if strong else 0))), t, inst="sub", midi=26, kind="LUB")
        st.add("low", bd.hit("pp", gain_db=g - 5), t + BEAT / 2, inst="bassdrum", kind="DUB")
        st.add("low", pre(sub(26, level_db=-23, decay_s=0.11)), t + BEAT / 2, inst="sub", midi=26, kind="DUB")

    # ---- bar 1: bed + heartbeat + low D
    st.add("bed", pre(pad([50, 57, 64], 5.9, attack=2.2, release=2.2, fc0=600, seed=1, level_db=-9)), T(1),
           inst="pad", chord="Dadd9 (D3 A3 E4)")
    heart(T(1), trim=-5.0)
    roll(T(1), [38, 45], 0.26, gap=0.05)
    # ---- bar 2: the motif over Dadd9
    heart(T(2))
    roll(T(2), [50, 57, 64], 0.28)
    pn(T(2, 1), 69, 0.55)
    pn(T(2, 2.5), 66, 0.5)
    pn(T(2, 4), 62, 0.46)
    # ---- bar 3: Bm7 + the paper colour
    heart(T(3))
    st.add("bed", pre(pad([47, 54, 62], 5.9, attack=1.8, release=2.2, fc0=600, seed=2, level_db=-9)), T(3) - 0.15,
           inst="pad", chord="Bm (B2 F#3 D4)")
    roll(T(3), [35, 47, 54, 57, 62], 0.27)
    st.add("mallets", vib.note(78, 0.6, gain_db=1), T(3, 1), inst="vibes", midi=78, vel=0.6)
    st.add("mallets", cel.note(93, 0.8, gain_db=-21), T(3, 2.5), inst="celesta", midi=93, vel=0.8)
    st.add("mallets", cel.note(90, 0.8, gain_db=-23), T(3, 3), inst="celesta", midi=90, vel=0.8)
    pn(T(3, 3), 66, 0.42)
    # ---- bar 4: Gmaj9 -> Asus4, bowed vibes and strings swell into the lift
    heart(T(4))
    roll(T(4), [43, 50, 54, 57, 59], 0.28)
    pn(T(4, 2.5), 64, 0.42)
    roll(T(4, 3), [45, 52, 57, 62], 0.26)
    st.add("bed", pre(pad([43, 50, 57, 59], 2.9, attack=1.0, release=1.6, fc0=650, seed=3, level_db=-10)),
           T(4) - 0.1, inst="pad", chord="Gmaj9 (G2 D3 A3 B3)")
    st.add("bed", pre(pad([45, 52, 57, 62], 2.6, attack=0.9, release=1.2, fc0=700, seed=4, level_db=-10)),
           T(4, 3) - 0.1, inst="pad", chord="Asus4 (A2 E3 A3 D4)")
    for m in (69, 76):
        st.add("bowed", bow.note(m, 0.6, dur=5.2, attack=0.4, gain_db=-1), T(4, 1), inst="bowed", midi=m)
    st.add("strings", vc.note(50, 0.3, dur=4.8, attack=1.4, gain_db=-3), T(4, 3), inst="cellos", midi=50)
    st.add("strings", vla.note(57, 0.3, dur=4.8, attack=1.4, gain_db=-5), T(4, 3), inst="violas", midi=57)
    # ---- bar 5: the lift / the seal
    heart(T(5), strong=True)
    st.add("mallets", chi.note(86, 0.6, gain_db=-4), T(5), inst="chimes", midi=86, vel=0.6, kind="SEAL")
    st.add("mallets", vib.note(74, 0.7, gain_db=-3), T(5), inst="vibes", midi=74, vel=0.7, kind="SEAL")
    roll(T(5), [38, 45, 54, 64, 69], 0.4, gap=0.03, with_grand=True)   # 'tre corde': the Kawai opens the lift
    st.add("bed", pre(pad([50, 57, 64, 66], 4.0, attack=0.25, release=2.5, fc0=600, fc1=2200, open_s=1.6,
                          open_at=0.0, seed=5, level_db=-8)), T(5) - 0.02, inst="pad", chord="Dmaj9 opens")
    st.add("strings", vc.note(38, 0.3, dur=3.0, attack=0.8, gain_db=-6), T(5), inst="cellos", midi=38)
    st.add("strings", vla.note(54, 0.3, dur=3.0, attack=0.8, gain_db=-6), T(5), inst="violas", midi=54)
    st.add("mallets", cel.note(98, 0.8, gain_db=-22), T(5, 2.5), inst="celesta", midi=98, vel=0.8)
    pn(T(5, 3), 74, 0.5)
    gn(T(5, 3), 74, 0.5, gain_db=-4)
    pn(T(5, 4), 69, 0.44)
    gn(T(5, 4), 69, 0.44, gain_db=-4)

    # ---- bus processing (pedalboard, float32 in/out)
    def pb(x, board):
        return board(x.T.astype(np.float32), SR).T.astype(np.float64)

    buses = {
        # Osiris is dark (centroid ~300 Hz, >4 kHz share -62 dB): a little presence so the motif reads on phones
        "piano": Pedalboard([HighpassFilter(45), LowShelfFilter(160, -3.0, 0.7), PeakFilter(2400, 4.0, 0.6),
                             HighShelfFilter(6500, 3.0, 0.7)]),
        "bed": Pedalboard([HighpassFilter(110)]),
        "bowed": Pedalboard([HighpassFilter(150), LowpassFilter(9000)]),
        "strings": Pedalboard([HighpassFilter(60), LowpassFilter(5500), PeakFilter(300, -1.5, 1.0)]),
        # celesta noise is mostly < 120 Hz (rumble): its notes are > 1 kHz
        "mallets": Pedalboard([HighpassFilter(140)]),
        "low": Pedalboard([LowpassFilter(400)]),
    }
    dry = {k: pb(v, buses[k]) for k, v in st.s.items()}
    # ---- sends to the generated rooms (mono-in, stereo-out convolution)
    irs = {k: make_ir(**ROOMS[k]) for k in ("chamber", "hall")}
    # unit energy per channel: a 0 dB send returns about the level it receives, so sends read as wet/dry ratios
    irs = {k: v / np.sqrt(np.mean(np.sum(v ** 2, axis=0))) for k, v in irs.items()}
    sends = {"piano": {"chamber": -17, "hall": -22}, "mallets": {"chamber": -16, "hall": -11},
             "bed": {"hall": -15}, "bowed": {"hall": -12}, "strings": {"chamber": -16, "hall": -11},
             "low": {"chamber": -26}}
    wet = {}
    for room, ir in irs.items():
        acc = np.zeros((st.n, 2))
        for stem, d in sends.items():
            if room in d and stem in dry:
                acc += dry[stem] * 10 ** (d[room] / 20)
        # the send is high-passed at 160 Hz: the rooms get no bass (pedal-down low notes made the chamber return
        # as loud as the dry piano at bar 4 in the first render; measured)
        mono = sosfiltfilt(butter(2, 160, "high", fs=SR, output="sos"), acc.mean(1))
        w = np.stack([fftconvolve(mono, ir[:, 0])[: st.n], fftconvolve(mono, ir[:, 1])[: st.n]], 1)
        wet["rev_" + room] = w
    allst = {**dry, **wet}
    # ---- master
    n = int(DUR * SR)
    mix = sum(allst.values())[:n]
    hp = butter(2, 25, "high", fs=SR, output="sos")
    mix = sosfiltfilt(hp, mix, axis=0)
    fade = np.ones(n)
    a, b = int(14.2 * SR), n
    fade[a:b] = 0.5 + 0.5 * np.cos(np.linspace(0, np.pi, b - a))
    fade[-1] = 0.0
    mix *= fade[:, None]
    g = 1.0
    for _ in range(3):                                 # gain so that the limited mix lands on TARGET_LUFS
        y, gr = limit(mix * g, SR, ceiling_db=-1.5)
        L = lufs(y, SR)
        g *= 10 ** ((TARGET_LUFS - L) / 20)
    y, gr = limit(mix * g, SR, ceiling_db=-1.5)
    y[0] = 0.0
    y[-1] = 0.0
    OUT.mkdir(exist_ok=True)
    (OUT / "stems").mkdir(exist_ok=True)
    sf.write(OUT / "palette-demo.wav", y, SR, subtype="PCM_24")
    stem_lufs = {}
    for k, v in allst.items():
        v = sosfiltfilt(hp, v[:n], axis=0) * fade[:, None] * g
        sf.write(OUT / "stems" / f"{k}.wav", v.astype(np.float32), SR, subtype="FLOAT")
        stem_lufs[k] = lufs(v, SR)
    rep = {"bpm": BPM, "beat": BEAT, "bar": BAR, "dur": DUR, "master_gain_db": 20 * np.log10(g),
           "limiter_max_gr_db": gr, "lufs": lufs(y, SR), "stem_lufs": stem_lufs,
           "md5": hashlib.md5(y.astype(np.float32).tobytes()).hexdigest(), "events": st.events}
    (OUT / "demo-events.json").write_text(json.dumps(rep, indent=1, default=float))
    return rep


if __name__ == "__main__":
    r = render()
    print(json.dumps({k: v for k, v in r.items() if k != "events"}, indent=1, default=float))
