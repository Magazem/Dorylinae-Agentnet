// S7 · The cause · 33.600 → 38.400 (15:1 → 17:1), 4.8 s. SCRIPT.md S7 and DIRECTOR'S AMENDMENT 2.
// The payoff: their agent does the work and sends its result; the product's own output is pushed to hero size.
// Sky: theo's machine (seed 6333; 4:5 the §3.8 fallback, seed 7333).
FILM.shot({ id: 'S7', start: 33.6, end: 38.4, build(root) {
  const F = FILM, { h, C, S, FONT, css, op, clamp } = F;
  const P = S.P;
  root.style.background = C.bg;
  // Sky (SCRIPT.md §3.8): theo's machine. S6 and S7 share it exactly (same place, hard cut) and it does not follow the
  // camera (follow 0): the stars do not jump at the 33.600 cut, and the collector (the time-compression cue) keeps the
  // sky it was checked on through S7's push. 16:9: seed 6333; 4:5: the §3.8 fallback seed 7333 (6333 put rose under
  // S6's trail and its brightest nebula under the empty lower half). Both PNGs are the brand field with the countable
  // stars left out around the screen-fixed labels (collector: every star within 8 px of the glyphs and the visible
  // ones, core r >= 0.8 px, within 40 px of its ends; captions, present through both shots: the visible ones), so no
  // star glints in `THEO` or sits in a caption like a stray full stop. Nebula, dust and vignette untouched
  // (scratchpad/s1347tools/gen-sky-clear.mts, zones for this exact placement, so a new SKY means regenerating the
  // PNGs). Placed so the captions, the collector and the trail sit on dark sky (4:5 trail column x 920-960, y 0-370),
  // and in 4:5 the lower half (y 660-1350) on the dark blue side.
  const SKY = P ? { name: 'sky-theo-clear-45.png', flip: 'y', off: [-72, -131] } : { name: 'sky-theo-clear-169.png', flip: 'y', off: [103, -67] };
  const sky = placeSky(SKY, 0);

  // Terminal geometry (world at camera s = 1). The plate bleeds right far enough to stay under the text after the push.
  // 4:5: the plate also bleeds off the bottom (script y1 1150), like S4's 4:5 plate: at 1150 a band of sky showed under the
  // empty plate at the cut, and the pan (+236 px down) swept the plate's lower edge out of frame.
  // driftAt: the post-push drift's fixed point. 16:9 anchors it on the token's left baseline (script: its centre) so the
  // token and underline keep x 96, the caption/collector column and title-safe; about the centre they slid to x ≈ 83.
  // 4:5 keeps the centre: both ends stay inside safe (x 65 → 1008).
  const L = P
    ? { plate: [-24, 340, 1800, 1600], x: 64, b: 410, size: 36, pitch: 54, Q1: [72, 700], s1: 1, s2: 1.015, ulGap: 24, driftAt: 'centre' }
    : { plate: [64, 330, 2400, 1110], x: 112, b: 410, size: 42, pitch: 64, Q1: [96, 600], s1: 64 / 42, s2: 1.55, ulGap: 26, driftAt: 'start' };
  const adv = L.size * 0.6;                            // Plex Mono advance (0.6 em)

  // Terminal text: capture/21-quarantine-release.txt (theo's agent submitting its result). Edits:
  // s-b3098b5fd25dd5ec2aa2b78fe588fc03 shortened to s-b309… (twice); command broken with ` \`.
  const CMD = 33.9, OUT = 34.02;
  const TOKEN = 'Cause found: two tests share one ledger row';
  // The empty prompt is present at the cut (its 120 ms fade ends on the cut frame).
  // At 34.200 the summary row's quotes and the output dim to 40 % (script). The other two command rows dim to 0 instead, as
  // S4's do: at rest they are only clipped grey fragments at the frame's left edge (`et result s-b309… --status pass \`,
  // `laky-finding.md`), which crowd the payoff frame. Leaving lines out is an allowed edit (§7).
  const GONE = 0;
  const rows = [
    [{ text: 'theo$', c: C.brand, at: 33.6 - 0.12, dimTo: GONE }, { text: ' agentnet result s-b309… --status pass \\', c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: "    --summary '", c: C.text2, at: CMD, dimTo: 0.14 }, { text: TOKEN, c: C.text, at: CMD, keep: true }, { text: "' \\", c: C.text2, at: CMD, dimTo: 0.14 }],
    [{ text: '    --file flaky-finding.md', c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: 'Submitted result for s-b309… (open)', c: C.text3, at: OUT, dimTo: 0.14 }],
  ];
  const term = terminal(rows, { x: L.x, baseline: L.b, size: L.size, pitch: L.pitch, dimAt: 34.2 });

  // Noticed token: characters 15 to 57 of line 2. Pt = its start (left edge, baseline) in world.
  const Pt = [L.x + 15 * adv, L.b + 1 * L.pitch];
  const tokW = TOKEN.length * adv;
  // Drift anchor A (see L.driftAt): 16:9 the token's left baseline, 4:5 the token's centre (centre of its width, middle
  // of the cap height).
  const A = L.driftAt === 'start' ? [Pt[0], Pt[1]] : [Pt[0] + tokW / 2, Pt[1] - 0.698 * L.size / 2];

  const world = h('div');
  // worldCamStable: a fixed, invisible 0.05° roll about the drift anchor, so the payoff line glides through the slow
  // drift on sub-pixel positions instead of snapping 1 px at a time (QA glitch), through the push as well.
  const cam = F.worldCamStable(world, A);
  world.append(S.plate(...L.plate), term.el);

  // Push 34.200 → 35.400 (2 beats). 16:9: s 1 → 1.5238, token to 64 px at (96, 600). 4:5: pan only (s 1). Camera ease
  // (FILM.CAM, the boot's sweep 0.4, 0, 0.6, 1): it starts and ends at rest.
  const push = F.push({ P: Pt, Q0: Pt, Q1: L.Q1, s0: 1, s1: L.s1, t0: 34.2, dur: 1.2, ease: F.CAM });
  // Drift 35.400 → 38.400: s1 → s2 about A, also FILM.CAM, so it starts from rest where the push stops (no speed jump or
  // bounce at the handover).
  const SA = [L.Q1[0] + L.s1 * (A[0] - Pt[0]), L.Q1[1] + L.s1 * (A[1] - Pt[1])];
  const drift = F.push({ P: A, Q0: SA, Q1: SA, s0: L.s1, s1: L.s2, t0: 35.4, dur: 3.0, ease: F.CAM });
  const camAt = F.camSeq([[0, push], [35.4, drift]]);
  // Motion blur (render.mjs: 180°, 8 sub-frames, linear light) over the whole push / pan (eased, it peaks at ≈ 13 px per
  // frame in 16:9 and 9 in 4:5, under the 19 px threshold, but it is the shot's one big move and keeps a filmic trail);
  // it fades to nothing with the speed at both ends, so the held frames either side stay sharp.
  // director: no blur on S7 (peaks 14 / 9 px/frame, under the 19 px rule)

  // 35.400: pheromone underline under the token, built in the world at 1/s1 so it is exactly the screen underline at the
  // push's end (16:9 x 96 → 1728 y 612, 69 dots; 4:5 x 72 → 972 y 716, 26 dots) and stays on the words through the drift.
  const pitchScr = 12 * F.Z;
  const ulLen = Math.floor((tokW * L.s1) / pitchScr + 1e-6) * pitchScr;
  const wsvg = S.overlay(world);
  // director: centre the dot run under the token; the gap clears the descenders (g in 'ledger')
  const ulOff = (tokW * L.s1 - ulLen) / 2 / L.s1;
  const ul = F.ui.underline(wsvg, { x0: Pt[0] + ulOff, x1: Pt[0] + ulOff + ulLen / L.s1 + 0.01, y: Pt[1] + L.ulGap / L.s1, at: 35.4, k: 1 / L.s1 });

  // ---- screen: caption (amendment 2) and collector, present at the cut
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  const c10 = S.caption('Their agent does the work.');
  const coll = S.collector('theo', '13:43');
  screen.append(c10.el, coll.el);
  root.append(world, screen);

  // Sound (SCRIPT.md §5.3). The 33.500 Gmaj9 is this shot's pre-lap (J-cut), so it is registered here.
  F.cue(33.5, 'PIANO', { note: 'Gmaj9 G2 D3 A3 B3 F#4', roll: 0.03, desc: 'pre-lap (J-cut) into S7' });
  F.cue(33.6, 'LUB', { desc: 'maya pulse' });
  F.cue(33.9, 'LAND', { desc: 'result command lands' });
  F.cue(33.9, 'DUB', { desc: 'theo pulse' });
  F.cue(35.4, 'MOTIF', { note: 'F#4', desc: 'the cause is underlined (motif note 2 of 3)' });
  F.cue(35.4, 'GLASS', { note: 'E6' });
  F.cue(36.0, 'LUB', { desc: 'maya pulse' });
  F.cue(36.0, 'PIANO', { note: 'Dadd9 D3 A3 E4 F#4', roll: 0.03, desc: 'the G -> D cadence' });
  F.cue(36.3, 'DUB', { desc: 'theo pulse' });

  return (tl, t) => {
    const c = camAt(t);
    cam(c);
    sky.render(c);
    term.render(t);
    ul.render(t);
  };

  /** Terminal rows by first baseline and pitch; each part lands whole (opacity 0 → 1, 120 ms linear) at its own time,
   *  and everything not marked keep dims to its dimTo (default 40 %) over 120 ms at dimAt. */
  function terminal(rowsIn, o) {
    const off = F.baselineOffset(FONT.mono, o.size, 400);
    const el = h('div', { style: 'position:absolute;left:0;top:0' });
    const parts = [];
    rowsIn.forEach((row, i) => {
      const r = h('div', { style: `position:absolute;left:${o.x}px;top:${(o.baseline + i * o.pitch - off).toFixed(2)}px;white-space:pre;font-family:${FONT.mono};font-weight:400;font-size:${o.size}px;line-height:${o.size}px` });
      for (const p of row) { const sp = h('span', { style: `color:${p.c};opacity:0`, text: p.text }); r.append(sp); parts.push({ sp, ...p }); }
      el.append(r);
    });
    return {
      el,
      render(t) {
        for (const p of parts) {
          let a = clamp((t - p.at) / 0.12) * (p.alpha ?? 1);
          if (o.dimAt != null && !p.keep) a *= 1 - (1 - (p.dimTo ?? 0.4)) * clamp((t - o.dimAt) / 0.12);
          css(p.sp, { opacity: op(a) });
        }
      },
    };
  }
  /** The place's sky, optionally mirrored (SCRIPT.md §3.8: mirror the PNG when the nebula's rose would sit under text).
   *  The mirror is applied by a wrapper about the frame centre, and the camera is mirrored too, so the parallax keeps its
   *  direction. follow: the sky's share of the camera move (script default 0.3). o.off: a shift of the PNG (display px,
   *  before the mirror) inside its 1.2× margin, to slide a dark band under the text column. Display size: §3.8. */
  function placeSky(o, follow) {
    const { W, H } = F.FMT;
    const fx = o.flip.includes('x'), fy = o.flip.includes('y');
    const box = h('div', { style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px;transform-origin:50% 50%;transform:scale(${fx ? -1 : 1}, ${fy ? -1 : 1}) translate(${o.off[0]}px, ${o.off[1]}px)` });
    root.append(box);
    const [w, hh] = P ? [1296, 1620] : [2304, 1296];
    const sk = F.ui.sky(box, { name: o.name, w, h: hh, follow });
    return { render: (c) => sk.render({ x: fx ? W - c.x : c.x, y: fy ? H - c.y : c.y, s: c.s }) };
  }
} });
