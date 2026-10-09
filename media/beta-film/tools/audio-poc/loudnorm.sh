#!/usr/bin/env bash
# Two-pass EBU R128 normalisation with ffmpeg loudnorm, linear mode (one static gain, no dynamic processing).
#   ./loudnorm.sh in.wav out.wav [I=-14] [TP=-1] [LRA=11]
# Pass 1 measures; pass 2 applies the measured values. If the gain needed to reach I would push the true
# peak above TP, loudnorm silently falls back to dynamic mode: check "normalization_type" in the report.
set -euo pipefail
in=$1; out=$2; I=${3:--14}; TP=${4:--1}; LRA=${5:-11}
m=$(ffmpeg -hide_banner -nostats -i "$in" -af "loudnorm=I=$I:TP=$TP:LRA=$LRA:print_format=json" -f null - 2>&1 | sed -n '/^{/,/^}/p')
get() { echo "$m" | python3 -c "import sys,json; print(json.load(sys.stdin)['$1'])"; }
rep=$(ffmpeg -hide_banner -nostats -y -i "$in" -af "loudnorm=I=$I:TP=$TP:LRA=$LRA:measured_I=$(get input_i):measured_TP=$(get input_tp):measured_LRA=$(get input_lra):measured_thresh=$(get input_thresh):offset=$(get target_offset):linear=true:print_format=json,aresample=48000" \
  -c:a pcm_s24le "$out" 2>&1 | sed -n '/^{/,/^}/p')
echo "$rep" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps({k: d[k] for k in ('input_i','input_tp','output_i','output_tp','output_lra','normalization_type')}))"
