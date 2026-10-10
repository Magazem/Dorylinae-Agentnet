// Section I · Cold open: the pain · G0 → G8 (1:1 → 3:1) · 0.000 → 5.714 s. SCRIPT-v2 §5.1, §4.3, §6 (C-1, C-2, S-1).
// The same brief pasted twice by hand. Monochrome: the ground steps (bg, surface, surface-2) and text, text-2 (§4.1 colour
// arc); no brand colour yet.
//
// dirfix (review r1, viewer): the hook was two identical grey blocks on black, nothing moving after 1.4 s, nothing saying
// which is a chat and which a coding agent. Now each plate has its own identity from the film's own grammar, and the
// second paste is an action (copy, then paste), with something moving through the whole hold:
//   plate YOU   a chat: the brief is a sent message, a raised bubble (surface-2, the chips' ground step) with a tail at its
//               bottom right, right-aligned in the plate
//   plate THEY  a coding agent: the scene's terminal block (a bg block inset in the plate, as the machines in II) with a
//               block caret blinking on the half beat, waiting at the first line
//
//   G0    both plates, the message in YOU's bubble (in `text`: it is what the viewer reads first), the caret in THEY's
//         empty block; caption line 1 `You paste the brief into a chat.` present at frame 0 (a complete thought)
//   0 → 1.15 s  a selection sweeps the message, character by character (text-2 at 28 %, under the text): it is copied.
//         final polish: it is already under way on frame 0 (`What:` selected), so the film moves from its first frame
//         (it used to start at 0.6 s, the first ~0.7 s were a still)
//   G2    the paste: the same block lands whole in THEY's terminal as one-frame ink (final polish; it was a 120 ms
//         fade, which read as a dissolve), in `text`, and the caret jumps to its end; the selection clears (240 ms, half
//         a beat later). LAND. Caption line 2 `They paste it into their coding agent.` enters (240 ms, rise 12 / 18 px)
//   G3    the pasted block settles to text-2 (240 ms); the caret keeps blinking (on for the first half of every beat)
//   G7.5  both lines out (120 ms, ending here); S-1 enters on its own layer above both shots and bridges the 21° cut (G8)
//
// No camera move (dirfix): the 1.00 → 1.02 drift made every glyph re-rasterise each frame (the text simmered, ±0.1 –
// 0.4 px) and carried the 4:5 text 7 px outside title-safe; the selection, the paste and the caret are the motion now.
// The text is the script's (§5.1), wrapped as before; only the containers changed. Copy unchanged.
//
// final polish, 4:5: the plates were full-bleed bands (x −24 → 1104). They are inset windows now, the 16:9 material at
// the 4:5 radius: plates x 48 → 1032 (radius 24, 48 px from the frame edges), THEY's terminal block inset 12 px
// (radius BRADIUS), the bubble 24 px inside YOU's right edge as in 16:9. Both text blocks share one left edge (x 101, the
// bubble's text), inside title-safe (64 → 1016) on every frame; nothing moves them.
//
// Section I holds its last frame for 0.6 s (post) under section II's 21° cut.
//
// S-1 (`With Dorylinae, your agent hands it to theirs.`, §6) bridges the cut on a layer above every shot (z 5) from G7.5.
// Section II may draw it too (its slot's window runs G7.5 → G13; sec2-scene.js OWN_S1). So that S-1 is drawn exactly once
// on every pixel, the shot 'I-S1' below looks at the other shots once their DOM exists:
//   - another shot draws S-1 whole from G7.5 on its own unclipped layer (sec2-scene.js 'II-cap', OWN_S1 = true, as built
//     now): 'I-S1' draws nothing;
//   - section II draws S-1 and enters with the 21° cut: 'I-S1' draws it alone until G8, then only right of the 21° edge
//     (the exact complement of FILM.cut21's clip), and nothing after the wipe (G8 + 0.6 s): the wipe passes under one line;
//   - section II draws S-1 with a hard cut: 'I-S1' draws it until G8;
//   - section II does not draw S-1: 'I-S1' draws it for its whole window, out at G13 (120 ms) where S-2 enters.
// "Dorylinae" is `text` until 6.314 s (the wipe complete), then cross-fades to `brand` in 120 ms (§5.1).
(() => {
  const F = window.FILM, V = F.V2;
  const { C, FMT, h, s: svgEl, css, seg, clamp, lerp, EASE } = F;
  const { T, Gof, pick, frameOf } = V;
  const WIPE = 0.6;                                   // FILM.cut21's duration

  // ---------------------------------------------------------------- layout (§5.1), frame px (no camera)
  // brief wrapped greedily at 35 (16:9) / 46 (4:5) characters, verbatim from capture/run/work/flaky-brief.md
  const BRIEF = pick([
    'What: Find out why the payments', 'integration test fails about one', 'run in ten.',
    'Why: It blocks merges twice a day', 'and nobody owns it yet.', 'Done when: You name the cause, or',
    'rule out the three usual suspects.',
  ], [
    'What: Find out why the payments integration', 'test fails about one run in ten.',
    'Why: It blocks merges twice a day and nobody', 'owns it yet.', 'Done when: You name the cause, or rule out the',
    'three usual suspects.',
  ]);
  // plates (§5.1; 4:5 inset, final polish). YOU: the bubble's right edge (its text right-aligned in it), padding, corner
  // radius, the first baseline. THEY: the terminal block inset 12 px in its plate (radius = the machines' blocks',
  // V2.BRADIUS), the text at x 1014 in 16:9 (dirfix 12a) and on YOU's text edge in 4:5, first baseline as before (16:9 as
  // YOU's, 4:5 + 398).
  const L = pick({
    you: [96, 170, 954, 650], they: [966, 170, 1824, 650], r: 16, size: 36, pitch: 50,
    bubble: { right: 930, padX: 26, padTop: 22, padBot: 22, r: 22 }, base: 270,
    term: [978, 182, 1812, 638], theyX: 1014, theyBase: 270,
    cap: { x: 960, bases: [894, 970] }, rise: 12,
  }, {
    you: [48, 140, 1032, 520], they: [48, 538, 1032, 918], r: 24, size: 32, pitch: 46,
    bubble: { right: 1008, padX: 24, padTop: 20, padBot: 20, r: 24 }, base: 224,
    term: [60, 550, 1020, 906], theyX: null, theyBase: 622,              // theyX null: YOU's text edge (stacked plates)
    cap: { x: 540, bases: [1138, 1196] }, rise: 18,
  });
  const C1 = 'You paste the brief into a chat.', C2 = 'They paste it into their coding agent.';
  // times
  // the selection runs 0 → 1.15 s and is under way on frame 0 (SEL_K0 characters, `What:`); then the paste at G2
  const SEL = [0, 1.15], SEL_K0 = 'What:'.length, PASTE = T(2), SETTLE = T(3), OUT = T(7.5);
  const SEL_OUT = PASTE + V.BEAT / 2;                 // the selection clears half a beat after the paste (240 ms)

  FILM.shot({ id: 'I', start: 0, end: T(8), post: WIPE, build(root) {
    css(root, { background: C.bg });                  // flat bg, no sky (§4.11)
    const mono = F.FONT.mono, adv = F.measure('0000000000', mono, L.size, 400) / 10;
    const cx = document.createElement('canvas').getContext('2d'); cx.font = `400 ${L.size}px ${mono}`;
    const asc = cx.measureText(BRIEF[0]).actualBoundingBoxAscent;
    const desc = cx.measureText(BRIEF[BRIEF.length - 1]).actualBoundingBoxDescent;
    const maxW = Math.max(...BRIEF.map((ln) => F.measure(ln, mono, L.size, 400)));
    const lastBase = L.base + (BRIEF.length - 1) * L.pitch;

    // ---- the world: both plates, at rest (worldCamStable's roll, as section II's world; no camera move)
    const world = h('div', { style: 'position:absolute;left:0;top:0' });
    root.append(world);
    F.worldCamStable(world, [L.cap.x, L.cap.bases[0]])({ x: FMT.W / 2, y: FMT.H / 2, s: 1 });
    const box = ([x0, y0, x1, y1], fill, r) => h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;background:${fill};border-radius:${r}px` });
    world.append(box(L.you, C.surface, L.r), box(L.they, C.surface, L.r));

    // YOU: a sent chat message. The bubble hugs the text block (padding, radius) and has a tail at its bottom right: the
    // right edge curves out to a point 16 px past it on the bottom edge (flat fill, one path)
    const B = L.bubble, youX = Math.round(B.right - B.padX - maxW);
    const bx0 = youX - B.padX, bx1 = B.right, by0 = Math.round(L.base - asc - B.padTop), by1 = Math.round(lastBase + desc + B.padBot), r = B.r;
    const bub = svgEl('svg', { width: FMT.W, height: FMT.H, style: 'position:absolute;left:0;top:0;overflow:visible' });
    bub.append(svgEl('path', { fill: C.surface2, d: `M${bx0 + r} ${by0} H${bx1 - r} A${r} ${r} 0 0 1 ${bx1} ${by0 + r} V${by1 - 30} C${bx1} ${by1 - 10} ${bx1 + 7} ${by1} ${bx1 + 16} ${by1} H${bx0 + r} A${r} ${r} 0 0 1 ${bx0} ${by1 - r} V${by0 + r} A${r} ${r} 0 0 1 ${bx0 + r} ${by0} Z` }));
    world.append(bub);
    // the selection (under the text): one box per line, line box tall (pitch), growing a character at a time
    const selTop = Math.round(L.pitch * 0.79), lens = BRIEF.map((ln) => ln.length), N = lens.reduce((a, b) => a + b, 0);
    const sels = BRIEF.map((ln, i) => { const d = h('div', { style: `position:absolute;left:${youX}px;top:${L.base + i * L.pitch - selTop}px;width:0;height:${L.pitch}px;background:rgba(167,159,196,0.28);border-radius:3px;display:none` }); world.append(d); return d; });
    const selLayer = sels;
    BRIEF.forEach((ln, i) => world.append(V.text(ln, { face: 'mono', size: L.size, weight: 400, colour: C.text, x: youX, baseline: L.base + i * L.pitch }).el));

    // THEY: the terminal block, empty until the paste; the pasted block; the caret
    world.append(box(L.term, C.bg, V.BRADIUS));
    const theyX = L.theyX ?? youX;
    const pasted = h('div', { style: 'position:absolute;left:0;top:0;opacity:0' });
    const pLines = BRIEF.map((ln, i) => { const tx = V.text(ln, { face: 'mono', size: L.size, weight: 400, colour: C.text, x: theyX, baseline: L.theyBase + i * L.pitch }); pasted.append(tx.el); return tx.el; });
    world.append(pasted);
    const caretH = Math.round(L.size * 1.0), caretUp = Math.round(L.size * 0.78);
    const caret = h('div', { style: `position:absolute;left:0;top:0;width:${adv.toFixed(2)}px;height:${caretH}px;background:${C.text}` });
    world.append(caret);
    const caretAt = (t) => (frameOf(t) >= frameOf(PASTE)
      ? [theyX + (lens[lens.length - 1] + 1) * adv, L.theyBase + (BRIEF.length - 1) * L.pitch]   // one cell past the paste
      : [theyX, L.theyBase]);                                                                   // waiting at the first line
    const pastedColour = V.colourShift(C.text, C.text2, SETTLE, 0.24);

    // ---- screen: the caption slot's two lines (§4.3 grammar), C-1 present at frame 0, C-2 entering at G2. On the world's
    // raster path (V2.stableText), so C-2's rise glides instead of stepping whole pixels
    const screen = h('div', { style: `position:absolute;left:0;top:0;width:${FMT.W}px;height:${FMT.H}px;${V.stableText(L.cap.x, L.cap.bases[0])}` });
    root.append(screen);
    const cap = (str, base) => { const tx = V.text(str, { role: 'caption', x: L.cap.x, baseline: base, anchor: 'middle' }); screen.append(tx.el); return tx; };
    const c1 = cap(C1, L.cap.bases[0]), c2 = cap(C2, L.cap.bases[1]);

    // ---- sound (§8): one LAND at G2, the second paste
    F.cue(T(2), 'LAND', { G: 2, desc: 'the second paste (the brief lands in the coding agent)' });

    return (tl, t) => {
      // the selection: characters selected so far (whole characters, linear drag from SEL_K0 on frame 0 to all N at
      // SEL[1]), cleared after the paste
      const k = SEL_K0 + Math.floor(clamp((t - SEL[0]) / (SEL[1] - SEL[0])) * (N - SEL_K0) + 1e-9), selA = 1 - seg(t, SEL_OUT, 0.24);
      let left = k;
      selLayer.forEach((d, i) => {
        const n = Math.min(lens[i], Math.max(0, left)); left -= lens[i];
        css(d, { display: n > 0 && selA > 0 ? 'block' : 'none', width: `${(n * adv).toFixed(2)}px`, opacity: F.op(selA) });
      });
      // the paste: one-frame ink (whole, full opacity, on its frame: frameOf(G2) = 86), then settles to text-2
      const pa = frameOf(t) >= frameOf(PASTE) ? 1 : 0;
      css(pasted, { opacity: F.op(pa), display: pa > 0 ? 'block' : 'none' });
      const col = pastedColour(t);
      for (const el of pLines) css(el, { color: col });
      // the caret: on for the first half of every beat (whole frames), at its place
      const [cx0, cb] = caretAt(t), on = (Gof(frameOf(t) / V.FPS) % 1) < 0.5;
      css(caret, { left: `${cx0.toFixed(2)}px`, top: `${cb - caretUp}px`, visibility: on ? 'visible' : 'hidden' });
      // captions
      const out = 1 - seg(t, OUT - 0.12, 0.12);
      css(c1.el, { opacity: F.op(out) });
      const a2 = seg(t, T(2), 0.24), dy = L.rise * (1 - a2);
      css(c2.el, { opacity: F.op(Math.min(a2, out)), transform: dy > 0.001 ? `translateY(${dy.toFixed(3)}px)` : 'none' });
    };
  } });

  // ---------------------------------------------------------------- S-1, the bridge over the 21° cut (§4.3, §5.1)
  FILM.shot({ id: 'I-S1', start: T(7.5), end: T(13), z: 5, build(root) {
    const S1 = V.COPY.S1.map((ln, i) => (i === 0
      ? [{ text: 'With ' }, { text: 'Dorylinae', colour: V.colourShift(C.text, C.brand, T(8) + WIPE) }, { text: ln.slice('With Dorylinae'.length) }]
      : ln));
    const slot = V.captionSlot(root, { items: [{ at: T(7.5), lines: S1, out: T(13) }] });
    // the complement of FILM.cut21's clip at local time u: everything right of the 21° edge (same edge, same rounding)
    const { W, H } = FMT, TAN = F.TAN21;
    const m0 = -(H / 2 + 10) * TAN - 10, m1 = W + (H / 2 + 10) * TAN + 10;
    const rightOfEdge = (u) => {
      const m = lerp(m0, m1, EASE(clamp(u / WIPE)));
      const xe = (y) => m + (H / 2 - y) * TAN;
      return `polygon(${xe(-10).toFixed(2)}px -10px, ${W + 10}px -10px, ${W + 10}px ${H + 10}px, ${xe(H + 10).toFixed(2)}px ${H + 10}px)`;
    };
    let mode = null;                                   // 'none' | 'own' | 'wipe' | 'cut' (see the header)
    return (tl, t) => {
      if (mode == null) {
        // every other shot that draws S-1 (section II's scene, or its own caption layer)
        const others = F.shots.filter((s) => s.id !== 'I-S1' && s.root && /hands it to theirs/.test(s.root.textContent));
        mode = !others.length ? 'own'
          : others.some((s) => !s.enter && s.start <= T(7.5) + 1e-6) ? 'none'      // drawn whole from G7.5 by another layer
            : others.some((s) => s.enter === 'cut21') ? 'wipe' : 'cut';
      }
      if (mode === 'none') { css(root, { visibility: 'hidden' }); return; }
      let show = true, clip = 'none';
      if (mode !== 'own' && t >= T(8)) {
        if (mode === 'wipe' && t < T(8) + WIPE) clip = rightOfEdge(t - T(8));
        else show = false;
      }
      css(root, { clipPath: clip, visibility: show ? 'visible' : 'hidden' });
      if (show) slot.render(t);
    };
  } });
})();
