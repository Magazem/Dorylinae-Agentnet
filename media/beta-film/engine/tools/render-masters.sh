#!/usr/bin/env bash
# Render both lossless masters, then mux each with its mix into a reviewable H.264 (60 fps, CRF 16, BT.709, AAC).
set -euo pipefail
cd "$(dirname "$0")/.."
V=${1:-v2}
A=../audio/out
for asp in 16x9 4x5; do
  node render.mjs --aspect $asp --codec ffv1 --workers 3 --out out/master-$asp-$V.mkv --stats out/render-stats.jsonl
  ffmpeg -hide_banner -loglevel error -y -i out/master-$asp-$V.mkv -i $A/mix-$asp.wav -map 0:v -map 1:a \
    -vf "zscale=rangein=full:range=limited:primariesin=709:primaries=709:transferin=709:transfer=709:matrix=709:dither=error_diffusion,format=yuv420p" \
    -c:v libx264 -preset slow -crf 16 -profile:v high -level:v 4.2 -g 120 -x264-params aq-mode=3 \
    -colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv \
    -c:a aac -b:a 320k -ar 48000 -movflags +faststart -shortest out/dorylinae-beta-$asp-$V.mp4
  echo "DONE $asp $(du -h out/dorylinae-beta-$asp-$V.mp4 | cut -f1)"
done
echo ALL MASTERS DONE
