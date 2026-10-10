"""Pictures for PALETTE.md (run with the plotting venv: ../.venv-plot/bin/python -I plot.py).

  out/palette-demo-qa.png       mix waveform (dBFS) + events, log-frequency spectrogram, momentary loudness per stem
  ../research/figs/tuning.png   measured f0 deviation from 12-TET per sample, per candidate (before correction)
  ../research/figs/survey.png   SNR, spectral centroid, layers for every candidate
  ../research/figs/ir.png       generated IRs: energy decay per octave band, late L/R coherence vs frequency
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
import soundfile as sf  # noqa: E402
from scipy.signal import butter, sosfiltfilt, stft  # noqa: E402

HERE = Path(__file__).resolve().parent
OUT = HERE / "out"
RES = HERE.parent / "research"
FIG = RES / "figs"
FIG.mkdir(parents=True, exist_ok=True)
plt.rcParams.update({"font.size": 8, "axes.titlesize": 9, "figure.dpi": 130})
COL = {"piano": "#7a4fd6", "grand": "#3f2a8c", "vibes": "#d9822b", "chimes": "#c2185b", "celesta": "#00897b",
       "bassdrum": "#455a64", "sub": "#90a4ae", "pad": "#8d6e63", "bowed": "#5d9cec", "cellos": "#2e7d32",
       "violas": "#66bb6a"}


def demo_png():
    rep = json.loads((OUT / "demo-events.json").read_text())
    x, sr = sf.read(OUT / "palette-demo.wav", always_2d=True)
    m = x.mean(1)
    t = np.arange(len(m)) / sr
    fig, ax = plt.subplots(3, 1, figsize=(13, 8.5), sharex=True, gridspec_kw={"height_ratios": [1.1, 2.2, 1.6]})
    # waveform envelope in dBFS (max over 10 ms)
    w = int(0.01 * sr)
    k = len(m) // w
    pk = np.abs(x[: k * w]).max(1).reshape(k, w).max(1)
    ax[0].plot(np.arange(k) * w / sr, 20 * np.log10(pk + 1e-9), lw=0.7, color="#333")
    ax[0].set_ylim(-70, 0)
    ax[0].set_ylabel("peak dBFS")
    seen = set()
    for e in rep["events"]:
        if e["inst"] in ("sub",):
            continue
        c = COL.get(e["inst"], "k")
        ax[0].axvline(e["t"], color=c, lw=0.6, alpha=0.7, label=None if e["inst"] in seen else e["inst"])
        seen.add(e["inst"])
    ax[0].legend(ncol=11, fontsize=6.5, loc="lower center", frameon=False)
    ax[0].set_title(f"palette demo: 84 BPM, D major, {rep['lufs']:.1f} LUFS, limiter GR {rep['limiter_max_gr_db']:.2f} dB "
                    f"(events coloured by instrument; dotted = bar lines)")
    # spectrogram (log frequency)
    f, tt, Z = stft(m, fs=sr, nperseg=4096, noverlap=4096 - 256)
    P = 20 * np.log10(np.abs(Z) + 1e-10)
    vmax = P.max()
    ax[1].pcolormesh(tt, f, P, shading="auto", vmin=vmax - 90, vmax=vmax, cmap="magma", rasterized=True)
    ax[1].set_yscale("log")
    ax[1].set_ylim(30, 16000)
    ax[1].set_ylabel("Hz")
    # momentary loudness per stem
    for name in ["piano", "bed", "bowed", "strings", "mallets", "low", "rev_chamber", "rev_hall"]:
        p = OUT / "stems" / f"{name}.wav"
        if not p.exists():
            continue
        y, _ = sf.read(p, always_2d=True)
        win, hop = int(0.4 * sr), int(0.05 * sr)
        ms = [np.mean(y[i: i + win] ** 2) for i in range(0, len(y) - win, hop)]
        L = 10 * np.log10(np.array(ms) * 2 + 1e-12) - 0.691
        ax[2].plot(np.arange(len(L)) * hop / sr + 0.2, L, lw=1.0, label=name,
                   ls="--" if name.startswith("rev") else "-")
    ax[2].set_ylim(-60, -5)
    ax[2].set_ylabel("stem level, 400 ms (dB, unweighted)")
    ax[2].legend(ncol=8, fontsize=7, loc="lower center", frameon=False)
    ax[2].set_xlabel("s")
    for a in ax:
        for b in range(1, 7):
            a.axvline((b - 1) * rep["bar"], color="#999", lw=0.6, ls=":")
    ax[2].set_xlim(0, rep["dur"])
    fig.tight_layout()
    fig.savefig(OUT / "palette-demo-qa.png")
    plt.close(fig)


def tuning_png():
    ids = [("osiris_b", "Osiris (mic B)"), ("steinwayB", "Steinway B (v1)"), ("kawai", "Kawai"),
           ("knight", "Knight upright"), ("yamaha_up", "Yamaha upright"), ("uprightKW", "Upright KW"),
           ("vibes_soft", "vibraphone soft"), ("handchimes", "hand chimes"), ("celesta", "celesta"),
           ("glock", "glockenspiel"), ("bowedvibes", "bowed vibes"), ("vc_sec", "cello section")]
    fig, axs = plt.subplots(3, 4, figsize=(13, 7.5), sharey=True)
    for a, (cid, name) in zip(axs.flat, ids):
        d = json.loads((RES / "measurements" / f"{cid}.json").read_text())
        xs, ys, cs = [], [], []
        for r in d["samples"]:
            tt = r.get("tuning") or {}
            if "cents" not in tt:
                continue
            xs.append(tt["target_midi"])
            ys.append(tt["cents"])
            cs.append("#c62828" if tt.get("label_offset_semitones") else "#333")
        a.scatter(xs, ys, s=6, c=cs)
        a.axhline(0, color="#999", lw=0.6)
        a.set_ylim(-60, 60)
        a.set_title(f"{name}  (n={len(xs)})")
        a.set_xlabel("sounding MIDI")
    axs[0, 0].set_ylabel("cents vs 12-TET")
    axs[1, 0].set_ylabel("cents vs 12-TET")
    axs[2, 0].set_ylabel("cents vs 12-TET")
    fig.suptitle("Measured pitch of every sample before correction (red: file labelled with the wrong note; "
                 "the sampler corrects all of these)")
    fig.tight_layout()
    fig.savefig(FIG / "tuning.png")
    plt.close(fig)


def survey_png():
    d = json.loads((RES / "measurements" / "summary.json").read_text())
    ids = [k for k in d if d[k].get("kind") in ("piano", "sustain", "mallet", "drum")]
    fig, ax = plt.subplots(1, 3, figsize=(13, 4.8))
    y = np.arange(len(ids))
    snr = [d[k].get("snr_db_median") or 0 for k in ids]
    ax[0].barh(y, snr, color=["#7a4fd6" if d[k]["kind"] == "piano" else "#999" for k in ids])
    ax[0].set_yticks(y, ids)
    ax[0].set_xlabel("median SNR (dB, max 50 ms RMS vs 5th-percentile floor)")
    ax[0].invert_yaxis()
    cen = [d[k].get("centroid_hz_median") or 0 for k in ids]
    ax[1].barh(y, cen, color="#d9822b")
    ax[1].set_yticks(y, [])
    ax[1].set_xscale("log")
    ax[1].set_xlabel("median spectral centroid, first 400 ms (Hz)")
    ax[1].invert_yaxis()
    corr = [d[k].get("lr_corr_median") or 0 for k in ids]
    lo = [(d[k].get("lr_corr_p10_p90") or [0, 0])[0] for k in ids]
    ax[2].barh(y, corr, color="#00897b")
    ax[2].scatter(lo, y, color="#c62828", s=10, zorder=3, label="10th percentile")
    ax[2].axvline(0, color="#333", lw=0.6)
    ax[2].set_yticks(y, [])
    ax[2].set_xlabel("L/R correlation (median; red: p10)")
    ax[2].legend(frameon=False, fontsize=7)
    ax[2].invert_yaxis()
    fig.suptitle("Candidate survey (all CC0)")
    fig.tight_layout()
    fig.savefig(FIG / "survey.png")
    plt.close(fig)


def ir_png():
    fig, ax = plt.subplots(1, 3, figsize=(13, 3.8))
    for i, name in enumerate(["chamber", "hall"]):
        ir, sr = sf.read(OUT / f"ir-{name}.wav", always_2d=True)
        for fc in [125, 500, 2000, 8000]:
            lo, hi = fc / np.sqrt(2), min(fc * np.sqrt(2), sr / 2 * 0.98)
            b = sosfiltfilt(butter(4, [lo, hi], "band", fs=sr, output="sos"), ir, axis=0)
            e = np.cumsum((b ** 2).sum(1)[::-1])[::-1]
            ax[i].plot(np.arange(len(e)) / sr, 10 * np.log10(e / e[0] + 1e-12), label=f"{fc} Hz", lw=1)
        ax[i].set_ylim(-70, 2)
        ax[i].set_title(f"IR '{name}': energy decay (Schroeder) per octave")
        ax[i].set_xlabel("s")
        ax[i].legend(frameon=False)
    rv = json.loads((RES / "reverb-ir.json").read_text())
    for name in ["chamber", "hall"]:
        bands = rv[name]["bands"]
        fs = [int(k) for k in bands]
        ax[2].semilogx(fs, [bands[k]["late_coherence"] for k in bands], "o-", label=name)
    ax[2].set_title("late L/R coherence (designed: high in the bass, ~0 above 2 kHz)")
    ax[2].set_xlabel("octave band (Hz)")
    ax[2].legend(frameon=False)
    fig.tight_layout()
    fig.savefig(FIG / "ir.png")
    plt.close(fig)


if __name__ == "__main__":
    what = sys.argv[1:] or ["demo", "tuning", "survey", "ir"]
    for w in what:
        {"demo": demo_png, "tuning": tuning_png, "survey": survey_png, "ir": ir_png}[w]()
        print("ok", w)
