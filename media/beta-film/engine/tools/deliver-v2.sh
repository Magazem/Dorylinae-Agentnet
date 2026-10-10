#!/usr/bin/env bash
# v2 delivery ("The envelope"): lossless masters (with motion blur) -> platform files and poster frames.
#   tools/deliver-v2.sh [version]        e.g. tools/deliver-v2.sh v2-r1
# Output: out/release-<version>/
#   dorylinae-beta-16x9-60fps.mp4   X (and any 60 fps player)              <= 29 MB, 2-pass H.264 High
#   dorylinae-beta-4x5-60fps.mp4    X mobile feed                          <= 29 MB
#   dorylinae-beta-16x9-30fps.mp4   LinkedIn, Reddit (30 fps cap)          <= 29 MB, rendered natively at 30 fps (180° shutter)
#   dorylinae-beta-4x5-30fps.mp4    LinkedIn, Reddit mobile feed           <= 29 MB
#   master-16x9-hq.mp4 / master-4x5-hq.mp4   archive quality (CRF 14, 60 fps)
#   poster-*.png (27.143 s, the wait at the relay), poster-alt-*.png (18.571 s, the address form close-up)
# The 30 fps files have their own lossless master, rendered natively at 30 fps (fix-r3, review r2: averaging pairs of 60 fps
# frames, `tmix=frames=2`, is not a 180° shutter at 30 fps: each pair was two separate 180° exposures 1/60 s apart, so
# every unblurred move showed two sharp copies 15 – 18 px apart (`RRelay`, doubled terminal lines) and every blurred one
# two dashes per star; the 21° wipe a 235 px band of both shots at 50 %). render.mjs --fps 30 exposes each frame over
# SHUTTER / fps = 1/60 s (180° at 30 fps), keeps sub-frames on their side of every hard cut, and adds the scene's 30 fps
# blur windows (CUES.blur entries [t0, t1, 30]: moves that pass 19 px a frame only at 30 fps), with 32 sub-frames.
# Keyframes sit on the cuts only (SCRIPT-v2 §2: frames 0, 343, 2400, 2786, 3428), so no I-frame "pulse" lands inside
# the still scene. Hard cuts sit on even 60 fps frames, so each falls exactly on a 30 fps frame (2k / 60 = k / 30): the
# cut lands on the same instant in both files (on an odd frame the 30 fps cut would come 1/60 s late); the check
# below reads the cut frames from the film's own cue file and stops if one is odd. Only shots that replace the picture
# are cuts: the overlay shots (the caption layers I-S1 and II-cap, z 5, from G7.5 = frame 321.43) start over another shot
# and are left out (dirfix: counted as a cut, frame 321 failed the check and would have forced a keyframe at 5.36 s).
set -euo pipefail
cd "$(dirname "$0")/.."
V=${1:-v2}
R=out/release-$V
A=../audio2/out
mkdir -p "$R"
DUR=64.2857
VF709="zscale=rangein=full:range=limited:primariesin=709:primaries=709:transferin=709:transfer=709:matrix=709:dither=error_diffusion,format=yuv420p"
TAGS="-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv"

for asp in 16x9 4x5; do
  [ -f out/v2-cues-$asp.json ] || { echo "missing out/v2-cues-$asp.json (export the cues first)"; exit 1; }
  # cut frames from the cue file; hard cuts (every cut but the 21° wipe at frame 343) must be even
  CUTS=$(python3 - "$asp" <<'PY'
import json, sys
c = json.load(open(f"out/v2-cues-{sys.argv[1]}.json"))
# overlays: layers drawn over another shot (a z / overlay flag if the cue file carries one; these ids as exported now)
OVERLAYS = {"I-S1", "II-cap"}
overlay = lambda s: s["id"] in OVERLAYS or bool(s.get("z")) or bool(s.get("overlay"))
cuts = sorted({round(s["start"] * 60) for s in c["shots"] if not overlay(s)})
bad = [f for f in cuts if f % 2 and f != 343]
if bad: sys.exit(f"odd hard-cut frames {bad}: the 30 fps file would cut 1/60 s later than the 60 fps one")
# a hair before each cut frame's time: printed to 6 places, 343 / 60 would round up past frame 343 and the keyframe would
# land on 344 (-force_key_frames takes the first frame at or after the time)
print(",".join(f"{max(0.0, (f - 0.01) / 60):.6f}" for f in cuts))
PY
)
  echo "$asp cuts: $CUTS"
  # keyint 2400 (40 s at 60 fps) is longer than the longest shot (the scene, 34.3 s), so keyframes fall only on the cuts:
  # with keyint 600 the encoder added one every 10 s inside the still scene and the note hold (15.7, 25.7, 35.7, 56.4 s),
  # and each one shifted ~26,000 sky pixels by 2-6 levels for a frame (a one-frame shimmer)
  X264="-preset slow -profile:v high -level:v 4.2 -force_key_frames $CUTS -x264-params keyint=2400:min-keyint=1:scenecut=0:aq-mode=3"
  M=out/master-$asp-$V.mkv; M30=out/master30-$asp-$V.mkv
  [ -f $M ] || node render.mjs --aspect $asp --cut v2 --codec ffv1 --workers 3 --blur-samples 16 --out $M --stats out/render-stats-v2.jsonl
  # 32 sub-frames at 30 fps: the shutter is twice as long, so this keeps the 60 fps master's spacing between sub-frames
  # (16 left a visible ladder of 2.4 px steps in the return's 38 px smears; qa/dirfix/fix-r3/r30)
  [ -f $M30 ] || node render.mjs --aspect $asp --cut v2 --fps 30 --codec ffv1 --workers 3 --blur-samples 32 --out $M30 --stats out/render-stats-v2.jsonl
  # archive quality
  ffmpeg -hide_banner -loglevel error -y -i $M -i $A/mix-$asp.wav -map 0:v -map 1:a -vf "$VF709" -c:v libx264 $X264 -crf 14 $TAGS \
    -c:a aac -b:a 320k -ar 48000 -movflags +faststart -shortest $R/master-$asp-hq.mp4
  # upload files: 2-pass to fit under 29 MB (audio 192 kb/s)
  for fps in 60 30; do
    # each frame rate from its own master (30 fps: the native 30 fps render, its own 180° shutter; no frame blending)
    if [ $fps = 30 ]; then SRC=$M30; else SRC=$M; fi
    VF="$VF709"
    VB=$(python3 -c "print(int((29*8*1024*1024/$DUR - 192000 - 60000)/1000))")   # kb/s for video
    OUT=$R/dorylinae-beta-$asp-${fps}fps.mp4
    ffmpeg -hide_banner -loglevel error -y -i $SRC -vf "$VF" -r $fps -c:v libx264 $X264 -b:v ${VB}k -pass 1 -passlogfile $R/p-$asp-$fps -an -f mp4 /dev/null
    ffmpeg -hide_banner -loglevel error -y -i $SRC -i $A/mix-$asp.wav -map 0:v -map 1:a -vf "$VF" -r $fps -c:v libx264 $X264 -b:v ${VB}k \
      -maxrate $((VB*2))k -bufsize $((VB*3))k -pass 2 -passlogfile $R/p-$asp-$fps $TAGS -c:a aac -b:a 192k -ar 48000 -movflags +faststart -shortest $OUT
    rm -f $R/p-$asp-$fps*
    echo "$(basename $OUT) $(du -m $OUT | cut -f1) MB"
  done
  node stills.mjs --aspect $asp --cut v2 --times 27.143,18.571 --out $R/stills-$asp >/dev/null
  cp $R/stills-$asp/${asp}_0027.143.png $R/poster-$asp.png
  cp $R/stills-$asp/${asp}_0018.571.png $R/poster-alt-$asp.png
  rm -rf $R/stills-$asp
  [ "${KEEP_MASTER:-0}" = 1 ] || rm -f $M $M30   # the lossless masters (several GB) are not kept; master-$asp-hq.mp4 is the archive copy
done
ls -la $R
echo RELEASE DONE
