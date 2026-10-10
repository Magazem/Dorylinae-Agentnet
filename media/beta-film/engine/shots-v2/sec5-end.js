// Section V · Boot and address · G80 → G90 (21:1 → 23:3) · 57.143 → 64.286 s. SCRIPT-v2 §5.5, §6 (E-1).
// As v1 S11 and S12, unchanged except the times.
//
//   G80  hard cut to the bare `end` sky (seed 1444), on frame 3428 = 57.1333 s (CUT4: an even frame, so the 30 fps pair
//        blend never mixes sections IV and V; G80 itself, 57.143, falls inside frame 3428). The brand boot plays unchanged
//        from the cut (FILM.boot via V2.endStage, render(t − 57.1333), its frames on the film's frame grid): plates, glint,
//        signal, the name typed (its last letter at +2.58 s = 59.713 s); nominal end 60.733. No camera, no bloom or
//        shadow, scanlines static. TYPE ×9 at −38 dBFS on the boot's own letter times (exported from here, so they move
//        with the boot).
//   G84  `Apply at dorylinae.net` enters (240 ms, rise 12 / 18 px, brand ease) in the boot's final hold; landed 60.240
//   G90  end: the address is held 4.05 s, to the last frame (3856); the music is silent from 63.857
(() => {
  const F = window.FILM, V = F.V2;
  const { FMT, css, seg } = F;
  const { T, pick } = V;
  // cut 4 on frame 3428 (sec4-invite.js has the same CUT4, 1 µs early so no frame time can fall 1 ulp short of it;
  // director's notes, v2 QA): section V starts there and the boot runs on the film's frame grid from that frame
  const CUT4 = 3428 / V.FPS - 1e-6;
  const BOOT_AT = 3428 / V.FPS, ADDR_AT = T(84);

  /**
   * V2.endStage (v1's end stage: the end sky and the boot at its script position), with one change: the sky is the v2 copy
   * of v1's `end` sky with the stars around the address left out (assets/sky-v2end-*.png, made by qa/v2-paper-sky/clear.py
   * over the address box grown by 28 px: scratchpad/b1345tools/end-sky.sh). v1's sky-end put a star 9 px under the baseline between
   * `r` and `y`, and others within a few px of the glyphs. Same size, follow and rest position as v1's sky.
   * Director's notes (v2 QA): the zones (b1345tools/zend169.json, zend45.json; thresholds still 28 / 3) also hold a band
   * over the DORYLINAE wordmark (16:9 PNG [1216, 1326, 2624, 1554], 4:5 [326, 1556, 1834, 1794]), and small boxes (star
   * bbox + 3 px) round the 45 / 22 conspicuous stars (peak >= 60 or >= 6 px) that showed in the mark's counters and channels
   * or touched its outline in the final lockup (made by qa/dirfix/sec145/starboxes.py). Faint dust is kept, so the bare
   * sky at the cut keeps its texture where the mark will assemble.
   * fix-r3 (review r2, frame): a second pass (tools/v2-endsky-addr.sh) clears 40 px round the address's ink box (was 28):
   * a 44 – 84 % star sat 32 – 45 px off the glyphs in each format, held 4 s; now nothing within 40 px is over 27 %.
   */
  async function endStage(root) {
    const { C, h, ui } = F;
    root.style.background = C.bg;
    const sk = ui.sky(root, { name: `sky-v2end-${V.P ? '45' : '169'}.png`, w: FMT.W, h: FMT.H, follow: 0.3 });
    sk.render({ x: FMT.W / 2, y: FMT.H / 2, s: 1 });             // no camera: the sky sits at rest
    const G = V.P ? { left: 105.07, top: 160.91, width: 869.9 } : { left: 552.25, top: 85.23, width: 815.5 };
    const box = h('div', { style: `position:absolute;left:${G.left}px;top:${G.top}px;width:${G.width}px;line-height:0` });
    root.append(box);
    const boot = await F.boot(box, { width: G.width });
    // this inlined copy of boot.svg gets its own clipPath ids (an id must be unique in the page)
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

  FILM.shot({ id: 'V', start: CUT4, end: F.duration ?? T(90), async build(root) {
    const { boot } = await endStage(root);
    // Special Elite 64, −0.01 em, `text`, centred x 960 baseline 930 (16:9) / x 540 baseline 1060 (4:5)
    const addr = V.text(V.COPY.ADDRESS, { role: 'address', x: FMT.W / 2, anchor: 'middle', baseline: pick(930, 1060) });
    // dirfix: on the world's raster path (V2.stableText, a permanent 0.05° roll), so its rise glides instead of stepping
    // whole pixels (the last 1 px tick landed on frame 3609, ~95 % opaque)
    const holder = F.h('div', { style: `position:absolute;left:0;top:0;width:${FMT.W}px;height:${FMT.H}px;${V.stableText(FMT.W / 2, pick(930, 1060))}` });
    holder.append(addr.el);
    root.append(holder);
    const RISE = pick(12, 18);

    // fix-r3: the boot's glint sweeps down the mark in 0.75 s from +1.2 s, 18.5 px a frame at its peak at 60 fps, 37 at
    // 30: a blur window for the 30 fps delivery only (V2.blur30), so it sweeps there too instead of stepping
    V.blur30(BOOT_AT + 1.2, BOOT_AT + 1.95);

    // ---- sound (§8): DORYLINAE typed by the boot, one TYPE per letter on the boot's own letter times, boot level
    boot.letterTimes.forEach((d, i) => F.cue(BOOT_AT + d, 'TYPE', { level: 'boot', desc: `boot letter ${'DORYLINAE'[i] ?? i}` }));

    return (tl, t) => {
      boot.render(t - BOOT_AT);
      const a = seg(t, ADDR_AT, 0.24);
      css(addr.el, { opacity: F.op(a), transform: a < 1 ? `translateY(${(RISE * (1 - a)).toFixed(3)}px)` : 'none' });
    };
  } });
})();
