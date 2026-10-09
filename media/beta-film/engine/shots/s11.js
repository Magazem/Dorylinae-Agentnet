// S11 · Boot · 52.800 → 57.600 (23:1 → 25:1), 4.8 s. SCRIPT.md S11, §5.2, §5.3.
// Hard cut to the bare end sky (seed 1444) and one beat of silence. At 53.400 the brand's boot plays unchanged for 3.6 s:
// FILM.boot drives the brand's own assets/boot.svg frame-exactly (render(t − 53.400)). From 57.000 the finished lockup
// holds alone for one beat. No camera (it never rolls), no tag, no copy, no bloom or shadow; the scanlines are static.
// S12 continues from this exact frame (state change, no cut): it builds the same end stage (FILM.endStage below).
(() => {
  const F = FILM;
  const BOOT_AT = 53.4;

  /**
   * The end stage shared by S11 and S12: the end sky and the boot lockup at its script position.
   * Boot container (viewBox −566.59 −90 1133.19 1042.27): 16:9 left 552.25, top 85.23, width 815.5 (signal mark 600 px
   * wide, top y 150, centred x 960, lockup bottom 768.0); 4:5 left 105.07, top 160.91, width 869.9 (mark 640 px wide,
   * top y 230, centred x 540, lockup bottom 889.2). Returns { sky, box, boot } (boot.render(t) on the boot's own clock).
   */
  F.endStage = async function endStage(root) {
    const { C, S, h, FMT } = F;
    const P = FMT.portrait;
    root.style.background = C.bg;
    const sky = S.sky(root, 'end');
    sky.render({ x: FMT.W / 2, y: FMT.H / 2, s: 1 });          // no camera: the sky sits at rest
    const G = P ? { left: 105.07, top: 160.91, width: 869.9 } : { left: 552.25, top: 85.23, width: 815.5 };
    const box = h('div', { style: `position:absolute;left:${G.left}px;top:${G.top}px;width:${G.width}px;line-height:0` });
    root.append(box);
    const boot = await F.boot(box, { width: G.width });
    // S11 and S12 each inline the boot SVG: give this copy's clipPath ids (b-al, b-ar, b-c) a per-shot suffix so each
    // copy clips with its own paths (an id must be unique in the page). Geometry and pixels are unchanged.
    const sfx = `-${root.dataset.id ?? 'end'}`;
    const ids = [...box.querySelectorAll('[id]')].map((el) => { const old = el.id; el.id = old + sfx; return old; });
    for (const el of box.querySelectorAll('*')) {
      for (const a of [...el.attributes]) {
        if (!a.value.includes('url(#')) continue;
        const v = a.value.replace(/url\(#([^)]+)\)/g, (m, id) => (ids.includes(id) ? `url(#${id}${sfx})` : m));
        if (v !== a.value) el.setAttribute(a.name, v);
      }
    }
    return { sky, box, boot };
  };

  FILM.shot({ id: 'S11', start: 52.8, end: 57.6, async build(root) {
    const { boot } = await F.endStage(root);

    // ---- sound (SCRIPT.md §5.3): silence, then the boot cue (6 beats)
    F.cue(52.8, 'SILENCE', { until: 53.4, desc: 'one beat of silence before the end card' });
    [['D2', 53.55], ['A2', 53.75], ['D3', 53.95], ['F#3', 54.15], ['A3', 54.35], ['D4', 54.54]]
      .forEach(([note, t]) => F.cue(t, 'RIPPLE', { note, desc: 'boot ripple: follows the plate locks' }));
    F.cue(54.6, 'AIR', { until: 55.35, desc: 'filtered-air sweep under the glint sweep' });
    F.cue(54.75, 'BLOOM', { notes: ['D3', 'F#3', 'A3', 'E4'], attack: 1.2, desc: 'the signal mark' });
    // DORYLINAE typed: one LABEL per letter, at the boot's own letter times (so picture and sound agree), −38 dBFS.
    for (const d of boot.letterTimes) F.cue(BOOT_AT + d, 'LABEL', { word: 'DORYLINAE', db: -2 });

    return (tl, t) => boot.render(t - BOOT_AT);
  } });
})();
