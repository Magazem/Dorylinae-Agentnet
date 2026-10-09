// Renders ONLY the deep field's dust layers (src/field.ts: the d1 and d2 rects), white on black, for one sky:
//   npx tsx film-gen/dust-only.ts <out.png> <seed> <w> <h> <nebula> <vignette>
// The film subtracts most of this from its sky PNGs (dust is per-pixel noise that video codecs smear into blocks).
import fs from 'node:fs';
import sharp from 'sharp';
import { fieldSvg } from '../src/field.js';
const [out, seed, W, H, nebula, vignette] = process.argv.slice(2);
let svg = fieldSvg({ w: +W, h: +H, seed: +seed, nebula: +nebula, vignette: +vignette, density: 0 });
const defs = svg.match(/<defs>[\s\S]*?<\/defs>/)![0];
const dust = [...svg.matchAll(/<rect [^>]*filter="url\(#f-d[12]\)"[^>]*\/>/g)].map((m) => m[0]).join('');
if (!dust) throw new Error('no dust rects found');
// the vignette is drawn over the dust in the real sky, so it attenuates the dust the same way here
const vig = (svg.match(/<rect [^>]*fill="url\(#f-v\)"[^>]*\/>/) || [''])[0];
svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" width="${W}" height="${H}">${defs}<rect width="${W}" height="${H}" fill="#000"/>${dust}${vig}</svg>`;
await sharp(Buffer.from(svg), { limitInputPixels: false, density: 72 }).png({ compressionLevel: 3 }).toFile(out + '.tmp.png');
fs.renameSync(out + '.tmp.png', out);
console.log(out, (dust.match(/<rect/g) || []).length, 'dust rects');
