// S9 · The terms · 40.800 → 46.200 (18:1 → 20:2), 5.4 s. SCRIPT.md S9, §3.2, §3.6, §5.3; amendment 4 (label wording).
// Entered by 21° cut #2 (the engine clips; S8 holds under it). What the beta is, stated as facts in the owner's own
// phrasing, at readable sizes. Sky: the invitation (seed 10), shared with S10. No plates, no pheromone.
FILM.shot({ id: 'S9', start: 40.8, end: 46.2, enter: 'cut21', build(root) {
  const F = FILM, { h, C, S, ui, FONT } = F;
  const P = S.P;
  root.style.background = C.bg;
  const sky = S.sky(root, 'inv');

  // Layout (frame px; world at camera s = 1). Hero: TERMS hero (Special Elite 96 / 72). Facts: Plex Sans 500 64 / 48 text-2.
  const R = S.ROLE;
  const L = P
    ? { x: 64, label: { size: 42, top: 200 }, hero: [520, 604], facts: [730, 794], about: [64, 640] }
    : { x: 96, label: { size: 40, top: 232 }, hero: [500, 604], facts: [740, 824], about: [96, 540] };

  // ---- world: the whole composition drifts with the camera (it scales about the column's left edge)
  const world = h('div');
  const cam = typeStableCam(world, L.about);   // FILM.worldCam plus a fixed 0.05° roll: see typeStableCam below
  // Beta label: specimen label, two lines, plate-locked (not typed). 16:9 card x 96 → 347, y 260 → 369;
  // 4:5 x 64 → 441, y 200 → 364. It locks in from y −36 (4:5 −54) as the cut lands, 500 ms.
  const label = specimen(['Closed beta', 'by invitation'], { size: L.label.size, x: L.x, top: L.label.top });
  // The lock-in keeps its move (from y −36 / −54, 500 ms, brand ease), but the stock is opaque within 80 ms (Stage W's
  // CARD_IN, as the S5 cards): the plain lock-in fades over 312 ms, which read as a see-through grey card over the sky.
  const lock = { render(t) {
    const at = 40.8, p = F.seg(t, at, 0.5);
    F.css(label, { opacity: F.op((t - at) / F.stageW.CARD_IN), transform: p >= 1 ? 'none' : `translate(0px, ${(S.LOCK.top * (1 - p)).toFixed(2)}px)` });
  } };
  // Hero: present in the incoming frame (fully revealed by ~40.89).
  const hero = ['The first wave opens', 'with 10 teams.'].map((str, i) =>
    ui.text(str, { face: 'display', size: R.terms.size, x: L.x, baseline: L.hero[i], colour: C.text }));
  // Facts: both lines enter together at 42.600 (240 ms, rise 12 / 18 px), land 42.840.
  const facts = ['Free during the beta.', 'macOS, Linux and Windows.'].map((str, i) =>
    ui.text(str, { face: R.facts.face, weight: R.facts.weight, size: R.facts.size, x: L.x, baseline: L.facts[i], colour: R.facts.colour, enter: { at: 42.6 } }));
  world.append(label, ...hero.map((t) => t.el), ...facts.map((t) => t.el));
  root.append(world);

  // Camera: s 1.00 → 1.03 over the shot about (96, 540) (4:5 (64, 640)), brand ease. The sky follows at 0.3.
  const camAt = F.drift({ t0: 40.8, t1: 46.2, s0: 1, s1: 1.03, about: L.about });

  // ---- sound (SCRIPT.md §5.3). The 40.700 pad and chord are this shot's pre-lap (J-cut), so they are registered here.
  F.cue(40.7, 'PAD', { state: 'open fifth', note: 'D2 A2', desc: 'pad to the open fifth (pre-lap into S9)' });
  F.cue(40.7, 'PIANO', { chord: 'D', notes: ['D2', 'A2', 'D3'], desc: 'pre-lap (J-cut) into S9' });
  F.cue(40.8, 'THUMP', { note: 'D2', desc: '21° cut #2' });
  F.cue(40.8, 'ROOM', { state: 'on', db_rms: -52, desc: 'room tone back on' });
  F.cue(43.2, 'PIANO', { chord: 'Bm7', db: -24, desc: 'soft' });
  F.cue(45.6, 'PIANO', { chord: 'Gmaj7', db: -24 });

  return (tl, t) => {
    const c = camAt(t);
    cam(c);
    sky.render(c);
    lock.render(t);
    for (const x of hero) x.render(t);
    for (const x of facts) x.render(t);
  };

  /** FILM.worldCam with a fixed, invisible 0.05° roll about the drift's fixed point (QA rv2, S9/S10 type jitter).
   *  Under a plain scale + translate, Chrome/Skia treats horizontal text as axis-aligned and rounds every line's
   *  baseline to a whole device pixel, so during the slow drift each line hopped 1 px on its own frames while the sky
   *  moved smoothly. Any rotation off the axis makes Skia place glyphs at sub-pixel positions on both axes, so the type
   *  follows the camera smoothly and is still rasterised at the true scale (no composited layer, no resampling: edges
   *  stay as crisp as at rest). The roll is constant (the camera never rolls during the shot): it lowers a line's far
   *  end by at most 0.9 px and moves left edges by under 0.5 px. The drift's fixed point keeps its exact screen
   *  position. The sky is untouched. Same as S10. Measured at 60 fps over 41.4–44.0: largest frame-to-frame baseline
   *  step 0.1 px (was 1.0). */
  function typeStableCam(el, about) {
    const { W, H } = F.FMT, [ax, ay] = about, ROLL = 0.05;
    F.worldCam(el);   // the engine's world-layer setup (absolute, full frame, origin 0 0)
    return (c) => {
      const tx = W / 2 - c.x * c.s, ty = H / 2 - c.y * c.s;
      F.css(el, { transform: `translate(${tx.toFixed(3)}px, ${ty.toFixed(3)}px) scale(${c.s.toFixed(5)}) translate(${ax}px, ${ay}px) rotate(${ROLL}deg) translate(${-ax}px, ${-ay}px)` });
    };
  }

  /** Specimen label (SCRIPT.md §3.2), the same card as Stage W's relay label, with several lines: label stock, ink,
   *  Special Elite, inset hairline 1 px at 55 % ink, radius 4 (16:9) / 6 (4:5), padding 0.9 em × 0.7 em,
   *  line-height 1.25 em. Positioned by left/top so a lock-in transform can move it. */
  function specimen(lines, o) {
    const fs = o.size, padX = 0.9 * fs, padY = 0.7 * fs, lh = 1.25 * fs;
    const tw = Math.max(...lines.map((ln) => F.measure(ln, FONT.display, fs)));
    const w = tw + 2 * padX, hgt = lines.length * lh + 2 * padY;
    const el = h('div', { style: `position:absolute;left:${o.x}px;top:${o.top}px;width:${w.toFixed(2)}px;height:${hgt.toFixed(2)}px;background:${C.stock};border-radius:${P ? 6 : 4}px;color:${C.ink};font-family:${FONT.display};font-size:${fs}px;line-height:${lh}px;white-space:pre` });
    const inset = (fs * 0.0893).toFixed(2);   // the brand label's inset (1.25 brand px on a 14 px specimen)
    el.append(h('div', { style: `position:absolute;left:${inset}px;top:${inset}px;right:${inset}px;bottom:${inset}px;border:1px solid ${C.ink};opacity:0.55;border-radius:${P ? 3 : 2}px` }));
    const txt = h('div', { style: `position:absolute;left:${padX.toFixed(2)}px;top:${padY.toFixed(2)}px` });
    for (const ln of lines) txt.append(h('div', { text: ln }));
    el.append(txt);
    return el;
  }
} });
