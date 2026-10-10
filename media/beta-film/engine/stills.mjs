#!/usr/bin/env node
// Stills for review: render chosen times to PNG and (optionally) a contact sheet.
//   node stills.mjs --aspect 16x9 --times 0,1.2,3.6 --out out/stills [--sheet sheet.png] [--cols 4] [--width 480] [--debug]
//   node stills.mjs --aspect 16x9 --every 0.6 --from 0 --to 12 --sheet out/sheet.png
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
const here = path.dirname(fileURLToPath(import.meta.url));
const argv = Object.fromEntries(process.argv.slice(2).reduce((a, x, i, all) => { if (x.startsWith('--')) a.push([x.slice(2), all[i + 1] && !all[i + 1].startsWith('--') ? all[i + 1] : 'true']); return a; }, []));
const aspect = argv.aspect ?? '16x9';
const W = aspect === '4x5' ? 1080 : 1920, H = aspect === '4x5' ? 1350 : 1080;
let times = argv.times ? argv.times.split(',').map(Number) : [];
if (argv.every) { const e = +argv.every, a = +(argv.from ?? 0), b = +(argv.to ?? 10); for (let t = a; t <= b + 1e-9; t += e) times.push(+t.toFixed(4)); }
const out = path.resolve(argv.out ?? path.join(here, 'out', 'stills'));
fs.mkdirSync(out, { recursive: true });
// --disable-partial-raster: every frame rastered whole, so a still is the same pixels as that frame in render.mjs (see there)
const browser = await chromium.launch({ args: ['--font-render-hinting=none', '--disable-lcd-text', '--force-color-profile=srgb', '--hide-scrollbars', '--disable-partial-raster'] });
const ctx = await browser.newContext({ viewport: { width: W, height: H }, deviceScaleFactor: Number(argv.dsf ?? 1) });
const page = await ctx.newPage();
page.on('pageerror', (e) => { console.error('[scene error]', e.message); process.exitCode = 1; });
page.on('console', (m) => { if (m.type() === 'error' || m.type() === 'warning') console.error('[scene]', m.text()); });
await page.goto(pathToFileURL(path.join(here, 'film.html')).href + `?format=${aspect}` + (argv.cut ? `&cut=${argv.cut}` : '') + (argv.debug ? '&debug=1' : ''));
await page.evaluate(() => window.ready);
const files = [];
for (const t of times) {
  await page.evaluate((tt) => window.renderAt(tt), t);
  const f = path.join(out, `${aspect}_${t.toFixed(3).padStart(8, '0')}.png`);
  await page.screenshot({ path: f });
  files.push(f);
}
const cues = await page.evaluate(() => window.CUES);
fs.writeFileSync(path.join(out, `cues-${aspect}.json`), JSON.stringify(cues, null, 1));
await browser.close();
if (argv.sheet) {
  const cols = Number(argv.cols ?? 4), w = Number(argv.width ?? 480);
  const rows = Math.ceil(files.length / cols);
  const list = path.join(out, 'list.txt');
  fs.writeFileSync(list, files.map((f) => `file '${f}'`).join('\n'));
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', list,
    '-vf', `scale=${w}:-1:flags=lanczos,tile=${cols}x${rows}:padding=4:color=0x222222`, '-frames:v', '1', path.resolve(argv.sheet)]);
  console.log('sheet', argv.sheet);
}
console.log(files.length, 'stills in', out);
