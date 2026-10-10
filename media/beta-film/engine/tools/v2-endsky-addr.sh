#!/usr/bin/env bash
# fix-r3 (review r2, frame): a second pass over assets/sky-v2end-{169,45}.png (after b1345tools/end-sky.sh, which clears a
# band 28 px round the address): leave out the stars within 40 px of `Apply at dorylinae.net` (§4.3: within 40 px of text,
# nothing over 1 px or 30 %). The band is the address's measured ink box (16:9 594,885 – 1324,944; 4:5 174,1015 – 904,1074,
# screen px at 62.0 s) grown by 40 px, in PNG px (2 × screen); same thresholds as end-sky.sh (peak >= 28 levels, >= 3 PNG px).
# Re-run it whenever end-sky.sh is re-run. Before this pass the brightest star within 40 px was 81 % / 84 % (16:9 / 4:5).
set -euo pipefail
E=$(cd "$(dirname "$0")/.." && pwd); F=$E/..; PY=$F/qa/sky-clear/venv/bin/python
T=$(mktemp -d)
echo '[{"sky":"sky-end-169.png","kind":"band","outer":[1108,1690,2728,1968]}]' > $T/z169.json
echo '[{"sky":"sky-end-45.png","kind":"band","outer":[268,1950,1888,2228]}]' > $T/z45.json
for f in 169 45; do
  cp $E/assets/sky-v2end-$f.png $T/in-$f.png
  $PY -I $F/qa/v2-paper-sky/clear.py $T/in-$f.png $T/z$f.json sky-end-$f.png $E/assets/sky-v2end-$f.png 28 3
done
rm -rf $T
