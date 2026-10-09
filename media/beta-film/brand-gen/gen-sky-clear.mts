// The brand deep field (src/field.ts, exactly as film-gen/gen-assets.ts renders it), with the countable stars (and their
// halos) left out inside given rectangles, so no star sits in a label's letters or word gaps. Nebula, dust and vignette
// are untouched; nothing is blurred or patched.
//   npx tsx gen-sky-clear.mts <out.png> <seed> <w> <h> <nebula> <vignette> <density> [zones.json]
// zones.json: [[x0, y0, x1, y1, minR], ...] in PNG px. A star (its core and, for a bright one, its halo) is left out when its
// core radius is at least minR and its outermost circle comes within 2 PNG px of the zone. minR 0 clears every star.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
const BRAND = '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/brand/dorylinae-brand';
const require = createRequire(BRAND + '/package.json');
const sharp = require('sharp');
const { fieldSvg } = await import(BRAND + '/src/field.ts');
const [out, seed, W, H, nebula, vignette, density, zonesFile] = process.argv.slice(2);
const zones: number[][] = zonesFile ? JSON.parse(fs.readFileSync(zonesFile, 'utf8')) : [];
let svg: string = fieldSvg({ w: +W, h: +H, seed: +seed, nebula: +nebula, vignette: +vignette, density: +density });
const RE = /<circle cx="([\d.-]+)" cy="([\d.-]+)" r="([\d.-]+)"[^>]*\/>/g;
const stars = new Map<string, { x: number; y: number; core: number; ext: number }>();
for (const m of svg.matchAll(RE)) {
  const k = m[1] + ',' + m[2], r = +m[3];
  const st = stars.get(k) ?? { x: +m[1], y: +m[2], core: Infinity, ext: 0 };
  st.core = Math.min(st.core, r); st.ext = Math.max(st.ext, r); stars.set(k, st);
}
const drop = new Set<string>();
for (const [k, st] of stars) {
  for (const [x0, y0, x1, y1, minR = 0] of zones) {
    if (st.core < minR) continue;
    const dx = Math.max(x0 - st.x, 0, st.x - x1), dy = Math.max(y0 - st.y, 0, st.y - y1), rr = st.ext + 2;
    if (dx * dx + dy * dy <= rr * rr) { drop.add(k); break; }
  }
}
let cut = 0;
svg = svg.replace(RE, (m, cx, cy) => (drop.has(cx + ',' + cy) ? (cut++, '') : m));
console.log(`stars ${stars.size}, left out ${drop.size} (${cut} circles)`);
const t0 = Date.now();
await sharp(Buffer.from(svg), { limitInputPixels: false, density: 72 }).png({ compressionLevel: 6 }).toFile(out + '.tmp.png');
fs.renameSync(out + '.tmp.png', out);
console.log(path.basename(out), `${W}x${H}`, `seed ${seed}`, `${Date.now() - t0} ms`);
