"""Designed (not sampled) parts of the palette: the bed pad and the heartbeat sub. numpy only, deterministic.

pad():  additive, harmonically locked partials per voice (3 voices per note, +/- detune cents, each with its own
        slow pitch drift of ~1 cent from smoothed seeded noise), a moving Gaussian spectral envelope (the "filter"
        opens from fc0 to fc1 over `open_s`, the lift), per-partial slow shimmer, raised-cosine attack and release.
        Band-limited by construction: no partial above 9 kHz, so no aliasing; exact 12-TET.
sub():  the heartbeat: sine that falls from +drop semitones onto the root in ~40 ms (a felt-beater pitch fall),
        3 ms attack, ~160 ms decay, a little tanh for 2nd/3rd harmonics so it reads on small speakers.
"""
from __future__ import annotations

import numpy as np

SR = 48000


def midi_hz(m: float) -> float:
    return 440.0 * 2 ** ((m - 69) / 12)


def smooth_noise(n: int, sr: int, rate_hz: float, rng: np.random.Generator) -> np.ndarray:
    """Band-limited random curve (values ~ N(0,1)), cosine-interpolated control points at rate_hz."""
    k = int(np.ceil(n / sr * rate_hz)) + 3
    pts = rng.standard_normal(k)
    x = np.arange(n) / sr * rate_hz
    i = np.floor(x).astype(int)
    f = x - i
    w = 0.5 - 0.5 * np.cos(np.pi * f)
    return pts[i] * (1 - w) + pts[i + 1] * w


def pad(midis, dur: float, *, sr: int = SR, attack: float = 1.6, release: float = 2.2, fc0: float = 700.0,
        fc1: float | None = None, open_s: float = 0.0, open_at: float = 0.0, tilt: float = 1.1, voices: int = 3,
        detune: float = 5.0, drift_c: float = 1.2, shimmer: float = 0.12, width: float = 0.7, seed: int = 0,
        level_db: float = 0.0, fmax: float = 9000.0) -> np.ndarray:
    n = int((dur + release) * sr)
    t = np.arange(n) / sr
    rng = np.random.default_rng(seed)
    out = np.zeros((n, 2))
    # spectral envelope cutoff over time (the lift opens it)
    fc = np.full(n, fc0, float)
    if fc1 is not None and open_s > 0:
        r = np.clip((t - open_at) / open_s, 0, 1)
        fc = fc0 * (fc1 / fc0) ** (0.5 - 0.5 * np.cos(np.pi * r))
    env = np.ones(n)
    na = max(1, int(attack * sr))
    env[:na] = 0.5 - 0.5 * np.cos(np.linspace(0, np.pi, na))
    nd = int(dur * sr)
    nr = n - nd
    env[nd:] = 0.5 + 0.5 * np.cos(np.linspace(0, np.pi, nr))
    env[-1] = 0.0
    pans = np.linspace(-width, width, voices) if voices > 1 else np.array([0.0])
    for m in midis:
        f0 = midi_hz(m)
        H = int(min(fmax, sr / 2 - 1000) // f0)
        for v in range(voices):
            dc = (v - (voices - 1) / 2) / max(1, (voices - 1) / 2) * detune
            cents = dc + drift_c * smooth_noise(n, sr, 0.15, rng)
            ph1 = 2 * np.pi * np.cumsum(f0 * 2 ** (cents / 1200)) / sr
            sig = np.zeros(n)
            for h in range(1, H + 1):
                fh = h * f0
                a = h ** (-tilt) * np.exp(-((fh / fc) ** 2))
                if np.max(a) < 1e-4:
                    continue
                a = a * (1 + shimmer * smooth_noise(n, sr, 0.25, rng))
                sig += a * np.sin(h * ph1 + rng.uniform(0, 2 * np.pi))
            th = (pans[v] + 1) * np.pi / 4
            out[:, 0] += sig * np.cos(th)
            out[:, 1] += sig * np.sin(th)
    out *= env[:, None]
    out /= max(1e-9, np.sqrt(np.mean(out[na: nd] ** 2)) if nd > na else np.abs(out).max())
    return out * 10 ** ((level_db - 20) / 20)               # -20 dBFS RMS at level_db = 0


def sub(midi: float, *, sr: int = SR, drop: float = 7.0, fall_s: float = 0.018, decay_s: float = 0.16,
        dur: float = 0.6, drive: float = 1.6, level_db: float = -12.0) -> np.ndarray:
    n = int(dur * sr)
    t = np.arange(n) / sr
    f = midi_hz(midi) * 2 ** (drop / 12 * np.exp(-t / fall_s))
    ph = 2 * np.pi * np.cumsum(f) / sr
    env = (1 - np.exp(-t / 0.003)) * np.exp(-t / decay_s)
    y = np.sin(ph) * env
    y = 0.7 * y + 0.3 * np.tanh(drive * y) / np.tanh(drive)
    f_ = int(0.03 * sr)
    y[-f_:] *= 0.5 + 0.5 * np.cos(np.linspace(0, np.pi, f_))
    y[0] = 0.0
    y[-1] = 0.0
    y = y / np.abs(y).max() * 10 ** (level_db / 20)
    return np.stack([y, y], 1)
