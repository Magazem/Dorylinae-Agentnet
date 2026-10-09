// S12 · Address · 57.600 → 63.000 (25:1 → 27:2), 5.4 s. SCRIPT.md §6 (end card), §5.3.
// State change from S11, no cut: the same end stage, with the boot held at its finished frame (the lockup is the boot's
// own last frame, pixel for pixel). The address enters below the clear space and holds 4.56 s. At 62.400 a hard cut to
// the bare sky leaves 0.6 s of ground, so an autoplay loop never clips the address. Nothing else is on screen.
FILM.shot({ id: 'S12', start: 57.6, end: 63.0, async build(root) {
  const F = FILM, { C, S, ui, css } = F;
  const P = S.P;
  const BOOT_AT = 53.4, CUT = 62.4;
  const { box, boot } = await F.endStage(root);

  // Address: Special Elite 64 px `text`, centred x 960 baseline 930 (x 586.9 → 1333.1; cap top 885.9, 117.9 px below
  // the lockup) / 4:5 centred x 540 baseline 1060 (x 166.9 → 913.1; cap top 1015.9, 126.7 px below the lockup).
  // 57.600: enters 240 ms, opacity 0 → 1, rise 12 px (4:5 18 px); lands 57.840; held to 62.400.
  const R = S.ROLE.address;
  const addr = ui.text('Apply at dorylinae.net', { face: R.face, size: R.size, colour: R.colour, x: P ? 540 : 960, baseline: P ? 1060 : 930,
    anchor: 'middle', enter: { at: 57.6 }, visible: (t) => t < CUT });
  root.append(addr.el);

  // ---- sound (SCRIPT.md §5.3)
  F.cue(57.6, 'ADDRESS', { note: 'D2', desc: 'one low felt note under the address' });
  F.cue(62.4, 'SILENCE', { until: 63.0, desc: 'digital silence' });

  return (tl, t) => {
    boot.render(t - BOOT_AT);                                   // ≥ 4.2 s on the boot's clock: its finished frame
    css(box, { visibility: t < CUT ? 'visible' : 'hidden' });  // 62.400: hard cut to the bare sky
    addr.render(t);
  };
} });
