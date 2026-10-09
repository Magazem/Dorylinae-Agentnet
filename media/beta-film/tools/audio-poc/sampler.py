"""A small, deterministic multisample player for the VCSL (CC0) Steinway B samples.

Why not FluidSynth/SFZ players: we want sample-accurate placement, per-note gain/pan/filter,
and the same bytes out on every run. A sampler this simple is 80 lines of numpy.

Samples: VCSL "Grand Piano, Steinway B" / Sus / JHPiano_Sus_Close_<note>_vl<2..4>_rr1.wav
         44.1 kHz, 24-bit stereo, sustain pedal down, peak-normalised per file (NORMALIZED.txt),
         so velocity is re-applied here as gain. Pitches every 2 semitones (C D E F# G# A#).
"""
from __future__ import annotations

import re
from fractions import Fraction
from functools import lru_cache
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.signal import resample_poly

NOTE = {"C": 0, "C#": 1, "D": 2, "D#": 3, "E": 4, "F": 5, "F#": 6, "G": 7, "G#": 8, "A": 9, "A#": 10, "B": 11}


def midi(name: str) -> int:
    m = re.fullmatch(r"([A-G]#?)(-?\d)", name)
    return 12 * (int(m.group(2)) + 1) + NOTE[m.group(1)]


class Piano:
    # Gain per velocity layer, since the files are normalised. vl2 is the softest recorded layer.
    LAYER_DB = {2: -14.0, 3: -8.0, 4: -3.0}

    def __init__(self, root: Path, sr: int = 48000):
        self.sr = sr
        self.files: dict[tuple[int, int], Path] = {}
        for f in sorted(Path(root).glob("JHPiano_Sus_Close_*_vl*_rr1.wav")):
            m = re.search(r"Close_([A-G]#?-?\d)_vl(\d)_", f.name)
            self.files[(midi(m.group(1)), int(m.group(2)))] = f
        if not self.files:
            raise FileNotFoundError(f"no Steinway B samples in {root}")
        self.pitches = sorted({k[0] for k in self.files})

    @lru_cache(maxsize=256)
    def _load(self, key: tuple[int, int], semis: int) -> np.ndarray:
        x, sr0 = sf.read(self.files[key], dtype="float64", always_2d=True)
        # Pitch shift by resampling (changes length, like a tape/sampler), then convert 44.1k -> 48k.
        # up/down = (sr_out / sr_in) / 2^(semis/12): fewer output samples = higher pitch.
        ratio = (Fraction(self.sr, sr0) / Fraction(2 ** (semis / 12)).limit_denominator(4000)).limit_denominator(4000)
        y = resample_poly(x, ratio.numerator, ratio.denominator, axis=0)
        return y

    def note(self, pitch: int, velocity: float, dur: float | None = None, release: float = 0.35) -> np.ndarray:
        """velocity 0..1 picks a layer and a gain. dur=None lets the sustained sample ring out."""
        layer = 2 if velocity < 0.45 else 3 if velocity < 0.75 else 4
        base = min(self.pitches, key=lambda p: (abs(p - pitch), p))
        if (base, layer) not in self.files:
            layer = min((l for (p, l) in self.files if p == base), key=lambda l: abs(l - layer))
        y = self._load((base, layer), pitch - base).copy()
        g = 10 ** ((self.LAYER_DB[layer] + 12 * np.log2(max(velocity, 0.05) / 0.6) * 0.5) / 20)
        y *= g
        y[:32] *= np.linspace(0, 1, 32)[:, None]                         # de-click the first sample
        if dur is not None:
            n_on, n_rel = int(dur * self.sr), int(release * self.sr)
            y = y[: n_on + n_rel]
            if len(y) > n_on:
                y[n_on:] *= np.exp(-np.linspace(0, 7, len(y) - n_on))[:, None]
        return y
