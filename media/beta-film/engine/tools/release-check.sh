#!/usr/bin/env bash
# Release check for the v2 upload files: specs, loudness, keyframes, and per-frame change in the still holds.
#   tools/release-check.sh out/release-<version>      (PY=<python with numpy>, default python3)
set -uo pipefail
R=${1:?release dir}
PY=${PY:-python3}   # needs numpy
W=$(mktemp -d)
for f in "$R"/dorylinae-beta-*.mp4 "$R"/master-*-hq.mp4; do
  n=$(basename "$f" .mp4)
  echo "== $n  $(du -b "$f" | cut -f1) bytes"
  ffprobe -v error -show_entries stream=codec_name,profile,level,width,height,r_frame_rate,color_space,color_transfer,color_primaries,color_range,duration -of compact=p=0 "$f"
  ffmpeg -hide_banner -nostats -i "$f" -vn -af ebur128=peak=true -f null - 2>&1 | awk '/Integrated loudness/{s=1} s&&/I:/{printf "I %s %s  ", $2, $3} /True peak/{t=1} t&&/Peak:/{printf "TP %s %s\n", $2, $3; t=0}'
  echo "keyframes: $(ffprobe -v error -select_streams v -skip_frame nokey -show_entries frame=pts_time -of csv=p=0 "$f" | tr '\n' ' ')"
done
# per-frame change across the old keyframe times inside the holds (should now be ~0 everywhere except real events)
for f in "$R"/dorylinae-beta-*.mp4; do
  n=$(basename "$f" .mp4); case $n in *16x9*) w=1920; h=1080;; *) w=1080; h=1350;; esac
  for t in 15.65 25.65 35.65 56.38; do
    ffmpeg -hide_banner -loglevel error -y -ss $t -i "$f" -frames:v 8 -pix_fmt gray -f rawvideo "$W/k.gray"
    "$PY" -I - "$W/k.gray" $w $h "$n" $t <<'EOF'
import numpy as np, sys
p, w, h, n, t = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4], sys.argv[5]
a = np.fromfile(p, np.uint8).reshape(-1, h, w).astype(int)
print(n, t, ' '.join(f"{np.abs(a[i]-a[i-1]).mean():.3f}/{(np.abs(a[i]-a[i-1]) > 2).sum()}" for i in range(1, len(a))))
EOF
  done
done
rm -rf "$W"
