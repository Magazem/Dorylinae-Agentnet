// S10 · Our brief to you · 46.200 → 52.800 (20:2 → 23:1), 6.6 s. SCRIPT.md S10, §3.2, §3.7, §5.3; amendment 3.
// The people at Dorylinae write to you in the product's own grammar (What / Why / Done when). The open field is filled
// by hand: "Your team applies." is typed (silent, no caret) and underlined. The last frame (52.200) is the poster.
// Hard cut in and out. Sky: the invitation (seed 10), as S9. The sheet is typography on the sky, no card behind it.
FILM.shot({ id: 'S10', start: 46.2, end: 52.8, build(root) {
  const F = FILM, { h, C, S, ui } = F;
  const P = S.P;
  root.style.background = C.bg;
  const sky = S.sky(root, 'inv');
  // 16:9: the tag's new row (below) sits on a bright star of seed 10 just before `APPLY`, where it read as a bullet
  // (`• APPLY AT DORYLINAE.NET`). Behind the tag only, a quieted copy of the same sky (Stage W's caption treatment, with
  // the stars at 8 %: at 20 % that star still read as a faint dot before the `A`; nebula and dust kept) is laid through a
  // feathered box, riding the sky's own parallax.
  const quiet = P ? null : quietBox(root, sky, [2560, 4032, 160, 720], [1352, 150, 1872, 222], 40);

  // Layout (frame px; world at camera s = 1). SHEET field: Plex Mono 28 / 32 `brand`. SHEET value: Special Elite 64 / 56.
  // SHEET Why: Plex Sans 500 64 / 48 `text-2`. Placeholder: Plex Mono 28 / 32 `text-3`.
  const L = P ? {
    x: 64, label: 42, labelTop: 150, field: 32, value: 56, why: 48, ph: 32,
    whatF: 340, what: [410, 474], whyF: 560, whyV: [628, 686, 744], doneF: 830, done: 902,
    ul: { x0: 64, x1: 568, y: 928 }, about: [64, 640],
    whyLines: ['Each of you keeps your own coding agent.', 'Dorylinae lets the agents', 'hand each other work.'],
  } : {
    x: 96, label: 36, labelTop: 144, field: 28, value: 64, why: 64, ph: 28,
    whatF: 300, what: [376, 448], whyF: 534, whyV: [610, 682], doneF: 768, done: 844,
    ul: { x0: 96, x1: 672, y: 868 }, about: [96, 540],
    whyLines: ['Each of you keeps your own coding agent.', 'Dorylinae lets the agents hand each other work.'],
  };

  // ---- world: the sheet (drifts with the camera; the underline stays on its words)
  const world = h('div');
  const cam = typeStableCam(world, L.about);
  // Sender label (amendment 3: "From Dorylinae"), present. The same specimen card as Stage W's relay label.
  // 16:9 card x 96 → 367.6, y 150 → 224.2; 4:5 x 64 → 471.4, y 150 → 261.3 (the script's left edge and top).
  const sender = F.stageW.card('From Dorylinae', { size: L.label, x: L.x, top: L.labelTop });
  // Field names and the placeholder are copied character for character from capture/02-request-help.txt.
  const field = (str, base) => ui.text(str, { face: 'mono', size: L.field, x: L.x, baseline: base, colour: C.brand });
  const items = [
    field('What:', L.whatF),
    ...['We are opening a closed beta', 'for small teams.'].map((str, i) =>
      ui.text(str, { face: 'display', size: L.value, x: L.x, baseline: L.what[i], colour: C.text })),
    field('Why:', L.whyF),
    ...L.whyLines.map((str, i) => ui.text(str, { face: 'sans', weight: 500, size: L.why, x: L.x, baseline: L.whyV[i], colour: C.text2 })),
    field('Done when:', L.doneF),
  ];
  // 47.880: the placeholder clears (120 ms, opacity → 0), done by 48.000.
  const ph = ui.text('<how the other side knows it is finished>', { face: 'mono', size: L.ph, x: L.x, baseline: L.done, colour: C.text3, out: { at: 47.88, dur: 0.12 } });
  // 48.000: "Your team applies." types at 85 ms per letter (jitter 0, space 0.6 step, 60 ms letter fade), no caret.
  // Lands 49.437. It is the only full line typed by hand, and it is silent.
  const typed = ui.text('Your team applies.', { face: 'display', size: L.value, x: L.x, baseline: L.done, colour: C.text, typed: { at: 48.0 } });
  // 49.800: pheromone underline, 240 ms (16:9: x 96 → 672, 25 dots; 4:5: x 64 → 568, 15 dots). QA fix: the dots sit
  // below the Special Elite descender zone (about 0.21 em), not at SCRIPT's 12 / 16 px, where they touched the feet of
  // both p's in "applies": 16:9 y 868 (baseline 844 + 24), 4:5 y 928 (902 + 26). Needs the director's sign-off (§3.7).
  // QA rv2: one end convention for the poster in both formats: the last dot sits under the last letter ("s") and the
  // full stop hangs, as in hanging punctuation. With the fixed pitch the dots can end flush with the stop in 16:9 only
  // (the §3.7 rule gives 26 dots there, last dot 696); in 4:5 the nearest ends are 568 (17 px short of the ink) or 604
  // (18 px past it, an underline longer than its words). So 4:5 keeps the script's 15 dots, and 16:9 takes 25: the
  // last dot is 6.7 / 5.9 px inside the end of the "s" in both. Needs the director's sign-off with the y offset
  // (S10 16:9 table says x 96 → 696, 26 dots).
  const svg = S.overlay(world);
  const ul = ui.underline(svg, { x0: L.ul.x0, x1: L.ul.x1 + 0.01, y: L.ul.y, at: 49.8 });
  world.append(sender.el, ...items.map((t) => t.el), ph.el, typed.el);

  // ---- screen: the call tag, present at the cut
  // 16:9: the tag (FILM.S.tag's style: Plex Mono 500, caps, 0.08 em, 28 px, text-2, right-aligned at x 1824) sits on the
  // sender card's row, so the sheet's metadata reads as one row (who it is from left, the call right), as the collector
  // row does in S3 to S8. Its default place (baseline 290) sat on the `What:` field's line (baseline 300). It is
  // vertically centred on the `From Dorylinae` card as the camera holds it: the card drifts up with the 1.02 drift
  // (centre y 191.9 at the cut, 185.3 by 49.8, 184.9 at the 52.2 poster), so the tag's cap block (Plex cap height
  // 0.698 em) is centred on the card from the typing through the poster: baseline 184.9 + 0.698 · 28 / 2 ≈ 195 (201.6
  // would centre it on the card at rest). A screen element, so it does not move. 4:5 keeps its place (bottom-left).
  const screen = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
  const tag = P ? S.tag() : ui.text('Apply at dorylinae.net', { face: 'mono', weight: 500, tracking: 0.08, upper: true, size: 28, colour: C.text2, x: 1824, baseline: 195, anchor: 'end' });
  screen.append(tag.el);
  root.append(world, screen);

  // Camera: s 1.00 → 1.02 over the shot about (96, 540) (4:5 (64, 640)), brand ease. The sky follows at 0.3, but it
  // continues S9's drift (S9 ends at s 1.03 about the same point), so the shared invitation sky does not jump on the
  // hard cut: same about point, s 1.03 → 1.05 (the same 0.02 of travel, so the parallax rate is unchanged).
  const camAt = F.drift({ t0: 46.2, t1: 52.8, s0: 1, s1: 1.02, about: L.about });
  const skyAt = F.drift({ t0: 46.2, t1: 52.8, s0: 1.03, s1: 1.05, about: L.about });

  // ---- sound (SCRIPT.md §5.3). The typing is silent (§5.1: read, not heard).
  F.cue(48.0, 'PIANO', { chord: 'Asus4', notes: ['A2', 'E3', 'D4'], desc: 'held; typing starts (silent)' });
  F.cue(49.8, 'MOTIF', { note: 'D4', desc: '"Your team applies." underlined: A4 – F#4 – D4 completes' });
  F.cue(52.65, 'RELEASE', { dur: 0.15, desc: 'everything releases (150 ms)' });

  return (tl, t) => {
    const c = camAt(t);
    cam(c);
    sky.render(skyAt(t));
    if (quiet) quiet.render();
    for (const x of items) x.render(t);
    ph.render(t);
    typed.render(t);
    ul.render(t);
    tag.render(t);
  };

  /**
   * A quieted copy of the sky behind a screen box (the technique of Stage W's quiet regions, shots/stage-w.js): the PNG
   * band src [col0, col1, row0, row1] (2× display px; it must cover every place the box can see over the shot plus the
   * filter's reach) is filtered: a radius-28 opening keeps the nebula, a radius-1 opening splits off
   * the 1-2 px grain (kept as dust, soft-kneed), and the stars and their glows come back at 8 %. The band rides the
   * sky image's transform; the box [x0, y0, x1, y1] (screen px) is feathered by fe px on every side.
   * Nothing in it may be composited (a canvas, even a software one, or an `overflow: hidden` box is promoted to a layer,
   * and then the sheet above it is composited too, which puts its type back on whole-pixel steps): the band is an <img>
   * under a CSS filter, and the boxes clip with `overflow: clip`.
   */
  function quietBox(parent, sk, src, box, fe) {
    const [c0, c1, r0, r1] = src, w = c1 - c0, hgt = r1 - r0, id = 's10-quiet-tag';
    const RL = 28, RS = 1, STAR = 0.08;
    const filter = F.s('filter', { id, x: 0, y: 0, width: w, height: hgt, filterUnits: 'userSpaceOnUse', 'color-interpolation-filters': 'sRGB' });
    const morph = (inp, op, r, res) => F.s('feMorphology', { in: inp, operator: op, radius: r, result: res });
    const fn = (attrs) => ['feFuncR', 'feFuncG', 'feFuncB'].map((f) => F.s(f, attrs));
    const kx = 60 / 255, knee = Array.from({ length: 33 }, (_, i) => { const x = i / 32; return (x < kx ? x : kx + 0.3 * (x - kx)).toFixed(4); }).join(' ');
    filter.append(
      morph('SourceGraphic', 'erode', RL, 'eL'), morph('eL', 'dilate', RL, 'oL'),
      morph('SourceGraphic', 'erode', RS, 'eS'), morph('eS', 'dilate', RS, 'oS'),
      F.s('feComponentTransfer', { in: 'oS', result: 'iS' }, ...fn({ type: 'linear', slope: -1, intercept: 1 })),
      F.s('feComposite', { in: 'SourceGraphic', in2: 'iS', operator: 'arithmetic', k1: 0, k2: 1, k3: 1, k4: -1, result: 'd' }),
      F.s('feComponentTransfer', { in: 'd', result: 'dk' }, ...fn({ type: 'table', tableValues: knee })),
      F.s('feComposite', { in: 'oL', in2: 'oS', operator: 'arithmetic', k1: 0, k2: 1 - STAR, k3: STAR, k4: 0, result: 'b' }),
      F.s('feComposite', { in: 'dk', in2: 'b', operator: 'arithmetic', k1: 0, k2: 1, k3: 1, k4: 0 }));
    const defs = F.s('svg', { width: 0, height: 0, style: 'position:absolute;left:0;top:0' }, filter);
    const [x0, y0, x1, y1] = box.map((v, i) => (i < 2 ? Math.floor(v - fe) : Math.ceil(v + fe)));
    const soft = (dir, a, b) => { const st = []; for (let i = 0; i <= 6; i++) { const u = i / 6, k = u * u * (3 - 2 * u); st.push(`rgba(0,0,0,${k.toFixed(4)}) ${(a + fe * u).toFixed(2)}px`); } for (let i = 0; i <= 6; i++) { const u = i / 6, k = 1 - u * u * (3 - 2 * u); st.push(`rgba(0,0,0,${k.toFixed(4)}) ${(b - fe + fe * u).toFixed(2)}px`); } return `linear-gradient(${dir}, ${st.join(', ')})`; };
    const mx = soft('to right', 0, x1 - x0), my = soft('to bottom', 0, y1 - y0);
    const outer = h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;overflow:clip;-webkit-mask-image:${mx};mask-image:${mx}` });
    const inner = h('div', { style: `position:absolute;left:0;top:0;width:100%;height:100%;-webkit-mask-image:${my};mask-image:${my}` });
    // the band of the sky PNG at its natural size (PNG px), clipped to the band, filtered in that space (radii in PNG
    // px), then scaled to display px and placed by the sky image's own transform
    const band = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${hgt}px;overflow:clip;transform-origin:0 0;filter:url(#${id})` });
    band.append(h('img', { src: sk.el.getAttribute('src'), style: `position:absolute;left:${-c0}px;top:${-r0}px;width:${sk.el.naturalWidth || 4032}px;height:${sk.el.naturalHeight || 2268}px` }));
    inner.append(band); outer.append(inner);
    sk.el.after(defs, outer);
    return {
      render() {
        F.css(band, { transform: `translate(${-x0}px, ${-y0}px) ${sk.el.style.transform} translate(${c0 / 2}px, ${r0 / 2}px) scale(0.5)` });
      },
    };
  }

  /** FILM.worldCam with a fixed, invisible 0.05° roll about the drift's fixed point (QA rv2, S9/S10 type jitter).
   *  Under a plain scale + translate, Chrome/Skia treats horizontal text as axis-aligned and rounds every line's
   *  baseline to a whole device pixel, so during the slow drift each line hopped 1 px on its own frames while the sky,
   *  the label card and the underline moved smoothly. Any rotation off the axis makes Skia place glyphs at sub-pixel
   *  positions on both axes, so the type follows the camera smoothly and is still rasterised at the true scale (no
   *  composited layer, no resampling: edges stay as crisp as at rest; will-change: transform cost ~12 % edge
   *  sharpness). At 0.02° some lines still snapped; 0.03° cleared them, and 0.05° leaves margin. The roll is constant
   *  (the camera never rolls during the shot): it lowers a line's far end by at most 1.2 px (16:9) / 0.8 px (4:5) and
   *  moves left edges by under 0.5 px. The drift's fixed point keeps its exact screen position. The sky is untouched.
   *  Measured at 60 fps over 46.25–47.0 and 50.1–52.8: largest frame-to-frame baseline step 0.2 px (was 1.0). */
  function typeStableCam(el, about) {
    const { W, H } = F.FMT, [ax, ay] = about, ROLL = 0.05;
    F.worldCam(el);   // the engine's world-layer setup (absolute, full frame, origin 0 0)
    return (c) => {
      const tx = W / 2 - c.x * c.s, ty = H / 2 - c.y * c.s;
      F.css(el, { transform: `translate(${tx.toFixed(3)}px, ${ty.toFixed(3)}px) scale(${c.s.toFixed(5)}) translate(${ax}px, ${ay}px) rotate(${ROLL}deg) translate(${-ax}px, ${-ay}px)` });
    };
  }
} });
