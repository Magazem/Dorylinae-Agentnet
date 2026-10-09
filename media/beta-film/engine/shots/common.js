// Shared film styles (SCRIPT.md §3.2 and the director's amendments). Every shot uses these, so the film stays one voice.
(() => {
  const F = window.FILM;
  const { C, FMT, ui, h, s } = F;
  const P = FMT.portrait;

  // Type roles in frame px for the current format
  const ROLE = {
    caption: { face: 'display', size: P ? 60 : 72, pitch: P ? 68 : 80, x: P ? 64 : 96, base: P ? 170 : 160, colour: C.text },
    terminal: { size: P ? 36 : 42, pitch: P ? 54 : 64 },
    file: { size: 36 },
    name: { face: 'mono', size: P ? 32 : 28, weight: 500, colour: C.text },
    collector: { face: 'mono', size: P ? 36 : 28, weight: 500, tracking: 0.08, upper: true, colour: C.text3, x: P ? 64 : 96, base: 290 },
    tag: { face: 'display', size: P ? 48 : 52, colour: C.text2 },
    terms: { size: P ? 72 : 96, pitch: P ? 84 : 104 },
    facts: { face: 'sans', weight: 500, size: P ? 48 : 64, pitch: P ? 64 : 84, colour: C.text2 },
    address: { face: 'display', size: 64, colour: C.text },
  };
  const RADIUS = P ? 24 : 16;          // plate radius (8 brand px at zoom)
  const CHANNEL = P ? 18 : 12;         // 6 brand px at zoom
  const LOCK = P ? { side: 138, top: -54, antenna: -90, bottom: 120 } : { side: 92, top: -36, antenna: -60, bottom: 80 }; // frame px offsets

  /** A caption line (Special Elite, `text`) by baseline. line: 0-based row under the first caption baseline. */
  function caption(str, o = {}) {
    const r = ROLE.caption;
    return ui.text(str, { face: 'display', size: o.size ?? r.size, x: o.x ?? r.x, baseline: o.baseline ?? r.base + (o.line ?? 0) * r.pitch,
      colour: o.colour ?? r.colour, spans: o.spans, typed: o.typed, enter: o.enter, out: o.out, visible: o.visible, anchor: o.anchor });
  }
  /** The call tag "Apply at dorylinae.net", set as a quiet brand label (Plex Mono 500, caps, 0.08 em, text-2) so it never
   *  competes with the caption. Director's change: 16:9 right-aligned x 1824 on the collector row (baseline 290), so the
   *  metadata reads as one row (who/when left, the call right); 4:5 bottom-left x 64 baseline 1206. Same words as the end card. */
  function tag(o = {}) {
    return ui.text('Apply at dorylinae.net', { face: 'mono', weight: 500, tracking: 0.08, upper: true, size: P ? 36 : 28, colour: C.text2, ...(P ? { x: 64, baseline: 1206, anchor: 'start' } : { x: 1824, baseline: 290, anchor: 'end' }), enter: o.enter, out: o.out });
  }
  /** Collector label, e.g. collector('maya', '13:41') -> "MAYA · 13:41 UTC" in the brand label style. */
  function collector(who, hhmm, o = {}) {
    const r = ROLE.collector;
    return ui.text(`${who} · ${hhmm} UTC`, { face: 'mono', size: r.size, weight: 500, tracking: 0.08, upper: true, colour: r.colour, x: o.x ?? r.x, baseline: o.baseline ?? r.base, enter: o.enter });
  }
  /** A plate (surface, radius 8 brand px). Returns the element; position in frame px. */
  function plate(x0, y0, x1, y1, fill = C.surface) {
    return h('div', { style: `position:absolute;left:${x0}px;top:${y0}px;width:${x1 - x0}px;height:${y1 - y0}px;background:${fill};border-radius:${RADIUS}px` });
  }
  /** Full-frame SVG overlay for dots, trails, underlines (frame px). */
  function overlay(parent, z = 5) {
    const svg = s('svg', { width: FMT.W, height: FMT.H, viewBox: `0 0 ${FMT.W} ${FMT.H}`, style: `position:absolute;left:0;top:0;overflow:visible;z-index:${z}` });
    parent.append(svg);
    return svg;
  }
  /** Sky by place name: 'w' (Stage W), 'maya', 'theo', 'inv', 'end'. Display sizes from SCRIPT.md §3.8. */
  const SKY = P
    ? { w: [1134, 1418], maya: [1296, 1620], theo: [1296, 1620], inv: [1134, 1418], end: [1080, 1350] }
    : { w: [2496, 1404], maya: [2304, 1296], theo: [2304, 1296], inv: [2016, 1134], end: [1920, 1080] };
  function sky(parent, place, o = {}) {
    const [w, hh] = SKY[place];
    return ui.sky(parent, { name: `sky-${place}-${P ? '45' : '169'}.png`, w, h: hh, follow: o.follow ?? 0.3, flip: o.flip });
  }
  /** Element locked in like a plate of the mark (frame-px offsets from LOCK). */
  function lock(el, at, dx = 0, dy = 0, dur = 0.5) {
    return ui.lockIn(el, { at, dur, dx: dx / F.Z, dy: dy / F.Z });
  }

  F.S = { P, ROLE, RADIUS, CHANNEL, LOCK, caption, tag, collector, plate, overlay, sky, lock };
})();
