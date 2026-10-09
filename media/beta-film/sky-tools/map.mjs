// Screen -> sky PNG mapping and the screen boxes of a shot's text and graphics.
//   node map.mjs --aspect 16x9 --shot S9 --times 41,42 --out dir
// Phase 1 (normal render): every text line fragment, every element with a background or border, every SVG shape of
// the shot root, at each time -> items.json. Phase 2: the shot's sky <img> is swapped for coordinate-encoding PNGs
// (R,G = coordinate, B = 200), drawn pixelated with everything else hidden, and screenshot at each time (x and y).
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const E = '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/engine';
const { chromium } = createRequire(E + '/render.mjs')('playwright');
const argv = Object.fromEntries(process.argv.slice(2).reduce((a, x, i, all) => { if (x.startsWith('--')) a.push([x.slice(2), all[i + 1]]); return a; }, []));
const aspect = argv.aspect, shot = argv.shot, times = argv.times.split(',').map(Number), out = argv.out;
const W = aspect === '4x5' ? 1080 : 1920, H = aspect === '4x5' ? 1350 : 1080;
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ args: ['--font-render-hinting=none', '--disable-lcd-text', '--force-color-profile=srgb', '--hide-scrollbars'] });
const page = await (await browser.newContext({ viewport: { width: W, height: H }, deviceScaleFactor: 1 })).newPage();
page.on('pageerror', (e) => console.error('[scene error]', e.message));
await page.goto(pathToFileURL(path.join(E, 'film.html')).href + '?format=' + aspect);
await page.evaluate(() => window.ready);
const items = {};
for (const t of times) {
  await page.evaluate((tt) => window.renderAt(tt), t);
  items[t] = await page.evaluate(([id, W, H]) => {
    const root = document.querySelector('.shot[data-id="' + id + '"]');
    const res = [];
    const R = (q) => [q.left, q.top, q.right, q.bottom].map((v) => +v.toFixed(2));
    const tw = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    while (tw.nextNode()) {
      const n = tw.currentNode; if (!n.textContent.trim()) continue;
      const r = document.createRange(); r.selectNodeContents(n);
      for (const q of r.getClientRects()) if (q.width > 0 && q.height > 0) res.push({ kind: 'text', label: n.textContent, rect: R(q) });
    }
    for (const el of root.querySelectorAll('*')) {
      if (el.tagName === 'IMG' || el.tagName === 'CANVAS' || el.closest('[data-quiet]')) continue;
      const cs = getComputedStyle(el);
      const tag = el.tagName.toLowerCase();
      const svgShape = el instanceof SVGGraphicsElement && !['svg', 'g', 'defs', 'filter', 'clippath', 'mask', 'lineargradient', 'radialgradient'].includes(tag);
      const bg = cs.backgroundColor && cs.backgroundColor !== 'transparent' && !/,\s*0\)$/.test(cs.backgroundColor);
      const border = parseFloat(cs.borderTopWidth) > 0 && cs.borderTopStyle !== 'none';
      if (!(svgShape || bg || border)) continue;
      const q = el.getBoundingClientRect();
      if (q.width * q.height > 0.5 * W * H || (q.width === 0 && q.height === 0)) continue;
      res.push({ kind: svgShape ? 'svg:' + tag : 'box', label: (el.textContent || '').trim().slice(0, 40), rect: R(q) });
    }
    return res;
  }, [shot, W, H]);
}
fs.writeFileSync(path.join(out, 'items.json'), JSON.stringify({ aspect, shot, times, items }));
// phase 2
const sky = await page.evaluate((id) => {
  const root = document.querySelector('.shot[data-id="' + id + '"]');
  const img = [...root.querySelectorAll('img')].find((i) => /sky-/.test(i.src));
  img.classList.add('__coord'); img.style.imageRendering = 'pixelated';
  const st = document.createElement('style');
  st.textContent = '.shot, .shot * { visibility: hidden !important; } .shot { background: none !important; } #stage { background: #000 !important; } img.__coord { visibility: visible !important; }';
  document.head.append(st);
  return { src: img.src, w: img.naturalWidth, h: img.naturalHeight };
}, shot);
console.log('sky', sky);
for (const ax of ['x', 'y']) {
  const url = pathToFileURL(path.join('/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/qa/sky-clear/coord', 'coord-' + sky.w + 'x' + sky.h + '-' + ax + '.png')).href;
  await page.evaluate(async (u) => { const img = document.querySelector('img.__coord'); img.src = u; await img.decode(); }, url);
  for (const t of times) {
    await page.evaluate((tt) => window.renderAt(tt), t);
    await page.screenshot({ path: path.join(out, 'c' + ax + '_' + t.toFixed(3) + '.png') });
  }
}
fs.writeFileSync(path.join(out, 'sky.json'), JSON.stringify(sky));
await browser.close();
console.log('done', out);
