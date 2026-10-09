// Film kit: the primitives SCRIPT.md is written against. Positions are frame pixels of the current format.
// Text is placed by its LEFT EDGE (or centre/right) and its BASELINE, exactly as the script specifies.
(() => {
  const F = window.FILM;
  const { Z, C, FONT, EASE, LINEAR, clamp, lerp, seg, h, s, css, op } = F;

  // ------------------------------------------------------------ font metrics
  const metricCache = new Map();
  /** distance from the top of a line box (line-height = size) to the baseline, for a font shorthand */
  function baselineOffset(family, size, weight = 400) {
    const key = `${family}|${size}|${weight}`;
    if (metricCache.has(key)) return metricCache.get(key);
    const box = h('div', { style: `position:absolute;left:-9999px;top:0;font-family:${family};font-size:${size}px;font-weight:${weight};line-height:${size}px;white-space:pre` }, 'Hxg');
    const probe = h('span', { style: 'display:inline-block;width:0;height:0;vertical-align:baseline' });
    box.append(probe);
    document.body.append(box);
    const off = probe.getBoundingClientRect().top - box.getBoundingClientRect().top;
    box.remove();
    metricCache.set(key, off);
    return off;
  }
  /** measured advance width of a string */
  const widthCache = new Map();
  function measure(text, family, size, weight = 400, tracking = 0) {
    const key = `${text}|${family}|${size}|${weight}|${tracking}`;
    if (widthCache.has(key)) return widthCache.get(key);
    const sp = h('span', { style: `position:absolute;left:-9999px;top:0;white-space:pre;font-family:${family};font-size:${size}px;font-weight:${weight};letter-spacing:${tracking}em` }, text);
    document.body.append(sp);
    const w = sp.getBoundingClientRect().width;
    sp.remove();
    widthCache.set(key, w);
    return w;
  }

  // ------------------------------------------------------------ text by baseline
  const FAM = { display: FONT.display, sans: FONT.sans, mono: FONT.mono };
  /**
   * A single line of text placed by baseline.
   * o: { x, baseline, face:'display'|'sans'|'mono', size, weight, colour, anchor:'start'|'middle'|'end', tracking(em), upper,
   *      typed:{at, step=0.085} | null, enter:{at, dur=0.24, rise} | null, out:{at, dur} | null, spans:[{text, colour}] }
   * Returns { el, width, x0, x1, render(t), times }
   */
  function text(str, o) {
    const fam = FAM[o.face ?? 'display'];
    const size = o.size, weight = o.weight ?? 400, tracking = o.tracking ?? 0;
    const shown = o.upper ? str.toUpperCase() : str;
    const wdt = measure(shown, fam, size, weight, tracking);
    const anchor = o.anchor ?? 'start';
    const x0 = anchor === 'start' ? o.x : anchor === 'middle' ? o.x - wdt / 2 : o.x - wdt;
    const top = o.baseline - baselineOffset(fam, size, weight);
    const el = h('div', { style: `position:absolute;left:${x0}px;top:${top}px;white-space:pre;font-family:${fam};font-size:${size}px;font-weight:${weight};line-height:${size}px;letter-spacing:${tracking}em;color:${o.colour ?? C.text}` });
    let ty = null;
    if (o.typed) {
      ty = F.ui.typed(shown, { at: o.typed.at, step: o.typed.step ?? 0.085, jitter: 0 });
      el.append(ty.el);
    } else if (o.spans) {
      for (const sp of o.spans) el.append(h('span', { style: sp.colour ? `color:${sp.colour}` : '', text: o.upper ? sp.text.toUpperCase() : sp.text }));
    } else el.textContent = shown;
    const rise = (o.enter?.rise ?? 6) * Z;
    return {
      el, width: wdt, x0, x1: x0 + wdt, top, times: ty ? ty.times : [],
      render(t) {
        let a = 1, dy = 0;
        if (o.enter) { const p = seg(t, o.enter.at, o.enter.dur ?? 0.24); a = p; dy = rise * (1 - p); }
        if (o.out) a = Math.min(a, 1 - seg(t, o.out.at, o.out.dur ?? 0.12, LINEAR));
        if (o.visible) a = Math.min(a, o.visible(t) ? 1 : 0);
        css(el, { opacity: op(a), transform: dy ? `translateY(${dy.toFixed(2)}px)` : 'none' });
        if (ty) ty.render(t);
      },
    };
  }

  // ------------------------------------------------------------ dotted underline (screen or world)
  /**
   * The brand trail used as an underline: dots of 3 brand px every 12 brand px (6/24 in 16:9, 9/36 in 4:5),
   * drawn left to right over `dur` (240 ms, brand ease); each dot fades in over 60 ms when the head reaches it.
   * o: { x0, x1, y (dot centre), at, dur=0.24, outAt, outDur=0.12, colour, k }
   */
  function underline(parentSvg, o) {
    const r = 1.5 * Z * (o.k ?? 1), pitch = 12 * Z * (o.k ?? 1);
    const n = Math.floor((o.x1 - o.x0) / pitch) + 1;
    const g = s('g', { fill: o.colour ?? C.accent });
    const dots = Array.from({ length: n }, (_, i) => { const c = s('circle', { cx: (o.x0 + i * pitch).toFixed(2), cy: o.y.toFixed(2), r: r.toFixed(2) }); g.append(c); return c; });
    parentSvg.append(g);
    const dur = o.dur ?? 0.24;
    return {
      el: g, n,
      render(t) {
        const head = EASE(clamp((t - o.at) / dur)) * (o.x1 - o.x0);
        const outA = o.outAt != null ? 1 - clamp((t - o.outAt) / (o.outDur ?? 0.12)) : 1;
        for (let i = 0; i < n; i++) {
          const x = i * pitch;
          // time the head reached this dot
          let a = 0;
          if (t >= o.at && head >= x - 0.01) {
            // solve when head crossed x: approximate by inverse with fine search (cached per dot)
            const tc = dots[i].__tc ?? (dots[i].__tc = crossTime(x, o.x1 - o.x0, o.at, dur));
            a = clamp((t - tc) / 0.06);
          }
          const v = op(a * outA);
          if (dots[i].__o !== v) { dots[i].setAttribute('opacity', v); dots[i].__o = v; }
        }
      },
    };
  }
  function crossTime(x, len, at, dur) {
    if (len <= 0) return at;
    let lo = 0, hi = 1;
    for (let i = 0; i < 40; i++) { const m = (lo + hi) / 2; if (EASE(m) * len < x) lo = m; else hi = m; }
    return at + hi * dur;
  }

  // ------------------------------------------------------------ presence dot (SVG)
  /**
   * Shape-coded presence dot. o: { cx, cy, d (diameter px), stroke (px), online:(t)=>bool, fillAt?: time the ring fills,
   *   pulses: [times], arrivals: [times], colourOff }
   * Online = solid `online`; offline = hollow ring `text-3`. Ring fill: 120 ms. Pulse: halo r ×1 → ×2.6, stroke `online`,
   * opacity 0.5 → 0 over 1.2 s brand ease; arrival pulse starts at 0.8.
   */
  function dot(parentSvg, o) {
    const r = o.d / 2, sw = o.stroke;
    const g = s('g');
    const halos = [...(o.pulses ?? []).map((t) => ({ t, a: 0.5 })), ...(o.arrivals ?? []).map((t) => ({ t, a: 0.8 }))]
      .map((p) => ({ ...p, c: s('circle', { cx: o.cx, cy: o.cy, r, fill: 'none', stroke: C.online, 'stroke-width': sw, opacity: 0 }) }));
    halos.forEach((p) => g.append(p.c));
    const ring = s('circle', { cx: o.cx, cy: o.cy, r: r - sw / 2, fill: C.online, 'fill-opacity': 0, stroke: C.text3, 'stroke-width': sw });
    g.append(ring);
    parentSvg.append(g);
    const mix = (a, b, p) => { const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16); const ch = (sh) => Math.round(lerp((pa >> sh) & 255, (pb >> sh) & 255, p)); return `rgb(${ch(16)},${ch(8)},${ch(0)})`; };
    return {
      el: g,
      render(t) {
        // online level 0..1 (120 ms transitions at the moments online(t) changes are given as fillAt / emptyAt)
        let k = o.level ? o.level(t) : (o.online ? (o.online(t) ? 1 : 0) : 1);
        ring.setAttribute('fill-opacity', op(k));
        ring.setAttribute('stroke', k >= 1 ? C.online : k <= 0 ? C.text3 : mix(C.text3, C.online, k));
        ring.setAttribute('opacity', op(o.fade ? o.fade(t) : 1));
        for (const p of halos) {
          const u = (t - p.t) / 1.2;
          if (u < 0 || u > 1) { if (p.c.__o !== '0') { p.c.setAttribute('opacity', '0'); p.c.__o = '0'; } continue; }
          const e = EASE(u);
          p.c.setAttribute('r', (r * (1 + 1.6 * e)).toFixed(2));
          const v = op(p.a * (1 - e) * (o.fade ? o.fade(t) : 1)); p.c.setAttribute('opacity', v); p.c.__o = v;
        }
      },
    };
  }
  /** online level for a ring that fills at `at` (120 ms) and optionally empties at `off` */
  const levelFill = (at, off = null) => (t) => (off != null && t >= off ? 1 - clamp((t - off) / 0.12) : clamp((t - at) / 0.12));

  // ------------------------------------------------------------ sky with parallax
  /**
   * The deep field behind a shot. The PNG is generated at 2× its display size (for supersampled renders).
   * o: { name (file in assets/), w, h (display px), follow: 0.3 } — call render(cam) with the shot camera {x,y,s}
   * where cam is the same object the world camera uses; the sky moves at `follow` of the camera.
   */
  function sky(parent, o) {
    const { W, H } = F.FMT;
    const img = h('img', { src: `assets/${o.name}`, style: `position:absolute;left:0;top:0;width:${o.w}px;height:${o.h}px;transform-origin:0 0;${o.mirror ? '' : ''}` });
    parent.append(img);
    const follow = o.follow ?? 0.3;
    const base = { x: (W - o.w) / 2, y: (H - o.h) / 2 };
    return {
      el: img,
      render(cam) {
        // camera at rest: c = (W/2, H/2), s = 1
        const s1 = 1 + follow * ((cam?.s ?? 1) - 1);
        const dx = follow * -(((cam?.x ?? W / 2) - W / 2) * (cam?.s ?? 1));
        const dy = follow * -(((cam?.y ?? H / 2) - H / 2) * (cam?.s ?? 1));
        // scale about frame centre
        const tx = W / 2 + (base.x - W / 2) * s1 + dx, ty = H / 2 + (base.y - H / 2) * s1 + dy;
        css(img, { transform: `translate(${tx.toFixed(2)}px, ${ty.toFixed(2)}px) scale(${s1.toFixed(5)})${o.flip ? ' scaleX(-1)' : ''}` });
      },
    };
  }

  // ------------------------------------------------------------ cameras
  /** world camera from a function cam(t) -> {x, y, s} (world point at frame centre, scale) */
  function worldCam(world) {
    const { W, H } = F.FMT;
    css(world, { position: 'absolute', left: '0px', top: '0px', width: `${W}px`, height: `${H}px`, transformOrigin: '0 0' });
    return (c) => {
      const tx = W / 2 - c.x * c.s, ty = H / 2 - c.y * c.s;
      css(world, { transform: `translate(${tx.toFixed(3)}px, ${ty.toFixed(3)}px) scale(${c.s.toFixed(5)})` });
    };
  }
  /**
   * worldCam with a fixed, invisible 0.05° roll about `about` (frame px). Under a plain scale + translate, Skia snaps each
   * text line's baseline to whole pixels, so text in a slowly moving layer ratchets 1 px at a time while plates glide;
   * the tiny rotation keeps text on the smooth (unsnapped) path. Measured in S9/S10: no baseline step over 0.2 px/frame.
   */
  function worldCamStable(world, about) {
    const { W, H } = F.FMT;
    const [ax, ay] = about ?? [W / 2, H / 2];
    css(world, { position: 'absolute', left: '0px', top: '0px', width: `${W}px`, height: `${H}px`, transformOrigin: '0 0' });
    return (c) => {
      const tx = W / 2 - c.x * c.s, ty = H / 2 - c.y * c.s;
      css(world, { transform: `translate(${tx.toFixed(3)}px, ${ty.toFixed(3)}px) scale(${c.s.toFixed(5)}) translate(${ax}px, ${ay}px) rotate(0.05deg) translate(${-ax}px, ${-ay}px)` });
    };
  }
  /** a slow drift: s from s0 to s1 over [t0, t1] about a world point that stays fixed on screen */
  function drift({ t0, t1, s0 = 1, s1 = 1.03, about, ease = EASE }) {
    const { W, H } = F.FMT;
    const ax = about?.[0] ?? W / 2, ay = about?.[1] ?? H / 2;
    return (t) => {
      const s = lerp(s0, s1, seg(t, t0, t1 - t0, ease));
      // keep world point A at its rest screen position: screen = (W/2,H/2) + s*(A - c) = A  ->  c = A - (A - (W/2,H/2))/s
      return { x: ax - (ax - W / 2) / s, y: ay - (ay - H / 2) / s, s };
    };
  }
  /**
   * The script's push: world point P travels from its screen position Q0 to Q1 while s goes s0 -> s1 (brand ease),
   * c(t) = P - (Q(t) - centre)/s(t).
   */
  function push({ P, Q0, Q1, s0 = 1, s1, t0, dur, ease = EASE }) {
    const { W, H } = F.FMT;
    return (t) => {
      const e = seg(t, t0, dur, ease);
      const s = lerp(s0, s1, e), qx = lerp(Q0[0], Q1[0], e), qy = lerp(Q0[1], Q1[1], e);
      return { x: P[0] - (qx - W / 2) / s, y: P[1] - (qy - H / 2) / s, s };
    };
  }
  /** chain camera functions by time: [[tStart, fn], ...] — the last one whose tStart <= t applies */
  const camSeq = (list) => (t) => { let f = list[0][1]; for (const [ts, fn] of list) if (t >= ts) f = fn; return f(t); };

  // ------------------------------------------------------------ the 21° cut
  const TAN21 = Math.tan((21 * Math.PI) / 180);
  /** clip-path for the incoming shot at local time u (0..0.6 s): everything left of the 21° edge is the new shot */
  function cut21(root, u) {
    const { W, H } = F.FMT;
    if (u >= 0.6) { css(root, { clipPath: 'none' }); return; }
    const m0 = -(H / 2 + 10) * TAN21 - 10, m1 = W + (H / 2 + 10) * TAN21 + 10;
    const m = lerp(m0, m1, EASE(clamp(u / 0.6)));
    const xe = (y) => m + (H / 2 - y) * TAN21;
    css(root, { clipPath: `polygon(-10px -10px, ${xe(-10).toFixed(2)}px -10px, ${xe(H + 10).toFixed(2)}px ${H + 10}px, -10px ${H + 10}px)` });
  }

  // ------------------------------------------------------------ terminal v2 (explicit pitch)
  /**
   * Terminal text at an exact size and line pitch, positioned by the first baseline. No plate (draw it yourself).
   * rows: [{ parts: [{text, colour}], at, dimAt? }] ; o: { x, baseline, size, pitch, dimAt, dimTo=0.4 }
   * Each row lands whole: opacity 0 -> 1 over 120 ms linear at row.at.
   */
  function termText(rows, o) {
    const el = h('div', { style: `position:absolute;left:0;top:0` });
    const off = baselineOffset(FONT.mono, o.size, 400);
    const items = rows.map((row, i) => {
      const r = h('div', { style: `position:absolute;left:${o.x}px;top:${o.baseline + i * o.pitch - off}px;white-space:pre;font-family:${FONT.mono};font-size:${o.size}px;line-height:${o.size}px;letter-spacing:${o.tracking ?? 0}em` });
      for (const p of row.parts) r.append(h('span', { style: `color:${p.colour ?? C.text3}`, text: p.text }));
      el.append(r);
      return { r, row };
    });
    return {
      el, items,
      render(t) {
        for (const { r, row } of items) {
          let a = clamp((t - row.at) / 0.12);
          const dimAt = row.keep ? null : (row.dimAt ?? o.dimAt);
          if (dimAt != null) a *= 1 - (1 - (o.dimTo ?? 0.4)) * clamp((t - dimAt) / 0.12);
          css(r, { opacity: op(a) });
        }
      },
    };
  }

  // ------------------------------------------------------------ cues
  /** register sound cues for letters (typed times), with an optional format filter */
  function cueLetters(times, kind, base, extra = {}) { for (const tt of times) F.cue(base + tt, kind, extra); }

  // ------------------------------------------------------------ camera ease and motion blur
  /** Camera moves (pushes, pans, drifts) start and end at rest: the brand's second curve, the boot's sweep
   *  (0.4, 0, 0.6, 1). UI elements keep the brand ease; only the camera uses this. */
  const CAM = F.SWEEP;
  F.blurWindows = [];
  /** Declare a time window [t0, t1] (absolute s) where the renderer adds 180° sub-frame motion blur (fast camera moves
   *  only; never a 21° edge or a reading hold). fmt: '16x9' | '4x5' | undefined (both). */
  function blur(t0, t1, fmt) { if (!fmt || fmt === F.FMT.name) F.blurWindows.push([+t0.toFixed(4), +t1.toFixed(4)]); }

  Object.assign(F, { CAM, blur, worldCamStable, baselineOffset, measure, TAN21, cut21, worldCam, drift, push, camSeq, levelFill, cueLetters });
  Object.assign(F.ui, { text, underline, dot, sky, termText });
})();
