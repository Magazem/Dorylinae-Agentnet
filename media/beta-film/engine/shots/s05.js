// S5 · The relay (signature) · 19.200 → 30.000 (SCRIPT.md S5, §3.5, §3.7). Hard cut in, hard cut out.
// The request stops at the relay. The relay's own log line is labelled with what it can see. "Not what." Stillness.
// On the 28.800 downbeat theo's ring fills, the log records delivery, and the trail walks on.
FILM.shot({ id: 'S5', start: 19.2, end: 30.0, build(root) {
  const F = FILM, { C, FMT, S, seg, clamp, lerp, op, css, s: svgEl, TAN21 } = F;
  const P = FMT.portrait;
  const G = F.stageW.G;
  const pitch = G.pitch;
  const aLen = G.relay[P ? 1 : 0] - G.gap - G.a0;           // segment A: maya → relay (368 / 344)
  const bStart = G.relay[P ? 1 : 0] + G.gap - G.a0;         // segment B starts 12 px past the relay (392 / 368)
  const len = G.a1 - G.a0;
  const BACK = 28.8;                                         // theo is back (13:1)

  // ---------------------------------------------------------------- 4:5 strip (character i at x = X + i·a)
  // OV is 15 px left of the script's −63.4: the `How big` card's right edge then lines up with the Relay card's (912)
  // and clears the trail by 23 px instead of 8.5 (`When` card at x 73.4, line A ends at x 885).
  const W1 = -281.6, W2 = -1170.4, W3 = -1713.6, OV = -78.4, A0 = 21.6, AOV = 7.962;
  // The two pans and the zoom are camera moves on the strip: camera ease (FILM.CAM, the boot's sweep 0.4, 0, 0.6, 1), so
  // each starts and ends at rest. Even eased they peak above 19 px per frame, so each gets render.mjs motion blur.
  function strip(t) {
    if (t < 23.4) return { X: W1, a: A0 };
    if (t < 24.0) return { X: lerp(W1, W2, seg(t, 23.4, 0.6, F.CAM)), a: A0 };
    if (t < 25.2) return { X: W2, a: A0 };
    if (t < 25.8) return { X: lerp(W2, W3, seg(t, 25.2, 0.6, F.CAM)), a: A0 };
    if (t < 26.4) return { X: W3, a: A0 };
    const e = seg(t, 26.4, 0.6, F.CAM);
    return { X: lerp(W3, OV, e), a: lerp(A0, AOV, e) };
  }
  // A screen-layer switch that must be hard (a caption cut, a card that re-pins opaque) is decided by the frame index, so
  // all sub-frames of a motion-blurred frame agree and the switch never renders as a one-frame 50 % mix.
  const frameOf = (t) => Math.round(t * 60);

  const stage = F.stageW(root, {
    level: { maya: () => 1, theo: F.levelFill(BACK) },
    pulses: { maya: [19.2, 21.6, 24.0, 28.8], theo: [29.1] },  // no pulse on 26.400: the held breath
    relayLabel: { typedAt: 19.2 },
    holdRing: { in: 20.4, out: BACK },
    log: true,
    trail: {
      dir: 1,
      // segment A draws maya → relay 19.200 → 20.400; segment B relay → theo 28.800 → 30.000 (brand ease)
      head: (t) => (t < 19.2 ? -1e9 : t < BACK ? seg(t, 19.2, 1.2) * aLen : bStart + seg(t, BACK, 1.2) * (len - bStart)),
      // one pitch per beat while in flight; frozen at the stop (20.400) until theo is back
      march: (t) => (pitch * (clamp(t, 19.2, 20.4) - 19.2 + Math.max(0, t - BACK))) / 0.6,
      // stop: pheromone → line-strong at 0.7 opacity (240 ms); back to pheromone (120 ms) at 28.800
      style: (t, sg) => {
        if (sg === 'B') return { colour: C.accent, alpha: 1 };
        const k = seg(t, 20.4, 0.24) * (1 - seg(t, BACK, 0.12));
        return { colour: F.stageW.mix(C.accent, C.lineStrong, k), alpha: lerp(1, 0.7, k) };
      },
    },
    // 16:9 push 21.600 → 22.800: s 1 → 2, c (960, 540) → (960, 525), camera ease (FILM.CAM: starts and ends at rest).
    // 4:5: no push.
    cam: P ? null : (t) => { const e = seg(t, 21.6, 1.2, F.CAM); return { x: 960, y: lerp(540, 525, e), s: lerp(1, 2, e) }; },
    // the machine plates are not read during the push: blur 0 → 4 px (screen), back to 0 at 28.800 (240 ms)
    plateBlur: P ? null : (t) => 4 * seg(t, 21.6, 1.2) * (1 - seg(t, BACK, 0.24)),
    // the shot's own world updates (they run with every sub-frame of the motion blur below)
    onWorld: (t) => {
      if (!P) {
        // 16:9: the push leaves only a fragment of each prompt at the frame edge (`th` at x 1855). The plates are not
        // read once pushed in: both prompts fade out with the push (600 ms from 21.600) and stay out to the cut.
        const pa = op(1 - seg(t, 21.6, 0.6));
        css(stage.prompts.maya.el, { opacity: pa }); css(stage.prompts.theo.el, { opacity: pa });
      }
      stage.log.render(t, 20.4, BACK);
      if (P) { const st = strip(t); stage.log.setStrip(st.X, st.a); }
    },
    // Motion blur: the push now uses the camera ease and render.mjs's own 180° blur (FILM.blur below) over the whole
    // move, so Stage W's in-page shutter is off (both together would blur twice).
    shutter: null,
  });
  // render.mjs motion blur (180°, 8 sub-frames, linear light) over each camera move. 16:9: the push (eased, it peaks at
  // ≈ 16 px per frame at the plate edges and 13.5 on the log text, under the 19 px threshold, but it is a 2× push and
  // keeps the blur its brand-ease version had). 4:5: the two strip pans and the zoom to the overview (peaks 41, 25 and
  // 40 px per frame). The blur fades out with the speed at both ends of each move.
  // director: no blur on the 16:9 push (peaks ~15 px/frame, under the 19 px rule)
  F.blur(23.4, 24.0, '4x5');
  F.blur(25.2, 25.8, '4x5');
  F.blur(26.4, 27.0, '4x5');

  // ---------------------------------------------------------------- captions (screen)
  const C6 = S.caption('It waits at the relay.', { visible: (t) => frameOf(t) < frameOf(22.2) });   // present; cuts out 22.200
  const C7 = [S.caption('The relay sees', { line: 0, enter: { at: 22.2 } }),                // enters 22.200, lands 22.440
    S.caption('who, when and how big.', { line: 1, enter: { at: 22.2 } })];
  const C8 = S.caption('Not what.', { baseline: P ? 306 : 320, typed: { at: 26.4 } });      // typed 26.400 → 27.106, silent
  for (const c of [C6, ...C7, C8]) stage.screen.append(c.el);

  // ---------------------------------------------------------------- determination cards (screen)
  const ov = S.overlay(stage.screen, 6);                 // leaders and the screen underlines
  const cardLayer = F.h('div', { style: `position:absolute;left:0;top:0;width:${FMT.W}px;height:${FMT.H}px;z-index:7` });
  stage.screen.append(cardLayer);
  const LW = P ? 3 : 2;                                  // leader: 1 brand px, text-2
  // screen underline: the spec's dot count (floor(w / pitch) + 1) with the dot run centred in its field, so the run
  // does not stop short of the field's last character (16:9 When +10.9, Who +1.6, How big +10.25; 4:5 When +14.4)
  const pitchU = 12 * F.Z;
  const centre = (w, p) => (w - Math.floor(w / p + 1e-6) * p) / 2;

  const cards = [];
  if (!P) {
    // 16:9: Special Elite 40 px, height 106, bottom y 740. Leader 21° off vertical to y 782.5 (8 px above line A's caps).
    const DEF = [
      { text: 'When', cx: 296.3, ax: 280.0, at: 23.4, ul: [96.3, 310.1], ulAt: 23.52, ulOut: 24.6 },
      { text: 'Who', cx: 1075.2, ax: 1091.5, at: 24.6, ul: [885.9, 1297.1], ulAt: 24.72, ulOut: 25.8 },
      { text: 'How big', cx: 1673.7, ax: 1690.0, at: 25.8, ul: [1659.0, 1823.5], ulAt: 25.92, ulOut: 26.4 },
    ];
    for (const d of DEF) {
      const hgt = 1.25 * 40 + 2 * 0.7 * 40;
      const cd = F.stageW.card(d.text, { size: 40, x: d.cx, anchor: 'middle', top: 740 - hgt, typedAt: d.at });
      cardLayer.append(cd.el);
      const ld = svgEl('line', { x1: d.cx, y1: 740, x2: d.cx, y2: 740, stroke: C.text2, 'stroke-width': LW, opacity: 0 });
      ov.append(ld);
      const dc = centre(d.ul[1] - d.ul[0], pitchU);
      const ul = F.ui.underline(ov, { x0: d.ul[0] + dc, x1: d.ul[1] + dc, y: 822, at: d.ulAt, outAt: d.ulOut });
      cards.push({ d, cd, ld, ul });
    }
  } else {
    // 4:5: Special Elite 42 px, height 111.3, bottom y 551. Cards are anchored to a strip character and ride with it.
    const DEF = [
      { text: 'When', i: 28.32, lean: 1, at: 22.8, ul: [16, 29], ulAt: 22.92, ulOut: 23.4, out: 23.4 },
      { text: 'Who', i: 76.5, lean: -1, at: 24.0, ul: [64, 89], ulAt: 24.12, ulOut: 25.2, out: 25.2 },
      // How big's underline fades 26.280 → 26.400, so it is gone before the zoom starts (it no longer rides the zoom)
      { text: 'How big', i: 111.4, lean: -1, at: 25.8, ul: [111, 121], ulAt: 25.92, ulOut: 26.28 },
    ];
    for (const d of DEF) {
      const hgt = 1.25 * 42 + 2 * 0.7 * 42;
      const cd = F.stageW.card(d.text, { size: 42, x: 0, anchor: 'middle', top: 551 - hgt, typedAt: d.at });
      cardLayer.append(cd.el);
      const ld = svgEl('line', { stroke: C.text2, 'stroke-width': LW, opacity: 0 });
      ov.append(ld);
      // underline (y 626) at its W-phase place; it rides with the strip while it fades
      const ug = svgEl('g');
      ov.append(ug);
      const W = d.at < 23.4 ? W1 : d.at < 25.2 ? W2 : W3;
      const ux0 = W + d.ul[0] * A0, ux1 = W + d.ul[1] * A0, dc = centre(ux1 - ux0, pitchU);
      const ul = F.ui.underline(ug, { x0: ux0 + dc, x1: ux1 + dc, y: 626, at: d.ulAt, outAt: d.ulOut });
      cards.push({ d, cd, ld, ul, ug, ux0 });
    }
  }

  // ---------------------------------------------------------------- sound (SCRIPT.md §5.3)
  const fmt = FMT.name;
  F.cue(19.2, 'LUB', { desc: 'maya pulse' });
  F.cue(19.2, 'TICK', { pan: -0.5, desc: 'trail to the relay' });
  F.cueLetters(stage.relayLabel.times, 'LABEL', 0, { desc: 'Relay typed' });
  F.cue(19.8, 'TICK', { pan: -0.25 });
  F.cue(20.4, 'PAD', { action: 'release', ms: 150, desc: 'hard stop: no tick; PAD → DRONE' });
  F.cue(20.4, 'DRONE', { note: 'A2', desc: 'the wait' });
  F.cue(20.4, 'ROOM', { gain: -56, desc: 'ROOM back at −56 dBFS' });
  F.cue(21.6, 'LUB', { gain: -4, desc: 'push in' });
  F.cue(24.0, 'LUB', { gain: -6, desc: 'the last before the held breath' });
  for (const c of cards) {
    const shared = c.d.text === 'How big';            // How big types at the same times in both formats
    F.cueLetters(c.cd.times, 'LABEL', 0, shared ? { desc: `${c.d.text} typed` } : { fmt, desc: `${c.d.text} typed (${fmt})` });
  }
  F.cue(1624 / 60, 'LOW', { note: 'D3', desc: 'first frame where the full stop of "Not what." shows' });
  F.cue(BACK, 'PIANO', { note: 'Dadd9 D3 A3 E4 F#4', roll: 0.03, desc: 'theo is back: the A drone resolves to D' });
  F.cue(BACK, 'LUB', { desc: 'maya pulse' });
  F.cue(BACK, 'TICK', { pan: 0 });
  F.cue(BACK, 'PAD', { action: 'return', attack: 0.6, desc: 'PAD back to the product colour' });
  F.cue(29.1, 'DUB', { desc: 'DUB returns' });
  F.cue(29.4, 'TICK', { pan: 0.5 });

  // ---------------------------------------------------------------- render
  const attr = (el, k, v) => { if (el['__' + k] !== v) { el.setAttribute(k, v); el['__' + k] = v; } };   // touch the DOM only on change
  return (tl, t) => {
    stage.render(t);
    const st = P ? strip(t) : null;
    C6.render(t); C7[0].render(t); C7[1].render(t); C8.render(t);

    for (const c of cards) {
      const { d, cd, ld, ul } = c;
      // card locks from y −8 over 240 ms; its stock is opaque within 80 ms (before the first letter has stamped in,
      // so it never reads as a see-through grey box); the leader draws over 120 ms meanwhile
      const p = seg(t, d.at, 0.24);
      let a = clamp((t - d.at) / F.stageW.CARD_IN);
      const dy = -8 * (1 - p);
      const lp = seg(t, d.at, 0.12);
      let cx, ax, yEnd, yTop;
      if (!P) { cx = d.cx; ax = d.ax; yTop = 740; yEnd = 782.5; } else {
        // leader end 8 px above line A's cap top (Plex cap height 0.698 em); 21° off vertical
        const fs = 36 * (st.a / A0);
        yTop = 551; yEnd = 610 - 0.698 * fs - 8;
        ax = st.X + d.i * st.a;
        cx = ax + d.lean * (yEnd - yTop) * TAN21;
        // a card the strip pans away from fades out with its leader over 120 ms from the pan start (as the underline
        // does), so no sliver of label stock hangs at the frame edge (at W3 the `Who` card would end at x 6); it
        // re-pins fully opaque from 26.400 (decided per frame, see frameOf), so the zoom to the overview slides an
        // opaque card back into frame, never a see-through one
        if (d.out != null) a *= frameOf(t) < frameOf(26.4) ? 1 - clamp((t - d.out) / 0.12) : 1;
      }
      css(cd.el, { opacity: op(a), transform: `translate(${(cx - cd.w / 2).toFixed(2)}px,${(cd.y0 + dy).toFixed(2)}px)` });
      cd.render(t);
      attr(ld, 'x1', cx.toFixed(2)); attr(ld, 'y1', yTop.toFixed(2));
      attr(ld, 'x2', (cx + (ax - cx) * lp).toFixed(2)); attr(ld, 'y2', (yTop + (yEnd - yTop) * lp).toFixed(2));
      attr(ld, 'opacity', op(t >= d.at ? a : 0));
      ul.render(t);
      if (P) attr(c.ug, 'transform', `translate(${(st.X + d.ul[0] * st.a - c.ux0).toFixed(2)},0)`);
    }
  };
} });
