// For the star-to-glyph check: each time rendered as-is (a_) and with only the shot's sky visible (b_).
//   node check.mjs --aspect 16x9 --shot S9 --times 44 --out dir
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
page.on('console', (m) => { if (m.type() === 'error') console.error('[scene]', m.text()); });
await page.goto(pathToFileURL(path.join(E, 'film.html')).href + '?format=' + aspect);
await page.evaluate(() => window.ready);
for (const t of times) {
  await page.evaluate((tt) => window.renderAt(tt), t);
  await page.screenshot({ path: path.join(out, 'a_' + t.toFixed(3) + '.png') });
}
await page.evaluate((id) => {
  // sky-only: hide every direct child of the shot root that is not the sky (the sky <img>, or a holder of the sky
  // image or of a quieted sky copy <canvas>, or a defs-only <svg>), so the sky is exactly as in the real frame
  const root = document.querySelector('.shot[data-id="' + id + '"]');
  for (const k of root.children) {
    const tag = k.tagName.toLowerCase();
    const sky = tag === 'img' || k.querySelector('img, canvas') || (tag === 'svg' && !k.querySelector('circle, path, line, rect, text, image'));
    if (!sky) k.classList.add('__hide');
  }
  const st = document.createElement('style');
  st.textContent = '.__hide { visibility: hidden !important; }';
  document.head.append(st);
}, shot);
for (const t of times) {
  await page.evaluate((tt) => window.renderAt(tt), t);
  await page.screenshot({ path: path.join(out, 'b_' + t.toFixed(3) + '.png') });
}
await browser.close();
console.log('done', out);
