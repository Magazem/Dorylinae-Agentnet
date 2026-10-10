#!/usr/bin/env bash
# v2 gallery gate r2: leave out the stars within ~10 px of the v2 paper rest edges and along the trail lanes.
# Inputs: assets/sky-v2scene-*.png (the sky agent's, untouched) and assets/sky-inv-*.png (v1's, untouched).
# Outputs (v2 only): assets/sky-v2scene-clear-*.png and assets/sky-v2inv-*.png, used by shots-v2/common-v2.js sky().
# Re-run whenever the source skies or the v2 geometry / cameras change.
# dirfix (review r1 motion): then the partial-removal repairs (16:9 inv: b1345tools/inv-sky.sh -> sky-v2inv2-169.png;
# 4:5 inv: one star beside the portrait card, below) and the display-resolution copies the film draws (onex.py ->
# assets/*-1x.png: Lanczos, so Chromium never mip-maps a moving sky).
set -euo pipefail
Q=$(cd "$(dirname "$0")" && pwd); E=$Q/../../engine; PY=$Q/../sky-clear/venv/bin/python; B=$Q/../../../b1345tools
ln -sfn $Q/../../tools/node_modules $Q/node_modules
node $Q/zones.mjs $E 16x9 $Q/z169.json
node $Q/zones.mjs $E 4x5 $Q/z45.json
$PY -I $Q/clear.py $E/assets/sky-inv-169.png $Q/z169.json sky-inv-169.png $E/assets/sky-v2inv-169.png
$PY -I $Q/clear.py $E/assets/sky-inv-45.png $Q/z45.json sky-inv-45.png $E/assets/sky-v2inv-45.png
$PY -I $Q/clear.py $E/assets/sky-v2scene-169.png $Q/z169.json sky-v2scene-169.png $E/assets/sky-v2scene-clear-169.png
$PY -I $Q/clear.py $E/assets/sky-v2scene-45.png $Q/z45.json sky-v2scene-45.png $E/assets/sky-v2scene-clear-45.png
# 16:9 inv: the four partial removals repaired (sky-v2inv2-169.png, what sections III and IV draw in 16:9)
bash $B/inv-sky.sh
# 4:5 inv (dirfix): the portrait card's ring leaves the bright star 21 px right of the card (screen ~ 1037, 819) with a
# neighbour removed from its halo (a bitten halo, bites.py); the cluster is left out whole, as inv-sky.sh does in 16:9
$PY -I $B/repair.py $E/assets/sky-v2inv-45.png $E/assets/sky-v2inv-45.png 2129,1707,48
$PY -I $B/bites.py $E/assets/sky-inv-45.png $E/assets/sky-v2inv-45.png
# 16:9 scene (final r1, frame review): S-4 at CAM-R+ is a flush-left block since the final polish (item 12), so its line 1
# starts at screen x ~ 510 and a soft 45 % star of the scene sky (screen ~ 503, 854 at CAM-R+, 15.25 - 21 s) sat 5 px
# left of the `I` of `It reads the outside:` and read as a bullet (§4.3: <= 1 px and <= 30 % within 40 px of a caption).
# Its cluster is left out whole, as inv-sky.sh does (PNG px of the 2x scene sky, mapped with fix-r1/tools/skypt.mjs)
$PY -I $B/repair.py $E/assets/sky-v2scene-clear-169.png $E/assets/sky-v2scene-clear-169.png 2560,2167,12
# the copies the film draws, at display size
for n in sky-v2scene-clear-169 sky-v2scene-clear-45 sky-v2inv2-169 sky-v2inv-45; do
  $PY -I $Q/onex.py $E/assets/$n.png $E/assets/$n-1x.png
done
echo V2 PAPER SKY DONE
