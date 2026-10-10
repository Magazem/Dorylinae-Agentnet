#!/usr/bin/env bash
set -euo pipefail
B=/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/../brand/dorylinae-brand; E=/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/engine
gen() { (cd $B && npx tsx film-gen/gen-assets.ts $E/out/v2sky sky $2 $3 $4 $1 0.6 0.55 2.25) && (cd $B && npx tsx film-gen/dust-only.ts $E/out/v2sky/dust-$1 $2 $3 $4 0.6 0.55) && python3 -I $E/tools/dedust.py $E/out/v2sky/$1 $E/out/v2sky/dust-$1 $E/assets/$1 0.35; }
mkdir -p $E/out/v2sky
gen sky-v2scene-169.png 97 6800 3000
gen sky-v2scene-45.png 97 3000 5800
echo V2 SKIES DONE
