// S3 · The brief · 9.600 → 13.200 (5:1 → 6:3), 3.6 s. SCRIPT.md S3.
// "Your agent writes the brief." The same brief as frame 0, now in the product's form; the Done when underline plants the
// through-line; the call tag enters. Sky: maya's machine (seed 4968).
FILM.shot({ id: 'S3', start: 9.6, end: 13.2, build(root) {
  const F = FILM, { h, C, S, ui } = F;
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

  // Layout (frame px, world at camera s = 1).
  const L = P
    ? { fx: 64, fb: 520, fsize: 32, px0: -24, px1: 1104, rows: [[550, 646], [664, 760], [778, 874]], tx: 64, base: [610, 724, 838], ul: { x0: 64, x1: 280, y: 854 }, about: [64, 700] }
    : { fx: 144, fb: 470, fsize: 28, px0: 96, px1: 1944, rows: [[498, 582], [594, 678], [690, 774]], tx: 144, base: [554, 650, 746], ul: { x0: 144, x1: 360, y: 758 }, about: [96, 640] };

  // ---- world: file name, three sub-plates with the brief (verbatim, capture/run/work/flaky-brief.md)
  const world = h('div');
  // worldCamStable: a fixed, invisible 0.05° roll about the drift's fixed point (L.about). The sub-plate lock groups and
  // the file name live in this world, so their text follows the lock-ins and the 1.02 drift on sub-pixel positions
  // instead of snapping to whole pixels (QA glitch: 1 px baseline hops while the plates glide).
  const cam = F.worldCamStable(world, L.about);
  world.append(ui.text('flaky-brief.md', { face: 'mono', size: L.fsize, x: L.fx, baseline: L.fb, colour: C.text3 }).el);
  const FIELDS = [
    ['What:', ' Find out why the payments integration test fails about one run in ten.'],
    ['Why:', ' It blocks merges twice a day and nobody owns it yet.'],
    ['Done when:', ' You name the cause, or rule out the three usual suspects.'],
  ];
  // Each sub-plate group clips at its own box (the frame at rest, `overflow: clip`, no scroll container). Unclipped, the
  // 4:5 lines and plates run past the frame, and Chrome then promotes a group that has a transform or an opacity (the
  // lock-in) to a composited layer in the rolled world: its text stepped whole pixels against its plate during the lock
  // and jumped up to 0.75 px on the frame the lock ended. Clipped, nothing is composited and the text follows its
  // plate on sub-pixel positions throughout. (The cut-off part lies outside the frame: at most 1.3 px past x 0 at s 1.02.)
  const subs = FIELDS.map(([k, v], i) => {
    const g = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%;overflow:clip' });
    g.append(S.plate(L.px0, L.rows[i][0], L.px1, L.rows[i][1], C.surface2));
    g.append(ui.text(k + v, { face: 'mono', size: 36, x: L.tx, baseline: L.base[i], spans: [{ text: k, colour: C.brand }, { text: v, colour: C.text2 }] }).el);
    world.append(g);
    return g;
  });
  // Lock-in in the boot's own order: Why (centre) 9.750 from y −36; Done when (lower) 9.795 from y +80; What (top) 9.840
  // from y −60 (4:5: −54 / +120 / −90). 500 ms each, with their text; all settled by 10.340.
  const locks = [
    S.lock(subs[1], 9.75, 0, S.LOCK.top),
    S.lock(subs[2], 9.795, 0, S.LOCK.bottom),
    S.lock(subs[0], 9.84, 0, S.LOCK.antenna),
  ];
  // 12.000: pheromone underline under `Done when:` (world, so it stays on the words through the camera drift).
  const wsvg = S.overlay(world);
  const ul = ui.underline(wsvg, { x0: L.ul.x0, x1: L.ul.x1 + 0.01, y: L.ul.y, at: 12.0 });

  // ---- screen: caption, tag, collector (all present at the cut)
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  const c5 = S.caption('Your agent writes the brief.');
  const tag = S.tag();
  const coll = S.collector('maya', '13:41');
  screen.append(c5.el, tag.el, coll.el);
  root.append(world, screen);

  // Camera: s 1.00 → 1.02 over the shot, brand ease, about the left column (96, 640) (4:5 (64, 700)). The script gives the
  // centre (960, 640) / (540, 700), but about the centre the world column slides off the screen column: 4:5 the brief and
  // file name went from x 64 to ≈ 54.5 (outside title-safe, left of the caption and collector), 16:9 the sub-plates' left
  // edge from 96 to ≈ 79. About the left column they stay locked to x 96 / 64.
  const camAt = F.drift({ t0: 9.6, t1: 13.2, s0: 1, s1: 1.02, about: L.about });

  // Sound (SCRIPT.md §5.3). The 9.500 Bm7 is this shot's pre-lap (J-cut), so it is registered here.
  F.cue(9.5, 'PIANO', { note: 'Bm7 B2 F#3 A3 D4', roll: 0.03, desc: 'pre-lap (J-cut) into S3' });
  F.cue(9.6, 'LUB', { gain: -6, desc: 'maya pulse; close-up' });
  F.cue(9.75, 'PLINK', { note: 'B3', desc: 'Why sub-plate locks' });
  F.cue(9.795, 'PLINK', { note: 'D4', desc: 'Done when sub-plate locks' });
  F.cue(9.84, 'PLINK', { note: 'F#4', desc: 'What sub-plate locks' });
  F.cue(9.9, 'DUB', { gain: -6, desc: 'theo pulse; close-up' });
  F.cue(12.0, 'LUB', { desc: 'maya pulse' });
  F.cue(12.0, 'MOTIF', { note: 'A4', desc: 'Done when: underline (motif note 1 of 3)' });
  F.cue(12.0, 'PIANO', { note: 'G G2 D3 B3', roll: 0.03 });
  F.cue(12.3, 'DUB', { desc: 'theo pulse; the last before theo is shown away' });

  return (tl, t) => {
    const c = camAt(t);
    cam(c);
    sky.render(c);
    for (const l of locks) l.render(t);
    ul.render(t);
  };
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
