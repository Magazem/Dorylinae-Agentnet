"""sampler2: a small, deterministic multisample player for the v2 score (numpy/scipy only).

What it does that v1's 80-line sampler did not:
  * any library layout: a folder, a glob and a filename regex with named groups note / layer / rr
  * velocity layers in their real order (pp < mf < f, soft < medium < loud, L < H, 1 < 2 ...), hard switching
    (no crossfades between unrelated takes: they comb-filter); the level follows one continuous curve per note
    anchored at each layer's measured level (vel_curve), so loudness is monotonic in velocity across layer switches;
    normalised libraries get their layer levels from a table, natural libraries keep the recorded levels
  * round robins: cycled per (sampled note, layer) in render order, so repeated notes do not repeat one take
  * tuning: every sample's f0 is measured once (analysis.measure_tuning: YIN octave, the library's octave convention
    forced on outliers, frame-wise fundamental partial or an inharmonic partial fit, A4 = 440 Hz) and cached; the
    note is resampled by (target - root - cents/100) semitones. tune="et": every note on 12-TET; tune="stretch":
    the instrument's smooth stretch curve kept with A4 on 440 Hz (pianos); tune="smooth": the whole curve kept.
    Mislabelled files (one octave or one semitone off) are mapped to the pitch they really sound.
  * stereo: keep / mono / left / best (the louder mic of a spaced pair: mono-safe on near-sine notes that sit
    close to anti-phase) / align (attack-aligned mid), optional width and pitch panning
  * timing: every sample's attack point is measured (struck sounds: the 1 ms envelope reaching peak - 15 dB, a
    perceptual-attack proxy that ignores the 8-20 ms of key/hammer noise Osiris has before the tone; sustained
    sounds: peak - 30 dB, the start of the bow), and the clip is cut so that this point sits exactly PREROLL (30 ms)
    after the clip start, keeping the pre-noise. place() puts the attack point on the cue time.
  * release: dur=None lets a pedal-down sample ring out; dur=x damps it with an exponential release (time from
    the library's sfz where it has one), and mixes the library's release sample (damper/key-up noise) at note-off,
    scaled by how much the note has decayed
  * sustain extension: sustained samples (strings, bowed vibes) are lengthened by equal-power crossfaded
    segments taken from their steady part, at seeded offsets, so a bed can hold any length without a loop click
  * resampling: one polyphase step (scipy resample_poly) with our own Kaiser FIR (beta 10, 96 taps per phase),
    rational ratio good to < 0.003 cent; passband flat to 18 kHz, images under -100 dB (measured in filter_report())
  * every clip starts and ends at exactly 0 (raised-cosine ramps)
"""
from __future__ import annotations

import hashlib
import json
import re
import sys
from dataclasses import dataclass, field
from fractions import Fraction
from functools import lru_cache
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.signal import butter, firwin, freqz, resample_poly, sosfilt
from scipy.signal.windows import blackmanharris

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from analysis import envelope_1ms, measure_tuning, name_to_midi  # noqa: E402

SR = 48000
PREROLL = 0.030
ALIGN_STRUCK_DB = -15.0
ALIGN_SUSTAIN_DB = -30.0
CACHE = HERE / "cache"
MEAS_VERSION = "t6"                     # bump to re-measure every sample
LAYER_ORDER = {"pp": 0, "p": 1, "mp": 2, "mf": 3, "f": 4, "ff": 5, "soft": 0, "medium": 1, "med": 1, "loud": 2,
               "hard": 1, "L": 0, "H": 1, "Slow": 0, "Fast": 1}


# ------------------------------------------------------------------------------------------- resampling
@lru_cache(maxsize=64)
def _fir(up: int, down: int) -> np.ndarray:
    m = max(up, down)
    half = 48 * m
    h = firwin(2 * half + 1, 0.90 / m, window=("kaiser", 10.0))
    return h                                                 # resample_poly multiplies by `up` itself


def ratio_for(sr_in: int, sr_out: int, semis: float) -> Fraction:
    r = Fraction(sr_out, sr_in) / Fraction(2 ** (semis / 12))
    return r.limit_denominator(1000)


def resample(x: np.ndarray, sr_in: int, sr_out: int, semis: float) -> np.ndarray:
    r = ratio_for(sr_in, sr_out, semis)
    up, down = r.numerator, r.denominator
    if up == down:
        return x.copy()
    return resample_poly(x, up, down, axis=0, window=_fir(up, down))


def filter_report(sr_in=44100, sr_out=SR, semis=0.37) -> dict:
    """Measured response of the FIR used for a typical ratio (passband ripple, image rejection)."""
    r = ratio_for(sr_in, sr_out, semis)
    up, down = r.numerator, r.denominator
    h = _fir(up, down)
    fs_up = sr_in * up                                       # rate of the zero-stuffed signal
    w, H = freqz(h, worN=1 << 18, fs=fs_up)
    mag = 20 * np.log10(np.abs(H) + 1e-15)
    out_nyq = min(sr_in, sr_out / (2 ** (-semis / 12))) / 2  # output Nyquist mapped back to input frequencies
    pb = (w > 20) & (w < 18000 * 2 ** (-semis / 12))
    sb = w > out_nyq * 1.0
    err_cents = 1200 * np.log2(float(r) / (sr_out / sr_in / 2 ** (semis / 12)))
    return {"up": up, "down": down, "taps": len(h), "passband_ripple_db": float(mag[pb].max() - mag[pb].min()),
            "passband_mean_db": float(mag[pb].mean()), "stopband_max_db": float(mag[sb].max()),
            "ratio_error_cents": float(err_cents)}


# ------------------------------------------------------------------------------------------- zones
@dataclass
class Zone:
    path: Path
    named: int                  # MIDI from the file name
    root: int                   # MIDI it really sounds (octave / label corrected)
    cents: float                # measured deviation of the fundamental from 12-TET at root
    layer: int
    rr: int
    sr: int
    onset: float                # s, physical onset in the file
    peak_db: float
    rms_db: float
    meas: dict = field(default_factory=dict)


def _quick_id(p: Path) -> str:
    st = p.stat()
    with open(p, "rb") as fh:
        head = fh.read(1 << 16)
    return hashlib.md5(head + str(st.st_size).encode()).hexdigest()[:16]


def prepare(x: np.ndarray, sr: int) -> np.ndarray:
    """What every file goes through before use: stereo, and a 0.5 ms fade-in when the file was cut inside the
    attack (first sample above -40 dB re peak). Onsets are measured on this, so placement matches playback."""
    if x.shape[1] == 1:
        x = np.repeat(x, 2, axis=1)
    x = x[:, :2].copy()
    if np.abs(x[0]).max() > np.abs(x).max() * 0.01:
        n = max(2, int(0.0005 * sr))
        x[:n] *= (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, n)))[:, None]
    return x


def align_mid(x: np.ndarray, sr: int, max_ms: float = 1.5) -> tuple[np.ndarray, float, float, float]:
    """Phase-aligned mid of a spaced stereo pair. The inter-channel delay is estimated on the broadband attack
    (5 ms before to 60 ms after the onset, high-passed at 800 Hz, |lag| <= max_ms), R is shifted onto L and the
    two are averaged. Returns (stereo array with mid on both channels, lag ms, corr before, corr after) where
    corr is the zero-lag L/R correlation over the first 1.5 s (a sine-like piano note on a spaced pair can sit
    near anti-phase: Osiris mic A E4 -0.87, i.e. -7 dB in mono)."""
    L, R = x[:, 0], x[:, 1]
    env = envelope_1ms(L + R, sr)
    on = int(np.argmax(env >= env.max() * 10 ** (-30 / 20)))
    a, b = max(0, on - int(0.005 * sr)), min(len(L), on + int(0.06 * sr))
    sos = butter(4, 800, "high", fs=sr, output="sos")
    l, r = sosfilt(sos, L[a:b]), sosfilt(sos, R[a:b])
    K = int(max_ms * sr / 1000)
    cc = [np.dot(l[max(0, -k): len(l) - max(0, k)], r[max(0, k): len(r) - max(0, -k)]) for k in range(-K, K + 1)]
    k = int(np.argmax(cc)) - K                        # R lags L by k samples
    Rs = np.zeros_like(R)
    if k >= 0:
        Rs[: len(R) - k] = R[k:]
    else:
        Rs[-k:] = R[: len(R) + k]
    seg = slice(on, min(len(L), on + int(1.5 * sr)))

    def corr(u, v):
        return float(np.dot(u[seg], v[seg]) / np.sqrt(np.dot(u[seg], u[seg]) * np.dot(v[seg], v[seg]) + 1e-30))
    mid = 0.5 * (L + Rs)
    return np.stack([mid, mid], 1), 1000 * k / sr, corr(L, R), corr(L, Rs)


def partial_pitch(clip: np.ndarray, midi: float, sr: int = SR, t0: float = 0.08, t1: float = 1.2, hmax: int = 6,
                  start: float | None = None) -> list[tuple[int, float, float]]:
    """Measured partials of a rendered note: [(h, cents of f_h against h x 12-TET(midi), level dB re the strongest
    of the partials found)], from t0 to t1 s after the attack point (`start` s into the clip; default PREROLL).
    Blackman-Harris window, 2^18-point FFT, the peak within +/-40 cents of h x f, parabolic interpolation on the
    log magnitude. This is the method of the score's tuning check, so the sampler and the report agree."""
    y = clip.mean(axis=1) if clip.ndim == 2 else clip
    s0 = PREROLL if start is None else start
    i0, i1 = int((s0 + t0) * sr), min(len(y), int((s0 + t1) * sr))
    seg = y[i0:i1]
    if len(seg) < int(0.15 * sr):
        return []
    nf = 1 << 18
    while nf < 2 * len(seg):
        nf <<= 1
    sp = np.abs(np.fft.rfft(seg * blackmanharris(len(seg)), nf))
    f = np.fft.rfftfreq(nf, 1 / sr)
    spdb = 20 * np.log10(sp + 1e-20)
    f0 = 440.0 * 2 ** ((midi - 69) / 12)
    out = []
    for h in range(1, hmax + 1):
        c = h * f0
        if c * 2 ** (40 / 1200) >= sr / 2:
            break
        sel = np.flatnonzero((f >= c * 2 ** (-40 / 1200)) & (f <= c * 2 ** (40 / 1200)))
        if len(sel) == 0:
            continue
        k = int(sel[np.argmax(sp[sel])])
        if k <= 0 or k >= len(sp) - 1:
            continue
        a, b, cc = spdb[k - 1], spdb[k], spdb[k + 1]
        den = a - 2 * b + cc
        d = 0.5 * (a - cc) / den if den != 0 else 0.0
        out.append((h, float(1200 * np.log2((k + d) * (f[1] - f[0]) / c)), float(b)))
    if out:
        mx = max(o[2] for o in out)
        out = [(h, c, lv - mx) for h, c, lv in out]
    return out


def pick_partial(parts: list[tuple[int, float, float]], prefer: int | None = None, floor_db: float = -30.0):
    """The partial that stands for the pitch: `prefer` when present above floor_db, else the fundamental when it is
    within floor_db of the strongest partial, else the lowest partial within 12 dB of the strongest."""
    if not parts:
        return None
    by = {h: (h, c, lv) for h, c, lv in parts}
    if prefer in by and by[prefer][2] > floor_db:
        return by[prefer]
    if 1 in by and by[1][2] > floor_db:
        return by[1]
    return next((p for p in parts if p[2] > -12.0), max(parts, key=lambda p: p[2]))


def best_channel(x: np.ndarray, sr: int) -> int:
    """The louder channel over the first 1.5 s after the onset (one mic of the pair: a single mic cannot cancel)."""
    env = envelope_1ms(x.mean(1), sr)
    on = int(np.argmax(env >= env.max() * 10 ** (-30 / 20)))
    seg = x[on: on + int(1.5 * sr)]
    return int(np.argmax(np.mean(seg ** 2, axis=0)))


def _measure_one(path: Path, named: int, short: bool, force_octave: int | None = None, chan: str = "mean",
                 align_db: float = ALIGN_STRUCK_DB) -> dict:
    x, sr = sf.read(str(path), dtype="float64", always_2d=True)
    x = prepare(x, sr)
    extra = {}
    if chan == "align":
        x, lag, c0, c1 = align_mid(x, sr)
        extra = {"align_lag_ms": lag, "corr_before": c0, "corr_after": c1}
    elif chan == "best":
        b = best_channel(x, sr)
        L, R = x[:, 0], x[:, 1]
        seg = slice(0, min(len(L), int(1.5 * sr)))
        c0 = float(np.dot(L[seg], R[seg]) / np.sqrt(np.dot(L[seg], L[seg]) * np.dot(R[seg], R[seg]) + 1e-30))
        e = np.mean(x[seg] ** 2, axis=0)
        extra = {"best_channel": b, "corr_lr": c0, "mono_sum_loss_db": float(10 * np.log10(
            np.mean((0.5 * (L[seg] + R[seg])) ** 2) / np.mean(e) + 1e-30)), "other_channel_db": float(
            10 * np.log10(e[1 - b] / e[b] + 1e-30))}
        x = np.repeat(x[:, b: b + 1], 2, axis=1)
    m = x[:, 0] if chan in ("left", "align", "best") else x.mean(axis=1)       # measure what will be played
    env = envelope_1ms(m, sr)
    pk = env.max()
    onset = int(np.argmax(env >= pk * 10 ** (align_db / 20)))
    o30 = int(np.argmax(env >= pk * 10 ** (-30 / 20)))
    t = measure_tuning(m, sr, o30, named, short=short, force_octave=force_octave)
    w = max(1, int(0.05 * sr))
    k = len(m) // w
    rms = np.sqrt(np.mean(m[: k * w].reshape(k, w) ** 2, axis=1)).max() if k else np.sqrt(np.mean(m ** 2))
    return {"sr": sr, "onset_s": onset / sr, "peak_db": float(20 * np.log10(np.abs(x).max() + 1e-12)),
            "rms_db": float(20 * np.log10(rms + 1e-12)), "tuning": t, **extra}


class Instrument:
    """Build from a folder of samples. Measurements are cached in palette/cache/<name>.json."""

    def __init__(self, name: str, folder: Path, glob: str, rx: str, *, short: bool = False,
                 layer_db: dict | None = None, vel_split: list[float] | None = None, tune: str = "et",
                 release: tuple | None = None, release_s: float | dict = 0.6, sustain: bool = False,
                 stereo: str = "keep", width: float = 1.0, max_shift: float = 7.0, sr: int = SR,
                 exclude: tuple = (), fix_label: bool = True, fix_octave: bool = True,
                 pitch_pan: tuple | None = None, stretch_floor: tuple | None = None,
                 tune_ref: "Instrument | None" = None, pitch_window: tuple | None = None,
                 level_ref: "Instrument | None" = None):
        """stretch_floor = (midi, cents): with tune="stretch", roots below `midi` aim no flatter than `cents`
        (the bass end of a fitted stretch curve can run flatter than the instrument should sound).
        tune_ref = another Instrument: every note is re-tuned to the pitch that instrument plays for the same
        (midi, velocity), measured on both rendered notes by partial_pitch (a doubling layer then cannot beat
        against its lead). The correction applied is kept per note in self.ref_corrections.
        pitch_window = (t0, t1) s after the attack: each zone's deviation from 12-TET is re-measured there by
        partial_pitch (the method of the score's tuning check) instead of the survey's whole-note frame median, so
        the pitch that is corrected is the pitch of the note's first second (Osiris B1 B2 G1: the frame median
        disagreed with it by 5 to 10 cents; pedal-down sympathetic strings pull the later frames).
        level_ref = another Instrument: gain_db is relative to that instrument's note at the same (midi, velocity)
        (first-second RMS of both, measured), so a doubling layer sits a known number of dB under its lead on every
        note (the Kawai's recorded levels put it 0 to 22 dB under Osiris depending on the note)."""
        self.name, self.sr, self.short, self.tune, self.sustain = name, sr, short, tune, sustain
        self.stretch_floor, self.tune_ref, self.pitch_window = stretch_floor, tune_ref, pitch_window
        self.level_ref = level_ref
        self._zone_cents_cache: dict = {}
        self.ref_corrections: dict = {}
        self._pitch_cache: dict = {}
        self.stereo, self.width, self.max_shift = stereo, width, max_shift
        self.release_s = release_s
        self._chan = {"left": "left", "align": "align", "best": "best"}.get(stereo, "mean")
        self.pitch_pan = pitch_pan
        self.align_db = ALIGN_SUSTAIN_DB if sustain else ALIGN_STRUCK_DB
        self.zones = self._scan(Path(folder), glob, rx, exclude, fix_label, fix_octave)
        if not self.zones:
            raise FileNotFoundError(f"{name}: no samples in {folder}")
        self.layers = sorted({z.layer for z in self.zones})
        self.vel_split = vel_split or [(i + 1) / len(self.layers) for i in range(len(self.layers) - 1)]
        self.layer_db = layer_db                               # None = keep the recorded levels
        # level of each layer: the table (normalised libraries: the file peak is replaced by it) or the median
        # max-50ms-RMS of the layer's files (natural libraries: note-to-note variation inside a layer is kept)
        self.layer_level = {l: (layer_db[l] if layer_db is not None else
                                float(np.median([z.rms_db for z in self.zones if z.layer == l])))
                            for l in self.layers}
        self.rel = None
        if release:
            rfolder, rglob, rrx = release
            self.rel = self._scan(Path(rfolder), rglob, rrx, (), fix_label, fix_octave, short=True,
                                  cache_name=name + "-rel")
        self._rr_counter: dict = {}
        self._smooth = self._fit_stretch() if tune in ("smooth", "stretch") else None

    # -------------------------------------------------------------------- scanning and measurement cache
    def _scan(self, folder, glob, rx, exclude, fix_label, fix_octave, short=None, cache_name=None):
        short = self.short if short is None else short
        cpath = CACHE / f"{cache_name or self.name}.json"
        cache = json.loads(cpath.read_text()) if cpath.exists() else {}
        rxc = re.compile(rx)
        raw = []
        for f in sorted(folder.glob(glob)):
            mm = rxc.search(f.name)
            if not mm or any(e in f.name for e in exclude):
                continue
            g = mm.groupdict()
            named = name_to_midi(g["note"])
            lab = g.get("layer")
            raw.append((f, named, lab, g.get("rr")))
        labs = sorted({r[2] for r in raw if r[2] is not None},
                      key=lambda s: LAYER_ORDER.get(s, int(re.sub(r"\D", "", s) or 0)))
        lidx = {l: i for i, l in enumerate(labs)}
        zones, changed = [], False
        keys = []
        for f, named, lab, rr in raw:
            key = f"{f.name}|{_quick_id(f)}|{MEAS_VERSION}|{int(short)}|{self._chan}|{self.align_db}"
            if key not in cache:
                cache[key] = _measure_one(f, named, short, chan=self._chan, align_db=self.align_db)
                changed = True
            keys.append(key)
        # a library names its octaves one way: files whose YIN octave disagrees with the library's mode are
        # re-measured with the mode forced (YIN can lock on the 2nd partial of a weak bass fundamental)
        octs = [cache[k]["tuning"].get("octave_offset") for k in keys if "octave_offset" in cache[k]["tuning"]]
        mode = max(set(octs), key=octs.count) if octs else 0
        for i, (f, named, lab, rr) in enumerate(raw):
            if cache[keys[i]]["tuning"].get("octave_offset", mode) != mode:
                k2 = keys[i] + f"|oct{mode}"
                if k2 not in cache:
                    cache[k2] = _measure_one(f, named, short, force_octave=mode, chan=self._chan,
                                             align_db=self.align_db)
                    changed = True
                keys[i] = k2
        for (f, named, lab, rr), key in zip(raw, keys):
            c = cache[key]
            t = c["tuning"]
            root = named
            if fix_octave and "octave_offset" in t:
                root += 12 * t["octave_offset"]
            if fix_label and t.get("label_offset_semitones"):
                root += t["label_offset_semitones"]
            zones.append(Zone(path=f, named=named, root=root, cents=float(t.get("cents", 0.0)),
                              layer=lidx.get(lab, 0), rr=int(rr) if rr and str(rr).isdigit() else 1, sr=c["sr"],
                              onset=c["onset_s"], peak_db=c["peak_db"], rms_db=c["rms_db"], meas=t))
        if changed:
            CACHE.mkdir(parents=True, exist_ok=True)
            cpath.write_text(json.dumps(cache, indent=0, default=float))
        return zones

    def _fit_stretch(self):
        """Smooth stretch curve (cubic in MIDI, robust: two passes dropping > 15 cent residuals)."""
        r = np.array([z.root for z in self.zones], float)
        c = np.array([z.cents for z in self.zones], float)
        if len(r) < 6:
            return None
        keep = np.ones(len(r), bool)
        for _ in range(2):
            p = np.polyfit(r[keep], c[keep], 3)
            keep = np.abs(c - np.polyval(p, r)) < 15
        return p

    def tuning_table(self) -> list[dict]:
        return [{"file": z.path.name, "named": z.named, "root": z.root, "layer": z.layer, "rr": z.rr,
                 "cents": round(z.cents, 2), "onset_ms": round(1000 * z.onset, 2)} for z in self.zones]

    # -------------------------------------------------------------------- selection
    def _note_levels(self, root: int) -> dict:
        """Median level of each layer for one sampled note (falls back to the layer's median where missing)."""
        out = {}
        for l in self.layers:
            v = [z.rms_db for z in self.zones if z.root == root and z.layer == l]
            out[l] = float(np.median(v)) if v else self.layer_level[l]
        return out

    def vel_curve(self, vel: float, levels: dict | None = None) -> float:
        """Target level (dB) for a velocity: anchors at each layer's velocity-range centre = that layer's level,
        12 dB under the softest layer at vel 0 and 3 dB over the loudest at vel 1, linear in between. The chosen
        layer gives the timbre; this curve gives the level, so loudness is monotonic in velocity."""
        edges = [0.0] + list(self.vel_split) + [1.0]
        cs = [0.5 * (edges[i] + edges[i + 1]) for i in range(len(self.layers))]
        lv = levels or self.layer_level
        ls = [lv[l] for l in self.layers]
        xs = [0.0] + cs + [1.0]
        ys = [ls[0] - 12.0] + ls + [ls[-1] + 3.0]
        return float(np.interp(min(max(vel, 0.0), 1.0), xs, ys))

    def layer_for(self, vel: float) -> int:
        i = 0
        for s in self.vel_split:
            if vel >= s:
                i += 1
        return self.layers[min(i, len(self.layers) - 1)]

    def _pick(self, midi: float, layer: int, zones: list[Zone], advance: bool = True) -> Zone:
        cand = [z for z in zones if z.layer == layer] or zones
        best = min(abs(z.root - midi) for z in cand)
        near = [z for z in cand if abs(z.root - midi) == best]
        roots = sorted({z.root for z in near})
        root = roots[0] if len(roots) == 1 else min(roots, key=lambda r: (r < midi, r))  # tie: shift down
        rrs = sorted([z for z in near if z.root == root], key=lambda z: (z.rr, z.path.name))
        k = (root, layer, id(zones))
        i = self._rr_counter.get(k, 0)
        if advance:
            self._rr_counter[k] = i + 1
        return rrs[i % len(rrs)]

    def reset_rr(self):
        self._rr_counter.clear()

    # -------------------------------------------------------------------- loading
    @lru_cache(maxsize=512)
    def _render_zone(self, path: str, sr_in: int, semis_q: int) -> np.ndarray:
        x, sr = sf.read(path, dtype="float64", always_2d=True)
        x = prepare(x, sr)
        if self.stereo == "align":
            x = align_mid(x, sr)[0]
        elif self.stereo == "best":
            b = best_channel(x, sr)
            x = np.repeat(x[:, b: b + 1], 2, axis=1)
        return resample(x, sr, self.sr, semis_q / 1e4)

    def target_cents(self, z: Zone) -> float:
        """Where a zone's notes are aimed, in cents from 12-TET (before any tune_ref correction)."""
        if self.tune == "none":
            return z.cents
        if self.tune == "smooth" and self._smooth is not None:        # keep the whole stretch curve
            return float(np.polyval(self._smooth, z.root))
        if self.tune == "stretch" and self._smooth is not None:       # keep the stretch shape, A4 exactly on 440
            t = float(np.polyval(self._smooth, z.root)) - float(np.polyval(self._smooth, 69))
            if self.stretch_floor and z.root < self.stretch_floor[0]:
                t = max(t, float(self.stretch_floor[1]))
            return t
        return 0.0

    def zone_cents(self, z: Zone) -> float:
        """The zone's measured deviation from 12-TET at its root: the survey's (z.cents), or with pitch_window the
        partial_pitch measurement of the zone as played (its channel, this sample rate)."""
        if self.pitch_window is None:
            return z.cents
        k = str(z.path)
        if k not in self._zone_cents_cache:
            y = self._render_zone(k, z.sr, 0)
            on = z.onset                                              # attack point (s): unshifted render
            t0, t1 = self.pitch_window
            p = pick_partial(partial_pitch(y, z.root, self.sr, t0, min(t1, len(y) / self.sr - on - 0.05), start=on))
            self._zone_cents_cache[k] = z.cents if p is None else p[1]
        return self._zone_cents_cache[k]

    def _shift_for(self, z: Zone, midi: float) -> float:
        return (midi - z.root) - (self.zone_cents(z) - self.target_cents(z)) / 100.0

    def _gain_db(self, z: Zone, vel: float) -> float:
        if self.layer_db is not None:
            return self.vel_curve(vel) - z.peak_db
        return self.vel_curve(vel, self._note_levels(z.root)) - z.rms_db   # this note's own layers anchor its curve

    def note_level_db(self, z: Zone, semis: float, vel: float) -> float:
        """RMS (dB, both channels) of zone z played at `vel` and `semis`, 80 ms to 1.2 s after the attack (cached)."""
        key = ("level", str(z.path), int(round(semis * 1e4)))
        if key not in self._pitch_cache:
            y = self._render_zone(str(z.path), z.sr, int(round(semis * 1e4)))
            on = z.onset * z.sr * ((self.sr / z.sr) / 2 ** (semis / 12)) / self.sr
            seg = y[int((on + 0.08) * self.sr): int((on + 1.2) * self.sr)]
            self._pitch_cache[key] = float(10 * np.log10(np.mean(seg ** 2) + 1e-30))
        return self._pitch_cache[key] + self._gain_db(z, vel)

    def zone_for(self, midi: float, vel: float, layer: int | None = None) -> Zone:
        """The zone note(midi, vel) would play next (round robins not advanced)."""
        return self._pick(midi, self.layer_for(vel) if layer is None else layer, self.zones, advance=False)

    def zone_pitch(self, z: Zone, semis: float, midi: float, prefer: int | None = None):
        """Measured (h, cents, level) of zone z rendered `semis` semitones away, as heard for `midi` (cached)."""
        key = (str(z.path), int(round(semis * 1e4)), round(float(midi), 4), prefer)
        if key not in self._pitch_cache:
            y = self._render_zone(str(z.path), z.sr, int(round(semis * 1e4)))
            speed = (self.sr / z.sr) / 2 ** (semis / 12)
            on = z.onset * z.sr * speed / self.sr                 # attack point in the rendered zone (s)
            self._pitch_cache[key] = pick_partial(partial_pitch(y, midi, self.sr, start=on), prefer)
        return self._pitch_cache[key]

    def pitch_of(self, midi: float, vel: float, prefer: int | None = None):
        """Measured pitch (h, cents, level) of the note this instrument plays for (midi, vel)."""
        z = self.zone_for(midi, vel)
        return self.zone_pitch(z, self._shift_for(z, midi), midi, prefer)

    # -------------------------------------------------------------------- one note
    def note(self, midi: float, vel: float, dur: float | None = None, *, release: float | None = None,
             attack: float = 0.0, offset: float = 0.0, layer: int | None = None, gain_db: float = 0.0,
             pan: float = 0.0, seed: int = 0, lpf: float | None = None, detune_c: float = 0.0) -> np.ndarray:
        """Stereo clip whose attack point is at PREROLL seconds. dur = note length (s) from the attack point.
        detune_c: cents added after all tuning (tests)."""
        lay = self.layer_for(vel) if layer is None else layer
        z = self._pick(midi, lay, self.zones)
        semis = self._shift_for(z, midi)
        if self.tune_ref is not None:                          # land on the lead's own measured pitch
            ref = self.tune_ref.pitch_of(midi, vel)
            own = self.zone_pitch(z, semis, midi, prefer=ref[0] if ref else None) if ref else None
            if ref and own and own[0] == ref[0]:
                corr = ref[1] - own[1]
                semis += corr / 100.0
                self.ref_corrections[(round(float(midi), 3), lay)] = {
                    "partial": ref[0], "ref_cents": round(ref[1], 2), "own_cents": round(own[1], 2),
                    "correction_cents": round(corr, 2), "zone": z.path.name}
        semis += detune_c / 100.0
        if abs(semis) > self.max_shift:
            raise ValueError(f"{self.name}: {midi} is {semis:.2f} semitones from the nearest sample")
        y = self._render_zone(str(z.path), z.sr, int(round(semis * 1e4))).copy()
        speed = (self.sr / z.sr) / 2 ** (semis / 12)            # samples out per sample in
        on = int(round(z.onset * z.sr * speed))
        pre = int(round(PREROLL * self.sr))
        if offset > 0:                                         # start later in the sample (e.g. after bow noise)
            on = on + int(offset * self.sr)
        if on >= pre:
            y = y[on - pre:]
            f = min(pre, int((0.02 if self.sustain else 0.002) * self.sr))   # fade where the file is cut
            y[:f] *= (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, f)))[:, None]
        else:
            y = np.vstack([np.zeros((pre - on, 2)), y])
        # level: one continuous velocity curve across the layers (no level jump at a layer switch), see vel_curve
        g_db = self._gain_db(z, vel)
        if self.level_ref is not None:                         # gain_db is then relative to the lead's note
            zr = self.level_ref.zone_for(midi, vel)
            g_db += (self.level_ref.note_level_db(zr, self.level_ref._shift_for(zr, midi), vel)
                     - self.note_level_db(z, semis, vel))
        g_db += gain_db
        y *= 10 ** (g_db / 20)
        n_end = len(y)
        # sustain extension
        if dur is not None and self.sustain:
            rel = release if release is not None else self._release_time(midi)
            need = pre + int((dur + rel) * self.sr) + 64
            if need > len(y):
                y = self._extend(y, need, seed=seed + int(midi * 100))
        if attack > 0:                                         # softer start: one ramp from the clip start
            n = min(len(y), pre + int(attack * self.sr))       # (pre-roll included, so no noise burst before it)
            y[:n] *= (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, n)))[:, None]
        if dur is not None:
            rel = release if release is not None else self._release_time(midi)
            n_off = pre + int(dur * self.sr)
            if n_off < len(y):
                env_off = float(np.sqrt(np.mean(y[max(0, n_off - 2400): n_off] ** 2)) + 1e-12)
                n_rel = max(64, int(rel * self.sr))
                tail = y[n_off: n_off + n_rel].copy()
                k = np.arange(len(tail)) / self.sr
                curve = np.exp(-6.91 * k / max(rel, 1e-3))      # -60 dB at `rel`
                curve *= 0.5 + 0.5 * np.cos(np.linspace(0, np.pi, len(tail)))   # and exactly 0 at the end
                y = np.vstack([y[:n_off], tail * curve[:, None]])
                if self.rel:
                    rz = self._pick(midi, lay, self.rel)
                    rs = float(midi - rz.root)                       # key-up noise: no measured-pitch correction
                    r = self._render_zone(str(rz.path), rz.sr, int(round(rs * 1e4))).copy()
                    if self.layer_db is not None:
                        r *= 10 ** ((self.layer_db.get(lay, 0.0) - rz.peak_db) / 20)
                    r *= 10 ** ((g_db - (self.layer_db.get(lay, 0.0) - z.peak_db if self.layer_db else 0.0)) / 20)
                    # scale by how far the note has decayed (rms at note-off vs the clip's max rms), sqrt law
                    pk = float(np.sqrt(np.max(np.convolve(np.mean(y[:n_off] ** 2, axis=1), np.ones(2400) / 2400,
                                                          mode="valid"))) + 1e-12) if n_off > 2400 else env_off
                    r *= np.sqrt(min(1.0, env_off / pk))
                    r[:32] *= np.linspace(0, 1, 32)[:, None]
                    r[-256:] *= np.linspace(1, 0, 256)[:, None]
                    end = n_off + len(r)
                    if end > len(y):
                        y = np.vstack([y, np.zeros((end - len(y), 2))])
                    y[n_off:end] += r
        # end at exactly zero
        f = min(len(y) // 4, int(0.05 * self.sr))
        if f > 1:
            y[-f:] *= (0.5 + 0.5 * np.cos(np.linspace(0, np.pi, f)))[:, None]
        y[-1] = 0.0
        if lpf:
            sos = butter(2, lpf, "low", fs=self.sr, output="sos")
            y = sosfilt(sos, y, axis=0)
        if not pan and self.pitch_pan:
            c, spread, lim = self.pitch_pan
            pan = float(np.clip((midi - c) / spread, -lim, lim))
        y = self._stereo(y, pan)
        return y

    def _release_time(self, midi: float) -> float:
        if isinstance(self.release_s, dict):
            ks = sorted(self.release_s)
            return float(np.interp(midi, ks, [self.release_s[k] for k in ks]))
        return float(self.release_s)

    def _extend(self, y: np.ndarray, need: int, seed: int) -> np.ndarray:
        """Lengthen with equal-power crossfaded segments from the steady part (uncorrelated takes)."""
        sr = self.sr
        m = np.mean(y ** 2, axis=1)
        w = int(0.05 * sr)
        k = len(m) // w
        rms = np.sqrt(m[: k * w].reshape(k, w).mean(axis=1))
        pk = rms.max()
        steady = np.where(rms > pk * 10 ** (-6 / 20))[0]
        a = int(steady[0] * w + 0.3 * sr) if len(steady) else len(y) // 3
        b = int(steady[-1] * w) if len(steady) else int(len(y) * 0.9)
        b = min(b, len(y) - 1)
        if b - a < int(1.5 * sr):
            a, b = len(y) // 3, int(len(y) * 0.9)
        cf = int(min(0.8 * sr, (b - a) / 3))
        seg = max(cf * 2, min(int(2.5 * sr), b - a))
        rng = np.random.default_rng(seed & 0xFFFFFFFF)
        out = y[: b].copy()
        fade_out = np.cos(np.linspace(0, np.pi / 2, cf))[:, None]
        fade_in = np.sin(np.linspace(0, np.pi / 2, cf))[:, None]
        while len(out) < need:
            s0 = int(rng.integers(a, max(a + 1, b - seg)))
            piece = y[s0: s0 + seg].copy()
            # match level of the piece to the end of what we have
            l_now = np.sqrt(np.mean(out[-cf:] ** 2)) + 1e-12
            l_new = np.sqrt(np.mean(piece[:cf] ** 2)) + 1e-12
            piece *= l_now / l_new
            head = out[-cf:] * fade_out + piece[:cf] * fade_in
            out = np.vstack([out[:-cf], head, piece[cf:]])
        return out[:need]

    def _stereo(self, y: np.ndarray, pan: float) -> np.ndarray:
        if self.stereo == "mono":
            mm = y.mean(axis=1, keepdims=True)
            y = np.repeat(mm, 2, axis=1)
        elif self.stereo in ("left", "align", "best"):
            y = np.repeat(y[:, :1], 2, axis=1)
        elif self.width != 1.0:
            M = (y[:, 0] + y[:, 1]) / 2
            S = (y[:, 0] - y[:, 1]) / 2 * self.width
            y = np.stack([M + S, M - S], axis=1)
        if pan:
            th = (pan + 1) * np.pi / 4
            y = y * np.array([np.cos(th), np.sin(th)]) * np.sqrt(2)
        return y


def place(buf: np.ndarray, clip: np.ndarray, t: float, sr: int = SR) -> None:
    """Add clip so that its attack point (PREROLL into the clip) lands on t seconds."""
    i = int(round(t * sr)) - int(round(PREROLL * sr))
    j0 = max(0, -i)
    i0 = max(0, i)
    n = min(len(clip) - j0, len(buf) - i0)
    if n > 0:
        buf[i0: i0 + n] += clip[j0: j0 + n]


def sfz_release_times(path: Path) -> dict:
    """{MIDI key centre: ampeg_release seconds} from an sfz mapping file (Osiris: per-note damper times)."""
    out, kc = {}, None
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if line.startswith("<region>"):
            kc = None
        elif line.startswith("pitch_keycenter="):
            kc = int(line.split("=")[1])
        elif line.startswith("ampeg_release=") and kc is not None:
            out[kc] = float(line.split("=")[1])
    return out


class OneShot:
    """Unpitched one-shots (drums): round robins per layer, onset aligned to PREROLL, natural or table levels."""

    def __init__(self, name: str, files: dict, sr: int = SR, hp: float | None = None, stereo: str = "keep"):
        self.name, self.sr, self.stereo = name, sr, stereo
        self.files = {k: sorted(v) for k, v in files.items()}      # layer -> [paths]
        self._rr: dict = {}
        self.hp = hp

    @lru_cache(maxsize=64)
    def _load(self, path: str, semis_q: int) -> tuple[np.ndarray, float]:
        x, sr = sf.read(path, dtype="float64", always_2d=True)
        x = prepare(x, sr)
        m = x.mean(axis=1)
        env = envelope_1ms(m, sr)
        onset = int(np.argmax(env >= env.max() * 10 ** (ALIGN_STRUCK_DB / 20)))
        y = resample(x, sr, self.sr, semis_q / 1e4)
        return y, onset * (self.sr / sr) / 2 ** (semis_q / 1e4 / 12)

    def hit(self, layer: str, gain_db: float = 0.0, semis: float = 0.0, pan: float = 0.0) -> np.ndarray:
        lst = self.files[layer]
        i = self._rr.get(layer, 0)
        self._rr[layer] = i + 1
        y, on = self._load(str(lst[i % len(lst)]), int(round(semis * 1e4)))
        y = y.copy()
        on = int(round(on))
        pre = int(round(PREROLL * self.sr))
        if on >= pre:
            y = y[on - pre:]
            f = min(pre, 96)
            y[:f] *= (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, f)))[:, None]
        else:
            y = np.vstack([np.zeros((pre - on, 2)), y])
        if self.hp:
            y = sosfilt(butter(2, self.hp, "high", fs=self.sr, output="sos"), y, axis=0)
        if self.stereo == "mono":
            y = np.repeat(y.mean(axis=1, keepdims=True), 2, axis=1)
        y *= 10 ** (gain_db / 20)
        f = min(len(y) // 4, int(0.05 * self.sr))
        y[-f:] *= (0.5 + 0.5 * np.cos(np.linspace(0, np.pi, f)))[:, None]
        y[-1] = 0.0
        if pan:
            th = (pan + 1) * np.pi / 4
            y = y * np.array([np.cos(th), np.sin(th)]) * np.sqrt(2)
        return y


class Blend:
    """Several mic positions of one library played together (same zones, same velocity layer), each with a gain."""

    def __init__(self, parts: list[tuple[Instrument, float]]):
        self.parts = parts
        self.sustain = parts[0][0].sustain
        self.tune = parts[0][0].tune
        self.stereo = "keep"

    def note(self, midi, vel, dur=None, **kw):
        g0 = kw.pop("gain_db", 0.0)
        ys = [inst.note(midi, vel, dur, gain_db=g0 + g, **kw) for inst, g in self.parts]
        n = max(len(y) for y in ys)
        out = np.zeros((n, 2))
        for y in ys:
            out[: len(y)] += y
        return out

    def reset_rr(self):
        for inst, _ in self.parts:
            inst.reset_rr()

    def __getattr__(self, k):
        return getattr(self.parts[0][0], k)
