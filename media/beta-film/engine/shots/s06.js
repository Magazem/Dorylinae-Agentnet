// S6 · Inbox · 30.000 → 33.600 (13:3 → 15:1), 3.6 s. SCRIPT.md S6.
// It lands in theo's inbox: the same title the viewer saw in maya's command. The midpoint (31.2 s) falls here.
// Sky: theo's machine (seed 6333; 4:5 the §3.8 fallback, seed 7333).
FILM.shot({ id: 'S6', start: 30.0, end: 33.6, build(root) {
  const F = FILM, { h, C, S, FONT, css, op, clamp, seg } = F;
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

  // Layout (world at camera s = 1).
  // 4:5: the plate bleeds right to x 2400 (was 1104) so it stays under the title after the pan (crop/pan only: the rows
  // are unchanged). ul: the title's underline (see the pan below); ulAt its draw time.
  const L = P
    ? { plate: [-24, 380, 2400, 640], x: 64, b: 440, size: 36, pitch: 54, trail: 'M 940 -36 L 940 368', ul: title4x5(), ulAt: 31.8, about: [136, 602] }
    : { plate: [184, 400, 1984, 664], x: 232, b: 470, size: 32, pitch: 50, trail: 'M -24 612 L 172 612', ul: { x0: 1019.2, x1: 1672.0, y: 640 }, ulAt: 31.2, about: [1345, 612] };
  /** 4:5 underline under the followed title (row 3, characters 41 to 74, Plex Mono 36: advance 21.6): the spec's dot
   *  count (floor(w / 36) + 1 = 21 dots) with the run centred under the title (as S4 / S7 centre theirs), y 628. */
  function title4x5() {
    const x0 = 64 + 41 * 21.6, w = 34 * 21.6, run = Math.floor(w / 36 + 1e-6) * 36, off = (w - run) / 2;
    return { x0: x0 + off, x1: x0 + off + run, y: 628 };
  }

  // Inbox: capture/15-offline-delivery.txt, theo's inbox right after his daemon restarted. Edits: ids shortened to
  // r-d0d8… and r-97ae…, the ID column re-padded to the shortened width; order exactly as captured.
  const CMD = 30.6;
  // The empty prompt is present at the cut (its 120 ms fade ends on the cut frame). Script: "Row 1 in text-3 at 60 %; row 2's
  // title in text", read with the output rows counted from 0 under the header: the other request (r-d0d8…) sits at 60 %,
  // and the followed request's title (r-97ae…, the one the underline marks) is in text, in both formats (4:5 pans to it).
  const rows = [
    [{ text: 'theo$', c: C.brand, at: 30.0 - 0.12 }, { text: ' agentnet inbox', c: C.text2, at: CMD }],
    [{ text: 'ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE', c: C.text3, at: 30.72 }],
    [{ text: 'r-d0d8…  maya  task  normal   2000  1m   Add queue settings to the staging runbook', c: C.text3, at: 30.762, alpha: 0.6 }],
    [{ text: 'r-97ae…  maya  task  normal   2000  0m   ', c: C.text3, at: 30.804 }, { text: 'Track down the flaky payments test', c: C.text, at: 30.804 }],
  ];
  const term = terminal(rows, { x: L.x, baseline: L.b, size: L.size, pitch: L.pitch });

  const world = h('div');
  // worldCamStable: a fixed, invisible 0.05° roll about the drift's fixed point, so the inbox rows glide with their plate
  // through the 1.02 drift (and the 4:5 pan) on sub-pixel positions instead of snapping to whole pixels (QA glitch).
  const cam = F.worldCamStable(world, L.about);
  world.append(S.plate(...L.plate), term.el);
  const wsvg = S.overlay(world);

  // Trail in: the head enters from the frame edge to the plate edge over 1 beat (brand ease), marching one pitch per beat;
  // 30.600 arrival: the march stops (120 ms) and the tail fades, linear, 30.600 → 31.200.
  const pitch = 12 * F.Z;
  const march = (t) => {
    const v = pitch / 0.6;                                         // px per second while in flight
    if (t < 30.6) return v * Math.max(0, t - 30.0);
    const u = Math.min(t - 30.6, 0.12);                            // linear deceleration to rest over 120 ms
    return v * 0.6 + v * (u - (u * u) / (2 * 0.12));
  };
  const trail = F.ui.trail(wsvg, L.trail, { draw: (t) => seg(t, 30.0, 0.6), march, tailFade: (t) => clamp((t - 30.6) / 0.6) });
  // Pheromone underline under the title: 16:9 at 31.200 (the midpoint); 4:5 at 31.800, as the pan to the title lands.
  const ul = F.ui.underline(wsvg, { x0: L.ul.x0, x1: L.ul.x1 + 0.01, y: L.ul.y, at: L.ulAt });

  // ---- screen: caption and collector, present at the cut
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  const c9 = S.caption('It lands in their inbox.');
  const coll = S.collector('theo', '13:41');
  screen.append(c9.el, coll.el);
  root.append(world, screen);

  // Camera: s 1.00 → 1.02 about the title (16:9 (1345, 612); 4:5 (136, 602)), brand ease.
  const drift = F.drift({ t0: 30.0, t1: 33.6, s0: 1, s1: 1.02, about: L.about });
  // 4:5 pan 31.200 → 31.800 (camera ease FILM.CAM: from rest to rest): the inbox slides left by PAN screen px so the
  // TITLE column and the whole title 'Track down the flaky payments test' are in frame (at 1080 px only `Track` was).
  // Crop and pan only, the rows are unchanged. The AGE column lands at x 32 when the pan ends: the frame edge then falls
  // in the two-space gap before it (the PRIO column's last glyph clears the edge by ≈ 14 px, so no cut glyph is left
  // at the edge), and the title runs x ≈ 142 → 891, inside title-safe. The underline draws once the camera is at rest.
  const PAN0 = 31.2, PAN_DUR = 0.6, AGE_AT = 32;
  const sEnd = drift(PAN0 + PAN_DUR).s, ageX = L.x + 36 * L.size * 0.6;
  const PAN = P ? L.about[0] + sEnd * (ageX - L.about[0]) - AGE_AT : 0;
  const camAt = (t) => {
    const c = drift(t);
    if (!PAN) return c;
    return { ...c, x: c.x + (PAN * F.seg(t, PAN0, PAN_DUR, F.CAM)) / c.s };   // screen shift −PAN·e at any scale
  };
  // render.mjs motion blur (180°, 8 sub-frames) over the 4:5 pan: it peaks at ≈ 38 px per frame.
  F.blur(PAN0, PAN0 + PAN_DUR, '4x5');

  // Sound (SCRIPT.md §5.3)
  F.cue(30.0, 'TICK', { pan: 0.7, desc: 'S6 trail entering' });
  F.cue(30.6, 'DYAD', { pan: 0.6, note: 'A4 D5', desc: 'arrival in the inbox' });
  F.cue(30.6, 'LAND', { desc: 'inbox command lands' });
  F.cue(31.2, 'LUB', { desc: 'maya pulse; midpoint, underline' });
  F.cue(31.5, 'DUB', { desc: 'theo pulse' });

  return (tl, t) => {
    const c = camAt(t);
    cam(c);
    sky.render(c);
    term.render(t);
    trail.render(t);
    ul.render(t);
  };

  /** Terminal rows by first baseline and pitch; each part lands whole (opacity 0 → 1, 120 ms linear) at its own time. */
  function terminal(rowsIn, o) {
    const off = F.baselineOffset(FONT.mono, o.size, 400);
    const el = h('div', { style: 'position:absolute;left:0;top:0' });
    const parts = [];
    rowsIn.forEach((row, i) => {
      const r = h('div', { style: `position:absolute;left:${o.x}px;top:${(o.baseline + i * o.pitch - off).toFixed(2)}px;white-space:pre;font-family:${FONT.mono};font-weight:400;font-size:${o.size}px;line-height:${o.size}px` });
      for (const p of row) { const sp = h('span', { style: `color:${p.c};opacity:0`, text: p.text }); r.append(sp); parts.push({ sp, ...p }); }
      el.append(r);
    });
    return { el, render(t) { for (const p of parts) css(p.sp, { opacity: op(clamp((t - p.at) / 0.12) * (p.alpha ?? 1)) }); } };
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
