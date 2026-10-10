#!/usr/bin/env bash
# v2 sections III / IV, 16:9: assets/sky-v2inv2-169.png = assets/sky-v2inv-169.png (qa/v2-paper-sky) with its four partial
# star removals repaired: a comet-like halo remnant above the card / envelope (screen ~ 746, 229) and three bitten stars
# (screen ~ 1632, 758 · 1405, 820 · 1717, 639); final polish: one more bitten star (screen ~ 644, 996) left by the IV
# note's ring at its held s 1.06 (qa/v2-paper-sky/zones.mjs). Each site's whole cluster is replaced by the local low-frequency sky plus
# the grain of a nearby star-free donor (repair.py). 4:5 (sky-v2inv-45.png) has none (bites.py). Re-run after run.sh.
set -euo pipefail
Q=$(cd "$(dirname "$0")" && pwd); F=$Q/../film; E=$F/engine; PY=$F/qa/sky-clear/venv/bin/python
$PY -I $Q/bites.py $E/assets/sky-inv-169.png $E/assets/sky-v2inv-169.png
$PY -I $Q/repair.py $E/assets/sky-v2inv-169.png $E/assets/sky-v2inv2-169.png 1568,505,58 3360,1569,32 2885,1682,56 3531,1331,32 1384,2047,26
$PY -I $Q/bites.py $E/assets/sky-inv-169.png $E/assets/sky-v2inv2-169.png
