// S4 · Offline · 13.200 → 19.200 (6:3 → 9:1), 6.0 s. SCRIPT.md S4.
// The request leaves while theo is away, and the product itself says so. No caption: the product speaks.
// Sky: maya's machine (seed 4968), same place as S3.
FILM.shot({ id: 'S4', start: 13.2, end: 19.2, build(root) {
  const F = FILM, { h, C, S, FONT, css, op, clamp } = F;
  const P = S.P;
  root.style.background = C.bg;
  // Sky (SCRIPT.md §3.8): maya's machine, seed 4968, both formats. S3 and S4 share it exactly (same place, hard cut)
  // and it does not follow the camera (follow 0), so the stars do not jump at the 13.200 cut and every label keeps the
  // sky it was checked on. The PNGs are the brand field with the countable stars left out around the screen-fixed
  // labels: every star within 8 px of the collector, file name and tag glyphs and the visible ones (core r >= 0.8 px)
  // within 40 px of their ends, so no star reads as a bullet, a full stop or an accent in them; on the caption only the
  // bright ones (r >= 1.75 px), because S4 shows that sky without the caption and a wider clearing would read as an
  // empty lane. Nebula, dust and vignette are untouched (scratchpad/s1347tools/gen-sky-clear.mts; zones from
  // s1347tools/zones.py for this exact placement, so a new SKY means regenerating the PNGs). Placement measured on the
  // PNG: no rose under any label (16:9: the caption's `writes the` sits on a faint violet edge, median R-G 14). The
  // channels between the sub-plates are dark in 16:9 (median ≈ 30, 20, 41); in 4:5 they are dark under the brief's left
  // half, but no mirror or shift of seed 4968 (or 5968, which puts rose under the text) darkens their right half too.
  const SKY = P ? { name: 'sky-maya-clear-45.png', flip: 'xy', off: [-104, -111] } : { name: 'sky-maya-clear-169.png', flip: 'x', off: [-120, 106] };
  const sky = placeSky(SKY, 0);

  // Terminal geometry (world at camera s = 1). The plate bleeds right far enough to stay under the text after the push.
  // 4:5: the plate also bleeds off the bottom (script y1 1150), as in 16:9: at 1150 its edge sat 24 px above the tag and the
  // push swept it down through the tag's letters. Bled, the tag sits on `surface` from the cut to the end.
  // driftAt: the post-push drift's fixed point. 16:9 anchors it on the token's left baseline (script: its centre) so the
  // token and underline keep x 96, the caption/collector column and title-safe; about the centre they slid to x ≈ 81.
  // 4:5 keeps the centre: both ends stay inside safe (x 66 → 1014), where a left anchor would push the end to 1020.
  const L = P
    ? { plate: [-24, 340, 2400, 1500], x: 64, b: 410, size: 36, pitch: 54, Q1: [72, 700], s1: 40 / 36, s2: 1.125, ulGap: 24, driftAt: 'centre' }
    : { plate: [64, 330, 2800, 1110], x: 112, b: 410, size: 42, pitch: 64, Q1: [96, 600], s1: 64 / 42, s2: 1.555, ulGap: 26, driftAt: 'start' };
  const adv = L.size * 0.6;                            // Plex Mono advance (0.6 em)

  // Terminal text: capture/15-offline-delivery.txt. Edits: command broken at its flags with ` \` and a 4-space indent;
  // r-97ae9a37feaf51fcf0c3c8246ae57be1 shortened to r-97ae…. Every flag kept.
  const CMD = 13.5, OUT = 13.8;
  const TOKEN = 'it will be delivered when theo is back.';
  // The empty prompt is present at the cut (its 120 ms fade ends on the cut frame).
  // At 14.400 the output dims to 40 % (script), and the four command rows dim to 0 instead: during the push they cross the
  // screen-fixed collector label (16:9, rows 0 and 1 at 14.50 → 14.83), and at rest only a lone `\` of them is left in frame.
  const GONE = 0;
  const rows = [
    [{ text: 'maya$', c: C.brand, at: 13.2 - 0.12, dimTo: GONE }, { text: ' agentnet request theo task \\', c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: "    --title 'Track down the flaky payments test' \\", c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: '    --brief-from-file flaky-brief.md \\', c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: '    --idempotency-key demo-flaky-1', c: C.text2, at: CMD, dimTo: GONE }],
    [{ text: 'Queued normal task request r-97ae… to theo (team atlas)', c: C.text3, at: OUT, dimTo: 0.14 }],
    [{ text: '  theo is offline, last seen 2026-10-08T13:41:37Z; ', c: C.text3, at: OUT + 0.042, dimTo: 0.14 }, { text: TOKEN, c: C.text, at: OUT + 0.042, keep: true }],
  ];
  const term = terminal(rows, { x: L.x, baseline: L.b, size: L.size, pitch: L.pitch, dimAt: 14.4 });

  // Noticed token: characters 51 to 89 of the last line. P = its start (left edge, baseline) in world.
  const Pt = [L.x + 51 * adv, L.b + 5 * L.pitch];
  const tokW = TOKEN.length * adv;
  // Drift anchor A (see L.driftAt): 16:9 the token's left baseline, 4:5 the token's centre (centre of its width, middle
  // of the cap height).
  const A = L.driftAt === 'start' ? [Pt[0], Pt[1]] : [Pt[0] + tokW / 2, Pt[1] - 0.698 * L.size / 2];

  const world = h('div');
  // worldCamStable: a fixed, invisible 0.05° roll about the drift anchor, so the token glides through the slow drift on
  // sub-pixel positions instead of snapping 1 px at a time (QA glitch), through the push as well.
  const cam = F.worldCamStable(world, A);
  world.append(S.plate(...L.plate), term.el);

  // Push 14.400 → 15.600 (2 beats): P moves from its screen position to Q1 as s goes 1 → s1 (FILM.push). Camera ease
  // (FILM.CAM, the boot's sweep 0.4, 0, 0.6, 1): it starts and ends at rest, so it neither jumps off the held frame nor
  // hands the drift a speed it has to drop.
  const push = F.push({ P: Pt, Q0: Pt, Q1: L.Q1, s0: 1, s1: L.s1, t0: 14.4, dur: 1.2, ease: F.CAM });
  // Drift 15.600 → 19.200: s1 → s2 about A, also FILM.CAM, so it starts from rest where the push stops (no speed jump or
  // bounce at the handover).
  const SA = [L.Q1[0] + L.s1 * (A[0] - Pt[0]), L.Q1[1] + L.s1 * (A[1] - Pt[1])];
  const drift = F.push({ P: A, Q0: SA, Q1: SA, s0: L.s1, s1: L.s2, t0: 15.6, dur: 3.6, ease: F.CAM });
  const camAt = F.camSeq([[0, push], [15.6, drift]]);
  // Motion blur (render.mjs: 180°, 8 sub-frames, linear light) over the whole push. Even eased, the plate edge and the
  // terminal text peak above 19 px per frame mid-push (measured: 16:9 39, 4:5 27); the blur fades to nothing with the
  // speed at both ends, so the held frames either side stay sharp.
  F.blur(14.4, 15.6);

  // 15.600: pheromone underline under the token. It is built in the world at 1/s1 so that at the push's end it is exactly
  // the screen underline (6 px dots / 24 px pitch, 4:5 9 / 36), x 96 → 1584 y 612 (4:5 72 → 1008 y 716), and it stays on
  // the words through the drift.
  const pitchScr = 12 * F.Z;
  const ulLen = Math.floor((tokW * L.s1) / pitchScr + 1e-6) * pitchScr;
  const wsvg = S.overlay(world);
  // director: centre the dot run under the token (it is a whole number of pitches, so it is shorter than the token)
  const ulOff = (tokW * L.s1 - ulLen) / 2 / L.s1;
  const ul = F.ui.underline(wsvg, { x0: Pt[0] + ulOff, x1: Pt[0] + ulOff + ulLen / L.s1 + 0.01, y: Pt[1] + L.ulGap / L.s1, at: 15.6, k: 1 / L.s1 });

  // ---- screen: tag and collector
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  const tag = S.tag();
  const coll = S.collector('maya', '13:41');
  screen.append(tag.el, coll.el);
  root.append(world, screen);

  // Sound (SCRIPT.md §5.3)
  F.cue(13.5, 'LAND', { desc: 'the command lands' });
  F.cue(14.4, 'LUB', { desc: 'maya pulse; push to the token' });
  F.cue(14.4, 'PIANO', { note: 'Asus4 A2 E3 D4', roll: 0.03, hold: true, desc: 'offline revealed' });
  F.cue(14.4, 'PAD', { action: 'lowpass', from: 2000, to: 900, dur: 2.4, desc: 'the pad darkens over 1 bar' });
  F.cue(16.8, 'LUB', { desc: 'maya pulse' });

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
          if (o.dimAt != null && !p.keep) a *= 1 - (1 - (p.dimTo ?? o.dimTo ?? 0.4)) * clamp((t - o.dimAt) / 0.12);
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
