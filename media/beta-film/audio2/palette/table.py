"""Print the survey summary as a markdown table (used for PALETTE.md)."""
import json, sys
from pathlib import Path
d = json.loads((Path(__file__).resolve().parent.parent / "research/measurements/summary.json").read_text())
def f(v, n=1):
    if v is None: return "-"
    if isinstance(v, (list, tuple)): return "/".join(f(x, n) for x in v)
    if isinstance(v, float): return f"{v:.{n}f}"
    return str(v)
cols = [("files","files"),("sr","sr"),("subtype","fmt"),("layers","layers"),("rr","rr"),("notes","notes"),("step_semitones_max","step"),
        ("snr_db_median","SNR"),("snr_db_worst","SNR worst"),("pre_rms_dbfs_median","pre-roll"),("onset_ms_p10_p90","onset p10/90 ms"),("rise_ms_median","rise ms"),
        ("dur_s_median","dur s"),("t30_s_median","T30 s"),("truncated_files","trunc"),("octave_offset_mode","oct"),
        ("cents_abs_median","|c| med"),("cents_abs_p90","|c| p90"),("cents_abs_max_mid","|c| max C3-B5"),("cents_spread_median","spread"),
        ("lr_corr_median","L/R corr"),("lr_corr_p10_p90","corr p10/90"),("mono_loss_db_median","mono dB"),("centroid_hz_median","centroid Hz"),("hf4k_share_db_median",">4k dB")]
ids = sys.argv[1:] or list(d)
print("| id | " + " | ".join(c[1] for c in cols) + " |")
print("|" + "---|" * (len(cols) + 1))
for k in ids:
    v = d[k]
    print(f"| {k} | " + " | ".join(f(v.get(c[0])) for c in cols) + " |")
