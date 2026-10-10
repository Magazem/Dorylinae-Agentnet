# Closed-beta film

A film that introduces Dorylinae to people who have never heard of it and invites small teams to apply for the closed
beta at **dorylinae.net**. Everything in it is made in code: no drawing tools, no stock footage, no generated imagery, no
voice-over. Terminal output on screen is real output from the product, captured from two running daemons.

There are two cuts. **v2, "The envelope", is the current film.** v1 is kept below as history.

## v2: "The envelope" (current)

| | |
|---|---|
| Length | 64.29 s (22½ bars at 84 BPM) |
| Formats | 16:9 1920×1080 and a separately laid-out 4:5 1080×1350 |
| Frame rates | 60 fps; 30 fps versions rendered natively at 30 fps (180° shutter, their own motion blur) |
| Sound | a new score composed in code (`audio2/score2.py`: CC0 sampled pianos, mallets, strings, designed pad), −14 LUFS, true peak −1.7 dBTP |

Why v2 exists: the owner found that v1 explained the round trip but did not connect or invite. v2 answers that with the
evidence in `design/BRIEF-v2.md`: one still scene with the caption changing in place beside the action (contiguity,
split attention), the post-office analogy the product's own docs use (the relay reads the outside of the envelope:
who, to whom, when, how big; it cannot open it), one card with the three features the film just showed, and a
personal invitation signed by a person.

1. **The pain** (0–5.7 s): you paste the brief into a chat; they paste it into their coding agent.
2. **The scene** (5.7–40.0 s), one continuous shot: maya's machine seals the brief in an envelope; the relay works like
   a post office and reads only the address form (real values from the relay log); theo is offline, so the envelope
   waits while the relay clock runs; theo comes back, his machine opens it, his agent does the work, and the answer
   comes back sealed the same way.
3. **The card** (40.0–46.4 s): End-to-end encrypted. Requests wait while your teammate is offline. You approve what
   their agent can read.
4. **The invitation** (46.4–57.1 s): our envelope, FROM Dorylinae TO your team, opens to a note on Dorylinae
   letterhead: a first wave of 10 small teams, tell us what breaks, free during the closed beta, apply at
   dorylinae.net, signed Yazan.
5. **End** (57.1–64.3 s): the brand boot and **Apply at dorylinae.net**.

Owner decisions: signature "Yazan"; the card does not carry "No independent security review yet." (that disclosure
belongs in the post text, the invitation email and the privacy note); the score is code-composed.

Source: `engine/shots-v2/` (`common-v2.js` is the component library; one file per section), `design/SCRIPT-v2.md`
(every time, size and colour), `audio2/` (score and sampler; the samples themselves are fetched as in `audio2/PALETTE.md`),
`sky-tools/v2-paper-sky/` and `sky-tools/v2-end/` (stars cleared near paper and type), `engine/tools/deliver-v2.sh`
(masters → upload files and posters). Render with `?cut=v2`; `tools/deliver-v2.sh v2` needs the cue files that
`stills.mjs --cut v2` writes (`out/v2-cues-<format>.json`) and the two mixes from `score2.py --cues …`. Chromium runs
with `--disable-partial-raster` (stale raster tiles made 1 px paper edges flicker in in-order renders).

## v1 (history)

| | |
|---|---|
| Length | 63.0 s (26¼ bars at 100 BPM: one bar = 2.4 s, the brand's presence pulse) |
| Formats | 16:9 1920×1080 (X, LinkedIn, Reddit) and a separately laid-out 4:5 1080×1350 (mobile feeds) |
| Frame rates | 60 fps master; 30 fps versions for LinkedIn and Reddit ads (frame-pair blend, never across a cut) |
| Sound | original score and sound design composed in code, −14 LUFS, true peak −1.5 dBTP |

The rendered videos and the sky images are not committed (size). See "Rebuilding" below.

## The film

1. **The pain** (0–3.6 s): you paste the brief into a chat, they paste it into their coding agent. Monochrome.
2. **The name** (3.6–9.6 s): "With Dorylinae, your agent hands it to theirs. For small teams."
3. **One real request**, followed end to end (9.6–40.8 s): the agent writes the brief; theo is offline, so it waits at
   the relay ("The relay sees who, when and how big. Not what."); theo comes back; it lands in his inbox; his agent
   does the work; the answer comes back.
4. **The invitation** (40.8–63.0 s): closed beta by invitation, the first wave opens with 10 teams, free during the beta,
   macOS, Linux and Windows; the invitation written as a brief whose *Done when* line is typed: "Your team applies.";
   the brand boot; **Apply at dorylinae.net**.

Why it is built this way (evidence, with sources) is in `research/evidence.md` and `research/craft.md`; the creative
decisions and every number are in `design/SCRIPT.md` (its DIRECTOR'S AMENDMENTS come first).

## Owner decisions this film relies on

- Call to action: dorylinae.net (a page with a way to apply must be live before the film is posted).
- Beta facts allowed on screen, phrased as a start, not a cap: invite-only for small teams, the first wave opens with
  10 teams, free during the beta, macOS/Linux/Windows.
- Coding agents stay generic (no product names).

## What is honest about it

- Every product claim is checked against the repo docs (`research/product-facts.md`, `research/fact-check.md`,
  `design/SCRIPT-factcheck.md`). Nothing planned but unbuilt (setup, login, invite codes, hosted relay) is shown.
- Terminal lines are copied from `capture/` with only these edits: shortened ids, ` \` line breaks, crops at a frame
  or plate edge, omitted or dimmed lines. The captures came from a scripted run of two real daemons and a local relay on
  one machine (`capture/INDEX.md`).
- The film makes no security superlatives ("audited", "secure", "zero-knowledge" are absent).

## How it is made

```
design/      brief, the final script and storyboard, fact and voice checks, the four competing concepts
research/    product and beta fact sheets, the fact check, evidence on what works, the craft reference
capture/     the real terminal output the film quotes (captured 08.x.2026 from commit 860afe8)
engine/      the film as one deterministic HTML page + renderer
  film.html    ?format=16x9|4x5; window.renderAt(t) sets every pixel as a pure function of t
  lib/         timing, brand tokens, components (typed text, specimen labels, trails, presence dots, cameras),
               the frame-exact brand boot sequence
  shots/       one file per shot (s01–s12) plus the shared Stage W and styles
  render.mjs   Playwright/Chromium → CDP PNG frames → ffmpeg; motion blur (16-sample, 180°, linear light) only in
               windows the shots declare
  stills.mjs   stills and contact sheets for review
  tools/       deliver.sh (masters → platform files and posters), dedust.py (sky clean-up for compression)
audio/       score.py: the score and sound design (numpy/scipy synthesis, VCSL Steinway B samples, pedalboard),
             rendered from the film's own cue list so picture and sound share one clock
tools/       the audio toolchain modules score.py imports, and TOOLING.md (measured decisions and licences)
brand-gen/   scripts run inside the brand kit: logo/boot SVGs and skies from the brand's own code
sky-tools/   star clearing: stars that fell inside word gaps were removed at the generator, per region
```

## Rebuilding

Requirements: Node 22, Playwright 1.56.1 with its Chromium, ffmpeg with libx264/zimg, Python 3.13 with numpy, scipy,
soundfile, pedalboard, pyloudnorm, and the `dorylinae-brand` kit (not in this repo; `render.mjs` loads `sharp` from it and
the skies are generated by it).

1. In the brand kit: `npx tsx film-gen/gen-assets.ts <engine>/assets logo` (copy `brand-gen/*` into `film-gen/` first),
   then the skies: seeds and sizes are listed in `design/SCRIPT.md` §3.8 and in the shots (`S.sky`, `sky-*-clear-*`),
   at nebula 0.6, vignette 0.55, density 2.25 (2× resolution). The cleared variants use `brand-gen/gen-sky-clear.mts`
   and `sky-tools/` with the box files in `sky-tools/`; then `engine/tools/dedust.py` (keep 0.35).
2. `node engine/bundle-assets.mjs`
3. Audio: fetch the VCSL samples (CC0) as in `tools/TOOLING.md`, then
   `python -I audio/score.py --cues engine/out/cues-16x9.json --format 16x9 --out audio/out/mix-16x9.wav` (and 4x5).
   The cue files come from the film page (`window.CUES`).
4. `engine/tools/deliver.sh v1` renders both lossless masters and writes every deliverable to `engine/out/release-v1/`.

## Licences of what is inside

IBM Plex Sans/Mono (SIL OFL 1.1) and Special Elite (Apache 2.0) — licence texts in `engine/fonts/`; GSAP is not used in
the final film; VCSL samples CC0; Playwright Apache 2.0; ffmpeg, pedalboard and espeak are tools only. No third-party
audio, imagery or footage is in the film.
