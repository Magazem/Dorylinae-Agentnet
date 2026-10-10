// Section II · the scene (design/SCRIPT-v2.md §5.2): G8 → G56 (3:1 → 15:1), 5.714 → 40.000 s, frames 343 → 2399.
// ONE continuous scene, no cuts inside it: maya's machine, the relay (a counter in front of a sorting rack), theo's machine,
// the brief's envelope travelling maya → counter → theo, the answer's envelope travelling back; one caption slot changing in
// place; the camera states and moves of §5.2.3 (FILM.CAM, worldCamStable); the counter's real-time clock in the wait; theo
// offline (hollow ring) → back on the music's return (G40). It enters with the 21° cut from section I (G8, DOWNBEAT).
//
// Built from FILM.V2 (shots-v2/common-v2.js: geometry, paper, machines, chips, relay, camera path, caption slot), with the
// component gates' decisions (r1 – r3) as they stand in common-v2.js and in the gallery choreography (shots-v2/gallery.js).
// Every time is a beat of the 84 BPM grid (T(G) = G × 5/7 s); discrete events land on whole frames (V2.inkOn / V2.snap).
//
// Two shots: 'II' is the scene (its root is revealed by the 21° edge); 'II-cap' is the caption slot on its own layer above
// every shot, so S-1 (entering at G7.5, half a beat before the cut) is read across the wipe and is never clipped by it.
//
// rev-sec2 r1 fixes (qa/sec2-fix-r1): the caption guard (CAP_GUARD: no light or text element of the stage under or below a
// caption in any frame; 4:5 moves 1, 4, 5); move 5's dip eased on the move's own progress (rest at both ends); theo's
// answer wholly in his slot before the answer envelope comes out (ANS_IN_DUR, ANS_FLAP, ANS_DROP); 16:9 rack stiles
// (now V.GEO.relay.holes); a readable crease on theo's copy; the answer out on the ⅛ grid (G46.875).
(() => {
  const F = window.FILM, V = F.V2;
  const { C, FMT, seg, lerp } = F;
  const { T, GEO: G, motion: M, cam, rows, pick, P } = V;
  const CAMF = F.CAM, S = V.SLIDE;

  // ================================================================ build decisions (one-line changes)
  /**
   * theo's copy of the brief. true (component gate r2, as built and reviewed in the gallery, page D): it goes back into his
   * machine at G46.25 (0.45 s, FILM.CAM, blur window) and the answer sheet comes out of the same slot only once it has gone
   * (G46.875, 0.60 s): two white sheets never cross. false: SCRIPT-v2 §5.2.4 as written: the answer slides out over the brief
   * at G46.75 and the brief stays at theo's dock to the cut (his copy, seen in K7). NOTE (rev-sec2 r1): in 4:5, false would
   * carry the brief (paper: it never fades) down through the caption block on move 5 (S-9), against §4.3, so it is a 16:9-only
   * option as built (e.g. pick(false, true)).
   */
  const BRIEF_BACK_IN = true;
  /** S-1 (`With Dorylinae, your agent hands it to theirs.`, G7.5 → G13) is drawn by this file, above both shots. Set false
   *  if section I draws it (then this slot starts at S-2, G13). It must be drawn exactly once. */
  const OWN_S1 = true;
  /**
   * The caption guard (§4.3: nothing of the stage enters the text block; only dark plates pass behind it). Every light or
   * text element of the stage (the `Relay` sign, both machines' terminal text, both chips) is hidden whenever any part of it
   * is lower on screen than CAP_GUARD px above the current caption's cap top (two lines 1104, one line 1162 in 4:5): it
   * fades out (120 ms, brand ease) before it gets there and back in (240 ms) once it has risen clear. Computed per frame
   * from the camera. In practice it acts only in 4:5, on moves 1, 4 and 5 (16:9 never brings these elements that low).
   */
  const CAP_GUARD = 24;

  const START = T(8), END = T(56);
  const sl = (t, at, k) => seg(t, at, S[k].dur, CAMF);         // a paper slide's progress (FILM.CAM over its own duration)
  const dir = P ? -1 : 1;                                       // theo's slot: above his dock (16:9), below it (4:5)
  /**
   * theo's folded answer goes into his slot in 0.25 s / 0.35 s (script 0.40; 4:5 peak 15.4 px a frame, no blur window;
   * final polish: the 16:9 sheet is 400 tall with its crease at 280, so its packet travels 274 px, peak ~30 px a frame:
   * a blur window, 35.893 → 36.143 s, below),
   * so it is wholly in before the answer envelope comes out of the same slot (16:9 G50.625, 4:5 G50.75): one paper in the
   * slot at a time (rev-sec2 r1: at 0.40 s its edge showed under the descending envelope, frames 2164 – 2168). 4:5 0.35 s
   * (dir-v2): its answer sheet is 400 tall; dirfix: it folds its top 160 px down (V2.SHEETS.answer), so the 240 px packet
   * travels 234 px (peak 18.6 px a frame, under 19); in by 36.243 s, the envelope starts at 36.250 s.
   */
  const ANS_IN_DUR = pick(0.25, 0.35);
  /**
   * 16:9 answer envelope (gate r2 beat, re-timed for G50.625): out 36.161 → 36.911 s (frame 2214.6); the folded answer shows
   * in its mouth from frame 2202, drops in over 2207 → 2214 as it settles, and the flap closes over 2210 → 2228 (still
   * behind the letter until it passes 90°, frame 2219); the seal and form on 2229 (G52) as before.
   */
  const ANS_FLAP = [2210, 2228], ANS_DROP = 2207;
  /**
   * theo's key line comes up to full text-3 at G41.75 (29.821 s), a beat and a quarter before the envelope addressed to it
   * sets down at his dock (G43), so the two read together for about a second (dir-v2; from G43 alone, ~0.3 s: the form's
   * TO row is in his slot 0.34 s (4:5) / 0.42 s (16:9) after it lands). The rest of his terminal comes up at G43 (§5.2.4).
   * In 4:5 the caption guard holds his text out until ≈ 29.93 s, so there it fades in at full with his terminal.
   */
  const KEY_UP = T(41.75);
  /**
   * fix-r3 (review r2, viewer: theo's arrival, the busiest 2 s, left the proof of delivery on screen too briefly). In 16:9
   * the 3-line result scrolls the inbox's command and header out but keeps its row (`r-97ae…  maya  task … Track down the
   * flaky payments test`) as the window's top line; it now stays at full text-3 until the answer sheet comes out (G46.875)
   * instead of dimming as the result lands (G45.75), so the inbox evidence reads beside the result for another 1.1 s.
   * 4:5: the 4-line result scrolls the row out on its own frame, so its dim is moot (unchanged).
   */
  const INBOX_DIM = P ? T(45.75) : T(46.875);

  // ================================================================ when things happen (§5.2.4), absolute seconds
  const AT = {
    letterOut: T(8.5), lines: Array.from({ length: 8 }, (_, i) => T(9.75 + i / 8)), command: T(11), fold: T(12), letterIn: T(12.5),
    envOut: T(13.25), drop: T(14.5), form: T(15.25), seal: T(16), queued: T(16.25),
    trip1: [T(16.75), T(20)], push: [T(20.25), T(21.75)], settle: [T(29), T(31)], wait: [T(32), T(40)],
    clockIn: T(33), back: T(40), clockOut: T(41), trip2: [T(40.25), T(43)], envIn: T(43),
    // the answer comes out on the ⅛-beat grid (G46.875, 33.482 s): theo's brief is in by then (its last 4 ms move it
    // 0.01 px; its slide ends 33.486)
    briefOut: T(44), inbox: T(44.5), result: T(45.75), briefIn: T(46.25),
    answerOut: BRIEF_BACK_IN ? T(46.875) : T(46.75), ptr: T(48), ansFold: T(49.75), ansIn: T(50.25),
    // 16:9: the answer envelope starts an eighth of a beat early (G50.625; gate r2 had G50.5, before the folded answer
    // was in), so the folded answer is still seen in its mouth before it drops (ANS_DROP) and the flap closes (ANS_FLAP);
    // 4:5 rises out closed (G50.75, 0.80 s)
    ansEnvOut: T(P ? 50.75 : 50.625), ansSeal: T(52), trip3: [T(52.5), T(55)], home: T(55),
  };
  // the counter clock: 13:41:40 at G33, then one real second per second, each tick on its whole frame (§4.8; §3 frames
  // 1474, 1534, 1594, 1654 and :45 on 1714 = G40, the music's return)
  const TICKS = [1, 2, 3, 4, 5].map((k) => V.snap(AT.clockIn + k));

  // ================================================================ sound accents (§8), registered once per format
  const cue = (t, kind, extra = {}) => F.cue(t, kind, { fmt: FMT.name, sec: 'II', ...extra });
  cue(AT.letterOut, 'PAPER', { variant: 'slide', desc: "the letter slides out of maya's machine" });
  cue(AT.command, 'LAND', { desc: "the agent's command lands on maya's terminal" });
  cue(AT.fold, 'PAPER', { variant: 'fold', desc: 'the letter folds in half' });
  cue(AT.envOut, 'PAPER', { variant: 'slide', desc: 'the open envelope slides out with the letter' });
  cue(AT.form, 'LAND', { desc: 'the address form is printed' });
  cue(AT.seal, 'SEAL', { desc: 'the seal (the lift)' });
  cue(AT.queued, 'LAND', { desc: 'output: Queued normal task request' });
  cue(AT.trip1[1], 'ARRIVE', { note: 'root', desc: 'it sets down on the counter' });
  TICKS.slice(0, 4).forEach((t, k) => cue(t, 'CLOCK', { variant: k % 2 ? 'tock' : 'tick', desc: `clock :${41 + k} (real seconds)` }));
  cue(TICKS[4], 'CLOCK', { variant: 'tick', desc: 'clock :45 (theo is back)' });
  cue(AT.back, 'BACK', { desc: "theo's ring fills" });
  cue(AT.envIn, 'ARRIVE', { note: 'fifth', desc: "it sets down at theo's dock" });
  cue(AT.briefOut, 'PAPER', { variant: 'slide', desc: "the brief slides out of theo's machine" });
  cue(AT.inbox, 'LAND', { desc: "theo's inbox lands" });
  cue(AT.result, 'LAND', { desc: 'the result command lands' });
  cue(AT.answerOut, 'PAPER', { variant: 'slide', desc: 'the answer sheet slides out' });
  cue(AT.ansFold, 'PAPER', { variant: 'fold', desc: 'the answer sheet folds' });
  cue(AT.ansEnvOut, 'PAPER', { variant: 'slide', desc: 'the answer envelope slides out' });
  cue(AT.ansSeal, 'SEAL', { desc: "the answer's seal" });
  cue(AT.home, 'ARRIVE', { note: 'root', desc: "it sets down at maya's dock" });

  // ================================================================ motion blur windows (§4.10 and V2.BLUR)
  // §4.10: the envelope into theo's machine (30.714 → 31.414) and move 5, the return (37.500 → 39.286), both formats.
  // V2.BLUR (gate r3): every hinge (the letter's fold, the flap, the answer's fold, the answer's flap in 16:9) and theo's
  // brief going back in. No ink event, caption change or cut falls inside any of them.
  [V.BLUR.hinge(AT.fold), V.BLUR.flap(V.FLAP.brief), [T(43), T(43) + S.envIn.dur], ...(BRIEF_BACK_IN ? [V.BLUR.slide(AT.briefIn, 'briefIn')] : []),
    V.BLUR.hinge(AT.ansFold), ...(P ? [] : [V.BLUR.flap(ANS_FLAP), [AT.ansIn, AT.ansIn + ANS_IN_DUR]]), AT.trip3].forEach(([a, b]) => F.blur(a, b));
  // fix-r3 (review r2, motion): the 30 fps delivery's own windows (V2.blur30; at 60 fps these peak under 19 px a frame and
  // stay sharp). At 30 fps (qa/dirfix/fix-r3/tools/scan30.mjs) they stepped 27 – 37 px a frame: maya's letter out and back
  // in, the brief's envelope out of her slot, move 1, move 4, theo's brief out, the answer out and back in, the answer's
  // envelope out. Each is trimmed clear of a 21° edge and of a caption's entrance (240 ms): the letter starts under the
  // wipe (ends 6.314 s), its way back in ends where S-2 enters (G13), the envelope out and move 1 start after S-2's and
  // S-3's rise, move 4 after S-7's, theo's brief ends where S-8 enters (G45); the trimmed ends are its slowest frames.
  // final r1 (review r1, motion): three window edges moved out by one 30 fps frame (K30), so no sharp frame stepping 19 –
  // 22 px sits next to a blurred one: the letter's first (k189, 6.300 s: the 21° edge is already 13 px past the frame's
  // right side for that frame's whole shutter, 6.292 → 6.308, in both formats), the letter's last on its way in (k279,
  // 9.300 s) and theo's brief's last (k965, 32.167 s). Those two now take the first frame of S-2's / S-8's rise: the
  // caption moves 0.4 px and fades 7 % within the 1/60 s shutter, so the sub-frames leave it as it was. And a 16:9 window
  // for move 2's fastest frames (k447 – 452, 14.900 – 15.067 s, 19.1 – 19.9 px a frame at 30 fps), clear of S-3's fade
  // out (15.238 s) and of any ink.
  const RISE = 0.24, WIPE_END = START + 0.6, K30 = 1 / 30;
  [[WIPE_END - K30, AT.letterOut + S.letterOut.dur], [AT.letterIn, Math.min(AT.letterIn + S.letterIn.dur, T(13) + K30)],
    [Math.max(AT.envOut, T(13) + RISE), AT.envOut + S.envOut.dur], [AT.trip1[0] + RISE, AT.trip1[1]],
    [AT.trip2[0], AT.trip2[1]].map((v, i) => (i ? v : Math.max(v, T(40) + RISE))),
    [AT.briefOut, Math.min(AT.briefOut + S.briefOut.dur, T(45) + K30)], [AT.answerOut, AT.answerOut + S.answerOut.dur],
    [AT.ansIn, AT.ansIn + ANS_IN_DUR], [AT.ansEnvOut, AT.ansEnvOut + S.answerEnvOut.dur],
    ...(P ? [] : [[14.85, 15.1]])].forEach(([a, b]) => V.blur30(a, b));

  // ================================================================ the camera (§5.2.3)
  // CAM-M · move 1 (truck / tilt to CAM-R0) · move 2 (push to CAM-R+) · move 3 (settle) · the wait (CAM-R0 held, dirfix:
  // was a 16:9 drift / 4:5 tilt) · move 4 (to CAM-T) · move 5 (the return, dip to s 0.7) · CAM-M: V2.cam.scene(), the one
  // source for this file and the sky QA (qa/v2-paper-sky/zones.mjs). Move 5's dip runs on the move's own eased progress
  // (V2.cam.ret: s = 1 − 0.3 sin(π e), e = FILM.CAM(τ), rev-sec2 r1 / dir-v2), so the camera leaves CAM-T and lands on
  // CAM-M at rest; same states, same path, same blur window (37.500 → 39.286).
  const camera = cam.scene();

  // ================================================================ the caption slot (§4.3, §6), its own layer
  const S1 = V.COPY.S1.map((ln, i) => (i === 0
    ? [{ text: 'With ' }, { text: 'Dorylinae', colour: V.colourShift(C.text, C.brand, START + 0.6) }, { text: ln.slice('With Dorylinae'.length) }]
    : ln));
  const CAPTIONS = [
    ...(OWN_S1 ? [{ at: T(7.5), lines: S1 }] : []),
    { at: T(13), lines: V.COPY.S2 },
    { at: T(16.75), lines: V.COPY.S3 },
    { at: T(21.5), lines: V.COPY.S4, build: V.COPY.S4b.map((s, i) => ({ text: s, at: T(22 + i) })) },
    { at: T(28), lines: V.COPY.S5 },
    { at: T(32), lines: V.COPY.S6 },
    { at: T(40), lines: V.COPY.S7 },
    { at: T(45), lines: V.COPY.S8 },
    { at: T(49), lines: V.COPY.S9 },
  ];
  F.shot({
    id: 'II-cap', start: CAPTIONS[0].at, end: END, z: 5,
    build(root) {
      const slot = V.captionSlot(root, { items: CAPTIONS });
      return (tl, t) => slot.render(t);
    },
  });

  // ================================================================ the scene
  F.shot({
    id: 'II', start: START, end: END, enter: 'cut21',
    build(root) {
      const L = V.layers(root, { place: 'scene' });
      const W = L.world;
      const parts = [];
      const add = (p) => { parts.push(p); return p; };

      // ---- the ground: trails lie on it, behind the relay, the plates and the chips
      const tsvg = L.svg(W);
      W.prepend(tsvg);
      // ---- the relay (§4.8): rack, sign, counter (V.GEO.relay: in 16:9 the pigeonhole box is held 10 px inside the frame
      // edges at CAM-M and CAM-T, eight shallow rows, so at CAM-R0 two full rows stand above the envelope); its clock runs
      // only in the wait
      // fix-r3 (review r2, frame): the clock takes the cover guard too (clockGuard, below; it acts in 4:5, where the brief's
      // envelope slides down over the readout window at the start of move 4 while `NOW 13:41:45 UTC` was fully lit: its
      // descending edge cut the letters, then a `TC` stub stood beside it). It leaves over the 120 ms before the paper
      // reaches the window (from ≈ 28.97 s, so `:45` reads whole for ≈ 0.4 s after G40) and its own G41 fade follows
      const relay = add(V.relay(W, { clock: { enterAt: AT.clockIn, ticks: TICKS, outAt: AT.clockOut, fade: (t) => clockGuard(t) } }));
      // ---- the machines (§4.6, §7): the real terminal lines, a 4-line window that scrolls; focus by dimming, never blur.
      // maya's text is texture (40 %) once the camera leaves her (G17.5) and full again when the answer arrives (G55);
      // theo's is 40 % until the envelope reaches his machine (G43) and again from the return (G52.5), when the camera
      // leaves him (in 4:5 his dark block then slides down behind the caption, its text faded out by the caption guard).
      // dir-v2: theo's key line (`public key:  ZhbF0Hsy…`) is not dimmed in its own right until the result lands (G45.75,
      // rows.identity keyDimAt; the result's lines scroll it out on that frame), and it comes up to full text-3 a beat
      // and a quarter before the rest of his terminal (KEY_UP), so it reads beside the envelope's `TO ZhbF0Hsy` for about
      // a second: as the envelope arrives (its form still, the key line settling) and while it goes into his machine (it
      // leaves the form's TO row in the slot 0.34 – 0.42 s after G43). maya's key line is dim from the start (§7)
      const mayaM = add(V.machine(W, { who: 'maya', dim: (t) => seg(t, T(17.5), 0.24) * (1 - seg(t, AT.home, 0.24)),
        rows: [rows.identity('maya', 'QZ_vPMFN', -1, AT.command), rows.cmd('maya', V.TERM.request, AT.command), rows.out(V.TERM.queued, AT.queued)] }));
      const theoM = add(V.machine(W, { who: 'theo', dim: (t) => 1 - seg(t, AT.envIn, 0.24) + seg(t, AT.trip3[0], 0.24),
        rows: [rows.identity('theo', 'ZhbF0Hsy', -1, AT.inbox, AT.result), rows.inbox('theo', AT.inbox, { dimAt: INBOX_DIM }), rows.cmd('theo', V.TERM.result, AT.result)] }));
      {
        // the key line's own level: the machine's focus dim, lifted from KEY_UP (240 ms, brand ease) instead of G43; its
        // own dim from AT.result (as the machine draws it). Drawn after the machine, on the row the machine placed
        const keyRow = theoM.rows.find(({ row }) => row.parts[0].text.startsWith('public key:'));
        const dimK = (t) => Math.min(1 - seg(t, KEY_UP, 0.24), 1 - seg(t, AT.envIn, 0.24)) + seg(t, AT.trip3[0], 0.24);
        add({ render(t) { if (keyRow.r.style.display !== 'none') F.css(keyRow.r, { opacity: F.op(Math.min(1 - 0.6 * F.clamp((t - AT.result) / 0.12), 1 - 0.6 * F.clamp(dimK(t)))) }); } });
      }
      /**
       * final polish (director, item 7), 16:9: the word of theo's first line that the CAM-R0 frame edge cuts (`identity`,
       * world x 1770 → 1965 against the edge at 1920) is held back, so his terminal is cropped at a word space at R0
       * (`theo$ agentnet`). It is drawn as its own span of the row (same colour and focus dim as the machine gives the
       * row) and fades in (240 ms, brand ease) from the first frame the camera has carried it wholly into the frame (move 4,
       * ≈ 29.0 s, while his text is still dimmed); it is never cut by the frame before that (move 1 stops short of it).
       */
      if (!P) {
        const idRow = theoM.rows.find(({ row }) => row.parts[1]?.text === 'agentnet identity');
        const cmd = idRow.spans[1], word = 'identity';
        cmd.sp.textContent = 'agentnet ';
        const wsp = F.h('span', { style: `color:${cmd.c}`, text: word });
        idRow.r.append(wsp); idRow.spans.push({ sp: wsp, c: cmd.c });
        const wx1 = G.theo.text.x + F.measure('theo$ agentnet identity', F.FONT.mono, V.ROLE.terminal.size, 400);
        let tIn = Infinity;
        for (let f = V.frameOf(START); f <= V.frameOf(END); f++) { const c = camera(f / V.FPS); if (FMT.W / 2 + c.s * (wx1 - c.x) <= FMT.W - 2) { tIn = f / V.FPS; break; } }
        add({ render(t) { F.css(wsp, { opacity: F.op(seg(t, tIn, 0.24)) }); } });
      }

      // ---- where the two envelopes are (world top-left), a pure function of t: the paper below and the guards use them.
      // The brief's: out of maya's slot open (0.75 s) with the folded letter in its mouth, the letter drops in, the form
      // is printed, the flap closes, the seal; it lifts and travels to the counter (move 1, the 16:9 camera locked to it);
      // it waits; it travels on to theo's dock (move 4) and goes into his machine (0.70 s). dirfix: in 16:9 trip 2 has no
      // settle (V2.motion.hop settle = false): the envelope arrives lifted and goes straight up into theo's slot (the
      // settle then the slide drew an 8 px down-then-up V at frames 1837 → 1844).
      // The answer's: out of theo's slot (16:9 open, the folded answer in its mouth, which drops in before the flap
      // closes; 4:5 rising closed), its seal and form on G52 (one frame), then move 5: back along the lane to maya's dock.
      const [mx, my] = G.envAt.maya, [rx, ry] = G.envAt.relay, [tx, ty] = G.envAt.theo;
      const trip = (t, [a, b]) => seg(t, a, b - a, CAMF);
      const briefPos = (t) => {
        let y = my - S.envOut.d * (1 - sl(t, AT.envOut, 'envOut'));
        if (!P) {
          let x = lerp(mx, rx, trip(t, AT.trip1));
          x = lerp(x, tx, trip(t, AT.trip2));
          y += M.hop(t, ...AT.trip1) + M.hop(t, ...AT.trip2, undefined, false) - S.envIn.d * sl(t, AT.envIn, 'envIn');
          return [x, y];
        }
        y = lerp(y, ry, trip(t, AT.trip1));
        y = lerp(y, ty, trip(t, AT.trip2));
        return [mx, y + S.envIn.d * sl(t, AT.envIn, 'envIn')];
      };
      const ansPos = (t) => {
        const r = trip(t, AT.trip3);
        if (!P) return [lerp(tx, mx, r), ty - S.answerEnvOut.d * (1 - sl(t, AT.ansEnvOut, 'answerEnvOut')) + M.hop(t, ...AT.trip3)];
        return [tx, lerp(ty + S.answerEnvOut.d * (1 - sl(t, AT.ansEnvOut, 'answerEnvOut')), my, r)];
      };
      /**
       * dirfix (4:5): paper over a light element on the lane. In 4:5 the lane (x 540) runs through theo's chip, maya's chip
       * and the `Relay` sign, so the travelling envelopes pass over them, and slow paper edges sliced them (maya's chip
       * text cut and then tangent to the envelope's top edge, frames 783 – 786; a 7 px sliver of the sign peeking over the
       * answer envelope like a tab, ~38.3 s). coverBy(envs)(rect) → fn(t): true while one of those envelopes (out of its
       * machine) overlaps the element's world rect. The caption guard below hides an element for that span too (out over the
       * 120 ms before, back over the 240 ms after it is clear), so paper never slices it and nothing is drawn over paper.
       * The sign: move 1 (the brief comes down over it, its top edge then uncovered `Relay` letter by letter, frames
       * 812 – 816) and move 5; joined with its caption-guard runs, it is out from 12.13 s and comes back at 13.70 s, whole.
       * 16:9 moves its envelopes along the lane at chip height only past chips that are pushed off or faded (unchanged).
       */
      const ENVS = {
        brief: (t) => (t >= AT.envOut && t < AT.envIn + S.envIn.dur ? briefPos(t) : null),
        answer: (t) => (t >= AT.ansEnvOut ? ansPos(t) : null),
      };
      const coverBy = (names) => (rect) => (t) => {
        const [x0, y0, x1, y1] = rect(t);
        return names.some((n) => { const p = ENVS[n](t); return p && p[0] < x1 && p[0] + G.env.w > x0 && p[1] < y1 && p[1] + G.env.h > y0; });
      };

      // ---- the caption guard (CAP_GUARD, §4.3). guardTop(t): CAP_GUARD px above the cap top of the caption on screen at t
      // (one caption at a time: each holds from its `at` to the next one's). guard(rectAt, cover) → opacity fn(t): rectAt(t)
      // is the element's world rect [x0, y0, x1, y1]; per frame, it is hidden while its screen bottom is below guardTop
      // (within the caption's width + CAP_GUARD), or (cover: coverBy above) while paper passes over it; hidden runs closer
      // than 0.4 s are joined (no blink); each run fades out over the 120 ms before its first frame and back in over the
      // 240 ms from its first clear frame (brand ease)
      const CAPH = V.capHeight(F.FONT.display, V.ROLE.caption.size, 400);
      const capAt = (t) => { let c = CAPTIONS[0]; for (const it of CAPTIONS) if (t >= it.at) c = it; return c; };
      const guardTop = (t) => { const c = capAt(t); return (c.lines.length + (c.build ? 1 : 0) >= 2 ? G.caption.two[0] : G.caption.one[0]) - CAPH - CAP_GUARD; };
      const capX = [G.caption.cx - G.caption.maxW / 2 - CAP_GUARD, G.caption.cx + G.caption.maxW / 2 + CAP_GUARD];
      const fr0 = V.frameOf(START), fr1 = V.frameOf(END);
      const guards = {};
      // offFrame: final r1 (review r1, motion: on move 5 theo's chip faded back in at x 1313 – 1549 only to leave the frame
      // 0.4 s later, and maya's came in from the left edge only to fade out 0.37 s later). With it, a hidden run that the
      // element enters, or leaves, within 0.6 s of being off the frame switches there instead, unseen (off the frame through
      // that frame's shutter and the next one's, as paperGuard checks), with no fade: the chips (both formats).
      const guard = (name, rectAt, cover = null, offFrame = false) => {
        const runs = [];
        let a = null;
        const covered = cover ? cover(rectAt) : null;
        for (let f = fr0; f <= fr1; f++) {
          const t = f / V.FPS, c = camera(t), [x0, y0, x1, y1] = rectAt(t);
          const sx0 = FMT.W / 2 + c.s * (x0 - c.x), sx1 = FMT.W / 2 + c.s * (x1 - c.x), sy0 = FMT.H / 2 + c.s * (y0 - c.y), sy1 = FMT.H / 2 + c.s * (y1 - c.y);
          const hid = (sy1 > guardTop(t) && sy0 < FMT.H && sx1 > capX[0] && sx0 < capX[1]) || (covered != null && covered(t));
          if (hid && a == null) a = f;
          if (!hid && a != null) { runs.push([a, f]); a = null; }
        }
        if (a != null) runs.push([a, fr1 + 1]);
        const joined = [];
        for (const r of runs) { const last = joined[joined.length - 1]; if (last && (r[0] - last[1]) / V.FPS < 0.4) last[1] = r[1]; else joined.push([...r]); }
        const out = (t) => { const c = camera(t), [x0, y0, x1, y1] = rectAt(t); return FMT.W / 2 + c.s * (x1 - c.x) <= 0 || FMT.W / 2 + c.s * (x0 - c.x) >= FMT.W || FMT.H / 2 + c.s * (y1 - c.y) <= 0 || FMT.H / 2 + c.s * (y0 - c.y) >= FMT.H; };
        const unseen = (f) => [-0.5, -0.25, 0, 0.25, 0.5, 0.75, 1, 1.25, 1.5].every((d) => out((f + d) / V.FPS));
        const reach = Math.round(0.6 * V.FPS);
        // [first hidden frame, first clear frame, fade out before it, fade in after it] in seconds
        guards[name] = joined.map(([f0, f1]) => {
          let a0 = f0, b0 = f1, fa = true, fb = true;
          if (offFrame) {
            for (let k = f0; k >= Math.max(fr0, f0 - reach); k--) if (unseen(k)) { a0 = k; fa = false; break; }
            for (let k = f1; k <= Math.min(fr1, f1 + reach); k++) if (unseen(k)) { b0 = k; fb = false; break; }
          }
          return [a0 / V.FPS, b0 / V.FPS, fa, fb];
        });
        return (t) => guards[name].reduce((o, [h0, h1, fa, fb]) => o * (t < h0 ? (fa ? 1 - seg(t, h0 - 0.12, 0.12) : 1) : t < h1 ? 0 : (fb ? seg(t, h1, 0.24) : 1)), 1);
      };
      // terminal text: its row boxes (ascent / descent of the face) from the first row to the last row shown at t, as the
      // machine shows them (the last 4 rows that have landed and not ended); faded as one group in a holder in the block
      const termGuard = (name, m, who) => {
        const g = G[who], size = V.ROLE.terminal.size, pitch = V.ROLE.terminal.pitch;
        const shown = (t) => Math.min(4, m.rows.filter(({ row }) => V.inkOn(t, row.at) && !(row.until != null && V.inkOn(t, row.until))).length);
        const holder = F.h('div', { style: 'position:absolute;left:0;top:0' });
        m.block.append(holder);
        for (const it of m.rows) holder.append(it.r);
        const fn = guard(name, (t) => [g.text.x, g.text.base - size, g.block[2], g.text.base + (Math.max(1, shown(t)) - 1) * pitch + 0.3 * size]);
        return { render(t) { F.css(holder, { opacity: F.op(fn(t)) }); } };
      };
      add(termGuard('mayaText', mayaM, 'maya'));
      add(termGuard('theoText', theoM, 'theo'));
      /**
       * fix-r3 (review r2, frame): the `Relay` sign is label stock, paper, and paper never fades (§4.4): it faded through
       * 30 – 80 % over the dark wall on the 4:5 moves 1 and 5 (frames ~820 – 830 and ~2293 – 2300), a grey translucent plate.
       * paperGuard(name, rectAt, cover) → fn(t) 0 | 1: the element is hidden on every frame its screen rect enters the
       * caption block (CAP_GUARD, as guard()), and its visibility changes only on a frame where it cannot be seen anyway:
       * off the frame, or wholly under one of the `cover` envelopes (its world rect inside the envelope's), through that
       * frame's whole shutter and the next frame's (frames f − ½ … f + 1½: a blurred frame averages ± ½ frame at 30 fps,
       * whose frame shows the state of 60 fps frame 2k; at 1 frame's slack the 30 fps file showed a grey sliver). Each hidden run
       * starts on the last such frame at or before the run (search ≤ 0.6 s) and ends on the first such frame at or after it;
       * runs closer than 0.4 s are joined. Partly covered, the sign is simply behind the paper: the envelope's straight edge
       * covers and uncovers it (move 1: it switches on under the descending brief's envelope, frames ≈ 786 – 810, and rises
       * out of its top edge; move 5: it goes in behind the answer envelope's top edge and switches off under it, ≈ 2316,
       * so it never comes out below it into the caption). An edge with no such frame in reach falls back to guard()'s fade.
       */
      const paperGuard = (name, rectAt, cover) => {
        const n = fr1 - fr0 + 1, need = new Uint8Array(n), free = new Uint8Array(n);
        const unseen = (t) => {                            // off the frame or wholly under paper at t
          const c = camera(t), [x0, y0, x1, y1] = rectAt(t);
          const sx0 = FMT.W / 2 + c.s * (x0 - c.x), sx1 = FMT.W / 2 + c.s * (x1 - c.x), sy0 = FMT.H / 2 + c.s * (y0 - c.y), sy1 = FMT.H / 2 + c.s * (y1 - c.y);
          return sx1 <= 0 || sx0 >= FMT.W || sy1 <= 0 || sy0 >= FMT.H
            || cover.some((k) => { const p = ENVS[k](t); return p && p[0] <= x0 && p[0] + G.env.w >= x1 && p[1] <= y0 && p[1] + G.env.h >= y1; });
        };
        for (let f = fr0; f <= fr1; f++) {
          const t = f / V.FPS, c = camera(t), [x0, y0, x1, y1] = rectAt(t);
          const sx0 = FMT.W / 2 + c.s * (x0 - c.x), sx1 = FMT.W / 2 + c.s * (x1 - c.x), sy0 = FMT.H / 2 + c.s * (y0 - c.y), sy1 = FMT.H / 2 + c.s * (y1 - c.y);
          need[f - fr0] = sy1 > guardTop(t) && sy0 < FMT.H && sx1 > capX[0] && sx0 < capX[1] ? 1 : 0;
          free[f - fr0] = [-0.5, -0.25, 0, 0.25, 0.5, 0.75, 1, 1.25, 1.5].every((d) => unseen((f + d) / V.FPS)) ? 1 : 0;
        }
        const reach = Math.round(0.6 * V.FPS), spans = [];
        for (let i = 0; i < n; i++) {
          if (!need[i] || (i && need[i - 1])) continue;
          let j = i; while (j < n && need[j]) j++;                       // need run [i, j)
          let a = null, b = null;
          for (let k = i; k >= Math.max(0, i - reach); k--) if (free[k]) { a = k; break; }
          for (let k = j; k < Math.min(n, j + reach); k++) if (free[k]) { b = k; break; }
          if (j >= n) b = n;
          spans.push({ a: a ?? i, b: b ?? j, fadeBefore: a == null, fadeAfter: b == null });
        }
        const joined = [];
        for (const r of spans) { const last = joined[joined.length - 1]; if (last && (r.a - last.b) / V.FPS < 0.4) { last.b = r.b; last.fadeAfter = r.fadeAfter; } else joined.push({ ...r }); }
        guards[name] = joined.map(({ a, b }) => [(a + fr0) / V.FPS, (b + fr0) / V.FPS]);
        return (t) => {
          const f = V.frameOf(t) - fr0;
          let o = 1;
          for (const { a, b, fadeBefore, fadeAfter } of joined) {
            if (f >= a && f < b) return 0;
            if (fadeBefore && f < a) o *= 1 - seg(t, (a + fr0) / V.FPS - 0.12, 0.12);
            if (fadeAfter && f >= b) o *= seg(t, (b + fr0) / V.FPS, 0.24);
          }
          return o;
        };
      };
      // the `Relay` sign: a fixture, read only at CAM-R0; where the 4:5 moves carry it past the caption it is hidden as
      // paper (paperGuard). 16:9 never brings it near the caption (unchanged: always shown)
      const sg = relay.sign, signFade = P ? paperGuard('sign', () => [sg.x0, sg.y0, sg.x1, sg.y1], ['brief', 'answer']) : () => 1;
      add({ render(t) { F.css(sg.el, { opacity: F.op(signFade(t)) }); } });
      // final polish (item 9): the mail in the pigeonholes is paper too: hidden as the sign is wherever the 4:5 moves carry
      // the rack past the caption (paperGuard over the group's world rect: it switches only off the frame or wholly under
      // a travelling envelope). 16:9 never brings the rack near the caption
      const ml = relay.mail, mailFade = P && ml.rect ? paperGuard('mail', () => ml.rect, ['brief', 'answer']) : () => 1;
      add({ render(t) { F.css(ml.el, { opacity: F.op(mailFade(t)) }); } });

      // ---- the presence chips (§4.7), on the sky beside each dock on the relay side. maya pulses on every downbeat, and
      // her arrival pulse at G55; theo is a hollow ring until G40 (ring fill 120 ms, arrival pulse), then pulses on beat 3.
      // Chip text to 60 % at the push (G20.25); theo's back at G32 (S-6 is about him), maya's at G55 (the answer arrives);
      // theo's to 60 % again as the answer leaves him (G52.5: focus moves to maya).
      // 16:9: the 1.3 push would cut both chips at the frame edges, so they leave with move 2 and return with move 3 (r2).
      // Both formats: the caption guard. In 4:5 it fades theo's chip in on move 1 only once the tilt has carried it clear
      // above the caption (it rises from below the frame through the block while S-3 is read: a hollow ring all along),
      // and out on move 5 before the return carries it down into the block
      const chipFade = P ? () => 1 : (t) => (t < T(30.25) ? 1 - seg(t, AT.push[0], 0.24) : seg(t, T(30.25), 0.24));
      const chipRect = (who) => { const ch = chips[who]; return () => [ch.x, ch.y, ch.x + ch.w, ch.y + 52]; };
      const chips = {};
      chips.maya = add(V.chip(W, { who: 'maya', level: 1, pulses: V.beats(8, 55.9, 4, 0), arrivals: [AT.home], fade: (t) => chipFade(t) * chipGuard.maya(t),
        dim: (t) => seg(t, AT.push[0], 0.24) * (1 - seg(t, AT.home, 0.24)) }));
      chips.theo = add(V.chip(W, { who: 'theo', level: F.levelFill(AT.back), pulses: V.beats(41, 55.9, 4, 2), arrivals: [AT.back],
        fade: (t) => chipFade(t) * chipGuard.theo(t),
        dim: (t) => seg(t, AT.push[0], 0.24) * (1 - seg(t, AT.wait[0], 0.24)) + seg(t, AT.trip3[0], 0.24) }));
      // 4:5: both chips sit on the lane (x 540), so the envelopes pass over them (theo's on move 4 and move 5, maya's on
      // move 1 and move 5). dirfix: paper is always over chips; the chip leaves (120 ms) before the envelope reaches it
      // and comes back (240 ms) once it is clear (coverBy). Move 4: theo's ring, filled at G40 under `Theo is back.`, has
      // been seen whole for ~0.6 s when it goes (out 29.20 → 29.32 s, back 30.42 → 30.66 s) (the old z-index swap drew his pill over the form and the seal)
      // fix-r3 (review r2, frame): 16:9 the same (it was 4:5 only): the travelling envelope swept across each chip on every
      // trip, eating and revealing its text letter by letter (`theo onl`, `ya (you)`; trip 1 maya's ≈ 740 – 767, trip 2
      // theo's ≈ 1752 – 1830, trip 3 both). Same 120 ms out / 240 ms in and the 0.4 s join, in both formats now
      const cover = coverBy(['brief', 'answer']);
      const chipGuard = { maya: guard('mayaChip', chipRect('maya'), cover, true), theo: guard('theoChip', chipRect('theo'), cover, true) };
      const clockGuard = guard('clock', () => G.relay.window, cover);
      root.__sec2 = { camera, guards, AT };                       // for the QA tools (qa/sec2-fix-r1)

      // ---- paper: one envelope at a time, a letter or a sheet at a dock; it rides in front of the plates and the chips
      // and is clipped at each machine's slot (it comes out of and goes into the slot's groove, never fades)
      const paperLayer = F.h('div', { style: 'position:absolute;left:0;top:0' });
      W.append(paperLayer);
      const paper = (p) => { paperLayer.append(p.el); return add(p); };

      // maya's letter (§4.5): out of her slot unfolded (0.90 s), the agent writes its 8 lines (⅛ beat apart), it folds in
      // half (bottom half up, toward the slot) and goes back in (0.45 s): her machine takes it
      const [lx, ly] = G.letterAt.maya;
      paper(V.paper({ kind: 'brief', x: lx, foldDir: 'up', clip: [G.maya.slot],
        y: (t) => ly - S.letterOut.d * (1 - sl(t, AT.letterOut, 'letterOut')) - S.letterIn.d * sl(t, AT.letterIn, 'letterIn'),
        fold: (t) => M.fold(t, AT.fold), lineAt: AT.lines }));

      // theo's copy (§5.2.4 G44): out of his machine unfolded, with the crease of its fold at maya's
      const [bx, by] = G.letterAt.theo;
      const theoBrief = paper(V.paper({ kind: 'brief', x: bx, crease: true, clip: [G.theo.slot],
        y: (t) => by - dir * S.briefOut.d * (1 - sl(t, AT.briefOut, 'briefOut')) - (BRIEF_BACK_IN ? dir * S.briefIn.d * sl(t, AT.briefIn, 'briefIn') : 0) }));
      // its crease, readable at viewing size (rev-sec2 r1: the 2 px notches and the #FCFBFE half alone did not read): a
      // short 1 px crease-line tick (C2.crease) in from each side edge at the fold, continuing each notch, in the clear gap
      // between `What` and `Why`. Not a ruled line across the sheet (gate r2); drawn on the sheet face, so the slot clips it
      {
        const B = V.SHEETS.brief, len = P ? 30 : 34, face = theoBrief.el.firstChild;
        face.append(V.hline(0, B.crease - 0.5, len, 1, V.C2.crease), V.hline(B.w - len, B.crease - 0.5, len, 1, V.C2.crease));
      }

      // the brief's envelope over its whole life (positions: briefPos, above)
      const brief = paper(V.sealed({ kind: 'scene', sheet: 'brief', address: V.ADDR.brief, x: (t) => briefPos(t)[0], y: (t) => briefPos(t)[1],
        flap: (t) => M.flapClose(t, ...V.FLAP.brief), formAt: AT.form, sealAt: AT.seal, dropAt: AT.drop, clip: [G.maya.slot, G.theo.slot] }));
      // the pointers on its form at CAM-R+ (§4.9): FROM → TO → TIME → SIZE on G22 … G25 (who, to whom, when, how big), SIZE
      // held to G27.75; then under the seal tab G28.5 → G31.5. Each leaves (120 ms) before the next appears
      const ps = cam.PUSH.s;
      ['from', 'to', 'time', 'size'].forEach((f, i) => brief.env.pointer(f, { at: T(22 + i), outAt: (i < 3 ? T(23 + i) : T(27.75)) - 0.12, s: ps }));
      brief.env.pointer('seal', { at: T(28.5), outAt: T(31.5) - 0.12, s: ps });

      // theo's answer sheet (§4.5, capture/21): out of his machine (0.60 s), the pointer under the finding, it folds (toward
      // his slot: up in 16:9, down in 4:5) and goes back in (ANS_IN_DUR)
      const [ax, ay] = G.answerAt.theo;
      const answer = paper(V.paper({ kind: 'answer', x: ax, foldDir: P ? 'down' : 'up', clip: [G.theo.slot], edge: 'strong',
        y: (t) => ay - dir * S.answerOut.d * (1 - sl(t, AT.answerOut, 'answerOut')) - dir * S.answerIn.d * seg(t, AT.ansIn, ANS_IN_DUR, CAMF),
        fold: (t) => M.fold(t, AT.ansFold) }));
      // gate r1: only row 2, the finding itself (`one ledger row`), is underlined, at its own width (a row-1 run between the
      // rows crowds row 2's ascenders); the pointer leaves 120 ms before the fold
      answer.pointer(1, { at: AT.ptr, outAt: AT.ansFold - 0.12 });

      // the answer's envelope: out of theo's slot (16:9 open, the folded answer in its mouth, which drops in before the flap
      // closes; 4:5 rising closed), its seal and form on G52 (one frame), then move 5: back along the lane to maya's dock
      paper(V.sealed({ kind: 'scene', sheet: 'answer', address: V.ADDR.answer, x: (t) => ansPos(t)[0], y: (t) => ansPos(t)[1], clip: [G.theo.slot],
        flap: (t) => (P ? 0 : M.flapClose(t, ...ANS_FLAP)), formAt: AT.ansSeal, sealAt: AT.ansSeal, dropAt: P ? null : ANS_DROP / V.FPS }));

      // ---- the pheromone trail (§4.9), one element per frame: under each trip, its head hidden under the envelope, marching
      // one pitch per beat, the tail fading linearly. Trip 1 G16.75 → G20 (tail to G22), trip 2 G40.25 → G43 (tail to G45),
      // the return G52.5 → G55 (tail to the cut). V2.trail's own settings, plus `band`: in 4:5 the lane (x 540) runs through
      // the centred caption, and on the return the dip carries theo's dock (the trail's tail end) down into the caption
      // block. So the return's tail is also erased up to the point 40 px above the two-line caption's cap tops (§4.3: nothing
      // of the stage enters the text block), with the trail's own soft 2-pitch tail fade: it reads as its tail, not a clip
      const BAND_Y = 1104 - 40;
      const trail = (o) => {
        const [t0, t1] = o.travel, pitch = 12 * F.Z;
        const len = Math.hypot(o.to[0] - o.from[0], o.to[1] - o.from[1]);
        const band = o.band ? (t) => {
          const c = camera(t), wy = c.y + (BAND_Y - FMT.H / 2) / c.s;            // world y on the band's top edge
          return F.clamp((o.from[1] - wy) / len);                                // the path runs up the screen from `from`
        } : () => 0;
        const tr = F.ui.trail(tsvg, `M${o.from[0]} ${o.from[1]} L${o.to[0]} ${o.to[1]}`, {
          draw: (t) => seg(t, t0, t1 - t0, CAMF),
          march: (t) => (pitch * (F.clamp(t, t0, t1) - t0)) / V.BEAT,
          tailFade: (t) => Math.max(F.clamp((t - o.tail[0]) / (o.tail[1] - o.tail[0])), band(t)),
          fade: (t) => (t < t0 || t >= o.tail[1] ? 0 : 1),
          colour: C.accent,
        });
        // fix-r3 (review r2, viewer): the trail never tucks under a chip. In 4:5 the lane runs through both chips, and at
        // the end of the return the last dots sat between the answer envelope and maya's chip, the lower one half under
        // the pill's top edge (39.30 – 39.85 s). A dot is drawn only clear of each chip's box: its opacity scales from 0
        // with its centre one dot radius (r) off the box to 1 a pitch further out (static dots near a chip are gone or
        // whole; a marching dot fades as it nears one). 16:9 lanes pass 136 px under the chips: no effect
        const r = 1.5 * F.Z, boxes = ['maya', 'theo'].map((w) => chipRect(w)());
        return { render(t) {
          tr.render(t);
          for (const c of tr.el.children) {
            if (c.getAttribute('visibility') !== 'visible') continue;
            const x = +c.getAttribute('cx'), y = +c.getAttribute('cy');
            let k = 1;
            for (const [x0, y0, x1, y1] of boxes) k = Math.min(k, F.clamp((Math.hypot(Math.max(x0 - x, 0, x - x1), Math.max(y0 - y, 0, y - y1)) - 2 * r) / pitch));
            if (k < 1) c.setAttribute('opacity', F.op(+c.getAttribute('opacity') * k));
          }
        } };
      };
      const centre = (pos) => [pos[0] + G.env.w / 2, pos[1] + G.env.h / 2];
      const relayC = centre(G.envAt.relay);
      add(trail({ from: G.maya.dock, to: relayC, travel: AT.trip1, tail: [AT.trip1[1], T(22)] }));
      add(trail({ from: relayC, to: G.theo.dock, travel: AT.trip2, tail: [AT.trip2[1], T(45)] }));
      add(trail({ from: G.theo.dock, to: G.maya.dock, travel: AT.trip3, tail: [AT.trip3[1], END], band: P }));
      return (tl, t) => {
        L.apply(camera(t));
        for (const p of parts) p.render(t);
      };
    },
  });
})();
