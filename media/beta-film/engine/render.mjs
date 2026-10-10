#!/usr/bin/env node
// Deterministic frame renderer: HTML scene -> Playwright/Chromium -> CDP screenshots -> ffmpeg (stdin pipe).
//
// The scene must expose  window.ready  (a Promise, resolves when fonts/images are decoded)
// and                    window.renderAt(t)  (sets the whole frame as a pure function of t, in seconds).
//
//   node render.mjs                                   # 2 s, 1080p60, H.264 -> poc.mp4
//   node render.mjs --dsf 2                           # render 3840x2160, Lanczos down to 1080p (supersampled AA)
//   node render.mjs --codec prores --out poc.mov      # ProRes 4444 10-bit master
//   node render.mjs --codec ffv1 --out poc.mkv        # lossless RGB master
//   node render.mjs --workers 3                       # 3 browsers render disjoint frame ranges -> FFV1 segments -> one encode
//   node render.mjs --format jpeg                     # JPEG q=100 transport (faster, not lossless)
//   node render.mjs --capture pw                      # page.screenshot() instead of raw CDP (slower)
//   node render.mjs --frames-dir frames               # also keep every PNG
//
// Every option: --scene --out --fps --dur --start --dsf --workers --capture cdp|pw --format png|jpeg
//               --codec h264|prores|ffv1 --crf --preset --frames-dir --audio <file> --stats <json> --cues <json>

import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
// sharp (PNG decode/encode for motion blur) comes from the brand kit's node_modules
const sharp = createRequire('/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/brand/dorylinae-brand/package.json')('sharp');

const here = path.dirname(fileURLToPath(import.meta.url));
// Film renderer (from tools/poc/render.mjs): film.html?format=16x9|4x5 (--aspect 16x9|4x5) -> CDP PNG frames -> ffmpeg.
const argv = Object.fromEntries(process.argv.slice(2).reduce((a, x, i, all) => {
  if (x.startsWith('--')) a.push([x.slice(2), all[i + 1] && !all[i + 1].startsWith('--') ? all[i + 1] : 'true']); return a; }, []));
const opt = {
  scene: path.resolve(here, argv.scene ?? 'film.html'),
  aspect: argv.aspect ?? '16x9',
  out: path.resolve(argv.out ?? path.join(here, 'out', 'film.mp4')),
  fps: Number(argv.fps ?? 60), dur: argv.dur ? Number(argv.dur) : undefined, start: Number(argv.start ?? 0),
  dsf: Number(argv.dsf ?? 1), workers: Number(argv.workers ?? 1),
  capture: argv.capture ?? 'cdp', format: argv.format ?? 'png',
  codec: argv.codec ?? 'h264', crf: argv.crf ?? '16', preset: argv.preset ?? 'slow',
  framesDir: argv['frames-dir'] ? path.resolve(argv['frames-dir']) : null,
  audio: argv.audio ? path.resolve(argv.audio) : null, stats: argv.stats ? path.resolve(argv.stats) : null,
  width: Number(argv.width ?? ((argv.aspect ?? '16x9') === '4x5' ? 1080 : 1920)), height: Number(argv.height ?? ((argv.aspect ?? '16x9') === '4x5' ? 1350 : 1080)),
};

// ---------- ffmpeg argument builders
// Chromium screenshots are sRGB. ffmpeg's RGB->YUV default matrix is BT.601, which shifts brand purples;
// convert explicitly with BT.709 and tag the stream so players decode it the same way.
const TAGS709 = ['-colorspace', 'bt709', '-color_primaries', 'bt709', '-color_trc', 'bt709', '-color_range', 'tv'];
function scaleFilter(pixfmt) {
  const down = opt.dsf !== 1 ? `scale=${opt.width}:${opt.height}:flags=lanczos+accurate_rnd+full_chroma_int,` : '';
  return `${down}zscale=rangein=full:range=limited:primariesin=709:primaries=709:transferin=709:transfer=709:matrix=709:dither=error_diffusion,format=${pixfmt}`;
}
function encoderArgs(codec) {
  if (codec === 'h264') return ['-vf', scaleFilter('yuv420p'), '-c:v', 'libx264', '-preset', opt.preset, '-crf', String(opt.crf),
    '-profile:v', 'high', '-level:v', '4.2', '-g', String(opt.fps * 2), '-x264-params', 'aq-mode=3', ...TAGS709, '-movflags', '+faststart'];
  if (codec === 'prores') return ['-vf', scaleFilter('yuva444p10le').replace(',format=yuva444p10le', ',format=yuv444p10le'),
    '-c:v', 'prores_ks', '-profile:v', '4', '-vendor', 'apl0', ...TAGS709];      // ProRes 4444
  if (codec === 'ffv1') return [...(opt.dsf !== 1 ? ['-vf', `scale=${opt.width}:${opt.height}:flags=lanczos+accurate_rnd`] : []),
    '-pix_fmt', 'bgr0', '-c:v', 'ffv1', '-level', '3', '-g', '1', '-slices', '16', '-slicecrc', '1'];   // lossless RGB
  throw new Error('unknown codec ' + codec);
}
function inputArgs() {
  return ['-f', 'image2pipe', '-framerate', String(opt.fps), '-c:v', opt.format === 'jpeg' ? 'mjpeg' : 'png', '-i', 'pipe:0'];
}
function runFfmpeg(args, label) {
  const p = spawn('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args], { stdio: ['pipe', 'inherit', 'inherit'] });
  const done = new Promise((res, rej) => p.on('close', (c) => (c === 0 ? res() : rej(new Error(`ffmpeg ${label} exited ${c}`)))));
  return { p, done };
}
async function writeTo(stream, buf) { if (!stream.write(buf)) await new Promise((r) => stream.once('drain', r)); }

// ---------- capture
async function openPage(browser) {
  const ctx = await browser.newContext({ viewport: { width: opt.width, height: opt.height }, deviceScaleFactor: opt.dsf });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => { console.error('[scene error]', e.message); process.exitCode = 1; });
  page.on('console', (m) => { if (m.type() === 'error' || m.type() === 'warning') console.error('[scene]', m.text()); });
  await page.goto(pathToFileURL(opt.scene).href + `?format=${opt.aspect}` + (argv.cut ? `&cut=${argv.cut}` : '') + (argv.debug ? '&debug=1' : ''), { waitUntil: 'load' });
  const info = await page.evaluate(() => window.ready);
  // cuts: the start of every shot that replaces the picture (z 0; overlay layers such as caption slots have z > 0)
  const meta = await page.evaluate(() => ({ dur: window.DURATION ?? null, cues: window.CUES ?? null, blur: window.CUES?.blur ?? [],
    cuts: (window.FILM?.shots ?? []).filter((s) => !s.z && s.start > 0).map((s) => s.start) }));
  const cdp = await ctx.newCDPSession(page);
  // A fresh CDP session does not inherit Playwright's device emulation: without this override
  // Page.captureScreenshot returns 1920x1080 even when deviceScaleFactor is 2.
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: opt.width, height: opt.height, deviceScaleFactor: opt.dsf, mobile: false });
  return { ctx, page, cdp, info, meta };
}
async function grab({ page, cdp }) {
  if (opt.capture === 'pw') return page.screenshot({ type: opt.format, ...(opt.format === 'jpeg' ? { quality: 100 } : {}), animations: 'allow', caret: 'initial', scale: 'device' });
  const r = await cdp.send('Page.captureScreenshot', {
    format: opt.format, ...(opt.format === 'jpeg' ? { quality: 100 } : {}),
    fromSurface: true, captureBeyondViewport: false, optimizeForSpeed: opt.format === 'png',
  });
  return Buffer.from(r.data, 'base64');
}
// ---------- motion blur: 180° shutter, N sub-frames averaged in linear light, only inside the scene's declared windows
// The shutter is 180° at the render's own frame rate (span SHUTTER / fps: 1/120 s at 60 fps, 1/60 s at 30 fps), so a
// 30 fps delivery is rendered natively with --fps 30 (fix-r3: averaging pairs of 60 fps frames gave two separate 180°
// exposures with a gap, i.e. doubled glyphs and paired stars, not a 180° shutter at 30 fps).
// A window [t0, t1, maxFps] with a third element applies only when fps <= maxFps (moves whose steps pass 19 px a frame
// at 30 fps but not at 60). Sub-frame times never cross a hard cut (meta.cuts): a frame after a cut never blends in the
// shot before it, nor one before a cut the shot after it (at 30 fps the first frame of a shot starting mid-frame would).
const BLUR_N = Number(argv['blur-samples'] ?? 16), SHUTTER = 0.5;      // 0.5 = 180°
const toLin = new Float32Array(256).map((_, i) => { const c = i / 255; return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; });
function toSrgb8(l) { const c = l <= 0.0031308 ? l * 12.92 : 1.055 * l ** (1 / 2.4) - 0.055; return Math.max(0, Math.min(255, Math.round(c * 255))); }
const inBlur = (t, wins) => argv['no-blur'] !== 'true' && wins.some(([a, b, maxFps]) => (maxFps == null || opt.fps <= maxFps + 1e-9) && t >= a - 1e-9 && t <= b + 1e-9);
/** a sub-frame time held on the frame's own side of every hard cut */
const sideOfCuts = (ts, t, cuts) => { for (const c of cuts) { if (t >= c && ts < c) ts = c; else if (t < c && ts >= c) ts = c - 1e-6; } return ts; };
async function grabBlurred(pg, t) {
  const span = SHUTTER / opt.fps;
  let acc = null, info = null;
  for (let i = 0; i < BLUR_N; i++) {
    const ts = sideOfCuts(t - span / 2 + ((i + 0.5) / BLUR_N) * span, t, pg.meta.cuts ?? []);
    await pg.page.evaluate((tt) => window.renderAt(tt), ts);
    const { data, info: inf } = await sharp(await grab(pg)).removeAlpha().raw().toBuffer({ resolveWithObject: true });
    info = inf;
    if (!acc) acc = new Float32Array(data.length);
    for (let k = 0; k < data.length; k++) acc[k] += toLin[data[k]];
  }
  const out = Buffer.alloc(acc.length);
  for (let k = 0; k < acc.length; k++) out[k] = toSrgb8(acc[k] / BLUR_N);
  return sharp(out, { raw: { width: info.width, height: info.height, channels: 3 } }).png({ compressionLevel: 1 }).toBuffer();
}
async function renderRange(browser, f0, f1, sink, timing) {
  const pg = await openPage(browser);
  const wins = pg.meta.blur ?? [];
  for (let f = f0; f < f1; f++) {
    const t = opt.start + f / opt.fps;
    const a = performance.now();
    let buf;
    if (inBlur(t, wins)) { buf = await grabBlurred(pg, t); timing.blurred = (timing.blurred ?? 0) + 1; }
    await pg.page.evaluate((tt) => window.renderAt(tt), t);
    const b = performance.now();
    if (!buf) buf = await grab(pg);
    const c = performance.now();
    timing.evaluate += b - a; timing.capture += c - b; timing.frames++;
    if (opt.framesDir) fs.writeFileSync(path.join(opt.framesDir, `f${String(f).padStart(6, '0')}.${opt.format === 'jpeg' ? 'jpg' : 'png'}`), buf);
    const d = performance.now();
    await writeTo(sink, buf);
    timing.pipe += performance.now() - d;
  }
  await pg.ctx.close();
  return pg;
}

// ---------- main
const t0 = performance.now();
if (opt.framesDir) fs.mkdirSync(opt.framesDir, { recursive: true });
// --disable-partial-raster (final polish, review r3 motion): with partial raster Chromium re-rasters only the damaged part
// of a tile and reuses the rest from the previous frame, so a frame's pixels depended on the frames rendered before it: in
// an in-order render a 1 px light line at a paper edge flickered on/off every frame (4:5 (215,444), frames 471-487, 7/203)
// and renderAt(t) after a jump differed from renderAt(t) in sequence. Whole-tile raster makes every frame a function of t.
const LAUNCH = { args: ['--font-render-hinting=none', '--disable-lcd-text', '--force-color-profile=srgb', '--hide-scrollbars', '--disable-partial-raster'] };
const browser = await chromium.launch(LAUNCH);
// Pages in one browser share its GPU/compositor process, so they do not scale; give each worker its own
// browser unless --shared-browser is passed (kept only to reproduce the benchmark).
const sharedBrowser = argv['shared-browser'] === 'true';
// Read the scene's own duration if none was given.
if (opt.dur === undefined || argv.cues) {
  const p = await openPage(browser); opt.dur ??= p.meta.dur ?? 2; await p.ctx.close();
  if (argv.cues) fs.writeFileSync(path.resolve(argv.cues), JSON.stringify(p.meta.cues, null, 1));   // timeline cues for the audio
}
const total = Math.round(opt.dur * opt.fps);
const timing = { evaluate: 0, capture: 0, pipe: 0, frames: 0, blurred: 0 };
const audioArgs = (inputs) => (opt.audio ? { pre: ['-i', opt.audio], map: ['-map', '0:v', '-map', `${inputs}:a`, '-c:a', opt.codec === 'h264' ? 'aac' : 'pcm_s24le', ...(opt.codec === 'h264' ? ['-b:a', '320k'] : []), '-shortest'] } : { pre: [], map: [] });

if (opt.workers <= 1) {
  // One page, PNGs piped straight into the final encoder.
  const a = audioArgs(1);
  const ff = runFfmpeg([...inputArgs(), ...a.pre, ...a.map, ...encoderArgs(opt.codec), opt.out], 'encode');
  await renderRange(browser, 0, total, ff.p.stdin, timing);
  ff.p.stdin.end(); await ff.done;
} else {
  // N pages in parallel, each pipes into its own lossless FFV1 segment; then one concat + final encode.
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'poc-seg-'));
  const per = Math.ceil(total / opt.workers);
  const segs = [];
  await Promise.all(Array.from({ length: opt.workers }, async (_, w) => {
    const f0 = w * per, f1 = Math.min(total, f0 + per); if (f0 >= f1) return;
    const seg = path.join(tmp, `seg${w}.mkv`); segs[w] = seg;
    const ff = runFfmpeg([...inputArgs(), '-pix_fmt', 'bgr0', '-c:v', 'ffv1', '-level', '3', '-g', '1', '-slices', '16', '-slicecrc', '1', seg], `seg${w}`);
    const b = sharedBrowser ? browser : await chromium.launch(LAUNCH);
    await renderRange(b, f0, f1, ff.p.stdin, timing);
    if (b !== browser) await b.close();
    ff.p.stdin.end(); await ff.done;
  }));
  const list = path.join(tmp, 'list.txt');
  fs.writeFileSync(list, segs.filter(Boolean).map((s) => `file '${s}'`).join('\n'));
  const a = audioArgs(1);
  // An FFV1 master is just the segments joined (-c copy, no re-encode); other codecs encode once from the lossless segments.
  const enc = opt.codec === 'ffv1' && opt.dsf === 1 ? ['-c:v', 'copy'] : encoderArgs(opt.codec);
  const ff = runFfmpeg(['-f', 'concat', '-safe', '0', '-i', list, ...a.pre, ...a.map, ...enc, opt.out], 'final');
  ff.p.stdin.end(); await ff.done;
  fs.rmSync(tmp, { recursive: true, force: true });
}
await browser.close();

const wall = (performance.now() - t0) / 1000;
const stats = {
  out: opt.out, frames: total, fps: opt.fps, dsf: opt.dsf, workers: opt.workers, sharedBrowser: opt.workers > 1 ? sharedBrowser : undefined, capture: opt.capture, format: opt.format, codec: opt.codec,
  wall_s: +wall.toFixed(2), wall_ms_per_frame: +((wall * 1000) / total).toFixed(1),
  page_ms_per_frame: { evaluate: +(timing.evaluate / timing.frames).toFixed(1), capture: +(timing.capture / timing.frames).toFixed(1), pipe: +(timing.pipe / timing.frames).toFixed(1) },
  realtime_factor: +(wall / opt.dur).toFixed(1), bytes: fs.statSync(opt.out).size, blurred_frames: timing.blurred,
};
console.log(JSON.stringify(stats));
if (opt.stats) fs.appendFileSync(opt.stats, JSON.stringify(stats) + '\n');
