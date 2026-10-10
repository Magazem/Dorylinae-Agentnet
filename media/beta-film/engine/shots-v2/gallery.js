// v2 component gallery (not in the film): film.html?cut=gallery (shots-gallery/manifest.js). Each page lays one part of
// SCRIPT-v2 out at its real world geometry and camera, and plays its states on the script's own beat times, so a still at
// gallery time g shows the scene at script time from + (g − page start). Pages:
//   A  maya's dock      CAM-M     letter out, 8 lines, fold, into the machine; envelope out, letter drops, form, flap, seal
//   B  the counter      R0 → R+   sealed envelope on the counter, push, pointer FROM → TO → TIME → SIZE → seal tab, settle
//   C  the wait         R0 drift  clock 13:41:40 → :45 in place, theo offline → online (ring fill, arrival pulse), lift + trail
//   D  theo's dock      CAM-T     envelope into the machine, brief out with its crease, inbox, result; the brief goes back in
//                                 (G46.25, 0.45 s) and only then the answer sheet comes out (G46.9): two sheets never cross
//                                 (r3), pointer, fold, answer envelope out carrying the folded answer, flap, seal
//   E1 flap states      static    180° 140° 100° | 80° 40° 0° + sealed (shoulders, seams, the open flap's inside tone)
//   E2 fold states      static    0 · 0.2 · 0.45 | 0.55 · 0.8 · 1 (hinge about the crease, print → blank white back at 90°, angle tone)
//   E3 thumbnail read   static    the sealed, the open and our envelope at 240 px wide (and 480): does it read as an envelope
//   F  our envelope     IV        travels in on its trail, push, slit, letter rises / envelope falls, unfold, note, signature
//   K  the card         III       display line, lines 2 and 3 (the footnote only if SHOW_REVIEW_FOOTNOTE), slow drift
//   V  end              V         V2.endStage: the boot unchanged, then `Apply at dorylinae.net`
//   F2 owner set (r3)   IV end    the note's signature as BRIEF-v2's final owner decision ('Yazan'), for review
//   K2 owner set (r3)   III       the card without the footnote (SHOW_REVIEW_FOOTNOTE = false), for review
// dir-v2: common-v2.js's defaults are now the owner set (SIGNATURE 'Yazan', SHOW_REVIEW_FOOTNOTE false), so F / K show
// what the film shows and F2 / K2 repeat it explicitly; the note is stationery (NOTE_LETTERHEAD) with its long-perspective
// unfold (NOTE_PERSP); theo's key line reads in full from G43 to G45.75 (rows.identity keyDimAt, as the film).
// r3: every page registers its blur windows (§4.10 and V2.BLUR: hinges, flaps, theo's brief going in) in gallery time, so
// render.mjs clips of the gallery blur exactly as the film will.
(() => {
  const F = window.FILM, V = F.V2;
  const { C, FMT, h, css, seg, lerp, clamp } = F;
  const { T, GEO: G, motion: M, cam, rows, pick } = V;
  const P = FMT.portrait, CAMF = F.CAM;
  const S = V.SLIDE;
  /** a slide's progress (FILM.CAM over its own duration) */
  const sl = (t, at, k) => seg(t, at, S[k].dur, CAMF);
  let gt = 0;
  const PAGES = [];
  /** a gallery page: `from` is the script time shown at the page's first frame. build(root, blur): blur([t0, t1]) registers
   *  a blur window given in script seconds (clipped to the page) */
  function page(id, from, dur, build) {
    const start = gt; gt += dur;
    PAGES.push({ id, start, from, dur });
    const blur = ([a, b]) => { const t0 = Math.max(a, from), t1 = Math.min(b, from + dur); if (t1 > t0) F.blur(start + t0 - from, start + t1 - from); };
    F.shot({ id, start, end: start + dur, async build(root) { const r = await build(root, blur); return (tl) => r(from + tl); } });
  }

  // ---------------------------------------------------------------- the scene's fixed stage
  function stage(root, o = {}) {
    const L = V.layers(root, { place: 'scene' });
    const W = L.world;
    const relay = V.relay(W, { clock: o.clock ?? null });
    const maya = V.machine(W, { who: 'maya', rows: o.mayaRows ?? [], dim: o.mayaDim });
    const theo = V.machine(W, { who: 'theo', rows: o.theoRows ?? [], dim: o.theoDim });
    // chips hang on the sky: the travelling envelope passes in front of them
    const tsvg = L.svg(W);                                    // trails lie on the ground: behind the relay, plates, chips
    W.prepend(tsvg);
    const chips = {
      maya: V.chip(W, { who: 'maya', level: 1, pulses: o.mayaPulses ?? [], dim: o.mayaChipDim, fade: o.chipFade }),
      theo: V.chip(W, { who: 'theo', level: o.theoLevel ?? 0, pulses: o.theoPulses ?? [], arrivals: o.theoArrivals ?? [], dim: o.theoChipDim, fade: o.chipFade }),
    };
    const paperLayer = h('div', { style: 'position:absolute;left:0;top:0' });
    W.append(paperLayer);
    const slot = V.captionSlot(L.screen, { items: o.captions ?? [] });
    const parts = [relay, maya, theo, chips.maya, chips.theo, slot];
    return {
      L, W, relay, maya, theo, tsvg, paperLayer, chips, slot,
      add(p) { paperLayer.append(p.el); parts.push(p); return p; },
      also(p) { parts.push(p); return p; },
      render(t, c) { L.apply(c); for (const p of parts) p.render(t); },
    };
  }
  // the machines' terminal contents over the scene (§7)
  const mayaRows = [rows.identity('maya', 'QZ_vPMFN', -1, T(11)), rows.cmd('maya', V.TERM.request, T(11)), rows.out(V.TERM.queued, T(16.25))];
  const theoRows = [rows.identity('theo', 'ZhbF0Hsy', -1, T(44.5), T(45.75)), rows.inbox('theo', T(44.5), { dimAt: T(45.75) }),
    rows.cmd('theo', V.TERM.result, T(45.75))];
  const S1 = V.COPY.S1.map((ln, i) => (i === 0 ? [{ text: 'With ' }, { text: 'Dorylinae', colour: V.colourShift(C.text, C.brand, 6.314) }, { text: ln.slice('With Dorylinae'.length) }] : ln));
  const captions = [
    { at: T(7.5), lines: S1 }, { at: T(13), lines: V.COPY.S2 }, { at: T(16.75), lines: V.COPY.S3 },
    { at: T(21.5), lines: V.COPY.S4, build: V.COPY.S4b.map((s, i) => ({ text: s, at: T(22 + i) })) },
    { at: T(28), lines: V.COPY.S5 }, { at: T(32), lines: V.COPY.S6 }, { at: T(40), lines: V.COPY.S7 },
    { at: T(45), lines: V.COPY.S8 }, { at: T(49), lines: V.COPY.S9 },
  ];
  const mayaPulses = V.beats(8, 56, 4, 0);
  const theoPulses = V.beats(41, 56, 4, 2);
  const theoLevel = F.levelFill(T(40));
  const chipDimMaya = (t) => seg(t, T(20.25), 0.24) * (1 - seg(t, T(55), 0.24));
  const chipDimTheo = (t) => seg(t, T(20.25), 0.24) * (1 - seg(t, T(32), 0.24));
  const theoTermDim = (t) => 1 - seg(t, T(43), 0.24);
  // r2: maya's terminal is texture at CAM-R0 too (cut by the frame edge): 40 % once the camera leaves her, back at G55
  const mayaTermDim = (t) => seg(t, T(17.5), 0.24) * (1 - seg(t, T(55), 0.24));
  // r2: in 16:9 the 1.3 push would cut both chips mid-word at the frame edges: they fade out with move 2 and back in during
  // move 3, once the settle has brought them inside the frame. 4:5 keeps theo's (centred, whole at s 1.2)
  const chipFade = P ? null : (t) => (t < T(30.25) ? 1 - seg(t, T(20.25), 0.24) : seg(t, T(30.25), 0.24));
  const common = { mayaRows, theoRows, captions, mayaPulses, theoPulses, theoLevel, theoArrivals: [T(40)], mayaChipDim: chipDimMaya, theoChipDim: chipDimTheo,
    theoDim: theoTermDim, mayaDim: mayaTermDim, chipFade };

  // the brief envelope over its whole life (maya's slot → the counter → theo's slot), in world px, the folded brief inside
  const EW = G.env.w;
  function briefEnvelope() {
    const [mx, my] = G.envAt.maya, [rx, ry] = G.envAt.relay, [tx, ty] = G.envAt.theo;
    const pos = (t) => {
      if (!P) {
        let x = mx, y = my - S.envOut.d * (1 - sl(t, T(13.25), 'envOut'));
        x = lerp(x, rx, seg(t, T(16.75), T(20) - T(16.75), CAMF)); y += M.hop(t, T(16.75), T(20));
        x = lerp(x, tx, seg(t, T(40.25), T(43) - T(40.25), CAMF)); y += M.hop(t, T(40.25), T(43), undefined, false);   // dirfix: no settle (as the film)
        y -= S.envIn.d * sl(t, T(43), 'envIn');
        return [x, y];
      }
      let y = my - S.envOut.d * (1 - sl(t, T(13.25), 'envOut'));
      y = lerp(y, ry, seg(t, T(16.75), T(20) - T(16.75), CAMF));
      y = lerp(y, ty, seg(t, T(40.25), T(43) - T(40.25), CAMF));
      y += S.envIn.d * sl(t, T(43), 'envIn');
      return [mx, y];
    };
    return V.sealed({ kind: 'scene', sheet: 'brief', address: V.ADDR.brief, x: (t) => pos(t)[0], y: (t) => pos(t)[1],
      flap: (t) => M.flapClose(t, ...V.FLAP.brief), formAt: T(15.25), sealAt: T(16), dropAt: T(14.5), clip: [G.maya.slot, G.theo.slot] });
  }
  /** the scene's blur windows (§4.10 and V2.BLUR), script seconds; each page registers the ones it shows */
  const SCENE_BLUR = [V.BLUR.hinge(T(12)), V.BLUR.flap(V.FLAP.brief), [30.714, 31.414], V.BLUR.slide(T(46.25), 'briefIn'),
    V.BLUR.hinge(T(49.75)), ...(P ? [] : [V.BLUR.flap(V.FLAP.answer)])];

  // ================================================================ A · maya's dock (CAM-M), script 5.714 → 12.31
  page('A', T(8), 6.6, (root, blur) => {
    SCENE_BLUR.forEach(blur);
    const st = stage(root, common);
    const [lx, ly] = G.letterAt.maya;
    const letter = st.add(V.paper({ kind: 'brief', x: lx, foldDir: 'up', clip: [G.maya.slot],
      y: (t) => ly - S.letterOut.d * (1 - sl(t, T(8.5), 'letterOut')) - S.letterIn.d * sl(t, T(12.5), 'letterIn'),
      fold: (t) => M.fold(t, T(12)), lineAt: Array.from({ length: 8 }, (_, i) => T(9.75 + i / 8)) }));
    const env = st.add(briefEnvelope());
    void letter; void env;
    return (t) => st.render(t, cam.STATES.M);
  });

  // ================================================================ B · the counter (R0 → R+ → R0), script 14.286 → 22.29
  page('B', T(20), 8.0, (root) => {
    const st = stage(root, common);
    st.also(V.trail(st.tsvg, { from: G.maya.dock, to: G.envAt.relay.map((v, i) => v + (i ? G.env.h / 2 : G.env.w / 2)), travel: [T(16.75), T(20)], tail: [T(20), T(22)] }));
    const be = st.add(briefEnvelope());
    const s = cam.PUSH.s;
    ['from', 'to', 'time', 'size'].forEach((f, i) => be.env.pointer(f, { at: T(22 + i), outAt: (i < 3 ? T(23 + i) : T(27.75)) - 0.12, s }));
    be.env.pointer('seal', { at: T(28.5), outAt: T(31.5) - 0.12, s });
    const c = cam.seq([[0, cam.hold(cam.STATES.R0)], [T(20.25), cam.pushRp(T(20.25), T(21.75) - T(20.25))], [T(29), cam.settleR0(T(29), T(31) - T(29))]]);
    return (t) => st.render(t, c(t));
  });

  // ================================================================ C · the wait (R0, drift), script 22.143 → 29.44
  page('C', T(31), 7.3, (root) => {
    const ticks = [1, 2, 3, 4, 5].map((k) => T(33) + k);
    const st = stage(root, { ...common, clock: { enterAt: T(33), ticks, outAt: T(41) } });
    const lane = G.lane;
    st.also(V.trail(st.tsvg, P ? { from: [540, G.envAt.relay[1] + G.env.h / 2], to: G.theo.dock, travel: [T(40.25), T(43)], tail: [T(43), T(45)] }
      : { from: [960, lane], to: [2240, lane], travel: [T(40.25), T(43)], tail: [T(43), T(45)] }));
    st.add(briefEnvelope());
    // dirfix: the wait holds CAM-R0 in both formats (as V2.cam.scene; the drift / tilt made text and stars simmer)
    const R0 = cam.STATES.R0, Tc = cam.STATES.T;
    const c = cam.seq([[0, cam.hold(R0)], [T(40.25), cam.tween(R0, Tc, T(40.25), T(43) - T(40.25))]]);
    return (t) => st.render(t, c(t));
  });

  // ================================================================ D · theo's dock (CAM-T), script 30.414 → 37.41
  page('D', T(43) - 0.3, 7.0, (root, blur) => {
    SCENE_BLUR.forEach(blur);
    const st = stage(root, common);
    const [bx, by] = G.letterAt.theo, [ax, ay] = G.answerAt.theo, [tx, ty] = G.envAt.theo;
    const slot = [G.theo.slot];
    const dir = P ? -1 : 1;                          // theo's slot is above his dock in 16:9 (paper comes down), below in 4:5
    // theo's copy of the brief: out of his machine unfolded, with the crease of its fold at maya's (notched edges, the
    // turned half a barely different white). r3 (gate r2): it goes back into his machine first (G46.25, 0.45 s, FILM.CAM,
    // blur window) and the answer comes out of the same slot only once it has gone (G46.9): two white sheets never cross
    st.add(V.paper({ kind: 'brief', x: bx, crease: true, clip: slot,
      y: (t) => by - dir * S.briefOut.d * (1 - sl(t, T(44), 'briefOut')) - dir * S.briefIn.d * sl(t, T(46.25), 'briefIn') }));
    st.add(briefEnvelope());
    const ans = st.add(V.paper({ kind: 'answer', x: ax, foldDir: P ? 'down' : 'up', clip: slot, edge: 'strong',
      y: (t) => ay - dir * S.answerOut.d * (1 - sl(t, T(46.9), 'answerOut')) - dir * S.answerIn.d * sl(t, T(50.25), 'answerIn'),
      fold: (t) => M.fold(t, T(49.75)) }));
    // the pointer under the summary, never longer than the line it marks (gate r1). A row-1 run in the 15 px between the rows
    // (pointer(0, {dy:'gap'})) crowds row 2's ascenders at the brief's 42 / 38 px pitch, so only row 2, the finding itself
    // (`one ledger row`), is underlined, at its own width, 16 / 18 px under its baseline
    ans.pointer(1, { at: T(48), outAt: T(49.75) - 0.12 });
    // the answer envelope: out of theo's slot with the folded answer in its mouth, which drops in before the flap closes
    // (16:9, V2.FLAP.answer: frames 2210 → 2228); 4:5 rises out already closed with the answer inside. r2: in 16:9 it starts
    // a quarter beat earlier (G50.5, script G50.75), so the answer is seen in its mouth: the mouth is out from 36.60 s, the
    // answer drops at frames 2202 → 2209 (r3), the envelope settles (36.82 s) and the flap closes; it emerges in front of
    // the folded answer's last 0.2 s into the slot.
    const envAt = T(P ? 50.75 : 50.5);
    st.add(V.sealed({ kind: 'scene', sheet: 'answer', address: V.ADDR.answer, x: tx, clip: slot,
      y: (t) => ty - dir * S.answerEnvOut.d * (1 - sl(t, envAt, 'answerEnvOut')),
      flap: (t) => (P ? 0 : M.flapClose(t, ...V.FLAP.answer)), formAt: T(52), sealAt: T(52), dropAt: P ? null : V.FLAP.answerDrop / 60 }));
    return (t) => st.render(t, cam.STATES.T);
  });

  // ================================================================ E1 · flap states (static)
  page('E1', 0, 2.0, (root) => {
    const L = V.layers(root, { place: null });
    const angles = [180, 140, 100, 80, 40, 0];
    const cols = P ? 2 : 3, cw = P ? 760 : 900, rh = P ? 620 : 640;
    const parts = angles.map((a, i) => {
      const cx = (i % cols) * cw, cy = Math.floor(i / cols) * rh;
      const e = V.sealed({ kind: 'scene', sheet: 'brief', x: cx, y: cy + V.ENV.scene.flapH, flap: a, formAt: null, sealAt: a === 0 ? null : Infinity, dropAt: a > 90 ? Infinity : null });
      L.world.append(e.el);
      return e;
    });
    const gw = (cols - 1) * cw + EW, gh = (Math.ceil(angles.length / cols) - 1) * rh + V.ENV.scene.flapH + V.ENV.scene.h;
    const s = P ? 0.62 : 0.66;
    return (t) => { L.apply({ x: gw / 2, y: gh / 2, s }); for (const p of parts) p.render(t); };
  });

  // ================================================================ E2 · fold states (static)
  page('E2', 0, 2.0, (root) => {
    const L = V.layers(root, { place: null });
    const folds = [0, 0.2, 0.45, 0.55, 0.8, 1];
    const cols = P ? 2 : 3, cw = P ? 720 : 820, rh = P ? 600 : 640;
    const parts = folds.map((f, i) => {
      const p = V.paper({ kind: 'brief', x: (i % cols) * cw, y: Math.floor(i / cols) * rh, fold: f, crease: f === 0, lineAt: null });
      L.world.append(p.el);
      return p;
    });
    const gw = (cols - 1) * cw + V.SHEETS.brief.w, gh = (Math.ceil(folds.length / cols) - 1) * rh + V.SHEETS.brief.h;
    const s = P ? 0.62 : 0.6;
    return (t) => { L.apply({ x: gw / 2, y: gh / 2, s }); for (const p of parts) p.render(t); };
  });

  // ================================================================ E3 · thumbnail read (r2): 240 px wide (top row) and 480 px wide
  page('E3', 0, 2.0, (root) => {
    const L = V.layers(root, { place: 'scene' });
    const parts = [];
    const cols = P ? 2 : 3, gap = P ? 60 : 80;
    const kinds = [
      { kind: 'scene', sheet: 'brief', flap: 0, formAt: null, sealAt: null },
      { kind: 'scene', sheet: 'brief', flap: 180, formAt: null, sealAt: Infinity, dropAt: Infinity },
      { kind: 'ours', sheet: 'brief', flap: 0, formAt: null, sealAt: null },
    ].slice(0, cols);
    // r3: in 4:5 the 480 px row starts at x 40 with a 40 px gap, so the open envelope is whole in the frame
    [240, 480].forEach((tw, row) => {
      let x = P ? (row ? 40 : 80) : 120;
      const y = row === 0 ? (P ? 240 : 140) : (P ? 520 : 380);
      for (const k of kinds) {
        const env = V.sealed({ ...k, x: 0, y: k.flap > 90 ? V.ENV.scene.flapH : 0 });
        const sc = tw / env.env.geo.w;
        const box = h('div', { style: `position:absolute;left:${x}px;top:${y}px;transform-origin:0 0;transform:scale(${sc.toFixed(5)})` }, env.el);
        root.append(box); parts.push(env);
        x += tw + (P && row ? 40 : gap);
      }
    });
    return (t) => { L.apply({ x: FMT.W / 2, y: FMT.H / 2, s: 1 }); for (const p of parts) p.render(t); };
  });

  // ================================================================ F · our envelope and the note (IV), script 46.429 → 57.143
  function invitation(root, blur, o = {}) {
    [[46.429, 47.143], [48.571, 49.286], V.BLUR.hinge(T(69))].forEach(blur);
    // dirfix: the sky holds at rest while the world pushes (as sec4-invite.js)
    V.sky(root, 'inv').render(null);
    const L = V.layers(root, { place: null });
    const E = V.ENV.ours, N = V.SHEETS.note;
    const rest = pick([460, 260], [90, 420]);
    const fall = pick(860, 930), travel = 600;
    const envPos = (t) => {
      const k = 1 - seg(t, T(65), V.BEAT, CAMF), d = fall * seg(t, T(68), V.BEAT, CAMF);
      return P ? [rest[0], rest[1] - travel * k + d] : [rest[0] - travel * k, rest[1] + d];
    };
    const tsvg = L.svg(L.world);
    // the trail: from beyond the frame edge to the envelope's centre (its head hides under the envelope)
    const centre = (t) => { const [x, y] = envPos(t); return [x + E.w / 2, y + E.h / 2]; };
    const tr = P
      ? V.trail(tsvg, { from: [540, -400], to: [540, rest[1] + E.h / 2], travel: [T(65), T(66)], tail: [T(66), T(67.5)], draw: (t) => (centre(t)[1] + 400) / (rest[1] + E.h / 2 + 400) })
      : V.trail(tsvg, { from: [-400, 490], to: [rest[0] + E.w / 2, 490], travel: [T(65), T(66)], tail: [T(66), T(67.5)], draw: (t) => (centre(t)[0] + 400) / (rest[0] + E.w / 2 + 400) });
    // the letter, folded inside (behind the envelope), rises out of the slit while the envelope falls away, then unfolds
    const inside = pick([20, 30], [10, 17]), rise = pick(160, 142);
    const note = V.paper({ kind: 'note', foldDir: 'up', crease: true, edge: 'strong',
      x: (t) => envPos(t)[0] + inside[0], y: (t) => (P ? envPos(t)[1] - seg(t, T(68), V.BEAT, CAMF) * 930 : rest[1]) + inside[1] - rise * seg(t, T(68), V.BEAT, CAMF),
      fold: (t) => M.unfold(t, T(69)), lineAt: [T(69.5), T(69.5), T(71), T(72), T(73.5), T(75)], sigAt: T(76), signature: o.signature });
    const env = V.envelope({ kind: 'ours', x: (t) => envPos(t)[0], y: (t) => envPos(t)[1], flap: 0, slitAt: T(67.5) });
    L.world.append(note.el, env.el);
    const push = cam.drift(pick([960, 556], [540, 696]), 1, pick(1.06, 1.04), T(66), T(68));
    return (t) => { L.apply(push(t)); tr.render(t); note.render(t); env.render(t); };
  }
  page('F', T(65), T(80) - T(65), (root, blur) => invitation(root, blur));

  // ================================================================ K · the card (III), script 40.000 → 46.429
  function cardPage(root, o = {}) {
    const L = V.layers(root, { place: 'inv' });
    const c = V.card(L.world, { at: [T(56), T(57), T(58), T(59.5)], footnote: o.footnote });
    // dirfix: the card and its sky hold still (as sec3-card.js; the drift made the card's text simmer)
    const rest = { x: FMT.W / 2, y: FMT.H / 2, s: 1 };
    return (t) => { L.apply(rest); c.render(t); };
  }
  page('K', T(56), T(65) - T(56), (root) => cardPage(root));

  // ================================================================ V · boot and address (V), script 57.143 → 64.286 (V2.endStage)
  page('V', T(80), T(90) - T(80), async (root) => {
    const { boot } = await V.endStage(root);
    const addr = V.text(V.COPY.ADDRESS, { role: 'address', x: FMT.W / 2, anchor: 'middle', baseline: pick(930, 1060) });
    root.append(addr.el);
    return (t) => { boot.render(t - T(80)); const a = seg(t, T(84), 0.24); css(addr.el, { opacity: F.op(a), transform: a < 1 ? `translateY(${(pick(12, 18) * (1 - a)).toFixed(2)}px)` : 'none' }); };
  });

  // ================================================================ F2 · K2 (r3): BRIEF-v2's final owner set, for review
  page('F2', T(75.5), T(80) - T(75.5), (root, blur) => invitation(root, blur, { signature: 'Yazan' }));
  page('K2', T(56), T(65) - T(56), (root) => cardPage(root, { footnote: false }));

  F.galleryPages = PAGES;
})();
