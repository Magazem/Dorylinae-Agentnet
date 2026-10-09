// The brand deep field (src/field.ts, as film-gen/gen-assets.ts and s1347tools/gen-sky-clear.mts render it), listing its
// countable stars, or rendering it with a given set of stars (core and halo) left out. Nothing else changes.
//   npx tsx gen-sky-drop.mts list   <out.json> <seed> <w> <h> <nebula> <vignette> <density>
//   npx tsx gen-sky-drop.mts render <out.png>  <seed> <w> <h> <nebula> <vignette> <density> [drop.json: ["x,y", ...]]
import fs from 'node:fs';
import { createRequire } from 'node:module';
const BRAND = '/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/brand/dorylinae-brand';
const require = createRequire(BRAND + '/package.json');
const sharp = require('sharp');
const { fieldSvg } = await import(BRAND + '/src/field.ts');
const [mode, out, seed, W, H, nebula, vignette, density, dropFile] = process.argv.slice(2);
let svg: string = fieldSvg({ w: +W, h: +H, seed: +seed, nebula: +nebula, vignette: +vignette, density: +density });
const RE = /<circle cx="([\d.-]+)" cy="([\d.-]+)" r="([\d.-]+)"([^>]*)\/>/g;
const stars = new Map<string, { k: string; x: number; y: number; core: number; ext: number; fill: string; op: number }>();
for (const m of svg.matchAll(RE)) {
  const k = m[1] + ',' + m[2], r = +m[3];
  const st = stars.get(k) ?? { k, x: +m[1], y: +m[2], core: Infinity, ext: 0, fill: '', op: 1 };
  if (!m[4].includes('url(')) { st.core = r; st.fill = (m[4].match(/fill="([^"]+)"/) || [])[1]; st.op = +((m[4].match(/opacity="([^"]+)"/) || [])[1] ?? 1); }
  st.ext = Math.max(st.ext, r); stars.set(k, st);
}
if (mode === 'list') { fs.writeFileSync(out, JSON.stringify([...stars.values()])); console.log('stars', stars.size); process.exit(0); }
const drop = new Set<string>(dropFile ? JSON.parse(fs.readFileSync(dropFile, 'utf8')) : []);
let cut = 0;
svg = svg.replace(RE, (m, cx, cy) => (drop.has(cx + ',' + cy) ? (cut++, '') : m));
const t0 = Date.now();
await sharp(Buffer.from(svg), { limitInputPixels: false, density: 72 }).png({ compressionLevel: 6 }).toFile(out + '.tmp.png');
fs.renameSync(out + '.tmp.png', out);
console.log(`stars ${stars.size}, left out ${drop.size} (${cut} circles)`, `${Date.now() - t0} ms`);
