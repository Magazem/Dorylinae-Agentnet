#!/usr/bin/env bash
# v2 section V: assets/sky-v2end-{169,45}.png = v1's sky-end with the stars around the end address left out
# Thresholds: blob peak >= 28 levels over its local background, >= 3 PNG px (stricter than the paper skies: text sits on it).
# (the address box, Special Elite 64 centred, cap top − 28 px to descenders + 28 px, ± 366 + 28 px; PNG px = 2 × screen).
set -euo pipefail
Q=$(cd "$(dirname "$0")" && pwd); F=$Q/../film; E=$F/engine; PY=$F/qa/sky-clear/venv/bin/python
for f in 169 45; do $PY -I $F/qa/v2-paper-sky/clear.py $E/assets/sky-end-$f.png $Q/zend$f.json sky-end-$f.png $E/assets/sky-v2end-$f.png 28 3; done
