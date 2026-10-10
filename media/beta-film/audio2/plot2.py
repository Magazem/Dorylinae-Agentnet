"""Pictures of a score2 render (run with the plotting venv: .venv-plot/bin/python -I plot2.py out/report-16x9.json).

  out/score2-<fmt>-overview.png   the score as notes (instrument rows), the mix envelope with sections and cues,
                                  a log-frequency spectrogram, momentary loudness per stem group
  out/score2-<fmt>-zoom.png       five moments (entrance + seal, breakdown + return, answer + card, invitation,
                                  silence + boot + address): spectrogram with the cue times and the measured attacks
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
from scipy.signal import sosfilt, stft  # noqa: E402

HERE = Path(__file__).resolve().parent
BEAT = 5 / 7
plt.rcParams.update({"font.size": 7.5, "axes.titlesize": 8.5, "figure.dpi": 120})
COL = {"piano": "#6a3fd0", "grand": "#2b1a7a", "vibes": "#d9822b", "chimes": "#c2185b", "celesta": "#00897b",
       "heartbeat": "#455a64", "bowed": "#3d8ee0", "cellos": "#2e7d32", "violas": "#7cb342", "fx": "#9e9e9e",
       "pad": "#8d6e63"}
GROUPS = {"piano": ["piano"], "mallets": ["mallets"], "bed (pad+bowed+strings)": ["bed", "bowed", "strings"],
          "heartbeat": ["low"], "accents": ["fx"], "room": ["room"], "reverb": ["rev_chamber", "rev_hall"]}
GCOL = ["#6a3fd0", "#d9822b", "#8d6e63", "#455a64", "#9e9e9e", "#bdbdbd", "#e57373"]


def kweight(x, sr):
    b1, a1 = [1.53512485958697, -2.69169618940638, 1.19839281085285], [1.0, -1.69065929318241, 0.73248077421585]
    b2, a2 = [1.0, -2.0, 1.0], [1.0, -1.99004745483398, 0.99007225036621]
    return sosfilt(np.array([b1 + a1, b2 + a2]), x, axis=0)


def momentary(x, sr, win=0.4, hop=0.05):
    p = np.sum(kweight(x, sr) ** 2, axis=1)
    w, h = int(win * sr), int(hop * sr)
    c = np.concatenate([[0.0], np.cumsum(p)])
    s = np.arange(0, len(p) - w, h)
    return (s + w / 2) / sr, -0.691 + 10 * np.log10((c[s + w] - c[s]) / w + 1e-20)


def spec(ax, m, sr, t0=0.0, nper=4096, hop=256, vmin_rel=90, fmin=30):
    f, tt, Z = stft(m, fs=sr, nperseg=nper, noverlap=nper - hop)
    P = 20 * np.log10(np.abs(Z) + 1e-10)
    vmax = P.max()
    sel = f >= fmin
    ax.pcolormesh(tt + t0, f[sel], P[sel], shading="auto", vmin=vmax - vmin_rel, vmax=vmax, cmap="magma", rasterized=True)
    ax.set_yscale("log")
    ax.set_ylim(fmin, 16000)
    ax.set_ylabel("Hz")


def main():
    rp = Path(sys.argv[1])
    rep = json.loads(rp.read_text())
    fmt = rep["format"]
    out_dir = rp.parent
    x, sr = sf.read(HERE / rep["file"], always_2d=True)
    stems = {k: sf.read(HERE / v["path"], always_2d=True)[0] for k, v in rep["stems"].items()}
    m = x.mean(1)
    dur = len(m) / sr
    ev = rep["events"]
    cues = rep["qa"]["onsets_per_cue"]
    secs = rep["qa"]["sections"]

    # ------------------------------------------------------------------ overview
    fig, ax = plt.subplots(4, 1, figsize=(16, 13), sharex=True, gridspec_kw={"height_ratios": [2.3, 1.0, 2.2, 1.5]})
    rows = ["piano", "grand", "vibes", "chimes", "celesta", "bowed", "cellos", "violas", "heartbeat", "fx"]
    for e in ev:
        if e["inst"] not in rows:
            continue
        y = e.get("midi")
        if e["inst"] == "heartbeat":
            y = 26 if e.get("kind") == "LUB" else 33
        if e["inst"] == "fx":
            y = 105
        if y is None:
            continue
        x1 = e.get("off", e["t"] + 0.25)
        if e["inst"] in ("vibes", "chimes", "celesta", "heartbeat", "fx"):
            x1 = e["t"] + 0.25
        ax[0].plot([e["t"], x1], [y, y], color=COL[e["inst"]], lw=2.2 if e["inst"] in ("piano", "grand") else 1.6,
                   solid_capstyle="butt", alpha=0.85)
    for tone in rep["info"]["pad_tones"]:
        from_, to = tone["from"], tone["to"]
        n = tone["note"]
        names = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"]
        midi = 12 * (int(n[-1]) + 1) + names.index(n[:-1])
        ax[0].plot([from_, to], [midi - 0.35, midi - 0.35], color=COL["pad"], lw=1.0, alpha=0.6)
    ax[0].set_ylim(22, 108)
    ax[0].set_yticks([26, 33, 38, 50, 62, 74, 86, 98, 105])
    ax[0].set_yticklabels(["D1 lub", "A1 dub", "D2", "D3", "D4", "D5", "D6", "D7", "accents"])
    handles = [plt.Line2D([], [], color=c, lw=2) for c in COL.values()]
    ax[0].legend(handles, list(COL), ncol=11, fontsize=6.5, loc="upper left", frameon=False)
    ax[0].set_title(f"score2 {fmt}: the score as rendered (notes from the event log; pad lines thin brown)")
    for g in range(0, 91, 4):
        for a in ax:
            a.axvline(g * BEAT, color="#cccccc" if g % 4 == 0 else "#eeeeee", lw=0.5, zorder=0)
    # envelope
    w = int(0.01 * sr)
    k = len(m) // w
    pk = np.abs(x[: k * w]).max(1).reshape(k, w).max(1)
    ax[1].plot(np.arange(k) * w / sr, 20 * np.log10(pk + 1e-9), lw=0.6, color="#333")
    ax[1].set_ylim(-80, 0)
    ax[1].set_ylabel("peak dBFS")
    for s in secs:
        ax[1].axvspan(s["from"], s["to"], color="#f3e5f5" if secs.index(s) % 2 else "#e8f5e9", alpha=0.5, zorder=0)
        ax[1].text(s["from"] + 0.1, -76, s["section"], fontsize=6, rotation=0)
    for c in cues:
        if c["kind"] != "PIC":
            ax[1].axvline(c["t"], color="#c2185b" if c["kind"] in ("SEAL", "BACK", "ARRIVE") else "#90a4ae", lw=0.5, alpha=0.8)
    L = rep["loudness"]
    ax[1].set_title(f"mix: {L['integrated_lufs_ffmpeg']} LUFS, LRA {L['lra_lu_ffmpeg']} LU, true peak {L['true_peak_dbtp_ffmpeg']} dBTP "
                    f"(sections shaded; sound cues as lines)")
    spec(ax[2], m, sr)
    ax[2].set_title("spectrogram (mid channel, 85 ms window)")
    for i, (name, ks) in enumerate(GROUPS.items()):
        s = sum(stems[k] for k in ks if k in stems)
        if np.abs(s).max() == 0:
            continue
        tt, Lm = momentary(s, sr)
        ax[3].plot(tt, Lm, lw=0.9, color=GCOL[i], label=name)
    tt, Lm = momentary(x, sr)
    ax[3].plot(tt, Lm, lw=1.2, color="k", label="mix")
    ax[3].axhline(-14, color="k", lw=0.5, ls=":")
    ax[3].set_ylim(-70, -4)
    ax[3].set_ylabel("momentary LUFS (400 ms)")
    ax[3].legend(ncol=8, fontsize=6.5, loc="lower center", frameon=False)
    ax[3].set_xlim(0, dur)
    ax[3].set_xlabel("s (grid: bars, 84 BPM)")
    fig.tight_layout()
    fig.savefig(out_dir / f"score2-{fmt}-overview.png")
    plt.close(fig)

    # ------------------------------------------------------------------ zooms
    wins = [("entrance, brief lines, the seal, the counter", 5.0, 15.8), ("breakdown and return", 21.8, 31.8),
            ("theo works, the answer, the card", 31.2, 44.0), ("the invitation", 46.0, 56.6),
            ("silence, boot, address", 55.6, 64.29)]
    fig, ax = plt.subplots(len(wins), 1, figsize=(16, 4.0 * len(wins)))
    for a, (title, t0, t1) in zip(ax, wins):
        i0, i1 = int(t0 * sr), int(t1 * sr)
        spec(a, m[i0:i1], sr, t0=t0, nper=2048, hop=128, vmin_rel=85)
        a.set_xlim(t0, t1)
        for c in cues:
            if t0 <= c["t"] <= t1:
                col = "#ffffff" if c["kind"] == "PIC" else "#4fc3f7"
                a.axvline(c["t"], color=col, lw=0.6, alpha=0.55, ls=":" if c["kind"] == "PIC" else "-")
                a.text(c["t"], 17500, (c["kind"] if c["kind"] != "PIC" else "") + " " + c["desc"][:24], rotation=90,
                       fontsize=5.2, va="top", ha="right", color="k")
        for e in ev:
            if "attack_measured" in e and t0 <= e["attack_measured"] <= t1 and e["inst"] != "fx":
                a.plot([e["attack_measured"]], [36], marker="^", ms=3.5, color=COL.get(e["inst"], "w"))
        a.set_title(f"{title} ({t0:.2f}-{t1:.2f} s): cues (blue = sound, white dotted = picture), measured attacks (triangles)")
    fig.tight_layout()
    fig.savefig(out_dir / f"score2-{fmt}-zoom.png")
    plt.close(fig)
    print("wrote", out_dir / f"score2-{fmt}-overview.png", out_dir / f"score2-{fmt}-zoom.png")


if __name__ == "__main__":
    main()
