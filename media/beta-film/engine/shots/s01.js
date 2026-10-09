// S1 · Paste · 0.000 → 3.600 (1:1 → 2:3), 3.6 s. SCRIPT.md S1 and DIRECTOR'S AMENDMENT 1.
// The pain: the same brief pasted twice by hand. Monochrome: only bg, surface, text and text-2 (no brand colour yet).
// Holds its last frame for 0.6 s under S2's 21° cut (post: 0.6).
FILM.shot({ id: 'S1', start: 0, end: 3.6, post: 0.6, build(root) {
  const F = FILM, { h, C, S, ui } = F;
  const P = S.P;
  root.style.background = C.bg;                       // flat bg, no stars

  // Brief text, verbatim from capture/run/work/flaky-brief.md. Field names stay text-2 here: no brand colour exists yet.
  const BRIEF = [
    'What: Find out why the payments integration test fails about one run in ten.',
    'Why: It blocks merges twice a day and nobody owns it yet.',
    'Done when: You name the cause, or rule out the three usual suspects.',
  ];
  // Layout (frame px). 4:5 plates bleed both sides and their lines crop at the right frame edge. QA fix: the 4:5 plates
  // and their text sit 120 px lower than the first build (A y 400 → 520, B 618 → 738).
  const L = P
    ? { A: [-24, 520, 1104, 720], ax: 64, ab: 584, B: [-24, 738, 1104, 938], bx: 160, bb: 802, lockDx: S.LOCK.side, about: [64, 170] }
    : { A: [96, 400, 1680, 600], ax: 144, ab: 464, B: [240, 612, 1960, 812], bx: 288, bb: 676, lockDx: S.LOCK.side, about: [96, 160] };
  // 16:9 plate B bleeds off the right edge (script x1 1824): with the 1.03 drift an 1824 edge would land at 1876, 44 px from
  // the frame edge (neither inside safe nor bleeding). Bleeding reads as another window; its text is unchanged.

  // ---- world (camera): the two paste plates
  const world = h('div');
  // worldCamStable: a fixed, invisible 0.05° roll about the drift's fixed point, so the brief lines glide with their
  // plates through the 1.03 drift instead of snapping to whole pixels (QA glitch: 1 px baseline hops).
  const cam = F.worldCamStable(world, L.about);
  // Each plate group clips at its own box (`overflow: clip`, no scroll container): world x −24 (the plates' left bleed) to
  // the frame's right edge, y 0 to the frame's bottom. Unclipped, the 4:5 plates and lines run past the frame's right edge,
  // and Chrome then promotes the locking group (transform, opacity) to a composited layer in the rolled world, which puts
  // its text back on whole-pixel steps. The box moves with the lock, so plate B slides in whole (rounded left edge
  // included); only parts past the frame's right edge are cut (the drift scales up about x 64 / 96, so that edge stays
  // beyond the frame).
  const BLEED = 24;
  function briefPlate(rect, x, b0) {
    const { W, H } = F.FMT;
    const g = h('div', { style: `position:absolute;left:${-BLEED}px;top:0;width:${W + BLEED}px;height:${H}px;overflow:clip` });
    const inner = h('div', { style: `position:absolute;left:${BLEED}px;top:0;width:${W}px;height:${H}px` });
    inner.append(S.plate(...rect));
    BRIEF.forEach((ln, i) => inner.append(ui.text(ln, { face: 'mono', size: 32, x, baseline: b0 + i * 48, colour: C.text2 }).el));
    g.append(inner);
    return g;
  }
  const plateA = briefPlate(L.A, L.ax, L.ab);          // present at frame 0
  const plateB = briefPlate(L.B, L.bx, L.bb);          // the second paste
  world.append(plateA, plateB);
  const lockB = S.lock(plateB, 1.8, L.lockDx, 0);      // 1.800: locks in from x +92 (4:5 +138), 500 ms

  // ---- screen: captions
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  // 4:5: the captions carry the brand display tracking −0.01 em (DESIGN.md display-xl / display-l). At 0 tracking C1
  // (amendment 1, 60 px) ends at x 1025, past the 4:5 title-safe edge 1016; tracked it ends at ≈ 1006 and keeps 60 px.
  const cap = (str, o = {}) => (P
    ? ui.text(str, { face: 'display', size: S.ROLE.caption.size, tracking: -0.01, x: S.ROLE.caption.x,
      baseline: S.ROLE.caption.base + (o.line ?? 0) * S.ROLE.caption.pitch, colour: C.text, enter: o.enter })
    : S.caption(str, o));
  const c1 = cap('You paste the brief into a chat.');                                        // amendment 1, present at frame 0
  const c2 = P
    ? [cap('They paste it into', { line: 1, enter: { at: 0.3 } }), cap('their coding agent.', { line: 2, enter: { at: 0.3 } })]
    : [cap('They paste it into their coding agent.', { line: 1, enter: { at: 0.3 } })];      // enters 0.300, lands 0.540
  screen.append(c1.el, ...c2.map((c) => c.el));
  root.append(world, screen);

  // Camera: s 1.000 → 1.030 over the whole shot, brand ease; world point (96, 160) (4:5 (64, 170)) stays fixed on screen.
  const camAt = F.drift({ t0: 0, t1: 3.6, s0: 1, s1: 1.03, about: L.about });

  // Sound (SCRIPT.md §5.3): room tone only, and the paste click as plate B locks in.
  F.cue(0, 'ROOM', { gain: -52, until: 3.55, desc: 'room tone only: the world before the product' });
  F.cue(1.8, 'PASTE', { desc: 'plate B locks in (the second paste)' });

  return (tl, t) => {
    cam(camAt(t));
    c1.render(t);
    for (const c of c2) c.render(t);
    lockB.render(t);
  };
} });
