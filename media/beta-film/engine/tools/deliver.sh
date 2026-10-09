#!/usr/bin/env bash
# Final delivery: lossless masters (with motion blur) -> platform files and poster frames.
#   tools/deliver.sh [version]        e.g. tools/deliver.sh v3
# Output: out/release-<version>/
#   dorylinae-beta-16x9-60fps.mp4   X (and any 60 fps player)              <= 29 MB, 2-pass H.264 High
#   dorylinae-beta-4x5-60fps.mp4    X mobile feed                          <= 29 MB
#   dorylinae-beta-16x9-30fps.mp4   LinkedIn, Reddit (30 fps cap)          <= 29 MB, 2-frame blend (180° shutter)
#   dorylinae-beta-4x5-30fps.mp4    LinkedIn, Reddit mobile feed           <= 29 MB
#   master-16x9-hq.mp4 / master-4x5-hq.mp4   archive quality (CRF 14, 60 fps)
#   poster-16x9.png / poster-4x5.png (52.2 s), poster-alt-*.png (7.2 s)
# Keyframes sit on the cuts only, so no I-frame "pulse" lands inside a still hold.
set -euo pipefail
cd "$(dirname "$0")/.."
V=${1:-v3}
R=out/release-$V
A=../audio/out
mkdir -p "$R"
CUTS="0,3.6,9.6,13.2,19.2,30,33.6,38.4,40.8,46.2,52.8,57.6,62.4"
VF709="zscale=rangein=full:range=limited:primariesin=709:primaries=709:transferin=709:transfer=709:matrix=709:dither=error_diffusion,format=yuv420p"
TAGS="-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv"
X264="-preset slow -profile:v high -level:v 4.2 -force_key_frames $CUTS -x264-params keyint=600:min-keyint=1:scenecut=0:aq-mode=3"
DUR=63.0

for asp in 16x9 4x5; do
  [ -f out/master-$asp-$V.mkv ] || node render.mjs --aspect $asp --codec ffv1 --workers 3 --blur-samples 16 --out out/master-$asp-$V.mkv --stats out/render-stats.jsonl
  M=out/master-$asp-$V.mkv
  # archive quality
  ffmpeg -hide_banner -loglevel error -y -i $M -i $A/mix-$asp.wav -map 0:v -map 1:a -vf "$VF709" -c:v libx264 $X264 -crf 14 $TAGS \
    -c:a aac -b:a 320k -ar 48000 -movflags +faststart -shortest $R/master-$asp-hq.mp4
  # upload files: 2-pass to fit under 29 MB (audio 192 kb/s)
  for fps in 60 30; do
    # 30 fps: average each pair of 60 fps frames (2k, 2k+1) -> one 30 fps frame. Pairs never straddle a cut (every cut is
    # on an even 60 fps frame), so no frame mixes two shots.
    if [ $fps = 30 ]; then VF="tmix=frames=2:weights='1 1',select='mod(n\\,2)',setpts=N/(30*TB),$VF709"; else VF="$VF709"; fi
    VB=$(python3 -c "print(int((29*8*1024*1024/$DUR - 192000 - 60000)/1000))")   # kb/s for video
    OUT=$R/dorylinae-beta-$asp-${fps}fps.mp4
    ffmpeg -hide_banner -loglevel error -y -i $M -vf "$VF" -r $fps -c:v libx264 $X264 -b:v ${VB}k -pass 1 -passlogfile $R/p-$asp-$fps -an -f mp4 /dev/null
    ffmpeg -hide_banner -loglevel error -y -i $M -i $A/mix-$asp.wav -map 0:v -map 1:a -vf "$VF" -r $fps -c:v libx264 $X264 -b:v ${VB}k \
      -maxrate $((VB*2))k -bufsize $((VB*3))k -pass 2 -passlogfile $R/p-$asp-$fps $TAGS -c:a aac -b:a 192k -ar 48000 -movflags +faststart -shortest $OUT
    rm -f $R/p-$asp-$fps*
    echo "$(basename $OUT) $(du -m $OUT | cut -f1) MB"
  done
  node stills.mjs --aspect $asp --times 52.2,7.2 --out $R/stills-$asp >/dev/null
  cp $R/stills-$asp/${asp}_0052.200.png $R/poster-$asp.png
  cp $R/stills-$asp/${asp}_0007.200.png $R/poster-alt-$asp.png
  rm -rf $R/stills-$asp
  rm -f $M     # the lossless master (several GB) is not kept; master-$asp-hq.mp4 is the archive copy
done
ls -la $R
echo RELEASE DONE
