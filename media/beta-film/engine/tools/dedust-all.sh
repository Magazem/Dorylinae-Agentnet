#!/usr/bin/env bash
# Render each in-use sky's dust layer from the brand code and write a de-dusted copy to assets-dedust/.
set -euo pipefail
B=/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/brand/dorylinae-brand; E=/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/engine
run() { # name seed w h
  (cd $B && npx tsx film-gen/dust-only.ts $E/out/dust/$1 $2 $3 $4 0.6 0.55)
  python3 -I $E/tools/dedust.py $E/assets/$1 $E/out/dust/$1 $E/assets-dedust/$1 0.35
}
run sky-w-169.png 97 4992 2808
run sky-w-45.png 97 2268 2836
run sky-maya-clear-169.png 4968 4608 2592
run sky-maya-clear-45.png 4968 2592 3240
run sky-theo-clear-169.png 6333 4608 2592
run sky-theo-clear-45.png 7333 2592 3240
run sky-inv-169.png 10 4032 2268
run sky-inv-45.png 10 2268 2836
run sky-end-169.png 1444 3840 2160
run sky-end-45.png 1444 2160 2700
echo ALL DONE
