"""Measure every candidate instrument (sample files are downloaded data: run with python -I).

    PY=../../tools/.venv/bin/python
    $PY -I survey.py                 # all candidates -> ../research/measurements/<id>.json + summary.json + summary.md
    $PY -I survey.py osiris_b kawai  # only some

Each candidate: id, title, source, licence, folder, glob, filename regex with named groups note / layer / rr,
an optional octave correction found by measurement, and the kind (piano, mallet, sustain, drum).
"""
from __future__ import annotations

import json
import os
import re
import sys
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from analysis import measure_file, name_to_midi  # noqa: E402

A2 = HERE.parent
FILM = A2.parent
S = A2 / "samples"
T = FILM / "tools" / "assets" / "vcsl"          # v1's VCSL checkout (read-only for us)
VC = S / "vcsl"
VS = S / "vsco2"
OUT = A2 / "research" / "measurements"

NOTE_RE = r"(?P<note>[A-Ga-g][#b]?-?\d)"
CANDIDATES = [
    # ---------------------------------------------------------------- pianos
    dict(id="steinwayB", title="VCSL Grand Piano, Steinway B (Sus, close) - v1's piano", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="piano",
         dir=T / "Chordophones/Zithers/Grand Piano, Steinway B/Sus", glob="*.wav",
         rx=r"JHPiano_Sus_Close_" + NOTE_RE + r"_vl(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="kawai", title="VCSL Grand Piano, Kawai (Sustains, player mic)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="piano",
         dir=T / "Chordophones/Zithers/Grand Piano, Kawai/Sustains", glob="*.wav",
         rx=r"GPiano_sus_" + NOTE_RE + r"_v(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="kawai_rel", title="VCSL Grand Piano, Kawai (Releases)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="release",
         dir=T / "Chordophones/Zithers/Grand Piano, Kawai/Releases", glob="*.wav",
         rx=r"GPiano_rel_" + NOTE_RE + r"_v(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="knight", title="VCSL Upright Piano, Knight (Sustains)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="piano",
         dir=VC / "Chordophones/Zithers/Upright Piano, Knight/Sustains", glob="*.wav",
         rx=r"Player_vl(?P<layer>\d)_rr(?P<rr>\d)_" + NOTE_RE),
    dict(id="knight_rel", title="VCSL Upright Piano, Knight (Releases)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="release",
         dir=VC / "Chordophones/Zithers/Upright Piano, Knight/Releases", glob="*.wav",
         rx=r"Player_rel_rr(?P<rr>\d)_" + NOTE_RE),
    dict(id="yamaha_up", title="VCSL Upright Piano, Yamaha (Sustains; remaster of VSCO-2 'Upright Nr1')", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="piano",
         dir=VC / "Chordophones/Zithers/Upright Piano, Yamaha/Sustains", glob="*.wav",
         rx=r"Upright1_Sus_" + NOTE_RE + r"_vl(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="yamaha_up_rel", title="VCSL Upright Piano, Yamaha (Releases)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="release",
         dir=VC / "Chordophones/Zithers/Upright Piano, Yamaha/Releases", glob="*.wav",
         rx=r"Upright1_Rel_" + NOTE_RE + r"_vl(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="osiris_b", title="Osiris Piano (Yamaha C2, una corda, half-stick), sustain pedal down, mic B (edge)",
         lic="CC0 1.0", src="github.com/sfzinstruments/Osiris_Piano 18c6afc (Versilian Studios + Karoryfer, KVRDC'26)",
         kind="piano", dir=S / "Osiris_Piano/UC-Sus/B", glob="*.flac",
         rx=r"Piano_UC-Sus_MicB_" + NOTE_RE + r"_vl(?P<layer>\d)"),
    dict(id="osiris_a", title="Osiris Piano, sustain down, mic A (inside)", lic="CC0 1.0",
         src="github.com/sfzinstruments/Osiris_Piano 18c6afc", kind="piano", dir=S / "Osiris_Piano/UC-Sus/A",
         glob="*.flac", rx=r"Piano_UC-Sus_MicA_" + NOTE_RE + r"_vl(?P<layer>\d)"),
    dict(id="osiris_c", title="Osiris Piano, sustain down, mic C (outside)", lic="CC0 1.0",
         src="github.com/sfzinstruments/Osiris_Piano 18c6afc", kind="piano", dir=S / "Osiris_Piano/UC-Sus/C",
         glob="*.flac", rx=r"Piano_UC-Sus_MicC_" + NOTE_RE + r"_vl(?P<layer>\d)"),
    dict(id="osiris_b_dry", title="Osiris Piano, sustain pedal up, mic B", lic="CC0 1.0",
         src="github.com/sfzinstruments/Osiris_Piano 18c6afc", kind="piano", dir=S / "Osiris_Piano/UC/B",
         glob="*.flac", rx=r"Piano_UC_MicB_" + NOTE_RE + r"_vl(?P<layer>\d)"),
    dict(id="uprightKW", title="FreePats Upright Piano KW (Kawai upright, Zoom H1, living room)", lic="CC0 1.0",
         src="freepats.zenvoid.org UprightPianoKW-SFZ+FLAC-20220221", kind="piano",
         dir=S / "UprightPianoKW/UprightPianoKW-SFZ+FLAC-20220221/samples", glob="*.flac",
         rx=NOTE_RE + r"v(?P<layer>[LH])"),
    dict(id="vsco_ur1", title="VSCO-2 CE Keys/Upright Nr1 (raw version of VCSL Yamaha upright)", lic="CC0 1.0",
         src="github.com/sgossner/VSCO-2-CE 4403009", kind="piano", dir=VS / "Keys/Upright Nr1", glob="*.wav",
         rx=r"UR1_" + NOTE_RE + r"_(?P<layer>pp|mf|f)_RR(?P<rr>\d)"),
    # ---------------------------------------------------------------- sustained beds
    dict(id="bowedvibes", title="VCSL Vibraphone, bowed", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc",
         kind="sustain", dir=T / "Idiophones/Struck Idiophones/Vibraphone/Bowed", glob="*.wav",
         rx=r"Vibes_bowed_" + NOTE_RE + r"_rr(?P<rr>\d)"),
    dict(id="vln_sec", title="VSCO-2 CE Violin Section, sustain vibrato", lic="CC0 1.0",
         src="github.com/sgossner/VSCO-2-CE 4403009", kind="sustain", dir=VS / "Strings/Violin Section/susVib",
         glob="*.wav", rx=r"VlnEns_susVib_" + NOTE_RE + r"_v(?P<layer>\d)"),
    dict(id="vla_sec", title="VSCO-2 CE Viola Section, sustain vibrato", lic="CC0 1.0",
         src="github.com/sgossner/VSCO-2-CE 4403009", kind="sustain", dir=VS / "Strings/Viola Section/susvib",
         glob="*.wav", rx=r"ViolaEns_susvib_" + NOTE_RE + r"_v(?P<layer>\d)_(?P<rr>\d)"),
    dict(id="vc_sec", title="VSCO-2 CE Cello Section, sustain vibrato", lic="CC0 1.0",
         src="github.com/sgossner/VSCO-2-CE 4403009", kind="sustain", dir=VS / "Strings/Cello Section/susvib",
         glob="*.wav", rx=r"susvib_" + NOTE_RE + r"_v(?P<layer>\d)_(?P<rr>\d)"),
    dict(id="cb_nv", title="VSCO-2 CE Solo Contrabass, sustain non-vibrato", lic="CC0 1.0",
         src="github.com/sgossner/VSCO-2-CE 4403009", kind="sustain", dir=VS / "Strings/Solo Contrabass/SusNV",
         glob="*.wav", rx=r"BKCtbss_SusNV_" + NOTE_RE + r"_v(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="glasses", title="VCSL Wine Glasses (rubbed)", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc",
         kind="sustain", dir=VC / "Idiophones/Friction Idiophones/Wine Glasses", glob="**/*.wav",
         rx=r"glass\d_" + NOTE_RE + r"_(?P<layer>Fast|Slow)_(?P<rr>\d)_(?:Main|main)"),
    # ---------------------------------------------------------------- mallets / colour
    dict(id="vibes_soft", title="VCSL Vibraphone, soft mallets", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc",
         kind="mallet", dir=VC / "Idiophones/Struck Idiophones/Vibraphone/Soft Mallets", glob="*.wav",
         rx=r"Vibes_soft_" + NOTE_RE + r"_v(?P<layer>\d)_rr(?P<rr>\d)"),
    dict(id="celesta", title="Celesta by stamperadam (Freesound pack 6166), edited by P. Battersby for VPO3",
         lic="CC0 1.0", src="github.com/open-soundfonts/Virtual_Playing_Orchestra_3 b0afa0f libs/stamperadam",
         kind="mallet", dir=S / "celesta-stamperadam", glob="*-PB.wav",
         rx=NOTE_RE + r"-(?P<layer>hard|soft)-PB"),
    dict(id="glock", title="VCSL Glockenspiel", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc", kind="mallet",
         dir=VC / "Idiophones/Struck Idiophones/Glockenspiel", glob="*.wav",
         rx=r"glock_(?P<layer>soft|medium|loud)_" + NOTE_RE + r"_(?P<rr>\d+)"),
    dict(id="handchimes", title="VCSL Hand Chimes", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc",
         kind="mallet", dir=VC / "Idiophones/Struck Idiophones/Hand Chimes", glob="**/*.wav",
         rx=r"sus_" + NOTE_RE + r"_r(?P<rr>\d+)_main"),
    dict(id="marimba_soft", title="VCSL Marimba, soft (outrigger)", lic="CC0 1.0",
         src="github.com/sgossner/VCSL c1ea7bc", kind="mallet",
         dir=VC / "Idiophones/Struck Idiophones/Marimba", glob="*.wav",
         rx=r"Marimba_hit_Outrigger_" + NOTE_RE + r"_(?P<layer>soft)_(?P<rr>\d+)"),
    # ---------------------------------------------------------------- low end (unpitched)
    dict(id="bassdrum", title="VCSL Bass Drum 2 (pp, mp hits)", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc",
         kind="drum", dir=VC / "Membranophones/Struck Membranophones/Bass Drum 2", glob="*.wav",
         rx=r"bassdrum_hit_(?P<layer>pp|mp)(?P<rr>\d)"),
    dict(id="framedrum", title="VCSL Frame Drum", lic="CC0 1.0", src="github.com/sgossner/VCSL c1ea7bc", kind="drum",
         dir=VC / "Membranophones/Struck Membranophones/Frame Drum", glob="*.wav",
         rx=r"HDrum(?P<note>[LS])_(?P<layer>Hand|HitMuted_v\d|Hit_v\d|Hand_v\d)_rr(?P<rr>\d)"),
]
SHORT = {"mallet", "release", "drum"}


def job(args):
    path, midi_exp, kind = args[:3]
    force = args[3] if len(args) > 3 else None
    try:
        return measure_file(path, midi_exp, short=kind in SHORT, pitched=kind != "drum", force_octave=force)
    except Exception as e:  # keep going; report it
        return {"file": str(path), "error": repr(e)}


def run(c) -> dict:
    files = sorted(Path(c["dir"]).glob(c["glob"]))
    rx = re.compile(c["rx"])
    tasks, meta = [], []
    for f in files:
        mm = rx.search(f.name)
        if not mm:
            continue
        g = mm.groupdict()
        midi = None
        if c["kind"] != "drum" and g.get("note"):
            midi = name_to_midi(g["note"])
        tasks.append((str(f), midi, c["kind"]))
        meta.append({"name": f.name, "note": g.get("note"), "midi_named": midi, "layer": g.get("layer"),
                     "rr": g.get("rr")})
    with ProcessPoolExecutor(max_workers=os.cpu_count() or 4) as ex:
        res = list(ex.map(job, tasks, chunksize=2))
        # pass 2: a library names its octaves one way; files whose YIN octave disagrees with the library's mode
        # are re-measured with the mode forced (YIN locks on the 2nd partial of weak bass fundamentals)
        ks = [r["tuning"]["octave_offset"] for r in res if r.get("tuning") and "octave_offset" in r["tuning"]]
        if ks:
            vals, cnt = np.unique(ks, return_counts=True)
            mode = int(vals[np.argmax(cnt)])
            redo = [i for i, r in enumerate(res) if r.get("tuning") is not None
                    and r["tuning"].get("octave_offset") != mode]
            again = list(ex.map(job, [tasks[i] + (mode,) for i in redo], chunksize=1))
            for i, r in zip(redo, again):
                r["tuning"]["pass1_octave"] = res[i]["tuning"].get("octave_offset")
                res[i] = r
    for m_, r in zip(meta, res):
        r.update(m_)
    summ = summarise(c, res)
    out = {k: (str(v) if isinstance(v, Path) else v) for k, v in c.items()}
    out["summary"] = summ
    out["samples"] = res
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{c['id']}.json").write_text(json.dumps(out, indent=1, default=float))
    return out


def pct(v, q):
    v = [x for x in v if x is not None and np.isfinite(x)]
    return float(np.percentile(v, q)) if v else None


def summarise(c, res) -> dict:
    ok = [r for r in res if "error" not in r and not r.get("empty")]
    s: dict = {"files": len(res), "errors": len(res) - len(ok)}
    if not ok:
        return s
    s["sr"] = sorted({r["sr"] for r in ok})
    s["subtype"] = sorted({r["subtype"] for r in ok})
    s["channels"] = sorted({r["channels"] for r in ok})
    s["layers"] = sorted({str(r["layer"]) for r in ok if r.get("layer") is not None})
    s["rr"] = sorted({str(r["rr"]) for r in ok if r.get("rr") is not None})
    s["notes"] = len({r["note"] for r in ok if r.get("note")})
    mids = sorted({r["midi_named"] for r in ok if r.get("midi_named") is not None})
    if mids:
        s["named_range"] = [mids[0], mids[-1]]
        st = np.diff(mids)
        s["step_semitones_median"] = float(np.median(st)) if len(st) else None
        s["step_semitones_max"] = int(np.max(st)) if len(st) else None
    s["peak_dbfs_median"] = pct([r["peak_dbfs"] for r in ok], 50)
    s["peak_dbfs_range"] = [pct([r["peak_dbfs"] for r in ok], 0), pct([r["peak_dbfs"] for r in ok], 100)]
    s["floor_dbfs_median"] = pct([r["floor_dbfs"] for r in ok], 50)
    s["floor_dbfs_worst"] = pct([r["floor_dbfs"] for r in ok], 100)
    s["snr_db_median"] = pct([r["snr_db"] for r in ok], 50)
    s["snr_db_worst"] = pct([r["snr_db"] for r in ok], 0)
    s["pre_rms_dbfs_median"] = pct([r.get("pre_rms_dbfs") for r in ok], 50)
    s["onset_ms_median"] = pct([r["onset_ms"] for r in ok], 50)
    s["onset_ms_p10_p90"] = [pct([r["onset_ms"] for r in ok], 10), pct([r["onset_ms"] for r in ok], 90)]
    s["first_sample_db_max"] = pct([r["first_sample_db"] for r in ok], 100)
    s["rise_ms_median"] = pct([r["rise_ms"] for r in ok], 50)
    s["dur_s_median"] = pct([r["dur"] for r in ok], 50)
    s["t30_s_median"] = pct([r.get("t30_s") for r in ok], 50)
    s["end_db_re_peak_max"] = pct([r["end_db_re_peak"] for r in ok], 100)
    s["truncated_files"] = sum(1 for r in ok if r["end_db_re_peak"] > -50 and not r["ends_in_zero"])
    s["ends_in_zero_files"] = sum(1 for r in ok if r["ends_in_zero"])
    for k in ("lr_corr", "side_mid_db", "balance_db", "mono_loss_db", "ic_delay_ms", "centroid_hz", "hf4k_share_db"):
        v = [r.get(k) for r in ok]
        if any(x is not None for x in v):
            s[k + "_median"] = pct(v, 50)
            s[k + "_p10_p90"] = [pct(v, 10), pct(v, 90)]
    tun = [r["tuning"] for r in ok if r.get("tuning") and "cents" in r["tuning"]]
    if tun:
        offs = [t["octave_offset"] for t in tun]
        vals, cnt = np.unique(offs, return_counts=True)
        s["octave_offset_mode"] = int(vals[np.argmax(cnt)])
        s["octave_offset_disagree_pass1"] = int(sum(1 for t in tun if t.get("pass1_octave") is not None))
        s["label_offset_files"] = sorted({(r["name"], t.get("label_offset_semitones", 0)) for t, r in
                                          zip(tun, [r for r in ok if r.get("tuning") and "cents" in r["tuning"]])
                                          if t.get("label_offset_semitones", 0)})
        cents = [t["cents"] for t in tun]
        s["tuning_files"] = len(tun)
        s["cents_median"] = pct(cents, 50)
        s["cents_abs_median"] = pct(np.abs(cents), 50)
        s["cents_abs_p90"] = pct(np.abs(cents), 90)
        s["cents_abs_max"] = pct(np.abs(cents), 100)
        s["cents_spread_median"] = pct([t.get("cents_spread", 0) for t in tun], 50)
        s["partial_fit_files"] = sum(1 for t in tun if t.get("method") == "partial-fit")
        s["B_median"] = pct([t.get("B") for t in tun], 50)
        # by register (named midi + offset)
        lo = [t["cents"] for t, r in zip(tun, [r for r in ok if r.get("tuning") and "cents" in r["tuning"]])
              if t["target_midi"] < 48]
        mid = [t["cents"] for t, r in zip(tun, [r for r in ok if r.get("tuning") and "cents" in r["tuning"]])
               if 48 <= t["target_midi"] < 84]
        hi = [t["cents"] for t, r in zip(tun, [r for r in ok if r.get("tuning") and "cents" in r["tuning"]])
              if t["target_midi"] >= 84]
        s["cents_median_by_register"] = {"<C3": pct(lo, 50), "C3-B5": pct(mid, 50), ">=C6": pct(hi, 50)}
        s["cents_abs_max_mid"] = pct(np.abs(mid), 100) if mid else None
    if c["kind"] == "piano":
        # layer level spacing (files normalised?) and centroid per layer
        bylayer = {}
        for r in ok:
            bylayer.setdefault(str(r["layer"]), []).append(r)
        s["by_layer"] = {k: {"peak_dbfs_median": pct([r["peak_dbfs"] for r in v], 50),
                             "max_rms_dbfs_median": pct([r["max_rms_dbfs"] for r in v], 50),
                             "centroid_hz_median": pct([r.get("centroid_hz") for r in v], 50),
                             "snr_db_median": pct([r["snr_db"] for r in v], 50)} for k, v in sorted(bylayer.items())}
    return s


def main(argv):
    ids = set(argv[1:])
    allsum = {}
    sp = OUT / "summary.json"
    if sp.exists():
        allsum = json.loads(sp.read_text())
    for c in CANDIDATES:
        if ids and c["id"] not in ids:
            continue
        if not Path(c["dir"]).exists():
            print("missing", c["id"], c["dir"])
            continue
        o = run(c)
        allsum[c["id"]] = {"title": c["title"], "lic": c["lic"], "src": c["src"], "kind": c["kind"], **o["summary"]}
        print(c["id"], json.dumps(o["summary"], default=float)[:1500], flush=True)
    sp.write_text(json.dumps(allsum, indent=1, default=float))


if __name__ == "__main__":
    main(sys.argv)
