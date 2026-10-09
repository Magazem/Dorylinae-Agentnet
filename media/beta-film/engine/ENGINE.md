# Film engine: how to build a shot

Folder: `film/engine/` (absolute: /tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/engine).
The spec you build from is `../design/SCRIPT.md`. Read its **DIRECTOR'S AMENDMENTS** first: they override the rest.

## Contract

- `film.html?format=16x9` (1920×1080) or `?format=4x5` (1080×1350) is the whole film. Nothing animates by itself:
  `window.renderAt(t)` sets every pixel as a pure function of `t`. **No CSS animations/transitions, no timers, no
  rAF, no Math.random, no Date.** Randomness only via `FILM.rng(seed)`.
- Each shot is one file `shots/sNN.js` that calls `FILM.shot({ id, start, end, pre, post, enter, build(root) })`.
  `build(root)` creates DOM once and returns `render(tLocal, tAbs)` where `tLocal = t − start`.
  A shot with `post: 0.6` holds its last frame for 0.6 s (it sits under the next shot's 21° cut).
  A shot with `enter: 'cut21'` is revealed by the 21° edge over its first 0.6 s (the engine does the clip).
- Branch on format with `FILM.FMT.portrait` (true in 4:5). Every shot must be laid out separately for both formats
  (no cropping one into the other).
- Only edit your own files. Never edit `lib/*`, `film.html`, `shots/common.js`, `shots/manifest.js` or other agents'
  shots. If you need a helper, put it in your own file.

## API (globals on `FILM`)

Core (`lib/core.js`): `FMT {W,H,portrait,zoom,safe}`, `Z` (zoom 2 or 3), `C` (brand colour tokens: bg, surface,
surface2, line, lineStrong, text, text2, text3, brand, accent, highlight, online, away, sting, violet, scanBase,
scanLight, stock, ink, inkMeta), `FONT {display,sans,mono}`, `EASE` (brand ease), `SWEEP`, `LINEAR`, `clamp`, `lerp`,
`seg(t, start, dur, ease=EASE)` → 0..1, `env(...)`, `rng(seed)`, `h(tag, attrs, ...kids)` (HTML), `s(tag, attrs,
...kids)` (SVG), `css(el, {prop: value})` (cached style set), `op(x)` (opacity string), `cue(tAbs, kind, extra)`.

Kit (`lib/kit.js`):
- `ui.text(str, {x, baseline, face:'display'|'sans'|'mono', size, weight, colour, anchor:'start'|'middle'|'end',
  tracking, upper, typed:{at, step=0.085}, enter:{at, dur=0.24, rise(brand px)=6}, out:{at, dur}, spans:[{text,colour}]})`
  → `{el, width, x0, x1, render(t), times}`. Places text by **left edge and baseline** exactly (fonts are measured).
  `times` are the typed letters' times (register sound cues for labels with them).
- `ui.underline(svg, {x0, x1, y, at, dur=0.24, outAt, outDur=0.12, colour, k})` the pheromone dotted underline
  (6 px dots / 24 px pitch in 16:9, 9 / 36 in 4:5).
- `ui.dot(svg, {cx, cy, d, stroke, level:(t)=>0..1 (0 = hollow ring, 1 = solid online), pulses:[t], arrivals:[t], fade:(t)=>0..1})`
  presence dot with halo pulses. `FILM.levelFill(at, off?)` gives a 120 ms ring-fill level function.
- `ui.sky(parent, {name, w, h, follow})` → `render(cam)`. Prefer `FILM.S.sky(parent, 'w'|'maya'|'theo'|'inv'|'end')`.
- `ui.termText(rows, {x, baseline, size, pitch, dimAt, dimTo=0.4, tracking})` rows `[{parts:[{text, colour}], at, keep}]`.
  Each row lands whole (120 ms). `keep: true` rows do not dim.
- `ui.trail(svg, d, {draw:(t)=>0..1, march:(t)=>px, fade:(t)=>0..1, tailFade:(t)=>0..1, colour, k})` world trail along
  an SVG path `d` (dots 3 brand px every 12 brand px × k). `march` returns an offset in px (use pitch × beats in flight).
- `ui.typed`, `ui.label(lines, {x, y, size(brand px), at, step, k, meta, typedIn})` specimen label on label stock.
- Cameras: `FILM.worldCam(worldEl)` → `apply({x, y, s})`; `FILM.drift({t0, t1, s0, s1, about:[x,y]})`;
  `FILM.push({P, Q0, Q1, s0, s1, t0, dur})` (SCRIPT's push formula); `FILM.camSeq([[t, fn], ...])`.
- `FILM.boot(container, {width})` (async) → `{el, render(t)}` the brand boot sequence, frame-exact.
- `FILM.cut21(root, u)` (the engine already applies it for `enter: 'cut21'`), `FILM.TAN21`.
- `FILM.measure(text, family, size, weight, tracking)` and `FILM.baselineOffset(family, size, weight)`.

Shared styles (`shots/common.js`, `FILM.S`): `S.P` (portrait?), `S.ROLE` (sizes), `S.RADIUS`, `S.CHANNEL`, `S.LOCK`
(frame-px lock offsets), `S.caption(str, {line, x, baseline, spans, typed, enter, out})`, `S.tag({enter, out})`
("Apply at dorylinae.net" at its fixed place), `S.collector(who, 'hh:mm', {x, baseline})` (MAYA · 13:41 UTC),
`S.plate(x0, y0, x1, y1, fill)`, `S.overlay(parent, z)` (full-frame SVG), `S.sky(parent, place)`,
`S.lock(el, at, dxFramePx, dyFramePx, dur=0.5)` (plate lock-in).

Layering inside a shot root: sky (bottom) → `world` div (camera-transformed: plates, terminal text, trails that move
with the camera) → screen layer (captions, tag, collector labels, screen underlines). Build the world at camera s = 1
coordinates as the script gives them; apply the camera with `worldCam`.

Sound cues: register every sound event of your shots from SCRIPT.md §5.3 with `FILM.cue(tAbs, KIND, {pan, note, fmt})`
using the palette names (ROOM, PASTE, THUMP, PAD, PIANO, MOTIF, LUB, DUB, TICK, DYAD, LAND, PLINK, THOCK, LABEL,
DRONE, LOW, GLASS). For typed label letters, register one LABEL per letter from the component's `times` (absolute =
shot start + local). Format-specific cues: pass `{fmt: FILM.FMT.name}`.

## Preview and check (do this; look at the images)

```
cd film/engine
node stills.mjs --aspect 16x9 --times 3.7,7.2,9.0 --out out/review-S2           # PNGs at exact times
node stills.mjs --aspect 4x5  --every 0.3 --from 3.6 --to 9.6 --out out/rv --sheet out/rv/sheet.png --cols 5 --width 360
node stills.mjs --aspect 16x9 --times 7.2 --debug --out out/dbg                   # adds a time/bar readout
node render.mjs --aspect 16x9 --start 3.6 --dur 6 --out out/S2-169.mp4             # real-time clip (60 fps)
```

Open the PNGs with your Read tool and look at them critically: alignment to the script's coordinates, safe areas, text
never on bright nebula, one pheromone element per frame, nothing clipped unintentionally, typing timing, the plates'
lock-in. Use `--every` sheets to check motion over time. Fix until it is right in BOTH formats. Console errors are
printed by the tools; there must be none.

Skies are in `assets/sky-*.png` (generated from the brand code, 2× resolution). The brand logo SVGs are in `assets/`
and inlined in `FILM.assetText['assets/<name>.svg']`.
