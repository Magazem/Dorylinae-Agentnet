"""The recommended v2 palette, built on sampler2 + synth (see PALETTE.md for the measurements behind each choice).

    from instruments import palette
    P = palette()              # dict of ready instruments (lazy sample loading, measurements cached in cache/)
    clip = P["piano"].note(69, 0.5)            # A4, onset at sampler2.PREROLL
"""
from __future__ import annotations

import sys
from functools import lru_cache
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from sampler2 import Instrument, OneShot, sfz_release_times  # noqa: E402

A2 = HERE.parent
FILM = A2.parent
S = A2 / "samples"
T = FILM / "tools" / "assets" / "vcsl"
VC = S / "vcsl"
VS = S / "vsco2"
NOTE = r"(?P<note>[A-Ga-g][#b]?-?\d)"
OSIRIS = S / "Osiris_Piano"


@lru_cache(maxsize=1)
def osiris_release() -> dict:
    """Per-note damper times from the library's own sfz (pedal-up mapping), keyed by sounding MIDI."""
    return sfz_release_times(OSIRIS / "Programs/modules/mappings/uc_micb_vl1_map.sfz")


# The fitted Osiris stretch curve runs 8-12 cents flat below E2 (B1 -11.6 c measured in the film): the bass is aimed
# no flatter than -5 cents there, so it lands no flatter than -6 (QA polish, item 2).
BASS_STRETCH_FLOOR = (40, -5.0)                        # -5 c aim: the resampler rounds to +/-0.9 c
# Each zone is tuned on its first second as heard (80 ms to 1.2 s after the attack), the window of the score's
# tuning check; the survey's whole-note frame median put Osiris B2 9.7 c and G2 5.5 c sharp of the curve.
PIANO_PITCH_WINDOW = (0.08, 1.2)
STRING_PITCH_WINDOW = (1.2, 3.0)                       # sections: the steady, vibrato part (mean pitch)


def piano(mic: str = "B", stereo: str = "best") -> Instrument:
    """Osiris Piano (CC0): worn Yamaha C2, una corda, half-stick, very low dynamics; sustain pedal down.
    Natural levels (not normalised): vl1 -> vl2 is +6 dB. Tuning: the piano's own stretch kept, A4 on 440 Hz.
    Stereo: the spaced pair puts some near-sine notes close to anti-phase (mic B E4 -0.55, A4 -0.38: -4.6 dB in
    mono); aligning the pair on the attack does not cure it (the sustained phase is modal, measured), so each
    sample is played from one mic of the pair (the louder, sampler2.best_channel), panned by pitch (low left,
    high right, as seen from the bench, +/-0.35); the width comes from the generated room."""
    return Instrument(f"osiris_sus_{mic}_{stereo}", OSIRIS / f"UC-Sus/{mic}", "*.flac",
                      rf"Piano_UC-Sus_Mic{mic}_" + NOTE + r"_vl(?P<layer>\d)", tune="stretch",
                      release_s=osiris_release(), max_shift=1.6, stereo=stereo,
                      pitch_pan=(64, 40, 0.35) if stereo != "keep" else None, stretch_floor=BASS_STRETCH_FLOOR,
                      pitch_window=PIANO_PITCH_WINDOW)


def piano_damped(mic: str = "B") -> Instrument:
    """Osiris, sustain pedal up: for notes that must stop (dur given -> the library's damper time)."""
    return Instrument(f"osiris_dry_{mic}_best", OSIRIS / f"UC/{mic}", "*.flac",
                      rf"Piano_UC_Mic{mic}_" + NOTE + r"_vl(?P<layer>\d)", tune="stretch",
                      release_s=osiris_release(), max_shift=1.6, stereo="best", pitch_pan=(64, 40, 0.35),
                      stretch_floor=BASS_STRETCH_FLOOR, pitch_window=PIANO_PITCH_WINDOW)


def grand(tune_ref: Instrument | None = None, level_ref: Instrument | None = None) -> Instrument:
    """VCSL Kawai grand (CC0), layers v2-v4 (v1 is 34 dB quieter with a 22 dB worst SNR: excluded), file names one
    octave below the sounding pitch (measured). For the 'tre corde' opening at the return: brighter than Osiris
    (1-4 kHz share of a mid note -7 to -14 dB against -21 to -36 dB). One mic of the pair per note (mono-safe).
    tune_ref: the lead piano it doubles. Each Kawai note is then re-tuned to the pitch that piano measurably plays for
    the same note and velocity (its own stretch is 4-8 cents off Osiris on the chord tones: a slow 1-1.5 Hz beat).
    level_ref: gain_db is then dB under that piano's note (measured per note; the recorded levels alone put the
    Kawai anywhere from 0 to 22 dB under Osiris at the score's velocities)."""
    return Instrument("kawai_best", T / "Chordophones/Zithers/Grand Piano, Kawai/Sustains", "*.wav",
                      r"GPiano_sus_" + NOTE + r"_v(?P<layer>[234])_rr(?P<rr>\d)", tune="stretch", release=(
                          T / "Chordophones/Zithers/Grand Piano, Kawai/Releases", "*.wav",
                          r"GPiano_rel_" + NOTE + r"_v(?P<layer>[234])_rr(?P<rr>\d)"),
                      release_s=0.5, max_shift=2.6, stereo="best", pitch_pan=(64, 40, 0.35), tune_ref=tune_ref,
                      level_ref=level_ref)


def vibes() -> Instrument:
    """VCSL vibraphone, soft mallets (CC0): 2 layers x 2 round robins, every 4 semitones, near-perfect tuning."""
    return Instrument("vibes_soft", VC / "Idiophones/Struck Idiophones/Vibraphone/Soft Mallets", "*.wav",
                      r"Vibes_soft_" + NOTE + r"_v(?P<layer>\d)_rr(?P<rr>\d)", short=True, release_s=0.9,
                      max_shift=2.6)


def chimes() -> Instrument:
    """VCSL hand chimes (CC0): mellow bell, 89.9 dB median SNR, every 1-3 semitones."""
    return Instrument("handchimes", VC / "Idiophones/Struck Idiophones/Hand Chimes", "**/*.wav",
                      r"sus_" + NOTE + r"_r(?P<rr>\d+)_main", short=True, release_s=1.2, max_shift=2.6)


def celesta() -> Instrument:
    """stamperadam celesta (CC0), hard layer only (the soft layer is normalised up to a -47 dBFS floor), one
    channel (several notes are near anti-phase L/R: correlation down to -0.85), G files are really G#."""
    return Instrument("celesta", S / "celesta-stamperadam", "*-hard-PB.wav", NOTE + r"-(?P<layer>hard)-PB",
                      short=True, stereo="left", release_s=0.8, max_shift=4.6,
                      exclude=("g7",))     # g7: fundamental 60 dB under another partial, pitch unreliable


def bowed() -> Instrument:
    """VCSL bowed vibraphone (CC0): 6 notes a fifth apart, 15.8 s each, tuning within 1.4 cents."""
    return Instrument("bowedvibes", T / "Idiophones/Struck Idiophones/Vibraphone/Bowed", "*.wav",
                      r"Vibes_bowed_" + NOTE + r"_rr(?P<rr>\d)", sustain=True, release_s=1.5, max_shift=3.6)


def cellos() -> Instrument:
    """VSCO-2 CE cello section, sustain with vibrato (CC0); layer 0 = v1 (soft)."""
    return Instrument("vc_sec", VS / "Strings/Cello Section/susvib", "*.wav",
                      r"susvib_" + NOTE + r"_v(?P<layer>\d)_(?P<rr>\d)", sustain=True, release_s=1.2, max_shift=2.6,
                      vel_split=[0.7], pitch_window=STRING_PITCH_WINDOW)


def violas() -> Instrument:
    return Instrument("vla_sec", VS / "Strings/Viola Section/susvib", "*.wav",
                      r"ViolaEns_susvib_" + NOTE + r"_v(?P<layer>\d)_(?P<rr>\d)", sustain=True, release_s=1.2,
                      max_shift=2.6, vel_split=[0.7], pitch_window=STRING_PITCH_WINDOW)


def bassdrum() -> OneShot:
    """VCSL Bass Drum 2 (CC0), felt beater, pp and mp hits, 2 takes each; mono; high-passed at 28 Hz."""
    d = VC / "Membranophones/Struck Membranophones/Bass Drum 2"
    return OneShot("bassdrum", {"pp": [d / "bassdrum_hit_pp1.wav", d / "bassdrum_hit_pp2.wav"],
                                "mp": [d / "bassdrum_hit_mp1.wav", d / "bassdrum_hit_mp2.wav"]},
                   hp=28.0, stereo="mono")


def palette() -> dict:
    p = piano("B")
    return {"piano": p, "piano_damped": piano_damped("B"), "grand": grand(tune_ref=p, level_ref=p), "vibes": vibes(), "chimes": chimes(),
            "celesta": celesta(), "bowed": bowed(), "cellos": cellos(), "violas": violas(), "bassdrum": bassdrum()}
