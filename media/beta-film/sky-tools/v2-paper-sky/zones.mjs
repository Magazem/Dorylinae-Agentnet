// Map the v2 paper rest edges and trail lanes into sky-PNG px, frame by frame, for the scene sky and the inv sky.
//   node zones.mjs <engine dir> <16x9|4x5> <out.json>
// Each zone: { sky, kind: 'ring' | 'band', outer: [x0,y0,x1,y1], inner?: [...] } in PNG px (2x display).
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const [E, fmt, out] = process.argv.slice(2);
const browser = await chromium.launch();
const page = await (await browser.newContext({ viewport: { width: fmt === '4x5' ? 1080 : 1920, height: fmt === '4x5' ? 1350 : 1080 } })).newPage();
await page.goto(pathToFileURL(path.join(E, 'film.html')).href + `?format=${fmt}&cut=gallery`);
await page.evaluate(() => window.ready);
const zones = await page.evaluate(() => {
  const F = window.FILM, V = F.V2, { W, H } = F.FMT, P = F.FMT.portrait, pick = V.pick, T = V.T, G = V.GEO;
  const out = [];
  const skyT = (c, sw, sh) => { // ui.sky's transform for camera c: display = (screen - t) / s1
    const s1 = 1 + 0.3 * (c.s - 1), bx = (W - sw) / 2, by = (H - sh) / 2;
    return { s1, tx: W / 2 + (bx - W / 2) * s1 - 0.3 * (c.x - W / 2) * c.s, ty: H / 2 + (by - H / 2) * s1 - 0.3 * (c.y - H / 2) * c.s };
  };
  const toPng = (c, st, [x, y]) => { const sx = W / 2 + (x - c.x) * c.s, sy = H / 2 + (y - c.y) * c.s; return [2 * (sx - st.tx) / st.s1, 2 * (sy - st.ty) / st.s1]; };
  const rectPng = (c, st, [x0, y0, x1, y1], m) => { // world rect, grown by m screen px, in PNG px
    const [a, b] = toPng(c, st, [x0, y0]), [d, e] = toPng(c, st, [x1, y1]), k = (2 * m) / st.s1;
    return [a - k, b - k, d + k, e + k].map((v) => +v.toFixed(1));
  };
  const each = (t0, t1, fn) => { for (let f = Math.ceil(t0 * 60); f <= Math.floor(t1 * 60); f++) fn(f / 60); };
  // skyCamAt: the camera the sky is drawn with (it follows at 0.3), when it is not the world's (dirfix: sections III and
  // IV hold their sky at rest while the world pushes)
  function zonesFor(sky, sw, sh, camAt, items, skyCamAt = camAt) {
    for (const it of items) each(it.win[0], it.win[1], (t) => {
      const c = camAt(t), st = skyT(skyCamAt(t), sw, sh);
      if (it.ring) out.push({ sky, kind: 'ring', outer: rectPng(c, st, it.ring, it.m ?? 10), inner: rectPng(c, st, it.ring, -3) });
      else { const [[x0, y0], [x1, y1]] = it.lane, r = pick(3, 4.5) + 10; out.push({ sky, kind: 'band', outer: rectPng(c, st, [Math.min(x0, x1), Math.min(y0, y1), Math.max(x0, x1), Math.max(y0, y1)], r) }); }
    });
  }
  // ---- the scene (cam.scene, script time)
  const sceneSky = `sky-v2scene-${P ? '45' : '169'}.png`, [ssw, ssh] = pick([3400, 1500], [1500, 2900]);
  const camScene = V.cam.scene();
  const env = (at, flapOpen = false) => [at[0], at[1] - (flapOpen ? V.ENV.scene.flapH : 0), at[0] + G.env.w, at[1] + G.env.h];
  const B = V.SHEETS.brief, A = V.SHEETS.answer;
  const rc = [G.lane === 540 ? 540 : G.envAt.relay[0] + G.env.w / 2, G.envAt.relay[1] + G.env.h / 2];
  const md = G.maya.dock, td = G.theo.dock;
  zonesFor(sceneSky, ssw, ssh, camScene, [
    { ring: [G.letterAt.maya[0], G.letterAt.maya[1], G.letterAt.maya[0] + B.w, G.letterAt.maya[1] + B.h], win: [T(8.5) + 0.9, T(12)] },
    { ring: env(G.envAt.maya, true), win: [T(13.25) + 0.75, T(15.6)] },
    { ring: env(G.envAt.maya), win: [T(15.6), T(16.75)] },
    { ring: env(G.envAt.relay), win: [T(20), T(40.25)] },
    { ring: [G.letterAt.theo[0], G.letterAt.theo[1], G.letterAt.theo[0] + B.w, G.letterAt.theo[1] + B.h], win: [T(44) + 0.9, T(46.25)] },
    { ring: [G.answerAt.theo[0], G.answerAt.theo[1], G.answerAt.theo[0] + A.w, G.answerAt.theo[1] + A.h], win: [T(46.9) + 0.6, T(49.75)] },
    { ring: env(G.envAt.theo, !P), win: [T(50.5) + 0.75, T(52)] },
    { ring: env(G.envAt.theo), win: [T(52), T(52.5)] },
    { ring: env(G.envAt.maya), win: [T(55), T(56)] },
    { lane: [md, rc], win: [T(16.75), T(22)] },
    { lane: [rc, td], win: [T(40.25), T(45)] },
    { lane: [td, md], win: [T(52.5), T(56)] },
  ]);
  // ---- the card and the invitation (sky 'inv', a v2 copy)
  const invSky = `sky-inv-${P ? '45' : '169'}.png`, [isw, ish] = pick([2016, 1134], [1134, 1418]);
  // dirfix: the card holds still and both sections hold the sky at rest (sec3-card.js, sec4-invite.js); IV's world pushes
  const rest = () => ({ x: W / 2, y: H / 2, s: 1 });
  // the card with and without the footnote (O-1; the film has none), from V2.card itself (dir-v2 / dirfix: the 4:5 card is
  // laid out for portrait and re-balanced, so its box comes from the component)
  for (const footnote of [true, false]) {
    const st = V.card(document.createElement('div'), { footnote }).el.style;
    const box = [parseFloat(st.left), parseFloat(st.top), parseFloat(st.left) + parseFloat(st.width), parseFloat(st.top) + parseFloat(st.height)];
    // final polish (item 15): section III's sky drifts 0.4 px/s left, from 2.6 px right of rest at G56 to rest at G65
    // (sec3-card.js SKY_DRIFT): the ring follows the sky (ui.sky's follow 0.3: an offset ox is a camera at W/2 - ox / 0.3)
    const drift = (t) => ({ x: W / 2 - (0.4 * Math.max(0, T(65) - t)) / 0.3, y: H / 2, s: 1 });
    zonesFor(invSky, isw, ish, rest, [{ ring: box, win: [T(56), T(65)] }], drift);
  }
  // final polish (director, item 11): section IV's camera is held at s 1.06 / 1.04 about the form for the whole section
  // (fix-r3; it was a push 1 -> 1.06 over G66 -> G68, as this drift still assumed, so the envelope's rest ring was computed
  // at s 1 - 1.06 and 70 / 66 / 46 % stars stayed 10 - 12 px off its s 1.06 edges, 46.75 - 48.5 s). The rings are the
  // rest rectangles at the held scale, grown by 14 px (was 10)
  const camF = V.cam.drift(pick([960, 556], [540, 696]), pick(1.06, 1.04), pick(1.06, 1.04), T(66), T(68));
  zonesFor(invSky, isw, ish, camF, [
    { ring: pick([460, 260, 1460, 720], [90, 420, 990, 834]), win: [T(66), T(68)], m: 14 },
    { ring: pick([480, 130, 1440, 950], [100, 295, 980, 1055]), win: [T(69) + V.HINGE, T(80)], m: 14 },
    { lane: pick([[-400, 490], [960, 490]], [[540, -400], [540, 627]]), win: [T(65), T(67.5)] },
  ], rest);
  return out;
});
fs.writeFileSync(out, JSON.stringify(zones));
console.log(fmt, zones.length, 'zone samples', [...new Set(zones.map((z) => z.sky))].join(' '));
await browser.close();
