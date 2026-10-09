// Inline the SVG/JSON assets into assets/assets.js (Chromium cannot fetch() file:// URLs).
import fs from 'node:fs';
import path from 'node:path';
const dir = path.join(path.dirname(new URL(import.meta.url).pathname), 'assets');
const out = {};
for (const f of fs.readdirSync(dir)) if (/\.(svg|json)$/.test(f)) out['assets/' + f] = fs.readFileSync(path.join(dir, f), 'utf8');
fs.writeFileSync(path.join(dir, 'assets.js'), `window.FILM.assetText = ${JSON.stringify(out)};\n`);
console.log(Object.keys(out).length, 'assets bundled');
