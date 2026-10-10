"""Write ../research/manifest.json: every sample file the recommended palette can play, with source, licence and md5."""
import hashlib, json, sys
from pathlib import Path
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import instruments as I  # noqa
SRC = {
    "piano": ("Osiris Piano (Versilian Studios + Karoryfer Samples, KVRDC 2026)", "github.com/sfzinstruments/Osiris_Piano @ 18c6afccb60c", "CC0 1.0"),
    "grand": ("VCSL Grand Piano, Kawai", "github.com/sgossner/VCSL @ c1ea7bcc3c73", "CC0 1.0"),
    "vibes": ("VCSL Vibraphone, soft mallets", "github.com/sgossner/VCSL @ c1ea7bcc3c73", "CC0 1.0"),
    "chimes": ("VCSL Hand Chimes", "github.com/sgossner/VCSL @ c1ea7bcc3c73", "CC0 1.0"),
    "celesta": ("Celesta by stamperadam, Freesound pack 6166 (edited by P. Battersby)", "github.com/open-soundfonts/Virtual_Playing_Orchestra_3 @ b0afa0f570e0, libs/stamperadam", "CC0 1.0"),
    "bowed": ("VCSL Vibraphone, bowed", "github.com/sgossner/VCSL @ c1ea7bcc3c73", "CC0 1.0"),
    "cellos": ("VSCO-2 CE Cello Section, sustain vibrato", "github.com/sgossner/VSCO-2-CE @ 440300901dfe", "CC0 1.0"),
    "violas": ("VSCO-2 CE Viola Section, sustain vibrato", "github.com/sgossner/VSCO-2-CE @ 440300901dfe", "CC0 1.0"),
    "bassdrum": ("VCSL Bass Drum 2 (pp, mp)", "github.com/sgossner/VCSL @ c1ea7bcc3c73", "CC0 1.0"),
}
P = I.palette()
out = {}
for k, (title, src, lic) in SRC.items():
    inst = P[k]
    if hasattr(inst, "zones"):
        files = [z.path for z in inst.zones] + ([z.path for z in inst.rel] if getattr(inst, "rel", None) else [])
    else:
        files = [f for v in inst.files.values() for f in v]
    rows = []
    for f in sorted(set(files)):
        rows.append({"file": str(f.relative_to(HERE.parent.parent)), "bytes": f.stat().st_size,
                     "md5": hashlib.md5(f.read_bytes()).hexdigest()})
    out[k] = {"title": title, "source": src, "licence": lic, "files": len(rows),
              "bytes": sum(r["bytes"] for r in rows), "list": rows}
    print(k, len(rows), round(out[k]["bytes"] / 1e6, 1), "MB")
(HERE.parent / "research" / "manifest.json").write_text(json.dumps(out, indent=1))
