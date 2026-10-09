import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const E = '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/qa/sky-clear/../../engine';
const { chromium } = createRequire(E + '/render.mjs')('playwright');
const browser = await chromium.launch({ args: ['--font-render-hinting=none', '--disable-lcd-text', '--force-color-profile=srgb', '--hide-scrollbars'] });
const page = await (await browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
await page.goto(pathToFileURL(path.join(E, 'film.html')).href + '?format=16x9');
await page.evaluate(() => window.ready);
await page.evaluate(() => window.renderAt(52.2));
console.log(await page.evaluate(() => {
  const root = document.querySelector('.shot[data-id="S10"]');
  return [...root.querySelectorAll('canvas')].map((c) => { const r = c.getBoundingClientRect(); const cs = getComputedStyle(c); const o = c.parentElement.parentElement; return { r: [r.left, r.top, r.width, r.height], vis: cs.visibility, disp: cs.display, outer: o.getAttribute('style').slice(0, 120), z: getComputedStyle(o).zIndex, idx: [...root.children].indexOf(o), kids: [...root.children].map((k) => k.tagName + (k.className ? '.' + k.className : '')).join(' ') }; });
}));
await page.screenshot({ path: '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/qa/sky-clear/an/dbg-s10.png', clip: { x: 1280, y: 120, width: 640, height: 140 } });
await page.evaluate(() => window.renderAt(52.21));
await page.screenshot({ path: '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/qa/sky-clear/an/dbg-s10b.png', clip: { x: 1280, y: 120, width: 640, height: 140 } });
await browser.close();
