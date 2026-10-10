// Section IV · The invitation · G65 → G80 (17:2 → 21:1) · 46.429 → 57.143 s. SCRIPT-v2 §5.4, §4.4 (ours), §6 (I-0, N-1 … N-6).
// The people at Dorylinae send you an envelope addressed to your team. It opens to a short note in their own voice.
//
//   G65    hard cut. Our envelope travels in along its trail (16:9 from x −140, 4:5 from y −180; 600 px, 1 beat, FILM.CAM,
//          blur window) and settles at G66. Form `FROM Dorylinae` · `TO your team`; the seal is our mark. PAPER.
//          Trail tail fades G66 → G67.5 (the only pheromone element in the section)
//   G65→80 the camera holds at s 1.06 (4:5 1.04) about the form's centre (fix-r3: was a slow push 1.00 → 1.06 over
//          G66 → G68, see CAM below); the sky holds at rest (invLayers)
//   G67.5  a slit opens along the top fold (ink meta, 3 px, 5 px under the fold, from the centre, 120 ms); flap and seal
//          stay whole. PAPER
//   G68    the letter rises out of the slit (16:9 folded block y 290 → 700 up to 130 → 540; 4:5 437 → 817 up to 295 → 675)
//          while the envelope falls away below the frame (16:9 860 px, 4:5 930), both 1 beat, FILM.CAM, blur window. PAPER
//   G69    the letter unfolds about its crease (V2.HINGE, FILM.CAM, blur window). PAPER
//   G69.5  `We’re inviting a first wave` / `of 10 small teams, like yours.` (ink, one frame)
//   G71    `Tell us what breaks.` · G72 `That decides what we change first.` · G73.5 `Free during the closed beta.`
//   G75    `Apply at dorylinae.net`
//   G76    the signature types, 85 ms a letter, no caret (O-2, SIGNATURE). TYPE per letter
//   G80    hard cut (cut 4) on frame 3428 = 57.1333 s, the frame G80 (57.143 s) falls in: see CUT4 below
//
// The letter's folded block (16:9 960 × 410, 4:5 880 × 380) rides inside the envelope (1000 × 460 / 900 × 414), so it is
// the same paper that unfolds. Paper never fades or scales: it slides, folds on its hinge and is hidden by the envelope.
(() => {
  // ================================================================ owner decision (one-line change)
  // O-2 (§5.4): the note's signature. The owner's final answer (design/BRIEF-v2.md, "Owner decisions after the v2 script
  // (final)"): 'Yazan', exactly. audio2/score2.py (SIGNATURE = "Yazan") types the same 5 letters on the same times. This
  // constant is the film's only switch for it: the note is built with it explicitly (common-v2.js's own SIGNATURE is only
  // the gallery's default). Any name up to `Yazan, Dorylinae` fits the signature line.
  const SIGNATURE = 'Yazan';

  const F = window.FILM, V = F.V2;
  const { seg } = F;
  const { T, pick, P, BEAT, motion: M } = V;
  const CAM = F.CAM;
  // Cut 4 (IV → V) on an even 60 fps frame (director's notes, v2 QA): the 30 fps delivery averages the frame pairs
  // (2k, 2k + 1), so a hard cut on an odd frame (G80 lands in frame 3429) blends the invitation and the bare end sky into
  // one 30 fps frame. Section IV ends and section V starts on frame 3428 (sec5-end.js has the same CUT4): frame 3427 is
  // the invitation's last, 3428 the bare end sky's first. 1 µs early: a frame time computed as start + f / fps (render.mjs
  // --start S) can land 1 ulp under 3428 / 60 and would still show section IV.
  const CUT4 = 3428 / V.FPS - 1e-6;

  // ---------------------------------------------------------------- geometry (§4.4 ours, §5.4), world px at s = 1
  const REST = pick([460, 260], [90, 420]);            // the envelope's top-left at rest
  const TRAVEL = 600, FALL = pick(860, 930);
  const INSIDE = pick([20, 30], [10, 17]);             // the folded letter's top-left inside the envelope
  const RISE = pick(160, 142);                         // 16:9 y 290 → 130, 4:5 437 → 295
  /**
   * The camera: held at the script's end state of the G66 → G68 push for the whole section (fix-r3, review r2, motion).
   * During the push the form's values simmered (`Dorylinae`, `your team`: horizontal frame-step error rms 0.18 / 0.23 px in
   * 16:9, 0.15 / 0.16 in 4:5, 11 – 17 of 85 steps backwards), as the card and the wait did before they were held. A blur
   * window, geometricPrecision and (tried here) the form text as glyph-outline SVG paths all left it as it was (paths:
   * rms 0.18 / 0.24, 0.13 / 0.17; the rasteriser quantises sub-pixel x either way), so the push is gone, not its framing:
   * the camera starts the section at s 1.06 / 1.04 about the same point, so the rise, the unfold and the note are framed
   * exactly as before, and the envelope arrives 6 % / 4 % larger. The slit, the rise and the unfold carry the motion.
   */
  const PUSH = pick({ about: [960, 556], s: 1.06 }, { about: [540, 696], s: 1.04 });
  // The trail's lane and its first dot, beyond the frame edge. `from` also sets the dots' phase at rest (they stop on
  // from + k × pitch): it is chosen so the last visible dot ends a pitch's worth of air short of the envelope's trailing
  // edge, never flush with the paper (director's notes, v2 QA: 16:9 had a dot 1 px from the edge that read as clipped).
  // 16:9 (pitch 24, r 3, edge x 460): the last dot at x 441, 16 px clear of the paper (dots are 18 px apart), the next at
  // 465 lies wholly under the paper. 4:5 (pitch 36, r 4.5, edge y 420): the last at y 393, 22.5 px clear (27 apart), the
  // next at 429 is under the paper.
  const TRAIL = pick({ y: 490, from: -15 }, { x: 540, from: -39 });

  // ---------------------------------------------------------------- motion
  const travelLeft = (t) => 1 - seg(t, T(65), BEAT, CAM);             // 1 → 0: the part of the 600 px still to go
  const fallen = (t) => seg(t, T(68), BEAT, CAM);                     // 0 → 1: the envelope falls away / the letter rises
  /** the envelope's top-left on its trip in (no fall) */
  const inPos = (t) => (P ? [REST[0], REST[1] - TRAVEL * travelLeft(t)] : [REST[0] - TRAVEL * travelLeft(t), REST[1]]);
  const envPos = (t) => { const [x, y] = inPos(t); return [x, y + FALL * fallen(t)]; };
  const notePos = (t) => { const [x, y] = inPos(t); return [x + INSIDE[0], y + INSIDE[1] - RISE * fallen(t)]; };

  /**
   * The invitation layers on the `inv` sky (V2.sky 'inv', as section III: the repaired 16:9 copy, both at display
   * resolution). dirfix: the sky holds at rest through the whole section, the push included (it followed the push at
   * 0.3: at 0.003 px a frame of parallax nobody sees it move, but its scale changed every frame, so the stars re-filtered:
   * a one-frame +12-level pop at frame 2836 and 5 – 12 % of the stars twinkling > 25 levels on every frame after it). The
   * cut at G65 leaves it exactly in place (section III's sky is the same image at rest); only the world moves.
   */
  function invLayers(root) {
    const sk = V.sky(root, 'inv');
    sk.render(null);                                   // at rest: c = frame centre, s = 1
    return V.layers(root, { place: null });            // apply(c) moves the world (the envelope and the note) only
  }

  /**
   * The slit (§4.4, "Opened by us"): a cut along the top fold, opening from the centre in 120 ms; the flap and seal stay
   * whole. Director's notes (v2 QA): the 2 px light-crease (#D5CBE8) gap drawn on the fold itself did not read at full
   * size (the PAPER cue fired on nothing), so it now takes V2.envelope's own slit style: ink meta (#665E88), a few px under
   * the top edge (SLIT.y), SLIT.h px, round ends, SLIT.inset px in from each side at full width. The letter then rises out
   * through it, from behind the envelope's top edge. Drawn as a filled path with one extra 0.0001 px point (V2.hline's
   * trick): not a rectangle, so Chromium anti-aliases it and it glides under the push's rolled camera instead of stepping
   * whole pixels. It rides on the envelope (its layer 8, over the flap and the seal tab, which starts 60+ px lower).
   */
  const SLIT = { y: 5, h: 3, inset: 12 };
  function slit(env, at) {
    const { w } = env.geo, { y: y0, h: HGT, inset } = SLIT, rr = HGT / 2, cx = w / 2, full = w - 2 * inset;
    const sv = F.s('svg', { width: w, height: y0 + HGT + 2, style: 'position:absolute;left:0;top:0;overflow:visible;z-index:8;display:none' });
    const path = F.s('path', { fill: V.C2.inkMeta });
    sv.append(path);
    env.el.append(sv);
    const y1 = y0 + HGT, f = (v) => +v.toFixed(3);
    return {
      render(t) {
        const half = (full * seg(t, at, 0.12)) / 2;
        F.css(sv, { display: half > 0 ? 'block' : 'none' });
        if (!(half > 0)) return;
        // a capsule from cx − half to cx + half (its round ends shrink to a dot while it is shorter than its height)
        const r = Math.min(rr, half), a = cx - half + r, b = cx + half - r;
        path.setAttribute('d', `M${f(a)} ${y0} L${cx} ${y0 + 0.0001} L${f(b)} ${y0} A${f(r)} ${rr} 0 0 1 ${f(b)} ${y1} `
          + `L${cx} ${y1 + 0.0001} L${f(a)} ${y1} A${f(r)} ${rr} 0 0 1 ${f(a)} ${y0} Z`);
      },
    };
  }

  FILM.shot({ id: 'IV', start: T(65), end: CUT4, build(root) {
    const L = invLayers(root);

    // the trail lies on the ground, behind the paper. Its head rides two pitches inside the envelope's trailing edge (hidden
    // by it, so the dots never end in a half-faded dot at the paper's edge), and the path ends there at rest: the tail fade
    // (G66 → G67.5, linear) then runs over the visible trail only, from the frame edge to the envelope.
    const tsvg = L.svg(L.world);
    const PITCH = 12 * F.Z, HEAD = 2 * PITCH;
    const head = (t) => (P ? inPos(t)[1] : inPos(t)[0]) + HEAD;
    const end = (P ? REST[1] : REST[0]) + HEAD;
    const trail = V.trail(tsvg, { from: P ? [TRAIL.x, TRAIL.from] : [TRAIL.from, TRAIL.y], to: P ? [TRAIL.x, end] : [end, TRAIL.y],
      travel: [T(65), T(66)], tail: [T(66), T(67.5)], draw: (t) => (head(t) - TRAIL.from) / (end - TRAIL.from) });

    // the letter: folded in half (the bottom half folded up behind it, blank back out), behind the envelope until it rises
    const note = V.paper({ kind: 'note', foldDir: 'up', crease: true, edge: 'strong',
      x: (t) => notePos(t)[0], y: (t) => notePos(t)[1], fold: (t) => M.unfold(t, T(69)),
      lineAt: [T(69.5), T(69.5), T(71), T(72), T(73.5), T(75)], sigAt: T(76), signature: SIGNATURE });
    // our envelope: closed, form and seal printed (ink present from the cut). Its slit is this file's slit() (above), not
    // V2.envelope's own (slitAt null)
    const env = V.envelope({ kind: 'ours', x: (t) => envPos(t)[0], y: (t) => envPos(t)[1], flap: 0, formAt: null, sealAt: null, slitAt: null });
    const sl = slit(env, T(67.5));
    L.world.append(note.el, env.el);

    const push = V.cam.drift(PUSH.about, PUSH.s, PUSH.s, T(66), T(68));      // constant: c and s at the push's end state

    // ---- motion blur (§4.10): our envelope in, the letter out / the envelope falling, the unfold (V2.BLUR.hinge)
    F.blur(T(65), T(66));
    F.blur(T(68), T(69));
    F.blur(...V.BLUR.hinge(T(69)));

    // ---- sound (§8)
    F.cue(T(65), 'PAPER', { G: 65, variant: 'slide', desc: 'our envelope travels in (cut 3)' });
    F.cue(T(67.5), 'PAPER', { G: 67.5, variant: 'slit', desc: 'a slit opens along the top fold' });
    F.cue(T(68), 'PAPER', { G: 68, variant: 'slide', desc: 'the letter rises out' });
    F.cue(T(69), 'PAPER', { G: 69, variant: 'unfold', desc: 'the letter unfolds' });
    const letters = [...SIGNATURE].filter((ch) => ch !== ' ');
    note.typedTimes.forEach((tt, i) => F.cue(tt, 'TYPE', { desc: `signature '${SIGNATURE}': '${letters[i]}'` }));

    return (tl, t) => { L.apply(push(t)); trail.render(t); note.render(t); env.render(t); sl.render(t); };
  } });
})();
