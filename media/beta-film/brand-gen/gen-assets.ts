// Generates the brand assets the film uses, straight from the brand's own source code.
//   npx tsx film-gen/gen-assets.ts <outDir> [sky <seed> <w> <h> <name> [nebula] [vignette]]...
import fs from 'node:fs';
import path from 'node:path';
import sharp from 'sharp';
import { fieldSvg } from '../src/field.js';
import { markSvg, plates, markBox } from '../src/mark.js';
import { lockupSvg, lockupParts } from '../src/lockup.js';
import { heroMark, heroLockup, svg } from '../src/compose.js';
import { bootSvg, BOOT_SECONDS } from '../src/motion.js';
import { logo } from '../src/tokens.js';
import { MARK_BOX, SCAN } from '../src/geometry.js';

const out = path.resolve(process.argv[2]);
fs.mkdirSync(out, { recursive: true });
const args = process.argv.slice(3);
const w = (f: string, s: string) => { fs.writeFileSync(path.join(out, f), s); console.log(f, s.length); };

if (!args.length || args[0] === 'logo') {
  w('mark-signal.svg', markSvg({ style: 'signal', id: 'ms' }));
  w('mark-flat.svg', markSvg({ style: 'flat', fill: logo.amethyst }));
  w('lockup-horizontal.svg', lockupSvg({ layout: 'horizontal', fill: logo.amethyst }));
  w('lockup-stacked.svg', lockupSvg({ layout: 'stacked', fill: logo.amethyst }));
  w('wordmark.svg', lockupSvg({ layout: 'wordmark', fill: logo.amethyst }));
  const hm = heroMark('hm', { glow: true, shade: 0 });
  w('mark-hero.svg', `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${hm.box.x} ${hm.box.y} ${hm.box.w} ${hm.box.h}">${hm.body}</svg>`);
  const hl = heroLockup('hl', { layout: 'stacked', glow: true, shade: 0 });
  w('lockup-stacked-hero.svg', `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${hl.box.x} ${hl.box.y} ${hl.box.w} ${hl.box.h}">${hl.body}</svg>`);
  w('boot.svg', bootSvg({ width: 900, ground: 'none' }));
  w('brand.json', JSON.stringify({ BOOT_SECONDS, MARK_BOX, SCAN, markBox: markBox('full'), lockupStacked: lockupParts({ layout: 'stacked' }).box, plates: plates('full'), logo }, null, 1));
}
for (let i = 0; i < args.length; i++) {
  if (args[i] !== 'sky') continue;
  i += 0;
  const [seed, W, H, name, nebula, vignette, density] = args.slice(i + 1, i + 8);
  // density is stars per 10,000 px² of the PNG: a 2× PNG shown at half size needs a quarter of the brand's 9
  const s = fieldSvg({ w: +W, h: +H, seed: +seed, nebula: nebula ? +nebula : 1, vignette: vignette ? +vignette : 0.55, density: density ? +density : 9 });
  const t0 = Date.now();
  await sharp(Buffer.from(s), { limitInputPixels: false, density: 72 }).png({ compressionLevel: 6 }).toFile(path.join(out, name + '.tmp.png'));
  fs.renameSync(path.join(out, name + '.tmp.png'), path.join(out, name));   // atomic swap: renders in progress never read half a file
  console.log(name, `${W}x${H}`, `${Date.now() - t0} ms`);
}
