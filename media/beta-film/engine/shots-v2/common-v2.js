// v2 "The envelope": shared components (design/SCRIPT-v2.md §4 and §12.2). Everything hangs off FILM.V2.
// Load after lib/* and shots/common.js (v1 shared styles, FILM.S); before the v2 section files.
//
// Every component is built once and drawn by render(t) as a pure function of t (absolute film seconds unless noted).
// Options that move or change can be given as constants or as functions of t (the kit's own style: ui.trail, ui.dot).
// Coordinates: world px of the camera state CAM-R0 (SCRIPT-v2 §0), or the component's local px where stated.
(() => {
  // ================================================================ owner decisions (one-line changes)
  // The owner's final answers (design/BRIEF-v2.md, "Owner decisions after the v2 script (final)"): the signature 'Yazan' and
  // no footnote on the card. These are the defaults (the gallery's, and any caller that passes none); the film's switches
  // are the section files' own constants (sec4-invite.js SIGNATURE, sec3-card.js SHOW_REVIEW_FOOTNOTE), passed explicitly.
  const SIGNATURE = 'Yazan';                       // O-2 (§5.4): the note's signature (the line fits up to 'Yazan, Dorylinae')
  const SHOW_REVIEW_FOOTNOTE = false;              // O-1 (§5.3): the card's footnote `No independent security review yet.`
  const SKY_READY = true;                          // true once assets/sky-v2scene-169.png / -45.png exist (out/v2-skies.log)
  const PAPER_TOOTH = false;                       // §4.1 optional fibre noise (≤ 2 %): invisible at full size (gate r1, E3); keep flat
  // The note as Dorylinae stationery (director, dir-v2): the compact mark over a hairline rule, pre-printed (present from the
  // first frame, under the fold), the six lines and the signature set lower (SHEETS.note). false = §5.4's plain layout.
  const NOTE_LETTERHEAD = true;

  const F = window.FILM;
  const { C, FMT, FONT, EASE, LINEAR, clamp, lerp, seg, h, s: svgEl, css, op, ui } = F;
  const P = FMT.portrait;
  const pick = (a, b) => (P ? b : a);                // pick(16:9 value, 4:5 value)
  const val = (v, t) => (typeof v === 'function' ? v(t) : v);
  const CAM = F.CAM;                                 // camera / paper-slide curve (0.4, 0, 0.6, 1)

  // ================================================================ clock: 84 BPM (§0, §2)
  const BEAT = 5 / 7, BAR = 20 / 7, FPS = 60;
  /** seconds of global beat G (G0 = film frame 0) */
  const T = (G) => G * BEAT;
  const Gof = (t) => t / BEAT;
  /** the film frame a time falls on (render.mjs blur sub-frames of one frame all round to it) */
  const frameOf = (t) => Math.round(t * FPS);
  /** a discrete event snapped to its whole frame (§0: cuts, ink, clock ticks) */
  const snap = (t) => frameOf(t) / FPS;
  /** ink is present from the frame of `at` on (no fade). at == null: always present. */
  const inkOn = (t, at) => at == null || frameOf(t) >= frameOf(at);
  const barBeat = (t) => { const g = Gof(t); return `${Math.floor(g / 4) + 1}:${+((g % 4) + 1).toFixed(3)}`; };
  /** times of every beat G0 ≤ G ≤ G1 with G ≡ phase (mod step): downbeats = beats(G0, G1, 4, 0), beat 3 = (…, 4, 2) */
  const beats = (G0, G1, step = 1, phase = 0) => { const out = []; for (let g = Math.ceil(G0); g <= G1 + 1e-9; g++) if ((((g - phase) % step) + step) % step === 0) out.push(T(g)); return out; };

  // ================================================================ tokens (§4.1)
  const C2 = {
    ...C,
    stock: '#F6F3FB',        // label stock (light bg): flaps, seal tabs, forms, the sign, the card
    paper: '#FFFFFF',        // paper white (light surface): letters, the answer sheet
    paper2: '#ECE6F7',       // light surface-2: envelope bodies, the inside face of an open flap
    ink: '#17122B',          // light text
    inkMeta: '#665E88',      // light text-3: letter headers, form captions, the card footnote
    brandInk: '#6B3FB8',     // light brand: field names What: Why: Done when:
    accentInk: '#A021AC',    // light accent: pointers on paper
    hairline: '#877CAE',     // light line-strong: the flap's edge
    crease: '#D5CBE8',       // light line: seams, letter rules, creases
    // r3 (gate r2): a sheet is one material, white on both sides. A half that turns is told apart by its line-strong free
    // edge and one flat fill per frame from its angle (paperTone: white flat, `turn` edge-on); folded flat it is white again
    back: '#FFFFFF',         // the back of every printed sheet: paper white
    turn: '#ECE6F7',         // the tone a turning half reaches edge-on (light surface-2), one flat fill per frame
    halfTone: '#FCFBFE',     // the half that was turned, once unfolded: a barely different plane (flat)
    flapIn: '#F6F3FB',       // the inside face of an open flap (label stock), distinct from the body behind it
    ink55: 'rgba(23,18,43,0.55)',   // inset hairline of a specimen label (ink at 55 %)
  };
  const hexRgb = (x) => { const n = parseInt(x.slice(1), 16); return [(n >> 16) & 255, (n >> 8) & 255, n & 255]; };
  /** mix two #rrggbb colours (p = 0 → a) */
  function mix(a, b, p) {
    if (p <= 0) return a; if (p >= 1) return b;
    const A = hexRgb(a), B = hexRgb(b);
    return `rgb(${A.map((v, i) => Math.round(lerp(v, B[i], p))).join(',')})`;
  }
  /** colour function: from → to over dur (brand ease) at `at` */
  const colourShift = (from, to, at, dur = 0.12) => (t) => mix(from, to, seg(t, at, dur));

  // ================================================================ type roles (§4.2), screen px at s = 1
  const ROLE = {
    caption: { face: 'display', size: pick(64, 48), weight: 400, tracking: -0.01, colour: C.text },
    terminal: { face: 'mono', size: pick(36, 32), weight: 400, pitch: 46 },
    chip: { face: 'mono', size: pick(36, 32), weight: 500, colour: C.text },
    letter: { face: 'mono', size: pick(36, 32), weight: 400, colour: C2.ink },
    // fix-r3 (review r2, frame: text that carries meaning under the floors, 36 px in 16:9, 32 in 4:5): the sheet headers
    // 28 / 26 (were 26 / 24), the form's field captions 22 / 24 (were 20: they map `who, to whom, when, how big`) and its
    // values 36 in 16:9 (were 34; the 16:9 form is 20 px wider for them, ENV.scene)
    letterHead: { face: 'mono', size: pick(28, 26), weight: 400, colour: C2.inkMeta },
    formCap: { face: 'mono', size: pick(22, 24), weight: 500, tracking: 0.08, upper: true, colour: C2.inkMeta },
    formVal: { face: 'mono', size: pick(36, 32), weight: 500, colour: C2.ink },
    sign: { face: 'display', size: pick(40, 36), weight: 400, colour: C2.ink },
    // dirfix: 36 / 32 (was 32 / 28, SCRIPT-v2 §4.2): the brief's floor for any line that matters (≥ 36 px in 16:9, ≥ 32
    // in 4:5); the clock is the only picture of the wait and part of the poster
    clock: { face: 'mono', size: pick(36, 32), weight: 500, tracking: 0.08, upper: true },
    // dirfix: the 4:5 card is laid out for portrait (V2.card): display 72 (was 64), lines 52 (was 40), each wrapped onto two
    cardDisplay: { face: 'display', size: pick(88, 72), weight: 400, tracking: -0.01, colour: C2.ink },
    cardLine: { face: 'sans', size: pick(64, 52), weight: 500, colour: C2.ink },
    cardFoot: { face: 'sans', size: pick(40, 32), weight: 500, colour: C2.inkMeta },
    ourFormCap: { face: 'mono', size: pick(26, 24), weight: 500, tracking: 0.08, upper: true, colour: C2.inkMeta },
    ourFormVal: { face: 'mono', size: pick(52, 48), weight: 500, colour: C2.ink },
    note: { face: 'sans', size: pick(44, 40), weight: 500, colour: C2.ink },
    signature: { face: 'display', size: pick(56, 48), weight: 400, colour: C2.ink },
    address: { face: 'display', size: 64, weight: 400, tracking: -0.01, colour: C.text },
  };
  const FAM = { display: FONT.display, sans: FONT.sans, mono: FONT.mono };
  const RADIUS = pick(16, 24), BRADIUS = pick(8, 12);         // plate / terminal block radius
  const HAIR = pick(1.6, 2);                                   // paper hairline weight

  // ================================================================ copy (§6) and real terminal lines (§7), verbatim
  const COPY = {
    S1: pick(['With Dorylinae, your agent hands it to theirs.'], ['With Dorylinae, your agent', 'hands it to theirs.']),
    S2: ['Your machine seals it.'],
    S3: ['The relay works like a post office.'],
    S4: ['It reads the outside:'],
    S4b: ['who,', ' to whom,', ' when,', ' how big.'],         // the building line, one span per beat
    S5: ['It cannot open the envelope.'],
    S6: ['Theo is offline. The envelope waits.'],
    S7: ['Theo is back. His machine opens it.'],
    S8: ['Theo’s agent does the work.'],
    S9: pick(['The answer comes back, sealed the same way.'], ['The answer comes back,', 'sealed the same way.']),
    K1: 'End-to-end encrypted.',
    K2: 'Requests wait while your teammate is offline.',
    K3: 'You approve what their agent can read.',
    K4: 'No independent security review yet.',
    NOTE: ['We’re inviting a first wave', 'of 10 small teams, like yours.', 'Tell us what breaks.',
      'That decides what we change first.', 'Free during the closed beta.', 'Apply at dorylinae.net'],
    ADDRESS: 'Apply at dorylinae.net',
  };
  const ind = pick('    ', '  ');                              // continuation indent (§7: 4:5 uses 2 spaces)
  const TERM = {
    identity: (who, key) => [[`${who}$`, ' agentnet identity'], `public key:  ${key}…`],
    request: ['agentnet request theo task \\', `${ind}--title 'Track down the flaky payments test' \\`,
      `${ind}--brief-from-file flaky-brief.md \\`, `${ind}--idempotency-key demo-flaky-1`],
    queued: 'Queued normal task request r-97ae… to theo (team atlas)',
    // the command (text-2), then its output (text-3): the header and the one row (§4.2 terminal hierarchy)
    inbox: { cmd: 'agentnet inbox', out: ['ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE',
      'r-97ae…  maya  task  normal   2000  0m   Track down the flaky payments test'] },
    result: pick(['agentnet result s-b309… --status pass \\', `${ind}--summary 'Cause found: two tests share one ledger row' \\`,
      `${ind}--file flaky-finding.md`],
    ['agentnet result s-b309… --status pass \\', `${ind}--summary \\`, `${ind}'Cause found: two tests share one ledger row' \\`,
      `${ind}--file flaky-finding.md`]),
  };
  /** what the forms say (capture/25-relay-log.txt) */
  const ADDR = {
    brief: { from: 'QZ_vPMFN', to: 'ZhbF0Hsy', time: '13:41:40 UTC', size: '1,444 bytes' },
    answer: { from: 'ZhbF0Hsy', to: 'QZ_vPMFN', time: '13:43:54 UTC', size: '1,328 bytes' },
    ours: { from: 'Dorylinae', to: 'your team' },
  };

  // ================================================================ stage geometry (§4.3 – §4.8, §5.2), world px
  // r2 (gallery gate): the slot is a 6 px groove in a 24 px rim (paper comes out of the groove: slot.y is the groove's centre
  // line, the clip edge); 4:5 maya's whole group (plate, letter, dock, chip) sits 102 px higher in the world and CAM-M with
  // it, so the rack's top edge and the `Relay` sign stay below the frame at CAM-M (§4.8) with her chip where it was on screen.
  const MY = pick(0, -102);
  // r3 (gate r2): the 4:5 scene envelope is 700 × 400 (was 372): its address form needs a clear band for the 9 px pointer
  // under each row (§4.4 form, below). It still rests on the counter (top 378) and docks by its top fold as before.
  // dir-v2 (sec2, K6): the 4:5 answer sheet is 400 tall (was 276; its text block re-centred, SHEETS.answer), so at CAM-T it
  // reaches 174 px under theo's chip instead of ~300 (the frame was bottom-heavy). 400 keeps its 0.60 s slide out of the
  // slot under 19 px a frame (394 px: 18.2); held in the slot as before (answerAt). 16:9 unchanged.
  // final polish (director, item 6): the 16:9 answer sheet is 400 tall too (was 300). At CAM-T (32.1 – 37.1 s, K6) ~350 px of
  // empty sky stood between its bottom (y 569) and the caption: the frame was top-heavy. Its header, rule and summary stay
  // at their document positions (the extra height is bottom margin) and it takes a letter fold (SHEETS.answer)
  const EH = pick(392, 400), ANS_H = 400;
  // r3: the rack is a frame of shelves (§4.8 construction, gate r2): a surface-2 frame lip around one bg recess, square
  // shelves (surface-2) under each row, thinner dividers (surface) between the columns; the counter has a top lip (line)
  // that the envelope stands on and an inset bg readout window for the clock, like the terminal blocks
  const GEO = pick({
    caption: { cx: 960, one: [970], two: [894, 970], rise: 12, maxW: 1500 },
    // final polish (director, item 7): at CAM-R0 (the poster) both machines read as live, their crops on word spaces.
    // maya's text starts at world x −918 (screen 362 at CAM-M, centred over her letter and the envelope; was −1190, screen
    // 90, inside the 96 px title-safe, and her whole terminal was off the R0 frame: a blank slab). Her four lines at R0
    // (`--title …`, `--brief-from-file …`, `--idempotency-key …`, `Queued …`) cross the R0 frame edge (world x 0) at
    // character 42.5, which is a space in both lines long enough to reach it: `test' \` and `(team atlas)` show, dimmed (her
    // texture 40 %), cut in a word space; the other two end before the edge. (No other column ≥ 96 px at CAM-M crops every
    // line in a space: from screen 96 to 362 the `queued` line always leaves a stub of `atlas)` at the edge.)
    // theo's text 4 px right (1468, screen 188 at CAM-T; the 75-character inbox row ends at 1808): his key line's `…` lies
    // wholly past the R0 frame edge (a sliver of its first dot showed at x 1919), so it reads `public key:  ZhbF0Hsy`, and
    // the cut word `identity` of his first line is held back until the camera has carried it whole into the frame on
    // move 4 (sec2-scene.js, item 7): at R0 it reads `theo$ agentnet`
    maya: { plate: [-1300, -40, 500, 287], block: [-1288, -28, 488, 263], text: { x: -918, base: 100 }, slot: { y: 275, keep: 'below', groove: [-736, 96] }, dock: [-320, 583] },
    theo: { plate: [1420, -40, 3220, 287], block: [1432, -28, 3208, 263], text: { x: 1468, base: 100 }, slot: { y: 275, keep: 'below', groove: [1824, 2656] }, dock: [2240, 583] },
    // r3: the chip's dot-to-name gap is 18 (was 12), so theo's fixed-width chip is 6 px wider, keeping its right edge
    chips: { maya: { x: 96, y: 395 }, theo: { x: 1487, y: 395, w: 337 } },
    // dir-v2: the sorting rack. The pigeonhole box is held 10 px inside the frame edges at CAM-M and CAM-T (world x 640 /
    // 1280: those frames show only the wall's stile and the counter's end; was 544 → 1376, a cut-off grid at the frame
    // edges), five 115 px columns; eight rows of shallow cells (115 × 55, 10 px shelves) so at CAM-R0 two full rows and
    // their shelves stand above the resting envelope, whose top edge (387; 379 at the hop) falls 21 px under the second
    // shelf, inside row 3 (a sorting rack; four rows of near-square cells read as a dark window grid)
    relay: { wall: [520, 96, 1400, 779], signTop: 108,
      holes: { cols: 5, rows: 8, box: [650, 226, 1270, 755], frame: 10, shelf: 10, divider: 6 },
      counter: [480, 779, 1440, 859], lip: { h: 8, over: 8 }, rest: [580, 387],
      // dirfix: the clock at 36 px (392 px wide, was 348): the window widened leftward and 4 px taller, the caps centred in
      // it (y 823), 11 px from the counter's lip and bottom edge, ~20 px of air each side of the ink
      clock: { x: 1400, base: 836 }, window: [990, 798, 1416, 848] },
    // final polish (director, item 9): other people's sealed mail in four of the cells above the resting envelope (rows 1
    // and 2; row 3 is cut by the envelope's top edge and stays empty). See relay(): MAIL
    mail: [{ cell: [0, 1], items: [[12, 88, 6, 0], [17, 80, 6, 0]] }, { cell: [0, 3], items: [[34, 68, 28, 1]] },
      { cell: [1, 0], items: [[11, 92, 7, 0]] }, { cell: [1, 4], items: [[15, 84, 6, 0], [9, 94, 5, 0], [19, 76, 6, 0]] }],
    env: { w: 760, h: EH }, lane: 583,
    // where paper sits (top-left) when it is out of a machine / at a dock. r2: a sheet out of a machine stays held in its
    // slot, its edge 6 px inside the groove (as the open flap's edge is), so nothing behind it ever shows between the
    // groove and the sheet (the brief going back in behind the answer, gate r1 / r2)
    letterAt: { maya: [-682, 269], theo: [1878, 269] }, answerAt: { theo: [1878, 269] },
    envAt: { maya: [-700, 387], theo: [1860, 387], relay: [580, 387] },
  }, {
    caption: { cx: 540, one: [1196], two: [1138, 1196], rise: 18, maxW: 952 },
    // docks are the envelope's centre: its top fold (envAt) + EH / 2
    maya: { plate: [-60, -922 + MY, 1140, -672 + MY], block: [-48, -910 + MY, 1128, -696 + MY], text: { x: 64, base: -863 + MY }, slot: { y: -684 + MY, keep: 'below', groove: [154, 926] }, dock: [540, -578 + MY + EH / 2] },
    theo: { plate: [-60, 1486, 1140, 1736], block: [-48, 1510, 1128, 1724], text: { x: 64, base: 1546 }, slot: { y: 1498, keep: 'above', groove: [154, 926] }, dock: [540, 962 + EH / 2] },
    // r2: 4:5 chips centred on the lane (x 540), so the 1.2 push keeps theo's between the counter and the caption, whole.
    // r3: 6 px wider for the 18 px dot gap, still centred
    chips: { maya: { x: 402, y: -120 + MY }, theo: { x: 386, y: 878, w: 309 } },
    // r3: 4:5 rack 4 columns × 5 rows (cells 177 × 84, the 16:9 cells' 2.1 : 1): with the 400 px envelope on the counter
    // (top 378) the first shelf ends 21 px above it, so the envelope's top edge never runs along a shelf.
    // fix-r3 (review r2, frame): the pigeonhole box sits 24 px inside the resting envelope's sides (x 190 – 890), as the
    // 16:9 box sits inside its envelope: beside the envelope only plain wall shows (it was 26 px wider than the envelope
    // each side, a column of dark recesses cut by 16 px shelf stubs); cells 152 × 84. The counter is 70 – 1010 (was
    // 110 – 970): at CAM-R+ (s 1.2) its lip runs off both frame edges instead of ending 14 px short of them; at CAM-R0 the
    // lip leaves 62 px of air each side
    // final polish (director, item 8): the sign at 135 (15 px of wall above it, 15 px to the pigeonhole box below; was
    // 12 / 18), and the clock's readout window ends on the wall's right edge (x 940; it stood 4 px past it), the clock
    // right-aligned 14 px inside it as before (x 926)
    relay: { wall: [140, 120, 940, 778], signTop: 135,
      holes: { cols: 4, rows: 5, box: [214, 245, 866, 754], frame: 12, shelf: 16, divider: 7 },
      counter: [70, 778, 1010, 854], lip: { h: 8, over: 8 }, rest: [190, 778 - EH],
      clock: { x: 926, base: 832 }, window: [561, 797, 940, 843] },
    // final polish (item 9): three of the four cells of row 1 (the one row whole above the resting envelope)
    mail: [{ cell: [0, 0], items: [[16, 114, 8, 0], [23, 102, 7, 0]] }, { cell: [0, 2], items: [[44, 88, 36, 1]] },
      { cell: [0, 3], items: [[14, 118, 8, 0]] }],
    env: { w: 700, h: EH }, lane: 540,
    letterAt: { maya: [210, -792], theo: [210, 1504 - 520] }, answerAt: { theo: [210, 1504 - ANS_H] },
    envAt: { maya: [190, -578 + MY], theo: [190, 962], relay: [190, 778 - EH] },    // maya: the open flap's edge 6 px in the groove
  });

  // ================================================================ text by baseline
  /**
   * One line of text placed by its left edge (or centre / right) and baseline, measured with the engine's fonts.
   * o: { role (name or object) | face, size, weight, tracking(em), upper, colour, x, baseline, anchor:'start'|'middle'|'end',
   *      spans:[{text, colour}] }. Returns { el, x0, x1, width, top, spans:[span el] } (static; animate with css()).
   */
  function text(str, o = {}) {
    const r = { ...(typeof o.role === 'string' ? ROLE[o.role] : o.role ?? {}), ...o };
    const fam = FAM[r.face ?? 'mono'], size = r.size, weight = r.weight ?? 400, tr = r.tracking ?? 0;
    const parts = r.spans ?? [{ text: str }];
    const full = parts.map((p) => p.text).join('');
    const shown = r.upper ? full.toUpperCase() : full;
    const width = F.measure(shown, fam, size, weight, tr);
    const anchor = r.anchor ?? 'start';
    const x0 = anchor === 'start' ? r.x : anchor === 'middle' ? r.x - width / 2 : r.x - width;
    const top = r.baseline - F.baselineOffset(fam, size, weight);
    const el = h('div', { style: `position:absolute;left:${x0}px;top:${top}px;white-space:pre;font-family:${fam};font-size:${size}px;font-weight:${weight};line-height:${size}px;letter-spacing:${tr}em;color:${typeof r.colour === 'string' ? r.colour : C.text};${r.upper ? 'text-transform:uppercase;' : ''}` });
    const spans = parts.map((p) => { const sp = h('span', { style: typeof p.colour === 'string' ? `color:${p.colour}` : '', text: p.text }); el.append(sp); return sp; });
    return { el, x0, x1: x0 + width, width, top, spans, size, fam, weight };
  }
  /** cap height of a face at a size (px), measured */
  const capCache = new Map();
  function capHeight(fam, size, weight = 400) {
    const k = `${fam}|${size}|${weight}`;
    if (!capCache.has(k)) { const cx = document.createElement('canvas').getContext('2d'); cx.font = `${weight} ${size}px ${fam}`; capCache.set(k, cx.measureText('H').actualBoundingBoxAscent); }
    return capCache.get(k);
  }

  // ================================================================ paper primitives
  /** A specimen label: label stock with an inset hairline (ink 55 %), the brand's typed-card construction. Local px. */
  function specimen({ x, y, w, h: hh, r = 3, inset = 5, line = HAIR, fill = C2.stock, tooth: tz, edge = null }) {
    const el = h('div', { style: `position:absolute;left:${x}px;top:${y}px;width:${w}px;height:${hh}px;background:${fill};border-radius:${r}px` });
    tooth(el, tz, 31);
    // edge: a 1 px outer edge in that colour, inside the label's box (final polish, item 10: the seal tabs)
    if (edge) el.append(h('div', { style: `position:absolute;inset:0;border:1px solid ${edge};border-radius:${r}px;box-sizing:border-box;pointer-events:none` }));
    el.append(h('div', { style: `position:absolute;left:${inset}px;top:${inset}px;width:${w - 2 * inset}px;height:${hh - 2 * inset}px;border:${line}px solid ${C2.ink55};border-radius:${Math.max(1, r - inset * 0.6)}px;box-sizing:border-box` }));
    return el;
  }
  /** the brand mark from assets/<file> (inline SVG, its own ink, never recoloured), `w` px wide */
  function mark(file, w) {
    const src = F.assetText?.[`assets/${file}`];
    if (!src) throw new Error(`asset missing: ${file}`);
    const box = h('div', { style: 'position:absolute;left:0;top:0;line-height:0' });
    box.innerHTML = src;
    const sv = box.querySelector('svg');
    sv.querySelector('title')?.remove();
    const vb = sv.viewBox.baseVal, hh = (w * vb.height) / vb.width;
    sv.setAttribute('width', String(w)); sv.setAttribute('height', String(hh));
    sv.style.display = 'block'; sv.style.overflow = 'visible';
    return { el: box, w, h: hh };
  }
  /**
   * A horizontal hairline that stays anti-aliased under worldCamStable's 0.05° roll. Chromium fills anything it can treat as
   * a plain rectangle (a CSS background box, an SVG rect, line or 4-point path) on a pixel-snapped fast path, so a long
   * thin line jumps a whole pixel where the rolled line crosses a pixel row (the gate's 1 px steps on the flap edge and the
   * throat; measured: x 1525 at y 300 – 600, x 1271 at y 387 in 16:9). A path with one extra, 0.0001 px point is not a
   * rectangle: it is filled with full anti-aliasing and glides (out/v2fix/hlt_kinds.png: div / rect / line / path step
   * 0.34 px, this and CSS borders 0.04). (x, y) is the line's top-left in the parent; extra: more CSS for the holder.
   */
  function hline(x, y, w, weight, colour, extra = '') {
    const sv = svgEl('svg', { width: w, height: Math.ceil(weight) + 2, style: `position:absolute;left:${x}px;top:${y - 1}px;overflow:visible;${extra}` });
    const b = 1 + weight;
    sv.append(svgEl('path', { d: `M0 1 L${w / 2} 1.0001 L${w} 1 L${w} ${b} L${w / 2} ${b + 0.0001} L0 ${b} Z`, fill: colour }));
    return sv;
  }
  /**
   * final polish (director, item 3): a filled box on hline's anti-aliased path, for the rack's horizontal bars, its recess
   * and the counter lip. A CSS box or a plain SVG rect is filled on Chromium's pixel-snapped fast path, so under
   * worldCamStable's roll and the 4:5 push its long horizontal edges stepped 0.45 – 0.7 px at fixed columns (frames 868 –
   * 900, x 508 / 762). One extra 0.0001 px point in the middle of each edge (and the arcs of a radius) make it a path that
   * is filled with full anti-aliasing, so the edges glide. [x0, y0, x1, y1] in the parent; r: corner radius (all four).
   */
  function aaBox(x0, y0, x1, y1, fill, r = 0) {
    const w = x1 - x0, hh = y1 - y0, e = 0.0001, k = Math.min(r, w / 2, hh / 2);
    const sv = svgEl('svg', { width: w + 2, height: hh + 2, style: `position:absolute;left:${x0 - 1}px;top:${y0 - 1}px;overflow:visible` });
    const X0 = 1, Y0 = 1, X1 = 1 + w, Y1 = 1 + hh, mx = 1 + w / 2, my = 1 + hh / 2;
    const d = k > 0
      ? `M${X0 + k} ${Y0} L${mx} ${Y0 + e} L${X1 - k} ${Y0} A${k} ${k} 0 0 1 ${X1} ${Y0 + k} L${X1 - e} ${my} L${X1} ${Y1 - k} `
        + `A${k} ${k} 0 0 1 ${X1 - k} ${Y1} L${mx} ${Y1 - e} L${X0 + k} ${Y1} A${k} ${k} 0 0 1 ${X0} ${Y1 - k} L${X0 + e} ${my} L${X0} ${Y0 + k} A${k} ${k} 0 0 1 ${X0 + k} ${Y0} Z`
      : `M${X0} ${Y0} L${mx} ${Y0 + e} L${X1} ${Y0} L${X1 - e} ${my} L${X1} ${Y1} L${mx} ${Y1 - e} L${X0} ${Y1} L${X0 + e} ${my} Z`;
    sv.append(svgEl('path', { d, fill }));
    return sv;
  }
  /**
   * Optional paper tooth (§4.1, PAPER_TOOTH, or per object with o.tooth): a static seeded tile of short fibres and grain, never
   * animated, at most ± 2 % luminance, laid over a paper face. Generated once from FILM.rng (no Math.random).
   */
  const toothURL = {};
  function tooth(el, on, seed = 97) {
    if (!(on ?? PAPER_TOOTH)) return;
    const key = 'paper';
    if (!toothURL[key]) {
      const n = 384, cv = document.createElement('canvas'); cv.width = cv.height = n;
      const cx = cv.getContext('2d'), r = F.rng(seed);
      // fine grain: single pixels ± 1.5 %
      const im = cx.createImageData(n, n);
      for (let i = 0; i < n * n; i++) { const d = r() < 0.5; const v = d ? 0 : 255; im.data[i * 4] = im.data[i * 4 + 1] = im.data[i * 4 + 2] = v; im.data[i * 4 + 3] = Math.round(r() * r() * 255 * 0.03); }
      cx.putImageData(im, 0, 0);
      // fibres: short soft strokes, mostly along the sheet, ≤ 2 %
      cx.lineCap = 'round';
      for (let k = 0; k < 900; k++) {
        const x = r() * n, y = r() * n, a = (r() - 0.5) * 1.2 + (r() < 0.5 ? 0 : Math.PI / 2) * (r() < 0.25 ? 1 : 0), l = 4 + r() * 18;
        cx.strokeStyle = r() < 0.55 ? `rgba(23,18,43,${(0.008 + r() * 0.012).toFixed(4)})` : `rgba(255,255,255,${(0.01 + r() * 0.01).toFixed(4)})`;
        cx.lineWidth = 0.6 + r() * 0.8;
        for (const [ox, oy] of [[0, 0], [-n, 0], [0, -n], [-n, -n]]) { cx.beginPath(); cx.moveTo(x + ox, y + oy); cx.lineTo(x + ox + Math.cos(a) * l, y + oy + Math.sin(a) * l); cx.stroke(); }
      }
      toothURL[key] = cv.toDataURL();
    }
    el.append(h('div', { style: `position:absolute;inset:0;border-radius:inherit;background-image:url(${toothURL[key]});background-size:384px 384px;pointer-events:none` }));
  }

  /**
   * The slot as a clip edge: paper is hidden on the machine's side of its slot line. clip: [{y, keep:'below'|'above'}] in the
   * paper's parent coords (world); (ox, oy) is the paper's top-left there. Sets clip-path on el (local coords).
   */
  function slotClip(el, clip, ox, oy, ext = null) {
    let top = -1e5, bot = 1e5;
    // the line sits 0.5 px inside the visible side, so an edge lying exactly on the slot leaves no anti-aliased sliver
    for (const c of clip ?? []) { if (c.keep === 'below') top = Math.max(top, c.y - oy + 0.5); else bot = Math.min(bot, c.y - oy - 0.5); }
    const none = top <= -1e5 + 1 && bot >= 1e5 - 1;
    const v = none ? 'none' : `polygon(-6000px ${top.toFixed(3)}px, 6000px ${top.toFixed(3)}px, 6000px ${bot.toFixed(3)}px, -6000px ${bot.toFixed(3)}px)`;
    // fully inside the machine: hidden (ext = [top, bottom] of what the element draws, local px)
    const hidden = ext && Math.min(ext[1], bot) - Math.max(ext[0], top) <= 0.25;
    css(el, { clipPath: v, visibility: hidden ? 'hidden' : 'visible' });
    return [top, bot];
  }

  // ================================================================ the envelope (§4.4)
  // [x, y, w, h] in local px; fields: key → [caption, x, caption baseline, value baseline]; seams: [x1, y1, x2, y2]
  // r2 (gallery gate): the flap has wallet shoulders (its lower edge stops `chamfer` px short of each side, 45°).
  // r3 (gate r2): the side seams are the script's short corner seams again (§4.4): 45° from each bottom corner, never
  // reaching the form. Seams converging under the tab drew the email icon on a blank body and hung the tab on two wires.
  // r3 form (gate r2): the caption + value block is centred optically in the inset hairline (equal air from the hairline
  // to the FROM / TO cap tops and from the row-2 descenders to the hairline), and each row has a pointer band. The pointer
  // sits ptrDy world px under its value's baseline (16:9 20 px on screen at CAM-R+ 1.3, 4:5 21 px at 1.2): 8 px clear
  // under the deepest descender (`y`, `_`), and at least 20 px clear over the next row's captions and 14 px over the
  // bottom hairline, so the dots stay nearer their own value than anything else. The seal pointer (sealY) has 8 – 10 px
  // clear to the tab and the form on screen. Measured metrics: caption cap 14.5, value cap 24 / 23, descender 7.
  // fix-r3 (ROLE: captions 22 / 24, 16:9 values 36): the 16:9 form is 580 wide (was 560, same centre x 380, so the push's
  // P is unchanged), columns at 114 / 409 (17 px of air each side of `FROM … 1,444 bytes`, 36 px between the columns),
  // rows re-spaced; measured (qa/dirfix/fix-r3/tools/formmetrics.mjs, screen px at CAM-R+): 16:9 air 24 / 24, pointer
  // 7.4 under the descenders, 19 over row 2's captions, 10.5 over the hairline; 4:5 (row 2 3 px lower) air 26 / 28,
  // 8 / 20 / 10.6. Caption cap 16 / 18, value cap 27 / 24, descender 8 / 7.
  const ENV = {
    scene: pick({
      w: 760, h: EH, r: 4, flapH: 118, chamfer: 30,
      tab: [328, 71, 104, 94], mark: ['compact-on-light.svg', 73, 380, 118], form: [90, 185, 580, 189],
      fields: { from: ['FROM', 114, 226, 259], to: ['TO', 409, 226, 259], time: ['TIME', 114, 308, 341], size: ['SIZE', 409, 308, 341] },
      cap: 'formCap', val: 'formVal', sealY: 175, ptrDy: 16, seams: [[0, 392, 68, 324], [760, 392, 692, 324]],
    }, {
      w: 700, h: EH, r: 6, flapH: 112, chamfer: 28,
      tab: [298, 65, 104, 94], mark: ['compact-on-light.svg', 73, 350, 112], form: [86, 183, 528, 200],
      fields: { from: ['FROM', 110, 230, 260], to: ['TO', 376, 230, 260], time: ['TIME', 110, 316, 346], size: ['SIZE', 376, 316, 346] },
      cap: 'formCap', val: 'formVal', sealY: 171, ptrDy: 17.4, seams: [[0, 400, 64, 336], [700, 400, 636, 336]],
    }),
    // r3: our form's text centred in its inset too (16:9 15 / 15.7 px of air, 4:5 14 / 14)
    // final r1 (frame review): the seal tab grows so the full mark has §4.4's >= 14 px of clear space to the inset hairline
    // on every side (measured, world px: 16:9 142 x 124 gave 14.1 / 14.1 / 13.2 / 12.3 left / right / top / bottom, 4:5
    // 136 x 120 12.5 / 12.5 / 10.6 / 11.5). 16:9 4 px taller (128), 4:5 4 px wider and 8 px taller (140 x 128), both still
    // centred on the mark and the flap edge (500, 138) / (450, 124); air to the form 30 / 28 px (was 32 / 32)
    ours: pick({
      w: 1000, h: 460, r: 4, flapH: 138, chamfer: 36,
      tab: [429, 74, 142, 128], mark: ['mark-on-light.svg', 104, 500, 138], form: [150, 232, 700, 128],
      fields: { from: ['FROM', 186, 272, 327], to: ['TO', 531, 272, 327] }, cap: 'ourFormCap', val: 'ourFormVal', sealY: 214,
      seams: [[0, 460, 90, 370], [1000, 460, 910, 370]],
    }, {
      w: 900, h: 414, r: 6, flapH: 124, chamfer: 32,
      tab: [380, 60, 140, 128], mark: ['mark-on-light.svg', 100, 450, 124], form: [130, 216, 640, 120],
      fields: { from: ['FROM', 162, 254, 305], to: ['TO', 477, 254, 305] }, cap: 'ourFormCap', val: 'ourFormVal', sealY: 198,
      seams: [[0, 414, 82, 332], [900, 414, 818, 332]],
    }),
  };
  const TAB_EDGE = C2.crease;                       // the seal tab's 1 px outer edge (item 10): the light line token
  let UID = 0;
  /**
   * Where a moving thing is drawn: its position, snapped to whole px whenever it is at rest (the same a frame before and
   * after), so a resting paper's hairlines sit on the pixel grid; while it moves it keeps its sub-pixel path.
   */
  function restPos(o, t) {
    const at = (tt) => [val(o.x, tt) ?? 0, val(o.y, tt) ?? 0];
    const [x, y] = at(t), [xa, ya] = at(t - 1 / FPS), [xb, yb] = at(t + 1 / FPS);
    const still = Math.abs(x - xa) + Math.abs(x - xb) + Math.abs(y - ya) + Math.abs(y - yb) < 1e-6;
    return still ? [Math.round(x), Math.round(y)] : [x, y];
  }
  /**
   * The back of a wallet envelope: body (light surface-2), a wallet flap (label stock) with shoulders on a hinge at the top
   * fold, short corner seams, the address form, the seal tab with the brand mark, the slit. Paper never fades or scales.
   * The envelope is a plain 2D element at rest: only while the flap swings (0 < a < 180) does the flap carry
   * `perspective(1600px) rotateX(a)`. (A perspective on the envelope itself made Chromium composite it as a 3D layer whose
   * 256 px tiles each rasterised at their own sub-pixel offset: the flap hairline stepped 1 px at every tile seam.)
   * o: { kind:'scene'|'ours', address:{from,to,time,size}, x, y (top-left in the parent, number or fn(t)),
   *      flap: degrees 0 (closed) … 180 (open, pointing up) | fn(t), formAt, sealAt (ink, one frame; null = present,
   *      Infinity = never), slitAt (the 120 ms slit), clip: [{y, keep}] | fn(t) (slot clip in parent coords) }
   * Returns { el, pocket (letters ride inside it: in front of the open flap, behind the body), geo, fields, pointer(), render(t) }.
   */
  function envelope(o = {}) {
    const E = ENV[o.kind ?? 'scene'];
    const addr = o.address ?? (o.kind === 'ours' ? ADDR.ours : ADDR.brief);
    const { w, r, flapH, chamfer: ch } = E, eh = E.h;
    const uid = `v2env${++UID}`;
    const root = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${eh}px` });
    // the flap: one paper panel hinged on the top fold. Outside label stock; inside label stock too (§4.1 light bg), so the
    // open flap stands apart from the body's surface-2; its free edge carries the line-strong hairline in every state.
    const fd = `M0 ${flapH - ch} L0 ${r} Q0 0 ${r} 0 L${w - r} 0 Q${w} 0 ${w} ${r} L${w} ${flapH - ch} L${w - ch} ${flapH} L${ch} ${flapH} Z`;
    const flap = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${flapH}px;transform-origin:50% 0px;z-index:6` });
    const fsvg = svgEl('svg', { width: w, height: flapH, viewBox: `0 0 ${w} ${flapH}`, style: 'position:absolute;left:0;top:0;overflow:visible' });
    const defs = svgEl('defs'), cp = svgEl('clipPath', { id: `${uid}-f` });
    cp.append(svgEl('path', { d: fd })); defs.append(cp);
    const ffill = svgEl('path', { d: fd, fill: C2.stock });
    // the edge: a 2 × HAIR stroke on the free edge clipped to the flap leaves exactly HAIR inside the paper
    const fedge = svgEl('path', { d: `M0 ${flapH - ch} L${ch} ${flapH} L${w - ch} ${flapH} L${w} ${flapH - ch}`, fill: 'none', stroke: C2.hairline, 'stroke-width': 2 * HAIR, 'stroke-linejoin': 'miter', 'clip-path': `url(#${uid}-f)` });
    fsvg.append(defs, ffill, fedge); flap.append(fsvg);
    const pocket = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${eh}px;z-index:2` });
    const body = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${eh}px;background:${C2.paper2};border-radius:${r}px;overflow:hidden;z-index:3` });
    // seams: the two short corner seams (§4.4), 45° in from the bottom corners; they never reach the form
    const seams = svgEl('svg', { width: w, height: eh, viewBox: `0 0 ${w} ${eh}`, style: 'position:absolute;left:0;top:0' });
    for (const [x1, y1, x2, y2] of E.seams) seams.append(svgEl('line', { x1, y1, x2, y2, stroke: C2.crease, 'stroke-width': HAIR, 'stroke-linecap': 'butt' }));
    body.append(seams); tooth(body, o.tooth, 12);
    // the throat: while the flap stands open, the body's top edge is a cut paper edge over the flap's inside
    const throat = hline(0, 0, w, HAIR, C2.hairline, 'z-index:4;display:none');
    // the address form (2 × 2 or, ours, 1 × 2): label stock, inset hairline, typed captions and values
    const [fx, fy, fw, fhh] = E.form;
    const form = specimen({ x: fx, y: fy, w: fw, h: fhh, tooth: o.tooth });
    form.style.zIndex = '5';
    const fields = {};
    for (const [key, [cap, x, capBase, valBase]] of Object.entries(E.fields)) {
      const c = text(cap, { role: E.cap, x: x - fx, baseline: capBase - fy });
      const v = text(addr[key], { role: E.val, x: x - fx, baseline: valBase - fy });
      form.append(c.el, v.el);
      fields[key] = { x0: x, x1: x + v.width, base: valBase, capBase, value: addr[key] };
    }
    // the seal: a plain specimen tab straddling the flap edge (label stock, 5 px inset hairline, radius 3), the mark in its ink
    const [tx, ty, tw, th] = E.tab;
    // final polish (director, item 10): the tab straddles the flap edge, so its label stock vanished into the flap (also
    // label stock) on its upper half and stood as a light rim on the body (surface-2) on its lower half. A uniform 1 px
    // outer edge (TAB_EDGE) makes it one crisp label all round
    const tab = specimen({ x: tx, y: ty, w: tw, h: th, tooth: o.tooth, edge: TAB_EDGE });
    tab.style.zIndex = '7';
    const [mf, mw, mcx, mcy] = E.mark;
    const mk = mark(mf, mw);
    css(mk.el, { left: `${mcx - tx - mw / 2}px`, top: `${mcy - ty - mk.h / 2}px` });
    tab.append(mk.el);
    fields.seal = { x0: tx, x1: tx + tw, base: E.sealY, value: null };
    // the slit (IV): a cut along the top fold, ink meta, 4 px a few px under the edge, opening from the centre
    const SL = { y: 6, h: 4, inset: 12 };
    const slit = h('div', { style: `position:absolute;top:${SL.y}px;height:${SL.h}px;left:${w / 2}px;width:0;background:${C2.inkMeta};border-radius:${SL.h / 2}px;z-index:8;display:none` });
    const psvg = svgEl('svg', { width: w, height: eh, style: 'position:absolute;left:0;top:0;overflow:visible;z-index:9' });
    root.append(flap, pocket, body, throat, form, tab, slit, psvg);
    const pointers = [];
    const closedClip = `polygon(0px ${flapH - ch - 1}px, ${ch}px ${flapH - 1}px, ${w - ch}px ${flapH - 1}px, ${w}px ${flapH - ch - 1}px, ${w}px ${eh}px, 0px ${eh}px)`;
    return {
      el: root, pocket, geo: E, fields,
      /** a pointer (accent ink dotted underline) under a field's value ('from'|'to'|'time'|'size'|'seal'), drawn on the paper.
       *  s: the camera scale it is read at (16:9 CAM-R+ = 1.3, 4:5 = 1.2), so it reads 6 / 24 (9 / 36) px on screen. It sits
       *  in its row's pointer band, E.ptrDy world px under the baseline (r3; dy, in screen px, overrides it). */
      pointer(field, { at, outAt, s = 1, dy = null } = {}) {
        const f = fields[field];
        const y = field === 'seal' ? f.base : f.base + (dy != null ? dy / s : E.ptrDy ?? pick(16, 18) / s);
        const p = pointer(psvg, { x0: f.x0, x1: f.x1, y, at, outAt, k: 1 / s, colour: C2.accentInk });
        pointers.push(p); return p;
      },
      render(t) {
        const [x, y] = restPos(o, t);
        css(root, { transform: `translate(${+x.toFixed(3)}px, ${+y.toFixed(3)}px)` });
        const a = clamp(val(o.flap, t) ?? 0, 0, 180), open = a > 90;
        // at rest the flap is 2D: closed = as drawn; open = mirrored about the top fold (what rotateX(180°) shows, flat)
        if (a <= 1e-3) css(flap, { transform: 'none', zIndex: '6' });
        else if (a >= 180 - 1e-3) css(flap, { transform: 'scaleY(-1)', zIndex: '1' });
        else css(flap, { transform: `perspective(1600px) rotateX(${a.toFixed(3)}deg)`, zIndex: open ? '1' : '6' });
        ffill.setAttribute('fill', open ? C2.flapIn : C2.stock);
        // closed: the body hides under the flap (1 px overlap, following its shoulders) so shared edges never double
        css(body, { borderRadius: open ? `0 0 ${r}px ${r}px` : `${r}px`, clipPath: a <= 1e-3 ? closedClip : 'none' });
        css(throat, { display: open ? 'block' : 'none' });
        css(form, { visibility: inkOn(t, o.formAt) ? 'visible' : 'hidden' });
        css(tab, { visibility: inkOn(t, o.sealAt) ? 'visible' : 'hidden' });
        const sl = o.slitAt == null ? 0 : seg(t, o.slitAt, 0.12);
        const sw = (w - 2 * SL.inset) * sl;
        css(slit, { display: sl > 0 ? 'block' : 'none', left: `${((w - sw) / 2).toFixed(2)}px`, width: `${sw.toFixed(2)}px` });
        for (const p of pointers) p.render(t);
        slotClip(root, val(o.clip, t), x, y, [a > 90 ? -flapH : 0, eh]);
      },
    };
  }
  /**
   * An envelope with a folded sheet in its pocket (§4.4: the folded letter rides inside, its top edge 24 px above the top
   * fold, in front of the open flap's inside, behind the body; it drops the last 24 px in 120 ms at `dropAt`).
   * o: envelope() options + { sheet: 'brief'|'answer', dropAt (null: already in) }. Returns { el, env, sheet, render(t) }.
   */
  function sealed(o = {}) {
    const env = envelope(o);
    const S = SHEETS[o.sheet ?? 'brief'];
    // the folded packet: folded in half (or with the crease past the middle) the bottom half is up and the packet is the
    // top part (local 0 → crease); with the crease above the middle (dirfix: the 4:5 answer, a letter fold) the top part
    // is folded down and the packet is the bottom part (crease → h), so it rides crease px higher to sit in the pocket
    const down = S.crease < S.h / 2, top = down ? S.crease : 0;
    const inside = paper({ kind: o.sheet ?? 'brief', fold: 1, foldDir: down ? 'down' : 'up', edge: 'strong', foldPrint: false, x: (env.geo.w - S.w) / 2,
      y: (t) => -top + (o.dropAt == null ? 0 : -24 + motion.drop(t, o.dropAt)) });
    env.pocket.append(inside.el);
    return { el: env.el, env, sheet: inside, pointer: env.pointer, render(t) { env.render(t); inside.render(t); } };
  }

  // ================================================================ letters and the answer sheet (§4.5, §5.4)
  // r2 (gallery gate): the brief's paragraphs sit at one rhythm (What → Why = Why → Done = 70 / 64 px, was 92 / 54) with the
  // in-half crease centred in the What / Why gap (19 / 17 px clear in 16:9, 21 / 14 in 4:5); the answer sets its summary
  // near the brief's rhythm (first line 78 px under the rule; the brief's 90 / 70) and folds in half between its two lines;
  // r3 (gate r2): the answer sheet is shorter and its block centred (header cap top to the last descender, 16:9 58 → 244 in
  // 300, 4:5 53 → 223 in 276; dir-v2: 4:5 115 → 285 in 400, the same block 62 px lower); it folds in half in the clear gap
  // between the rule and the summary (19 / 17 px above line 1's cap tops, 56 / 52 under the rule). The note is Dorylinae
  // stationery (NOTE_LETTERHEAD, dir-v2): the compact mark over a hairline rule, pre-printed, no date (§11 cut the postmark date, and a capture-day date would date the invitation); its
  // crease falls in the gap between `That decides …` and `Free during …`. NOTE_LETTERHEAD false: §5.4's plain layout.
  // dir-v2: the note's hinge has a long perspective (NOTE_PERSP). At 1600 px its turning half (410 / 380 px deep) brought its
  // free edge 1.3 × the sheet's width toward the camera at edge-on (4:5: past both frame edges for 3 frames), a CSS card
  // flip; at 3600 it swings toward the viewer at most 1.13 / 1.12 × wide. The scene's sheets and flaps keep 1600.
  // dirfix: every sheet folds with the same lens: the brief and the answer take NOTE_PERSP too (at 1600 the brief's turning
  // half flared up to 76 px past each side edge in 16:9, the answer's 33 / 44 px, while the note's stayed near 1.1 ×).
  // The envelope's flap keeps 1600 (its swing adds only ~21 px).
  // dirfix: the 4:5 answer sheet is laid out as the same paper as the brief: header, rule and summary at the brief's
  // document positions (header baseline 70, rule 86; it sat 113 px down a 400 px sheet and read as a card) and the extra
  // height is bottom margin. Its fold moves with the text: the crease at 160 (74 px under the rule, 24 px over line 1's
  // cap tops), so the top 160 px fold down over the rest (a letter fold, not in half) and the 240 px packet goes into
  // theo's slot (SLIDE.answerIn). 16:9: the same since the final polish (400 tall, letter fold at 280, up).
  const NOTE_PERSP = 3600;
  const SHEETS = {
    brief: pick({ w: 724, h: 590, r: 4, crease: 295, head: ['flaky-brief.md', 49, 76], rule: [94, 49, 675], x: 49, bases: [184, 226, 268, 338, 380, 450, 492, 534], persp: NOTE_PERSP },
      { w: 660, h: 520, r: 6, crease: 260, head: ['flaky-brief.md', 51, 70], rule: [86, 51, 609], x: 51, bases: [156, 194, 232, 296, 334, 398, 436, 474], persp: NOTE_PERSP }),
    // final polish (item 6): 16:9 400 tall with a letter fold toward theo's slot (above): the crease at 280, in the clear
    // band under the summary (36 px under the pointer's dots), so the bottom 120 px fold up over the 280 px packet that goes
    // into his slot (in half, the crease would have crossed the summary's first line)
    answer: pick({ w: 724, h: ANS_H, r: 4, crease: 280, head: ['status pass', 49, 76], rule: [94, 49, 675], x: 49, bases: [194, 236], persp: NOTE_PERSP },
      { w: 660, h: ANS_H, r: 6, crease: 160, head: ['status pass', 51, 70], rule: [86, 51, 609], x: 51, bases: [206, 244], persp: NOTE_PERSP }),
    note: NOTE_LETTERHEAD
      ? pick({ w: 960, h: 820, r: 4, crease: 410, x: 72, bases: [176, 234, 312, 370, 490, 580], sig: 700, mark: ['compact-on-light.svg', 52, 72, 36], rule: [104, 72, 888], persp: NOTE_PERSP },
        { w: 880, h: 760, r: 6, crease: 380, x: 64, bases: [160, 214, 284, 338, 456, 538], sig: 650, mark: ['compact-on-light.svg', 48, 64, 34], rule: [94, 64, 816], persp: NOTE_PERSP })
      // §5.4 world baselines less the letter's top (16:9 y 130, 4:5 y 295)
      : pick({ w: 960, h: 820, r: 4, crease: 410, x: 72, bases: [105, 163, 253, 311, 490, 580], sig: 700, persp: NOTE_PERSP },
        { w: 880, h: 760, r: 6, crease: 380, x: 64, bases: [96, 150, 230, 284, 456, 538], sig: 650, persp: NOTE_PERSP }),
  };
  /**
   * Paper slides (§4.4, §4.5): the distance from "wholly behind the slot's clip edge" to the rest place, and the duration.
   * Durations are the script's; every peak stays under 19 px a frame (FILM.CAM peaks at 1.667 × the average) except the
   * ones with blur windows (BLUR): the envelope into theo's machine (§4.10) and, r3, theo's brief going back in (0.45 s,
   * so the answer comes out only after it has gone: two sheets never cross, gate r2).
   */
  const SLIDE = (() => {
    const B = SHEETS.brief, A = SHEETS.answer, E = { h: EH, flapH: pick(118, 112) };
    const mS = GEO.maya.slot.y, tS = GEO.theo.slot.y;
    const [, ly] = GEO.letterAt.maya, [, ty] = GEO.letterAt.theo, [, ay] = GEO.answerAt.theo;
    const [, my] = GEO.envAt.maya, [, ey] = GEO.envAt.theo;
    return pick({
      letterOut: { d: ly - mS + B.h, dur: 0.9 }, letterIn: { d: ly - mS + B.crease, dur: 0.45 },
      envOut: { d: my + E.h - mS, dur: 0.75 }, envIn: { d: ey + E.h - tS, dur: 0.7 },
      briefOut: { d: ty - tS + B.h, dur: 0.9 }, briefIn: { d: ty - tS + B.h, dur: 0.45 },
      answerOut: { d: ay - tS + A.h, dur: 0.6 }, answerIn: { d: ay - tS + A.crease, dur: 0.4 },
      answerEnvOut: { d: ey + E.h - tS, dur: 0.75 },
    }, {
      letterOut: { d: ly - mS + B.h, dur: 0.9 }, letterIn: { d: ly - mS + B.crease, dur: 0.45 },
      envOut: { d: my + E.h - mS, dur: 0.75 }, envIn: { d: tS - ey, dur: 0.7 },
      briefOut: { d: tS - (ty + B.h) + B.h, dur: 0.9 }, briefIn: { d: tS - (ty + B.h) + B.h, dur: 0.45 },
      // the answer folds down (its top part over the rest): the packet's top edge is at ay + crease, and it goes until
      // that edge is in the slot (dirfix: general for a crease off the middle; at ANS_H / 2 it is the same distance)
      answerOut: { d: tS - (ay + A.h) + A.h, dur: 0.6 }, answerIn: { d: tS - (ay + A.crease), dur: 0.4 },
      answerEnvOut: { d: tS - ey, dur: 0.8 },
    });
  })();
  // flaky-brief.md word for word, wrapped at 29 characters field by field; f = field name (brand ink), v = value (ink)
  const BRIEF = [[['What:', 'f'], [' Find out why the', 'v']], [['payments integration test', 'v']], [['fails about one run in ten.', 'v']],
    [['Why:', 'f'], [' It blocks merges twice a', 'v']], [['day and nobody owns it yet.', 'v']],
    [['Done when:', 'f'], [' You name the', 'v']], [['cause, or rule out the three', 'v']], [['usual suspects.', 'v']]];
  const ANSWER = ['Cause found: two tests share', 'one ledger row'];
  /** the printed face of a sheet (one copy; paper() builds two, one per fold half) */
  function sheetFace(kind, o) {
    const S = SHEETS[kind];
    const el = h('div', { style: `position:absolute;left:0;top:0;width:${S.w}px;height:${S.h}px` });
    const inks = [];
    let typed = null;
    if (S.head) {
      el.append(text(S.head[0], { role: 'letterHead', x: S.head[1], baseline: S.head[2] }).el);
    }
    if (S.mark) {
      // the note's letterhead (NOTE_LETTERHEAD): the compact mark in its own ink, top left
      const [mf, mw, mx, my] = S.mark, mk = mark(mf, mw);
      css(mk.el, { left: `${mx}px`, top: `${my}px` });
      el.append(mk.el);
    }
    if (S.rule) {
      const [ry, rx0, rx1] = S.rule;
      el.append(hline(rx0, ry - HAIR / 2, rx1 - rx0, HAIR, C2.crease));
    }
    const at = (i) => (Array.isArray(o.lineAt) ? o.lineAt[i] : typeof o.lineAt === 'function' ? o.lineAt(i) : null);
    if (kind === 'brief') BRIEF.forEach((ln, i) => {
      const tx = text('', { role: 'letter', x: S.x, baseline: S.bases[i], spans: ln.map(([s, k]) => ({ text: s, colour: k === 'f' ? C2.brandInk : C2.ink })) });
      el.append(tx.el); inks.push({ el: tx.el, at: at(i) });
    });
    if (kind === 'answer') ANSWER.forEach((s, i) => { const tx = text(s, { role: 'letter', x: S.x, baseline: S.bases[i] }); el.append(tx.el); inks.push({ el: tx.el, at: at(i) }); });
    if (kind === 'note') {
      // final polish (director, item 14): the next step stands out: `dorylinae.net` in `Apply at dorylinae.net` is set in the
      // paper brand ink (#6B3FB8, 6.9 : 1 on paper white); colour only, the words are the copy's
      const CTA = 'dorylinae.net';
      COPY.NOTE.forEach((s, i) => {
        const k = s.endsWith(CTA) && s === COPY.ADDRESS ? s.length - CTA.length : -1;
        const tx = k < 0 ? text(s, { role: 'note', x: S.x, baseline: S.bases[i] })
          : text('', { role: 'note', x: S.x, baseline: S.bases[i], spans: [{ text: s.slice(0, k) }, { text: CTA, colour: C2.brandInk }] });
        el.append(tx.el); inks.push({ el: tx.el, at: at(i) });
      });
      const sg = text('', { role: 'signature', x: S.x, baseline: S.sig });
      typed = ui.typed(o.signature ?? SIGNATURE, { at: o.sigAt ?? Infinity, step: 0.085, jitter: 0 });
      sg.el.append(typed.el); el.append(sg.el);
    }
    return {
      el, typed,
      render(t) { for (const k of inks) css(k.el, { visibility: inkOn(t, k.at) ? 'visible' : 'hidden' }); if (typed) typed.render(t); },
    };
  }
  /** the flat fill of a turning half at angle a (degrees): paper white flat, C2.turn edge-on (r3; one colour per frame) */
  const paperTone = (a, base = C2.paper) => mix(base, C2.turn, 1 - Math.abs(Math.cos((a * Math.PI) / 180)));
  /** a rounded rectangle's outline path, with optional V notches (depth nd, half-height nh) in both side edges at y = notchY */
  function sheetPath(w, hh, r, notchY = null, nd = 2, nh = 6) {
    const side = (x, inward, down) => (notchY == null ? '' : down
      ? `L${x} ${notchY - nh} L${x + inward * nd} ${notchY} L${x} ${notchY + nh} `
      : `L${x} ${notchY + nh} L${x + inward * nd} ${notchY} L${x} ${notchY - nh} `);
    return `M${r} 0 L${w - r} 0 A${r} ${r} 0 0 1 ${w} ${r} ${side(w, -1, true)}L${w} ${hh - r} A${r} ${r} 0 0 1 ${w - r} ${hh} `
      + `L${r} ${hh} A${r} ${r} 0 0 1 0 ${hh - r} ${side(0, 1, false)}L0 ${r} A${r} ${r} 0 0 1 ${r} 0 Z`;
  }
  /**
   * A sheet of paper (white on both sides, printed on one): the brief, the answer sheet or our note. It folds in half on a
   * hinge (rotateX about the crease, perspective `persp` px, only while it turns; at rest it is flat 2D), the face switching
   * from print to its blank back at 90°. r3 (gate r2): the turning half is white paper too, told apart by its line-strong
   * free edge and one flat fill per frame from its angle (paperTone); folded flat it is white with a line-strong outline.
   * Once it has been folded (o.crease), the fold shows in the sheet itself: a 2 px V notch in both side edges at the crease
   * and the half that turned a barely different flat white (C2.halfTone), never a ruled line. Its lines are ink (one frame).
   * o: { kind:'brief'|'answer'|'note', x, y (top-left, number|fn), fold: 0 (flat) … 1 (folded) | fn(t),
   *      foldDir: 'up' (the bottom half folds up over the top: slot above) | 'down' (the top half folds down: slot below),
   *      crease: bool|fn (it has been folded before: notches + half tone), lineAt: [t per line] | fn(i) (null = printed),
   *      sigAt (note: typing starts), signature, edge: 'strong' (a sheet that lies over other paper: line-strong outline) |
   *      default (1 px line), clip: [{y, keep}] | fn(t), persp: the hinge's perspective px (default SHEETS[kind].persp, else
   *      1600: the scene's folds; the note's is NOTE_PERSP), foldPrint: false (folded, the print never shows) }
   * Returns { el, geo, typedTimes (signature letters), render(t) }.
   */
  function paper(o = {}) {
    const kind = o.kind ?? 'brief', S = SHEETS[kind];
    const { w, r, crease } = S, ph = S.h, persp = o.persp ?? S.persp ?? 1600;
    const up = (o.foldDir ?? 'up') === 'up';
    const strong = o.edge === 'strong';
    const lw = strong ? HAIR : 1, lc = strong ? C2.hairline : C2.crease;
    const outline = (face, wd = lw, col = lc) => { const d = h('div', { style: `position:absolute;inset:0;border:${wd}px solid ${col};border-radius:inherit;pointer-events:none` }); face.append(d); return d; };
    const root = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${ph}px` });
    const near = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${ph}px;overflow:hidden;background:${C2.paper};border-radius:${r}px` });
    const nearFace = sheetFace(kind, o);
    // the creased sheet (flat, folded before): one SVG behind the print draws the whole paper, with no clip at its outer
    // edge: the notched sheet in paper white, the half that turned in its own flat white (C2.halfTone), and the outline as
    // the notched path inset by half a line and stroked one line wide. The fills stop under the line's centre, so the
    // outer edge is the line's own anti-aliased edge. (r1 review: a CSS clip-path, and the sheet's overflow clip, are
    // aliased in Chromium, so under worldCamStable's 0.05° roll the 4:5 note's top edge showed a 1 px jog where the rolled
    // edge crossed a pixel row; this glides like the envelope and the card.)
    const ND = 2, NH = 6, i2 = lw / 2;
    const inset = sheetPath(w - lw, ph - lw, Math.max(r - i2, 0), crease - i2, ND, NH);
    const halfD = up
      ? `M${i2 + ND} ${crease} L${w - i2 - ND} ${crease} L${w - i2} ${crease + NH} L${w - i2} ${ph - r} A${r - i2} ${r - i2} 0 0 1 ${w - r} ${ph - i2} L${r} ${ph - i2} A${r - i2} ${r - i2} 0 0 1 ${i2} ${ph - r} L${i2} ${crease + NH} Z`
      : `M${r} ${i2} L${w - r} ${i2} A${r - i2} ${r - i2} 0 0 1 ${w - i2} ${r} L${w - i2} ${crease - NH} L${w - i2 - ND} ${crease} L${i2 + ND} ${crease} L${i2} ${crease - NH} L${i2} ${r} A${r - i2} ${r - i2} 0 0 1 ${r} ${i2} Z`;
    const nSheet = svgEl('svg', { width: w, height: ph, viewBox: `0 0 ${w} ${ph}`, style: 'position:absolute;left:0;top:0;overflow:visible;display:none;pointer-events:none' });
    const g2 = svgEl('g', { transform: `translate(${i2} ${i2})` });
    g2.append(svgEl('path', { d: inset, fill: C2.paper }));
    nSheet.append(g2, svgEl('path', { d: halfD, fill: C2.halfTone }),
      svgEl('path', { d: inset, transform: `translate(${i2} ${i2})`, fill: 'none', stroke: lc, 'stroke-width': lw, 'stroke-linejoin': 'miter' }));
    const nearIn = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${ph}px` }, nearFace.el);
    near.append(nSheet, nearIn); tooth(near, o.tooth, 21);
    const nOutlineDiv = outline(near);
    // the turning half: front = its print, back = blank; both white paper with a line-strong outline and free-edge hairline
    const farTop = up ? crease : 0, farH = up ? ph - crease : crease;
    const rad = up ? `0 0 ${r}px ${r}px` : `${r}px ${r}px 0 0`;
    const far = h('div', { style: `position:absolute;left:0;top:${farTop}px;width:${w}px;height:${farH}px;transform-origin:50% ${up ? 0 : farH}px;display:none` });
    const farFace = sheetFace(kind, o);
    const farPrint = h('div', { style: `position:absolute;left:0;top:${-farTop}px;width:${w}px;height:${ph}px` }, farFace.el);
    const farFront = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${farH}px;overflow:hidden;background:${C2.paper};border-radius:${rad}` }, farPrint);
    const farBack = h('div', { style: `position:absolute;left:0;top:0;width:${w}px;height:${farH}px;overflow:hidden;background:${C2.back};border-radius:${rad}` });
    // the turning half's outline and free edge go from the sheet's own line (light, 1 px, or line-strong) to line-strong
    // as it turns (one flat colour per frame), so nothing pops on the fold's first frame; folded flat: line-strong. Its
    // hinge side (the crease) carries a line only near folded (a 120° → 180°): flat-ish, the two halves meet seamlessly,
    // so an unfold ends on the notched, toned sheet without a ruled line vanishing on its last frame
    const farLines = [];
    for (const face of [farFront, farBack]) {
      tooth(face, o.tooth, 22);
      const ol = outline(face, HAIR, C2.hairline);
      css(ol, up ? { borderTopWidth: '0px' } : { borderBottomWidth: '0px' });
      const fe = hline(0, up ? farH - HAIR : 0, w, HAIR, C2.hairline);
      const hg = hline(0, up ? 0 : farH - HAIR, w, HAIR, C2.hairline);
      face.append(fe, hg);
      farLines.push({ ol, fe: fe.firstChild, hg });
    }
    const farLine = (k, kh) => {
      const col = mix(lc, C2.hairline, k), wd = `${(lw + (HAIR - lw) * k).toFixed(3)}px`;
      for (const { ol, fe, hg } of farLines) {
        css(ol, up ? { borderColor: col, borderLeftWidth: wd, borderRightWidth: wd, borderBottomWidth: wd } : { borderColor: col, borderLeftWidth: wd, borderRightWidth: wd, borderTopWidth: wd });
        fe.setAttribute('fill', col); hg.firstChild.setAttribute('fill', col); hg.setAttribute('opacity', op(kh));
      }
    };
    far.append(farFront, farBack);
    // folded flat, the near half's print shows only where the turned half leaves printed rows uncovered: a fold in half
    // covers it all (hidden, as before); the 16:9 answer's letter fold (crease 280 of 400, up) leaves rows 0 → 160, its
    // header and rule, showing (final r1, motion major: they vanished on the fold's last frame); the 4:5 answer's
    // (crease 160, down) leaves rows 320 → 400, blank paper, so it stays hidden there (pixels unchanged)
    const bare = up ? [0, crease - farH] : [crease + farH, ph];
    const inkY = [S.head && S.head[2], S.rule && S.rule[0], ...(S.bases ?? []), S.sig, S.mark && S.mark[3]].filter((v) => v != null);
    const inkBare = inkY.filter((v) => v + 16 > bare[0] && v - 48 < bare[1]);
    // (o.foldPrint false: never; the packet sealed in an envelope, whose uncovered print stays below the pocket's mouth)
    const foldShowsPrint = o.foldPrint !== false && bare[1] > bare[0] && inkBare.length > 0;
    // the printed band it leaves showing; once the slot has clipped all of it away (the packet going in), the print hides
    // again, so the slot's edge row rasters exactly as before (otherwise its anti-aliasing shifted by up to 21 levels)
    const inkBand = foldShowsPrint ? [Math.min(...inkBare) - 48, Math.max(...inkBare) + 16] : null;
    const psvg = svgEl('svg', { width: w, height: ph, style: 'position:absolute;left:0;top:0;overflow:visible' });
    root.append(near, far, psvg);
    const pointers = [];
    let state = null, creased = null;
    return {
      el: root, geo: S, typedTimes: nearFace.typed ? nearFace.typed.times : [],
      /** a pointer (accent ink) under printed line i of the sheet, on the paper, as long as that line. dy: px under the
       *  baseline on screen (16 / 18), or 'gap' to centre the dots between line i and the next line's ascenders. */
      pointer(i, { at, outAt, dur = 0.24, dy = pick(16, 18), s = 1 } = {}) {
        const ln = kind === 'brief' ? BRIEF[i].map(([x]) => x).join('') : kind === 'answer' ? ANSWER[i] : COPY.NOTE[i];
        const role = kind === 'note' ? ROLE.note : ROLE.letter;
        const wd = F.measure(ln, FAM[role.face], role.size, role.weight);
        let off = dy / s;
        if (dy === 'gap') { const cx = document.createElement('canvas').getContext('2d'); cx.font = `${role.weight} ${role.size}px ${FAM[role.face]}`; const asc = cx.measureText('ldh').actualBoundingBoxAscent; off = (S.bases[i + 1] - S.bases[i] - asc) / 2; }
        const p = pointer(psvg, { x0: S.x, x1: S.x + wd, y: S.bases[i] + off, at, outAt, dur, k: 1 / s, colour: C2.accentInk });
        pointers.push(p); return p;
      },
      render(t) {
        const [x, y] = restPos(o, t), f = clamp(val(o.fold, t) ?? 0);
        css(root, { transform: `translate(${+x.toFixed(3)}px, ${+y.toFixed(3)}px)` });
        const st = f <= 0 ? 'flat' : f >= 1 ? 'folded' : 'half';
        if (st !== state) {
          state = st;
          if (st === 'flat') css(near, { top: '0px', height: `${ph}px`, borderRadius: `${r}px` });
          else if (st === 'folded') css(near, up ? { top: '0px', height: `${crease}px`, borderRadius: `${r}px ${r}px 0 0` } : { top: `${crease}px`, height: `${ph - crease}px`, borderRadius: `0 0 ${r}px ${r}px` });
          // half: the near half runs 1 px under the hinge so the seam never shows the ground
          else css(near, up ? { top: '0px', height: `${crease + 1}px`, borderRadius: `${r}px ${r}px 0 0` } : { top: `${crease - 1}px`, height: `${ph - crease + 1}px`, borderRadius: `0 0 ${r}px ${r}px` });
          css(nearIn, { top: `${-(st === 'flat' || up ? 0 : st === 'folded' ? crease : crease - 1)}px`, display: st === 'folded' && !foldShowsPrint ? 'none' : 'block' });
          css(far, { display: st === 'flat' ? 'none' : 'block' });
          // while it turns, the near half has no line on its hinge side (the turning half's own hinge line takes over
          // near folded), so no seam shows between two white halves
          css(nOutlineDiv, up ? { borderBottomWidth: `${st === 'half' ? 0 : lw}px` } : { borderTopWidth: `${st === 'half' ? 0 : lw}px` });
        }
        if (st === 'half') {
          const a = 180 * f;
          css(far, { transform: `perspective(${persp}px) rotateX(${(up ? a : -a).toFixed(3)}deg)` });
          css(farFront, { display: a < 90 ? 'block' : 'none' }); css(farBack, { display: a < 90 ? 'none' : 'block' });
          css(farPrint, { display: a < 72 ? 'block' : 'none' });
          // the print side lands as the creased sheet's turned half (C2.halfTone) when it unfolds onto a crease
          css(farFront, { background: paperTone(a, val(o.crease, t) ? C2.halfTone : C2.paper) }); css(farBack, { background: paperTone(a) });
          farLine(clamp(a / 30), clamp((a - 120) / 60));
          farFace.render(t);
        } else if (st === 'folded') {
          // folded flat: the turned half mirrored about the crease, in 2D (what rotateX(180°) shows, without a 3D layer)
          css(far, { transform: 'scaleY(-1)' });
          css(farFront, { display: 'none' }); css(farBack, { display: 'block', background: C2.back });
          farLine(1, 1);
        }
        const cr = st === 'flat' && !!val(o.crease, t);
        if (cr !== creased) {
          creased = cr;
          // creased flat: nSheet is the paper (fill, half tone, line); the sheet's own box draws and clips nothing
          css(near, cr ? { background: 'transparent', overflow: 'visible' } : { background: C2.paper, overflow: 'hidden' });
          css(nSheet, { display: cr ? 'block' : 'none' }); css(nOutlineDiv, { display: cr ? 'none' : 'block' });
        }
        nearFace.render(t);
        for (const p of pointers) p.render(t);
        const [vt, vb] = slotClip(root, val(o.clip, t), x, y, st === 'folded' ? (up ? [0, crease] : [crease, ph]) : [0, ph]);
        if (st === 'folded' && inkBand) css(nearIn, { display: Math.min(inkBand[1], vb) > Math.max(inkBand[0], vt) ? 'block' : 'none' });
      },
    };
  }

  // ================================================================ the machines (§4.6)
  /** terminal rows: rows.prompt('maya', at, until) · rows.cmd('maya', lines, at) · rows.out(text, at). A row: {parts, at, until, dimAt} */
  const rows = {
    prompt: (who, at, until) => ({ parts: [{ text: `${who}$`, colour: C.brand }], at, until }),
    /** a command lands whole; its continuation lines follow 42 ms apart (lines[0] without the prompt) */
    cmd: (who, lines, at, extra = {}) => lines.map((ln, i) => ({
      parts: i === 0 ? [{ text: `${who}$ `, colour: C.brand }, { text: ln, colour: C.text2 }] : [{ text: ln, colour: C.text2 }], at: at + i * 0.042, ...extra })),
    out: (txt, at, extra = {}) => ({ parts: [{ text: txt, colour: C.text3 }], at, ...extra }),
    /** theo's inbox (capture/15): the command lands, its two output lines follow 42 ms apart; rowExtra goes on the inbox row */
    inbox: (who, at, rowExtra = {}) => [rows.cmd(who, [TERM.inbox.cmd], at)[0], rows.out(TERM.inbox.out[0], at + 0.042),
      rows.out(TERM.inbox.out[1], at + 0.084, rowExtra)],
    /**
     * agentnet identity, as the machine shows it at rest (capture/04, other lines omitted); the key line is dim (text-3 at
     * 40 %) from keyDimAt on (120 ms). keyDimAt = -1 (default): dim from the start (§7). A later time keeps the key line at
     * full text-3 until then (dir-v2: theo's key reads in full while the envelope addressed to it enters his machine).
     */
    identity: (who, key, at = -1, until = null, keyDimAt = -1) => [
      { parts: [{ text: `${who}$ `, colour: C.brand }, { text: 'agentnet identity', colour: C.text2 }], at },
      { parts: [{ text: `public key:  ${key}…`, colour: C.text3 }], at, dimAt: keyDimAt },
      { parts: [{ text: `${who}$`, colour: C.brand }], at, until },
    ],
  };
  /**
   * A machine: an opaque surface plate with the brand's terminal block inset in it, a 4-line window that scrolls like a real
   * terminal (older lines leave by the top in the frame a new one lands), and the slot (its bottom edge; theo's top edge in
   * 4:5) that paper comes out of and goes into (use machine.slot as a paper clip).
   * o: { who:'maya'|'theo' (geometry from GEO) | plate:[x0,y0,x1,y1], block, text:{x, base}, rows (rows.* lists),
   *      lines = 4, dim: fn(t) 0..1 (focus dim: text → text-3 at 40 %) }
   */
  function machine(parent, o = {}) {
    const g = o.who ? GEO[o.who] : {};
    const [px0, py0, px1, py1] = o.plate ?? g.plate;
    const [bx0, by0, bx1, by1] = o.block ?? g.block ?? [px0 + 12, py0 + 12, px1 - 12, py1 - 12];
    const tx = (o.text ?? g.text).x, base = (o.text ?? g.text).base;
    const size = o.size ?? ROLE.terminal.size, pitch = o.pitch ?? ROLE.terminal.pitch, LINES = o.lines ?? 4;
    const el = h('div', { style: 'position:absolute;left:0;top:0' });
    const plate = h('div', { style: `position:absolute;left:${px0}px;top:${py0}px;width:${px1 - px0}px;height:${py1 - py0}px;background:${C.surface};border-radius:${RADIUS}px` });
    const block = h('div', { style: `position:absolute;left:${bx0}px;top:${by0}px;width:${bx1 - bx0}px;height:${by1 - by0}px;background:${C.bg};border-radius:${BRADIUS}px;overflow:hidden` });
    el.append(plate, block); parent.append(el);
    // the slot: a 6 px groove (bg) in the plate's 24 px rim, inset from the corners, with a 2 px surface-2 lip on the side the
    // paper comes out of; paper is clipped at the groove's centre line, so it comes out of the groove (flat fills only)
    const sl = o.slot ?? g.slot;
    if (sl?.groove) {
      const [gx0, gx1] = sl.groove, below = sl.keep === 'below';
      el.append(h('div', { style: `position:absolute;left:${gx0}px;top:${sl.y - 3}px;width:${gx1 - gx0}px;height:6px;background:${C.bg};border-radius:3px` }));
      el.append(hline(gx0 + 3, below ? sl.y + 3 : sl.y - 5, gx1 - gx0 - 6, 2, C.surface2));
    }
    const bo = F.baselineOffset(FONT.mono, size, 400);
    const all = (o.rows ?? []).flat();
    // final polish (director, item 1): a command typed at a waiting prompt. When a bare-prompt row (`maya$`) ends on the
    // frame a command row starting with the same prompt lands in its place, the prompt is the same ink: it stays at full
    // strength and only the command text lands (120 ms). Before, the whole row faded in, so the purple prompt blinked off
    // for a frame and came back (16:9 / 4:5 frames 471 → 478, 1907 → 1914).
    const promptOf = (row) => (row.parts.length > 1 && /\$ $/.test(row.parts[0].text) ? row.parts[0].text.trimEnd() : null);
    const heldPrompt = (row) => { const p = promptOf(row); return p != null && row.at != null && all.some((q) => q !== row && q.until != null && q.parts.length === 1 && q.parts[0].text === p && frameOf(q.until) === frameOf(row.at)); };
    const items = all.map((row) => {
      const r = h('div', { style: `position:absolute;left:${tx - bx0}px;top:0;white-space:pre;font-family:${FONT.mono};font-size:${size}px;font-weight:400;line-height:${size}px;display:none` });
      const spans = row.parts.map((p) => { const c = p.colour ?? C.text3; const sp = h('span', { style: `color:${c}`, text: p.text }); r.append(sp); return { sp, c }; });
      block.append(r);
      return { r, row, spans, hold: heldPrompt(row) };
    });
    return {
      el, plate, block, slot: sl, rows: items,
      render(t) {
        const d = o.dim ? clamp(o.dim(t)) : 0;
        const live = items.filter(({ row }) => inkOn(t, row.at) && !(row.until != null && inkOn(t, row.until)));
        const shown = live.slice(-LINES);
        for (const it of items) {
          const i = shown.indexOf(it);
          if (i < 0) { css(it.r, { display: 'none' }); continue; }
          const land = it.row.at == null ? 1 : clamp((t - it.row.at) / 0.12);          // lands whole, 120 ms linear
          const rowDim = it.row.dimAt == null ? 1 : 1 - 0.6 * clamp((t - it.row.dimAt) / 0.12);
          // a held prompt (item 1): the row is at its level and its command spans land; otherwise the row lands whole
          css(it.r, { display: 'block', top: `${(base + i * pitch - bo - by0).toFixed(3)}px`, opacity: op((it.hold ? 1 : land) * Math.min(rowDim, 1 - 0.6 * d)) });
          it.spans.forEach((s, j) => { css(s.sp, { color: mix(s.c, C.text3, d) }); if (it.hold && j > 0) css(s.sp, { opacity: op(land) }); });
        }
      },
    };
  }

  // ================================================================ presence chips (§4.7)
  /**
   * A presence pill on the sky: surface-2, 52 px tall, radius 26, padding 22, dot Ø 22, gap 18 (r3; was 12), Plex Mono 500
   * 36 / 32. Shape-coded dot (ui.dot): online = solid `online`; offline = hollow 3 px `text-3` ring; ring fill 120 ms.
   * r3 (gate r2): the halo pulses are the chip's own: a 3 px `online` ring growing from the dot's radius to the pill's inner
   * half-height (24 px, its outer edge 0.5 px inside the pill), opacity 0.5 (arrival 0.8) → 0 over 1.2 s, brand ease, and
   * clipped to the pill (overflow hidden, radius 26): a pulse never leaves the pill or reaches the name (18 px away).
   * o: { who:'maya'|'theo' (geometry), name, x, y (top), w (fixed width), level: fn(t) 0..1 | 1 (FILM.levelFill(at)),
   *      words (state words offline/online, right-aligned to the padding; default for theo), pulses:[t], arrivals:[t],
   *      dim: fn(t) 0..1 (chip text to 60 %), fade: fn(t) 0..1 (the whole chip's opacity: 0 while a push would cut it at the
   *      frame edge, §5.2.3 moves 2 / 3) }
   */
  function chip(parent, o = {}) {
    const g = o.who ? GEO.chips[o.who] : {};
    const size = ROLE.chip.size, HT = 52, PAD = 22, D = 22, GAP = 18, HALO = HT / 2 - 2;
    const name = o.name ?? (o.who === 'maya' ? 'maya (you)' : o.who);
    const words = o.words ?? o.who === 'theo';
    const nameW = F.measure(name, FONT.mono, size, 500), wordW = F.measure('offline', FONT.mono, size, 500);
    const w = o.w ?? g.w ?? PAD + D + GAP + nameW + (words ? GAP + wordW : 0) + PAD;
    const x = o.x ?? g.x, y = o.y ?? g.y;
    const el = h('div', { style: `position:absolute;left:${x}px;top:${y}px;width:${w}px;height:${HT}px;border-radius:${HT / 2}px;background:${C.surface2};overflow:hidden` });
    const base = Math.round((HT + capHeight(FONT.mono, size, 500)) / 2);
    const nm = text(name, { role: 'chip', x: PAD + D + GAP, baseline: base });
    el.append(nm.el);
    let off = null, on = null;
    if (words) {
      off = text('offline', { role: 'chip', colour: C.text3, x: w - PAD - wordW, baseline: base });
      on = text('online', { role: 'chip', colour: C.text3, x: w - PAD - wordW, baseline: base });
      el.append(off.el, on.el);
    }
    const dsvg = svgEl('svg', { width: 1, height: 1, style: `position:absolute;left:${PAD + D / 2}px;top:${HT / 2}px;overflow:visible` });
    el.append(dsvg); parent.append(el);
    const level = typeof o.level === 'function' ? o.level : () => o.level ?? 1;
    const halos = [...(o.pulses ?? []).map((tp) => ({ t: tp, a: 0.5 })), ...(o.arrivals ?? []).map((tp) => ({ t: tp, a: 0.8 }))]
      .map((p) => ({ ...p, c: svgEl('circle', { cx: 0, cy: 0, r: D / 2, fill: 'none', stroke: C.online, 'stroke-width': 3, opacity: 0 }), v: null }));
    for (const p of halos) dsvg.append(p.c);
    const dot = ui.dot(dsvg, { cx: 0, cy: 0, d: D, stroke: 3, level, pulses: [], arrivals: [] });
    return {
      el, w, x, y, base,
      render(t) {
        const fa = o.fade ? clamp(o.fade(t)) : 1;
        css(el, { opacity: op(fa), display: fa > 0 ? 'block' : 'none' });
        if (fa <= 0) return;
        dot.render(t);
        for (const p of halos) {
          const u = (t - p.t) / 1.2;
          const v = u < 0 || u >= 1 ? '0' : op(p.a * (1 - EASE(u)));
          if (v !== p.v) { p.c.setAttribute('opacity', v); p.v = v; }
          if (v !== '0') p.c.setAttribute('r', (D / 2 + (HALO - D / 2) * EASE(u)).toFixed(2));
        }
        const a = 1 - 0.4 * (o.dim ? clamp(o.dim(t)) : 0), k = clamp(level(t));
        css(nm.el, { opacity: op(a) });
        if (off) { css(off.el, { opacity: op(a * (1 - clamp(k / 0.5))) }); css(on.el, { opacity: op(a * clamp((k - 0.5) / 0.5)) }); }
      },
    };
  }

  // ================================================================ the relay: rack, sign, counter, clock (§4.8)
  /** the `Relay` sign: a specimen label (pad 0.9 em × 0.7 em, lead 1.25 em, inset hairline 5 px), centred on cx */
  function sign(parent, { cx, top, str = 'Relay' }) {
    const R = ROLE.sign, size = R.size;
    const tw = F.measure(str, FONT.display, size, 400), padX = 0.9 * size, padY = 0.7 * size, lead = 1.25 * size;
    const w = Math.round(tw + 2 * padX), hh = Math.round(2 * padY + lead), x = Math.round(cx - w / 2);
    const el = specimen({ x, y: top, w, h: hh, r: pick(4, 6) });
    el.append(text(str, { role: 'sign', x: (w - tw) / 2, baseline: padY + (lead - size) / 2 + F.baselineOffset(FONT.display, size, 400) }).el);
    parent.append(el);
    return { el, x0: x, x1: x + w, y0: top, y1: top + hh };
  }
  /**
   * The counter's clock: `NOW` (text-3) + ` 13:41:40 UTC` (text-2), Plex Mono 500 caps 0.08 em, every character in its own
   * monospace cell so the seconds change in place (final polish: a hard flip on the tick's frame, no cross-fade). o: { x, baseline, anchor:'end'|'start', start:'13:41:40',
   * ticks:[t] (one second each), enterAt, outAt (240 ms fades), window: [x0, y0, x1, y1] (r3: its bg readout window),
   * fade: fn(t) 0..1 (fix-r3: an extra opacity factor, e.g. a section's cover guard) }
   */
  function clock(parent, o = {}) {
    const g = GEO.relay.clock;
    const size = o.size ?? ROLE.clock.size, tr = 0.08;
    const adv = F.measure('0000000000', FONT.mono, size, 500, tr) / 10;
    const start = (o.start ?? '13:41:40').split(':').map(Number);
    const ticks = o.ticks ?? [];
    const str = (k) => { let s = start[0] * 3600 + start[1] * 60 + start[2] + k; const p2 = (n) => String(n).padStart(2, '0'); return `NOW ${p2(Math.floor(s / 3600) % 24)}:${p2(Math.floor(s / 60) % 60)}:${p2(s % 60)} UTC`; };
    const n = str(0).length, width = n * adv;
    const x0 = (o.anchor ?? 'end') === 'end' ? (o.x ?? g.x) - width : (o.x ?? g.x);
    const top = (o.baseline ?? g.base) - F.baselineOffset(FONT.mono, size, 500);
    const el = h('div', { style: `position:absolute;left:0;top:0;opacity:0` });
    if (o.window) { const [rx0, ry0, rx1, ry1] = o.window; el.append(h('div', { style: `position:absolute;left:${rx0}px;top:${ry0}px;width:${rx1 - rx0}px;height:${ry1 - ry0}px;background:${C.bg};border-radius:${BRADIUS}px` })); }
    const cells = Array.from({ length: n }, (_, i) => {
      const st = `position:absolute;left:${x0 + i * adv}px;top:${top}px;white-space:pre;font-family:${FONT.mono};font-size:${size}px;font-weight:500;line-height:${size}px;letter-spacing:${tr}em;color:${i < 3 ? C.text3 : C.text2}`;
      const a = h('div', { style: st }), b = h('div', { style: st + ';opacity:0' });
      el.append(a, b);
      return { a, b, ta: null, tb: null };
    });
    parent.append(el);
    const setText = (c, k, s) => { if (c[`t${k}`] !== s) { c[k].textContent = s; c[`t${k}`] = s; } };
    return {
      el, x0, x1: x0 + width,
      render(t) {
        // final polish (director, item 2): each second's digit flips hard on its tick frame, like ink (SCRIPT-v2 §3: clock
        // ticks are discrete events on whole frames); the 120 ms cross-fade drew two digits over each other for 7 frames.
        // inkOn: every blur sub-frame of a tick's frame shows the new digit
        let k = 0; for (const tk of ticks) if (inkOn(t, tk)) k++;
        const cur = str(k);
        cells.forEach((c, i) => { setText(c, 'a', cur[i]); css(c.a, { opacity: '1' }); css(c.b, { opacity: '0' }); });
        const a = (o.enterAt == null ? 1 : seg(t, o.enterAt, 0.24)) * (o.outAt == null ? 1 : 1 - seg(t, o.outAt, 0.24)) * (o.fade ? clamp(o.fade(t)) : 1);
        css(el, { opacity: op(a), display: a > 0 ? 'block' : 'none' });
      },
    };
  }
  /**
   * The relay: a counter in front of a sorting rack, built from the three ground steps (bg recesses, surface wall,
   * surface-2 counter) and the `Relay` sign. No icons, no clerk; final polish: a little other sealed mail in a few cells.
   * r3 (gate r2): construction, flat fills only. The rack is a surface-2 frame lip (radius 4) around one bg recess, crossed
   * by square-ended shelves (surface-2, 10 / 16 px deep: the floor of each row) over thinner dividers (surface, 6 / 7 px,
   * set back behind the shelves' front edge). The counter has a top lip (line, 8 px, 8 px proud of each end, radius 2) that
   * the envelope stands on, and the clock sits in an inset bg readout window (radius = the terminal blocks'), which is
   * there only while the clock runs (it enters and leaves with it).
   * o: { clock: clock() options | null }. Returns { el, wall, counter, sign, clock, holes, render(t) }.
   */
  function relay(parent, o = {}) {
    const g = GEO.relay;
    const el = h('div', { style: 'position:absolute;left:0;top:0' });
    parent.append(el);
    const [wx0, wy0, wx1, wy1] = g.wall, [cx0, cy0, cx1, cy1] = g.counter;
    const box = (x0, y0, x1, y1, fill, rad = '0') => h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;background:${fill};border-radius:${rad}` });
    // the wall stands behind the counter: its lower corners are hidden by the counter (the ledge reads as one straight edge)
    const wall = box(wx0, wy0, wx1, wy1 + RADIUS, C.surface, `${RADIUS}px`);
    el.append(wall);
    const { cols, rows: nr, box: [hx0, hy0, hx1, hy1], frame: fr, shelf: sh, divider: dv } = g.holes;
    const ix0 = hx0 + fr, iy0 = hy0 + fr, ix1 = hx1 - fr, iy1 = hy1 - fr;
    const cw = (ix1 - ix0 - (cols - 1) * dv) / cols, chh = (iy1 - iy0 - (nr - 1) * sh) / nr;
    // final polish (item 3): the frame lip, the recess, the dividers and the shelves (the rack's bars) on the anti-aliased
    // path (aaBox), so their long edges glide under the push instead of stepping at fixed columns
    el.append(aaBox(hx0, hy0, hx1, hy1, C.surface2, 4), aaBox(ix0, iy0, ix1, iy1, C.bg));
    for (let i = 1; i < cols; i++) { const x = ix0 + i * cw + (i - 1) * dv; el.append(aaBox(x, iy0, x + dv, iy1, C.surface)); }
    for (let j = 1; j < nr; j++) { const y = iy0 + j * chh + (j - 1) * sh; el.append(aaBox(ix0, y, ix1, y + sh, C.surface2)); }
    // final polish (director, item 9): without the caption the rack read as a dark window grid. A few cells hold other
    // sealed mail: the front edges of envelopes lying in the cell (strips 5 – 8 px, stacked with a 2 px gap) or one standing
    // at the back, seen front-on (taller, a step darker still). Flat paper tones a step darker than our envelope (the light
    // line token; the back ones mixed 18 % of the way to the shelf), so they recede; no text, no seal; each stands on
    // its cell's floor, clear of the cell's sides. Texture, not an object: GEO.mail [{ cell: [row, col], items:
    // [[x from the cell's left, width, height, tone 0 near | 1 back], …] (bottom up) }]. One group (`mail`), so a section
    // can hide it as one thing (sec2-scene.js: the 4:5 moves carry it past the caption, as they do the sign)
    const mail = h('div', { style: 'position:absolute;left:0;top:0' });
    const MAIL_TONE = [C2.crease, mix(C2.crease, C.surface2, 0.18)];
    let mailRect = null;
    for (const { cell: [j, i], items } of GEO.mail ?? []) {
      const cx = ix0 + i * (cw + dv), floor = iy0 + j * (chh + sh) + chh;
      let y = floor;
      for (const [dx, w, hh, tone] of items) {
        mail.append(aaBox(cx + dx, y - hh, cx + dx + w, y, MAIL_TONE[tone]));
        const r = [cx + dx, y - hh, cx + dx + w, y];
        mailRect = mailRect ? [Math.min(mailRect[0], r[0]), Math.min(mailRect[1], r[1]), Math.max(mailRect[2], r[2]), Math.max(mailRect[3], r[3])] : r;
        y -= hh + 2;
      }
    }
    el.append(mail);
    const sg = sign(el, { cx: (wx0 + wx1) / 2, top: g.signTop });
    const lip = g.lip;
    const counter = box(cx0, cy0 + lip.h, cx1, cy1, C.surface2, `0 0 ${RADIUS}px ${RADIUS}px`);
    el.append(counter, aaBox(cx0 - lip.over, cy0, cx1 + lip.over, cy0 + lip.h, C.line, 2));
    // the readout window belongs to the clock: it is lit (present) only while the clock runs, so the counter is a plain
    // counter at CAM-R+ (an empty black window there read as a dead display)
    const ck = o.clock ? clock(el, { window: g.window, ...o.clock }) : null;
    return { el, wall, counter, sign: sg, clock: ck, mail: { el: mail, rect: mailRect }, holes: { cw, ch: chh, inner: [ix0, iy0, ix1, iy1] }, render(t) { if (ck) ck.render(t); } };
  }

  // ================================================================ pointers and trails (§4.9)
  /**
   * The pointer: the brand's dotted underline (6 / 24 px in 16:9, 9 / 36 in 4:5, × k), the dot run centred under [x0, x1],
   * drawn left to right in 240 ms (each dot fades in over 60 ms), out over 120 ms from outAt. colour: accent ink on paper
   * (default), C.accent on the ground. o: { x0, x1, y (dot centres), at, outAt, k = 1, colour }
   */
  function pointer(svgParent, o) {
    const k = o.k ?? 1, pitch = 12 * F.Z * k, w = o.x1 - o.x0;
    const n = Math.floor(w / pitch + 1e-6) + 1, off = (w - (n - 1) * pitch) / 2;
    return ui.underline(svgParent, { x0: o.x0 + off, x1: o.x0 + off + (n - 1) * pitch + 0.01, y: o.y, at: o.at, dur: o.dur ?? 0.24, outAt: o.outAt, colour: o.colour ?? C2.accentInk, k });
  }
  /**
   * The pheromone trail along a lane, behind the travelling envelope: its head rides under the envelope's centre, it marches
   * one pitch per beat while the envelope moves (linear) and stops on arrival; the tail is erased linearly over `tail`.
   * o: { from:[x,y], to:[x,y], travel:[t0, t1], ease = CAM, tail:[t0, t1], k = 1, draw: fn(t) 0..1 (head along the path;
   *      default: from → to over the travel) }
   */
  function trail(svgParent, o) {
    const [t0, t1] = o.travel, ease = o.ease ?? CAM;
    const pitch = 12 * F.Z * (o.k ?? 1);
    const tr = ui.trail(svgParent, `M${o.from[0]} ${o.from[1]} L${o.to[0]} ${o.to[1]}`, {
      draw: o.draw ?? ((t) => seg(t, t0, t1 - t0, ease)),
      march: (t) => (pitch * (clamp(t, t0, t1) - t0)) / BEAT,
      tailFade: (t) => (o.tail ? clamp((t - o.tail[0]) / (o.tail[1] - o.tail[0])) : 0),
      fade: (t) => (t < t0 || (o.tail && t >= o.tail[1]) ? 0 : 1),
      colour: o.colour ?? C.accent, k: o.k ?? 1,
    });
    return tr;
  }

  // ================================================================ motion grammar (§4.10)
  // r3 (gate r2): paper hinges are physical moves, not UI: an in-out curve (FILM.CAM) over HINGE, so a panel visibly travels
  // through edge-on instead of snapping past 90° in 50 ms and creeping (the brand ease). Their free edges still peak at
  // 30 – 100 px a frame (16:9: the flap 33, the answer half 39, the brief half 76, the note half 105), so every hinge has a
  // blur window (BLUR.hinge / BLUR.flap). The flaps start earlier to keep their frames clear of the ink: the brief's flap
  // closes over frames 666 → 685 and its seal lands on 686 (G16); the answer's (16:9) over 2210 → 2228 after its letter
  // drops at 2202 → 2209, seal and form on 2229 (G52). 4:5 answer: closed already, no flap.
  const HINGE = 0.34;
  const FLAP = { brief: [666, 685], answer: [2210, 2228], answerDrop: 2202 };
  const motion = {
    /** paper slide / envelope travel: a → b over [at, at + dur], FILM.CAM */
    slide: (t, at, dur, a, b, ease = CAM) => lerp(a, b, seg(t, at, dur, ease)),
    /** the flap closing 180° → 0° between two frames (FLAP), FILM.CAM */
    flapClose: (t, f0, f1) => 180 * (1 - seg(t, f0 / FPS, (f1 - f0) / FPS, CAM)),
    /** a fold (0 → 1) or unfold (1 → 0) on its hinge, HINGE (340 ms), FILM.CAM */
    fold: (t, at, dur = HINGE) => seg(t, at, dur, CAM),
    unfold: (t, at, dur = HINGE) => 1 - seg(t, at, dur, CAM),
    /** the letter dropping the last px into the envelope (24 px, 120 ms). dirfix: FILM.CAM, a physical move that starts
     *  from rest and stops (the brand ease fell 7 then 9 px in its first two frames: it started at full speed) */
    drop: (t, at, k = 24) => k * seg(t, at, 0.12, CAM),
    /** 16:9 lift before a trip (−8 px, 120 ms) and settle in its last 120 ms (+8 px). dirfix: FILM.CAM (the brand ease
     *  jumped ~4 px on the lift's first frame and dropped 3.3 – 3.8 px on the settle's). settle = false: no settle, the
     *  paper arrives lifted (16:9 trip 2, which goes straight up into theo's slot: a settle there drew an 8 px V) */
    hop: (t, t0, t1, k = pick(8, 0), settle = true) => -k * seg(t, t0, 0.12, CAM) + (settle ? k * seg(t, t1 - 0.12, 0.12, CAM) : 0),
    /** UI fades */
    fadeIn: (t, at, dur = 0.24) => seg(t, at, dur),
    fadeOut: (t, at, dur = 0.12) => 1 - seg(t, at, dur),
  };
  /**
   * Blur windows [t0, t1] (script seconds; register with FILM.blur(t0, t1)) for the paper moves §4.10's table does not
   * cover (r3): every hinge (fold, unfold, flap) and theo's brief going back in (0.45 s, 36 / 32 px a frame at its peak).
   * None of them holds an ink event or a caption change (the seals land one frame after their flap window ends).
   */
  const BLUR = {
    hinge: (at, dur = HINGE) => [at, at + dur],
    flap: ([f0, f1]) => [f0 / FPS, f1 / FPS],
    slide: (at, k) => [at, at + SLIDE[k].dur],
  };
  /**
   * fix-r3: a blur window for the 30 fps delivery only (render.mjs --fps 30; [t0, t1, 30] in CUES.blur). The rule is the
   * one above, at the delivery's own rate: a move gets a blur window where its step passes 19 px a frame. At 60 fps the
   * scene's paper slides and moves 1 and 4 peak at 15 – 18 px (no blur, as reviewed); at 30 fps the same moves step 27 –
   * 37 px and strobe, so they are blurred there (1/60 s, 180° at 30 fps). Same exclusions: never a 21° edge, a caption's
   * entrance, a cut. Measured with qa/dirfix/fix-r3/tools/scan30.mjs.
   */
  const blur30 = (t0, t1) => F.blurWindows.push([+t0.toFixed(4), +t1.toFixed(4), 30]);

  // ================================================================ cameras (§5.2.3)
  // the push to CAM-R+ (move 2) and back (move 3): world point P (the form's centre) goes from its R0 screen place to Q1
  // while s goes 1 → s. r2: 4:5 is a real push too (s 1.2 with the form's centre moved down to 753, so the envelope fills
  // the width, 120 → 960, and its bottom comes to 880; theo's chip fades for it, as both chips do in 16:9)
  // r3: P is the form's centre at rest on the counter, from the geometry (16:9 (960, 666.5), 4:5 (540, 661)); 4:5 Q1 keeps
  // the envelope's bottom at screen 880 with the 400 px envelope
  const FORM_C = (() => { const [fx, fy, fw, fh] = ENV.scene.form, [rx, ry] = GEO.relay.rest; return [rx + fx + fw / 2, ry + fy + fh / 2]; })();
  const PUSH = pick({ P: FORM_C, Q1: [960, 540], s: 1.3 }, { P: FORM_C, Q1: [540, Math.round(880 - (778 - FORM_C[1]) * 1.2)], s: 1.2 });
  const Rp = { x: PUSH.P[0] - (PUSH.Q1[0] - FMT.W / 2) / PUSH.s, y: PUSH.P[1] - (PUSH.Q1[1] - FMT.H / 2) / PUSH.s, s: PUSH.s };
  const CAMS = pick({ M: { x: -320, y: 540, s: 1 }, R0: { x: 960, y: 540, s: 1 }, Rp, T: { x: 2240, y: 540, s: 1 } },
    // final polish (director, item 5): 4:5 CAM-T 18 px lower in the world (c.y 1463, was 1445). At 1445 the counter's lip
    // stood on the frame's top edge with an 8 px strip of sky above it for ~7 s (30.7 – 37.5 s); now the lip is 10 px
    // above the frame and the counter bleeds off the top (66 px of it show). Chip, sheet and terminal keep their spacing
    // (world); the caption guard is computed from this camera
    { M: { x: 540, y: -453 + MY, s: 1 }, R0: { x: 540, y: 675, s: 1 }, Rp, T: { x: 540, y: 1463, s: 1 } });
  const cam = {
    STATES: CAMS,
    hold: (A) => () => ({ ...A }),
    /** c and s straight from A to B, FILM.CAM (trucks and tilts; the 16:9 truck is locked to an envelope with the same curve) */
    tween: (A, B, t0, dur, ease = CAM) => (t) => { const e = seg(t, t0, dur, ease); return { x: lerp(A.x, B.x, e), y: lerp(A.y, B.y, e), s: lerp(A.s, B.s, e) }; },
    PUSH,
    /** move 2, the push CAM-R0 → CAM-R+: the form's centre goes to its R+ screen place as s 1 → 1.3 (16:9) / 1.2 (4:5) */
    pushRp: (t0, dur) => F.push({ P: PUSH.P, Q0: PUSH.P, Q1: PUSH.Q1, s0: 1, s1: PUSH.s, t0, dur, ease: CAM }),
    /** move 3, the settle CAM-R+ → CAM-R0 (the exact reverse path) */
    settleR0: (t0, dur) => F.push({ P: PUSH.P, Q0: PUSH.Q1, Q1: PUSH.P, s0: PUSH.s, s1: 1, t0, dur, ease: CAM }),
    /** a slow drift s0 → s1 about a world point that stays put on screen (from a camera at rest c = frame centre) */
    drift: (about, s0, s1, t0, t1, ease = CAM) => F.drift({ t0, t1, s0, s1, about, ease }),
    /**
     * move 5, the return: the envelope from theo's dock to maya's (16:9 along x; 4:5 along y) held at its screen point, with
     * the dip s = 1 − 0.3 sin(π e) on the move's own eased progress e = FILM.CAM(τ) (the envelope's travel), so the camera
     * leaves CAM-T and lands on CAM-M at rest (dir-v2; rev-sec2 r1: the script's sin(πτ) on linear τ started and stopped
     * the frame edges at 15 px a frame). Same states, same path, same blur window.
     */
    ret: (t0, t1) => (t) => {
      const tau = clamp((t - t0) / (t1 - t0)), e = CAM(tau), sc = 1 - 0.3 * Math.sin(Math.PI * e);
      if (!P) { const xe = lerp(GEO.theo.dock[0], GEO.maya.dock[0], e); return { x: xe, y: 583 - 43 / sc, s: sc }; }
      const ye = lerp(GEO.theo.dock[1], GEO.maya.dock[1], e);
      const ys = lerp(675 + GEO.theo.dock[1] - CAMS.T.y, 675 + GEO.maya.dock[1] - CAMS.M.y, e);   // 360 → 742
      return { x: 540, y: ye - (ys - 675) / sc, s: sc };
    },
    seq: F.camSeq,
    /**
     * The scene's whole camera path, G8 → G56 (§5.2.3): CAM-M, move 1 (truck / tilt to R0), move 2 (push to R+), move 3
     * (settle), the wait, move 4 (to CAM-T), move 5 (the return with its dip), CAM-M. Every move FILM.CAM. One source for
     * the section file and the QA tools (qa/v2-paper-sky).
     * dirfix: the wait holds CAM-R0 in both formats (was: 16:9 drift s 1 → 1.02 about the envelope, 4:5 tilt c y 675 → 699).
     * At 0.05 px a frame the drift and the tilt were invisible as motion but not as artefacts: text and chips simmered
     * (glyphs re-rasterised ±0.1 – 0.4 px a frame under the changing scale), the 4:5 tilt moved every horizontal edge in
     * 0.4 – 0.55 px ticks about every 12 frames, and the sky (following at 0.3) re-filtered its stars, so they breathed
     * and popped. Held, the wait is still and the clock, maya's pulses and the caption carry it; the sky holds with it.
     */
    scene() {
      const S = CAMS;
      const wait = cam.hold(S.R0);
      return F.camSeq([[0, cam.hold(S.M)], [T(16.75), cam.tween(S.M, S.R0, T(16.75), T(20) - T(16.75))],
        [T(20.25), cam.pushRp(T(20.25), T(21.75) - T(20.25))], [T(29), cam.settleR0(T(29), T(31) - T(29))], [T(32), wait],
        [T(40.25), cam.tween(wait(T(40)), S.T, T(40.25), T(43) - T(40.25))], [T(52.5), cam.ret(T(52.5), T(55))]]);
    },
  };

  // ================================================================ layers, skies
  /**
   * Sky by place: 'scene' (v2 seed 97; flat bg until SKY_READY), 'inv' (card + invitation), 'end'. render(cam).
   * r3 (gate r2): the scene and inv skies are v2 copies with the stars within ~10 px of the paper rest edges and along the
   * trail lanes left out (qa/v2-paper-sky/run.sh, from assets/sky-v2scene-*.png and v1's sky-inv-*.png, which stay as they
   * are; re-run it when those skies or the v2 geometry change). Same sizes and follow as their sources.
   * dirfix: the film draws display-resolution copies (`*-1x.png`, Lanczos from the cleared 2x PNGs by run.sh / onex.py):
   * a 2x PNG drawn at half size is mip-mapped by Chromium with a filter chosen from its current scale, so a following
   * sky's stars changed in single frames (pops, twinkle, breathing). 'inv' in 16:9 is the repaired copy (sky-v2inv2,
   * b1345tools/inv-sky.sh), as sections III and IV draw it.
   */
  function sky(parent, place) {
    if (place === 'scene') {
      if (!SKY_READY) return { el: null, render() {} };
      return ui.sky(parent, { name: `sky-v2scene-clear-${P ? '45' : '169'}-1x.png`, w: pick(3400, 1500), h: pick(1500, 2900), follow: 0.3 });
    }
    if (place === 'inv') return ui.sky(parent, { name: pick('sky-v2inv2-169-1x.png', 'sky-v2inv-45-1x.png'), w: pick(2016, 1134), h: pick(1134, 1418), follow: 0.3 });
    return F.S.sky(parent, place);
  }
  /**
   * A shot's layers: ground → sky → world (camera-transformed with worldCamStable) → screen. apply(cam) moves world and sky.
   * Returns { world, screen, wsvg (full-frame SVG in world coords, for trails), ssvg (screen SVG), apply(cam) }.
   */
  function layers(root, { place = 'scene', about } = {}) {
    css(root, { background: C.bg });
    const sk = place ? sky(root, place) : null;
    const world = h('div', { style: 'position:absolute;left:0;top:0' });
    root.append(world);
    const apply = F.worldCamStable(world, about);
    const screen = h('div', { style: `position:absolute;left:0;top:0;width:${FMT.W}px;height:${FMT.H}px` });
    root.append(screen);
    const mk = (parent) => { const s = svgEl('svg', { width: FMT.W, height: FMT.H, viewBox: `0 0 ${FMT.W} ${FMT.H}`, style: 'position:absolute;left:0;top:0;overflow:visible' }); parent.append(s); return s; };
    return { world, screen, svg: mk, sky: sk, apply(c) { apply(c); sk?.render(c); } };
  }

  // ================================================================ the caption slot (§4.3)
  /**
   * One caption slot: centred, bottom-anchored (one line on the lower baseline, two lines on both). The change grammar: the
   * old line fades out over 120 ms ending at the next one's `at`; the new one enters at `at` over 240 ms (opacity + rise
   * 12 / 18 px), brand ease. Never two captions at once.
   * items: [{ at, lines: [string | [{text, colour (string | fn(t))}]], build: [{text, at}] (a building line: spans laid out
   *   once, centred, each fading in over 120 ms on its beat, in place), out (explicit end, else the next item's at) }]
   * Returns { el, render(t) }. Put the slot's layer above everything it must bridge (S-1 crosses the 21° cut).
   */
  /**
   * dirfix: screen text on the same non-axis-aligned raster path the world uses (worldCamStable's 0.05° roll), about a
   * point (the caption slot's centre line). Under a plain translate Skia snaps each text line's baseline to whole pixels,
   * so the 12 / 18 px entrance rise moved in 1 px ticks with holds, its last tick ~9 frames in, at ~95 % opacity. The
   * roll is permanent (never only during the rise), so the end of the rise does not switch raster modes. 0.05° moves a
   * caption's ends by at most ±0.65 px: invisible. css: the CSS to add to the holder's style.
   */
  const stableText = (ax, ay) => `transform-origin:${ax}px ${ay}px;transform:rotate(0.05deg)`;
  function captionSlot(parent, { items, z = 30 }) {
    const g = GEO.caption;
    const layer = h('div', { style: `position:absolute;left:0;top:0;width:${FMT.W}px;height:${FMT.H}px;z-index:${z};pointer-events:none;${stableText(g.cx, g.one[0])}` });
    parent.append(layer);
    const list = items.slice().sort((a, b) => a.at - b.at);
    const built = list.map((it, i) => {
      const n = it.lines.length + (it.build ? 1 : 0);
      const bases = n === 1 ? g.one : g.two;
      const box = h('div', { style: 'position:absolute;left:0;top:0;opacity:0' });
      const fns = [];
      // final polish (director, item 12): a caption with a building line is set as one flush-left block, centred in the
      // slot by its widest line (the building line's final layout, reserved from the start, as before). Each word still
      // appears in its final place (no reflow), and the first word lands right under line 1's first letter. With line 1
      // centred on its own, `who,` appeared ~115 px left of `It reads …` (its visible part ~370 px left of line 1's centre)
      // and read as misaligned on the analogy's key beat (S-4, 15.71 – 17.86 s)
      const lw = (ln) => text(typeof ln === 'string' ? ln : '', { role: 'caption', x: 0, baseline: 0, spans: typeof ln === 'string' ? null : ln }).width;
      const blockX = it.build ? g.cx - Math.max(...it.lines.map(lw), lw(it.build.map((b) => ({ text: b.text })))) / 2 : null;
      it.lines.forEach((ln, k) => {
        const spans = typeof ln === 'string' ? null : ln;
        const tx = text(typeof ln === 'string' ? ln : '', { role: 'caption', x: blockX ?? g.cx, baseline: bases[k], anchor: blockX != null ? 'start' : 'middle', spans });
        box.append(tx.el);
        if (spans) spans.forEach((sp, j) => { if (typeof sp.colour === 'function') fns.push([tx.spans[j], sp.colour]); });
      });
      let buildLine = null;
      if (it.build) {
        const tx = text('', { role: 'caption', x: g.cx, baseline: bases[n - 1], anchor: 'middle', spans: it.build.map((b) => ({ text: b.text })) });
        tx.spans.forEach((sp) => css(sp, { opacity: '0' }));
        buildLine = { el: tx.el, spans: tx.spans };
        layer.append(tx.el);
        css(tx.el, { opacity: '0' });
      }
      layer.append(box);
      return { it, box, fns, buildLine, out: it.out ?? list[i + 1]?.at ?? Infinity };
    });
    return {
      el: layer,
      render(t) {
        for (const b of built) {
          const on = t >= b.it.at && t < b.out;
          const aIn = seg(t, b.it.at, 0.24), aOut = 1 - seg(t, b.out - 0.12, 0.12);
          const a = on ? Math.min(aIn, aOut) : 0;
          const dy = g.rise * (1 - aIn);
          css(b.box, { opacity: op(a), transform: dy > 0.001 && on ? `translateY(${dy.toFixed(3)}px)` : 'none', display: a > 0 ? 'block' : 'none' });
          for (const [sp, fn] of b.fns) css(sp, { color: fn(t) });
          if (b.buildLine) {
            css(b.buildLine.el, { opacity: op(on ? aOut : 0), display: on ? 'block' : 'none' });
            b.it.build.forEach((s, j) => css(b.buildLine.spans[j], { opacity: op(seg(t, s.at, 0.12)) }));
          }
        }
      },
    };
  }

  // ================================================================ the card (§5.3)
  /** A line of copy broken onto two lines after `after` (a layout break only: the words are the copy's, verbatim) */
  function breakAfter(str, after) {
    const i = str.indexOf(after);
    if (i < 0) throw new Error(`card break: '${after}' not in '${str}'`);
    const a = str.slice(0, i + after.length), b = str.slice(i + after.length).replace(/^ /, '');
    if (`${a} ${b}` !== str) throw new Error(`card break changed the copy: '${str}'`);
    return [a, b];
  }
  /**
   * 4:5 card layout (dirfix): laid out for portrait instead of the landscape strip scaled down (952 × 342 in a 1080 × 1350
   * frame, ~500 px of empty sky above and below it, lines at 40 px). Display line 72 px; lines 2 and 3 at 52 px, each
   * broken onto two lines (no copy change; final polish, director item 13: at the phrase, `Requests wait while` / `your
   * teammate is offline.`, `You approve what` / `their agent can read.`, was after `your` / `their`). Each statement is a paragraph: CARD45.lead between its lines, CARD45.para between them,
   * CARD45.drop from the display line to the first. The card keeps x 64 → 1016, radius 24, its 10 px inset and 2 px
   * line, equal air (CARD45.air) from the inset hairline to the display line's cap tops and from the last line's
   * descenders to the hairline, and is centred in the frame. The footnote (O-1, gallery only) is one more paragraph.
   */
  const CARD45 = { air: 100, drop: 118, lead: 66, para: 104, footPara: 96, x: 112 };
  /**
   * The card: label stock with an inset hairline (ink 55 %), the display line in Special Elite and two Plex Sans lines, plus
   * the footnote when SHOW_REVIEW_FOOTNOTE (O-1). Lines are ink: one frame each. Screen px.
   * r3 (gate r2): the inset scales with the card, as a specimen label's does (16:9 12 px in, 2 px line; 4:5 10 px, 2 px;
   * the 6 px inset of a 158 px form read as a UI double border at 1536 × 580).
   * o: { at: [t display, t line 2, t line 3, t footnote], footnote (default SHOW_REVIEW_FOOTNOTE; the gallery shows both),
   *      tooth }. Returns { el, box, bases, render(t) }.
   */
  function card(parent, o = {}) {
    const foot = o.footnote ?? SHOW_REVIEW_FOOTNOTE;
    // rows: [lines, role]; one ink time per row (o.at[i]): a broken statement inks whole, both lines on one frame
    const rowsK = P
      ? [[[COPY.K1], 'cardDisplay'], [breakAfter(COPY.K2, 'while'), 'cardLine'], [breakAfter(COPY.K3, 'what'), 'cardLine'], ...(foot ? [[[COPY.K4], 'cardFoot']] : [])]
      : [[[COPY.K1], 'cardDisplay'], [[COPY.K2], 'cardLine'], [[COPY.K3], 'cardLine'], ...(foot ? [[[COPY.K4], 'cardFoot']] : [])];
    const descOf = (str, role) => { const R = ROLE[role], cx = document.createElement('canvas').getContext('2d'); cx.font = `${R.weight} ${R.size}px ${FAM[R.face]}`; return cx.measureText(str).actualBoundingBoxDescent; };
    let g;
    if (P) {
      // every line's baseline from the display line's (0), then the box from the measured cap top and last descender
      const K = CARD45, r = 24, inset = 10, line = 2, edge = inset + line;
      const capD = capHeight(FAM.display, ROLE.cardDisplay.size, ROLE.cardDisplay.weight);
      const rel = [[0]];
      let b = 0;
      rowsK.slice(1).forEach(([lines, role], i) => {
        b += role === 'cardFoot' ? K.footPara : i === 0 ? K.drop : K.para;
        const ls = lines.map((_, j) => b + j * K.lead); b = ls[ls.length - 1]; rel.push(ls);
      });
      const [lastLines, lastRole] = rowsK[rowsK.length - 1];
      const bottomRel = b + descOf(lastLines[lastLines.length - 1], lastRole);
      const hgt = Math.round(edge + K.air + capD + bottomRel + K.air + edge);
      const y0 = Math.round(FMT.H / 2 - hgt / 2), b0 = y0 + edge + K.air + capD;
      g = { box: [64, y0, 1016, y0 + hgt], r, inset, line, x: K.x, bases: rel.map((ls) => ls.map((v) => b0 + v)) };
    } else {
      g = { box: [192, 250, 1728, 830], r: 16, inset: 12, line: 2, x: 288, bases: [[392], [522], [614], [740]] };
      if (!foot) {
        // without the footnote the card ends under line 3, and is centred again in the frame (otherwise ~200 px of empty
        // card hang under line 3). The air from line 3's deepest descender to the inset hairline is the air it had under
        // the footnote, but never more than the air from the hairline to the display line's cap tops (67 above, 62 below)
        const [bx0, by0, bx1, by1] = g.box, edge = g.inset + g.line;
        const above = g.bases[0][0] - capHeight(FAM.display, ROLE.cardDisplay.size, ROLE.cardDisplay.weight) - (by0 + edge);
        const desc = descOf(COPY.K3, 'cardLine');
        const below = Math.min(by1 - g.bases[3][0] - edge - desc, above);
        const bottom = Math.round(g.bases[2][0] + desc + below + edge);
        const dy = Math.round(FMT.H / 2 - (by0 + bottom) / 2);
        g.box = [bx0, by0 + dy, bx1, bottom + dy]; g.bases = g.bases.slice(0, 3).map((ls) => ls.map((v) => v + dy));
      }
    }
    const [x0, y0, x1, y1] = g.box;
    const el = specimen({ x: x0, y: y0, w: x1 - x0, h: y1 - y0, r: g.r, inset: g.inset, line: g.line, tooth: o.tooth });
    const inks = rowsK.map(([lines, role], i) => {
      const els = lines.map((str, j) => { const tx = text(str, { role, x: g.x - x0, baseline: g.bases[i][j] - y0 }); el.append(tx.el); return tx.el; });
      return { els, at: o.at?.[i] };
    });
    parent.append(el);
    return { el, box: g.box, bases: g.bases, render(t) { for (const k of inks) for (const e of k.els) css(e, { visibility: inkOn(t, k.at) ? 'visible' : 'hidden' }); } };
  }

  // ================================================================ end stage (copied from v1 shots/s11.js, F.endStage)
  /**
   * The end stage: the end sky and the boot lockup at its script position (§5.5). Boot container (viewBox −566.59 −90 1133.19
   * 1042.27): 16:9 left 552.25, top 85.23, width 815.5; 4:5 left 105.07, top 160.91, width 869.9. Returns { sky, box, boot }
   * (boot.render(t) on the boot's own clock). Copied, not loaded: s11.js also registers v1's S11.
   */
  async function endStage(root) {
    root.style.background = C.bg;
    const sk = F.S.sky(root, 'end');
    sk.render({ x: FMT.W / 2, y: FMT.H / 2, s: 1 });
    const G = P ? { left: 105.07, top: 160.91, width: 869.9 } : { left: 552.25, top: 85.23, width: 815.5 };
    const box = h('div', { style: `position:absolute;left:${G.left}px;top:${G.top}px;width:${G.width}px;line-height:0` });
    root.append(box);
    const boot = await F.boot(box, { width: G.width });
    // each inlined copy of boot.svg gets its own clipPath ids (an id must be unique in the page)
    const sfx = `-${root.dataset.id ?? 'end'}`;
    const ids = [...box.querySelectorAll('[id]')].map((el) => { const old = el.id; el.id = old + sfx; return old; });
    for (const el of box.querySelectorAll('*')) {
      for (const a of [...el.attributes]) {
        if (!a.value.includes('url(#')) continue;
        const v = a.value.replace(/url\(#([^)]+)\)/g, (m, id) => (ids.includes(id) ? `url(#${id}${sfx})` : m));
        if (v !== a.value) el.setAttribute(a.name, v);
      }
    }
    return { sky: sk, box, boot };
  }

  F.V2 = {
    SIGNATURE, SHOW_REVIEW_FOOTNOTE, SKY_READY, PAPER_TOOTH, NOTE_LETTERHEAD,
    P, pick, val, BEAT, BAR, FPS, T, Gof, frameOf, snap, inkOn, barBeat, beats,
    C2, mix, colourShift, ROLE, RADIUS, BRADIUS, HAIR, COPY, TERM, ADDR, GEO, ENV, SHEETS, SLIDE, HINGE, FLAP, BLUR, paperTone,
    text, capHeight, specimen, mark, hline, aaBox, slotClip, restPos, stableText,
    envelope, sealed, paper, rows, machine, chip, sign, clock, relay, pointer, trail, motion, cam, sky, layers, captionSlot, card, endStage,
    blur30,
  };
})();
