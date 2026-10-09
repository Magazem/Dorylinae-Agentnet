"""Dorylinae closed-beta film, "When theo is back": the 63.0 s score, composed and rendered in code.

    PY=../tools/.venv/bin/python
    $PY -I score.py --cues ../engine/out/cues-16x9.json --format 16x9 --out out/mix-16x9.wav   # the delivery: the
    $PY -I score.py --cues ../engine/out/cues-4x5.json  --format 4x5  --out out/mix-4x5.wav    # film's own cue files
    (run them one after the other: both update out/report.json; cues-engine-*.json here are earlier copies of them)
    $PY -I score.py --emit-script-cues cues-script.json            # cue file from SCRIPT §5.3 (both formats, fmt-tagged)
    $PY -I score.py --cues cues-script.json --format 16x9 --out out/mix-16x9.wav    # script-only reference render

Sources: design/SCRIPT.md §5 (palette 5.1, sections 5.2, cue list 5.3, mix rules) and tools/TOOLING.md §2.
Key D major, 100 BPM, 4/4 (beat 0.600 s, bar 2.400 s), 48 kHz, stereo.

Two kinds of material:
  * SCORE (fixed, on the bar grid, identical in both formats): ROOM, PAD/DRONE, PIANO chords, the boot ripple,
    bloom chord and the address note. Music cues found in a cue file are only cross-checked. The glint air sweep
    (SCRIPT §5.3, 54.6 -> 55.35) is dropped by the director (QA round 3: a whoosh, which the brief bans); an AIR cue
    in a cue file is reported as dropped, not rendered.
  * EVENTS (placed from the cue file, so picture and sound agree): PASTE THUMP THOCK LUB DUB TICK DYAD PLINK LAND
    LABEL MOTIF GLASS LOW. If a cue file has no cue of a kind in a shot where SCRIPT §5.3 expects one, the script's
    cues for that kind and shot are used instead and the report says so (disable with --no-fallback).

Quality by construction (nobody can listen): every clip starts and ends at exactly 0 through raised-cosine ramps
(>= 2 ms for tonal sounds, 0.3 to 1 ms only for the intended transients), all synthesis is additive or filtered noise
below 16 kHz (no aliasing), saturation is applied only to sub content, stems are high-passed (no DC), and the
silences are multiplied by an exact zero mask after every filter. Every random number comes from a seeded generator
keyed by (seed, kind, time), so a cue added in one format never changes the noise of another sound.

Piano tuning: every felt-piano note at B3 and below is pitch-corrected to equal temperament (TunedPiano). Each VCSL
sample's offsets are measured once and cached in piano-tuning.json next to this file (re-measured only if a sample's
md5 or the measurement settings change); notes above B3 (the motif, the dyads) play the samples exactly as before.

Levels: the palette (SCRIPT §5.1) is the starting point of a reference mix (dBFS peak, or RMS where stated), rebalanced
by ear-and-meter after audio QA (see PAD_RMS_DB, MIX_TRIM_DB, BOOT_DB and the CHORDS notes): the felt piano leads, the
pad is a bed 5 to 7 LU under it, and the heartbeat and effects land where QA passed them. Each piano clip's first
strike is shaved by a clip-level look-ahead limiter (PIANO_SHAVE_DB) so the master limiter does not duck the mix on
onsets. Effects are held at least 6 dB under the dry music within +/- 1 beat (the §5.3 mix rule; a safeguard).
Every felt-piano sound (chords, MOTIF, DYAD, PLINK, LOW, the address) runs through one piano bus with a gentle high
shelf (+1.5 dB at 6 kHz, PIANO_AIR) before it joins the music stem and its reverb send.
The master then applies one static gain plus a 4x-oversampled true-peak limiter (tools/audio-poc/master.py:limit's
structure with a Hann-smoothed attack, re-implemented to return its gain curve so the stems get the same gain and sum
exactly to the mix), and a final static trim from ffmpeg loudnorm's first-pass measurement (what two-pass linear
loudnorm does, without its internal 192 kHz round trip, so digital silence stays exactly zero). The master gain is
computed without the LABEL taps, so one master serves both formats: music, heartbeat and room stems are
bit-identical between 16x9 and 4x5, and the fx stem differs only at S5's format-specific label ticks.

Final QA (audio-picture, phone translation): the boot ripple's D2, A2 and D3 are doubled one and two octaves up
(RIPPLE_DOUBLE_DB); the relay stop at 20.4 ducks the music stem 4.5 dB (RELAY_STOP_DUCK); the silent hard cuts at 13.2,
30.0 and 46.2 get a soft THOCK (CUT_ACCENTS) and the 30.0 TICK comes up 2 dB; LOW D3 is placed on the first frame of
the full stop of "Not what." (27.0667, CUE_RETIME), whatever the cue file says within 70 ms of 27.0.
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
from fractions import Fraction
from functools import lru_cache
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.ndimage import convolve1d, map_coordinates, minimum_filter1d, uniform_filter1d
from scipy.signal import butter, fftconvolve, firwin, hilbert, kaiserord, resample_poly, sosfilt, sosfiltfilt

HERE = Path(__file__).resolve().parent
FILM = HERE.parent
TOOLS = FILM / "tools"
sys.dont_write_bytecode = True                     # never write __pycache__ into tools/ (it is read-only for us)
sys.path.insert(0, str(TOOLS / "audio-poc"))      # run with python -I: import the toolchain's own modules explicitly
from sampler import Piano, midi  # noqa: E402
from master import lufs, true_peak  # noqa: E402

SR = 48000
BEAT = 0.6
BAR = 2.4
DURATION = 63.0
SEED = 9744                                          # r-97ae / 1444: any fixed number; everything derives from it
SAMPLES = TOOLS / "assets" / "vcsl" / "Chordophones" / "Zithers" / "Grand Piano, Steinway B" / "Sus"

# ------------------------------------------------------------------------------------------------ SCRIPT tables
SHOTS = [("S1", 0.0, 3.6), ("S2", 3.6, 9.6), ("S3", 9.6, 13.2), ("S4", 13.2, 19.2), ("S5", 19.2, 30.0),
         ("S6", 30.0, 33.6), ("S7", 33.6, 38.4), ("S8", 38.4, 40.8), ("S9", 40.8, 46.2), ("S10", 46.2, 52.8),
         ("S11", 52.8, 57.6), ("S12", 57.6, 63.0)]
CLOSE_UPS = {"S3", "S4", "S6", "S7"}                 # §5.1 LUB/DUB: "In close-ups both run at -6 dB"

SECTIONS = [("Before", 0.0, 3.6), ("Definition", 3.6, 9.6), ("The request", 9.6, 19.2), ("The wait", 19.2, 28.8),
            ("Back and answered", 28.8, 40.8), ("Our brief", 40.8, 52.8), ("Silence", 52.8, 53.4),
            ("Sonic logo", 53.4, 57.0), ("Logo hold", 57.0, 57.6), ("Address", 57.6, 62.4),
            ("Digital silence", 62.4, 63.0)]

# name: (SCRIPT §5.1 palette peak dBFS, stem, reverb send)
PALETTE = {
    "PASTE": (-24.0, "fx", 0.00), "THUMP": (-20.0, "music", 0.10), "THOCK": (-26.0, "fx", 0.12),
    "LUB": (-24.0, "heartbeat", 0.05), "DUB": (-26.0, "heartbeat", 0.05), "TICK": (-28.0, "fx", 0.18),
    "DYAD": (-22.0, "music", 0.30), "PLINK": (-30.0, "music", 0.30), "LAND": (-30.0, "fx", 0.04),
    "LABEL": (-36.0, "fx", 0.06), "MOTIF": (-18.0, "music", 0.32), "GLASS": (-34.0, "music", 0.40),
    "LOW": (-18.0, "music", 0.30),
}
# Mix trims (dB, added to the palette), set by ear-and-meter after the pad rebalance. The pad no longer carries the
# loudness (it is 10 dB under the palette), so the master gain that brings the film to -14 LUFS rises by about 4.5 dB.
# The piano family rides that gain up: it is the lead now. Everything that is not the piano is trimmed so it lands at
# about the delivered level QA measured and passed in the pad-led mix (heartbeat soft, effects small, 8 to 19 dB under
# the chord peaks). Trims that answer QA directly: MOTIF -3 (it stacked on the 12.0 and 35.4 downbeats and drove the
# limiter), LOW -10 (at 27.0 it read as a hit inside the held breath, as loud as the 28.8 resolve), THUMP -5 (at 3.6 it
# landed on the Dadd9 roll's F#4 strike; see also CUE_TRIM_DB), GLASS -4.
MIX_TRIM_DB = {
    "PASTE": -3.0, "THUMP": -5.0, "THOCK": -3.0, "LUB": -4.0, "DUB": -4.0, "TICK": -3.0, "LAND": -3.0, "LABEL": -3.0,
    "GLASS": -4.0, "MOTIF": -3.0, "DYAD": -2.0, "LOW": -10.0, "PLINK": 0.0,
}
# Per-cue trims on top of MIX_TRIM_DB, matched by kind and time (+/- 50 ms). Director, QA round 3: the first entrance
# (3.5 -> 3.9) is softened; THUMP at 3.6 (cut #1) comes down 3 dB. THUMP at 40.8 (cut #2) is unchanged.
# Final QA (audio-picture): the TICK at 30.0 (S6's trail entering, on the hard cut) comes up 2 dB, so the cut's own
# tick reads over the Dadd9 it lands in.
CUE_TRIM_DB = {("THUMP", 3.6): -3.0, ("TICK", 30.0): 2.0}
# Final QA (audio-picture): the hard cuts at 13.2, 30.0 and 46.2 had no sound of their own (the 21° cuts at 3.6 and
# 40.8 have THUMP). Each gets a soft accent: the plates-lock THOCK (80 ms felt low-mid) at its palette + trim level
# (-29 dBFS ref), centred. Score-fixed and format-independent; skipped if the cue file already has a THOCK there.
CUT_ACCENTS = [(13.2, "S3 -> S4"), (30.0, "S5 -> S6"), (46.2, "S9 -> S10")]
# Final QA (audio-picture): LOW D3 lands on the first frame where the full stop of "Not what." shows, frame 1624 at
# 60 fps (27.0667 s; the full stop starts at 27.046 and fades in over 60 ms), not at 27.000, where it came 4 frames
# early. The engine's cue (shots/s05.js) still says 27.0: a LOW cue within 70 ms of 27.0 is placed at this frame.
LOW_AT = 1624 / 60
CUE_RETIME = [("LOW", 27.0, 0.07, LOW_AT)]       # (kind, cue time, tolerance s, placed at)
# Boot (S11/S12) levels, reference dBFS peak: SCRIPT §5.3 ripple -28, bloom -20, address -22. Trimmed by the same
# reasoning (the logo sat 4 LU over the body at the palette levels).
BOOT_DB = {"RIPPLE": -31.0, "BLOOM": -23.5, "ADDRESS": -26.0}
# Final QA (phone speakers): through a 200 Hz high-pass the ripple's first notes (D2, A2, D3: 73 to 147 Hz) sat 11 to
# 19 dB under the D4 at 54.54 and vanished. Each keeps its low layer and is doubled one and two octaves up (D2: D3 + D4,
# A2: A3 + A4, D3: D4 + D5), the same felt mallet without a second contact noise, in dB re the low layer's peak; the
# lower the note, the more a phone loses of it, so the stronger its doubling (D2 -6, A2 -7, D3 -8). Measured through a
# 200 Hz 4th-order high-pass, in the mix (peak and 5 ms envelope over each note's first 150 ms): D2 6.9 / 7.9 dB
# under the D4, A2 and D3 3 to 6 (were 11 to 19); each note alone, 3 dB or less.
RIPPLE_DOUBLE_DB = {"D2": (-6.0, -6.0), "A2": (-7.0, -7.0), "D3": (-8.0, -8.0)}
EVENT_KINDS = set(PALETTE)
SCORE_KINDS = {"ROOM", "PAD", "PIANO", "DRONE", "RIPPLE", "BLOOM", "ADDRESS", "RELEASE", "SILENCE"}
# Score cues of SCRIPT §5.3 the director has cut. A cue file may still carry them: they are reported, never rendered.
DROPPED_KINDS = {"AIR": "glint air sweep 54.6 -> 55.35: a whoosh, which the brief bans (director, QA round 3)"}
PIANO_KINDS = {"MOTIF", "DYAD", "PLINK", "LOW"}      # events played on the felt piano: they go through the piano bus
LUB_OVERRIDES = {9.6: -6.0, 9.9: -6.0, 21.6: -4.0, 24.0: -6.0}   # explicit in §5.3
SILENT_TYPING = [(26.4, 27.11, "Not what."), (48.0, 49.44, "Your team applies.")]   # §5.1: read, not heard
MOTIF_ORDER = ["A4", "F#4", "D4"]


def _labels(t0, n, step=0.085, skip=()):
    out, t = [], t0
    for i in range(n + len(skip)):
        if i not in skip:
            out.append(round(t, 3))
        t += step if i not in skip else 0.6 * step
    return out


def script_cues() -> list[dict]:
    """SCRIPT §5.3 as a cue list in the film's own format ({t, kind, ...extras}). fmt marks format-only cues."""
    c = []
    add = lambda t, kind, **kw: c.append({"t": round(t, 4), "kind": kind, **kw})  # noqa: E731
    add(0.0, "ROOM", state="on", db_rms=-52); add(3.55, "ROOM", state="off")
    add(1.8, "PASTE")
    add(3.5, "PAD", state="in", attack=1.2); add(3.5, "PIANO", chord="Dadd9", notes=["D3", "A3", "E4", "F#4"])
    add(3.6, "THUMP"); add(3.9, "THOCK")
    add(4.8, "LUB"); add(4.8, "TICK", pan=-0.4); add(5.1, "DUB"); add(5.4, "TICK", pan=0.0); add(6.0, "DYAD", pan=0.4)
    add(7.2, "LUB"); add(7.2, "PIANO", chord="Gmaj7/D", notes=["D3", "G3", "B3", "F#4"]); add(7.5, "DUB")
    add(9.5, "PIANO", chord="Bm7", notes=["B2", "F#3", "A3", "D4"])
    add(9.6, "LUB", db=-6)
    for t, n in ((9.75, "B3"), (9.795, "D4"), (9.84, "F#4")):
        add(t, "PLINK", note=n)
    add(9.9, "DUB", db=-6)
    add(12.0, "LUB"); add(12.0, "MOTIF", note="A4"); add(12.0, "PIANO", chord="G", notes=["G2", "D3", "B3"])
    add(12.3, "DUB"); add(13.5, "LAND")
    add(14.4, "LUB"); add(14.4, "PIANO", chord="Asus4", notes=["A2", "E3", "D4"]); add(14.4, "PAD", state="darken")
    add(16.8, "LUB")
    add(19.2, "LUB"); add(19.2, "TICK", pan=-0.5)
    for t in _labels(19.2, 5):
        add(t, "LABEL", word="relay")
    add(19.8, "TICK", pan=-0.25)
    add(20.4, "PAD", state="drone"); add(20.4, "DRONE", note="A2"); add(20.4, "ROOM", state="on", db_rms=-56)
    add(21.6, "LUB", db=-4)
    for t in _labels(22.8, 4):
        add(t, "LABEL", word="when", fmt="4x5")
    for t in _labels(23.4, 4):
        add(t, "LABEL", word="when", fmt="16x9")
    add(24.0, "LUB", db=-6)
    for t in _labels(24.0, 3):
        add(t, "LABEL", word="who", fmt="4x5")
    for t in _labels(24.6, 3):
        add(t, "LABEL", word="who", fmt="16x9")
    for t in (25.8, 25.885, 25.97, 26.106, 26.191, 26.276):
        add(t, "LABEL", word="how big")
    add(LOW_AT, "LOW", note="D3")                    # §5.3 says 27.000; final QA: the full stop's first frame
    add(28.8, "PIANO", chord="Dadd9", notes=["D3", "A3", "E4", "F#4"]); add(28.8, "LUB"); add(28.8, "TICK", pan=0.0)
    add(28.8, "PAD", state="product", attack=0.6); add(28.8, "ROOM", state="off")
    add(29.1, "DUB"); add(29.4, "TICK", pan=0.5); add(30.0, "TICK", pan=0.7)
    add(30.6, "DYAD", pan=0.6); add(30.6, "LAND")
    add(31.2, "LUB"); add(31.5, "DUB")
    add(33.5, "PIANO", chord="Gmaj9", notes=["G2", "D3", "A3", "B3", "F#4"])
    add(33.6, "LUB"); add(33.9, "LAND"); add(33.9, "DUB")
    add(35.4, "MOTIF", note="F#4"); add(35.4, "GLASS", note="E6")
    add(36.0, "LUB"); add(36.0, "PIANO", chord="Dadd9", notes=["D3", "A3", "E4", "F#4"]); add(36.3, "DUB")
    add(38.4, "LUB"); add(38.4, "TICK", pan=0.5)
    add(38.4, "PIANO", chord="D upward roll", notes=["D3", "F#3", "A3", "D4", "E4"], roll=0.06)
    add(38.7, "DUB"); add(39.0, "TICK", pan=0.0); add(39.6, "DYAD", pan=-0.5)
    add(40.7, "PAD", state="open fifth"); add(40.7, "PIANO", chord="D", notes=["D2", "A2", "D3"])
    add(40.8, "THUMP"); add(40.8, "ROOM", state="on", db_rms=-52)
    add(43.2, "PIANO", chord="Bm7", db=-24); add(45.6, "PIANO", chord="Gmaj7", db=-24)
    add(48.0, "PIANO", chord="Asus4", notes=["A2", "E3", "D4"])
    add(49.8, "MOTIF", note="D4")
    add(52.65, "RELEASE", dur=0.15); add(52.8, "SILENCE", until=53.4)
    for t, n in zip((53.55, 53.75, 53.95, 54.15, 54.35, 54.54), ("D2", "A2", "D3", "F#3", "A3", "D4")):
        add(t, "RIPPLE", note=n)
    # §5.3's AIR (54.6 -> 55.35, glint sweep) is not emitted: cut by the director (DROPPED_KINDS)
    add(54.75, "BLOOM", notes=["D3", "F#3", "A3", "E4"], attack=1.2)
    for t in _labels(55.3, 9):
        add(t, "LABEL", word="DORYLINAE", db=-2)
    add(57.6, "ADDRESS", note="D2")
    add(62.4, "SILENCE", until=63.0)
    return sorted(c, key=lambda e: (e["t"], e["kind"]))


# Piano chords (score-fixed). (t, name, notes, peak dBFS, roll s, pedal-off time, release s, layer velocity)
# Voicings are SCRIPT §5.3's; the one not spelled there is voiced for common tones: Gmaj7 at 45.6 keeps F#3 and
# D4 from the Bm7 before it (only the bass and one inner voice move). Only the script's chords are played: the
# Dadd9 at 28.8 (theo is back) holds under the pedal for two bars, into the Gmaj9 pre-lap at 33.5.
# The G at 12.0 shares its downbeat with the motif's first note (A4): it is voiced 3 dB under the palette (-23) so
# the melody sits over its accompaniment and the two do not stack into the limiter (QA: 2.97 dB there). The Dadd9 at
# 36.0 (the cadence) lands while the motif's F#4 (35.4) still rings on the same pitch: it is 2 dB under (-22). The
# Dadd9 at 28.8 (it lands on LUB, TICK and the pad's return) and the bright upward roll at 38.4 (the mp layer, whose
# strike is peakier) sit 1 dB under the palette for the same reason; both still land 7 to 10 LU over what precedes.
# The chords are the lead now (the pad is a bed under them): no extra decay on the soft-layer recordings and a +6 dB
# aftersound lift (FeltPiano.voice), so a chord tone 0.8 to 1.8 s after its onset sits near the pad's lines instead
# of 16 to 33 dB under them. Peaks are unchanged (each chord is normalised to its peak).
CHORD_TAU = None
CHORD_LIFT_DB = 6.0
# Peak control at the source: chords, MOTIF, DYAD and LOW have the first strike of each clip shaved by this much
# (FeltPiano.chord). Without it the piano's peak-to-loudness ratio (13 to 17 dB per chord) made the master limiter
# take 2 to 4 dB off the whole mix at every onset once the pad no longer carried the loudness.
PIANO_SHAVE_DB = 4.0
# Voice emphasis (dB on top of the default weights: bass 0, top -1, inner -2.5): the tone that names the chord is
# brought forward where the pad would otherwise cover it (the thirds of the G chords, the sus4 of the Asus4 at 14.4
# under the darkening pad, the root of the Bm7s).
CHORD_VOICE_DB = {7.2: {"B3": 2.5}, 9.5: {"B2": 1.0}, 12.0: {"B3": 2.0}, 14.4: {"D4": 3.0}, 33.5: {"B3": 2.0},
                  40.7: {"A2": 1.0}, 43.2: {"B2": 1.0}, 45.6: {"B3": 3.0, "D4": 3.0}}
# The first entrance (director, QA round 3: "soften the first entrance at 3.5-3.9"; target: the master limiter no
# longer engages there). The Dadd9 at 3.5 comes in out of room tone: its roll is slower (40 ms per note, not 30), each
# hammer is softer (15 ms ramp, not 5) and the roll swells in over a 120 ms raised cosine (onset_ms, applied after the
# clip is set to its level, so the body is untouched and the bass strike is the softest). Measured (dev/r3/entrance.py):
# the limiter at 3.6-4.4 was driven by the chord's lifted body (the strikes are already shaved), which no onset shape
# reaches, so the chord also sits 2 dB under the palette (-22), with THUMP 3.6 at -3 dB (CUE_TRIM_DB). Together they take
# the entrance from 1.2 dB of limiting to none and the largest 200 ms rise of momentary loudness from 20 to 17 LU.
CHORD_ONSET = {3.5: {"soft_ms": 15.0, "onset_ms": 120.0}}
CHORDS = [
    (3.500, "Dadd9 (the first entrance, softened)", ["D3", "A3", "E4", "F#4"], -22.0, 0.040, 7.25, 0.40, 0.30),
    (7.200, "Gmaj7/D", ["D3", "G3", "B3", "F#4"], -20.0, 0.030, 9.55, 0.40, 0.30),
    (9.500, "Bm7", ["B2", "F#3", "A3", "D4"], -20.0, 0.030, 12.05, 0.40, 0.30),
    (12.000, "G (under the motif's A4)", ["G2", "D3", "B3"], -23.0, 0.030, 14.45, 0.40, 0.30),
    (14.400, "Asus4", ["A2", "E3", "D4"], -20.0, 0.030, 20.40, 0.15, 0.30),
    (28.800, "Dadd9", ["D3", "A3", "E4", "F#4"], -21.0, 0.030, 33.55, 0.40, 0.30),
    (33.500, "Gmaj9", ["G2", "D3", "A3", "B3", "F#4"], -20.0, 0.030, 36.05, 0.40, 0.30),
    (36.000, "Dadd9 (the motif's F#4 still rings)", ["D3", "A3", "E4", "F#4"], -22.0, 0.030, 38.50, 0.40, 0.30),
    (38.400, "D upward roll", ["D3", "F#3", "A3", "D4", "E4"], -21.0, 0.060, 40.75, 0.40, 0.60),
    (40.700, "D", ["D2", "A2", "D3"], -21.0, 0.030, 43.25, 0.40, 0.30),
    (43.200, "Bm7", ["B2", "F#3", "A3", "D4"], -24.0, 0.030, 45.65, 0.40, 0.30),
    (45.600, "Gmaj7", ["G2", "F#3", "B3", "D4"], -24.0, 0.030, 48.05, 0.40, 0.30),
    (48.000, "Asus4", ["A2", "E3", "D4"], -20.0, 0.030, 52.65, 0.15, 0.30),
]

# Pad voices: gain breakpoints (t, linear) with raised-cosine interpolation; cutoff breakpoints (t, Hz).
# Voicing, re-done after QA (the pad sat 7 to 9 LU over the piano and its E3 + F#3 major second beat at 20 Hz, 54 % AM):
#  * D2 is the pedal: it is consonant with every chord of the progression (root of D, fifth of G, third of Bm, fourth
#    of Asus4), so it holds through the product world.
#  * The product world's F# sounds an octave up (F#4, not F#3): no major second against anything in the bass register.
#  * E3 leaves the product world and the piano carries the add9 (E4 in every Dadd9). With the piano now leading, a
#    pad E3 would beat at 18 to 20 Hz against the chords' own D3 and F#3 (Bm7 at 9.5 measured 60 % in 150-200 Hz).
#    E3 only colours the Asus4 bar (unison with the piano's E3), entering as D2 and F#4 leave.
#  * A2 sounds only over D- and A-rooted harmony. Over a G2 or B2 bass (Bm7, G, Gmaj9, Gmaj7) it would be a major
#    second in the bass register, a 12 to 14 Hz flutter, and over Gmaj7/D its second partial beats against G3 and B3
#    (24 and 27 Hz), so it steps out 0.2 s before G and B chords and returns with the next D or A chord. It enters with the Asus4 at 14.4 and grows into the drone across the held chord
#    (14.4 -> 20.4), so the drone emerges from under the piano; at 20.4 the stop releases E3 (150 ms).
#  * The voices are steeper than a saw (k^-2; the D2 pedal k^-3, nearly a sine, so its second partial does not beat
#    against the chords' sympathetic E3), so their upper partials do not beat against the chord tones;
#    dev/padchords.py measures the pad + chords against the chords alone. A2 keeps a brighter k^-1.2: it is the
#    drone, the only music in the wait, and its partials above 250 Hz are what a phone speaker plays of it.
# The held breath (SCRIPT §5.2/5.3: no LUB at 26.4, "Not what." typed in silence, 27.106 -> 28.8 stillness): the
# drone dips 7 dB across 26.1 -> 26.7 (director, QA round 3: was 4) and stays down until the resolve at 28.8, so the
# stillness is the quietest music up to the resolve (dev/r3/stillness.py: momentary loudness 4.4 LU under anything
# before it, 10 LU under the resolve) and the 28.8 chord lands well above it. Its level is now carried by the LOW D3
# tail; the end of the soft Gmaj7 (46.7-48.0) is as quiet.
DRONE = ("drone", 0.0)                               # placeholder: the calibrated drone gain (times 10^(dB/20))
BREATH = ("drone", -7.0)
OPEN5 = 0.5                                          # the open fifth (40.7 -> 52.65) sits 6 dB under the full pad
PAD_VOICES = {
    "D2": (-6.0, [(3.5, 0), (4.7, 1), (14.4, 1), (16.8, 0), (28.8, 0), (29.4, 1), (40.7, 1), (41.3, OPEN5),
                  (52.65, OPEN5), (52.8, 0)]),
    "A2": (-3.0, [(3.5, 0), (4.7, 1), (7.0, 1), (7.4, 0), (14.4, 0), (20.4, DRONE), (26.1, DRONE), (26.7, BREATH),
                  (28.8, BREATH), (29.4, 1), (33.3, 1), (33.7, 0), (36.0, 0), (36.5, 1), (40.7, 1), (41.3, OPEN5),
                  (43.0, OPEN5), (43.4, 0), (48.0, 0), (48.5, OPEN5), (52.65, OPEN5), (52.8, 0)]),
    "E3": (-6.0, [(3.5, 0), (15.2, 0), (16.8, 1), (20.4, 1), (20.55, 0)]),
    "F#4": (-5.5, [(3.5, 0), (4.7, 1), (14.4, 1), (16.8, 0), (28.8, 0), (29.4, 1), (40.7, 1), (41.3, 0)]),
}
PAD_SLOPE = {"D2": 3.0, "A2": 1.2, "E3": 2.0, "F#4": 2.0}    # harmonic k^-slope per voice
PAD_CUTOFF = [(3.5, 2000), (14.4, 2000), (16.8, 900), (28.8, 900), (29.4, 2000), (40.7, 2000), (41.3, 1200)]
# Pad level, set by ear-and-meter rather than by the palette's -30 dBFS RMS: at -30 the pad sat 7 to 9 LU over the
# piano chords and buried the progression. The piano now leads; the pad is a bed under it (see pad_layer's report).
PAD_RMS_DB = -38.0                                   # reference mix, 5-7 s, full colour (the palette's -30, minus 8 dB)
DRONE_RMS_DB = -36.0                                 # A2 alone at drone gain, 21-26 s (palette -32, minus 4 dB)
ROOM_GAIN = [(0.0, 0), (0.04, 1), (3.45, 1), (3.55, 0), (20.4, 0), (20.7, 10 ** (-4 / 20)), (26.1, 10 ** (-4 / 20)),
             (26.7, 10 ** (-7 / 20)), (28.8, 10 ** (-7 / 20)), (29.4, 0), (40.8, 0), (40.88, 1), (52.65, 1), (52.8, 0)]
# The relay stop (final QA: the hard stop at 20.4 measured a 0.4 dB step in 100 ms windows; the Asus4's pedal-off and
# the pad's 150 ms release are too soft to read as a stop, and the drone is still growing there). No new instrument:
# the music stem (piano release, pad, drone and their reverb) ducks 4.5 dB with a 10 ms attack at 20.400, holds 240 ms
# and is back by 20.70, with the ROOM ramp (20.4 -> 20.7).
RELAY_STOP_DUCK = [(0.0, 1.0), (20.400, 1.0), (20.410, 10 ** (-4.5 / 20)), (20.650, 10 ** (-4.5 / 20)), (20.700, 1.0)]
# The master masks: everything before the silence releases 52.65 -> 52.80 and stays at zero; the boot and the
# address start at 53.40 and reach digital silence at 62.40.
SILENCE_END = 53.4
MASK_BEFORE = [(0.0, 1), (52.65, 1), (52.8, 0)]
MASK_AFTER = [(0.0, 0), (53.4, 0), (53.45, 1), (61.9, 1), (62.4, 0)]


# ------------------------------------------------------------------------------------------------ helpers
def db(x: float) -> float:
    return 10 ** (x / 20)


def todb(x: float) -> float | None:
    return None if x <= 0 else round(20 * math.log10(x), 2)


def hz(name_or_midi) -> float:
    m = midi(name_or_midi) if isinstance(name_or_midi, str) else name_or_midi
    return 440.0 * 2 ** ((m - 69) / 12)


def rng_for(*key) -> np.random.Generator:
    h = hashlib.sha256(repr((SEED,) + key).encode()).digest()
    return np.random.default_rng(int.from_bytes(h[:8], "little"))


def ramp_in(n: int) -> np.ndarray:                   # 0 -> 1 raised cosine, first value exactly 0
    return 0.5 - 0.5 * np.cos(np.pi * np.arange(n) / n)


def ramp_out(n: int) -> np.ndarray:                  # 1 -> 0 raised cosine, last value exactly 0
    return 0.5 + 0.5 * np.cos(np.pi * (np.arange(n) + 1) / n)


def seal(x: np.ndarray, a_ms: float, r_ms: float) -> np.ndarray:
    """Force a clip to start and end at exactly zero through raised-cosine ramps."""
    x = np.array(x, dtype=np.float64)
    na, nr = max(2, int(a_ms * SR / 1000)), max(2, int(r_ms * SR / 1000))
    shape = (-1, 1) if x.ndim == 2 else (-1,)
    x[:na] *= ramp_in(na).reshape(shape)
    x[-nr:] *= ramp_out(nr).reshape(shape)
    return x


def automation(points, n: int, t0: float = 0.0) -> np.ndarray:
    """Breakpoint curve sampled at SR from t0 for n samples; raised-cosine between points, held outside."""
    t = t0 + np.arange(n) / SR
    out = np.full(n, float(points[0][1]))
    for (ta, va), (tb, vb) in zip(points, points[1:]):
        m = (t >= ta) & (t < tb)
        if tb > ta and m.any():
            u = (t[m] - ta) / (tb - ta)
            out[m] = va + (vb - va) * (0.5 - 0.5 * np.cos(np.pi * u))
    out[t >= points[-1][0]] = points[-1][1]
    return out


def to_stereo(x: np.ndarray) -> np.ndarray:
    return np.stack([x, x], 1) if x.ndim == 1 else x


def pan_gains(p: float) -> tuple[float, float]:     # equal power, unity at centre
    a = (np.clip(p, -1, 1) + 1) * np.pi / 4
    return float(np.cos(a) * np.sqrt(2)), float(np.sin(a) * np.sqrt(2))


def filt(x: np.ndarray, sos) -> np.ndarray:
    return sosfilt(sos, x, axis=0)


def biquad(kind: str, f0: float, gain_db: float = 0.0, q: float = 0.707) -> np.ndarray:
    """RBJ cookbook biquad as one SOS row: 'peak', 'lowshelf', 'highshelf'."""
    A, w = 10 ** (gain_db / 40), 2 * np.pi * f0 / SR
    cw, sw = np.cos(w), np.sin(w)
    if kind == "peak":
        al = sw / (2 * q)
        b, a = [1 + al * A, -2 * cw, 1 - al * A], [1 + al / A, -2 * cw, 1 - al / A]
    else:
        al = sw / 2 * np.sqrt(2)                     # shelf slope S = 1
        s = 2 * np.sqrt(A) * al
        if kind == "highshelf":
            b = [A * ((A + 1) + (A - 1) * cw + s), -2 * A * ((A - 1) + (A + 1) * cw), A * ((A + 1) + (A - 1) * cw - s)]
            a = [(A + 1) - (A - 1) * cw + s, 2 * ((A - 1) - (A + 1) * cw), (A + 1) - (A - 1) * cw - s]
        else:
            b = [A * ((A + 1) - (A - 1) * cw + s), 2 * A * ((A - 1) - (A + 1) * cw), A * ((A + 1) - (A - 1) * cw - s)]
            a = [(A + 1) + (A - 1) * cw + s, -2 * ((A - 1) + (A + 1) * cw), (A + 1) + (A - 1) * cw - s]
    return np.array([[b[0] / a[0], b[1] / a[0], b[2] / a[0], 1.0, a[1] / a[0], a[2] / a[0]]])


def norm_peak(x: np.ndarray, peak_db: float) -> np.ndarray:
    p = np.abs(x).max()
    return x * (db(peak_db) / p) if p > 0 else x


def rms_db(x: np.ndarray) -> float | None:
    return todb(float(np.sqrt(np.mean(np.square(x))))) if x.size else None


class Bus:
    """A stereo stem plus its reverb send. Every placement is logged for the report."""

    def __init__(self, name: str, n: int):
        self.name, self.x, self.send, self.log = name, np.zeros((n, 2)), np.zeros((n, 2)), []

    def add(self, clip: np.ndarray, t: float, gain: float = 1.0, pan: float = 0.0, send: float = 0.0, **info):
        clip = to_stereo(clip)
        assert abs(clip[0]).max() < 1e-12 and abs(clip[-1]).max() < 1e-12, f"unsealed clip {info}"
        i = int(round(t * SR))
        n = len(self.x)
        if i >= n:
            return
        c = clip[: n - i].copy()
        if len(c) < len(clip):                       # cut by the end of the film: fade the cut edge too
            c[-96:] *= ramp_out(96)[:, None]
        gl, gr = pan_gains(pan)
        c[:, 0] *= gain * gl
        c[:, 1] *= gain * gr
        self.x[i: i + len(c)] += c
        if send:
            self.send[i: i + len(c)] += c * send
        if info:
            self.log.append({"t_placed": round(i / SR, 6), "sample": i, **info})


# ------------------------------------------------------------------------------------------------ piano tuning
# Director, QA round 3: the VCSL Steinway B bass samples are off equal temperament and beat against the pad (QA
# measured D2 -29, A2 -24, B2 +11, B3 -11 ct). Every felt-piano note at B3 and below is corrected to ET.
# What the measurement shows (dev/r3/probe*.py, track*.py): in these samples the FIRST partial is not where the rest of
# the string is, and it moves. Partials 2-8 sit within -4..+1 ct of ET (energy-weighted) and hold still; the first
# partial is two components of a detuned unison, a prompt one that dominates the first 1.5-2.3 s and a slower one
# after it: D2 -28 ct then -8, G#2 (A2) -23 then -10, A#2 (B2) +7 then -10, A#3 (B3) -7 then -3 (QA's 0.15-1.2 s
# readings are the prompt component). One resampling ratio cannot fix that: by the first partial it would put D2 and
# A2's audible partials 20-25 ct sharp against D3, E3 and the pad's A2 harmonics; by the harmonics it would leave the
# fundamental that beats against the pad's near-sine D2 and A2 where it is; by QA's window alone it would put the slow
# component of D2 and A2 16-21 ct sharp for the long holds (measured: the Asus4 at 48 then beat at 1 Hz, 52 %).
# So each sample is split at sqrt(2) x f0 (linear-phase Kaiser FIR, the two bands sum back exactly): partials 2+ are
# resampled by their measured offset, and the first-partial band is read at a slowly varying rate (cubic-spline
# variable speed, then resampled exactly like the rest) that cancels its measured offset: QA's own reading (FFT peak,
# 0.15-1.2 s) up to 1.2 s, then, after a 0.5 s raised-cosine blend, its measured track (phase slope of the band-passed
# analytic signal in 0.4 s windows, median then Hann smoothed over +/- 0.5 s), which follows the slow component that
# carries the long holds. Measured once per sample (mono sum, from the sample's first frame, QA's convention) and
# cached in TUNING_JSON with each file's md5 and the measurement settings.
TUNING_JSON = HERE / "piano-tuning.json"
TUNE_UP_TO = "B3"                                    # notes at and below are corrected; above (the motif) untouched
TUNE_FIRST_WINDOW = (0.15, 1.2)                      # s: QA's reading of the first partial, applied up to its end
TUNE_BLEND_S = 0.5                                   # s: then blended into the measured track over this long
TUNE_HARMONIC_WINDOW = (0.15, 2.4)                   # s: partials 2-8, over the length a chord is held
TUNE_HARMONICS = range(2, 9)
TUNE_TRACK = {"window_s": 0.4, "hop_s": 0.05, "span_s": 8.0, "floor_db": -45.0, "smooth_s": 0.5, "store_hop_s": 0.1}


def first_partial_track(x: np.ndarray, sr: int, f0: float) -> tuple[np.ndarray, np.ndarray]:
    """Cents re f0 of the first partial over time: weighted phase slope of the +/-6 % band-passed analytic signal in
    sliding windows (weights |z|), windows quieter than floor_db re the loudest dropped, then a running median and a
    Hann-weighted mean over +/- smooth_s (no pitch wobble from the unison's own beating survives into the curve)."""
    P = TUNE_TRACK
    W, hop, span = P["window_s"], P["hop_s"], P["span_s"]
    z = hilbert(sosfiltfilt(butter(4, [f0 * 0.94, f0 * 1.06], "band", fs=sr, output="sos"), x[: int((span + W) * sr)]))
    ph, amp = np.unwrap(np.angle(z)), np.abs(z)
    t, c, w = [], [], []
    for t0 in np.arange(0.05, span, hop):
        i0, i1 = int(t0 * sr), int((t0 + W) * sr)
        if i1 > len(z):
            break
        slope = np.polyfit(np.arange(i1 - i0) / sr, ph[i0:i1], 1, w=amp[i0:i1])[0]
        t.append(t0 + W / 2)
        c.append(1200 * math.log2(slope / (2 * np.pi) / f0))
        w.append(amp[i0:i1].mean())
    t, c, w = np.array(t), np.array(c), np.array(w)
    keep = 20 * np.log10(w / w.max()) > P["floor_db"]
    t, c = t[keep], c[keep]
    near = [np.abs(t - ti) <= P["smooth_s"] for ti in t]
    med = np.array([np.median(c[m]) for m in near])
    sm = np.array([np.average(med[m], weights=np.cos(np.pi * (t[m] - ti) / (2.0 * P["smooth_s"] + 1e-9)) ** 2)
                   for m, ti in zip(near, t)])
    step = max(1, int(round(P["store_hop_s"] / hop)))
    return t[::step], sm[::step]


def measure_sample(path: Path, nominal: int) -> dict:
    """A sample's tuning (cents re ET at its nominal pitch): partials 2-8 (energy-weighted, applied), the first
    partial's track (applied), and QA's 0.15-1.2 s first-partial reading (reported)."""
    x, sr = sf.read(path, dtype="float64", always_2d=True)
    x = x.mean(1)
    f0 = hz(nominal)

    def partials(a, b, ks):
        seg = x[int(a * sr): int(b * sr)]
        nfft = 2 ** 21
        X = np.abs(np.fft.rfft(seg * np.hanning(len(seg)), nfft))
        f = np.fft.rfftfreq(nfft, 1 / sr)
        out = []
        for k in ks:
            m = np.flatnonzero((f > k * f0 * 0.97) & (f < k * f0 * 1.03))
            i = int(m[np.argmax(X[m])])
            la, lb, lc = np.log(X[i - 1: i + 2])
            d = 0.5 * (la - lc) / (la - 2 * lb + lc)
            out.append((1200 * math.log2((i + d) * sr / nfft / (k * f0)), float(X[i])))
        return out

    first = partials(*TUNE_FIRST_WINDOW, [1])[0][0]
    hp = partials(*TUNE_HARMONIC_WINDOW, TUNE_HARMONICS)
    e = np.array([a for _, a in hp]) ** 2
    harm = float(np.sum(e * np.array([c for c, _ in hp])) / e.sum())
    lv = 10 * np.log10(e / e.max())
    tt, cc = first_partial_track(x, sr, f0)
    return {"md5": hashlib.md5(path.read_bytes()).hexdigest(), "nominal": path.name.split("_")[3],
            "settings": _tune_settings(), "harmonic_cents": round(harm, 2),
            "first_partial_track": {"t_s": [round(float(v), 3) for v in tt], "cents": [round(float(v), 2) for v in cc]},
            "first_partial_cents_qa_window": round(first, 2),
            "partials_2_8_cents": [round(c, 2) for c, _ in hp], "partials_2_8_level_db": [round(float(v), 1) for v in lv]}


def _tune_settings() -> dict:
    return {"first_qa_window_s": list(TUNE_FIRST_WINDOW), "harmonic_window_s": list(TUNE_HARMONIC_WINDOW),
            "harmonics": [TUNE_HARMONICS.start, TUNE_HARMONICS.stop - 1], "track": TUNE_TRACK}


def first_partial_curve(row: dict, hop: float = 0.01) -> tuple[np.ndarray, np.ndarray]:
    """The first partial's offset (cents) over the sample's time: QA's window reading up to the window's end, a
    raised-cosine blend over TUNE_BLEND_S, then the measured track (held at its last value)."""
    tr = row["first_partial_track"]
    t = np.arange(0.0, max(tr["t_s"][-1], TUNE_FIRST_WINDOW[1] + TUNE_BLEND_S) + hop, hop)
    u = np.clip((t - TUNE_FIRST_WINDOW[1]) / TUNE_BLEND_S, 0.0, 1.0)
    w = 0.5 - 0.5 * np.cos(np.pi * u)
    return t, (1 - w) * row["first_partial_cents_qa_window"] + w * np.interp(t, tr["t_s"], tr["cents"])


def _name(m: int) -> str:
    return ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"][m % 12] + str(m // 12 - 1)


def variable_speed(y: np.ndarray, sr: int, t_pts, cents) -> np.ndarray:
    """Read y at the rate 2^(c(t)/1200) (c > 0 raises the pitch), c interpolated from the track and held at its ends;
    cubic-spline interpolation (the band read here lies below 1.7 f0, far under Nyquist)."""
    r = 2 ** (np.interp(np.arange(len(y)) / sr, t_pts, cents) / 1200)
    pos = np.concatenate([[0.0], np.cumsum(r[:-1])])
    pos = pos[pos <= len(y) - 1]
    return np.stack([map_coordinates(y[:, ch], [pos], order=3, mode="nearest") for ch in range(y.shape[1])], 1)


class TunedPiano(Piano):
    """tools/audio-poc/sampler.Piano with the samples behind every note at or below TUNE_UP_TO corrected to equal
    temperament (two bands, see above). Notes above it go through Piano._load untouched (bit-identical)."""

    def __init__(self, root: Path, sr: int, table_path: Path = TUNING_JSON):
        super().__init__(root, sr)
        self.table_path = table_path
        try:
            self.table = json.loads(table_path.read_text()).get("samples", {})
        except (OSError, ValueError):
            self.table = {}
        self.limit = midi(TUNE_UP_TO)
        self.used: set = set()

    def offsets(self, key: tuple[int, int]) -> dict:
        """The cached measurement for one sample; measured (and the cache rewritten) only if missing or stale."""
        f = self.files[key]
        row = self.table.get(f.name)
        if (row is None or row.get("md5") != hashlib.md5(f.read_bytes()).hexdigest()
                or row.get("settings") != json.loads(json.dumps(_tune_settings()))):
            self.table[f.name] = row = measure_sample(f, key[0])
            doc = {"about": "VCSL Steinway B sample tuning re equal temperament, measured once by score.py "
                            "(measure_sample) for the samples behind notes at or below " + TUNE_UP_TO + ". Applied: "
                            "partials 2+ resampled by -harmonic_cents; the first-partial band (below sqrt(2) f0) read "
                            "at the rate that cancels first_partial_cents_qa_window (QA's 0.15-1.2 s reading) up to "
                            "1.2 s, then first_partial_track (after a 0.5 s blend).",
                   "samples": dict(sorted(self.table.items()))}
            self.table_path.write_text(json.dumps(doc, indent=1) + "\n")
        return row

    def report(self) -> dict:
        """The corrections applied in this render, per sample used below the limit."""
        rows = {}
        for k in sorted({k for (k, s) in self.used}):
            r = self.table[self.files[k].name]
            rows[self.files[k].name] = {
                "plays": sorted({_name(k[0] + s) for (kk, s) in self.used if kk == k}),
                "partials_2_8_cents": r["harmonic_cents"], "first_partial_qa_window_cents": r["first_partial_cents_qa_window"],
                "first_partial_offset_cents_at_0.5_1_1.5_2_3_5s": [round(float(np.interp(v, *first_partial_curve(r))), 1)
                                                                    for v in (0.5, 1, 1.5, 2, 3, 5)]}
        return {"tuned_up_to": TUNE_UP_TO, "cache": str(self.table_path),
                "method": "split at sqrt(2) f0; partials 2+ resampled by -harmonic_cents; the first-partial band read "
                          "at the rate that cancels its offset (QA window reading to 1.2 s, then its measured track), "
                          "then resampled with the rest", "samples": rows}

    @lru_cache(maxsize=256)
    def _load(self, key: tuple[int, int], semis: int) -> np.ndarray:
        if key[0] + semis > self.limit:
            return Piano._load(self, key, semis)
        row = self.offsets(key)
        self.used.add((key, semis))
        x, sr0 = sf.read(self.files[key], dtype="float64", always_2d=True)

        def shift(y, cents):                         # Piano._load's ratio, with the correction in it
            r = (Fraction(self.sr, sr0) / Fraction(2 ** ((semis + cents / 100) / 12)).limit_denominator(4000)
                 ).limit_denominator(4000)
            return resample_poly(y, r.numerator, r.denominator, axis=0)

        f0 = hz(key[0])
        ntaps, beta = kaiserord(80.0, 0.5 * f0 / (0.5 * sr0))
        h = firwin(ntaps | 1, math.sqrt(2) * f0, window=("kaiser", beta), fs=sr0)
        low = fftconvolve(x, h[:, None], mode="same", axes=0)
        tt, cc = first_partial_curve(row)
        a = shift(variable_speed(low, sr0, tt, -cc), 0.0)
        b = shift(x - low, -row["harmonic_cents"])
        n = min(len(a), len(b))
        return a[:n] + b[:n]


# ------------------------------------------------------------------------------------------------ instruments
# The piano bus (director, QA round 3): a gentle high shelf for a touch of air, on the felt piano only.
PIANO_AIR = ("highshelf", 6000, 1.5)


class FeltPiano:
    """VCSL Steinway B (CC0) through tools/audio-poc/sampler.py (tuned below B3, TunedPiano), made 'felt': softest
    layer, a 5 ms hammer softening ramp, una corda colour (high shelf -3 dB at 2.2 kHz), a 4 kHz low-pass (§5.1), a
    small 280 Hz dip against mud, and a shorter sustain than the pedal-down recordings."""

    def __init__(self, root: Path):
        self.p = TunedPiano(root, SR)
        self.chain = np.vstack([butter(2, 40, "high", fs=SR, output="sos"), biquad("peak", 280, -2.0, 0.8),
                                biquad("highshelf", 2200, -3.0), butter(4, 4000, "low", fs=SR, output="sos")])

    def voice(self, name: str, vel: float, length: float, tau: float | None, rel: float, soft_ms: float = 5.0,
              lift_db: float = 0.0, lift_tau: float = 0.45):
        """One string: sample from onset, extra decay tau, raised-cosine release over `rel` ending at `length`.
        lift_db raises the aftersound against the hammer's prompt sound (gain 1 at the onset, rising smoothly to
        +lift_db with time constant lift_tau): what una corda does to a real string, and what lets a soft-layer
        chord sustain under the motif instead of falling 17 dB in 2 s."""
        y = self.p.note(midi(name), vel, dur=None)
        n = int(length * SR)
        y = y[:n] if len(y) >= n else np.vstack([y, np.zeros((n - len(y), 2))])
        y = y.copy()
        ns = int(soft_ms * SR / 1000)
        y[:ns] *= ramp_in(ns)[:, None]
        t = np.arange(n) / SR
        if tau:
            y *= np.exp(-t / tau)[:, None]
        if lift_db:
            y *= db(lift_db * (1 - np.exp(-t / lift_tau)))[:, None]
        nr = max(2, int(rel * SR))
        y[-nr:] *= ramp_out(nr)[:, None]
        return y

    def felt(self, x: np.ndarray, tail: float = 0.08) -> np.ndarray:
        x = np.vstack([x, np.zeros((int(tail * SR), 2))])
        return filt(x, self.chain)

    def chord(self, notes, roll, off, rel, vel, peak_db, tau=4.0, weights=None, rng=None, lift_db=0.0, shave_db=0.0,
              soft_ms=5.0, onset_ms=0.0):
        """Rolled chord: note i at i*roll; all keys up (pedal change) at `off` s after the first note.
        shave_db: the strike's true peak is taken down by that much by a look-ahead peak limiter on this clip alone
        (limiter_gain: 5 ms Hann attack, 150 ms release) before the clip is normalised to peak_db, so the piano's
        own transient is softened instead of the master limiter ducking the whole mix on every onset.
        soft_ms: each hammer's softening ramp. onset_ms: a raised-cosine swell over the start of the clip, applied
        after it is set to peak_db (the body keeps its level; the first strikes of the roll come in under it)."""
        length = off + rel
        bus = np.zeros((int((length + 0.01) * SR), 2))
        k = len(notes)
        for i, name in enumerate(notes):
            w = weights[i] if weights else (0.0 if i == 0 else -1.0 if i == k - 1 else -2.5)
            w += 0.4 * rng.standard_normal() if rng is not None else 0.0
            v = self.voice(name, vel, length - i * roll, tau, rel, soft_ms=soft_ms, lift_db=lift_db)
            j = int(round(i * roll * SR))
            bus[j: j + len(v)] += v * db(w)
        y = self.felt(bus)
        if shave_db:
            y = y * limiter_gain(y, 20 * math.log10(float(true_peak(y).max())) - shave_db)[:, None]
        y = norm_peak(y, peak_db)
        if onset_ms:
            no = int(onset_ms * SR / 1000)
            y[:no] *= ramp_in(no)[:, None]
        return seal(y, 0.5, 40)


def mallet(f: float, length: float, tau: float, partials, attack_ms: float, rng, noise_db=-30.0, noise_band=(60, 900),
           noise_tau=0.003, drop=0.0, drop_tau=0.015, rel_ms=None) -> np.ndarray:
    """Struck bar / felt mallet by modal synthesis: (ratio, amplitude, decay scale) partials below 16 kHz, a
    felt-contact noise burst, an optional pitch drop in semitones, raised-cosine attack and end."""
    n = int(length * SR)
    t = np.arange(n) / SR
    bend = 2 ** (drop * np.exp(-t / drop_tau) / 12) if drop else 1.0
    y = np.zeros(n)
    for r, a, ds in partials:
        fr = f * r
        if fr * (2 ** (drop / 12) if drop else 1) >= 16000:
            continue
        ph = 2 * np.pi * np.cumsum(fr * bend * np.ones(n)) / SR
        ph -= ph[0]
        y += a * np.sin(ph) * np.exp(-t / (tau * ds))
    if noise_db > -90:
        nz = rng.standard_normal(n) * np.exp(-t / noise_tau)
        nz = filt(nz, butter(2, noise_band, "band", fs=SR, output="sos"))
        y += db(noise_db) * nz / (np.abs(nz).max() + 1e-12) * np.abs(y).max()
    return seal(y, attack_ms, rel_ms if rel_ms else max(3.0, length * 250))


def heart(rng, glass_rel_db: float, distant: bool) -> tuple[np.ndarray, np.ndarray]:
    """LUB/DUB: a sub thump on D1 (60 ms body, falling an octave onto D1 over 40 ms) and a glass tick on E7
    (2637 Hz, ~2.6 kHz, 8 ms). Saturation (odd harmonics) and a quiet 2nd harmonic make it read on small speakers."""
    n = int(0.16 * SR)
    t = np.arange(n) / SR
    f1 = hz("D1")
    f = f1 * 2 ** (np.clip(1 - t / 0.04, 0, 1) ** 2)
    ph = 2 * np.pi * np.cumsum(f) / SR
    ph -= ph[0]
    env = np.exp(-np.maximum(t - 0.004, 0) / 0.026)
    body = np.sin(ph) * env
    body = np.tanh(1.5 * body) / np.tanh(1.5) + 0.20 * np.sin(2 * ph) * env
    body = seal(body, 3.0, 45.0)
    m = int(0.008 * SR)
    tt = np.arange(m) / SR
    tick = np.sin(2 * np.pi * hz("E7") * tt) * np.exp(-tt / 0.0022)
    tick = seal(tick, 0.6, 2.0)
    if distant:                                      # the wait: same heart, further away
        tick = filt(np.concatenate([tick, np.zeros(96)]), butter(2, 1800, "low", fs=SR, output="sos"))
        tick = seal(tick, 0.6, 1.0)
    return body, tick / np.abs(tick).max() * db(glass_rel_db)


def noise_tap(rng, length_ms, band, attack_ms, tau_ms, body_hz=None, body_db=-12.0, body_tau_ms=1.5):
    n = int(length_ms * SR / 1000)
    t = np.arange(n) / SR
    exc = rng.standard_normal(n) * np.exp(-t / (tau_ms / 1000))
    exc[: max(2, int(attack_ms * SR / 1000))] *= ramp_in(max(2, int(attack_ms * SR / 1000)))
    y = filt(np.concatenate([exc, np.zeros(int(0.002 * SR))]), butter(2, band, "band", fs=SR, output="sos"))
    y /= np.abs(y).max() + 1e-12
    if body_hz:
        tb = np.arange(len(y)) / SR
        y += db(body_db) * np.sin(2 * np.pi * body_hz * tb) * np.exp(-tb / (body_tau_ms / 1000))
    return seal(y, attack_ms, 1.2)


def sine_tone(f, length, attack_ms, tau, rel_ms):
    t = np.arange(int(length * SR)) / SR
    return seal(np.sin(2 * np.pi * f * t) * np.exp(-t / tau), attack_ms, rel_ms)


# ------------------------------------------------------------------------------------------------ event sounds
def make_event(kind: str, cue: dict, piano: FeltPiano, peak_db: float, ctx: dict):
    """Return the clips [(clip, 0), ...] for one cue, normalised to the reference peak level peak_db."""
    t = cue["t"]
    rng = rng_for(kind, round(t * 1000), cue.get("note"), cue.get("fmt"))
    if kind == "PASTE":                              # dry plastic click, 8 ms: the one unmusical sound
        y = noise_tap(rng, 6.5, (1100, 3600), 0.3, 0.7)
        tt = np.arange(len(y)) / SR
        y = y + 0.55 * np.sin(2 * np.pi * 1650 * tt) * np.exp(-tt / 0.0016) + 0.5 * np.sin(2 * np.pi * 620 * tt) * np.exp(-tt / 0.0025)
        return [(seal(norm_peak(y, peak_db), 0.3, 1.5), 0)]
    if kind == "THUMP":                              # low felt-mallet thump on D2, 120 ms
        y = mallet(hz("D2"), 0.16, 0.045, [(1, 1, 1), (2, 0.30, 0.6), (3.99, 0.10, 0.3)], 2.0, rng,
                   noise_db=-16, noise_band=(40, 420), noise_tau=0.004, drop=1.5, drop_tau=0.012, rel_ms=40)
        return [(norm_peak(y, peak_db), 0)]
    if kind == "THOCK":                              # felt low-mid thock, 80 ms, tuned to A3
        y = mallet(hz("A3"), 0.08, 0.020, [(1, 1, 1), (2.32, 0.35, 0.6), (4.1, 0.10, 0.35)], 1.5, rng,
                   noise_db=-12, noise_band=(150, 1200), noise_tau=0.002, rel_ms=20)
        return [(norm_peak(y, peak_db), 0)]
    if kind == "TICK":                               # muted marimba A4, 180 ms
        y = mallet(hz("A4"), 0.18, 0.045, [(1, 1, 1), (3.99, 0.22, 0.35), (10.65, 0.05, 0.15)], 1.5, rng,
                   noise_db=-24, noise_band=(200, 1600), noise_tau=0.0015, rel_ms=40)
        return [(norm_peak(y, peak_db), 0)]
    if kind == "LAND":                               # dry wood tick, 20 ms, tuned to D6
        y = mallet(hz("D6"), 0.020, 0.0045, [(1, 1, 1), (1.58, 0.55, 0.8), (2.37, 0.22, 0.5)], 0.4, rng,
                   noise_db=-8, noise_band=(600, 2500), noise_tau=0.0006, rel_ms=5)
        return [(norm_peak(y, peak_db), 0)]
    if kind == "LABEL":                              # paper tap, 6 ms, band 1 to 3 kHz
        c = 1 + 0.07 * rng.standard_normal()
        y = noise_tap(rng, 6.0, (1000 * c, 3000 * c), 0.25, 1.1, body_hz=380 * c, body_db=-12)
        return [(norm_peak(y, peak_db + 0.8 * rng.standard_normal()), 0)]
    if kind in ("LUB", "DUB"):
        body, tick = heart(rng, glass_rel_db=-12.0 + (-6.0 if ctx["distant"] else 0.0), distant=ctx["distant"])
        body = norm_peak(body, peak_db)
        tick = tick * db(peak_db)                    # tick is -12 dB re the thump peak (palette: -24 / -36)
        return [(body, 0), (tick, 0)]
    if kind == "GLASS":                              # sine E6, 0.4 s
        return [(norm_peak(sine_tone(hz(cue.get("note") or "E6"), 0.4, 3.0, 0.12, 60), peak_db), 0)]
    if kind == "DYAD":                               # felt piano A4 + D5 on an arrival
        y = piano.chord(["A4", "D5"], 0.012, 1.7, 0.6, 0.30, peak_db, tau=1.3, weights=[0, -1.0], rng=rng,
                        shave_db=PIANO_SHAVE_DB)
        return [(y, 0)]
    if kind == "PLINK":                              # felt piano single note per sub-plate lock
        n = cue.get("note") or ctx["plink_note"]
        y = piano.chord([n], 0, 0.9, 0.4, 0.30, peak_db, tau=0.6, rng=rng)
        return [(y, 0)]
    if kind == "MOTIF":                              # felt piano, ~3 s natural decay; mp layer so it sings
        n = cue.get("note") or ctx["motif_note"]
        off = min(3.0, ctx.get("motif_until", 99) - t)
        y = piano.chord([n], 0, off, 0.6 if off >= 3.0 else 0.15, 0.60, peak_db, tau=2.2, rng=rng,
                        shave_db=PIANO_SHAVE_DB)
        return [(y, 0)]
    if kind == "LOW":                                # felt piano D3, 4 s decay
        y = piano.chord([cue.get("note") or "D3"], 0, 3.6, 0.6, 0.60, peak_db, tau=2.0, rng=rng,
                        shave_db=PIANO_SHAVE_DB)
        return [(y, 0)]
    raise KeyError(kind)


# ------------------------------------------------------------------------------------------------ score layers
def piano_layer(piano: FeltPiano, bus: Bus):
    for t0, name, notes, pk, roll, off, rel, vel in CHORDS:
        rng = rng_for("PIANO", round(t0 * 1000))
        k, emph = len(notes), CHORD_VOICE_DB.get(t0, {})
        weights = [(0.0 if i == 0 else -1.0 if i == k - 1 else -2.5) + emph.get(nm, 0.0) for i, nm in enumerate(notes)]
        onset = CHORD_ONSET.get(t0, {})
        y = piano.chord(notes, roll, off - t0, rel, vel, pk, tau=CHORD_TAU, weights=weights, rng=rng,
                        lift_db=CHORD_LIFT_DB, shave_db=PIANO_SHAVE_DB, **onset)
        bus.add(y, t0, send=0.30, kind="PIANO", source="score", t_cue=t0, chord=name, notes=notes,
                onsets=[round(t0 + i * roll, 4) for i in range(len(notes))], level_dbfs_ref=pk, roll_ms=roll * 1000,
                sustain_lift_db=CHORD_LIFT_DB, strike_shave_db=PIANO_SHAVE_DB, voice_weights_db=weights,
                **({"onset": onset} if onset else {}))


def fold_piano(buses: dict) -> dict:
    """The piano bus joins the music stem through its EQ (PIANO_AIR), dry and send alike (a post-insert send)."""
    pb = buses.pop("piano")
    sos = biquad(*PIANO_AIR)
    buses["music"].x += filt(pb.x, sos)
    buses["music"].send += filt(pb.send, sos)
    buses["music"].log += pb.log
    return {"piano_bus_eq": {"type": PIANO_AIR[0], "f_hz": PIANO_AIR[1], "gain_db": PIANO_AIR[2]},
            "piano_bus_peak_dbfs_ref": todb(float(np.abs(pb.x).max()))}


def additive(f: float, n: int, t0: float, cutoff: np.ndarray, rng, slope=1.5, fmax=4500.0, poles=4,
             width=0.35, drift=0.5) -> np.ndarray:
    """Band-limited additive tone, stereo: harmonics k*f < fmax weighted k^-slope through a time-varying
    Butterworth-shaped magnitude at `cutoff` (Hz per sample). No aliasing by construction.
    Mono-safe width and life without beating: each harmonic exists once (no detuned twins, whose beating would be
    a tremolo in a mono sum), is panned on its own (odd left, even right, fundamental centred; L+R never cancels),
    and drifts in phase by `drift` rad at its own 0.04 to 0.17 Hz (a frequency wander under 0.09 Hz, ~2 cents on D2)."""
    t = t0 + np.arange(n) / SR
    out = np.zeros((n, 2))
    for k in range(1, int(fmax // f) + 1):
        h = k ** -slope / np.sqrt(1 + (k * f / cutoff) ** (2 * poles))
        rate, psi, theta = rng.uniform(0.04, 0.17), rng.uniform(0, 2 * np.pi), rng.uniform(0, 2 * np.pi)
        x = h * np.sin(2 * np.pi * k * f * t + theta + drift * np.sin(2 * np.pi * rate * t + psi))
        gl, gr = pan_gains(0.0 if k == 1 else width * (1 if k % 2 else -1) * rng.uniform(0.5, 1.0))
        out[:, 0] += x * gl
        out[:, 1] += x * gr
    return out


def pad_layer(n_total: int, bus: Bus) -> dict:
    """PAD (Dsus2 + F#4 in the product world) and DRONE (thinned to A2) as one set of voices whose gains and
    low-pass follow SCRIPT §5.2/5.3. Calibrated to PAD_RMS_DB (pad, 5-14 s) and DRONE_RMS_DB (drone, 21-26 s)."""
    t0, t1 = 3.5, 52.8
    i0, n = int(t0 * SR), int((t1 - t0) * SR)
    fc = automation(PAD_CUTOFF, n, t0)
    breath = 1 + 0.05 * np.sin(2 * np.pi * 0.071 * (t0 + np.arange(n) / SR))      # very slow, < 0.5 dB
    voices = {}
    for name, (vdb, _) in PAD_VOICES.items():
        voices[name] = additive(hz(name), n, t0, fc, rng_for("PAD", name), slope=PAD_SLOPE[name]) * db(vdb) * breath[:, None]

    def value(v, drone_gain):
        return drone_gain * db(v[1]) if isinstance(v, tuple) else v

    def gains(drone_gain):
        return {k: automation([(tt, value(v, drone_gain)) for tt, v in pts], n, t0)
                for k, (_, pts) in PAD_VOICES.items()}

    def mix(drone_gain):
        g = gains(drone_gain)
        return sum(voices[k] * g[k][:, None] for k in voices)

    hp = butter(2, 50, "high", fs=SR, output="sos")
    y = filt(mix(1.0), hp)
    w = lambda a, b: slice(int((a - t0) * SR), int((b - t0) * SR))  # noqa: E731
    scale = db(PAD_RMS_DB) / np.sqrt(np.mean(y[w(5.0, 7.0)] ** 2))
    drone_alone = filt(voices["A2"], hp) * scale     # A2 at gain 1, as calibrated
    dg = db(DRONE_RMS_DB) / np.sqrt(np.mean(drone_alone[w(21.0, 26.0)] ** 2))
    y = filt(mix(dg), hp) * scale
    y = seal(y, 1.0, 1.0)
    label = lambda v: f"drone{v[1]:+.1f}dB" if isinstance(v, tuple) else v  # noqa: E731
    bus.add(y, t0, send=0.15, kind="PAD", source="score", t_cue=t0,
            automation={"voices": {k: [(tt, label(v)) for tt, v in pts] for k, (_, pts) in PAD_VOICES.items()},
                        "cutoff_hz": PAD_CUTOFF, "drone_gain_db": round(20 * math.log10(dg), 2),
                        "pad_rms_dbfs_ref_5_14": PAD_RMS_DB, "drone_rms_dbfs_ref_21_26": DRONE_RMS_DB})
    # mono tremolo check: envelope of the mono sum's 60-250 Hz band in 50 ms windows over a held stretch
    mono = filt(y[w(5.0, 7.0)].sum(1), butter(4, [60, 250], "band", fs=SR, output="sos"))[int(0.5 * SR):]
    env = np.sqrt(uniform_filter1d(mono ** 2, 2400)[::2400])
    ripple = 20 * math.log10(np.percentile(env, 95) / np.percentile(env, 5))
    return {"pad_mono_low_band_envelope_ripple_db_5_7s": round(ripple, 2),
            "pad_rms_5_7": rms_db(y[w(5.0, 7.0)]), "pad_rms_5_14": rms_db(y[w(5.0, 14.0)]), "drone_rms_21_26": rms_db(y[w(21.0, 26.0)]),
            "drone_rms_breath_26.7_28.7": rms_db(y[w(26.7, 28.7)]),
            "pad_rms_30_40": rms_db(y[w(30.0, 40.0)]), "open_fifth_rms_42_52": rms_db(y[w(42.0, 52.0)])}


def room_layer(n_total: int, bus: Bus) -> dict:
    """ROOM: a quiet 'paper room': pink-tilted noise band-passed 200 Hz to 2 kHz, decorrelated L/R, a slow
    (0.2 Hz) seeded drift of +/- 1 dB. -52 dBFS RMS when fully on."""
    rng = rng_for("ROOM")
    x = rng.standard_normal((n_total, 2))
    x = filt(x, np.vstack([butter(4, [200, 2000], "band", fs=SR, output="sos"), biquad("highshelf", 900, -3.0)]))
    drift = uniform_filter1d(rng.standard_normal(n_total // 24000 + 6), 5, mode="nearest")     # 0.5 s points, ~2.5 s smoothing
    drift = np.interp(np.arange(n_total) / 24000, np.arange(len(drift)), drift)
    x *= db(1.0 * drift / (np.abs(drift).max() + 1e-12))[:, None]
    x *= db(-52.0) / np.sqrt(np.mean(x ** 2))
    g = automation(ROOM_GAIN, n_total)
    y = seal(x * g[:, None], 1.0, 1.0)
    bus.add(y, 0.0, kind="ROOM", source="score", t_cue=0.0, automation=ROOM_GAIN, rms_dbfs_on=-52.0)
    return {"room_rms_0.1_3.4": rms_db(y[int(0.1 * SR): int(3.4 * SR)]), "room_rms_21_28": rms_db(y[int(21 * SR): int(28 * SR)]),
            "room_rms_41_52": rms_db(y[int(41 * SR): int(52 * SR)])}


RIPPLE = list(zip((53.55, 53.75, 53.95, 54.15, 54.35, 54.54), ("D2", "A2", "D3", "F#3", "A3", "D4")))


def ripple_note(n: str) -> tuple[np.ndarray, list[dict]]:
    """One boot-ripple note at its reference level: the felt mallet on n (BOOT_DB["RIPPLE"] peak) plus, for the low
    notes, its octave-up doublings (RIPPLE_DOUBLE_DB, dB re the low layer's peak). Mono, sealed."""
    def strike(f, rng, noise_db):
        tau = 0.75 * (hz("D2") / f) ** 0.25
        return mallet(f, 2.2, tau, [(1, 1, 1), (2.0, 0.10, 0.6), (3.99, 0.18, 0.35), (10.65, 0.03, 0.12)], 2.5, rng,
                      noise_db=noise_db, noise_band=(60, 700), noise_tau=0.003, rel_ms=300)

    y = norm_peak(strike(hz(n), rng_for("RIPPLE", n), -26), BOOT_DB["RIPPLE"])
    layers = []
    for k, d in enumerate(RIPPLE_DOUBLE_DB.get(n, ()), 1):
        up = _name(midi(n) + 12 * k)
        y = y + norm_peak(strike(hz(up), rng_for("RIPPLE", n, up), -99), BOOT_DB["RIPPLE"] + d)
        layers.append({"note": up, "db_re_low_layer": d})
    return y, layers


def boot_layer(piano: FeltPiano, music: Bus, piano_bus: Bus):
    """S11/S12: ripple, bloom, address note (SCRIPT §5.3 53.55 to 62.4; the glint air is cut, DROPPED_KINDS)."""
    for i, (t, n) in enumerate(RIPPLE):            # felt-mallet arpeggio following the plate locks
        y, layers = ripple_note(n)
        music.add(y, t, pan=-0.3 + 0.12 * i, send=0.35, kind="RIPPLE", source="score", t_cue=t, note=n,
                  level_dbfs_ref=BOOT_DB["RIPPLE"], **({"octave_doublings": layers} if layers else {}))

    # bloom: D3 F#3 A3 E4 (the motif's notes sounded together, upward, plus add9), 1.2 s attack.
    tb = 54.75
    rel_start, end = 56.4, 59.4                      # breathes out after the boot completes (57.0)
    n = int((end - tb) * SR)
    t = np.arange(n) / SR
    y = np.zeros((n, 2))
    for i, nm in enumerate(["D3", "F#3", "A3", "E4"]):
        rng = rng_for("BLOOM", nm)
        d = 0.05 * i                                 # upward: each voice starts 50 ms after the one below
        a = np.clip((t - d) / 1.2, 0, 1)
        env = (0.5 - 0.5 * np.cos(np.pi * a)) * np.where(t + tb < rel_start, 1.0, np.exp(-(t + tb - rel_start) / 0.75))
        x = additive(hz(nm), n, 0.0, np.full(n, 1800.0), rng, slope=2.0, fmax=5000, poles=2, width=0.25)
        gl, gr = pan_gains(-0.3 + 0.2 * i)            # the chord opens left to right, low to high
        y[:, 0] += x[:, 0] * env * gl * (0.85 if i == 3 else 1.0)
        y[:, 1] += x[:, 1] * env * gr * (0.85 if i == 3 else 1.0)
    y = seal(norm_peak(y, BOOT_DB["BLOOM"]), 2.0, 200.0)
    music.add(y, tb, send=0.30, kind="BLOOM", source="score", t_cue=tb, notes=["D3", "F#3", "A3", "E4"],
              attack_s=1.2, release_from=rel_start, level_dbfs_ref=BOOT_DB["BLOOM"])

    # address: one low felt D2, decaying to digital silence by 62.4
    ta = 57.6
    y = piano.chord(["D2"], 0, 62.38 - ta - 0.9, 0.9, 0.30, BOOT_DB["ADDRESS"], tau=1.6, rng=rng_for("ADDRESS"))
    piano_bus.add(y, ta, send=0.30, kind="ADDRESS", source="score", t_cue=ta, note="D2", level_dbfs_ref=BOOT_DB["ADDRESS"])


# ------------------------------------------------------------------------------------------------ cues
def shot_of(t: float, shots) -> str:
    for sid, a, b in shots:
        if a <= t < b:
            return sid
    return shots[-1][0]


def resolve_cues(doc: dict, fmt: str, fallback: bool) -> tuple[list[dict], dict]:
    """Event cues for this format from the cue file, with per-(kind, shot) fallback to SCRIPT §5.3."""
    shots = [(s["id"], s["start"], s["end"]) for s in doc.get("shots", [])] or SHOTS
    want = lambda c: c.get("fmt") in (None, fmt)  # noqa: E731
    film = [dict(c, source="cue file") for c in doc["cues"] if want(c)]
    events = [c for c in film if c["kind"] in EVENT_KINDS]
    other = [c for c in film if c["kind"] not in EVENT_KINDS]
    script = [dict(c, source="SCRIPT §5.3 fallback") for c in script_cues() if want(c) and c["kind"] in EVENT_KINDS]
    retimed = []
    for c in events + script:                        # CUE_RETIME: placed on the frame QA asked for
        for kind, tc, tol, to in CUE_RETIME:
            if c["kind"] == kind and abs(c["t"] - tc) < tol and c["t"] != to:
                if c["source"] == "cue file":
                    retimed.append({"kind": kind, "t_cue_file": c["t"], "t_placed": round(to, 6)})
                c["t"] = to
    have = {(c["kind"], shot_of(c["t"], shots)) for c in events}
    fallbacks = {}
    if fallback:
        for c in script:
            key = (c["kind"], shot_of(c["t"], shots))
            if key not in have:
                events.append(c)
                fallbacks.setdefault(f"{key[0]} in {key[1]}", []).append(c["t"])
    events.sort(key=lambda c: (c["t"], c["kind"]))
    silenced = [c for c in events if c["kind"] == "LABEL" and any(a <= c["t"] < b for a, b, _ in SILENT_TYPING)]
    events = [c for c in events if c not in silenced]
    dedup, last = [], {}
    for c in events:                                 # the same kind twice within 5 ms is one sound
        if c["kind"] in last and c["t"] - last[c["kind"]] < 0.005:
            continue
        last[c["kind"]] = c["t"]
        dedup.append(c)
    # cross-check score-fixed kinds against the cue file
    score_times = {"PIANO": [c[0] for c in CHORDS], "MOTIF": []}
    for c in script_cues():
        score_times.setdefault(c["kind"], []).append(c["t"])
    mismatched, unknown, dropped = [], [], []
    for c in other:
        if c["kind"] in DROPPED_KINDS:
            dropped.append({"t": c["t"], "kind": c["kind"], "why": DROPPED_KINDS[c["kind"]]})
        elif c["kind"] in SCORE_KINDS:
            ts = score_times.get(c["kind"], [])
            if not ts or min(abs(c["t"] - x) for x in ts) > 0.02:
                mismatched.append({"t": c["t"], "kind": c["kind"]})
        else:
            unknown.append({"t": c["t"], "kind": c["kind"]})
    exp, got = {}, {}
    for c in script:
        k = f"{c['kind']} in {shot_of(c['t'], shots)}"
        exp[k] = exp.get(k, 0) + 1
    for c in dedup:
        k = f"{c['kind']} in {shot_of(c['t'], shots)}"
        got[k] = got.get(k, 0) + 1
    info = {"format": fmt, "cue_file_cues_used": sum(1 for c in dedup if c["source"] == "cue file"),
            "counts_differing_from_script": {k: {"script": exp.get(k, 0), "rendered": got.get(k, 0)}
                                             for k in sorted(set(exp) | set(got)) if exp.get(k, 0) != got.get(k, 0)},
            "label_cues_dropped_in_silent_typing": [c["t"] for c in silenced],
            "fallbacks": fallbacks, "score_cues_not_matching_score": mismatched, "unknown_kinds_ignored": unknown,
            "score_cues_dropped_by_director": dropped, "retimed": retimed,
            "counts": {k: sum(1 for c in dedup if c["kind"] == k) for k in sorted(EVENT_KINDS)}}
    return dedup, info


def level_for(c: dict, shots) -> float:
    """Reference peak level (dBFS) of an event cue: palette + mix trim + the cue's own offset + any CUE_TRIM_DB."""
    trim = sum(d for (k, tt), d in CUE_TRIM_DB.items() if k == c["kind"] and abs(c["t"] - tt) < 0.05)
    return _level(c, shots) + trim


def _level(c: dict, shots) -> float:
    kind, t = c["kind"], c["t"]
    base = PALETTE[kind][0] + MIX_TRIM_DB.get(kind, 0.0)
    # A cue's level offset is in dB under any of these names. The engine writes `gain` in dB (FILM.cue(9.6, 'LUB',
    # {gain: -6}), as SCRIPT §5.3's "LUB (−6 dB)"): reading it as a linear factor silenced LUB 9.6, DUB 9.9, LUB 21.6
    # and LUB 24.0 (-120 dB) in the first render from the engine's cue file.
    rel = c.get("db", c.get("gain_db", c.get("gain")))
    if rel is not None:
        if not -40.0 <= float(rel) <= 12.0:
            raise ValueError(f"cue level offset out of range (dB expected): {c}")
        return base + float(rel)
    if kind in ("LUB", "DUB"):
        for tt, d in LUB_OVERRIDES.items():
            if abs(t - tt) < 0.05:
                return base + d
        return base + (-6.0 if shot_of(t, shots) in CLOSE_UPS else 0.0)
    if kind == "LABEL" and 53.4 <= t < 57.0:          # the boot's DORYLINAE letters: -38 dBFS
        return base - 2.0
    return base


def place_events(events, piano, buses, shots) -> list[dict]:
    placed = []
    motif = [c for c in events if c["kind"] == "MOTIF"]
    plinks = [c for c in events if c["kind"] == "PLINK"]
    label_groups, grp = [], []
    for c in [c for c in events if c["kind"] == "LABEL"]:
        if grp and c["t"] - grp[-1]["t"] > 0.25:
            label_groups.append(grp)
            grp = []
        grp.append(c)
    if grp:
        label_groups.append(grp)
    label_pan = {}
    for g in label_groups:                           # pan a typed word gently left to right
        width = 0.6 if g[0]["t"] >= 53.4 else 0.16
        for i, c in enumerate(g):
            label_pan[id(c)] = -width / 2 + width * (i / max(1, len(g) - 1))
    # music first, so the effects can be held under it (§5.3 mix rule: effects 6 to 12 dB under music peaks):
    # an effect is trimmed to 6 dB under the dry music's peak within +/- 1 beat when its palette level is higher.
    order = {"music": 0, "heartbeat": 1, "fx": 2}
    for c in sorted(events, key=lambda c: (order[PALETTE[c["kind"]][1]], c["t"])):
        kind, t = c["kind"], c["t"]
        if not (0 <= t < DURATION):
            continue
        pk = palette_pk = level_for(c, shots)
        if PALETTE[kind][1] == "fx":
            i = int(round(t * SR))
            w = slice(max(0, i - int(BEAT * SR)), i + int(BEAT * SR))
            loc = np.abs(buses["music"].x[w] + buses["piano"].x[w]).max()      # the dry music, piano bus included
            if loc > 0:
                pk = min(pk, 20 * math.log10(loc) - 6.0)
        ctx = {"distant": kind in ("LUB", "DUB") and 20.4 <= t < 28.8}
        if kind == "MOTIF":
            i = motif.index(c)
            ctx["motif_note"] = MOTIF_ORDER[min(i, 2)]
            ctx["motif_until"] = 52.65 if t >= 40.8 else 99
        if kind == "PLINK":
            ctx["plink_note"] = ["B3", "D4", "F#4"][min(plinks.index(c), 2)]
        stem, send = PALETTE[kind][1], PALETTE[kind][2]
        if ctx["distant"]:
            send = 0.25
        pan = float(c.get("pan", label_pan.get(id(c), 0.0)))
        if kind == "PLINK" and "pan" not in c:
            pan = [-0.12, 0.0, 0.12][min(plinks.index(c), 2)]
        parts = make_event(kind, c, piano, pk, ctx)
        bus = buses["label" if kind == "LABEL" else "piano" if kind in PIANO_KINDS else stem]
        for j, (clip, _) in enumerate(parts):
            bus.add(clip, t, pan=pan, send=send)
        note = c.get("note") or ctx.get("motif_note") or ctx.get("plink_note")
        rec = {"kind": kind, "t_cue": t, "t_placed": round(int(round(t * SR)) / SR, 6), "sample": int(round(t * SR)),
               "source": c["source"], "level_dbfs_ref": round(pk, 2), "pan": round(pan, 3), "stem": stem}
        if pk < palette_pk - 0.05:
            rec["trimmed_by_mix_rule_db"] = round(pk - palette_pk, 2)
        if kind in ("MOTIF", "PLINK", "LOW", "GLASS", "DYAD"):
            rec["note"] = note if kind != "DYAD" else ["A4", "D5"]
            if kind == "GLASS":
                rec["note"] = c.get("note") or "E6"
        if "fmt" in c:
            rec["fmt"] = c["fmt"]
        if ctx["distant"]:
            rec["distant"] = True
        placed.append(rec)
    return sorted(placed, key=lambda r: (r["t_cue"], r["kind"]))


# ------------------------------------------------------------------------------------------------ space + master
def make_ir(length=2.6, rt60=(1.7, 1.35, 0.6), predelay=0.016) -> np.ndarray:
    """A warm small hall generated here (no third-party IR): decorrelated noise in three bands with their own
    decay, sparse early reflections, unit energy."""
    rng = rng_for("IR")
    n = int(length * SR)
    t = np.arange(n) / SR
    ir = np.zeros((n, 2))
    bands = [butter(2, 400, "low", fs=SR, output="sos"), butter(2, [400, 4000], "band", fs=SR, output="sos"),
             butter(2, 4000, "high", fs=SR, output="sos")]
    for ch in range(2):
        nz = rng.standard_normal(n)
        for sos, rt in zip(bands, rt60):
            ir[:, ch] += sosfilt(sos, nz) * np.exp(-6.91 * t / rt)
        for d, g in ((0.006, 0.45), (0.011, 0.32), (0.019, 0.25), (0.029, 0.18), (0.041, 0.12)):
            ir[int((d + 0.0017 * ch) * SR), ch] += g * (1 if rng.random() > 0.5 else -1) * 4
    ir *= np.minimum(1, t / 0.003)[:, None]
    ir[-int(0.2 * SR):] *= ramp_out(int(0.2 * SR))[:, None]
    ir = np.concatenate([np.zeros((int(predelay * SR), 2)), ir])
    return ir / np.sqrt((ir ** 2).sum() / 2)


def reverb(send: np.ndarray, ir: np.ndarray) -> np.ndarray:
    wet = np.stack([fftconvolve(send[:, c], ir[:, c])[: len(send)] for c in range(2)], 1)
    # the wet return is high-passed at 300 Hz: the hall adds air and length, not low-mid build-up (its tail of the
    # piano's low strings otherwise added 3 points of 15-26 Hz AM in the 150-200 Hz band)
    return filt(wet, np.vstack([butter(2, 300, "high", fs=SR, output="sos"), butter(2, 6500, "low", fs=SR, output="sos")]))


def limiter_gain(x: np.ndarray, ceiling_db: float, lookahead_ms=5.0, release_ms=150.0) -> np.ndarray:
    """tools/audio-poc/master.py:limit's structure (4x true-peak detection, look-ahead minimum, instant down /
    exponential release), returning the gain curve instead of the audio, with one change: the attack is a Hann
    window (length L) over the +/-L minimum, with no min() afterwards. Every value inside the window is <= the
    need at the peak, so the ceiling still holds, and the curve has no single-sample steps (the original's final
    min() reintroduced a 0.4 dB step at 27.002 s in our test)."""
    c = db(ceiling_db)
    need = np.minimum(1.0, c / np.maximum(true_peak(x), 1e-12))
    if need.min() >= 1.0:
        return np.ones(len(x))
    L = max(2, int(lookahead_ms * SR / 1000))
    w = np.hanning(L + 2)[1:-1]
    g = convolve1d(minimum_filter1d(need, size=2 * L + 1, mode="nearest"), w / w.sum(), mode="nearest")
    assert np.all(g <= need + 1e-12), "limiter attack would exceed the ceiling"
    rel = math.exp(-1.0 / (release_ms * SR / 1000))  # recovery is walked until within 1e-5 of unity
    out = np.ones_like(g)
    active = np.flatnonzero(g < 1.0)
    cur, i, n = 1.0, int(active[0]), len(g)
    while i < n:                                     # only walk where gain reduction exists or is recovering
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
    """Contiguous stretches of gain reduction > 0.1 dB: start time and maximum reduction."""
    on = g < db(-0.1)
    edges = np.flatnonzero(np.diff(np.concatenate([[0], on.astype(np.int8), [0]])))
    return [{"t": round(a / SR, 3), "dur_ms": round((b - a) / SR * 1000), "max_gr_db": round(float(-20 * np.log10(g[a:b].min())), 2)}
            for a, b in zip(edges[::2], edges[1::2])]


def ffmpeg_measure(path: Path) -> dict:
    """EBU R128 via ffmpeg: ebur128 (I, LRA, true peak) and loudnorm's first pass (input_i/tp/lra)."""
    r = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-filter_complex",
                        "ebur128=peak=true:framelog=quiet", "-f", "null", "-"], capture_output=True, text=True)
    s = r.stderr[r.stderr.rfind("Summary:"):]
    grab = lambda pat: float(re.search(pat, s, re.S).group(1))  # noqa: E731
    out = {"I_lufs": grab(r"I:\s+(-?[\d.]+) LUFS"), "LRA_lu": grab(r"LRA:\s+(-?[\d.]+) LU"),
           "true_peak_dbtp": grab(r"True peak:.*?Peak:\s+(-?[\d.inf]+) dBFS")}
    r = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-af",
                        "loudnorm=I=-14:TP=-1:LRA=11:print_format=json", "-f", "null", "-"], capture_output=True, text=True)
    j = json.loads(r.stderr[r.stderr.rfind("{"): r.stderr.rfind("}") + 1])
    out["loudnorm_pass1"] = {k: j[k] for k in ("input_i", "input_tp", "input_lra", "input_thresh", "target_offset")}
    return out


# ------------------------------------------------------------------------------------------------ QA
def qa(mix: np.ndarray, stems: dict, placed: list, score_log: list, gain_db: float) -> dict:
    out = {}
    out["dc_offset"] = {k: [float(f"{v:.2e}") for v in s.mean(0)] for k, s in {"mix": mix, **stems}.items()}
    a = mix[np.abs(mix).sum(1) > 0]
    out["stereo"] = {"lr_correlation": round(float(np.corrcoef(a[:, 0], a[:, 1])[0, 1]), 3),
                     "mono_fold_down_db": round(20 * math.log10(np.sqrt(np.mean((a.sum(1) / 2) ** 2)) / np.sqrt(np.mean(a ** 2))), 2)}
    # silences must be exact zeros
    sil = {}
    for a, b in ((52.8, 53.4), (53.4, 53.55), (62.4, 63.0)):   # 53.4-53.55: nothing may return before the ripple
        seg = mix[int(a * SR): int(b * SR)]
        sil[f"{a}-{b}"] = {"max_abs": float(np.abs(seg).max()), "exact_zero": bool(np.all(seg == 0))}
    out["silences"] = sil
    # spectrum: band powers of the whole mix, relative to the 250 Hz - 1 kHz band
    X = np.abs(np.fft.rfft(mix.mean(1))) ** 2
    f = np.fft.rfftfreq(len(mix), 1 / SR)
    bands = [(20, 40), (40, 60), (60, 120), (120, 250), (250, 1000), (1000, 2000), (2000, 5000), (5000, 10000),
             (10000, 16000), (16000, 24000)]
    pw = {f"{a}-{b}": float(X[(f >= a) & (f < b)].sum()) for a, b in bands}
    ref = pw["250-1000"]
    out["spectrum_band_db_re_250_1k"] = {k: round(10 * math.log10(v / ref + 1e-30), 1) for k, v in pw.items()}
    tot = sum(pw.values())
    out["spectrum_share_pct"] = {k: round(100 * v / tot, 3) for k, v in pw.items()}
    # clicks: 1 ms frames of energy above 12 kHz. A discontinuity is broadband, so it shows up here even under
    # loud music; band-limited sounds do not. Frames within 40 ms after a placed onset are 'at an intended onset'.
    hp = butter(8, 12000, "high", fs=SR, output="sos")
    onsets = sorted({p["sample"] // 48 for p in placed} |
                    {int(round(o * SR)) // 48 for e in score_log for o in (e.get("onsets") or [e["t_placed"]])})
    near = np.zeros(len(mix) // 48 + 1, bool)
    for o in onsets:
        near[o: o + 40] = True
    clicks = {}
    for k, s in {"mix": mix, **stems}.items():
        h = filt(s.mean(1), hp)
        fr = np.sqrt(np.maximum(uniform_filter1d(h ** 2, 48)[::48], 0.0))
        rel = 20 * np.log10(fr / (np.abs(mix).max() + 1e-12) + 1e-15)      # re the mix peak
        bad = np.flatnonzero((rel > -70) & ~near[: len(rel)])
        clicks[k] = {"max_hf_frame_db_re_mix_peak": round(float(rel.max()), 1),
                     "max_hf_frame_at_t": round(float(rel.argmax()) / 1000, 3),
                     "frames_above_-70db_at_intended_onsets": int(((rel > -70) & near[: len(rel)]).sum()),
                     "frames_above_-70db_elsewhere": int(len(bad)), "elsewhere_times": [round(b / 1000, 3) for b in bad[:10]]}
    out["hf_click_scan_12k"] = clicks
    # aliasing / air: energy above 16 kHz relative to the whole mix
    out["energy_above_16k_db_re_total"] = round(10 * math.log10(pw["16000-24000"] / tot + 1e-30), 1)
    # effects under the music (§5.3 mix rule: 6 to 12 dB under music peaks). Each effect's own peak is its placed
    # reference level plus the master gain (not a window of the fx stem, so neighbours do not mask each other);
    # it is compared with the music stem's peak over the whole film (global) and within +/- 1 bar (local).
    mus = stems["music"]
    music_peak = 20 * math.log10(np.abs(mus).max())
    rows = []
    for p in placed:
        if p["stem"] not in ("fx", "heartbeat"):
            continue
        own = p["level_dbfs_ref"] + gain_db
        i = p["sample"]
        loc = np.abs(mus[max(0, i - int(BAR * SR)): i + int(BAR * SR)]).max()
        rows.append({"kind": p["kind"], "t": p["t_cue"], "peak_dbfs": round(own, 1),
                     "db_under_music_peak_global": round(music_peak - own, 1),
                     "db_under_music_peak_local": round(20 * math.log10(loc) - own, 1) if loc > 0 else None})
    per_kind = {}
    for r in rows:
        k = per_kind.setdefault(r["kind"], {"global": [], "local": []})
        k["global"].append(r["db_under_music_peak_global"])
        if r["db_under_music_peak_local"] is not None:
            k["local"].append(r["db_under_music_peak_local"])
    out["effects_under_music"] = {
        "music_peak_dbfs": round(music_peak, 2),
        "per_kind": {k: {"global_db": min(v["global"]), "local_min_db": min(v["local"]) if v["local"] else None,
                         "local_median_db": round(float(np.median(v["local"])), 1) if v["local"] else None}
                     for k, v in per_kind.items()},
        "local_below_6db": [f"{r['kind']}@{r['t']} ({r['db_under_music_peak_local']} dB)" for r in rows
                            if r["db_under_music_peak_local"] is not None and r["db_under_music_peak_local"] < 6],
        "no_music_within_1_bar": [f"{r['kind']}@{r['t']}" for r in rows if r["db_under_music_peak_local"] is None]}
    return out


def section_table(mix: np.ndarray) -> list[dict]:
    rows = []
    for name, a, b in SECTIONS:
        seg = mix[int(a * SR): int(b * SR)]
        pk = float(np.abs(seg).max())
        row = {"section": name, "start": a, "end": b, "rms_dbfs": rms_db(seg), "peak_dbfs": todb(pk)}
        if b - a >= 3.0 and pk > 0:
            try:
                row["lufs_ungated_window"] = round(lufs(seg, SR), 1)
            except Exception:                        # noqa: BLE001
                pass
        rows.append(row)
    return rows


# ------------------------------------------------------------------------------------------------ main
def render(args):
    t_start = time.time()
    doc = json.loads(Path(args.cues).read_text())
    fmt = args.format or (doc.get("format") if doc.get("format") in ("16x9", "4x5") else "16x9")
    dur = float(doc.get("duration") or DURATION)
    if abs(dur - DURATION) > 1e-6:
        print(f"warning: cue file duration {dur} differs from the script's {DURATION}; rendering {DURATION}", file=sys.stderr)
    n = int(DURATION * SR)
    shots = [(s["id"], s["start"], s["end"]) for s in doc.get("shots", [])] or SHOTS
    events, cue_info = resolve_cues(doc, fmt, not args.no_fallback)
    accents = [{"t": t, "kind": "THOCK", "pan": 0.0, "source": f"score: hard-cut accent {cut}"} for t, cut in CUT_ACCENTS
               if not any(c["kind"] == "THOCK" and abs(c["t"] - t) < 0.05 for c in events)]
    events = sorted(events + accents, key=lambda c: (c["t"], c["kind"]))
    cue_info["hard_cut_accents"] = [c["t"] for c in accents]

    piano = FeltPiano(Path(args.samples))
    # "label" holds the LABEL taps: part of the fx stem, but kept out of the master gain so that one master serves
    # both formats (the formats differ only in S5's label ticks), making music/heartbeat/room bit-identical.
    # "piano" is the felt-piano bus: it is folded into the music stem through its EQ before the reverb (fold_piano).
    buses = {k: Bus(k, n) for k in ("music", "piano", "heartbeat", "fx", "room", "label")}
    room_info = room_layer(n, buses["room"])
    pad_info = pad_layer(n, buses["music"])
    piano_layer(piano, buses["piano"])
    boot_layer(piano, buses["music"], buses["piano"])
    placed = place_events(events, piano, buses, shots)
    piano_info = fold_piano(buses)

    # space: one generated hall, per-stem sends, a common return level set by loudness against the dry music
    ir = make_ir()
    ret = db(-12.0) * 10 ** ((lufs(buses["music"].x, SR) - lufs(reverb(buses["music"].send, ir), SR)) / 20)  # music wet at -12 LU
    stems = {}
    hp = butter(2, 25, "high", fs=SR, output="sos")    # no DC, controlled sub (the heartbeat's D1 is 36.7 Hz)
    # Two halves around the silence, each with its own reverb and mask, so that nothing from before 52.8 (a 2.6 s
    # reverb tail, filter ringing) can come back when the boot starts at 53.4. Nothing is scheduled in between.
    cut = int(SILENCE_END * SR)
    before = (np.arange(n) < cut)[:, None]
    masks = (automation(MASK_BEFORE, n)[:, None], automation(MASK_AFTER, n)[:, None])
    for k, b in buses.items():
        s = 0
        for half, m in zip((before, ~before), masks):
            w = reverb(b.send * half, ir) * ret if np.abs(b.send * half).max() > 0 else 0
            s = s + filt(b.x * half + w, hp) * m
        stems[k] = s
    stems["music"] = stems["music"] * automation(RELAY_STOP_DUCK, n)[:, None]     # the relay stop at 20.4
    labels = stems.pop("label")
    ref_mix = sum(stems.values())                     # format-independent: everything but the label taps

    # master: static gain G + true-peak limiter, iterated so the limited mix integrates to the target
    target, ceiling = args.lufs, args.ceiling
    G = db(target - lufs(ref_mix, SR))
    for _ in range(6):
        g = limiter_gain(ref_mix * G, ceiling)
        I = lufs(ref_mix * G * g[:, None], SR)
        if abs(I - target) < 0.01:
            break
        G *= db(target - I)
    gain = (G * g)[:, None]
    stems["fx"] = stems["fx"] + labels
    final = {k: s * gain for k, s in stems.items()}
    mix = sum(final.values())
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    sf.write(out, mix, SR, subtype="PCM_24")
    # final static trim from ffmpeg's own first-pass measurement (two-pass linear loudnorm, without resampling)
    meas = ffmpeg_measure(out)
    trim_db = target - float(meas["loudnorm_pass1"]["input_i"])
    if abs(trim_db) >= 0.05 and float(meas["loudnorm_pass1"]["input_tp"]) + trim_db <= -1.0:
        final = {k: s * db(trim_db) for k, s in final.items()}
        mix = sum(final.values())
        sf.write(out, mix, SR, subtype="PCM_24")
        meas = ffmpeg_measure(out)
    else:
        trim_db = 0.0
    tag = fmt
    stem_paths = {}
    for k, s in final.items():
        p = out.parent / f"stem-{k}-{tag}.wav"
        sf.write(p, s, SR, subtype="PCM_24")
        stem_paths[k] = str(p)
    mix_q = sf.read(out, dtype="float64")[0]
    gr = -20 * np.log10(g.min())

    master_db = 20 * math.log10(G) + trim_db
    rep = {
        "file": str(out), "format": fmt, "cue_file": str(Path(args.cues).resolve()),
        "cue_file_md5": hashlib.md5(Path(args.cues).read_bytes()).hexdigest(), "sample_rate": SR, "bits": 24,
        "duration_s": DURATION, "md5": hashlib.md5(out.read_bytes()).hexdigest(),
        "loudness": {"integrated_lufs_ffmpeg": meas["I_lufs"], "lra_lu_ffmpeg": meas["LRA_lu"],
                     "true_peak_dbtp_ffmpeg": meas["true_peak_dbtp"], "integrated_lufs_pyloudnorm": round(lufs(mix_q, SR), 2),
                     "true_peak_dbtp_4x_python": todb(float(true_peak(mix_q).max())),
                     "sample_peak_dbfs": todb(float(np.abs(mix_q).max())), "loudnorm_pass1": meas["loudnorm_pass1"],
                     "loudnorm_would_be_linear": float(meas["loudnorm_pass1"]["input_tp"]) <= -1.0},
        "master": {"reference_mix_lufs": round(lufs(ref_mix, SR), 2), "static_gain_db": round(master_db, 2),
                   "limiter_ceiling_dbtp": ceiling, "limiter_max_gain_reduction_db": round(float(gr), 2),
                   "limiter_time_in_reduction_s": round(float((g < 0.999).sum()) / SR, 3),
                   "limiter_events": limiter_events(g), "ffmpeg_trim_db": round(trim_db, 3),
                   "reverb_return_db": round(20 * math.log10(ret), 2),
                   "palette_note": "palette levels (SCRIPT §5.1) are the reference mix; final level = reference + static_gain_db"},
        "sections": section_table(mix_q),
        "stems": {k: {"path": stem_paths[k], "peak_dbfs": todb(float(np.abs(s).max())), "rms_dbfs": rms_db(s)} for k, s in final.items()},
        "cues": cue_info, "events_placed": placed,
        "score_events": [e for b in buses.values() for e in b.log],
        "layers": {**pad_info, **room_info, **piano_info,
                   "relay_stop_duck": {"points_t_db": [(t, round(20 * math.log10(v), 2)) for t, v in RELAY_STOP_DUCK],
                                       "applies_to": "music stem (dry + reverb), before the master"}},
        "piano_tuning": piano.p.report(),
        "onset_ramps_ms": {"piano (all felt-piano voices)": 5.0, "Dadd9 at 3.5": "15 per hammer + 120 swell (CHORD_ONSET)",
                           "PAD": "1200 (3.5 s), 600 (28.8 s)", "ROOM": 40,
                           "LUB/DUB sub": 3.0, "LUB/DUB glass tick (transient)": 0.6, "THUMP": 2.0, "THOCK": 1.5,
                           "TICK": 1.5, "LAND (transient)": 0.4, "PASTE (transient)": 0.3, "LABEL (transient)": 0.25,
                           "GLASS": 3.0, "RIPPLE": 2.5, "BLOOM": "2 + 1200 attack",
                           "every clip end": "raised-cosine to exactly 0 (>= 1 ms)"},
        "qa": qa(mix_q, final, placed, [e for b in buses.values() for e in b.log], master_db),
        "render_seconds": round(time.time() - t_start, 1), "seed": SEED,
    }
    rp = Path(args.report) if args.report else out.parent / "report.json"
    allrep = json.loads(rp.read_text()) if rp.exists() else {}
    allrep[fmt] = rep
    if "16x9" in allrep and "4x5" in allrep:
        allrep["formats_differ"] = allrep["16x9"]["md5"] != allrep["4x5"]["md5"]
    np_safe = lambda o: o.item() if hasattr(o, "item") else str(o)  # noqa: E731
    rp.write_text(json.dumps(allrep, indent=1, default=np_safe))
    print(json.dumps({"out": str(out), "md5": rep["md5"][:12], **rep["loudness"], "limiter_gr_db": rep["master"]["limiter_max_gain_reduction_db"],
                      "fallbacks": list(cue_info["fallbacks"]), "seconds": rep["render_seconds"]}, indent=None, default=str))


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--cues", help="cue JSON {cues:[{t, kind, ...}], duration, format}")
    ap.add_argument("--out", help="mix WAV path (stems and report.json are written next to it)")
    ap.add_argument("--format", choices=["16x9", "4x5"], help="format to render (default: the cue file's)")
    ap.add_argument("--report", help="report JSON path (default: <out dir>/report.json)")
    ap.add_argument("--samples", default=str(SAMPLES))
    ap.add_argument("--lufs", type=float, default=-14.0)
    ap.add_argument("--ceiling", type=float, default=-1.5, help="limiter true-peak ceiling (dBTP)")
    ap.add_argument("--no-fallback", action="store_true", help="never fill missing event cues from SCRIPT §5.3")
    ap.add_argument("--emit-script-cues", metavar="PATH", help="write SCRIPT §5.3 as a cue file and exit")
    a = ap.parse_args()
    if a.emit_script_cues:
        doc = {"cues": script_cues(), "duration": DURATION, "format": "all", "formats": ["16x9", "4x5"],
               "shots": [{"id": s, "start": x, "end": y} for s, x, y in SHOTS],
               "source": "design/SCRIPT.md §5.3 (cues tagged fmt apply to that format only), less the cues the director cut: "
                         + "; ".join(DROPPED_KINDS.values())}
        Path(a.emit_script_cues).write_text(json.dumps(doc, indent=1))
        print(f"wrote {a.emit_script_cues}: {len(doc['cues'])} cues")
        return
    if not (a.cues and a.out):
        ap.error("--cues and --out are required (or --emit-script-cues)")
    render(a)


if __name__ == "__main__":
    main()
