# Creative brief: Dorylinae closed-beta film

Director's brief. Everything here is decided; build on it, do not reopen it.

## The job

A ~60 second film, posted organically on X, LinkedIn and maybe Reddit, that:
1. tells people who have never heard of Dorylinae what it is and why it matters to them (they know nothing),
2. shows it working (real product, real output),
3. makes the right small teams apply for the closed beta at **dorylinae.net**.

Quality bar: the owner wants something Apple, Samsung, Google or Amazon would be happy to put their name on.
Taste, restraint, precision. No AI slop: no glitch effects, particles, neon, stock transitions, emoji, hype words,
blue-to-purple gradients, swooshes, lens flares or "futuristic HUD" clichés.

## Owner decisions (final)

- Call to action: **dorylinae.net**. One destination, repeated identically. No GitHub, no repo, no handles, no hashtags.
- Formats: 16:9 1920x1080 master (X, LinkedIn, Reddit) and a separately laid-out **4:5 1080x1350** cut (mobile feeds).
  Each scene must work in both; in 4:5 the two machines stack vertically, in 16:9 side by side.
- No voice-over (no synthetic voice, no human voice). Silent-first: every claim is burned-in type. An original score and
  sound design composed in code support it but never carry meaning.
- No people on screen. No drawn OS notification banners or any GUI the product does not have.
- Coding agents stay generic: "your coding agent", "each of you keeps your own agent". Never name Claude Code, Codex,
  OpenCode, Hermes, Antigravity or any other product. Crop harness names out of captured output.
- Beta facts allowed on screen, phrased as a start, not a hard cap (the owner may raise capacity):
  - Closed beta, by invitation, for small teams (do NOT state a fixed team size like "2 to 5"; say "small teams").
  - The first wave opens with 10 teams (phrase as an opening, e.g. "The first wave opens with 10 teams." never "only 10").
  - Free during the beta (say nothing about later pricing).
  - macOS, Linux and Windows.
- Do not show anything planned but not built: no `agentnet setup`, `login`, `feedback`, invite codes `BETA-...`,
  install script, Homebrew, hosted relay hostname, account pages.
- Do not claim: audited, secure/unbreakable, zero-knowledge, tamper-proof, open source, works with every agent,
  live shared sessions, group debates, "everything is private", "every action is logged".
  The relay sees who, when and how big; it cannot read content. "Signed" does not mean "agreed".
- "We" is allowed only where Dorylinae (the people) speak, i.e. the beta invitation.

## Source material (read these)

All under /tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/
- research/product-facts.md: what is built, numbers, safe commands, false claims (sections 1, 3, 5, 6).
- research/fact-check.md: corrections to the fact sheets (overrides them where they disagree).
- research/beta-facts.md: beta mechanics and what not to promise.
- research/evidence.md: "Implications for our film" (length, structure, time shares, CTA, tone).
- research/craft.md: the film language: spec sheet (section 0), signature moves (4.3), typography and safe areas (5),
  reading time (5.4), honesty rules (7), clichés (3).
- capture/INDEX.md and capture/NN-*.txt: REAL terminal output from the running product. Every terminal line shown in
  the film must be copied from these files (allowed edits: omit lines, crop, shorten ids like `r-0282…`, break long
  commands with ` \`, replace the scratchpad path with `~/work/payments-ci`, drop version strings, drop harness names).
- Brand kit: ../brand/dorylinae-brand/dist/DESIGN.md, dist/docs/voice.md, dist/docs/brand-book.md, src/motion.ts.
  The brand code generates the mark, lockups, deep-field skies (seeded), specimen labels and the boot sequence.
  Logo rules: never redraw, stretch, tilt, outline or take the mark apart (its own boot sequence is the only assembly allowed).

## Brand voice (hard rules)

Field notes. Short declarative sentences, present tense, exact nouns, numerals. No exclamation marks, no emoji,
no hype (revolutionary, seamless, supercharge, unleash, AI-powered, 10x...). "You" for the reader. At most one ant word
per screen (colony, trail, column, bivouac; never hive, swarm, drone). Sentence case. The wordmark is always DORYLINAE.
There is no tagline: do not invent one. Special Elite only for display lines and labels (never paragraphs).

## Production facts (what the code renderer can do)

HTML/SVG/CSS scenes rendered deterministically frame by frame at 60 fps (Playwright + ffmpeg); GSAP timelines allowed.
Anything vector/typographic/diagrammatic is cheap; camera moves (scale/translate, depth of field via blur on layers),
parallax deep-field skies, typed text, terminal blocks, plates, trails, specimen labels, the brand boot sequence.
Music clock: 100 BPM; 1 beat = 600 ms (brand "slow" token), 1 bar = 2.4 s (the presence pulse). Cuts land on beats.
