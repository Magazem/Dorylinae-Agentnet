// Brand components for the film. Each returns { el, render(t) } where t is the time in seconds
// on the component's own clock (the shot passes its local time). Sizes are brand px times FILM.Z.
(() => {
  const { Z, C, FONT, EASE, LINEAR, clamp, lerp, seg, env, type, h, s, css, op } = window.FILM;

  // ---------------------------------------------------------------- typed text
  /**
   * Text that is typed one letter at a time (the boot's cadence is 85 ms; longer lines 40-50 ms).
   * Each letter stamps in over 60 ms (motion.ts: .ch 0.06 s linear). Returns the time typing ends.
   * opts: { at, cps|step, style (css), cursor: colour|false, jitter, seed, keepCursor }
   */
  function typed(text, opts = {}) {
    const step = opts.step ?? 0.085;
    const at = opts.at ?? 0;
    const el = h('span', { style: `white-space:pre;${opts.style ?? ''}` });
    const r = window.FILM.rng(opts.seed ?? 7);
    const chars = [...text];
    let k = at;
    const times = chars.map((ch) => { const tt = k; k += ch === ' ' ? step * 0.6 : step * (1 + (opts.jitter ?? 0) * (r() - 0.5)); return tt; });
    const spans = chars.map((ch) => { const sp = h('span', { text: ch }); sp.style.opacity = '0'; el.append(sp); return sp; });
    let cursor = null;
    if (opts.cursor) { cursor = h('span', { style: `display:inline-block;width:0.55em;height:1em;vertical-align:-0.12em;margin-left:0.04em;background:${opts.cursor}` }); el.append(cursor); }
    const end = chars.length ? times[times.length - 1] + 0.06 : at;
    return {
      el, end, times: times.filter((_, i) => chars[i] !== ' '),
      render(t) {
        for (let i = 0; i < spans.length; i++) css(spans[i], { opacity: op((t - times[i]) / 0.06) });
        if (cursor) {
          // the cursor rides after the last stamped letter; after typing it blinks at the brand's 1.2 s rhythm or hides
          const typing = t >= at - 0.3 && t < end + 0.15;
          const after = opts.keepCursor ? (Math.floor((t - end) / 0.6) % 2 === 0 ? 1 : 0) : 0;
          css(cursor, { opacity: t < at - 0.3 ? '0' : typing ? '1' : String(after) });
        }
      },
    };
  }

  // ---------------------------------------------------------------- lock-in (the boot's plate grammar)
  /**
   * An element that locks in like a plate of the mark: from an offset and transparent, to rest, over
   * 0.5 s with the brand ease (motion.ts .pl). dx/dy are brand px (motion.ts uses ±46 sides, -18..-30 top, +40 bottom).
   */
  function lockIn(el, { at = 0, dur = 0.5, dx = 0, dy = 0, out = null, outDur = 0.24 } = {}) {
    return {
      el,
      render(t) {
        const p = seg(t, at, dur);
        let o = clamp((t - at) / dur * 1.6);
        if (out != null) o = Math.min(o, 1 - seg(t, out, outDur));
        css(el, { opacity: op(o), transform: p >= 1 ? 'none' : `translate(${(dx * Z * (1 - p)).toFixed(2)}px, ${(dy * Z * (1 - p)).toFixed(2)}px)` });
      },
    };
  }

  /** A line that enters whole (240 ms, rises 6 brand px) and optionally leaves. */
  function enter(el, { at = 0, dur = 0.24, rise = 6, out = null, outDur = 0.24 } = {}) {
    return {
      el,
      render(t) {
        const p = seg(t, at, dur);
        let o = p;
        if (out != null) o = Math.min(o, 1 - seg(t, out, outDur));
        css(el, { opacity: op(o), transform: p >= 1 ? 'none' : `translateY(${(rise * Z * (1 - p)).toFixed(2)}px)` });
      },
    };
  }

  // ---------------------------------------------------------------- plate
  function plate({ x, y, w, hgt, fill = C.surface, radius = 8, style = '' }) {
    return h('div', { style: `position:absolute;left:${x}px;top:${y}px;width:${w}px;height:${hgt}px;background:${fill};border-radius:${radius * Z}px;${style}` });
  }

  // ---------------------------------------------------------------- specimen label
  /**
   * The typed card on label stock (#F6F3FB, dark ink, Special Elite, inset hairline, radius 2).
   * lines: [string, ...]; size: specimen font size in brand px (14 base). Typing at 85 ms per letter.
   * Returns { el, end, render(t) }. Position it yourself (el is absolute when x,y given).
   */
  function label(lines, { x = null, y = null, size = 14, at = 0, step = 0.085, k = 1, rotate = 0, meta = null, ink = C.ink, seed = 3, typedIn = true } = {}) {
    const fs = size * Z * k;
    const padX = fs * 0.9, padY = fs * 0.7;
    const el = h('div', { style: `position:${x == null ? 'relative' : 'absolute'};${x == null ? '' : `left:${x}px;top:${y}px;`}display:inline-block;background:${C.stock};color:${ink};border-radius:${2 * Z}px;padding:${padY}px ${padX}px;font-family:${FONT.display};font-size:${fs}px;line-height:${fs * 1.25}px;letter-spacing:0.02em;white-space:pre;transform-origin:50% 50%;${rotate ? `rotate:${rotate}deg;` : ''}` });
    const hair = h('div', { style: `position:absolute;inset:${1.25 * Z * k}px;border:${Math.max(1, 0.4 * Z * k)}px solid ${C.ink};opacity:0.55;border-radius:${1 * Z}px;pointer-events:none` });
    el.append(hair);
    let tt = at, parts = [];
    lines.forEach((ln, i) => {
      const row = h('div');
      if (typedIn) {
        const ty = typed(ln, { at: tt, step, seed: seed + i });
        row.append(ty.el); parts.push(ty); tt = ty.end + step * 2;
      } else row.textContent = ln;
      el.append(row);
    });
    if (meta) { const m = h('div', { style: `font-family:${FONT.mono};font-weight:500;font-size:${10 * Z * k}px;line-height:${14 * Z * k}px;letter-spacing:0.08em;text-transform:uppercase;color:${C.inkMeta};margin-top:${2 * Z * k}px` , text: meta }); el.append(m); }
    const end = parts.length ? parts[parts.length - 1].end : at;
    return { el, end, times: parts.flatMap((p) => p.times), render(t) { for (const p of parts) p.render(t); } };
  }

  // ---------------------------------------------------------------- presence chip
  /**
   * Pill on surface-2, mono 12, with the shape-coded dot: online = solid teal dot, away = half dot,
   * dnd = bar, offline = hollow ring in text-3. state(t) -> 'online'|'away'|'dnd'|'offline'.
   * The online dot may pulse every 2.4 s (pulsePhase in seconds).
   */
  function chip(name, { state = () => 'online', word = null, k = 1, pulse = true, pulsePhase = 0 } = {}) {
    const fs = 12 * Z * k, d = 8 * Z * k;
    const el = h('div', { style: `display:inline-flex;align-items:center;gap:${8 * Z * k}px;height:${28 * Z * k}px;padding:0 ${12 * Z * k}px 0 ${10 * Z * k}px;border-radius:${999}px;background:${C.surface2};font-family:${FONT.mono};font-weight:500;font-size:${fs}px;letter-spacing:0.02em;color:${C.text};white-space:pre` });
    const dotBox = h('span', { style: `position:relative;width:${d}px;height:${d}px;flex:none` });
    const ring = h('span', { style: `position:absolute;inset:0;border-radius:50%;border:${Math.max(1.5, 1.5 * Z * k)}px solid ${C.text3};box-sizing:border-box` });
    const fill = h('span', { style: `position:absolute;inset:0;border-radius:50%;background:${C.online}` });
    const half = h('span', { style: `position:absolute;inset:0;border-radius:50%;background:linear-gradient(90deg, ${C.away} 50%, transparent 50%);box-shadow:inset 0 0 0 ${Math.max(1.5, 1.5 * Z * k)}px ${C.away}` });
    const bar = h('span', { style: `position:absolute;left:0;right:0;top:${d * 0.38}px;height:${d * 0.24}px;border-radius:${d}px;background:${C.sting}` });
    const halo = h('span', { style: `position:absolute;inset:0;border-radius:50%;border:${Math.max(1, 1 * Z * k)}px solid ${C.online}` });
    dotBox.append(halo, ring, fill, half, bar);
    const nameEl = h('span', { text: name });
    const wordEl = h('span', { style: `color:${C.text3}` });
    el.append(dotBox, nameEl, wordEl);
    let lastState = null;
    return {
      el,
      render(t) {
        const st = state(t);
        css(ring, { opacity: st === 'offline' ? '1' : '0' });
        css(fill, { opacity: st === 'online' ? '1' : '0' });
        css(half, { opacity: st === 'away' ? '1' : '0' });
        css(bar, { opacity: st === 'dnd' ? '1' : '0' });
        const w = word ? word(t, st) : st;
        if (w !== lastState) { wordEl.textContent = w ? `  ${w}` : ''; lastState = w; }
        if (pulse && st === 'online') {
          const ph = (((t - pulsePhase) % 2.4) + 2.4) % 2.4;          // one pulse per bar
          const p = clamp(ph / 1.2);
          css(halo, { opacity: op((1 - p) * 0.7 * (ph < 1.2 ? 1 : 0)), transform: `scale(${(1 + 1.4 * EASE(p)).toFixed(3)})` });
        } else css(halo, { opacity: '0' });
      },
    };
  }

  // ---------------------------------------------------------------- trail
  /**
   * The pheromone trail: 3 px dots every 12 px (brand px), drawn along an SVG path.
   * opts: { d (path in px), draw: (t)=>0..1 head position, march: (t)=>px offset, fade: (t)=>0..1 opacity,
   *         tailFade:(t)=>0..1 erase from the tail, colour, k (dot size multiplier) }
   */
  function trail(svgParent, d, opts = {}) {
    const colour = opts.colour ?? C.accent;
    const r = 1.5 * Z * (opts.k ?? 1), pitch = 12 * Z * (opts.k ?? 1);
    const path = s('path', { d, fill: 'none', stroke: 'none' });
    svgParent.append(path);
    const len = path.getTotalLength();
    const n = Math.floor(len / pitch) + 2;
    const g = s('g', { fill: colour });
    svgParent.append(g);
    const dots = Array.from({ length: n }, () => { const c = s('circle', { r: r.toFixed(2) }); g.append(c); return c; });
    // precompute point lookup at 1px resolution for speed
    const pts = []; for (let i = 0; i <= Math.ceil(len); i++) { const p = path.getPointAtLength(Math.min(len, i)); pts.push([p.x, p.y]); }
    const at = (l) => { const i = clamp(l, 0, len); const a = Math.floor(i), b = Math.min(pts.length - 1, a + 1), f = i - a; return [lerp(pts[a][0], pts[b][0], f), lerp(pts[a][1], pts[b][1], f)]; };
    return {
      el: g, len, pitch, at,
      render(t) {
        const head = (opts.draw ? opts.draw(t) : 1) * len;
        const tail = (opts.tailFade ? opts.tailFade(t) : 0) * len;
        const m = opts.march ? ((opts.march(t) % pitch) + pitch) % pitch : 0;
        css(g, { opacity: op(opts.fade ? opts.fade(t) : 1) });
        for (let i = 0; i < n; i++) {
          const l = i * pitch + m;
          const c = dots[i];
          if (l > head + 0.01 || l > len || l < tail) { if (c.__v !== 0) { c.setAttribute('visibility', 'hidden'); c.__v = 0; } continue; }
          const [x, y] = at(l);
          if (c.__v !== 1) { c.setAttribute('visibility', 'visible'); c.__v = 1; }
          // dots just behind the head and just after the tail fade in so the line never pops
          const a = Math.min(clamp((head - l) / pitch + 0.15), clamp((l - tail) / (pitch * 2)));
          c.setAttribute('cx', x.toFixed(2)); c.setAttribute('cy', y.toFixed(2)); c.setAttribute('opacity', op(a));
        }
      },
    };
  }

  // ---------------------------------------------------------------- terminal block
  /**
   * Terminal block: always dark (#07060F), mono 14 (code), prompt in amethyst, output in text-3,
   * the one thing to notice in pheromone. lines: [{ kind:'cmd'|'out', text, who?, hl?: substring to mark, at, typedStep? }]
   * A human's command is typed (typedStep); an agent's command lands whole. Output lands whole.
   */
  function terminal(lines, { w, k = 1, title = null, pad = 16, ground = C.bg, radius = 8 } = {}) {
    const fs = 14 * Z * k, lh = 22 * Z * k;
    const el = h('div', { style: `position:relative;width:${w}px;background:${ground};border-radius:${radius * Z}px;padding:${pad * Z * k}px ${pad * 1.25 * Z * k}px;box-sizing:border-box;font-family:${FONT.mono};font-size:${fs}px;line-height:${lh}px;color:${C.text3};white-space:pre;overflow:hidden` });
    if (title) el.append(h('div', { style: `${type('label', k)};color:${C.text3};margin-bottom:${10 * Z * k}px`, text: title }));
    const parts = [];
    for (const ln of lines) {
      const row = h('div', { style: 'opacity:0' });
      if (ln.kind === 'cmd') {
        const prompt = h('span', { style: `color:${C.brand}`, text: `${ln.who ?? ''}$ ` });
        row.append(prompt);
        if (ln.typedStep) {
          const ty = typed(ln.text, { at: ln.at, step: ln.typedStep, style: `color:${C.text}`, seed: ln.seed ?? 5 });
          row.append(ty.el);
          parts.push({ row, at: ln.at - 0.12, ty });
        } else {
          row.append(markup(ln.text, ln.hl, C.text));
          parts.push({ row, at: ln.at });
        }
      } else {
        row.append(markup(ln.text, ln.hl, ln.colour ?? C.text3));
        parts.push({ row, at: ln.at });
      }
      el.append(row);
    }
    function markup(text, hl, colour) {
      const sp = h('span', { style: `color:${colour}` });
      if (!hl) { sp.textContent = text; return sp; }
      const i = text.indexOf(hl);
      if (i < 0) { sp.textContent = text; return sp; }
      sp.append(text.slice(0, i), h('span', { style: `color:${C.accent}`, text: hl }), text.slice(i + hl.length));
      return sp;
    }
    return {
      el, keystrokes: parts.filter((p) => p.ty).flatMap((p) => p.ty.times), lines: parts,
      render(t) {
        for (const p of parts) {
          const v = clamp((t - p.at) / 0.12);
          css(p.row, { opacity: op(v) });
          if (p.ty) p.ty.render(t);
        }
      },
    };
  }

  // ---------------------------------------------------------------- camera
  /** Camera on a world element: scale about the frame centre, then translate. cam(t) -> {x, y, s} (world px at frame centre). */
  function camera(world, cam) {
    const { W, H } = window.FILM.FMT;
    css(world, { transformOrigin: '0 0' });
    return {
      el: world,
      render(t) {
        const c = cam(t);
        const tx = W / 2 - c.x * c.s, ty = H / 2 - c.y * c.s;
        css(world, { transform: `translate(${tx.toFixed(2)}px, ${ty.toFixed(2)}px) scale(${c.s.toFixed(5)})` });
        if (c.blur != null) css(world, { filter: c.blur > 0.05 ? `blur(${c.blur.toFixed(2)}px)` : 'none' });
      },
    };
  }

  window.FILM.ui = { typed, lockIn, enter, plate, label, chip, trail, terminal, camera };
})();
