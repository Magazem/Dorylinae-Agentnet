// Section III · The card · G56 → G65 (15:1 → 17:2) · 40.000 → 46.429 s. SCRIPT-v2 §5.3, §6 (K-1 … K-4).
// Name what the viewer just saw, with hierarchy, plus the one fact about control, plus the honest limit.
//
//   G56    hard cut. The card (label stock, inset hairline) on the `inv` sky with its display line `End-to-end encrypted.`
//   G57    line 2 `Requests wait while your teammate is offline.` (ink: one frame)
//   G58    line 3 `You approve what their agent can read.`
//   G59.5  the footnote `No independent security review yet.` (O-1, SHOW_REVIEW_FOOTNOTE; off: the owner's decision)
//   G56→65 the card holds still (dirfix; was a drift s 1.00 → 1.02 about the card's centre, see below); its sky alone
//          drifts 0.4 px/s to its rest place at the cut (final polish, skyDrift)
//   G65    hard cut
//
// No pointer, no moving underline, no icons, no logo, no sound accent (§8 lists none here). The card itself is
// FILM.V2.card (common-v2.js): its r3 inset (12 / 10 px, 2 px line, gate r2) replaces §5.3's 6 / 8 px, and without the
// footnote it re-balances (ends as far under line 3 as it did under the footnote, centred in the frame again).
(() => {
  // ================================================================ owner decision (one-line change)
  // O-1 (§5.3): the card's footnote `No independent security review yet.`. The owner's final answer (design/BRIEF-v2.md,
  // "Owner decisions after the v2 script (final)"): no footnote on the card; the disclosure goes in the invitation, privacy
  // note and terms. audio2/score2.py (CARD_FOOTNOTE = False) matches. This constant is the film's only switch for it: the
  // card is built with it explicitly (common-v2.js's own SHOW_REVIEW_FOOTNOTE is only the gallery's default).
  const SHOW_REVIEW_FOOTNOTE = false;

  const F = window.FILM, V = F.V2;
  const { T } = V;

  /**
   * The card layers on the `inv` sky (V2.sky 'inv': in 16:9 the repaired copy assets/sky-v2inv2-169, with its four partial
   * star removals left out cleanly, scratchpad/b1345tools/inv-sky.sh; 4:5 V2's own; both at display resolution, dirfix).
   * The sky drifts very slowly to its rest place (skyDrift, final polish) and section IV's holds there, so the hard cut at
   * G65 changes the object only.
   */
  function invLayers(root) {
    const sk = V.sky(root, 'inv');
    sk.render(null);                                   // the camera at rest: c = frame centre, s = 1
    return { ...V.layers(root, { place: null }), sky: sk };   // apply(c) moves the world (the card) only
  }
  /**
   * final polish (director, item 15): the card stays pixel-still for 5 s after its last line, so the sky alone drifts,
   * very slowly: SKY_DRIFT px/s to the left, linear, from SKY_DRIFT × 6.43 s (2.6 px) right of its rest place at G56 to
   * exactly its rest place at the G65 cut, where section IV's sky (the same image, at rest) takes over without a jump.
   * Scale stays 1 on the display-resolution sky (Chromium never changes its image filter). A fractional translate is not
   * smooth: Skia samples an image at 1/16 px positions, so the sky moved in 0.0625 px ticks every ~10 frames, each a
   * one-frame change of up to 13 levels on ~430 stars' px (measured, qa/final/scene-polish/drift). So the sub-pixel part is
   * drawn as a blend of two whole-pixel placements of the same opaque image: the sky at floor(ox) and, over it, at
   * floor(ox) + 1 with opacity frac(ox) (exactly a linear resample in x, in 1/255 steps: ~1 level a frame).
   */
  const SKY_DRIFT = 0.4;
  function skyDrift(sk) {
    const a = sk.el, b = a.cloneNode();
    a.after(b);
    const w = parseFloat(a.style.width), hh = parseFloat(a.style.height);
    const bx = (F.FMT.W - w) / 2, by = (F.FMT.H - hh) / 2;     // the rest place (whole px in both formats)
    return (t) => {
      const ox = SKY_DRIFT * Math.max(0, T(65) - t), n = Math.floor(ox), fr = ox - n;
      F.css(a, { transform: `translate(${bx + n}px, ${by}px)` });
      F.css(b, { transform: `translate(${bx + n + 1}px, ${by}px)`, opacity: F.op(fr), display: fr > 0 ? 'block' : 'none' });
    };
  }

  /**
   * dirfix: no camera drift on the card. Under the 1.00 → 1.02 drift (0.03 px a frame at the card's edges) every glyph was
   * re-rasterised each frame and jumped ±0.1 – 0.4 px back and forth (line 2's ink centroid: jitter rms 0.20 px against an
   * expected motion under 0.04 px a frame; ~6,000 card px changed > 6 levels every frame): the text simmered through the
   * reading hold. `will-change: transform` hid it but made frames depend on render history (not renderAt-pure). The card
   * holds still at CAM rest, as the sky already did; the three lines inking on their beats are the section's motion.
   */
  const REST = { x: F.FMT.W / 2, y: F.FMT.H / 2, s: 1 };
  FILM.shot({ id: 'III', start: T(56), end: T(65), build(root) {
    const L = invLayers(root);
    const card = V.card(L.world, { at: [T(56), T(57), T(58), T(59.5)], footnote: SHOW_REVIEW_FOOTNOTE });
    const drift = skyDrift(L.sky);
    return (tl, t) => { L.apply(REST); drift(t); card.render(t); };
  } });
})();
