"""Dorylinae closed-beta film v2, "The envelope": the 64.2857 s score, composed and rendered in code.

    PY=../tools/.venv/bin/python
    $PY -I score2.py --emit-script-cues cues-script.json                    # cue file from SCRIPT-v2 §3 + §8
    $PY -I score2.py --cues cues-script.json --format 16x9 --out out/mix-16x9.wav
    $PY -I score2.py --cues cues-script.json --format 4x5  --out out/mix-4x5.wav
    (without --cues: engine/out/v2-cues-<format>.json when the picture has exported it, else cues-script.json)

Sources: design/SCRIPT-v2.md §2 (music fit), §3 (master timeline), §5 (sections, boot), §8 (sound accents); BRIEF-v2
(owner decisions: signature "Yazan", no card footnote); audio2/PALETTE.md (instruments, all CC0); tools/TOOLING.md §2.
84 BPM, 4/4, beat 5/7 s, bar 20/7 s, 90 beats = 64.2857 s, 48 kHz, stereo, 24-bit. Key: D major.

THE COMPOSITION (every musical event sits on a picture event; G = global beat from frame 0, t = G x 5/7 s)
  The motif M: F#4 - A4 - E5 - D5 (3, 5, 9 -> 8 of D major), rhythm 1 - 2 - 3 - (next downbeat). It leaps up and
  lands a step down: "it goes, and it arrives". Its four notes sounded together are Dadd9, the boot's chord.
  I    Cold open  G0-G8     dry: room tone and the second paste (LAND at G2) only.
  II   The scene  G8-G56
       G7.5  the pad swells in with the name (S-1); G8 the 21-degree cut: rolled Dadd9, the heartbeat begins
             (LUB = maya on D1, DUB = theo on A1, half a beat later; once per bar, with the presence pulse).
       G9.75 the 8 brief lines: 8 soft piano notes, one per line, falling through D pentatonic (the agent writes).
       G12   the fold: Bm7; G14 Gmaj9; G15 G/A; bowed vibraphone and strings swell into the lift.
       G16   THE LIFT, THE SEAL: felt stamp + hand chime D6 + vibraphone D5; D with the piano opened by the Kawai
             grand; M on the piano: F#4 (the seal) A4 E5 ... D5 lands with the envelope on the counter (G20,
             ARRIVE, root). The bass walks down D C# B A G F# E (bars 5-8) under it.
       G22-25 "who, to whom, when, how big": the vibraphone reads the form, one note per word (B5 A5 F#5 D5).
       G28   "It cannot open the envelope.": Em11, closed and quartal.
       G32   BREAKDOWN, theo offline: almost nothing. A9sus4 (A E B D, no third) held by a dark pad; the piano
             plays M's first two notes, F#4 (G32) A4 (G33), and stops: the motif waits. Only maya's LUB (distant);
             the room tone comes back; the counter clock ticks in real seconds (24.571 ... 27.571), off the beat.
       G37   bowed vibraphone and strings swell into the return.
       G40   THE RETURN, theo is back: the harmony resolves to D, the missing note E5 arrives on the ring fill,
             the hand chime A5 (BACK) and theo's DUB return; E5 hangs over the trip and lands on D5 at his dock
             (G43, ARRIVE: the dyad A4 + D5, theo = the fifth).
       G44   opened: Gmaj9, a celesta sparkle; G44.5-50.5 the work: the vibraphone pulses A4/D5 in eighths
             (theo's notes, common to every chord) under Gmaj9 Bm7 Em11 A7sus4.
       G52   the answer's seal ("sealed the same way"): the same stamp, chime and vibraphone, and M again; D5 lands
             at maya's dock on G55 (ARRIVE, root) over a V6-I cadence.
  III  The card   G56-G65   the clear statement: M on the card's lines (F#4 "End-to-end encrypted.", A4 line 2,
             E5 line 3) re-harmonised Gmaj9 - A7sus4 - D (an authentic cadence, D5 on G60), strings; the
             vibraphone echoes M's head (G62, G63); Bm7, Bm7/A, and the cut.
  IV   The invitation G65-G80  warmth: Gmaj7 on the cut, strings (cello and viola sections), slower harmony;
             M in augmentation on the letter's lines (F#4 line 1, A4 "Tell us what breaks.", E5 the outro line)
             doubled an octave down by the cellos, D5 on the signature (G76) over Gmaj9: an open IV.
             Released from G78.25; exact digital silence G79-G80 (56.429-57.143), the breath before the boot.
  V    Boot G80, address G84  the plates: M's notes rising as an arpeggio, D2+F#3 A3 E4 D5 on plates 1, 7, 13,
             19 and a celesta F#6 on the last plate (23); the motif chord blooms (pad, bowed vibraphone,
             strings: D F# A E, the IV resolving to I across the silence); hand chime A5 on the signal mark; a
             paper tap per typed letter. G84 the address: one low D2 with maya's last heartbeat, decaying to exact
             digital silence by 63.857 s (G89.4), as the picture's own music end.

SOUND ACCENTS (code, SCRIPT-v2 §8, placed from the cue file): PAPER (slide, fold, slit, unfold), LAND (wood tick),
SEAL (felt stamp), CLOCK (tick / tock), TYPE (paper tap). ARRIVE and BACK are notes of the score (above).

POLISH (QA pass 1; the composition and every time are unchanged):
  1 the Kawai layer is re-tuned per note to the Osiris note it doubles and set in dB under it (sampler2 tune_ref,
    level_ref); 2 Osiris is tuned on each note's first second, its bass no flatter than -6 c (instruments); 3 the
    breakdown's G32 roll and motif notes sit 5 dB down (NOTE_GAIN_DB), so bar 9 lands on a drop; 4 clean pedalling: a
    low note leaving with a step-away bass entering is damped in 0.25 s, and a pad / string entry a semitone from a
    leaving note waits for it (STEP_RELEASE, string_notes, pad_part); 5 the bowed vibraphone is shelved above 1.4 kHz;
    6 the accents are raised and ridden against the music (FX_TRIM_DB, accent_rider); 7 clarity: the motif 1.5 dB
    forward with the open grand 4 dB under it, the vibraphone doubling it an octave up, a presence shelf on the
    piano and 200-400 Hz trimmed on the piano, the bed and the room sends; 8 the report measures the tuning of every
    (instrument, note) played (tuning_check).

QUALITY BY CONSTRUCTION (nobody can listen): the sampler aligns every struck note's attack on its cue (PREROLL);
every synthesized clip starts and ends at exactly 0; pads are band-limited additive synthesis (no aliasing); every
random number is seeded by (seed, kind, time); the voicings are checked against low-interval limits, the melody
against the harmony and the heartbeat pitches against the chord (report "harmony_checks"); silences are an exact
zero mask applied after every filter and reverb; the master is one static gain, a 4x true-peak look-ahead limiter
whose gain curve is applied to every stem (stems sum to the mix), and a static trim from ffmpeg's own measurement.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
import subprocess
import sys
import time
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.ndimage import convolve1d, maximum_filter1d, minimum_filter1d, uniform_filter1d
from scipy.signal import butter, fftconvolve, sosfilt

HERE = Path(__file__).resolve().parent                 # film/audio2
FILM = HERE.parent
PAL = HERE / "palette"
sys.dont_write_bytecode = True
sys.path.insert(0, str(PAL))
sys.path.insert(0, str(FILM / "tools" / "audio-poc"))
import instruments as I  # noqa: E402
from analysis import envelope_1ms, midi_to_hz, name_to_midi, peak_freq  # noqa: E402
from master import lufs, true_peak  # noqa: E402
from reverb import ROOMS, make_ir  # noqa: E402
from sampler2 import PREROLL, SR, place  # noqa: E402
from synth import smooth_noise, sub as synth_sub  # noqa: E402

BPM = 84
BEAT = 5 / 7
BAR = 20 / 7
N_BEATS = 90
DURATION = N_BEATS * BEAT                               # 64.2857 s
N = int(round(DURATION * SR))
FPS = 60
SEED = 9744                                             # r-97ae / 1444: any fixed number


def G(g: float) -> float:
    return g * BEAT


# ------------------------------------------------------------------------------------------------ owner decisions
SIGNATURE = "Yazan"                                     # BRIEF-v2, owner decisions after the v2 script (final)
CARD_FOOTNOTE = False                                   # BRIEF-v2: the card does NOT carry the review footnote

# ------------------------------------------------------------------------------------------------ the boot
BOOT_T0 = 3428 / 60                                     # cut 4 on frame 3428 (even, for the 30 fps pair blend): the boot plays render(t - 57.1333)
BOOT_PLATES = [0.15, 0.2, 0.24, 0.29, 0.33, 0.38, 0.42, 0.46, 0.51, 0.55, 0.6, 0.65, 0.69, 0.73, 0.78, 0.82, 0.87,
               0.92, 0.96, 1.0, 1.05, 1.09, 1.14]       # engine/assets/boot.svg .pl --d (s from the boot start)
BOOT_LETTERS = [1.9, 1.98, 2.07, 2.15, 2.24, 2.33, 2.41, 2.5, 2.58]   # .ch --d: D O R Y L I N A E
BOOT_GLINT = (1.2, 1.95)                                # .sw sweep
BOOT_SIGNAL = 1.35                                      # .sig signal mark (0.5 s ease-out)

# ------------------------------------------------------------------------------------------------ silences
SILENCE_PRE_BOOT = (G(79), G(80))                       # 56.429 -> 57.143: exact zero, one beat
PRE_BOOT_FADE = (G(78.25), G(79))                       # everything before releases to zero over 0.536 s
MUSIC_END = G(89.4)                                     # 63.857: the picture's music end (SCRIPT-v2 §2)
END_FADE = (62.35, MUSIC_END)                           # the address note's last 1.5 s, raised cosine to exact 0

SECTIONS = [("I cold open", 0.0, G(8)), ("II scene: maya", G(8), G(20)), ("II relay", G(20), G(32)),
            ("II wait (breakdown)", G(32), G(40)), ("II theo is back", G(40), G(52)),
            ("II the answer", G(52), G(56)), ("III card", G(56), G(65)), ("IV invitation", G(65), G(79)),
            ("silence", G(79), G(80)), ("V boot", G(80), G(84)), ("V address", G(84), MUSIC_END),
            ("digital silence", MUSIC_END, DURATION)]


# ------------------------------------------------------------------------------------------------ helpers
def m(name: str) -> int:
    return name_to_midi(name)


def notes(s: str) -> list[tuple[int, bool]]:
    """'G2 D3 ~F#3' -> [(43, False), (50, False), (54, True)]; '~' = tied from the previous chord (held)."""
    return [(m(w.lstrip("~")), w.startswith("~")) for w in s.split()]


def nname(midi: int) -> str:
    return ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"][midi % 12] + str(midi // 12 - 1)


def db(x: float) -> float:
    return 10 ** (x / 20)


def todb(x: float) -> float | None:
    return None if x <= 0 else round(20 * math.log10(x), 2)


def rng_for(*key) -> np.random.Generator:
    h = hashlib.sha256(repr((SEED,) + key).encode()).digest()
    return np.random.default_rng(int.from_bytes(h[:8], "little"))


def ramp_in(n: int) -> np.ndarray:                      # 0 -> 1 raised cosine, first value exactly 0
    return 0.5 - 0.5 * np.cos(np.pi * np.arange(n) / n)


def ramp_out(n: int) -> np.ndarray:                     # 1 -> 0 raised cosine, last value exactly 0
    return 0.5 + 0.5 * np.cos(np.pi * (np.arange(n) + 1) / n)


def seal_clip(x: np.ndarray, a_ms: float, r_ms: float) -> np.ndarray:
    """Force a clip to start and end at exactly zero through raised-cosine ramps."""
    x = np.array(x, dtype=np.float64)
    na, nr = max(2, int(a_ms * SR / 1000)), max(2, int(r_ms * SR / 1000))
    shape = (-1, 1) if x.ndim == 2 else (-1,)
    x[:na] *= ramp_in(na).reshape(shape)
    x[-nr:] *= ramp_out(nr).reshape(shape)
    return x


def automation(points, n: int, t0: float = 0.0, log: bool = False) -> np.ndarray:
    """Breakpoint curve (t, value) sampled at SR from t0; raised-cosine between points (geometric when log)."""
    t = t0 + np.arange(n) / SR
    v0 = np.log(points[0][1]) if log else points[0][1]
    out = np.full(n, float(v0))
    for (ta, va), (tb, vb) in zip(points, points[1:]):
        if log:
            va, vb = math.log(va), math.log(vb)
        i0, i1 = np.searchsorted(t, [ta, tb])
        if i1 > i0:
            u = (t[i0:i1] - ta) / (tb - ta) if tb > ta else np.ones(i1 - i0)
            out[i0:i1] = va + (vb - va) * (0.5 - 0.5 * np.cos(np.pi * u))
    last = math.log(points[-1][1]) if log else points[-1][1]
    out[t >= points[-1][0]] = last
    return np.exp(out) if log else out


def biquad(kind: str, f0: float, gain_db: float = 0.0, q: float = 0.707) -> np.ndarray:
    """RBJ cookbook biquad as one SOS row: 'peak', 'lowshelf', 'highshelf'."""
    A, w = 10 ** (gain_db / 40), 2 * np.pi * f0 / SR
    cw, sw = np.cos(w), np.sin(w)
    if kind == "peak":
        al = sw / (2 * q)
        b, a = [1 + al * A, -2 * cw, 1 - al * A], [1 + al / A, -2 * cw, 1 - al / A]
    else:
        al = sw / 2 * np.sqrt(2)
        s = 2 * np.sqrt(A) * al
        if kind == "highshelf":
            b = [A * ((A + 1) + (A - 1) * cw + s), -2 * A * ((A - 1) + (A + 1) * cw), A * ((A + 1) + (A - 1) * cw - s)]
            a = [(A + 1) - (A - 1) * cw + s, 2 * ((A - 1) - (A + 1) * cw), (A + 1) - (A - 1) * cw - s]
        else:
            b = [A * ((A + 1) - (A - 1) * cw + s), 2 * A * ((A - 1) - (A + 1) * cw), A * ((A + 1) - (A - 1) * cw - s)]
            a = [(A + 1) + (A - 1) * cw + s, -2 * ((A - 1) + (A + 1) * cw), (A + 1) + (A - 1) * cw - s]
    return np.array([[b[0] / a[0], b[1] / a[0], b[2] / a[0], 1.0, a[1] / a[0], a[2] / a[0]]])


def bw(order: int, f, kind: str) -> np.ndarray:
    return butter(order, f, kind, fs=SR, output="sos")


def pre(y: np.ndarray, align: bool = False) -> np.ndarray:
    """Synth clips start at their onset: give them the sampler's PREROLL so place() treats them alike. With align,
    the clip's own attack point (1 ms envelope at peak - 15 dB, the sampler's convention) lands exactly on PREROLL."""
    y = y if y.ndim == 2 else np.stack([y, y], 1)
    k = attack_index(y) if align else 0
    return np.vstack([np.zeros((int(round(PREROLL * SR)) - k, 2)), y])


def pan_st(y: np.ndarray, p: float) -> np.ndarray:
    """Equal-power pan of a stereo clip, unity at centre."""
    th = (np.clip(p, -1, 1) + 1) * np.pi / 4
    return y * np.array([np.cos(th), np.sin(th)]) * np.sqrt(2)


def attack_index(clip: np.ndarray, rel_db: float = -15.0) -> int:
    """Where the clip's 1 ms envelope first reaches its peak + rel_db (the sampler's attack point convention)."""
    env = envelope_1ms(np.abs(clip).max(axis=1), SR)
    return int(np.argmax(env >= env.max() * db(rel_db)))


# ------------------------------------------------------------------------------------------------ cues (SCRIPT-v2)
SOUND_KINDS = {"PAPER", "LAND", "SEAL", "ARRIVE", "CLOCK", "BACK", "TYPE"}
FX_KINDS = {"PAPER", "LAND", "SEAL", "CLOCK", "TYPE"}          # rendered from the cue file (code sound design)
SCORE_KINDS = {"ARRIVE", "BACK"}                                # notes of the score: cross-checked, never moved
FX_LEVEL_DBFS = {"PAPER": -32.0, "LAND": -32.0, "SEAL": -22.0, "CLOCK": -34.0, "TYPE": -36.0}   # SCRIPT-v2 §8, peak
TYPE_BOOT_DBFS = -38.0


def script_cues() -> dict:
    """SCRIPT-v2 §3 (master timeline) and §8 (sound accents) as a cue file, plus the picture anchors the score is
    timed to (kind PIC). Owner decisions applied: signature 'Yazan' (5 typed letters), no card footnote."""
    c = []

    def add(g, kind, desc, t=None, **kw):
        tt = G(g) if t is None else t
        c.append({"t": round(tt, 6), "kind": kind, **({"G": round(g, 4)} if g is not None else {}), "desc": desc, **kw})

    # §8 sound accents
    add(2, "LAND", "the second paste (plate THEY locks in)")
    add(8.5, "PAPER", "the letter slides out of maya's machine", variant="slide")
    add(11, "LAND", "the agent's command lands on maya's terminal")
    add(12, "PAPER", "the letter folds in half", variant="fold")
    add(13.25, "PAPER", "the open envelope slides out with the letter", variant="slide")
    add(15.25, "LAND", "the address form is printed")
    add(16, "SEAL", "the seal (the lift)")
    add(16.25, "LAND", "output: Queued normal task request")
    add(20, "ARRIVE", "it sets down on the counter", note="root")
    for k in range(4):                                   # 24.571, 25.571, 26.571, 27.571: one per real second
        add(None, "CLOCK", f"clock :{41 + k} (real seconds)", t=G(33) + 1.0 * (k + 1), variant="tick" if k % 2 == 0 else "tock")
    add(40, "CLOCK", "clock :45 (theo is back)", variant="tick")
    add(40, "BACK", "theo's ring fills")
    add(43, "ARRIVE", "it sets down at theo's dock", note="fifth")
    add(44, "PAPER", "the brief slides out of theo's machine", variant="slide")
    add(44.5, "LAND", "theo's inbox lands")
    add(45.75, "LAND", "the result command lands")
    add(46.75, "PAPER", "the answer sheet slides out", variant="slide")
    add(49.75, "PAPER", "the answer sheet folds", variant="fold")
    add(50.75, "PAPER", "the answer envelope slides out", variant="slide")
    add(52, "SEAL", "the answer's seal")
    add(55, "ARRIVE", "it sets down at maya's dock", note="root")
    add(65, "PAPER", "our envelope travels in (cut 3)", variant="slide")
    add(67.5, "PAPER", "a slit opens along the top fold", variant="slit")
    add(68, "PAPER", "the letter rises out", variant="slide")
    add(69, "PAPER", "the letter unfolds", variant="unfold")
    for k, ch in enumerate(SIGNATURE):
        if ch != " ":
            add(None, "TYPE", f"signature '{SIGNATURE}': '{ch}'", t=G(76) + 0.085 * k)
    for k, d in enumerate(BOOT_LETTERS):
        add(None, "TYPE", f"boot letter {'DORYLINAE'[k]}", t=BOOT_T0 + d, level="boot")
    # picture anchors the music is timed to (no sound of their own)
    pic = [(0, "frame 0: plate YOU, caption C-1"), (2, "plate THEY, caption C-2"), (7.5, "S-1 with the name"),
           (8, "cut 1 (21 degrees) into the scene; maya pulse"), (9.75, "brief line 1 (8 lines, 1/8 beat apart)"),
           (12, "maya pulse"), (13, "S-2 Your machine seals it."), (14.5, "the letter drops into the envelope"),
           (16, "maya pulse; the seal"), (16.75, "the envelope lifts; S-3"), (21.5, "S-4 It reads the outside:"),
           (22, "pointer FROM: who,"), (23, "pointer TO: to whom,"), (24, "pointer TIME: when,"),
           (25, "pointer SIZE: how big."), (28, "S-5 It cannot open the envelope."), (28.5, "pointer under the seal tab"),
           (32, "S-6 Theo is offline. The envelope waits. (breakdown)"), (33, "the counter clock enters"),
           (40, "theo's ring fills; S-7 (return)"), (40.25, "the envelope lifts to theo"), (45, "S-8"),
           (48, "pointer under the answer"), (49, "S-9"), (52.5, "the answer travels back"), (55, "maya's arrival pulse"),
           (56, "cut 2: the card, display line"), (57, "card line 2"), (58, "card line 3"),
           (65, "cut 3: the invitation"), (69.5, "note lines 1-2"), (71, "note: Tell us what breaks."),
           (72, "note: That decides what we change first."), (73.5, "note: Free during the closed beta."),
           (75, "note: Apply at dorylinae.net"), (76, f"signature '{SIGNATURE}' types (85 ms a letter)"),
           (80, "cut 4: the boot"), (84, "Apply at dorylinae.net enters"), (90, "end")]
    for g, d in pic:
        add(g, "PIC", d)
    for k, d in enumerate(BOOT_PLATES):
        add(None, "PIC", f"boot plate {k + 1}", t=BOOT_T0 + d)
    add(None, "PIC", "boot glint sweep", t=BOOT_T0 + BOOT_GLINT[0])
    add(None, "PIC", "boot signal mark", t=BOOT_T0 + BOOT_SIGNAL)
    add(None, "PIC", "the music is silent from here (SCRIPT-v2 §2)", t=MUSIC_END)
    c.sort(key=lambda e: (e["t"], e["kind"]))
    return {"cues": c, "duration": DURATION, "format": "all", "formats": ["16x9", "4x5"], "bpm": BPM,
            "source": "design/SCRIPT-v2.md §3 + §8 (both formats share every time), owner decisions from BRIEF-v2: "
                      f"signature '{SIGNATURE}', card footnote {'on' if CARD_FOOTNOTE else 'off'}. Kinds in "
                      f"{sorted(SOUND_KINDS)} are sound cues; PIC are picture anchors the score is timed to."}


# ================================================================================================ THE SCORE (data)
# Piano chords (Osiris, sustain pedal down; the pedal changes on every chord). '~' = a common tone held over from the
# previous chord (not struck again). (G, label, voicing, velocity, roll s, Kawai doubling). Bass notes are voiced
# softer (a low Osiris note carries about 10x the energy of a mid one, PALETTE §3.1).
# Voicings obey the low-interval limits (checked in harmony_checks): nothing closer than a fifth below Bb2, no
# third below C3, no second below Eb3.
CHORDS = [
    (8, "Dadd9 (the music arrives with the cut)", "D2 A2 F#3 A3 E4", 0.24, 0.055, False),
    (12, "Bm7 (the fold)", "B1 B2 ~F#3 ~A3 D4", 0.25, 0.040, False),
    (14, "Gmaj9", "G2 D3 ~F#3 ~A3 B3", 0.24, 0.035, False),
    (15, "G/A (A9sus4)", "A2 G3 ~B3 D4", 0.22, 0.030, False),
    (16, "D (the lift, the seal)", "D2 A2 D3 A3", 0.36, 0.025, True),
    (18, "A/C#", "C#2 E3 ~A3 E4", 0.28, 0.030, True),
    (20, "Bm7 (arrival at the counter)", "B2 F#3 A3 D4", 0.27, 0.035, False),
    (22, "Bm7/A", "A2 ~F#3 ~A3 ~D4", 0.20, 0.0, False),
    (24, "Gmaj9", "G2 D3 ~F#3 ~A3 B3", 0.25, 0.035, False),
    (26, "D/F#", "F#2 ~D3 ~A3 D4", 0.22, 0.030, False),
    (28, "Em11 (It cannot open the envelope.)", "E2 B2 ~A3 ~D4 G4", 0.26, 0.040, False),
    (32, "A9sus4 (the breakdown: no third)", "A2 E3", 0.15, 0.080, False),
    (40, "D (the return)", "D2 A2 F#3 A3 D4", 0.40, 0.030, True),
    (44, "Gmaj9 (opened)", "G2 D3 F#3 ~A3 B3", 0.27, 0.035, False),
    (46, "Bm7", "B1 B2 ~F#3 ~A3 D4", 0.25, 0.035, False),
    (48, "Em11 (the pointer under the answer)", "E2 B2 ~A3 ~D4 G4", 0.26, 0.035, False),
    (50, "A7sus4", "A2 E3 G3 ~D4", 0.24, 0.030, False),
    (52, "D (the answer's seal)", "D2 A2 D3 A3", 0.36, 0.025, True),
    (54, "A/C#", "C#2 E3 ~A3 E4", 0.28, 0.030, True),
    (55, "D (home: maya's dock)", "D2 A2 F#3 ~A3 D4", 0.31, 0.030, True),
    (56, "Gmaj9 (the card)", "G2 D3 A3 B3", 0.36, 0.030, True),
    (58, "A7sus4", "A2 E3 G3 D4", 0.34, 0.030, True),
    (60, "D (the cadence)", "D2 A2 F#3 A3 D4", 0.40, 0.030, True),
    (62, "Bm7", "B1 B2 ~F#3 ~A3 ~D4", 0.25, 0.040, False),
    (64, "Bm7/A", "A2 ~F#3 ~A3 ~D4", 0.20, 0.0, False),
    (65, "Gmaj7 (cut 3: the invitation)", "G2 D3 F#3 B3", 0.30, 0.040, False),
    (68, "Bm7 (the letter rises)", "B1 B2 ~F#3 A3 D4", 0.27, 0.040, False),
    (70, "Gmaj7", "G2 D3 ~F#3 B3", 0.25, 0.040, False),
    (72, "Em11 (the outro)", "E2 B2 G3 D4 A4", 0.27, 0.040, False),
    (74, "A7sus4", "A2 E3 G3 ~D4", 0.25, 0.035, False),
    (76, "Gmaj9 (the signature: an open IV)", "G2 D3 A3 B3", 0.27, 0.045, False),
]
PEDAL_UP = G(78.25)                                     # the last pedal lift before the silence
CHORD_END = {76: G(78.25)}                              # explicit releases (default: the next chord)

# The melody (Osiris; Kawai doubling where marked). (G, note, velocity, Kawai, role)
MELODY = [
    (16, "F#4", 0.53, True, "M1 on the seal"),
    (17, "A4", 0.47, True, "M2"),
    (18, "E5", 0.52, True, "M3, held over the flight"),
    (20, "D5", 0.46, True, "M4 = ARRIVE on the counter (root)"),
    (32, "F#4", 0.30, False, "M1: theo is offline"),
    (33, "A4", 0.26, False, "M2 ... and the motif waits (the clock enters)"),
    (40, "E5", 0.52, True, "M3, the missing note, on the ring fill"),
    (43, "D5", 0.45, True, "M4 = ARRIVE at theo's dock"),
    (43, "A4", 0.33, False, "ARRIVE: theo's fifth under it"),
    (52, "F#4", 0.52, True, "M1: the answer, sealed the same way"),
    (53, "A4", 0.45, True, "M2"),
    (54, "E5", 0.50, True, "M3"),
    (55, "D5", 0.46, True, "M4 = ARRIVE at maya's dock (root)"),
    (56, "F#4", 0.53, True, "M1, card line 1: End-to-end encrypted."),
    (57, "A4", 0.51, True, "M2, card line 2"),
    (58, "E5", 0.54, True, "M3, card line 3"),
    (60, "D5", 0.48, True, "M4: the cadence"),
    (69.5, "F#4", 0.40, True, "M1 (augmented), note lines 1-2"),
    (71, "A4", 0.38, True, "M2, Tell us what breaks."),
    (72, "E5", 0.42, True, "M3, the outro line"),
    (76, "D5", 0.35, True, "M4 on the signature"),
]
MELODY_HOLD_MAX = 4 * BEAT                              # a finger holds a melody note at most a bar
# The breakdown lands on a drop (QA polish, item 3): the G32 roll (A2 E3) and the motif's two notes (F#4 G32, A4 G33)
# sit 5 dB under their velocities' level, so bar 9's downbeat is under bar 8, not a step over it.
NOTE_GAIN_DB = {32: -5.0, 33: -5.0}                     # G -> dB on the piano notes struck there
# Clean pedalling (QA polish, item 4): a low note (< C4) that leaves at a pedal change while a note a step away
# (1 or 2 semitones, in any octave up to two) enters is damped in STEP_RELEASE instead of its sfz damper time
# (0.5-0.8 s in the bass: the old tail sat a minor second against the new bass for 0.2-0.4 s). A pad or string
# entry a semitone (or a major 7th / minor 9th) from such a note waits STEP_ENTRY_DELAY, until it has gone.
STEP_RELEASE = 0.25
STEP_ENTRY_DELAY = 0.25
STEP_LOW = 60                                           # below C4

# The 8 brief lines (G9.75 -> G10.625, one every 1/8 beat): one soft note per line, falling through D pentatonic.
RUN = [(9.75 + 0.125 * k, n, v) for k, (n, v) in enumerate(
    zip("D6 B5 A5 F#5 E5 D5 B4 A4".split(), (0.15, 0.16, 0.16, 0.17, 0.16, 0.15, 0.14, 0.13)))]

# The bed: a designed pad in four lines. A change of pitch is an equal-power crossfade (PAD_XF); a held common tone
# is one continuous tone. '-' = the line rests.
PAD_CHORDS = [
    (7.5, "D3 A3 E4 -"), (12, "B2 A3 D4 -"), (14, "G2 A3 D4 -"), (15, "A2 G3 D4 -"), (16, "D3 A3 F#4 -"),
    (18, "C#3 A3 E4 -"), (20, "B2 A3 D4 -"), (22, "A2 B3 D4 -"), (24, "G2 B3 D4 -"), (26, "F#2 A3 D4 -"),
    (28, "E2 A3 D4 -"), (32, "A2 B3 D4 E3"), (40, "A2 A3 D4 F#3"), (44, "G2 A3 D4 F#3"), (46, "B2 A3 D4 F#3"),
    (48, "E3 A3 D4 G3"), (50, "A2 A3 D4 G3"), (52, "D3 A3 D4 F#3"), (54, "C#3 A3 E4 E3"), (55, "D3 A3 D4 F#3"),
    (56, "G2 A3 D4 F#3"), (58, "A2 A3 D4 G3"), (60, "D3 A3 D4 F#3"), (62, "B2 A3 D4 F#3"), (64, "A2 A3 D4 F#3"),
    (65, "G2 B3 D4 F#3"), (68, "B2 A3 D4 F#3"), (70, "G2 B3 D4 F#3"), (72, "E3 B3 D4 G3"), (74, "A2 A3 D4 G3"),
    (76, "G2 A3 D4 F#3"), (78.25, "- - - -"),
]
PAD_XF = 0.5                                            # s, crossfade at a change of pitch
PAD_SEQ_IN = (0.20, 0.45)                               # s: a semitone handover enters 0.2 s late, over 0.45 s
PAD_ENTRY = 0.62                                        # s, the first swell (from G7.5, the name: S-1)
PAD_EXIT = 0.6                                          # s, the last release (from G78.25)
# Spectral envelope of the pad (Gaussian, centre in Hz): it opens on the lift, the return and the card, and closes
# in the wait. (G, Hz)
PAD_FC = [(7.5, 620), (15.5, 650), (17.5, 1900), (20, 1700), (22, 1300), (28, 1000), (32, 900), (33.2, 470),
          (38, 520), (40, 650), (41.6, 2000), (44, 1500), (52, 1500), (53, 1800), (56, 1900), (60, 1700),
          (65, 1250), (76, 1100), (79, 1000)]
# Pad level (dB re its reference level). (G, dB)
PAD_LEVEL = [(7.5, 0.0), (15.5, 0.0), (16.5, 1.0), (20, 0.5), (22, 0.0), (31.5, 0.0), (32.5, -4.5), (39, -4.5),
             (40.5, 0.0), (43.5, 0.0), (44.5, -2.0), (51, -2.0), (52.5, 0.0), (55.5, 0.0), (56.5, 0.5), (61, 0.0),
             (65, -0.5), (76, -1.0)]

# Strings (VSCO-2 sections, soft layer, vibrato): warmth at the lift, the return, the card and the invitation.
# (G on, G off, note, velocity, attack s). In the invitation the cellos double the melody an octave down.
CELLOS = [(14, 18.12, "D3", 0.30, 1.4), (18, 20.12, "C#3", 0.30, 0.35), (20, 22.12, "B2", 0.30, 0.35),
          (37.5, 44.12, "A2", 0.30, 2.4),
          (55.4, 58.12, "G2", 0.32, 0.6), (58, 60.12, "A2", 0.32, 0.35), (60, 62.12, "D3", 0.34, 0.35), (62, 64.12, "B2", 0.32, 0.35),
          (64, 65.12, "A2", 0.30, 0.3), (65, 68.12, "G2", 0.30, 0.4), (68, 69.62, "B2", 0.30, 0.35),
          (69.5, 71.12, "F#3", 0.32, 0.3), (71, 72.12, "A3", 0.32, 0.3), (72, 76.12, "E4", 0.32, 0.3),
          (76, 78.4, "D4", 0.30, 0.3)]
VIOLAS = [(14, 22.5, "A3", 0.28, 1.4), (37.5, 40.12, "E3", 0.28, 2.4), (40, 44.5, "F#3", 0.30, 0.35),
          (55.4, 64.5, "D4", 0.30, 0.6), (65, 76.12, "D4", 0.28, 0.5), (76, 78.4, "B3", 0.28, 0.3)]
# Bowed vibraphone swells into the lift and the return, and holds the signature's note. (G on, G off, notes, vel, attack)
BOWED = [(13, 20.5, ["A4", "F#5"], 0.6, 1.8), (37, 42.5, ["A4", "F#5"], 0.6, 2.2), (76, 78.4, ["D5"], 0.45, 0.8)]

# Mallets. Vibraphone (soft mallets): the paper colour, the counter reading the form, theo's working pulse.
VIBES = [(8.5, "F#5", 0.35, "the letter comes out (paper colour)"),
         (13.25, "D5", 0.30, "the envelope comes out"),
         (16, "D5", 0.30, "the seal"),
         (22, "B5", 0.38, "who,"), (23, "A5", 0.36, "to whom,"), (24, "F#5", 0.38, "when,"), (25, "D5", 0.36, "how big."),
         (52, "D5", 0.30, "the answer's seal"),
         (62, "F#5", 0.32, "M1 echo (the card holds)"), (63, "A5", 0.30, "M2 echo"),
         (65, "D5", 0.30, "our envelope (paper colour)"),
         (73.5, "B5", 0.26, "note: Free during the closed beta."), (75, "A5", 0.26, "note: Apply at dorylinae.net")]
# theo's agent works: eighths alternating D5 (on the beat) and A4 (off), G44.5 -> G50.5, swelling and fading.
PULSE = [(g, "D5" if float(g).is_integer() else "A4",
          round(0.20 + 0.08 * math.sin(math.pi * (g - 44.5) / 6.0), 3)) for g in np.arange(44.5, 50.51, 0.5)]
PULSE_DUR = 0.30
# QA polish (item 7): the vibraphone doubles the motif an octave up, softly, wherever the open grand plays it (the
# lift, the return, the answer, the card): the line reads on a phone speaker without brightening the piano itself.
MELODY_DOUBLE = [(16, "F#5"), (17, "A5"), (18, "E6"), (20, "D6"), (40, "E6"), (43, "D6"), (52, "F#5"), (53, "A5"),
                 (54, "E6"), (55, "D6"), (56, "F#5"), (57, "A5"), (58, "E6"), (60, "D6")]
MELODY_DOUBLE_VEL, MELODY_DOUBLE_DB = 0.24, -6.0
CHIMES = [(16, "D6", 0.55, "the seal"), (40, "A5", 0.65, "BACK: theo's ring fills (theo = the fifth)"),
          (52, "D6", 0.55, "the answer's seal")]
CHIME_TRIM_DB = {16: -3.0, 52: -3.0}                    # the seal bell sits under the motif's first note
CELESTA = [(44, "D6", 0.80, "opened: the brief comes out of theo's machine")]

# The heartbeat (presence): LUB = maya (felt bass drum + sub on D1), DUB = theo (softer, sub on A1, half a beat
# later), once per bar with the presence pulse. Reduced to maya's alone in the wait; theo's returns on G40.
# (G, kind, strength) strength: 'soft' | 'mid' | 'strong' | 'distant'
HEART = []
for g in (8, 12, 16, 20, 24, 28, 40, 44, 48, 52):
    s = "strong" if g in (16, 40, 52) else ("soft" if g == 8 else "mid")
    HEART += [(g, "LUB", s), (g + 0.5, "DUB", s)]
HEART += [(32, "LUB", "distant"), (36, "LUB", "distant"), (55, "LUB", "soft"), (84, "LUB", "soft")]
HEART.sort()
HEART_NOTE = {"LUB": 26, "DUB": 33}                    # D1, A1

# The boot (relative to BOOT_T0): M's notes rising on the plates, the motif chord blooming.
BOOT_ARP = [(BOOT_PLATES[0], ["D2", "F#3"], [0.34, 0.40]), (BOOT_PLATES[6], ["A3"], [0.42]),
            (BOOT_PLATES[12], ["E4"], [0.45]), (BOOT_PLATES[18], ["D5"], [0.47])]
BOOT_CELESTA = (BOOT_PLATES[22], "F#6", 0.8)            # the last plate
BOOT_CHIME = (BOOT_SIGNAL, "A5", 0.45)                  # the signal mark
BOOT_VIBES = (BOOT_PLATES[18], "D5", 0.40)
BOOT_BLOOM = {"notes": ["D3", "F#3", "A3", "E4"], "on": 0.55, "attack": 1.3, "off": G(84), "release": 2.2,
              "fc": [(0.55, 600), (2.2, 1600), (3.0, 1500)]}
BOOT_STRINGS = [("cellos", "D3", 0.28), ("violas", "A3", 0.27)]
BOOT_BOWED = (["A4", "E5"], 0.55)
ADDRESS = (G(84), "D2", 0.34)                           # the last note, under 'Apply at dorylinae.net'


# ================================================================================================ rendering: score
STRUCK = {"piano", "grand", "vibes", "chimes", "celesta", "bassdrum", "sub", "fx"}


class Stems:
    """Stereo stems in two halves (before the silence, and the boot onwards: each half gets its own reverb and its
    own exact-zero mask, so nothing from before the silence can return). Every placement is logged with its
    measured attack (struck sounds) or swell peak (sustained sounds)."""

    def __init__(self):
        self.pre: dict[str, np.ndarray] = {}
        self.post: dict[str, np.ndarray] = {}
        self.events: list[dict] = []
        self.fx_clips: list[tuple] = []                  # (t, clip, kind, event index): for the accent rider

    def add(self, stem: str, clip: np.ndarray, t: float, *, inst: str, struck: bool = True,
            measure: np.ndarray | None = None, **meta):
        half = self.pre if t < BOOT_T0 - 0.05 else self.post
        buf = half.setdefault(stem, np.zeros((N, 2)))
        place(buf, clip, t)
        start = t - PREROLL
        rec = {"t": round(t, 6), "stem": stem, "inst": inst, **meta}
        if struck:                                       # attack point, measured on the clip (before any shave)
            mc = clip if measure is None else measure
            k = min(len(mc), int(round((PREROLL + 0.45) * SR)))
            ia = attack_index(mc[:k])
            rec["attack_measured"] = round(start + ia / SR, 6)
            rec["attack_err_ms"] = round(1000 * (start + ia / SR - t), 3)
            rec["peak_dbfs_ref"] = todb(float(np.abs(clip).max()))
        else:                                            # a swell: when it reaches its maximum (100 ms RMS)
            e = uniform_filter1d(np.mean(clip ** 2, axis=1), int(0.1 * SR))
            rec["swell_peak_at"] = round(start + int(np.argmax(e)) / SR, 4)
        self.events.append(rec)


def pedal_changes() -> list[float]:
    return sorted({G(c[0]) for c in CHORDS} | {G(43), PEDAL_UP})


def piano_notes() -> list[dict]:
    """The piano part as notes with on/off times: chords (common tones held), the melody (a finger holds it until
    the next melody note, at most a bar; the pedal then holds it to the next pedal change), the brief-lines run."""
    out, sounding = [], {}
    pedals = pedal_changes()
    for idx, (g, label, voicing, vel, roll, kawai) in enumerate(CHORDS):
        t = G(g)
        end = CHORD_END.get(g) or (G(CHORDS[idx + 1][0]) if idx + 1 < len(CHORDS) else PEDAL_UP)
        new, k = {}, 0
        struck = [mi for mi, tied in notes(voicing) if not (tied and mi in sounding)]
        top = max(struck) if struck else None
        for mi, tied in notes(voicing):
            if tied and mi in sounding:
                rec = sounding[mi]
                rec["off"] = end
                rec["held_to"] = label
            else:
                r = rng_for("vel", g, mi)
                shape = 0.62 if mi < 48 else (1.0 if mi == top else 0.88)
                rec = {"on": t + k * roll, "midi": mi, "vel": round(vel * shape + 0.012 * r.standard_normal(), 4),
                       "off": end, "kawai": kawai and mi >= 45, "kawai_db": KAWAI_CHORD_DB, "role": f"chord {label}", "G": g,
                       "gain_db": NOTE_GAIN_DB.get(g, 0.0)}
                out.append(rec)
                k += 1
            new[mi] = rec
        sounding = new
    mel = sorted(MELODY, key=lambda x: x[0])
    for i, (g, n, v, kawai, role) in enumerate(mel):
        t = G(g)
        nxt = [G(x[0]) for x in mel[i + 1:] if x[0] > g]
        finger = min(nxt[0] if nxt else t + MELODY_HOLD_MAX, t + MELODY_HOLD_MAX)
        want = max(t + 0.3, finger)
        cand = [p for p in pedals if p >= want - 1e-9]
        off = min(cand[0] if cand else PEDAL_UP, PEDAL_UP)
        kdb = KAWAI_INVITATION_DB if g >= 65 else KAWAI_MELODY_DB
        out.append({"on": t, "midi": m(n), "vel": v, "off": off, "kawai": kawai, "kawai_db": kdb, "role": role, "G": g,
                    "melody": True, "gain_db": NOTE_GAIN_DB.get(g, MELODY_GAIN_DB)})
    for g, n, v in RUN:
        t = G(g)
        off = min([p for p in pedals if p >= t + 0.3] + [PEDAL_UP])
        out.append({"on": t, "midi": m(n), "vel": v, "off": off, "kawai": False, "role": "brief line", "G": g})
    out.sort(key=lambda r: (r["on"], r["midi"]))
    # clean pedalling (see STEP_RELEASE): (a) the bass, when the next bass is a step away (pitch class 1-2 semitones,
    # up to two octaves apart); (b) any low chord tone with a note a semitone / major 7th / minor 9th from it entering
    # in the low register (the piano's, the pad's or a string's)
    chord_low = {}
    for r in out:
        if r["role"].startswith("chord"):
            chord_low[r["G"]] = min(chord_low.get(r["G"], 999), r["midi"])
    for r in out:
        if not r["role"].startswith("chord") or r["midi"] >= STEP_LOW:
            continue
        hit = []
        if r["midi"] == chord_low[r["G"]]:
            nb = [q["midi"] for q in out if q["role"].startswith("chord") and abs(q["on"] - r["off"]) < 1e-6]
            if nb and 0 < abs(min(nb) - r["midi"]) <= 26 and abs(min(nb) - r["midi"]) % 12 in (1, 2, 10, 11):
                hit.append(min(nb))
        hit += [p for p in entering_at(r["off"], out) if p < 64 and abs(p - r["midi"]) in (1, 11, 13)]
        if hit:
            r["release"] = STEP_RELEASE
            r["step_to"] = sorted(nname(p) for p in set(hit))
    return out


def entering_at(t: float, pn: list[dict]) -> list[int]:
    """Pitches that start at time t (within 0.25 s after it): struck piano notes, new pad tones, string entries."""
    out = [r["midi"] for r in pn if t - 0.01 <= r["on"] <= t + 0.25 and not r.get("melody")]
    prev = None
    for g, spec in PAD_CHORDS:
        cur = [None if w == "-" else m(w) for w in spec.split()]
        if abs(G(g) - t) < 1e-6 and prev is not None:
            out += [c for c, p in zip(cur, prev) if c is not None and c != p]
        prev = cur
    out += [m(n) for part in (CELLOS, VIOLAS) for g0, g1, n, v, a in part if abs(G(g0) - t) < 0.2]
    return out


def leaving_low_at(t: float, pn: list[dict]) -> list[int]:
    """Low pitches (< C4) that stop at time t: piano chord tones released there, pad tones changed, string notes."""
    out = [r["midi"] for r in pn if abs(r["off"] - t) < 1e-6 and not r.get("melody") and r["midi"] < STEP_LOW]
    prev = None
    for g, spec in PAD_CHORDS:
        cur = [None if w == "-" else m(w) for w in spec.split()]
        if abs(G(g) - t) < 1e-6 and prev is not None:
            out += [p for c, p in zip(cur, prev) if p is not None and c != p and p < STEP_LOW]
        prev = cur
    out += [m(n) for part in (CELLOS, VIOLAS) for g0, g1, n, v, a in part if abs(G(g1) - t) < 0.2 and m(n) < STEP_LOW]
    return out


def clashes_semitone(p: int, low: list[int]) -> bool:
    return any(abs(p - q) in (1, 11, 13) for q in low)


def string_notes(pn: list[dict]) -> list[dict]:
    """The string parts as notes. A note followed in its own part by a step (1-2 semitones) ends where the next
    begins and is released in STEP_RELEASE (not 0.12 beat later with 0.6 s); an entry a semitone from a low note
    that leaves at that moment waits STEP_ENTRY_DELAY (the cello C#3 at G18 under the piano's and cello's D3)."""
    out = []
    for inst, part, gdb in (("cellos", CELLOS, CELLO_DB), ("violas", VIOLAS, VIOLA_DB)):
        seq = sorted(part)
        for i, (g0, g1, n, v, a) in enumerate(seq):
            on, off, rel, mi = G(g0), G(g1), 0.6, m(n)
            nxt = seq[i + 1] if i + 1 < len(seq) else None
            if nxt and G(nxt[0]) <= off + 0.05 and abs(m(nxt[2]) - mi) in (1, 2):
                off, rel = G(nxt[0]), STEP_RELEASE
            delay = STEP_ENTRY_DELAY if clashes_semitone(mi, leaving_low_at(G(g0), pn)) else 0.0
            out.append({"inst": inst, "midi": mi, "note": n, "vel": v, "attack": a, "gain_db": gdb, "G": g0,
                        "on": on + delay, "off": off, "release": rel, "entry_delay": delay})
    return out


def fc_curve() -> np.ndarray:
    return automation([(G(g), hz) for g, hz in PAD_FC], N, log=True)


def pad_tone(midi: int, t0: float, t1: float, a: float, r: float, a_kind: str, r_kind: str, fc: np.ndarray,
             seed_key, tilt: float = 1.15, subs: int = 3, detune: float = 3.0, width: float = 0.5) -> tuple[int, np.ndarray]:
    """One pad tone from t0 (start of its attack, length a) to t1 (start of its release, length r): additive,
    harmonically locked partials of 3 sub-voices (+/- detune cents, each with a slow ~1 cent drift and amplitude
    drift), a Gaussian spectral envelope centred on fc(t), nothing above 9 kHz (no aliasing). a_kind / r_kind:
    'xf' (equal-power quarter-sine, for crossfades) or 'rc' (raised cosine). Returns (start sample, stereo clip);
    unit RMS per tone at fc = 800 Hz."""
    rng = rng_for("PAD", midi, round(t0, 4), seed_key)
    i0 = int(round(t0 * SR))
    n = int(round((t1 + r) * SR)) - i0
    n = min(n, N - i0)
    tt = np.arange(n) / SR
    f0 = midi_to_hz(midi)
    fcs = fc[i0: i0 + n]
    env = np.ones(n)
    na, nr = max(2, int(a * SR)), max(2, int(r * SR))
    u = np.arange(na) / na
    env[:na] = np.sin(0.5 * np.pi * u) if a_kind == "xf" else 0.5 - 0.5 * np.cos(np.pi * u)
    k1 = int(round((t1 - t0) * SR))
    if k1 < n:
        m_ = min(nr, n - k1)
        u = (np.arange(m_) + 1) / nr
        env[k1: k1 + m_] *= np.cos(0.5 * np.pi * u) if r_kind == "xf" else 0.5 + 0.5 * np.cos(np.pi * u)
        env[k1 + m_:] = 0.0
    env[0] = 0.0
    env[-1] = 0.0
    H = int(9000 // f0)
    hs = np.arange(1, H + 1)
    norm = math.sqrt(float(np.sum((hs ** -tilt * np.exp(-(hs * f0 / 800.0) ** 2)) ** 2)) / 2 * subs)
    fmax = float(fcs.max())
    out = np.zeros((n, 2))
    pans = np.linspace(-width, width, subs) if subs > 1 else np.array([0.0])
    for s in range(subs):
        dc = (s - (subs - 1) / 2) / max(1, (subs - 1) / 2) * detune
        cents = dc + 1.0 * smooth_noise(n, SR, 0.15, rng)
        ph = 2 * np.pi * np.cumsum(f0 * 2 ** (cents / 1200)) / SR
        sig = np.zeros(n)
        for h in hs:
            if h * f0 > 3.1 * fmax:
                break
            amp = h ** -tilt * np.exp(-((h * f0) / fcs) ** 2)
            sig += amp * np.sin(h * ph + rng.uniform(0, 2 * np.pi))
        sig *= 1 + 0.08 * smooth_noise(n, SR, 0.25, rng)
        th = (pans[s] + 1) * np.pi / 4
        out[:, 0] += sig * np.cos(th) * math.sqrt(2)
        out[:, 1] += sig * np.sin(th) * math.sqrt(2)
    return i0, out * (env / norm)[:, None]


def pad_part(st: Stems, fc: np.ndarray, ref_db: float, pn: list[dict] | None = None) -> list[dict]:
    """The bed: PAD_CHORDS as four continuous lines. Returns the tones for the report. A change of pitch is an
    equal-power crossfade, except where the new tone is a semitone (or a major 7th / minor 9th) from a low note that
    leaves at that moment (the line's own, the piano's or a string's): then the old tone is released in STEP_RELEASE
    and the new one enters after it (PAD_SEQ_IN), so the two never sound together."""
    pn = piano_notes() if pn is None else pn
    lines: list[list[tuple[float, int | None]]] = [[], [], [], []]
    for g, spec in PAD_CHORDS:
        for i, w in enumerate(spec.split()):
            lines[i].append((G(g), None if w == "-" else m(w)))
    tones = []
    for li, seq in enumerate(lines):
        segs = []                                        # merge held common tones
        for (t, mi) in seq:
            if segs and segs[-1][2] == mi:
                continue
            if segs:
                segs[-1][1] = t
            segs.append([t, None, mi])
        segs = [s for s in segs if s[2] is not None and s[1] is not None]
        for k, (ta, tb, mi) in enumerate(segs):
            prev_join = k > 0 and abs(segs[k - 1][1] - ta) < 1e-9
            next_join = k + 1 < len(segs) and abs(segs[k + 1][0] - tb) < 1e-9
            seq_in = prev_join and clashes_semitone(mi, leaving_low_at(ta, pn))
            seq_out = next_join and clashes_semitone(segs[k + 1][2], leaving_low_at(tb, pn))
            if seq_in:
                t0, a, ak = ta + PAD_SEQ_IN[0], PAD_SEQ_IN[1], "rc"
            elif prev_join:
                t0, a, ak = ta - PAD_XF / 2, PAD_XF, "xf"
            else:
                t0, a, ak = ta, (PAD_ENTRY if ta < G(8) else 0.9), "rc"
            if seq_out:
                t1, r, rk = tb - 0.03, STEP_RELEASE, "rc"
            elif next_join:
                t1, r, rk = tb - PAD_XF / 2, PAD_XF, "xf"
            else:
                t1, r, rk = tb, PAD_EXIT, "rc"
            i0, y = pad_tone(mi, t0, t1, a, r, ak, rk, fc, ("line", li))
            buf = st.pre.setdefault("bed", np.zeros((N, 2)))
            buf[i0: i0 + len(y)] += y * db(ref_db)
            tones.append({"line": li + 1, "note": nname(mi), "from": round(ta, 4), "to": round(tb, 4),
                          **({"enters_after_release_s": round(t0 - ta, 3)} if seq_in else {})})
    return tones


def pad_level_curve() -> np.ndarray:
    return db(automation([(G(g), v) for g, v in PAD_LEVEL], N))


# ------------------------------------------------------------------------------------------------ sound design (code)
def _band_noise(rng, n, lo, hi, order=2):
    return sosfilt(bw(order, [lo, hi], "band"), rng.standard_normal(n + 256))[256:]


def fx_paper(variant: str, rng) -> np.ndarray:
    """Paper: a slide (130 ms), a fold (90 ms + a crease), a slit (110 ms, dense fibre crackle) or an unfold (150 ms,
    two soft creases). Band 800 Hz to 6 kHz, tilted down above 3 kHz; the friction swells and fades with the speed;
    a sparse fibre crackle rides on it. Mono, peak 1."""
    L = {"slide": 0.13, "fold": 0.09, "slit": 0.11, "unfold": 0.15}[variant]
    n = int(L * SR)
    t = np.arange(n) / SR
    body = _band_noise(rng, n, 800, 6000, order=4)
    body = sosfilt(biquad("highshelf", 3000, -4.0), body)
    rough = 0.75 + 0.5 * uniform_filter1d(rng.standard_normal(n), int(SR / 300))  # fibre roughness, ~300 Hz
    body *= rough / (np.abs(rough).max() + 1e-12)
    rise = np.clip(t / 0.006, 0, 1)                      # the paper starts to move (6 ms)
    env = (0.5 - 0.5 * np.cos(np.pi * rise)) * (0.45 + 0.55 * np.sin(np.pi * np.clip(t / L, 0, 1)))  # then its speed
    y = body * env
    rate = {"slide": 220, "fold": 260, "slit": 900, "unfold": 200}[variant]
    nimp = rng.poisson(rate * L)
    crack = np.zeros(n)
    for i in rng.integers(0, n - 64, size=nimp):
        crack[i] += rng.uniform(0.3, 1.0) * (1 if rng.random() < 0.5 else -1)
    crack = sosfilt(bw(2, [1200, 3500], "band"), sosfilt(bw(4, 3500, "low"), crack))
    crack *= env
    y = y / (np.abs(y).max() + 1e-12) + (0.25 if variant != "slit" else 0.6) * crack / (np.abs(crack).max() + 1e-12)
    for tc in {"fold": [0.075], "unfold": [0.06, 0.125], "slide": [], "slit": []}[variant]:
        k = int(tc * SR)
        c = _band_noise(rng, 240, 1200, 3500) * np.exp(-np.arange(240) / 40.0)
        c[:72] *= ramp_in(72)                               # a crease, not a click: 1.5 ms rise
        y[k: k + 240] += 0.45 * c / (np.abs(c).max() + 1e-12)
    y = seal_clip(y, 4.0, 12.0)
    return y / np.abs(y).max()


def modal(f: float, L: float, partials, tau: float, attack_ms: float, rng, noise_db=-20.0, noise_band=(600, 2500),
          noise_tau=0.001, drop=0.0, drop_tau=0.012, rel_ms=3.0) -> np.ndarray:
    """Struck object by modal synthesis: (ratio, amplitude, decay scale) partials below 16 kHz, a contact noise,
    an optional pitch drop (semitones), raised-cosine attack and end. Mono, peak 1."""
    n = int(L * SR)
    t = np.arange(n) / SR
    bend = 2 ** (drop * np.exp(-t / drop_tau) / 12) if drop else np.ones(n)
    y = np.zeros(n)
    for r_, a_, ds in partials:
        if f * r_ * (2 ** (drop / 12)) >= 16000:
            continue
        ph = 2 * np.pi * np.cumsum(f * r_ * bend) / SR
        ph -= ph[0]
        y += a_ * np.sin(ph) * np.exp(-t / (tau * ds))
    nz = rng.standard_normal(n) * np.exp(-t / noise_tau)
    nz = sosfilt(bw(2, list(noise_band), "band"), nz)
    y += db(noise_db) * nz / (np.abs(nz).max() + 1e-12) * np.abs(y).max()
    y = seal_clip(y, attack_ms, rel_ms)
    return y / np.abs(y).max()


def fx_land(rng) -> np.ndarray:                          # dry wood tick, 20 ms, tuned to D6 (v1's LAND)
    return modal(midi_to_hz(86), 0.020, [(1, 1, 1), (1.58, 0.55, 0.8), (2.37, 0.22, 0.5)], 0.0045, 0.4, rng,
                 noise_db=-8, noise_band=(600, 2500), noise_tau=0.0006, rel_ms=5)


def fx_seal(rng) -> np.ndarray:
    """A felt stamp pressed onto paper, 140 ms: a felt thump on D3 falling 1.5 semitones (a stamp, not a hit),
    the felt's low contact, and the paper giving under it (800 Hz to 3 kHz, 12 ms)."""
    y = modal(midi_to_hz(50), 0.14, [(1, 1, 1), (2.0, 0.28, 0.6), (3.0, 0.08, 0.35)], 0.034, 2.0, rng,
              noise_db=-12, noise_band=(60, 600), noise_tau=0.004, drop=1.5, drop_tau=0.012, rel_ms=30)
    n = len(y)
    p = _band_noise(rng, n, 800, 3000) * np.exp(-np.arange(n) / (0.012 * SR))
    y = y + db(-10) * p / (np.abs(p).max() + 1e-12)       # the paper's give (was -14 dB: it is what reads)
    y = seal_clip(y, 2.0, 30.0)
    # polarity: the stamp's first half-cycle lands on the heartbeat's (same sample, same sign: +0.2 on a -0.82 peak);
    # inverted, the two no longer stack and the limiter is not asked for 2 dB at the seals
    return -y / np.abs(y).max()


def fx_clock(variant: str, rng) -> np.ndarray:
    """A soft clock, 25 ms of wood: 'tick' (2.1 kHz, body 700 Hz) and 'tock' (1.5 kHz, body 520 Hz) alternate."""
    f, body = (2100.0, 700.0) if variant == "tick" else (1500.0, 520.0)
    y = modal(f, 0.025, [(1, 1, 1), (2.7, 0.30, 0.5), (body / f, 0.55, 2.0)], 0.0028, 0.3, rng,
              noise_db=-14, noise_band=(1000, 4000), noise_tau=0.0005, rel_ms=4)
    return y


def fx_type(rng) -> np.ndarray:
    """Paper tap: a 6 ms tap (band 1 to 3 kHz, a little 380 Hz body) and the sheet giving under it (700 Hz to
    3.5 kHz noise, 4 ms decay, 8 dB under, 14 ms in all): the 'give' lets the tap read at a modest peak level."""
    c = 1 + 0.07 * rng.standard_normal()
    n = int(0.006 * SR)
    t = np.arange(n) / SR
    exc = rng.standard_normal(n) * np.exp(-t / 0.0011)
    exc[:12] *= ramp_in(12)
    y = sosfilt(bw(2, [1000 * c, 3000 * c], "band"), np.concatenate([exc, np.zeros(96 + int(0.008 * SR))]))
    y /= np.abs(y).max() + 1e-12
    tb = np.arange(len(y)) / SR
    y += db(-12) * np.sin(2 * np.pi * 380 * c * tb) * np.exp(-tb / 0.0015)
    give = _band_noise(rng, len(y), 700 * c, 3500 * c) * np.exp(-np.maximum(tb - 0.001, 0) / 0.004)
    give[:48] *= ramp_in(48)
    y += db(-8) * give / (np.abs(give).max() + 1e-12)
    y = seal_clip(y, 0.25, 3.0)
    return y / np.abs(y).max()


def fx_clip(cue: dict) -> tuple[np.ndarray, float, str]:
    """The clip (stereo, PREROLL-aligned) and its peak level (dBFS, final) for one sound cue."""
    kind, t = cue["kind"], cue["t"]
    rng = rng_for(kind, round(t * 1000), cue.get("variant"))
    if kind == "PAPER":
        var = cue.get("variant") or ("fold" if "fold" in cue.get("desc", "") and "un" not in cue.get("desc", "") else
                                     "slit" if "slit" in cue.get("desc", "") else "slide")
        y, lvl = fx_paper(var, rng), FX_LEVEL_DBFS["PAPER"]
    elif kind == "LAND":
        y, lvl, var = fx_land(rng), FX_LEVEL_DBFS["LAND"], "wood"
    elif kind == "SEAL":
        y, lvl, var = fx_seal(rng), FX_LEVEL_DBFS["SEAL"], "stamp"
    elif kind == "CLOCK":
        var = cue.get("variant") or "tick"
        y, lvl = fx_clock(var, rng), FX_LEVEL_DBFS["CLOCK"]
    elif kind == "TYPE":
        y, var = fx_type(rng), "tap"
        lvl = TYPE_BOOT_DBFS if (cue.get("level") == "boot" or t >= BOOT_T0) else FX_LEVEL_DBFS["TYPE"]
        lvl += 0.8 * rng.standard_normal()
    else:
        raise KeyError(kind)
    lvl += FX_TRIM_DB.get(kind, 0.0) + (RETURN_TICK_DB if kind == "CLOCK" and abs(t - G(40)) < 0.05 else 0.0)
    y = pre(y * db(lvl), align=True)
    return y, lvl, var


def room_tone(rng) -> np.ndarray:
    """A quiet room (v1's 'paper room'): noise band-passed 200 Hz to 2 kHz, softened above 900 Hz, decorrelated
    L/R, a slow seeded drift of +/- 1 dB. Unit RMS; gain by ROOM_GAIN."""
    x = rng.standard_normal((N, 2))
    x = sosfilt(np.vstack([bw(4, [200, 2000], "band"), biquad("highshelf", 900, -3.0)]), x, axis=0)
    drift = uniform_filter1d(rng.standard_normal(N // 24000 + 6), 5, mode="nearest")
    drift = np.interp(np.arange(N) / 24000, np.arange(len(drift)), drift)
    x *= db(1.0 * drift / (np.abs(drift).max() + 1e-12))[:, None]
    return x / np.sqrt(np.mean(x ** 2))


ROOM_GAIN = [(0.0, 0.0), (0.04, db(-52)), (G(7.75), db(-52)), (G(8.25), 0.0), (G(32), 0.0), (G(32.8), db(-58)),
             (G(39.6), db(-58)), (G(40.05), 0.0)]
# Per-kind trims on SCRIPT-v2 §8's levels. §8's dBFS were set against a licensed track; measured against this score
# they sat 21-38 LU under the music in their own windows (report accents_vs_music, first render), i.e. inaudible.
# QA polish (item 6): measured 11-31 LU under the music in their own windows (SEAL 13.6, PAPER 15, TYPE 24-31, the
# return's CLOCK 30.9); target 6-12 LU under: SEAL +5 (its paper +4 more), PAPER +3, CLOCK +3 (the return's tick
# +17 more: it lands on the return's downbeat, the loudest attack in the film), TYPE +17 (and a 'give', fx_type).
FX_TRIM_DB: dict[str, float] = {"PAPER": 12.0, "LAND": 9.0, "CLOCK": 10.0, "SEAL": 14.0, "TYPE": 29.0}
RETURN_TICK_DB = 17.0


# ------------------------------------------------------------------------------------------------ levels (reference)
# Reference levels before the master gain (the palette demo's balance as the start, then set by the measurements in
# the report: the piano leads; the bed sits 11-15 LU under the piano's peaks; mallet accents 8-10 LU under).
PAD_REF_DB = -35.5                                      # dBFS RMS of one pad tone at fc 800 Hz
VIBES_DB, CHIME_DB, CELESTA_DB, BOWED_DB = 0.0, -6.0, -21.0, 2.0
CELLO_DB, VIOLA_DB = 0.0, -2.0
# The Kawai layer, in dB under the Osiris note it doubles (sampler2 level_ref: measured per note on both; the recorded
# levels alone had put it anywhere from 0 to 22 dB under, so F#4 was Kawai-bright and A4 dark within one motif).
# QA polish (item 7): re-tuned to Osiris (tune_ref), the motif carries the open grand 4 dB under the una corda, which
# is what reads on a phone; the chords keep it as colour (power mean of the first polish render, made even).
KAWAI_CHORD_DB, KAWAI_MELODY_DB = -16.5, -4.0
KAWAI_BOOT_DB = -10.5                                   # the boot's rising motif notes
MELODY_GAIN_DB = 1.5                                    # the motif a little forward of its chords (reads on a phone)
KAWAI_INVITATION_DB = -13.5                             # the invitation's melody: a touch of the open grand, kept warm
# Dynamics: one gain curve (dB) on the music before the rooms, so the arc breathes: p into the scene, a crescendo into
# the lift, the relay a step down, the wait on the velocities, f on the return and the card, the invitation warm and
# a step down, the boot mf, the address p. (G, dB)
DYN = [(7.5, -3.5), (14, -3.5), (15.8, -1.0), (16, -0.5), (20.2, -0.5), (21, -2.0), (31.5, -2.0), (32, -1.0),
       (39.6, -1.0), (40, 0.0), (43.6, 0.0), (44.4, -2.0), (50.6, -2.0), (51.6, -0.5), (55.6, -0.5), (56, 0.0),
       (64.2, 0.0), (65, -1.5), (75.5, -1.5), (76, -2.0), (79, -2.0), (80, -1.0), (83.8, -1.0), (84, -2.0)]
# A slow leveler on the music in the master (3:1 above LEVELER_ABOVE_LU over the target, 300 ms detector looking
# 250 ms ahead, 0.5 s release): it brings the loudest moments (the return, the card) down a little so the true-peak
# limiter does not have to.
LEVELER_ABOVE_LU, LEVELER_RATIO = 2.5, 3.0
# The piano bus: a look-ahead peak shaver PIANO_SHAVE_DB under the bus's own loudest true peak (per half), so a
# hammer-heavy stack does not drive the master limiter (v1 shaved each chord's first strike for the same reason).
PIANO_SHAVE_DB = 3.0
PIANO_CLIP_SHAVE_DB = 4.0                               # every piano clip's strike (shave_attack)
HEART_DB = {  # strength: (LUB drum layer, drum dB, sub dB), DUB = (pp, drum dB, sub dB)
    "strong": (("mp", -6.0, -16.0), ("pp", -11.0, -20.0)),
    "mid": (("pp", -10.0, -19.0), ("pp", -15.0, -23.0)),
    "soft": (("pp", -13.0, -22.0), ("pp", -18.0, -26.0)),
    "distant": (("pp", -14.0, -22.0), ("pp", -19.0, -26.0)),
}
ADDRESS_DB = 0.0
BOOT_PAD_DB = -33.0
DUCK_DB, DUCK_S = -2.0, 0.30                            # SCRIPT-v2 §8: SEAL and BACK duck the music 2 dB for 300 ms
DUCK_AT = [G(16), G(40), G(52)]
DUCK_STEMS = ("bed", "strings", "bowed")

BUS_EQ = {
    # Osiris is dark (centroid ~300 Hz): presence so the motif reads on phones (PALETTE §3.1)
    # QA polish (item 7): the 200-400 Hz build-up trimmed, a gentle presence shelf from 1.2 kHz
    "piano": [bw(2, 40, "high"), biquad("lowshelf", 180, -4.5), biquad("peak", 300, -3.5, 0.8),
              biquad("highshelf", 1200, 2.5), biquad("peak", 2400, 4.0, 0.6), biquad("highshelf", 6500, 3.0)],
    "bed": [bw(2, 70, "high"), biquad("peak", 300, -2.0, 0.9)],
    # the bowed bars' 4th partial (D5 -> D7 2349 Hz) sits 6 dB under the fundamental: shelved off above 1.5 kHz
    "bowed": [bw(2, 150, "high"), bw(2, 9000, "low"), biquad("highshelf", 1400, -9.0)],
    "strings": [bw(2, 60, "high"), bw(2, 5500, "low"), biquad("peak", 300, -2.5, 1.0), biquad("peak", 2500, 3.0, 0.8)],
    "mallets": [bw(2, 140, "high")],                   # the celesta's noise is below 120 Hz
    "low": [bw(2, 600, "low")],
    "fx": [bw(2, 60, "high")],
    "room": [],
}
SENDS = {"piano": {"chamber": -17, "hall": -23}, "mallets": {"chamber": -16, "hall": -12}, "bed": {"hall": -15},
         "bowed": {"hall": -12}, "strings": {"chamber": -16, "hall": -11}, "low": {"chamber": -28},
         "fx": {"chamber": -22}}
SEND_LOWMID_DB = -3.0                                   # the rooms' 200-400 Hz build-up (QA polish, item 7)
STEM_ORDER = ["piano", "mallets", "bed", "bowed", "strings", "low", "fx", "room", "rev_chamber", "rev_hall"]
MUSIC_STEMS = ("piano", "mallets", "bed", "bowed", "strings", "low", "rev_chamber", "rev_hall")


def heart_clip(P, kind: str, strength: str) -> np.ndarray:
    """LUB / DUB: the felt bass drum (round robins), damped to a heartbeat's length, and the tuned sub (D1 / A1)."""
    (lay, ddb, sdb) = HEART_DB[strength][0 if kind == "LUB" else 1]
    d = P["bassdrum"].hit(lay, gain_db=ddb)
    t = np.arange(len(d)) / SR - PREROLL
    env = np.where(t < 0.10, 1.0, np.exp(-np.maximum(t - 0.10, 0) / 0.16))
    d = d * env[:, None]
    d = d[: int((PREROLL + 0.8) * SR)]
    if strength == "distant":
        d = sosfilt(bw(2, 700, "low"), d, axis=0)
    d = seal_clip(d, 0.5, 60.0)
    s = pre(synth_sub(HEART_NOTE[kind], level_db=sdb))
    n = max(len(d), len(s))
    y = np.zeros((n, 2))
    y[: len(d)] += d
    y[: len(s)] += s
    k = attack_index(y[: int((PREROLL + 0.2) * SR)]) - int(round(PREROLL * SR))   # drum + sub: re-align the pair
    if k > 0:
        y = np.vstack([y[k:], np.zeros((k, 2))])
    elif k < 0:
        y = np.vstack([np.zeros((-k, 2)), y[: len(y) + k]])
    return seal_clip(y, 0.5, 5.0)


def shave_attack(clip: np.ndarray, shave_db: float) -> np.ndarray:
    """Take shave_db off a piano clip's strike (look-ahead true-peak limiter on its first second, 150 ms release:
    the hammer transient comes down, the body is untouched), as v1 did, so the master limiter does not duck the
    mix on the onsets."""
    k = min(len(clip), int((PREROLL + 1.0) * SR))
    head = clip[:k]
    tp = float(true_peak(head).max())
    if tp <= 0:
        return clip
    g = limiter_gain(head, 20 * math.log10(tp) - shave_db)
    g[-1] = 1.0 if g[-1] > 0.999 else g[-1]
    out = clip.copy()
    out[:k] *= g[:, None]
    return out


def compose(P, cues: list[dict]) -> tuple[Stems, dict]:
    st = Stems()
    info: dict = {}
    fc = fc_curve()
    # ---- piano (Osiris) + the Kawai opening the una corda at the lift, the return, the answer and the card
    pn = piano_notes()
    for r in pn:
        dur = r["off"] - r["on"] + 0.04
        rel, gdb = r.get("release"), r.get("gain_db", 0.0)
        meta = {"midi": r["midi"], "note": nname(r["midi"]), "vel": r["vel"], "off": round(r["off"], 4), "role": r["role"],
                **({"release": rel, "step_to": r["step_to"]} if rel is not None else {})}
        c = P["piano"].note(r["midi"], r["vel"], dur=dur, release=rel, gain_db=gdb)
        st.add("piano", shave_attack(c, PIANO_CLIP_SHAVE_DB), r["on"], inst="piano", measure=c, **meta,
               **({"gain_db": gdb} if gdb else {}))
        if r["kawai"]:
            c = P["grand"].note(r["midi"], r["vel"], dur=dur, gain_db=r["kawai_db"] + gdb, release=rel)
            st.add("piano", shave_attack(c, PIANO_CLIP_SHAVE_DB), r["on"], inst="grand", measure=c, **meta,
                   gain_db=r["kawai_db"] + gdb)
    info["piano_notes"] = len(pn)
    # ---- mallets
    for g, n, v, role in VIBES:
        st.add("mallets", P["vibes"].note(m(n), v, gain_db=VIBES_DB), G(g), inst="vibes", midi=m(n), note=n, vel=v, role=role)
    for g, n in MELODY_DOUBLE:
        st.add("mallets", P["vibes"].note(m(n), MELODY_DOUBLE_VEL, gain_db=VIBES_DB + MELODY_DOUBLE_DB), G(g),
               inst="vibes", midi=m(n), note=n, vel=MELODY_DOUBLE_VEL, role="the motif, an octave up")
    for g, n, v in PULSE:
        st.add("mallets", P["vibes"].note(m(n), v, dur=PULSE_DUR, gain_db=VIBES_DB - 1.0), G(g), inst="vibes",
               midi=m(n), note=n, vel=v, role="theo's agent works")
    for g, n, v, role in CHIMES:
        st.add("mallets", P["chimes"].note(m(n), v, gain_db=CHIME_DB + CHIME_TRIM_DB.get(g, 0.0)), G(g), inst="chimes",
               midi=m(n), note=n, vel=v, role=role)
    for g, n, v, role in CELESTA:
        st.add("mallets", P["celesta"].note(m(n), v, gain_db=CELESTA_DB), G(g), inst="celesta", midi=m(n), note=n, vel=v,
               role=role)
    # ---- the bed
    info["pad_tones"] = pad_part(st, fc, PAD_REF_DB, pn)
    for g0, g1, ns, v, a in BOWED:
        for n in ns:
            st.add("bowed", P["bowed"].note(m(n), v, dur=G(g1) - G(g0), attack=a, gain_db=BOWED_DB), G(g0),
                   inst="bowed", struck=False, midi=m(n), note=n, vel=v, off=round(G(g1), 4), attack_s=a)
    for s in string_notes(pn):
        st.add("strings", P[s["inst"]].note(s["midi"], s["vel"], dur=s["off"] - s["on"], attack=s["attack"],
                                            gain_db=s["gain_db"], release=s["release"]), s["on"],
               inst=s["inst"], struck=False, midi=s["midi"], note=s["note"], vel=s["vel"], off=round(s["off"], 4),
               attack_s=s["attack"], release=s["release"], entry_delay=s["entry_delay"])
    # ---- the heartbeat
    for g, kind, strength in HEART:
        st.add("low", heart_clip(P, kind, strength), G(g), inst="heartbeat", kind=kind, strength=strength,
               note=nname(HEART_NOTE[kind]))
    # ---- V: the boot (on its own plate and letter times) and the address
    b0 = BOOT_T0
    boot_off = ADDRESS[0] + 0.05                         # the pedal changes under the address
    for (d, ns, vs) in BOOT_ARP:
        for n, v in zip(ns, vs):
            dur = boot_off - (b0 + d)
            c = P["piano"].note(m(n), v, dur=dur)
            st.add("piano", shave_attack(c, PIANO_CLIP_SHAVE_DB), b0 + d, inst="piano", measure=c,
                   midi=m(n), note=n, vel=v, role="boot: the motif's notes rising on the plates")
            if m(n) >= 45:
                c = P["grand"].note(m(n), v, dur=dur, gain_db=KAWAI_BOOT_DB)
                st.add("piano", shave_attack(c, PIANO_CLIP_SHAVE_DB), b0 + d, inst="grand", measure=c, midi=m(n),
                       note=n, vel=v, role="boot (Kawai)")
    d, n, v = BOOT_VIBES
    st.add("mallets", P["vibes"].note(m(n), v, gain_db=VIBES_DB), b0 + d, inst="vibes", midi=m(n), note=n, vel=v,
           role="boot: plate 19")
    d, n, v = BOOT_CELESTA
    st.add("mallets", P["celesta"].note(m(n), v, gain_db=CELESTA_DB), b0 + d, inst="celesta", midi=m(n), note=n, vel=v,
           role="boot: the last plate")
    d, n, v = BOOT_CHIME
    st.add("mallets", P["chimes"].note(m(n), v, gain_db=CHIME_DB - 3.0), b0 + d, inst="chimes", midi=m(n), note=n,
           vel=v, role="boot: the signal mark")
    bl = BOOT_BLOOM
    fcb = automation([(b0 + a, f) for a, f in bl["fc"]], N, log=True)
    for li, n in enumerate(bl["notes"]):
        i0, y = pad_tone(m(n), b0 + bl["on"] + 0.05 * li, bl["off"], bl["attack"], bl["release"], "rc", "rc", fcb,
                         ("bloom", li))
        buf = st.post.setdefault("bed", np.zeros((N, 2)))
        buf[i0: i0 + len(y)] += y * db(BOOT_PAD_DB)
    info["boot_bloom"] = bl
    for inst, n, v in BOOT_STRINGS:
        gdb = CELLO_DB if inst == "cellos" else VIOLA_DB
        st.add("strings", P[inst].note(m(n), v, dur=bl["off"] + 0.3 - (b0 + 0.6), attack=1.2, gain_db=gdb - 1.0,
                                       release=1.5), b0 + 0.6, inst=inst, struck=False, midi=m(n), note=n, vel=v,
               role="boot bloom")
    ns, v = BOOT_BOWED
    for n in ns:
        st.add("bowed", P["bowed"].note(m(n), v, dur=bl["off"] + 0.2 - (b0 + 0.7), attack=1.2, gain_db=BOWED_DB - 1.0),
               b0 + 0.7, inst="bowed", struck=False, midi=m(n), note=n, vel=v, role="boot bloom")
    ta, n, v = ADDRESS
    c = P["piano"].note(m(n), v, gain_db=ADDRESS_DB)
    st.add("piano", shave_attack(c, PIANO_CLIP_SHAVE_DB), ta, inst="piano", measure=c, midi=m(n), note=n, vel=v,
           role="the address: the last note")
    # ---- sound accents (code) from the cue file
    fx = []
    for c in cues:
        if c["kind"] not in FX_KINDS:
            continue
        y, lvl, var = fx_clip(c)
        st.add("fx", y, c["t"], inst="fx", kind=c["kind"], variant=var, level_dbfs=round(lvl, 2), desc=c.get("desc"),
               source=c.get("source", "cue file"))
        st.fx_clips.append((c["t"], y, c["kind"], len(st.events) - 1))
        fx.append(c)
    info["fx_cues"] = len(fx)
    # ---- room tone (cold open, and the wait)
    rt = room_tone(rng_for("ROOM")) * automation(ROOM_GAIN, N)[:, None]
    st.pre["room"] = rt
    st.events.append({"t": 0.0, "stem": "room", "inst": "room", "automation_db": [(round(t, 3), todb(v)) for t, v in ROOM_GAIN]})
    return st, info


# ------------------------------------------------------------------------------------------------ space + master
def irs() -> dict:
    out = {}
    for k in ("chamber", "hall"):
        ir = make_ir(**ROOMS[k])
        out[k] = ir / np.sqrt(np.mean(np.sum(ir ** 2, axis=0)))       # unit energy per channel
    return out


def eq(x: np.ndarray, chain) -> np.ndarray:
    for sos in chain:
        x = sosfilt(sos, x, axis=0)
    return x


FX_WINDOW_S = {"PAPER": 0.13, "LAND": 0.02, "SEAL": 0.14, "CLOCK": 0.025, "TYPE": 0.006}   # the accent's own length
# The accent rider (QA polish, item 6): the accents keep their SCRIPT-v2 §8 levels (+ FX_TRIM_DB) unless the music
# around them would bury them or they would stick out: each one is measured against the music in its own window (as
# the report measures it, K-weighted, the window max(length, 50 ms)) and moved by at most RIDER_MAX_DB towards
# RIDER_RANGE LU under the music. The music is estimated before the rooms (dry stems x the static gain that would
# bring them to the target, x the leveler); RIDER_ROOM_DB is what the rooms then add in an accent's window. A raise
# never lifts the window's peak more than RIDER_PEAK_RISE_DB over the music's own (or over RIDER_PEAK_DBFS, when that
# is higher), with the accent's polarity chosen to stack least, so the accents read without the limiter paying.
RIDER_RANGE, RIDER_MAX_DB, RIDER_ROOM_DB = (7.0, 10.5), 4.0, 1.0
RIDER_PEAK_DBFS, RIDER_PEAK_RISE_DB = -1.5, 0.3


def accent_rider(st: Stems, drys: dict, target: float) -> dict:
    music = np.zeros((N, 2))
    for dry, mask in drys.values():
        for k, y in dry.items():
            if k in MUSIC_STEMS:
                music += y * mask[:, None]
    music *= db(target - lufs(music, SR))
    music *= leveler_gain(music, target + LEVELER_ABOVE_LU, LEVELER_RATIO)[:, None]
    out = {}
    for t, clip, kind, ei in st.fx_clips:
        lw = max(FX_WINDOW_S[kind], 0.05)
        lm = window_level(music, t, t + lw) + RIDER_ROOM_DB
        buf = np.zeros((int((PREROLL + lw + 0.2) * SR), 2))
        place(buf, clip, PREROLL + 0.1)
        lf = window_level(buf, PREROLL + 0.1, PREROLL + 0.1 + lw)
        under = lm - lf
        want = 0.0
        if lm > -50.0:
            if under > RIDER_RANGE[1]:
                want = min(RIDER_MAX_DB, under - RIDER_RANGE[1])
            elif under < RIDER_RANGE[0]:
                want = -min(RIDER_MAX_DB, RIDER_RANGE[0] - under)
        i0 = int(round(t * SR)) - int(round(PREROLL * SR))
        seg = music[max(0, i0): i0 + len(clip)]
        cl = clip[max(0, -i0): max(0, -i0) + len(seg)]
        allowed = max(db(RIDER_PEAK_DBFS), float(np.abs(seg).max()) * db(RIDER_PEAK_RISE_DB))
        g, sgn = want, 1.0
        while True:
            pk = {s_: float(np.abs(seg + s_ * cl * db(g)).max()) for s_ in (1.0, -1.0)}
            sgn = min(pk, key=pk.get)
            if pk[sgn] <= allowed or g <= min(0.0, want):
                break
            g = max(min(0.0, want), g - 0.5)
        out[ei] = (round(g, 2), sgn)
        st.events[ei].update({"rider_db": round(g, 2), "rider_polarity": int(sgn), "rider_under_estimate_LU": round(under, 1)})
    return out


def mixdown(st: Stems, target: float = -14.0) -> tuple[dict, dict]:
    """Bus EQ, ducking, the accent rider, reverb sends (per half), the 25 Hz high-pass and the exact-zero masks."""
    rooms = irs()
    hp25 = bw(2, 25, "high")
    send_hp = np.vstack([bw(2, 160, "high"), biquad("peak", 300, SEND_LOWMID_DB, 0.8)])
    mask_pre = automation([(0.0, 1.0), (PRE_BOOT_FADE[0], 1.0), (PRE_BOOT_FADE[1], 0.0)], N)
    mask_pre[int(round(PRE_BOOT_FADE[1] * SR)):] = 0.0
    mask_post = automation([(0.0, 0.0), (END_FADE[0], 1.0), (END_FADE[1], 0.0)], N)
    mask_post[: int(round(BOOT_T0 * SR))] = 0.0
    mask_post[int(round(BOOT_T0 * SR)): int(round(END_FADE[0] * SR))] = 1.0
    mask_post[int(round(END_FADE[1] * SR)):] = 0.0
    duck = np.ones(N)
    for t in DUCK_AT:
        duck *= automation([(0.0, 1.0), (t - 0.01, 1.0), (t, db(DUCK_DB)), (t + DUCK_S, db(DUCK_DB)),
                            (t + DUCK_S + 0.08, 1.0)], N)
    padlev = pad_level_curve()
    dyn = db(automation([(G(g), v) for g, v in DYN], N))
    out = {k: np.zeros((N, 2)) for k in STEM_ORDER}
    shave = {}
    drys = {}
    for hname, half, mask in (("pre", st.pre, mask_pre), ("post", st.post, mask_post)):
        dry = {}
        for k, x in half.items():
            y = eq(x, BUS_EQ.get(k, []))
            if k == "bed" and half is st.pre:
                y = y * padlev[:, None]
            if k in DUCK_STEMS:
                y = y * duck[:, None]
            if k in MUSIC_STEMS:
                y = y * dyn[:, None]
            if k == "piano":
                tp = float(true_peak(y).max())
                g = limiter_gain(y, 20 * math.log10(tp) - PIANO_SHAVE_DB)
                y = y * g[:, None]
                shave[hname] = {"threshold_dbfs_ref": round(20 * math.log10(tp) - PIANO_SHAVE_DB, 2),
                                "time_in_reduction_s": round(float((g < 0.999).sum()) / SR, 3),
                                "events": limiter_events(g)}
            dry[k] = y
        drys[hname] = (dry, mask)
    rider = accent_rider(st, drys, target)
    for hname, (dry, mask) in drys.items():
        if "fx" in dry:                                  # rebuild the accents' bus with the rider's gains
            buf = np.zeros((N, 2))
            for t, clip, kind, ei in st.fx_clips:
                if (t < BOOT_T0 - 0.05) == (hname == "pre"):
                    g, sgn = rider.get(ei, (0.0, 1.0))
                    place(buf, clip * (sgn * db(g)), t)
            dry["fx"] = eq(buf, BUS_EQ.get("fx", []))
        for room, ir in rooms.items():
            acc = np.zeros(N)
            for k, d in SENDS.items():
                if room in d and k in dry:
                    acc += dry[k].mean(1) * db(d[room])
            if np.abs(acc).max() > 0:
                mono = sosfilt(send_hp, acc)
                w = np.stack([fftconvolve(mono, ir[:, 0])[:N], fftconvolve(mono, ir[:, 1])[:N]], 1)
                dry["rev_" + room] = w
        for k, y in dry.items():
            out[k] += sosfilt(hp25, y, axis=0) * mask[:, None]
    return out, {"piano_shave": shave, "accent_rider_db": {f"{st.events[ei]['kind']}@{t:.3f}": rider[ei][0] * rider[ei][1]
                                                          for t, clip, kind, ei in st.fx_clips}, "dynamics_db": DYN, "pad_level_db": PAD_LEVEL, "duck": {"at": [round(t, 3) for t in DUCK_AT], "db": DUCK_DB,
                                                      "s": DUCK_S, "stems": DUCK_STEMS},
                 "sends_db": SENDS, "bus_eq": {k: len(v) for k, v in BUS_EQ.items()}}


def limiter_gain(x: np.ndarray, ceiling_db: float, lookahead_ms=5.0, release_ms=150.0) -> np.ndarray:
    """v1's limiter (tools/audio-poc/master.py:limit structure): 4x true-peak detection, look-ahead minimum, a
    Hann-window attack that never exceeds the ceiling, exponential release; returns the gain curve."""
    c = db(ceiling_db)
    need = np.minimum(1.0, c / np.maximum(true_peak(x), 1e-12))
    if need.min() >= 1.0:
        return np.ones(len(x))
    L = max(2, int(lookahead_ms * SR / 1000))
    w = np.hanning(L + 2)[1:-1]
    g = convolve1d(minimum_filter1d(need, size=2 * L + 1, mode="nearest"), w / w.sum(), mode="nearest")
    assert np.all(g <= need + 1e-12)
    rel = math.exp(-1.0 / (release_ms * SR / 1000))
    out = np.ones_like(g)
    active = np.flatnonzero(g < 1.0)
    cur, i, n = 1.0, int(active[0]), len(g)
    while i < n:
        v = g[i]
        cur = v if v < cur else v + (cur - v) * rel
        out[i] = cur
        if cur > 1 - 1e-5:
            nxt = active[np.searchsorted(active, i + 1):]
            if not len(nxt):
                break
            cur, i = 1.0, int(nxt[0])
            continue
        i += 1
    return out


def limiter_events(g: np.ndarray) -> list[dict]:
    on = g < db(-0.1)
    edges = np.flatnonzero(np.diff(np.concatenate([[0], on.astype(np.int8), [0]])))
    return [{"t": round(a / SR, 3), "dur_ms": round((b - a) / SR * 1000), "max_gr_db": round(float(-20 * np.log10(g[a:b].min())), 2)}
            for a, b in zip(edges[::2], edges[1::2])]


def ffmpeg_measure(path: Path) -> dict:
    r = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-filter_complex",
                        "ebur128=peak=true:framelog=quiet", "-f", "null", "-"], capture_output=True, text=True)
    s = r.stderr[r.stderr.rfind("Summary:"):]
    grab = lambda pat: float(re.search(pat, s, re.S).group(1))  # noqa: E731
    out = {"I_lufs": grab(r"I:\s+(-?[\d.]+) LUFS"), "LRA_lu": grab(r"LRA:\s+(-?[\d.]+) LU"),
           "true_peak_dbtp": grab(r"True peak:.*?Peak:\s+(-?[\d.inf]+) dBFS")}
    r = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-af",
                        "loudnorm=I=-14:TP=-1.5:LRA=11:print_format=json", "-f", "null", "-"], capture_output=True, text=True)
    j = json.loads(r.stderr[r.stderr.rfind("{"): r.stderr.rfind("}") + 1])
    out["loudnorm_pass1"] = {k: j[k] for k in ("input_i", "input_tp", "input_lra", "input_thresh", "target_offset")}
    return out


def leveler_gain(x: np.ndarray, thr: float, ratio: float) -> np.ndarray:
    """Gain curve of a slow look-ahead leveler: K-weighted loudness over [t - 50 ms, t + 250 ms] every 10 ms; above
    thr (LUFS) the excess is reduced by (1 - 1/ratio); the reduction is held over +/- 30 ms, smoothed (80 ms Hann)
    and released exponentially (0.5 s)."""
    y = kweight(x)
    p = np.sum(y ** 2, axis=1)
    c = np.concatenate([[0.0], np.cumsum(p)])
    hop, a, b = int(0.01 * SR), int(0.05 * SR), int(0.25 * SR)
    centers = np.arange(0, len(p), hop)
    lo, hi = np.clip(centers - a, 0, len(p)), np.clip(centers + b, 0, len(p))
    L = -0.691 + 10 * np.log10((c[hi] - c[lo]) / np.maximum(hi - lo, 1) + 1e-20)
    gr = np.maximum(0.0, L - thr) * (1 - 1 / ratio)
    gr = maximum_filter1d(gr, 7, mode="nearest")
    k = math.exp(-0.01 / 0.5)
    for i in range(1, len(gr)):
        gr[i] = max(gr[i], gr[i - 1] * k)
    w = np.hanning(10)
    gr = np.convolve(gr, w / w.sum(), mode="same")
    return db(-np.interp(np.arange(len(p)), centers, gr))


def master(stems: dict, target: float, ceiling: float) -> tuple[dict, dict]:
    """Music stems x one static gain (the sound accents and the room tone keep their absolute SCRIPT-v2 §8 levels),
    then one true-peak limiter gain curve applied to every stem, iterated so the limited mix integrates to target."""
    music = sum(stems[k] for k in MUSIC_STEMS)
    other = sum(stems[k] for k in STEM_ORDER if k not in MUSIC_STEMS)
    Gm = db(target - lufs(music, SR))
    g = np.ones(N)
    lev = np.ones(N)
    for _ in range(10):
        lev = leveler_gain(music * Gm, target + LEVELER_ABOVE_LU, LEVELER_RATIO)
        mix = music * (Gm * lev)[:, None] + other
        g = limiter_gain(mix, ceiling)
        L = lufs(mix * g[:, None], SR)
        if abs(L - target) < 0.005:
            break
        Gm *= db(target - L)
    final = {k: stems[k] * ((Gm * lev if k in MUSIC_STEMS else 1.0) * g)[:, None] for k in STEM_ORDER}
    return final, {"music_gain_db": round(20 * math.log10(Gm), 3),
                   "leveler": {"threshold_lufs": target + LEVELER_ABOVE_LU, "ratio": LEVELER_RATIO,
                               "max_gr_db": round(float(-20 * np.log10(lev.min())), 2),
                               "time_above_1db_s": round(float((lev < db(-1)).sum()) / SR, 2)},
                   "limiter_ceiling_dbtp": ceiling,
                   "limiter_max_gr_db": round(float(-20 * np.log10(g.min())), 3),
                   "limiter_time_in_reduction_s": round(float((g < 0.999).sum()) / SR, 3),
                   "limiter_events": limiter_events(g)}


# ================================================================================================ checks (static)
LOW_LIMIT = {1: 52, 2: 51, 3: 48, 4: 46, 5: 45, 6: 47, 7: 34, 8: 41, 9: 41, 10: 41, 11: 41}  # lowest MIDI per interval


def sounding_at(t: float, pn: list[dict]) -> dict:
    """Pitches sounding at time t: piano notes held (on <= t < off), pad lines, strings."""
    piano = sorted({r["midi"] for r in pn if r["on"] <= t + 0.06 and t < r["off"] and not r.get("melody")
                    and r["role"] != "brief line"})
    pad = []
    for g, spec in PAD_CHORDS:
        if G(g) <= t + 0.06:
            pad = [m(w) for w in spec.split() if w != "-"]
    strings = sorted({m(n) for part in (CELLOS, VIOLAS) for g0, g1, n, v, a in part if G(g0) <= t + 0.06 < G(g1)})
    return {"piano": piano, "pad": pad, "strings": strings}


def harmony_checks(pn: list[dict]) -> dict:
    """Low-interval limits on everything sustained at each chord; melody, mallets and heartbeat against the
    harmony (pitch classes of piano chord + pad + strings); pad voice leading."""
    out = {"low_interval_violations": [], "chords": [], "melody": [], "colour_notes": [], "heartbeat": []}

    def pcs_at(t):
        s = sounding_at(t, pn)
        return {p % 12 for v in s.values() for p in v}, s

    for g, label, voicing, *_ in CHORDS:
        t = G(g) + 0.05
        pcs, s = pcs_at(t)
        allp = sorted(set(s["piano"]) | set(s["pad"]) | set(s["strings"]))
        bad = []
        for a, b in zip(allp, allp[1:]):
            iv = b - a
            if iv < 12 and a < LOW_LIMIT.get(iv, 0):
                bad.append(f"{nname(a)}-{nname(b)} ({iv} st, limit {nname(LOW_LIMIT[iv])})")
        out["chords"].append({"G": g, "chord": label, "sounding": [nname(p) for p in allp], "ok": not bad})
        if bad:
            out["low_interval_violations"].append({"G": g, "chord": label, "pairs": bad})
    mel = sorted(MELODY, key=lambda x: x[0])
    for i, (g, n, v, k, role) in enumerate(mel):
        pcs, _ = pcs_at(G(g) + 0.05)
        ok = m(n) % 12 in pcs
        nxt = next((x for x in mel[i + 1:] if x[0] > g), None)
        res = None if ok or not nxt else f"-> {nxt[1]} ({m(nxt[1]) - m(n):+d} st)"
        out["melody"].append({"G": g, "note": n, "chord_tone": ok, **({"resolution": res} if res else {})})
    for name, lst in (("vibes", [(g, n) for g, n, *_ in VIBES] + [(g, n) for g, n, _ in PULSE]),
                      ("chimes", [(g, n) for g, n, *_ in CHIMES]), ("celesta", [(g, n) for g, n, *_ in CELESTA]),
                      ("brief-line run", [(g, n) for g, n, _ in RUN])):
        for g, n in lst:
            pcs, _ = pcs_at(G(g) + 0.05)
            if m(n) % 12 not in pcs:
                out["colour_notes"].append({"inst": name, "G": g, "note": n})
    for g, kind, s in HEART:
        if g >= 80:
            continue
        pcs, _ = pcs_at(G(g) + 0.05)
        out["heartbeat"].append({"G": g, "kind": kind, "note": nname(HEART_NOTE[kind]), "chord_tone": HEART_NOTE[kind] % 12 in pcs})
    # pad voice leading: largest move of any line at a change
    lines = [[m(w) if w != "-" else None for w in spec.split()] for _, spec in PAD_CHORDS]
    moves = [abs(b - a) for p, q in zip(lines, lines[1:]) for a, b in zip(p, q) if a is not None and b is not None]
    out["pad_max_move_semitones"] = max(moves)
    out["pad_moves_histogram"] = {str(k): moves.count(k) for k in sorted(set(moves))}
    out["summary"] = {"low_interval_violations": len(out["low_interval_violations"]),
                      "melody_non_chord_tones": [f"G{x['G']} {x['note']} {x.get('resolution', '')}".strip()
                                                 for x in out["melody"] if not x["chord_tone"]],
                      "colour_notes_outside_chord": len(out["colour_notes"]),
                      "heartbeat_outside_chord": [x for x in out["heartbeat"] if not x["chord_tone"]]}
    return out


# ================================================================================================ QA (measured)
def ebur128(path: Path) -> dict:
    return ffmpeg_measure(path)


def kweight(x: np.ndarray) -> np.ndarray:
    """ITU-R BS.1770 K-weighting (pre-filter shelf + RLB high-pass) at 48 kHz."""
    b1, a1 = [1.53512485958697, -2.69169618940638, 1.19839281085285], [1.0, -1.69065929318241, 0.73248077421585]
    b2, a2 = [1.0, -2.0, 1.0], [1.0, -1.99004745483398, 0.99007225036621]
    sos = np.array([b1 + a1, b2 + a2])
    return sosfilt(sos, x, axis=0)


def momentary(x: np.ndarray, win: float = 0.4, hop: float = 0.05) -> tuple[np.ndarray, np.ndarray]:
    y = kweight(x)
    p = np.sum(y ** 2, axis=1)
    w, h = int(win * SR), int(hop * SR)
    c = np.concatenate([[0.0], np.cumsum(p)])
    starts = np.arange(0, len(p) - w, h)
    ms = (c[starts + w] - c[starts]) / w
    return (starts + w / 2) / SR, -0.691 + 10 * np.log10(ms + 1e-20)


def window_level(x: np.ndarray, t0: float, t1: float) -> float:
    a, b = max(0, int(t0 * SR)), min(len(x), int(t1 * SR))
    y = kweight(x[max(0, a - 4800): b])[a - max(0, a - 4800):]
    return float(-0.691 + 10 * np.log10(np.sum(np.mean(y ** 2, axis=0)) + 1e-20))


def clicks(x: np.ndarray, onsets: list[float], ref_peak: float | None = None) -> list[dict]:
    """Discontinuities: the 2nd difference of a 6 kHz high-passed signal at more than 30x its local level (the
    louder of the 10 ms block medians within +/- 30 ms, so the last milliseconds of a sound before silence are
    compared with the sound itself; PALETTE qa.py used one 50 ms block) and above -70 dB re the mix peak. A struck
    note's own attack is expected in [t - 35 ms, t + 30 ms] and is not reported."""
    hp = sosfilt(bw(4, 6000, "high"), x.mean(1))
    d2 = np.abs(np.diff(hp, 2))
    w = int(0.01 * SR)
    k = len(d2) // w
    loc = maximum_filter1d(np.median(d2[: k * w].reshape(k, w), axis=1), size=7, mode="nearest")   # +/- 30 ms
    loc = np.repeat(loc, w)
    loc = np.concatenate([loc, np.full(len(d2) - len(loc), loc[-1])])
    peak = ref_peak if ref_peak is not None else np.abs(x).max()      # floor: -70 dB re the MIX peak (v1's rule)
    hits = np.where((d2 > 30 * (loc + 1e-9)) & (d2 > peak * db(-70)))[0]
    out, last = [], -1
    on = np.array(sorted(onsets))
    for i in hits:
        if last >= 0 and i - last < int(0.01 * SR):
            continue
        last = i
        t = i / SR
        if len(on) and np.any((t > on - 0.035) & (t < on + 0.030)):
            continue
        out.append({"t": round(t, 4), "ratio_db": round(20 * np.log10(d2[i] / (loc[i] + 1e-12)), 1)})
    return out


def onset_in(x: np.ndarray, t: float, before_s=0.06, after_s=0.25) -> dict | None:
    """Attack point measured in a mixed signal: the first crossing of (peak - 15 dB) of the 1 ms envelope after
    t - 25 ms, where the level 60..5 ms before t is at least 15 dB under the event's own peak (else not isolated)."""
    mono = np.abs(x).max(axis=1)
    a, b = int((t - 0.025) * SR), int((t + after_s) * SR)
    env = maximum_filter1d(mono[max(0, a - int(before_s * SR)): b], size=SR // 1000)
    off = a - max(0, a - int(before_s * SR))
    pk = env[off:].max()
    pre_ = env[: off + int(0.02 * SR)].max() if off > 0 else 0.0
    if pk <= 0 or pre_ > pk * db(-15):
        return None
    j = off + int(np.argmax(env[off:] >= pk * db(-15)))
    return {"measured": round((max(0, a - int(before_s * SR)) + j) / SR, 5),
            "err_ms": round(1000 * ((max(0, a - int(before_s * SR)) + j) / SR - t), 2)}


TUNE_INSTS = ("piano", "grand", "vibes", "chimes", "celesta", "bowed", "cellos", "violas")


def tuning_check(P, events: list[dict]) -> dict:
    """Every (instrument, note) the score plays, rendered alone and measured (sampler2.partial_pitch: Blackman-Harris,
    2^18 FFT, +/-40 cent search per partial, parabolic peak): the fundamental, or the lowest strong partial where
    the fundamental is more than 30 dB under the strongest. Target: Osiris = its stretch curve (A4 on 440, the bass
    no flatter than -6 c); Kawai = the Osiris note it doubles, measured the same way; the heartbeat sub and every
    other instrument = 12-TET. Struck notes are measured 80 ms to 1.2 s after the attack (mallets 50-700 ms);
    sustained notes (vibrato) 1.2-3.0 s into a 4 s note."""
    from sampler2 import partial_pitch, pick_partial
    used: dict = {}
    for e in events:
        if e.get("inst") in TUNE_INSTS and e.get("midi") is not None:
            used.setdefault((e["inst"], int(e["midi"])), []).append(e)
    rows, kv = [], []
    for (inst, mi), evs in sorted(used.items(), key=lambda kv_: (TUNE_INSTS.index(kv_[0][0]), kv_[0][1])):
        vel = float(evs[0]["vel"])
        if inst in ("piano", "grand"):
            clip, win = P[inst].note(mi, vel, dur=2.5), (0.08, 1.2)
        elif inst in ("vibes", "chimes", "celesta"):
            clip, win = P[inst].note(mi, vel), (0.05, 0.7)
        else:
            clip, win = P[inst].note(mi, vel, dur=4.0, attack=0.5), (1.2, 3.0)
        parts = partial_pitch(clip, mi, SR, *win)
        prefer = None
        if inst == "grand":
            ref = P["piano"].pitch_of(mi, vel)
            prefer = ref[0] if ref else None
        p = pick_partial(parts, prefer)
        if p is None:
            continue
        h, cents, _ = p
        if inst == "piano":
            target = P["piano"].target_cents(P["piano"].zone_for(mi, vel))
        elif inst == "grand":
            target = ref[1] if ref else 0.0
        else:
            target = 0.0
        h1 = next((lv for hh, c, lv in parts if hh == 1), None)
        row = {"inst": inst, "note": nname(mi), "midi": mi, "events": len(evs), "vel": round(vel, 3), "partial": h,
               "fundamental_re_strongest_db": None if h1 is None else round(h1, 1), "cents": round(cents, 2),
               "target": round(target, 2), "err": round(cents - target, 2)}
        if inst == "grand":
            row["vs"] = "Osiris (same note and velocity)"
            kv.append(row)
        rows.append(row)
    # the heartbeat sub (D1, A1): synthesized, 12-TET by construction; its pitch fall is over by 150 ms
    for kind, mi in HEART_NOTE.items():
        y = synth_sub(mi, level_db=-12.0)
        parts = partial_pitch(y, mi, SR, 0.15, 0.5, hmax=3, start=0.0)
        p = pick_partial(parts)
        if p:
            rows.append({"inst": "sub", "note": nname(mi), "midi": mi, "events": sum(1 for e in events if e.get("kind") == kind),
                         "partial": p[0], "cents": round(p[1], 2), "target": 0.0, "err": round(p[1], 2)})
    per = {}
    for r in rows:
        per.setdefault(r["inst"], []).append(abs(r["err"]))
    return {"method": tuning_check.__doc__.split("\n")[0], "rows": rows,
            "max_abs_err_cents_by_inst": {k: round(max(v), 2) for k, v in per.items()},
            "kawai_minus_osiris_max_abs_cents": round(max(abs(r["err"]) for r in kv), 2) if kv else None,
            "notes_over_5_cents": [f"{r['inst']} {r['note']} {r['err']:+.1f}" for r in rows if abs(r["err"]) > 5]}


def qa(mix: np.ndarray, stems: dict, st: Stems, cues: list[dict], P) -> dict:
    q: dict = {}
    q["first_last_sample"] = [float(np.abs(mix[0]).max()), float(np.abs(mix[-1]).max())]
    q["dc_dbfs"] = todb(float(np.abs(mix.mean(0)).max()))
    # exact digital silence where the picture has silence
    sil = {}
    for name, (a, b) in {"before the boot (G79-G80)": SILENCE_PRE_BOOT, "the end (G89.4-end)": (MUSIC_END, DURATION)}.items():
        seg = mix[int(round(a * SR)): int(round(b * SR))]
        nz = np.flatnonzero(np.abs(seg).max(axis=1) > 0)
        sil[name] = {"from": round(a, 4), "to": round(b, 4), "samples": len(seg), "nonzero_samples": int(len(nz)),
                     "exact_zero": bool(len(nz) == 0)}
    q["silences"] = sil
    lead = np.flatnonzero(np.abs(mix).max(axis=1) > 0)
    q["first_nonzero_s"] = round(lead[0] / SR, 4) if len(lead) else None
    # clicks
    struck_t = sorted({e["t"] for e in st.events if "attack_measured" in e})
    q["clicks"] = {"mix": clicks(mix, struck_t)}
    mpk = float(np.abs(mix).max())
    q["clicks"].update({k: clicks(v, struck_t, mpk) for k, v in stems.items() if np.abs(v).max() > 0})
    # onsets per cue: every struck event within 20 ms of a cue, its attack measured on the clip, and (when isolated)
    # its attack measured again in its own stem and in the final mix
    by_t = sorted([e for e in st.events if "attack_measured" in e], key=lambda e: e["t"])
    rows = []
    for c in cues:
        hit = [e for e in by_t if abs(e["t"] - c["t"]) <= 0.02]
        row = {"t": c["t"], "G": c.get("G"), "kind": c["kind"], "desc": c.get("desc", "")}
        row["sounds"] = [{"inst": e["inst"], "what": e.get("note") or e.get("kind") or e.get("variant"),
                          "attack": e["attack_measured"], "err_ms": e["attack_err_ms"]} for e in hit]
        if hit:
            stem = hit[0]["stem"]
            o = onset_in(stems[stem], c["t"])
            if o:
                row["in_stem"] = {"stem": stem, **o}
            o = onset_in(mix, c["t"])
            if o:
                row["in_mix"] = o
        rows.append(row)
    q["onsets_per_cue"] = rows
    errs = [abs(e["attack_err_ms"]) for e in by_t]
    q["attack_error_ms_all_struck"] = {"n": len(errs), "median": round(float(np.median(errs)), 3), "max": round(float(np.max(errs)), 3)}
    sound_rows = [r for r in rows if r["kind"] in SOUND_KINDS]
    q["sound_cues_without_a_sound"] = [f"{r['kind']}@{r['t']}" for r in sound_rows if not r["sounds"]]
    mix_err = [abs(r["in_mix"]["err_ms"]) for r in rows if "in_mix" in r]
    q["attack_error_ms_in_mix_isolated"] = {"n": len(mix_err), "median": round(float(np.median(mix_err)), 2) if mix_err else None,
                                            "max": round(float(np.max(mix_err)), 2) if mix_err else None}
    pic = [r for r in rows if r["kind"] == "PIC"]
    q["picture_anchors_with_a_musical_onset"] = f"{sum(1 for r in pic if r['sounds'])} of {len(pic)}"
    # tuning of melodic struck notes (>= C4) in their stem, 80..380 ms after the attack
    pno = P["piano"]
    tun = []
    for e in st.events:
        if e.get("midi") is None or e["inst"] not in ("piano", "vibes", "chimes", "celesta") or e["midi"] < 60:
            continue
        if e.get("role") == "theo's agent works":
            continue
        f_t = midi_to_hz(e["midi"])
        expect = 0.0
        if e["inst"] == "piano" and pno._smooth is not None:
            expect = pno.target_cents(pno.zone_for(e["midi"], e["vel"]))
        a, b = int((e["t"] + 0.08) * SR), int((e["t"] + 0.38) * SR)
        f, lv = peak_freq(stems[e["stem"]][a:b].mean(1), SR, f_t * 2 ** (-0.6 / 12), f_t * 2 ** (0.6 / 12), pad=8)
        if not np.isfinite(f):
            continue
        cc = 1200 * np.log2(f / f_t)
        tun.append({"t": e["t"], "inst": e["inst"], "note": e.get("note"), "cents": round(float(cc), 2),
                    "expect": round(expect, 2), "err": round(float(cc - expect), 2)})
    q["tuning"] = tun
    te = [abs(x["err"]) for x in tun]
    q["tuning_abs_err_cents"] = {"n": len(te), "median": round(float(np.median(te)), 2), "p90": round(float(np.percentile(te, 90)), 2),
                                 "max": round(float(np.max(te)), 2)}
    # every (instrument, note) played, rendered alone (the bass, the Kawai against Osiris, the strings, the sub)
    q["tuning_all_notes"] = tuning_check(P, st.events)
    q["kawai_retune_cents"] = {f"{nname(int(k[0]))} layer {k[1]}": v["correction_cents"]
                               for k, v in sorted(P["grand"].ref_corrections.items())}
    # mono compatibility
    mono = np.repeat(mix.mean(1, keepdims=True), 2, 1)
    q["mono_minus_stereo_LU"] = round(lufs(mono, SR) - lufs(mix, SR), 3)
    loss = {}
    for k, (lo, hi) in {"<200": (20, 200), "200-2k": (200, 2000), "2k-8k": (2000, 8000), ">8k": (8000, 20000)}.items():
        xs = sosfilt(bw(4, [lo, hi], "band"), mix, axis=0)
        loss[k] = round(float(10 * np.log10(np.mean(xs.mean(1) ** 2) / (np.mean(xs ** 2) + 1e-30) + 1e-30)), 2)
    q["mono_loss_db_by_band"] = loss
    w = int(0.4 * SR)
    cors = []
    for s0 in range(0, len(mix) - w, w // 2):
        L, R = mix[s0: s0 + w, 0], mix[s0: s0 + w, 1]
        if np.sqrt(np.mean(L ** 2 + R ** 2) / 2) > db(-50):
            cors.append((s0 / SR, float(np.sum(L * R) / np.sqrt(np.sum(L ** 2) * np.sum(R ** 2)))))
    q["lr_correlation_400ms"] = {"min": round(min(c for _, c in cors), 3), "at": round(min(cors, key=lambda x: x[1])[0], 2),
                                 "median": round(float(np.median([c for _, c in cors])), 3)}
    S = (mix[:, 0] - mix[:, 1]) / 2
    q["side_to_mid_db"] = round(float(10 * np.log10(np.mean(S ** 2) / np.mean(mix.mean(1) ** 2))), 2)
    # spectrum
    mm = mix.mean(1)
    Pw = np.abs(np.fft.rfft(mm)) ** 2
    f = np.fft.rfftfreq(len(mm), 1 / SR)
    tot = Pw[f > 20].sum()
    q["spectrum_share_db"] = {k: round(float(10 * np.log10(Pw[(f >= lo) & (f < hi)].sum() / tot)), 2)
                              for k, (lo, hi) in {"sub <60": (20, 60), "low 60-250": (60, 250), "lowmid 250-1k": (250, 1000),
                                                  "mid 1-4k": (1000, 4000), "high 4-10k": (4000, 10000),
                                                  "air >10k": (10000, 24000)}.items()}
    q["spectral_centroid_hz"] = round(float(np.sum(f * Pw) / np.sum(Pw)), 1)
    q["energy_above_16k_db"] = round(float(10 * np.log10(Pw[f >= 16000].sum() / tot + 1e-30)), 1)
    # sections: loudness arc, brightness
    tm, Lm = momentary(mix)
    secs = []
    for name, a, b in SECTIONS:
        sel = (tm >= a) & (tm < b)
        seg = mix[int(a * SR): int(b * SR)]
        row = {"section": name, "from": round(a, 3), "to": round(b, 3), "peak_dbfs": todb(float(np.abs(seg).max()))}
        if sel.any() and np.abs(seg).max() > 0:
            act = Lm[sel][Lm[sel] > -70]
            row["momentary_max_lufs"] = round(float(Lm[sel].max()), 1)
            row["momentary_median_lufs"] = round(float(np.median(act)), 1) if len(act) else None
            sp = np.abs(np.fft.rfft(seg.mean(1))) ** 2
            fs = np.fft.rfftfreq(len(seg), 1 / SR)
            row["centroid_hz"] = round(float(np.sum(fs * sp) / np.sum(sp)), 0)
            row["share_1_4k_db"] = round(float(10 * np.log10(sp[(fs >= 1000) & (fs < 4000)].sum() / sp[fs > 20].sum())), 1)
        secs.append(row)
    q["sections"] = secs
    # balance per bar: piano max momentary vs the bed (pad + bowed + strings) median
    tp, Lp = momentary(stems["piano"])
    tb, Lb = momentary(stems["bed"] + stems["bowed"] + stems["strings"])
    _, Lmal = momentary(stems["mallets"])
    bars = []
    for bar in range(3, 23):
        a, b = (bar - 1) * BAR, bar * BAR
        sel = (tp >= a) & (tp < b)
        if not sel.any() or Lp[sel].max() < -60:
            continue
        bars.append({"bar": bar, "piano_max_M": round(float(Lp[sel].max()), 1), "bed_median_M": round(float(np.median(Lb[sel])), 1),
                     "bed_under_piano_LU": round(float(Lp[sel].max() - np.median(Lb[sel])), 1),
                     "mallets_max_M": round(float(Lmal[sel].max()), 1)})
    q["balance_by_bar"] = bars
    # accents (sound design) under the music: K-weighted level over each accent's own window vs the music
    music = sum(stems[k] for k in MUSIC_STEMS)
    acc = []
    for e in st.events:
        if e["inst"] != "fx":
            continue
        Lw = max(FX_WINDOW_S[e["kind"]], 0.05)
        la = window_level(stems["fx"], e["t"], e["t"] + Lw)
        lmu = window_level(music, e["t"], e["t"] + Lw)
        acc.append({"t": e["t"], "kind": e["kind"], "fx_LUFS_window": round(la, 1), "music_LUFS_window": round(lmu, 1),
                    "under_music_LU": round(lmu - la, 1) if lmu > -70 else None, "rider_db": e.get("rider_db"),
                    "rider_estimate_LU": e.get("rider_under_estimate_LU")})
    q["accents_vs_music"] = acc
    q["accents_vs_music_summary"] = {k: {"n": len(v), "min_under_LU": min(v) if v else None, "max_under_LU": max(v) if v else None,
                                         "median_under_LU": round(float(np.median(v)), 1) if v else None}
                                     for k in sorted(FX_KINDS) for v in [[a["under_music_LU"] for a in acc
                                                                           if a["kind"] == k and a["under_music_LU"] is not None]]}
    # a phone speaker (2nd-order 350 Hz high-pass, 9 kHz low-pass): loudness lost, and the melody's lead
    sos_ph = np.vstack([bw(2, 350, "high"), bw(2, 9000, "low")])
    ph = lambda v: sosfilt(sos_ph, v, axis=0)  # noqa: E731
    q["phone_loudness_drop_LU"] = round(lufs(ph(mix), SR) - lufs(mix, SR), 2)
    rest = sum(v for k, v in stems.items() if k != "piano")
    tpp, Lpp = momentary(ph(stems["piano"]))
    _, Lrp = momentary(ph(rest))
    lead = []
    for r in piano_notes():
        if r.get("melody"):
            sel = (tpp >= r["on"]) & (tpp < r["on"] + 0.4)
            lead.append({"G": r["G"], "note": nname(r["midi"]), "piano_minus_rest_LU": round(float(Lpp[sel].max() - Lrp[sel].max()), 1)})
    q["phone_melody_lead"] = lead
    q["phone_melody_lead_min_LU"] = min(x["piano_minus_rest_LU"] for x in lead)
    return q


# ================================================================================================ cues in, render
def load_cues(path: Path, fmt: str, fallback: bool) -> tuple[list[dict], dict]:
    doc = json.loads(path.read_text())
    raw = doc["cues"] if isinstance(doc, dict) else doc
    want = lambda c: c.get("fmt") in (None, fmt, "all")  # noqa: E731
    cues = [dict(c, source="cue file") for c in raw if want(c)]
    known = SOUND_KINDS | {"PIC"}
    unknown = sorted({c["kind"] for c in cues if c["kind"] not in known})
    info = {"file": rel(path), "md5": hashlib.md5(path.read_bytes()).hexdigest(), "n": len(cues),
            "ignored_kinds": unknown, "fallbacks": []}
    cues = [c for c in cues if c["kind"] in known]
    if fallback and Path(path).name != "cues-script.json":
        script = script_cues()["cues"]
        sec = lambda t: next((s for s, a, b in SECTIONS if a <= t < b), SECTIONS[-1][0])  # noqa: E731
        have = {(c["kind"], sec(c["t"])) for c in cues}
        for c in script:
            if c["kind"] in FX_KINDS and (c["kind"], sec(c["t"])) not in have:
                cues.append(dict(c, source="SCRIPT-v2 fallback"))
                info["fallbacks"].append(f"{c['kind']}@{c['t']}")
    cues.sort(key=lambda c: (c["t"], c["kind"]))
    return cues, info


def cross_check(cues: list[dict], st: Stems) -> list[dict]:
    """ARRIVE and BACK are notes of the score (on the grid): the cue file's times are compared with them."""
    out = []
    for c in cues:
        if c["kind"] not in SCORE_KINDS:
            continue
        inst = "chimes" if c["kind"] == "BACK" else "piano"
        near = [e for e in st.events if e["inst"] == inst and abs(e["t"] - c["t"]) < 0.25]
        e = min(near, key=lambda e: abs(e["t"] - c["t"])) if near else None
        out.append({"kind": c["kind"], "cue_t": c["t"], "score_t": e["t"] if e else None, "note": e.get("note") if e else None,
                    "delta_ms": round(1000 * (e["t"] - c["t"]), 2) if e else None,
                    "ok_within_1_frame": bool(e and abs(e["t"] - c["t"]) <= 1 / FPS)})
    return out


def rel(p: Path) -> str:
    p = Path(p).resolve()
    return str(p.relative_to(HERE)) if p.is_relative_to(HERE) else str(p)


def write_wav(path: Path, x: np.ndarray):
    path.parent.mkdir(parents=True, exist_ok=True)
    sf.write(path, x, SR, subtype="PCM_24")


def render(args):
    t_start = time.time()
    fmt = args.format
    cue_path = Path(args.cues) if args.cues else default_cues(fmt)
    if cue_path.name == "cues-script.json" and not cue_path.exists():
        cue_path.write_text(json.dumps(script_cues(), indent=1))
    cues, cue_info = load_cues(cue_path, fmt, not args.no_fallback)
    P = I.palette()
    st, info = compose(P, cues)
    t_comp = time.time() - t_start
    stems_ref, mix_info = mixdown(st, args.lufs)
    final, minfo = master(stems_ref, args.lufs, args.ceiling)
    mix = sum(final[k] for k in STEM_ORDER)
    out = Path(args.out)
    write_wav(out, mix)
    meas = ffmpeg_measure(out)
    trim = args.lufs - float(meas["loudnorm_pass1"]["input_i"])
    if abs(trim) >= 0.02 and float(meas["loudnorm_pass1"]["input_tp"]) + trim <= -1.5:
        final = {k: v * db(trim) for k, v in final.items()}
        mix = sum(final[k] for k in STEM_ORDER)
        write_wav(out, mix)
        meas = ffmpeg_measure(out)
    else:
        trim = 0.0
    mix_q = sf.read(out, dtype="float64", always_2d=True)[0]
    stem_dir = out.parent / "stems" / fmt
    stem_paths = {}
    for k in STEM_ORDER:
        p = stem_dir / f"{k}.wav"
        write_wav(p, final[k])
        stem_paths[k] = rel(p)
    resid = float(np.abs(sum(final.values()) - mix).max())
    rep = {
        "file": rel(out),
        "format": fmt, "duration_s": round(DURATION, 6), "samples": N, "sample_rate": SR, "bits": 24,
        "md5_file": hashlib.md5(out.read_bytes()).hexdigest(),
        "md5_audio": hashlib.md5(np.ascontiguousarray(mix_q).tobytes()).hexdigest(),
        "cues": cue_info, "score_cue_cross_check": cross_check(cues, st),
        "loudness": {"integrated_lufs_ffmpeg": meas["I_lufs"], "lra_lu_ffmpeg": meas["LRA_lu"],
                     "true_peak_dbtp_ffmpeg": meas["true_peak_dbtp"], "integrated_lufs_pyloudnorm": round(lufs(mix_q, SR), 2),
                     "true_peak_dbtp_4x_python": todb(float(true_peak(mix_q).max())),
                     "sample_peak_dbfs": todb(float(np.abs(mix_q).max())), "loudnorm_pass1": meas["loudnorm_pass1"]},
        "master": {**minfo, "ffmpeg_trim_db": round(trim, 3), "stems_sum_minus_mix_max": resid,
                   "note": "music stems x music_gain; sound accents and room tone at their SCRIPT-v2 §8 absolute levels; "
                           "one limiter gain curve on all stems; then the trim"},
        "mix": mix_info,
        "stems": {k: {"path": stem_paths[k], "peak_dbfs": todb(float(np.abs(v).max())),
                      "lufs": round(lufs(v, SR), 2) if np.abs(v).max() > 0 else None} for k, v in final.items()},
        "composition": composition_summary(),
        "harmony_checks": harmony_checks(piano_notes()),
        "qa": qa(mix_q, final, st, cues, P),
        "events": st.events, "info": info, "seed": SEED,
        "render_seconds": {"compose": round(t_comp, 1), "total": round(time.time() - t_start, 1)},
    }
    rp = Path(args.report) if args.report else out.parent / f"report-{fmt}.json"
    np_safe = lambda o: o.item() if hasattr(o, "item") else str(o)  # noqa: E731
    rp.write_text(json.dumps(rep, indent=1, default=np_safe))
    allp = out.parent / "report.json"
    allrep = json.loads(allp.read_text()) if allp.exists() else {}
    allrep[fmt] = {k: rep[k] for k in ("file", "md5_audio", "loudness", "master", "cues")}
    allrep[fmt]["report"] = rp.name
    if "16x9" in allrep and "4x5" in allrep:
        allrep["formats_identical_audio"] = allrep["16x9"]["md5_audio"] == allrep["4x5"]["md5_audio"]
    allp.write_text(json.dumps(allrep, indent=1, default=np_safe))
    write_tables(rp)
    if not args.no_plot:
        py = HERE / ".venv-plot" / "bin" / "python"
        if py.exists():
            subprocess.run([str(py), "-I", str(HERE / "plot2.py"), str(rp)], check=False)
    q = rep["qa"]
    print(json.dumps({"out": rep["file"], "md5_audio": rep["md5_audio"][:12], **{k: rep["loudness"][k] for k in (
        "integrated_lufs_ffmpeg", "lra_lu_ffmpeg", "true_peak_dbtp_ffmpeg")}, "limiter_gr_db": minfo["limiter_max_gr_db"],
        "silences_exact": all(v["exact_zero"] for v in q["silences"].values()), "clicks_mix": len(q["clicks"]["mix"]),
        "attack_err_ms_max": q["attack_error_ms_all_struck"]["max"], "tuning_max_c": q["tuning_abs_err_cents"]["max"],
        "tuning_all_max_c": max(q["tuning_all_notes"]["max_abs_err_cents_by_inst"].values()),
        "kawai_vs_osiris_max_c": q["tuning_all_notes"]["kawai_minus_osiris_max_abs_cents"],
        "mono_LU": q["mono_minus_stereo_LU"], "seconds": rep["render_seconds"]}, default=str))


def write_tables(rp: Path) -> Path:
    """onsets-<format>.csv next to the report: one row per cue (sound cues and picture anchors), the sounds whose
    attack lands within 20 ms of it, each attack as measured on its clip, and (isolated events) re-measured in the
    stem and in the final mix."""
    rep = json.loads(Path(rp).read_text())
    path = Path(rp).parent / f"onsets-{rep['format']}.csv"
    lines = ["t_s,G,frame_60fps,kind,desc,sounds (instrument:note@attack_s err_ms),stem_err_ms,mix_err_ms"]
    for r in rep["qa"]["onsets_per_cue"]:
        snd = "; ".join(f"{x['inst']}:{x['what']}@{x['attack']:.4f} {x['err_ms']:+.2f}" for x in r["sounds"])
        desc = (r.get("desc") or "").replace(",", ";").replace('"', "'")
        g = "" if r.get("G") is None else f"{r['G']:g}"
        lines.append(f"{r['t']:.4f},{g},{round(r['t'] * FPS)},{r['kind']},{desc},{snd},"
                     f"{r['in_stem']['err_ms'] if 'in_stem' in r else ''},{r['in_mix']['err_ms'] if 'in_mix' in r else ''}")
    path.write_text("\n".join(lines) + "\n")
    return path


def default_cues(fmt: str) -> Path:
    eng = FILM / "engine" / "out" / f"v2-cues-{fmt}.json"
    return eng if eng.exists() else HERE / "cues-script.json"


def composition_summary() -> dict:
    return {
        "key": "D major", "bpm": BPM, "meter": "4/4", "beats": N_BEATS,
        "motif": {"notes": ["F#4", "A4", "E5", "D5"], "degrees": "3 5 9 -> 8", "rhythm": "beats 1, 2, 3, then the next downbeat",
                  "chord": "Dadd9 (the boot)"},
        "chords": [{"G": g, "t": round(G(g), 3), "chord": lab, "voicing": v} for g, lab, v, *_ in CHORDS],
        "melody": [{"G": g, "t": round(G(g), 3), "note": n, "role": r} for g, n, v, k, r in MELODY],
        "boot": {"arpeggio": [{"t": round(BOOT_T0 + d, 3), "notes": ns} for d, ns, _ in BOOT_ARP],
                 "bloom": BOOT_BLOOM["notes"], "address": {"t": round(ADDRESS[0], 3), "note": ADDRESS[1]}},
        "silences": {"pre_boot": [round(x, 4) for x in SILENCE_PRE_BOOT], "end": [round(MUSIC_END, 4), round(DURATION, 4)]},
        "owner_decisions": {"signature": SIGNATURE, "card_footnote": CARD_FOOTNOTE},
    }


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--cues", help="cue JSON (default: engine/out/v2-cues-<format>.json if present, else cues-script.json)")
    ap.add_argument("--format", choices=["16x9", "4x5"], default="16x9")
    ap.add_argument("--out", help="mix WAV (stems in <out dir>/stems/<format>/, report-<format>.json next to it)")
    ap.add_argument("--report", help="report JSON path")
    ap.add_argument("--lufs", type=float, default=-14.0)
    ap.add_argument("--ceiling", type=float, default=-1.7, help="limiter true-peak ceiling (dBTP); delivery <= -1.5")
    ap.add_argument("--no-fallback", action="store_true", help="never fill missing sound cues from SCRIPT-v2")
    ap.add_argument("--no-plot", action="store_true")
    ap.add_argument("--emit-script-cues", metavar="PATH", help="write SCRIPT-v2 §3 + §8 as a cue file and exit")
    ap.add_argument("--tables-from", metavar="REPORT", help="write onsets-<format>.csv from an existing report and exit")
    a = ap.parse_args()
    if a.emit_script_cues:
        doc = script_cues()
        Path(a.emit_script_cues).write_text(json.dumps(doc, indent=1))
        print(f"wrote {a.emit_script_cues}: {len(doc['cues'])} cues")
        return
    if a.tables_from:
        print("wrote", write_tables(Path(a.tables_from)))
        return
    if not a.out:
        ap.error("--out is required (or --emit-script-cues / --tables-from)")
    render(a)


if __name__ == "__main__":
    main()
