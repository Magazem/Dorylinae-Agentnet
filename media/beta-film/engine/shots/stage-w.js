// Stage W (SCRIPT.md §3.5): the two machines and the relay, shared by S2, S5 and S8.
// One layout per format, built in world coordinates (camera s = 1 and c = frame centre: world = screen).
//
//   const W = FILM.stageW(root, cfg)  ->  { world, screen, svg, groups, plates, prompts, names, dots, relayDot,
//                                           holdRing, relayLabel, trail, log, cam, render(tAbs) }
//
// cfg (all times absolute seconds, as in the script):
//   lock:       { maya: at, theo: at }            plate groups lock in (plate + prompt + name + dot), else present
//   relayDotIn: at                                 relay dot fades in 240 ms, else present
//   level:      { maya: (t)=>0..1, theo: (t)=>0..1 } presence (0 hollow ring, 1 solid online)
//   pulses / arrivals: { maya: [t], theo: [t] }
//   relayLabel: null | { typedAt } | { present: true }   (card locks from y −8 over 240 ms while it types)
//   holdRing:   null | { in, out }                 (enters 240 ms, leaves 120 ms)
//   trail:      { dir: +1 maya→theo | −1 theo→maya, head:(t)=>l px, march:(t)=>px, tail?:(t)=>l px,
//                 style?:(t, seg 'A'|'B') => { colour, alpha } }
//   log:        null | true (S5: the relay-log plate; 16:9 world text, 4:5 strip; drive with W.log.*)
//   plateBlur:  null | (t)=>screen px (S5 16:9 push)
//   cam:        (t)=>{x, y, s}
//   onWorld:    null | (t)=>void   the shot's own updates to world elements (prompts, log), run with every (sub-)frame
//   shutter:    null | (t)=>0..1   sub-frame motion blur on the world, as a fraction of a 180° shutter (S5 16:9 push)
(() => {
  const F = window.FILM;
  const { C, FMT, FONT, EASE, clamp, lerp, seg, h, s, css, op, ui } = F;
  const P = FMT.portrait;

  // ------------------------------------------------------------------ geometry (SCRIPT.md §3.4, §3.5)
  // 4:5 plates and prompts sit 28 px right of §3.5 (plates x 808 → 1328, prompts x 856) so that the 60 px captions
  // (ending at x ~759) keep ≥ 48 px of clear space from the plate edge. Dots, names, relay and trail are as §3.5.
  const G = P ? {
    maya: { plate: [808, -200, 1328, 360], prompt: [856, 320], dot: [940, 392], name: { x: 914, base: 403, anchor: 'end' } },
    theo: { plate: [808, 1160, 1328, 1550], prompt: [856, 1214], dot: [940, 1128], name: { x: 914, base: 1139, anchor: 'end' } },
    relay: [940, 760], dotD: 18, relayD: 15, stroke: 3, promptSize: 32, nameSize: 32, radius: 24,
    // the trail runs along y at x 940: segment A 404 → 748, segment B 772 → 1116
    axis: 'y', across: 940, a0: 404, a1: 1116, gap: 12, pitch: 36, r: 4.5, phase: 6,
    relayLabel: { size: 42, right: 912, top: 704.4 }, hold: { r: 21, w: 1.5 },
    log: { plate: [-24, 568, 920, 692], base: 610, size: 36, pitch: 54, adv: 21.6 },
  } : {
    maya: { plate: [-240, 470, 540, 580], prompt: [96, 535], dot: [568, 525], name: { x: 552, base: 503, anchor: 'start' } },
    theo: { plate: [1380, 470, 2160, 580], prompt: [1408, 535], dot: [1352, 525], name: { x: 1368, base: 503, anchor: 'end' } },
    relay: [960, 525], dotD: 12, relayD: 10, stroke: 2, promptSize: 28, nameSize: 28, radius: 16,
    // lattice phase +8: dots at 588 … 1332, 20 px from each presence dot and symmetric about the relay (924 | 996)
    axis: 'x', across: 525, a0: 580, a1: 1340, gap: 12, pitch: 24, r: 3, phase: 8,
    // relay label 8 px above §3.5 (card y 424.8 → 499.0): 12 px clear of the holding ring at s 1, 24 px at the push
    relayLabel: { size: 28, centre: 960, top: 424.8 }, hold: { r: 14, w: 1 },
    log: { plate: [380, 640, 1600, 698], x: 396.55, baseA: 660, baseC: 682, size: 14, tracking: -0.0125 },
  };

  // The relay's own log (capture/25-relay-log.txt; m-b2d3dc4566beac6d9631949d221555c0 shortened to m-b2d3…).
  const LOG_A = 'time=2026-10-08T13:41:40.228Z level=INFO msg=queued event=queue from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444';
  const LOG_C = 'time=2026-10-08T13:41:45.886Z level=INFO msg="delivered from queue" event=queue_flush peer=ZhbF0Hsy count=1';

  const hex = (c) => [parseInt(c.slice(1, 3), 16), parseInt(c.slice(3, 5), 16), parseInt(c.slice(5, 7), 16)];
  /** colour between two tokens (p 0..1) as rgb() */
  function mix(a, b, p) {
    if (p <= 0) return a; if (p >= 1) return b;
    const A = hex(a), B = hex(b);
    return `rgb(${A.map((v, i) => Math.round(lerp(v, B[i], p))).join(',')})`;
  }

  // ------------------------------------------------------------------ specimen card (SCRIPT.md §3.2)
  /** a typed card's stock reaches full opacity this long after its lock-in starts (the y −8 move stays 240 ms) */
  const CARD_IN = 0.08;
  /**
   * Specimen label on label stock: Special Elite, ink, inset hairline 1 px at 55 % ink, radius 4 (16:9) / 6 (4:5),
   * padding 0.9 em × 0.7 em, line-height 1.25 em (the box; the text is centred on its cap height, see below).
   * Text is typed at 85 ms per letter (jitter 0) or present.
   * o: { size, x, anchor:'start'|'middle'|'end', top, typedAt }  ->  { el, x0, x1, y0, y1, w, hgt, times, render(t) }
   */
  function card(str, o) {
    const fs = o.size, padX = 0.9 * fs, padY = 0.7 * fs, lh = 1.25 * fs;
    const tw = F.measure(str, FONT.display, fs);
    const w = tw + 2 * padX, hgt = lh + 2 * padY;
    const anchor = o.anchor ?? 'start';
    const x0 = anchor === 'middle' ? o.x - w / 2 : anchor === 'end' ? o.x - w : o.x;
    const el = h('div', { style: `position:absolute;left:0;top:0;width:${w.toFixed(2)}px;height:${hgt.toFixed(2)}px;background:${C.stock};border-radius:${P ? 6 : 4}px;color:${C.ink};font-family:${FONT.display};font-size:${fs}px;line-height:${lh}px;white-space:pre;transform:translate(${x0.toFixed(2)}px,${o.top.toFixed(2)}px)` });
    const inset = fs * 0.0893;     // the brand label's inset (1.25 brand px on a 14 px specimen)
    el.append(h('div', { style: `position:absolute;left:${inset.toFixed(2)}px;top:${inset.toFixed(2)}px;right:${inset.toFixed(2)}px;bottom:${inset.toFixed(2)}px;border:1px solid ${C.ink};opacity:0.55;border-radius:${P ? 3 : 2}px` }));
    // the text is centred by its cap block (Special Elite cap height 0.689 em), not by padY: a label without
    // descenders (`When`, `Who`) otherwise sits 5 px high, and the cards in a row would not share a baseline.
    // Baseline in the card = (hgt + capH) / 2; the line box (line-height lh) puts it half-leading + offset below its top.
    const capH = 0.689 * fs, base = (hgt + capH) / 2;
    const txtTop = base - (lh - fs) / 2 - F.baselineOffset(FONT.display, fs);
    const txt = h('div', { style: `position:absolute;left:${padX.toFixed(2)}px;top:${txtTop.toFixed(2)}px` });
    el.append(txt);
    let ty = null;
    if (o.typedAt != null) { ty = ui.typed(str, { at: o.typedAt, step: 0.085, jitter: 0 }); txt.append(ty.el); } else txt.textContent = str;
    return { el, x0, x1: x0 + w, y0: o.top, y1: o.top + hgt, w, hgt, tw, times: ty ? ty.times : [], end: ty ? ty.end : null, render(t) { if (ty) ty.render(t); } };
  }

  // ------------------------------------------------------------------ the trail
  /**
   * Dots of 3 brand px every 12 brand px along Stage W's line (one lattice through both segments), drawn by a head,
   * marching, erased by a tail. Dots within 12 px of the relay dot centre are never drawn.
   * l is the distance along the line from the trail's start (maya's end when dir = +1, theo's when dir = −1).
   * Edges are soft over one dot diameter so that marching dots never pop. The trail layer sits under the presence
   * dots, so dots emerge from under maya's (or theo's) dot and sink into the other.
   * 4:5 lattice phase: +6 px, so that the first dot keeps the 16:9 clearance from the 18 px presence dot
   * (1.5 brand px) instead of touching it (12 px from centre < 9 + 4.5).
   * While the holding ring is visible (cfg.ring(t) = its opacity), dots closer to the relay than the ring's outer edge
   * plus a dot and 6 px fade with it, so no paused dot touches the ring (4:5: the dot at y 734 would overlap it).
   */
  function trail(svg, cfg) {
    const { pitch, r, a0, a1, gap } = G;
    const len = a1 - a0, fd = 2 * r;                       // fade distance: one dot diameter
    const start = cfg.dir > 0 ? a0 : a1, rel = G.relay[G.axis === 'x' ? 0 : 1];
    const clear = G.hold.r + G.hold.w + r + 6;            // ring clearance from the relay centre
    const n = Math.ceil(len / pitch) + 3;
    const g = s('g');
    svg.append(g);
    const dots = Array.from({ length: n }, () => { const c = s('circle', { r: r.toFixed(2), visibility: 'hidden' }); g.append(c); return c; });
    const set = (c, k, v) => { if (c['__' + k] !== v) { c.setAttribute(k, v); c['__' + k] = v; } };
    return {
      el: g, len,
      render(t) {
        const head = cfg.head(t), m = cfg.march ? cfg.march(t) : 0, tail = cfg.tail ? cfg.tail(t) : -1e9;
        const mm = ((m % pitch) + pitch) % pitch;
        const stA = cfg.style ? cfg.style(t, 'A') : null, stB = cfg.style ? cfg.style(t, 'B') : null;
        const ro = cfg.ring ? cfg.ring(t) : 0;
        for (let i = 0; i < n; i++) {
          const l = (i - 1) * pitch + mm + G.phase;
          const pos = start + cfg.dir * l;
          let a = clamp(l / fd + 1) * clamp((head - l) / fd + 1) * clamp((l - tail) / fd) * clamp((len - l) / fd + 1)
            * clamp((Math.abs(pos - rel) - gap) / fd);
          if (ro > 0) a *= 1 - ro * (1 - clamp((Math.abs(pos - rel) - clear) / fd));
          const c = dots[i];
          if (a <= 0.002) { set(c, 'visibility', 'hidden'); continue; }
          const st = (pos < rel ? stA : stB) ?? { colour: C.accent, alpha: 1 };
          a *= st.alpha;
          set(c, 'visibility', 'visible');
          set(c, G.axis === 'x' ? 'cx' : 'cy', pos.toFixed(2));
          set(c, G.axis === 'x' ? 'cy' : 'cx', String(G.across));
          set(c, 'fill', st.colour);
          set(c, 'opacity', op(a));
        }
      },
    };
  }
  /** march distance helper: one pitch per beat from t0; if t1 is given the march stops there, easing to rest over
   *  [t1 − 0.06, t1 + 0.06] (the "120 ms" stop) so the total travel is exactly (t1 − t0) beats. */
  function flow(t, t0, t1 = null, d = 0.06) {
    if (t <= t0) return 0;
    if (t1 == null || t <= t1 - d) return t - t0;
    if (t <= t1 + d) { const u = t - (t1 - d); return (t1 - d - t0) + u - (u * u) / (4 * d); }
    return t1 - t0;
  }

  // ------------------------------------------------------------------ quiet sky regions
  /** Bands of the sky PNG (2× display px: 16:9 4992 × 2808, 4:5 2268 × 2836) that each quiet region can ever cover
   *  over S2 (s 1 → 1.03), S5 (16:9 s 1 → 2, c.y 540 → 525; 4:5 still) and S8 (s 1 → 1.02) at parallax 0.3, plus the
   *  filter's reach (2 · 28 PNG px) on each side: [col0, col1, row0, row1]. 4:5 columns are in the mirrored sky's own
   *  (unmirrored PNG) coordinates. */
  const QUIET_SRC = P
    ? { lane: [40, 608, 616, 2568] }                                  // display cols ~73 → 251, rows ~359 → 1233
    : { lane: [0, 4992, 1040, 1800], caption: [464, 3184, 236, 1360] }; // lane rows ~566 → 824; caption cols 288 → 1536, rows 174 → 624
  /**
   * Star suppression by size, not brightness (the dust and the star glows are equally bright). Radii in PNG px.
   * Two morphological openings (erode, then dilate): oL (radius 28) keeps only the nebula; oS (radius 4) keeps the
   * nebula, the stars and their glows. Dust = src − oS, soft-kneed above +60/255 (slope 0.3) so no small bright
   * star survives. Out = oL + dust + star · (oS − oL): nebula and dust as they are, stars at `star` of their strength
   * (default 20 %). Measured over S2, S5 and S8: no 16:9 lane, caption, relay or name pixel brighter than text-3.
   * o.fine (the 4:5 lane, where even a small star beside the paused trail or inside the holding ring reads as a
   * second relay point): the dust is split off by a radius-1 opening instead (o1: only the 1-2 px grain is dust), so
   * small stars and the bright cores of large ones (3 to 9 px, which oS would hand back as dust) are scaled with the
   * glows: out = oL + knee(src − o1) + star · (o1 − oL). The grain, and so the texture of the sky, is unchanged.
   * Subtractions keep alpha at 1 (src + (1 − oS) − 1) because SVG filters clamp premultiplied colour.
   */
  function quietFilter(id, w, hgt, o = {}) {
    const RL = 28, RS = o.fine ?? 4, STAR = o.star ?? 0.2;
    const filter = s('filter', { id, x: 0, y: 0, width: w, height: hgt, filterUnits: 'userSpaceOnUse', 'color-interpolation-filters': 'sRGB' });
    const morph = (inp, op, r, res) => s('feMorphology', { in: inp, operator: op, radius: r, result: res });
    const fn = (attrs) => ['feFuncR', 'feFuncG', 'feFuncB'].map((f) => s(f, attrs));
    const kx = 60 / 255, knee = Array.from({ length: 33 }, (_, i) => { const x = i / 32; return (x < kx ? x : kx + 0.3 * (x - kx)).toFixed(4); }).join(' ');
    filter.append(
      morph('SourceGraphic', 'erode', RL, 'eL'), morph('eL', 'dilate', RL, 'oL'),
      morph('SourceGraphic', 'erode', RS, 'eS'), morph('eS', 'dilate', RS, 'oS'),
      s('feComponentTransfer', { in: 'oS', result: 'iS' }, ...fn({ type: 'linear', slope: -1, intercept: 1 })),
      s('feComposite', { in: 'SourceGraphic', in2: 'iS', operator: 'arithmetic', k1: 0, k2: 1, k3: 1, k4: -1, result: 'd' }),
      s('feComponentTransfer', { in: 'd', result: 'dk' }, ...fn({ type: 'table', tableValues: knee })),
      s('feComposite', { in: 'oL', in2: 'oS', operator: 'arithmetic', k1: 0, k2: 1 - STAR, k3: STAR, k4: 0, result: 'b' }),
      s('feComposite', { in: 'dk', in2: 'b', operator: 'arithmetic', k1: 0, k2: 1, k3: 1, k4: 0 }));
    return filter;
  }

  // ------------------------------------------------------------------ builder
  let quietId = 0;
  const quietCache = {};                                   // region key → its filtered canvas (one filter run per page)
  const smooth = (u) => u * u * (3 - 2 * u);
  /** A CSS mask along one axis of a box: 1 on [a0 + f0, a1 − f1], 0 outside [a0, a1], smoothstep feathers (f = 0: a
   *  hard edge, used only where the box meets the frame edge). */
  function bandMask(dir, a0, f0, a1, f1) {
    const st = [], N = 6;
    if (f0 > 0) for (let i = 0; i <= N; i++) st.push(`rgba(0,0,0,${smooth(i / N).toFixed(4)}) ${(a0 + (f0 * i) / N).toFixed(2)}px`);
    else st.push(`#000 ${a0.toFixed(2)}px`);
    if (f1 > 0) for (let i = 0; i <= N; i++) st.push(`rgba(0,0,0,${smooth(1 - i / N).toFixed(4)}) ${(a1 - f1 + (f1 * i) / N).toFixed(2)}px`);
    else st.push(`#000 ${a1.toFixed(2)}px`);
    return `linear-gradient(${dir}, ${st.join(', ')})`;
  }
  /**
   * A quieted copy of the sky (quietFilter: stars at 20 %, nebula and dust kept, nothing blurred) shown through a
   * feathered box. The copy is computed once per page into a canvas from the PNG band `src` (QUIET_SRC) and rides the
   * sky's own transform (parallax 0.3): the box offset, the sky image's transform, then the band's offset. It lives in
   * the sky holder (mirrored in 4:5), so boxes are given in that holder's coordinates.
   * render(box [x0, y0, x1, y1], feather [left, top, right, bottom]) in holder px.
   */
  function quietRegion(parent, sky, key, src, fo) {
    const [c0, c1, r0, r1] = src, w = c1 - c0, hgt = r1 - r0;
    const id = `stagew-quiet-${key}-${++quietId}`;
    const fs = s('svg', { width: 0, height: 0, style: 'position:absolute;left:0;top:0' });
    fs.append(quietFilter(id, w, hgt, fo));
    parent.append(fs);
    // two nested boxes: the outer masks across x, the inner across y (masks multiply)
    const outer = h('div', { 'data-quiet': key, style: 'position:absolute;left:0;top:0;overflow:hidden' });
    const inner = h('div', { style: 'position:absolute;left:0;top:0;width:100%;height:100%' });
    const cv = h('canvas', { width: w, height: hgt, style: `position:absolute;left:0;top:0;width:${w / 2}px;height:${hgt / 2}px;transform-origin:0 0` });
    inner.append(cv); outer.append(inner); parent.append(outer);
    let drawn = false;
    return {
      render(box, fe) {
        const img = sky.el;
        if (!drawn && img.complete && img.naturalWidth) {
          const g = cv.getContext('2d');
          if (quietCache[key]) g.drawImage(quietCache[key], 0, 0);
          else { g.filter = `url(#${id})`; g.drawImage(img, c0, r0, w, hgt, 0, 0, w, hgt); quietCache[key] = cv; }
          drawn = true;
        }
        const x0 = Math.floor(box[0]), y0 = Math.floor(box[1]), x1 = Math.ceil(box[2]), y1 = Math.ceil(box[3]);
        css(outer, { left: `${x0}px`, top: `${y0}px`, width: `${x1 - x0}px`, height: `${y1 - y0}px` });
        css(cv, { transform: `translate(${-x0}px, ${-y0}px) ${img.style.transform} translate(${c0 / 2}px, ${r0 / 2}px)` });
        const mx = bandMask('to right', box[0] - x0, fe[0], box[2] - x0, fe[2]);
        const my = bandMask('to bottom', box[1] - y0, fe[1], box[3] - y0, fe[3]);
        css(outer, { maskImage: mx, webkitMaskImage: mx });
        css(inner, { maskImage: my, webkitMaskImage: my });
      },
    };
  }

  function stageW(root, cfg = {}) {
    const W = FMT.W, H = FMT.H;
    root.style.background = C.bg;
    // Sky seed 97. In 4:5 the nebula's rose sits under the `theo` name, so the PNG is mirrored horizontally
    // (SCRIPT.md §3.8 remedy). Mirrored about the frame centre in a holder (the kit's own flip scales about the
    // image corner); the camera's x offset is mirrored with it.
    const MIRROR = P;
    const skyBox = h('div', { style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px;overflow:hidden;${MIRROR ? 'transform:scaleX(-1);transform-origin:50% 50%;' : ''}` });
    root.append(skyBox);
    const sky = F.S.sky(skyBox, 'w');
    // Quiet regions. Seed 97 is dense and no mirror keeps its stars off the hand-off line or out of the caption word
    // spaces (all four orientations checked: stars sit on the line, one beside the relay at the 2× push, one after
    // `maya`; in 16:9 a near-white star between `your` and `agent` and lilac ones between `it` and `to`, after
    // `relay.` and between `relay` and `sees` read as punctuation). Over those places a quieted copy of the same sky
    // is laid through a feathered mask, so no star there is brighter than text-3:
    //  - the lane along the trail (core half-width 36·s, feather 48·s; 16:9 horizontal at y 525, 4:5 vertical at
    //    x 940 from y 380 to 1140), so the trail, relay dot, holding ring and names read on a quiet ground;
    //  - 16:9 only, the caption field (screen x 0 → 1200, y 60 → 400, feather 48): every caption line box of S2, S5
    //    and S8 (x 96 → line end + 24, cap top − 12 → baseline + 16) lies inside it. (4:5 captions are clean.)
    // The caption field and the 4:5 lane use the fine split (quietFilter o.fine), which also takes the small stars
    // down. Measured on sky-only renders every 0.1 s of S2, S5 and S8: inside the caption line boxes no 3 × 3 feature
    // above luma 75 (text-3 is 137; single grain pixels ≤ 128); on the 4:5 line ≤ 59 and inside the ring ≤ 45 (60 %
    // of text-3 is 82).
    const quiet = [];
    if (!P) {
      const lane = quietRegion(skyBox, sky, 'lane', QUIET_SRC.lane);
      quiet.push((cam) => {
        const y = H / 2 + cam.s * (G.across - cam.y), hh = 36 * cam.s, fe = 48 * cam.s;
        lane.render([0, y - hh - fe, W, y + hh + fe], [0, fe, 0, fe]);
      });
      const cap = quietRegion(skyBox, sky, 'caption', QUIET_SRC.caption, { fine: 1, star: 0.2 });
      quiet.push(() => cap.render([0, 12, 1248, 448], [0, 48, 48, 48]));
    } else {
      // 4:5: the holder is mirrored, so the lane's holder x is W − screen x
      const lane = quietRegion(skyBox, sky, 'lane', QUIET_SRC.lane, { fine: 1, star: 0.2 });
      quiet.push((cam) => {
        const sx = (v) => W / 2 + cam.s * (v - cam.x), sy = (v) => H / 2 + cam.s * (v - cam.y);
        const x = W - sx(G.across), hw = 36 * cam.s, fe = 48 * cam.s;
        lane.render([x - hw - fe, sy(380) - fe, x + hw + fe, sy(1140) + fe], [fe, fe, fe, fe]);
      });
    }
    const world = h('div', { 'data-layer': 'world', style: 'position:absolute;left:0;top:0' });
    const screen = h('div', { 'data-layer': 'screen', style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px;z-index:5` });
    root.append(world, screen);
    const camApply = F.worldCam(world);

    // plate groups: plate (+ its prompt) + name + presence dot. The group locks in as one.
    const parts = [];
    const groups = {}, plates = {}, prompts = {}, names = {}, dots = {};
    for (const who of ['maya', 'theo']) {
      const g = G[who];
      const grp = h('div', { style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px` });
      const [x0, y0, x1, y1] = g.plate;
      const plate = h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;background:${C.surface};border-radius:${G.radius}px` });
      // the prompt sits inside the plate (it blurs with it); position it in plate coordinates
      const pr = ui.text(`${who}$`, { face: 'mono', size: G.promptSize, x: g.prompt[0] - x0, baseline: g.prompt[1] - y0, colour: C.brand });
      plate.append(pr.el);
      const nm = ui.text(who, { face: 'mono', size: G.nameSize, weight: 500, x: g.name.x, baseline: g.name.base, anchor: g.name.anchor, colour: C.text });
      const dsvg = s('svg', { width: W, height: H, viewBox: `0 0 ${W} ${H}`, style: 'position:absolute;left:0;top:0;overflow:visible' });
      const dt = ui.dot(dsvg, { cx: g.dot[0], cy: g.dot[1], d: G.dotD, stroke: G.stroke, level: cfg.level?.[who] ?? (() => 1),
        pulses: cfg.pulses?.[who] ?? [], arrivals: cfg.arrivals?.[who] ?? [] });
      grp.append(plate, nm.el, dsvg);
      world.append(grp);
      groups[who] = grp; plates[who] = plate; prompts[who] = pr; names[who] = nm; dots[who] = dt;
      parts.push(dt);
      if (cfg.lock?.[who] != null) {
        const L = F.S.LOCK, side = who === 'maya' ? -1 : 1;
        // 16:9: maya from x −92, theo from x +92. 4:5: maya from y −54 (top), theo from y +120 (bottom).
        const lk = P ? F.S.lock(grp, cfg.lock[who], 0, who === 'maya' ? L.top : L.bottom) : F.S.lock(grp, cfg.lock[who], side * L.side, 0);
        parts.push(lk);
      }
    }

    // trail, relay dot, holding ring (world SVG), placed under the plate groups (see trail()); the relay-log plate
    // (S5) sits under the trail layer
    const svg = s('svg', { width: W, height: H, viewBox: `0 0 ${W} ${H}`, style: 'position:absolute;left:0;top:0;overflow:visible' });
    world.insertBefore(svg, groups.maya);
    let log = null;
    if (cfg.log) { log = buildLog(world); world.insertBefore(log.plate, svg); }
    // holding ring opacity: enters 240 ms, leaves 120 ms (the trail clears the ring with the same envelope)
    const ringOp = cfg.holdRing ? (t) => Math.min(seg(t, cfg.holdRing.in, 0.24), 1 - seg(t, cfg.holdRing.out, 0.12)) : null;
    const tr = cfg.trail ? trail(svg, { ...cfg.trail, ring: ringOp }) : null;
    const relayDot = s('circle', { cx: G.relay[0], cy: G.relay[1], r: G.relayD / 2, fill: C.text3 });
    svg.append(relayDot);
    const holdRing = s('circle', { cx: G.relay[0], cy: G.relay[1], r: G.hold.r, fill: 'none', stroke: C.lineStrong, 'stroke-width': G.hold.w, opacity: 0 });
    svg.append(holdRing);

    // relay label (world): 16:9 centred x 960, card y 424.8 → 499.0 (§3.5 + 8 px up, see G); 4:5 right edge x 912, y 704.4 → 815.6
    let relayLabel = null;
    if (cfg.relayLabel) {
      const R = G.relayLabel;
      relayLabel = card('Relay', { size: R.size, x: P ? R.right : R.centre, anchor: P ? 'end' : 'middle', top: R.top, typedAt: cfg.relayLabel.typedAt });
      world.append(relayLabel.el);
    }

    // sub-frame motion blur (cfg.shutter, SCRIPT.md §3.9 and amendment 8): the world is rendered at N sub-frame times
    // across the open shutter and each state is cloned; the clones are summed at 1/N with plus-lighter inside isolated
    // groups, so the result is the average of the N world images (premultiplied, transparency included), which is
    // then composited over the sky as the world itself would be. Only the world moves fast enough to need it: the sky follows at
    // 0.3 and the screen layer (captions) is never blurred.
    const MB_TREE = [2, 2, 2, 2], MB_N = 16;
    const mb = h('div', { style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px;isolation:isolate;display:none` });
    root.insertBefore(mb, screen);
    let mbOn = false;
    const api = {
      G, world, screen, svg, sky, groups, plates, prompts, names, dots, relayDot, holdRing, relayLabel, trail: tr, log,
      camAt: { x: W / 2, y: H / 2, s: 1 },
      render(t) {
        const sh = cfg.shutter ? cfg.shutter(t) : 0;
        if (sh > 1e-3) {
          const tau = sh / 120;                      // 180° at 60 fps = 1/120 s, centred on the frame time
          css(world, { display: 'block' });
          let level = [];
          for (let k = 0; k < MB_N; k++) {
            api.core(t + ((k + 0.5) / MB_N - 0.5) * tau);
            const c = world.cloneNode(true);
            c.setAttribute('data-layer', 'world-mb');
            level.push(c);
          }
          // average in a binary tree of isolated groups (each child at 1/2 with plus-lighter): every level rounds
          // once, so the result stays within 2 levels of 8 bits of the unblurred frame (measured on a still frame;
          // mean 0.06). 12 clones in one group at 1/12 each drift by up to 14 (the plates' surface visibly brightens),
          // and a 2·2·3 tree by 6, hence 16 sub-samples (§3.9 asks for 12; more only makes the smear smoother).
          for (const b of MB_TREE) {
            const next = [];
            for (let i = 0; i < level.length; i += b) {
              const g = h('div', { style: `position:absolute;left:0;top:0;width:${W}px;height:${H}px;isolation:isolate` });
              for (const c of level.slice(i, i + b)) { c.style.mixBlendMode = 'plus-lighter'; c.style.opacity = (1 / b).toFixed(6); g.append(c); }
              next.push(g);
            }
            level = next;
          }
          mb.replaceChildren(level[0]);
          api.core(t);
          css(world, { display: 'none' }); css(mb, { display: 'block' });
          mbOn = true;
          return;
        }
        if (mbOn) { mb.replaceChildren(); css(mb, { display: 'none' }); css(world, { display: 'block' }); mbOn = false; }
        api.core(t);
      },
      /** the frame at time t without motion blur (the shot's own world updates run in cfg.onWorld(t)) */
      core(t) {
        const cam = cfg.cam ? cfg.cam(t) : { x: W / 2, y: H / 2, s: 1 };
        api.camAt = cam;
        camApply(cam);
        sky.render(MIRROR ? { ...cam, x: W - cam.x } : cam);
        for (const q of quiet) q(cam);
        for (const p of parts) p.render(t);
        if (cfg.relayDotIn != null) css(relayDot, { opacity: op(seg(t, cfg.relayDotIn, 0.24)) });
        if (ringOp) css(holdRing, { opacity: op(ringOp(t)) });
        if (relayLabel) {
          relayLabel.render(t);
          if (cfg.relayLabel.typedAt != null) {
            // locks from y −8 over 240 ms; the stock is opaque within 80 ms (CARD_IN), before its first letter has
            // stamped in, so it never reads as a see-through grey box with half-typed letters
            const at = cfg.relayLabel.typedAt, p = seg(t, at, 0.24);
            css(relayLabel.el, { opacity: op(clamp((t - at) / CARD_IN)), transform: `translate(${relayLabel.x0.toFixed(2)}px,${(relayLabel.y0 - 8 * (1 - p)).toFixed(2)}px)` });
          }
        }
        if (tr) tr.render(t);
        if (cfg.plateBlur) {
          const b = cfg.plateBlur(t) / cam.s;        // screen px → world px (the world is scaled by the camera)
          for (const who of ['maya', 'theo']) css(plates[who], { filter: b > 0.01 ? `blur(${b.toFixed(3)}px)` : 'none' });
        }
        if (cfg.onWorld) cfg.onWorld(t);
      },
    };
    return api;
  }

  // ------------------------------------------------------------------ relay-log plate
  /** 16:9: world plate x 380 → 1600, y 640 → 698, Plex Mono 14 px (−0.0125 em), first character at x 396.55.
   *  4:5: plate x −24 → 920, y 568 → 692 (clips its text); the text is a strip: character i at x = X + i·a,
   *  line A's baseline fixed at 610, line C at 610 + 54·a/21.6. The plate's bottom edge scales with the strip
   *  (610 + 82·a/21.6: 692 at the W phases, 640.2 at the overview), so it keeps its W-phase padding under line C
   *  instead of leaving a blank half plate under 13 px type. Drive with log.setStrip(X, a). */
  function buildLog(world) {
    const L = G.log;
    const [x0, y0, x1, y1] = L.plate;
    const plate = h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;background:${C.surface};border-radius:${G.radius}px;overflow:hidden;opacity:0` });
    world.append(plate);
    const line = (str, size, tracking, base) => {
      const off = F.baselineOffset(FONT.mono, size, 400);
      return h('div', { style: `position:absolute;left:0;top:${(base - off).toFixed(3)}px;white-space:pre;font-family:${FONT.mono};font-size:${size}px;line-height:${size}px;letter-spacing:${tracking}em;color:${C.text3}`, text: str });
    };
    let lineA, lineC, strip = null, holder = null;
    if (!P) {
      const holder = h('div', { style: `position:absolute;left:${(L.x - x0).toFixed(3)}px;top:${-y0}px` });
      lineA = line(LOG_A, L.size, L.tracking, L.baseA);
      lineC = line(LOG_C, L.size, L.tracking, L.baseC);
      holder.append(lineA, lineC);
      plate.append(holder);
    } else {
      strip = h('div', { style: 'position:absolute;left:0;top:0;transform-origin:0 0' });
      lineA = line(LOG_A, L.size, 0, 0);
      lineC = line(LOG_C, L.size, 0, L.pitch);
      strip.append(lineA, lineC);
      // the plate's right edge (x 920) crops the strip mid-glyph (W1: the `n` of `event` would read as `r`, so
      // `msg=queued ever`): the strip fades out toward the edge, so a cut glyph fades instead of turning into another
      // letter. When the line ends inside the plate (W3: x 900) the fade leaves `1444` untouched (see setStrip).
      // The frame's left edge (x 0) crops it too (W3: 2/3 of the `o` of `to=` would read as a field `o=ZhbF0Hsy`):
      // the same fade, mirrored, while the line runs past it.
      holder = h('div', { style: `position:absolute;left:0;top:0;width:${x1 - x0}px;height:100%` });
      holder.append(strip);
      plate.append(holder);
    }
    css(lineC, { opacity: '0' });
    return {
      plate, lineA, lineC, strip, LOG_A, LOG_C,
      /** 4:5 strip transform: character i at frame x = X + i·a, line A baseline at 610 */
      setStrip(X, a) {
        const k = a / L.adv;
        css(strip, { transform: `translate(${(X - x0).toFixed(3)}px, ${(L.base - y0).toFixed(3)}px) scale(${k.toFixed(5)})` });
        css(plate, { height: `${(L.base + (y1 - L.base) * k - y0).toFixed(2)}px` });
        // right: line runs past the plate edge: clear for the last 8 px, opaque from 36 px (the cut glyph is gone, the
        // one before it fades). Line ends inside (W3, x 900): opaque from 20 px, so its last glyph is untouched.
        // left (frame x 0 = holder x −x0): line starts left of the frame: clear for the first 8 px, opaque from 28 px
        // (a cut glyph's fragment is gone, the glyph after it fades in). It is continuous in X, so pans never pop.
        const u = clamp(((X + LOG_A.length * a) - (x1 - 8)) / 8), v = clamp(-X / 8), wd = x1 - x0, e = -x0;
        const m = `linear-gradient(to right, transparent 0, transparent ${(e + 8 * v).toFixed(2)}px, #000 ${(e + 28 * v).toFixed(2)}px, `
          + `#000 ${(wd - 20 - 16 * u).toFixed(2)}px, transparent ${(wd - 8 * u).toFixed(2)}px, transparent ${wd}px)`;
        css(holder, { maskImage: m, webkitMaskImage: m });
      },
      /** plate + line A land (120 ms) at `at`; line C lands (120 ms) at `cAt` */
      render(t, at, cAt) {
        css(plate, { opacity: op((t - at) / 0.12) });
        css(lineC, { opacity: op((t - cAt) / 0.12) });
      },
    };
  }

  stageW.G = G;
  stageW.card = card;
  stageW.CARD_IN = CARD_IN;
  stageW.mix = mix;
  stageW.flow = flow;
  F.stageW = stageW;
})();
