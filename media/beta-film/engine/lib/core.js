// Film core: format, timing, easing and DOM helpers. Classic script; everything hangs off window.FILM.
// Every visual is a pure function of time t (seconds). No CSS animation, no timers, no rAF.
(() => {
  const params = new URLSearchParams(location.search);
  const format = params.get('format') === '4x5' ? '4x5' : '16x9';
  const FORMATS = {
    // zoom: brand px -> video px (craft.md 5.1). safe: title-safe box (craft.md 5.3).
    '16x9': { W: 1920, H: 1080, zoom: 2, safe: { l: 96, r: 96, t: 54, b: 54 } },
    '4x5': { W: 1080, H: 1350, zoom: 3, safe: { l: 64, r: 64, t: 64, b: 128 } },
  };
  const FMT = { name: format, ...FORMATS[format] };
  FMT.portrait = format === '4x5';
  const Z = FMT.zoom;

  // ---------- music clock (100 BPM, 4/4): beat = 0.6 s = brand "slow", bar = 2.4 s = presence pulse
  const BEAT = 0.6, BAR = 2.4;
  const bb = (bar, beat = 0) => bar * BAR + beat * BEAT;   // bars and beats from 0 -> seconds

  // ---------- easing
  function bezier(p1x, p1y, p2x, p2y) {
    const cx = 3 * p1x, bx = 3 * (p2x - p1x) - cx, ax = 1 - cx - bx;
    const cy = 3 * p1y, by = 3 * (p2y - p1y) - cy, ay = 1 - cy - by;
    const sx = (u) => ((ax * u + bx) * u + cx) * u, sy = (u) => ((ay * u + by) * u + cy) * u;
    const dx = (u) => (3 * ax * u + 2 * bx) * u + cx;
    return (x) => {
      if (x <= 0) return 0; if (x >= 1) return 1;
      let u = x;
      for (let i = 0; i < 10; i++) { const e = sx(u) - x; if (Math.abs(e) < 1e-7) break; u -= e / (dx(u) || 1e-6); }
      if (u < 0 || u > 1) { let lo = 0, hi = 1; u = x; for (let i = 0; i < 40; i++) { const v = sx(u); if (v < x) lo = u; else hi = u; u = (lo + hi) / 2; } }
      return sy(Math.min(1, Math.max(0, u)));
    };
  }
  const EASE = bezier(0.2, 0.7, 0.2, 1);       // the brand ease
  const SWEEP = bezier(0.4, 0, 0.6, 1);        // the boot's glint sweep: the only other curve
  const LINEAR = (x) => Math.min(1, Math.max(0, x));
  const clamp = (x, a = 0, b = 1) => Math.min(b, Math.max(a, x));
  const lerp = (a, b, p) => a + (b - a) * p;
  /** progress of a segment that starts at `start` and lasts `dur`, eased */
  const seg = (t, start, dur, ease = EASE) => ease(clamp((t - start) / (dur || 1e-9)));
  /** in-hold-out envelope: 0 -> 1 over [a, a+fi], hold, 1 -> 0 over [b-fo, b] */
  const env = (t, a, fi, b, fo, ease = EASE) => Math.min(seg(t, a, fi, ease), 1 - seg(t, b - fo, fo, ease));

  // ---------- seeded randomness
  function rng(seed) {
    let a = seed >>> 0;
    return () => { a = (a + 0x6d2b79f5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
  }

  // ---------- brand tokens (dark theme) and type at film zoom
  const C = {
    bg: '#07060F', surface: '#0E0C1F', surface2: '#1A1733', line: '#2B2649', lineStrong: '#6E64A8',
    text: '#EFE9FB', text2: '#A79FC4', text3: '#8C85AB', brand: '#A678E6', onBrand: '#07060F',
    accent: '#D653E1', highlight: '#DDBAFC', online: '#5CE6C8', away: '#E8A838', sting: '#F0506E',
    violet: '#9756F0', scanBase: '#8E58D2', scanLight: '#C49BFF',
    stock: '#F6F3FB', ink: '#17122B', inkSoft: '#4E4670', inkMeta: '#665E88',
  };
  const FONT = { display: '"Special Elite"', sans: '"IBM Plex Sans"', mono: '"IBM Plex Mono"' };
  // [family, size, line, weight, tracking(em), upper]
  const TYPE = {
    'display-xl': ['display', 72, 72, 400, -0.01], 'display-l': ['display', 48, 52, 400, -0.01], 'display-m': ['display', 32, 36, 400, 0],
    'heading-l': ['sans', 28, 34, 600, 0], 'heading-m': ['sans', 22, 28, 600, 0], 'heading-s': ['sans', 18, 24, 600, 0],
    'body-l': ['sans', 18, 29, 400, 0], body: ['sans', 16, 26, 400, 0], 'body-s': ['sans', 14, 21, 400, 0],
    label: ['mono', 12, 16, 500, 0.08, true], code: ['mono', 14, 22, 400, 0], specimen: ['display', 14, 18, 400, 0],
  };
  /** CSS for a type style at film zoom, optionally scaled (k) for hero moments. */
  function type(style, k = 1) {
    const [fam, size, line, weight, track, upper] = TYPE[style];
    return `font-family:${FONT[fam]};font-size:${size * Z * k}px;line-height:${line * Z * k}px;font-weight:${weight};letter-spacing:${track}em;${upper ? 'text-transform:uppercase;' : ''}`;
  }
  const px = (v) => `${v}px`;

  // ---------- DOM helpers
  const NS = 'http://www.w3.org/2000/svg';
  function h(tag, attrs = {}, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null) continue;
      if (k === 'style') e.style.cssText = v; else if (k === 'text') e.textContent = v; else if (k === 'html') e.innerHTML = v; else e.setAttribute(k, v);
    }
    for (const k of kids.flat()) if (k != null) e.append(k);
    return e;
  }
  function s(tag, attrs = {}, ...kids) {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) if (v != null) e.setAttribute(k, v);
    for (const k of kids.flat()) if (k != null) e.append(k);
    return e;
  }
  /** set style props only when they change (keeps renderAt cheap) */
  function css(el, props) {
    const c = el.__css || (el.__css = {});
    for (const [k, v] of Object.entries(props)) { if (c[k] !== v) { el.style[k] = v; c[k] = v; } }
  }
  const op = (v) => (Math.round(clamp(v) * 1000) / 1000).toString();

  window.FILM = { FMT, Z, BEAT, BAR, bb, bezier, EASE, SWEEP, LINEAR, clamp, lerp, seg, env, rng, C, FONT, TYPE, type, px, h, s, css, op, params };
})();
