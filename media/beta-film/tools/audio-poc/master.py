"""Mastering helpers: loudness-based stem staging and a transparent look-ahead true-peak limiter.

pedalboard.Limiter is JUCE's dsp::Limiter: two compressor stages (the first at -10 dB) plus make-up gain
that maps the threshold to 0 dBFS. It audibly flattens a soft score (LRA fell to 1.4 LU in our test),
so the final peak control is done here instead.
"""
from __future__ import annotations

import numpy as np
import pyloudnorm as pyln
from scipy.ndimage import minimum_filter1d, uniform_filter1d
from scipy.signal import resample_poly


def lufs(x: np.ndarray, sr: int) -> float:
    return pyln.Meter(sr).integrated_loudness(x)


def stage(x: np.ndarray, sr: int, target_lufs: float) -> np.ndarray:
    """Scale a stem so its integrated loudness hits target_lufs (mixing by numbers, then by ear)."""
    l = lufs(x, sr)
    return x * 10 ** ((target_lufs - l) / 20) if np.isfinite(l) else x


def true_peak(x: np.ndarray, os: int = 4) -> np.ndarray:
    """Per-sample peak of the 4x oversampled signal (what a true-peak meter sees), max over channels."""
    up = np.abs(resample_poly(x, os, 1, axis=0)).max(axis=1)
    return up[: len(x) * os].reshape(len(x), os).max(axis=1)


def limit(x: np.ndarray, sr: int, ceiling_db: float = -1.2, lookahead_ms: float = 5.0, release_ms: float = 150.0) -> tuple[np.ndarray, float]:
    """Look-ahead brickwall on true peak. Returns (audio, max gain reduction in dB)."""
    c = 10 ** (ceiling_db / 20)
    need = np.minimum(1.0, c / np.maximum(true_peak(x), 1e-12))
    L = max(1, int(lookahead_ms * sr / 1000))
    g = minimum_filter1d(need, size=2 * L + 1, mode="nearest")            # reach the floor L samples early
    g = uniform_filter1d(g, size=L, mode="nearest")                        # smooth attack ramp
    g = np.minimum(g, minimum_filter1d(need, size=2 * L + 1, mode="nearest"))
    rel = np.exp(-1.0 / (release_ms * sr / 1000))
    out = np.empty_like(g); cur = 1.0
    for i, v in enumerate(g):                                               # instant down, exponential release up
        cur = v if v < cur else v + (cur - v) * rel
        out[i] = cur
    y = x * out[:, None]
    return np.clip(y, -1.0, 1.0), float(-20 * np.log10(out.min()))
