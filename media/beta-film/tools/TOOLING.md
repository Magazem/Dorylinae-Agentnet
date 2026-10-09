# Film toolchain: decisions, measurements, commands

Every number on this page was measured in this container (4 vCPU, 15 GB RAM, no GPU) on 08.x.2026.
Folder: `film/tools/`. To rebuild every proof, run `./make-poc.sh` (add `--voice` for the narration samples).

## 0. Decisions in one screen

| Area | Decision | Why |
| --- | --- | --- |
| Frame renderer | Our own: an HTML scene with `window.renderAt(t)` → Playwright 1.56.1 + Chromium 141 (headless shell) → raw CDP `Page.captureScreenshot` (PNG, `optimizeForSpeed`) → piped into ffmpeg's stdin | Deterministic: the same pixels whatever the order, worker or browser instance (§1.3). 4× faster than `page.screenshot()`. No Remotion licence question. |
| Animation authoring | Plain functions of `t`, plus GSAP 3.15 timelines that are `paused` and positioned with `tl.totalTime(t, true)` | GSAP is free for commercial use (Standard "no charge" licence). Seeking is order-independent (tested mid-tween). |
| Capture method | `Page.captureScreenshot`. Rejected: screencast (3 frames for 60 states), `HeadlessExperimental.beginFrame` (same pixels, 4× slower, headless-shell only) | §1.2 |
| Masters | Lossless FFV1 (RGB) rendered by 3 parallel browsers, joined with `-c copy`; deliverables encoded once from it | 1.7× faster than one browser, bit-identical frames |
| Delivery encode | H.264 High 4.2, yuv420p, CRF 16, preset slow, `aq-mode=3`, BT.709 matrix via `zscale` with error-diffusion dither, BT.709 tags, `+faststart`; AAC 320k | Brand colours within 1 code value (§1.4). The naive `-pix_fmt yuv420p` is off by up to 21. |
| Score | Python: numpy/scipy synthesis + a small sampler over **VCSL Steinway B (CC0)** + pedalboard (EQ, compressor, convolution with an IR we generate) + our own true-peak limiter + ffmpeg `loudnorm` (two passes, linear) | Real piano without licence strings, every layer under code control, bit-identical output on each run |
| Voice (optional) | Kokoro-82M (Apache-2.0) through kokoro-onnx on CPU: 2.5× faster than real time | Only clearly commercial-safe model that is easy on CPU. Whether to use synthetic narration at all is an owner decision (§5). |

## 1. Video

### 1.1 The scene contract

A scene is one HTML file that does nothing by itself: no CSS animations or transitions (`animation: none !important`),
no `requestAnimationFrame` loops, no timers. It exposes:

- `window.ready`: a Promise that resolves once every font face is loaded and decoded (`document.fonts.load` for each
  face/weight in use, then `document.fonts.ready`, then `document.fonts.check` must pass, else it throws) and every image is decoded (`img.decode()`).
- `window.renderAt(t)`: sets the whole frame as a pure function of `t` in seconds. All randomness comes from a seeded PRNG.
- Optional: `window.DURATION`, and `window.CUES` (timeline events such as keystroke times) so the sound is built from the same timeline.

Fonts are local files with `font-display: block` (`poc/fonts/`, copied from the brand kit). Large generated imagery
(the brand sky) is rasterised once to PNG. Do not animate an SVG with `feTurbulence` filters: it re-rasterises every frame.

POC: `poc/scene.html` types DORYLINAE in Special Elite with a seeded keystroke cadence and a stamped glyph, blinks
an amethyst cursor, draws and marches a Pheromone trail (3 px dots every 12 px, brand ease `cubic-bezier(0.2, 0.7, 0.2, 1)`)
over the brand sky, and runs a GSAP timeline (eyebrow label, swatches, trail head) inside `renderAt`.

### 1.2 Capture methods, measured (1920×1080, 60 frames of the POC each)

| Method | 1× ms/frame | 2× (3840×2160) ms/frame | Notes |
| --- | --- | --- | --- |
| Playwright `page.screenshot({type:'png'})` | 616 | 1478 | Same pixels as CDP; just slower |
| CDP `Page.captureScreenshot` png | 603 | 1413 | |
| **CDP png + `optimizeForSpeed: true`** | **153** | **431** | Still lossless PNG (less zlib effort); chosen |
| CDP jpeg q100 | 122 | 381 | Lossy. Only 20% faster; not worth it |
| CDP webp q100 | 1435 | n/a | |
| `HeadlessExperimental.beginFrame` (headless shell, `--deterministic-mode`, raw CDP) | 713 (png) | n/a | Pixel-identical to CDP (`b61ffa68cebf`); no benefit because the scene does not depend on rAF; not in new headless |
| `Page.startScreencast` | n/a | n/a | 3 frames received for 60 states: frames come on compositor damage, unsynchronised. Unusable |

`renderAt` itself costs 13 to 20 ms per frame. End to end (render + pipe + x264 slow): **223 ms/frame at 1×, 521 ms/frame at 2×**.

### 1.3 Determinism proofs

- Frame at t = 0.35 s, 1.0 s (GSAP tweens mid-flight) and 1.5 s: rendered in order, after random seeks
  (1.99, 0.1, 0.95, 0, 1.2, 0.5), twice in a row, in a fresh browser, and through `page.screenshot`: one pixel hash per t
  (`bb1c2e5e5834`, `f374dc50eb52`, `b61ffa68cebf`).
- `poc.mp4` rendered with 1, 2, 3 and 4 workers and through the Playwright screenshot path: byte-identical MP4s (md5 `47b32a3bb442`).
  FFV1 masters from 1, 3 and 4 browsers decode to identical frames (framemd5 `604bd378374c`; MKV bytes differ only by random container UIDs).
- Fonts: a screenshot taken before `window.ready` differs from one after it; every capture after it matched across browsers.
  With the `ready` gate there were no half-loaded fonts in any run.

### 1.4 Colour

Swatches rendered in the POC, decoded back from the MP4 as BT.709:

| Brand colour | Our pipeline (max error, 8-bit) | Naive `-pix_fmt yuv420p` |
| --- | --- | --- |
| Amethyst #A678E6 | 1 | 12 |
| Pheromone #D653E1 | 0 | 21 |
| Violet #9756F0 | 1 | 16 |
| Online #5CE6C8 | 1 | 15 |
| Text #EFE9FB | 1 | 1 |

### 1.5 Throughput and parallelism

| Run (2 to 4 s of the POC) | ms per frame |
| --- | --- |
| 1 browser → H.264 pipe | 223 |
| 2/3/4 pages in **one** browser → H.264 | 276 / 292 / 288 (no scaling: they share one GPU/compositor process) |
| 1 browser → FFV1 | 199 |
| 3 / 4 **separate** browsers → FFV1 segments → `-c copy` | **118** / 117 |
| FFV1 master → H.264 slow (x264 uses about 0.4 CPU-seconds per frame) | 119 |
| 2× supersampled, 1 browser → H.264 | 521 |

Projection for a 90 s film at 60 fps (5,400 frames, frames as busy as the POC): about 11 min for the lossless master with 3 browsers,
plus 11 min for H.264 encoding. A single pipelined pass takes about 20 min. A 2× supersampled final takes about 47 min.
Sizes for 2 s of the POC: H.264 CRF 16 is 7.6 MB (30 Mbit/s, because the starfield is noisy), ProRes 4444 is 175 MB, and FFV1 is 157 MB.

### 1.6 Commands

```bash
cd film/tools
node poc/render.mjs                                   # 2 s 1080p60 H.264 -> poc/poc.mp4
node poc/render.mjs --dsf 2 --out out_2x.mp4          # render 3840x2160, Lanczos to 1080p
node poc/render.mjs --codec ffv1 --workers 3 --out master.mkv   # lossless master, 3 browsers
node poc/render.mjs --codec prores --out master.mov   # ProRes 4444 (prores_ks profile 4, 12-bit 4:4:4)
node poc/render.mjs --width 1080 --height 1350 ...    # 4:5 (the scene must lay out for it)
node poc/render.mjs --audio mix.wav ...               # mux audio (AAC 320k into MP4, PCM 24 into MOV/MKV)
node poc/render.mjs --cues cues.json ...              # write window.CUES for the sound pass
node poc/bench.mjs [speed|determinism|fonts|screencast|beginframe]   # re-run the measurements -> poc/bench.jsonl
```

Final H.264 from a master (the exact filter matters, see §1.4):

```bash
ffmpeg -i master.mkv -i mix_-14LUFS.wav -map 0:v -map 1:a \
  -vf "zscale=rangein=full:range=limited:primariesin=709:primaries=709:transferin=709:transfer=709:matrix=709:dither=error_diffusion,format=yuv420p" \
  -c:v libx264 -preset slow -crf 16 -profile:v high -level:v 4.2 -g 120 -x264-params aq-mode=3 \
  -colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv \
  -c:a aac -b:a 320k -ar 48000 -movflags +faststart -shortest film.mp4
```

### 1.7 GSAP

- Licence: GSAP 3.15.0 ships under the Standard "no charge" licence (effective 30 April 2025, modified 30 May 2025).
  Commercial use is free, and all plugins are included (SplitText, DrawSVG, MorphSVG, MotionPath). The one prohibited use is
  inside a no-code visual animation builder that competes with Webflow. Rendering a film is fine. It is not an OSI
  open-source licence, so we keep its notices intact and do not offer it as part of any animation tool. Source: https://gsap.com/standard-license
- Deterministic use (as in `poc/scene.html`): build every timeline once at load with `{paused: true}`, prefer `fromTo` to `from`,
  call `gsap.config({force3D: false})` so a mid-tween `translate3d` layer never changes rasterisation, and call `gsap.ticker.lagSmoothing(0)`.
  In `renderAt` call `tl.totalTime(t, true)` (suppressing events). Do not use `delayedCall`, ScrollTrigger, or `onUpdate` side effects that depend on call history.

### 1.8 Video gotchas

1. **A fresh CDP session does not inherit Playwright's device emulation.** At `deviceScaleFactor: 2` it returned 1920×1080
   until we sent `Emulation.setDeviceMetricsOverride` on our own session (done in `render.mjs`).
2. **ffmpeg's default RGB→YUV matrix is BT.601 and the stream is left untagged.** Use the `zscale` chain above.
3. Pages in one browser do not parallelise. Use one browser per worker.
4. Launch flags for stable text: `--font-render-hinting=none --disable-lcd-text --force-color-profile=srgb --hide-scrollbars`.
5. Pin `playwright@1.56.1`: it matches `/opt/pw-browsers/chromium*-1194`, and browsers cannot be installed here.
6. The ffmpeg here is **6.1.1** (Ubuntu build, `--enable-gpl`, with libzimg), not 7.
7. Dark gradients in 8-bit H.264 band. Keep `aq-mode=3` and the error-diffusion dither, and add subtle seeded grain in the scene for large dark areas.
   For Apple devices only, HEVC Main10 (`libx265 -pix_fmt yuv420p10le -tag:v hvc1`) is an option (not measured).
8. PNG encoding dominates capture time, so keep frames as simple as the design allows. A noisy full-frame starfield costs about 2 MB per frame.

## 2. Music and sound

### 2.1 Routes evaluated

| Route | Result here | Verdict |
| --- | --- | --- |
| numpy/scipy synthesis + **pedalboard** 0.9.26 | `pip install` works (manylinux wheel). Gives EQ, compressor, convolution, chorus, Ladder filter, and VST3 hosting | **Chosen** for synthesis, mixing and effects |
| MIDI/notes in code → **sampled piano** | **VCSL (CC0)** is reachable through a git sparse checkout from GitHub: Steinway B (3 velocity layers, 42 pitches, 44.1 kHz/24-bit, sustain-pedal recordings), Kawai grand (4 layers), 2 uprights, bowed vibraphone. A custom numpy sampler of about 80 lines plays them. jsDelivr refuses the repo (>50 MB). FreePats is reachable: Upright Piano KW is CC0, Salamander Grand is CC-BY 3.0 | **Chosen** for the piano |
| SoundFont players | `tinysoundfont` (MIT, self-contained wheel) and `pyfluidsynth` (needs system libfluidsynth through apt, which is reachable) both exist. GM SoundFonts sound older than the multisamples | Not needed |
| Tone.js 15.1.22 offline (`Tone.Offline` in Chromium) | Works: 10 s rendered in about 3 s. Not bit-identical across runs even with `Math.random` seeded; the difference is −150 dBFS (float noise). Basic synths and reverb, and samples must be served to the page | Viable fallback. Weaker DSP than Python |

### 2.2 The 10-second proof (`audio-poc/`)

100 BPM, one bar = 2.4 s, Dmaj9 → Bm9 → Gmaj9 → Asus, then a low D "button" at 9.6 s.

- **sub**: sine with tanh saturation (adds harmonics so it reads on small speakers), a heartbeat on beats 1 and 3 with a 40 ms pitch drop, and a quiet bed underneath
- **pulse**: restrained 8th notes, an additive band-limited saw through a moving low-pass
- **piano**: VCSL Steinway B, soft layer, rolled open voicings, darkened ("felt": LPF 3.8 kHz, high shelf −3 dB), light compression
- **pad**: detuned additive saws, slow swell, chorus
- **tick**: 1.5 ms band-passed noise click + 2.4 kHz glass ping, one per keystroke **read from the scene's `window.CUES`**, plus a lower confirm tick on the button
- **space**: convolution reverb with a 3-band, decorrelated-noise IR generated in code (no third-party IR)
- **master**: stems staged by loudness (piano −21, sub −25, pulse −28, pad −29, reverb −28 LUFS), glue compressor, our own
  4× oversampled look-ahead true-peak limiter (ceiling −1.5 dBTP, 3.7 dB maximum gain reduction), then `loudnorm.sh` (two passes, linear)

Measured: **−13.9 LUFS integrated, −1.6 dBTP, `normalization_type: linear`**. Two runs give the same WAV (md5 `9f68b7031170`).
Muxed with the 10 s video into `poc/poc_av_10s.mp4`: the first tick is at 0.2502 s in the decoded AAC against a cue at 0.2500 s
(video frame 15), so the drift is 0.2 ms. Sampler pitch is within 2 to 4 cents (the piano's own stretch tuning).
Render time: 12 s for 10 s of audio.

```bash
.venv/bin/python -I audio-poc/score.py --cues poc/out/cues.json    # -> audio-poc/out/score_premaster.wav + stems + IR
audio-poc/loudnorm.sh audio-poc/out/score_premaster.wav audio-poc/out/score_-14LUFS.wav -14 -1
node audio-poc/tone/render-tone.mjs                                 # Tone.js comparison -> tone_offline.wav
```

Levels, sync, pitch and determinism were checked by measurement only; nobody has listened yet. Listen to
`audio-poc/out/score_-14LUFS.wav` and `poc/poc_av_10s.mp4`; each stem is in `audio-poc/out/stem_*.wav`.
This is a toolchain proof, not the composition.

### 2.3 Audio gotchas

1. **`pedalboard.Limiter` is JUCE's limiter**: two compressor stages (the first at −10 dB) plus make-up gain that maps the threshold to 0 dBFS.
   It squashed the first mix to an LRA of 1.4 LU. Use `audio-poc/master.py:limit` instead.
2. `pedalboard.Convolution` normalises the IR, so set the reverb return by loudness rather than by a gain number.
3. VCSL Steinway B files are peak-normalised (`NORMALIZED.txt`), so velocity must be re-applied as gain (done in `sampler.py`).
   VCSL Kawai file names are one octave below what they sound (the "C4" file plays C5, checked by f0).
4. `loudnorm` falls back to dynamic mode silently when linear gain would break the true-peak ceiling. Read `normalization_type`.
   Feed it a premaster that is already near target with a −1.5 dBTP ceiling.
5. Run Python with `-I` (sample files are downloaded data). Scripts then import their own modules through an explicit `sys.path` entry.
6. pedalboard is GPL-3.0. That is fine as a tool, because the audio it outputs is ours. Only shipping code that links it would bring GPL obligations.

## 3. Voice (optional route, about 15 minutes spent)

Kokoro-82M was rendered here: 4 voices, one line each of about 7 to 8 s (`voice-poc/out/{af_heart,am_michael,bm_george,bf_emma}.wav`, 24 kHz).
It ran at **RTF 0.39 to 0.46 on CPU**, 2.5× faster than real time, with a 326 MB fp32 ONNX model.
"Dorylinae" is passed as phonemes (`dˌɔɹɪlˈInˌi`, dor-ih-LYE-nee). espeak alone says "DOR-il-in-ee".

| Model | Licence (weights) | Commercial | CPU (only Kokoro measured; the rest per model card) | Note |
| --- | --- | --- | --- | --- |
| **Kokoro-82M** v1.0 | Apache-2.0 | Yes | **Measured: RTF 0.4** | 54 preset voices, no cloning. Not auditioned by a human yet: listen to `voice-poc/out/` |
| Chatterbox (0.5B) / Turbo / Nano | MIT | Yes | Nano (110M, 2026) is described as built for CPU | Emotion control and cloning |
| Kitten TTS mini 0.8 | Apache-2.0 | Yes | Tiny, CPU-first | Fixed expression presets |
| NeuTTS Air | Apache-2.0 (gated) | Yes | Described as on-device / CPU | Cloning |
| Kyutai Pocket TTS | CC-BY-4.0 (gated) | Yes, with attribution | Described as CPU-oriented | Reference-voice workflow |
| Qwen3-TTS 1.7B | Apache-2.0 | Yes | GPU-oriented (1.7B) | |
| Dia 1.6B | Apache-2.0 | Yes | GPU-oriented | Dialogue model |
| Orpheus 3B | Apache-2.0 (gated) | Yes | Impractical on CPU | LLM-based |
| Sesame CSM-1B | Apache-2.0 (gated) | Yes | GPU-oriented | Conversational |
| VibeVoice 1.5B | MIT | Yes | GPU-oriented | Long-form, multi-speaker |
| Zonos v0.1 | Apache-2.0 | Yes | GPU-oriented | |
| Supertonic 2 | OpenRAIL | Use-based restrictions apply | Described as fast on CPU | |
| F5-TTS | CC-BY-NC-4.0 | **No** | | Excluded |
| Piper voices | MIT repo; each voice's dataset licence varies | Check per voice | CPU, fast | Older VITS voices |

Licences come from each Hugging Face model card's metadata (fetched today). Only Kokoro was benchmarked.
Gotcha: espeakng-loader 0.2.4 ignores the data path it is given and the process exits. We installed the distro library
(`apt-get install libespeak-ng1 espeak-ng-data`; apt is reachable) and pass `EspeakConfig(lib_path=..., data_path=...)`.
`phonemizer-fork` replaces `phonemizer`.

```bash
.venv/bin/python -I voice-poc/kokoro_line.py af_heart am_michael bm_george bf_emma
```

## 4. Licences of everything used

| Item | Licence | Link |
| --- | --- | --- |
| Playwright 1.56.1 | Apache-2.0 | https://github.com/microsoft/playwright/blob/main/LICENSE |
| Chromium 141 (headless shell, preinstalled) | BSD-3-Clause and others | https://www.chromium.org/chromium-projects/ |
| GSAP 3.15.0 | Standard "no charge" licence (commercial OK) | https://gsap.com/standard-license |
| Tone.js 15.1.22 (evaluation only) | MIT | https://github.com/Tonejs/Tone.js/blob/dev/LICENSE.md |
| FFmpeg 6.1.1 (Ubuntu, `--enable-gpl`; x264, x265, zimg) | GPL-2.0+ build; the output is ours | https://ffmpeg.org/legal.html |
| IBM Plex Sans / Mono (brand kit) | SIL OFL 1.1 | `poc/fonts/IBMPlex-LICENSE.txt` |
| Special Elite (brand kit) | Apache-2.0 | `poc/fonts/SpecialElite-LICENSE.txt` |
| numpy 2.5, scipy 1.18, soundfile 0.14 | BSD-3-Clause | https://pypi.org/project/scipy/ |
| pyloudnorm 0.2 | MIT | https://pypi.org/project/pyloudnorm/ |
| pedalboard 0.9.26 | GPL-3.0 (tool only) | https://github.com/spotify/pedalboard/blob/master/LICENSE |
| VCSL samples (commit c1ea7bc, 14 Jan 2026) | **CC0 1.0** (no credit needed) | https://github.com/sgossner/VCSL (`assets/vcsl/LICENSE`) |
| Reverb IR, synths, ticks | Generated in our code | n/a |
| Kokoro-82M weights | Apache-2.0 | https://huggingface.co/hexgrad/Kokoro-82M · ONNX: https://huggingface.co/onnx-community/Kokoro-82M-v1.0-ONNX |
| kokoro-onnx 0.6.1 | MIT | https://github.com/thewh1teagle/kokoro-onnx |
| phonemizer-fork, espeak-ng (distro) | GPL-3.0 (tools only) | https://github.com/espeak-ng/espeak-ng |
| Alternatives not used: Salamander Grand V3 | CC-BY 3.0 per FreePats (some say public domain since 2022; unconfirmed) | https://freepats.zenvoid.org/Piano/acoustic-grand-piano.html |
| Alternatives not used: Upright Piano KW | CC0 | same page |
| Alternatives not used: tinysoundfont | MIT | https://pypi.org/project/tinysoundfont/ |

## 5. Owner questions (where a decision could raise quality)

1. **Narration.** "No AI-generated music" does not cover voice. Is synthetic narration acceptable at all? An 82M-parameter TTS is
   unlikely to match a professional voice; listen to `voice-poc/out/`. The premium routes are a human VO, or no VO at all
   (typed supers + score, the usual product-film grammar). I recommend supers only unless a human voice is available.
2. **Pronunciation of "Dorylinae"**, if anyone says it: dor-ih-LYE-nee (used in the sample) or another form?
3. **Attribution-licensed samples.** Salamander Grand (16 velocity layers) would beat VCSL Steinway B (3 layers) for an exposed solo piano.
   It needs a CC-BY credit line in the film or its description. VCSL stays credit-free.
4. **Frame rate.** 60 fps gives the smoothest UI and type motion and is what we benchmarked. 30 fps halves render time; 24 fps reads more "cinematic" but makes typing and trails judder.

## 6. File map

```
tools/
  TOOLING.md            this page
  make-poc.sh           rebuilds everything
  package.json          playwright@1.56.1, gsap@3.15.0, tone@15.1.22
  .venv/                numpy scipy soundfile pedalboard pyloudnorm kokoro-onnx phonemizer-fork
  assets/vcsl/          VCSL sparse checkout (CC0): Steinway B Sus, Kawai, bowed vibraphone (1.9 GB with git objects)
  assets/kokoro/        Kokoro ONNX model + 6 voices
  poc/scene.html        the deterministic test scene      poc/render.mjs  renderer (CDP -> ffmpeg)
  poc/bench.mjs         capture benchmarks, determinism, screencast, beginFrame
  poc/poc.mp4           2 s, 1080p60 H.264               poc/poc_av_10s.mp4  10 s with the score
  poc/out/poc_2x_w1.mp4 2x supersampled render           poc/bench.jsonl, scale.jsonl  raw numbers
  audio-poc/            score.py, sampler.py, master.py, loudnorm.sh, tone/ (Tone.js comparison), out/ (WAVs, stems, spectrogram)
  voice-poc/            kokoro_line.py, out/*.wav
```
