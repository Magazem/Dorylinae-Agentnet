# Craft reference: how premium launch films are made, and how Dorylinae makes one

Prepared for the people who will script, design, build, score and QA the Dorylinae closed-beta film.
It covers five things: what makes a launch film read as premium rather than template work (section 1),
techniques that suit a command-line product with no GUI (2), clichés to avoid (3), how the brand kit
becomes a film language with three signature moves (4), and type sizes, safe areas and reading time (5).
Sections 6 to 9 cover delivery, honesty rules, questions for the owner, and sources.

Confidence tags used throughout:

- **[S]** sourced. The citation is in section 9. Figures from studio or vendor blogs are reported claims, not verified facts.
- **[O]** an observation about a reference film, taken from a secondary description of that film.
- **[J]** our own judgment or arithmetic. Each one is a recommendation that the team can overrule.

Brand files read: `dist/DESIGN.md`, `dist/docs/brand-book.md`, `dist/docs/voice.md`, `dist/docs/logo.md`,
`src/motion.ts`, `src/tokens.ts`, `src/geometry.ts`, `src/terminal.ts`, and `ref/boot-sheet.png`. Real CLI output is in
`film/capture/` (see `capture/INDEX.md`). Product truth is in `research/product-facts.md` and
`research/beta-facts.md`. This document does not repeat product claims. Check every on-screen sentence against those two files.

---

## 0. The spec sheet: every number on one page

| Item | Value | Basis |
|---|---|---|
| Master frame rate | **60 fps** (30 fps fallback). No 24 fps for terminal or data motion | [S] judder (24 fps on 60 Hz uses a 3:2 cadence); [J] 100 BPM beat = 36 frames exactly |
| Film clock | **100 BPM**: 1 beat = 600 ms = brand `slow` = 36 frames; 1 bar = 2.4 s = presence pulse = 144 frames | [J] from tokens |
| Brand motion tokens | 120 ms state change, 240 ms enter, 600 ms boot part; ease `cubic-bezier(0.2,0.7,0.2,1)`; pulse 2.4 s | [S] DESIGN.md, tokens.ts |
| Second sanctioned curve | `cubic-bezier(0.4,0,0.6,1)`, only for the glint sweep (traverse) | [S] motion.ts |
| Linear timing | only for loops (scanline drift, 1 pitch per 1.6 s; trail march) and the 60 ms letter fade | [S] motion.ts |
| Boot sequence | 3.6 s; plates stagger 45 ms and each lock in over 500 ms with the brand ease; sweep at 1.2 s for 0.75 s; signal at 1.35 s; letters from 1.9 s, every 85 ms | [S] motion.ts |
| Shot length | average 3.6 to 4.8 s (1.5 to 2 bars); minimum 1.2 s (half bar); maximum 9.6 s (4 bars) | [S] Cutting average shot length; [J] |
| Ideas per shot | one. One accent per view. One moving thing plus at most one settling thing | [S] Lago/Linear, DESIGN.md |
| Ideas per film | at most 4 product ideas, about 12 to 18 s each, plus the call to action | [S] Lago "twenty seconds"; [J] |
| Film length | hero cut 60 to 75 s; cutdowns 15 to 20 s and 6 s | [S] moonb (under ~90 s holds up); [J] |
| Reading pace | average adult 238 wpm silent (4 words/s); subtitles 160 to 180 wpm (0.33 to 0.375 s/word); Netflix ceiling 20 chars/s | [S] Brysbaert, BBC, Netflix |
| Text hold, floor | max(1.5 s, 0.5 s + 0.35 s × words), counted from the moment the line has fully landed | [J] built on BBC and Brysbaert |
| Text hold, key claim | max(2.0 s, 0.5 s + 0.6 s × words), which is two reads at 200 wpm | [S] SSW rule; [J] |
| Typed-on speed | names and labels 85 ms per letter (boot cadence); longer typed lines 40 to 50 ms per letter; never type command flags letter by letter | [S] motion.ts, VHS default 50 ms; [J] |
| Smallest story text, 16:9 1080p | 64 px (about 13 pt when a 16:9 video plays inline on a portrait phone) | [S] Apple HIG; [J] arithmetic |
| Smallest story text, 4:5 1080×1350 | 48 px (about 17.5 pt on a phone); small print floor 32 px (about 11.6 pt) | [S] Apple HIG; [J] |
| Title safe 16:9 | 90 %: 96 px left and right, 54 px top and bottom (inner 1728 × 972) | [S] SMPTE ST 2046-1, EBU R 095 |
| Safe 4:5 | 64 px left and right, 64 px top, 128 px bottom for anything that must be read | [J] from feed crops and controls |
| Loudness | −14 LUFS integrated, true peak at most −1 dBTP | [S] (third-party mastering sources) |
| Flashes | never more than 3 per second | [S] WCAG 2.3.1 |

---

## 1. What separates premium launch films from template motion and "AI slop"

### 1.1 The short version

Premium films are built from **subtraction, real material and rhythm**. Template work and AI slop come from
**addition, invented material and evenness**. Commentators describe AI-generated slop as technically polished but creatively shallow, with motion that
is overly smooth, floaty and at odds with physics, and a structure that resembles stock corporate explainer videos [S Magic Hour,
Artlist]. The common thread is sameness: models average across samples, so outputs converge on a safe look [S].
The fix is to make choices that only this product and this brand could make, with real footage and real constraints.

### 1.2 Pacing and restraint

- **One sentence carries one scene.** In Apple's iPhone Air reveal, a single short statement does the work of an entire
  scene. The film opens on its claim and spends the runtime proving it, and most frames are pure product [O moonb]. The text animation "never
  competes with the message": a phrase may fade, scale or slide into place, and nothing more [O moonb].
- **No narrator is needed.** Apple's 108-second year-in-review film has "No voiceover. No presenter. Just words, motion, and rhythm," and
  each sentence is its own beat [O Contra/Shah].
- **Every shot has to earn its place.** Lago's written rules for Linear-style films: each shot must advance one of three named
  axes, and a shot that advances none gets deleted [S Lago, paraphrased]. Walter Murch ranks the reasons for a cut as emotion 51 %,
  story 23 %, rhythm 10 %, eye-trace 7 %, 2D plane 5 % and 3D space 4 %. If a cut does not advance the story, leave it out [S Murch].
- **Shot length.** Cutting measured the average shot in English-language films falling from about 12 s (1930) to about 2.5 s
  in 2014, and to about 4 s in a 1935 to 2005 popular-film sample [S Cutting]. A software film needs reading time, so it should sit
  at the slow end of modern pacing. We recommend an average of 3.6 to 4.8 s. Shorter cuts are fine for a single montage, and one
  long held shot is fine for the payoff [J].
- **Restraint is a single rule set.** ManvsMachine's iMac Pro film used one base algorithmic structure to govern each form and
  movement in the film [S Cannes Lions, paraphrased]. Restraint here means one rule set applied everywhere, rather than many separate effects.
- **Length.** The reference durations: Linear feature films about 20 to 23 s [S Lago]; Raycast's 2025 teaser 39 s; Pixel 9 Pro
  teaser 30 s; Oura Ring 4 31 s; Figma Motion 1:09; Meta Quest 3 1:18 [O moonb]; Apple's year recap 108 s [O Contra].
  Moonb's author observes that pieces hold up best under about 90 seconds [S].

### 1.3 Typography is the hero

- When a brand has a strong typeface, use it with confidence. Extra outlines, shadows and decoration weaken a restrained system [S moonb].
- Figma Config splits type motion into "expressive" type, which "takes a bit more time to animate on and has more personality", and
  "functional" type, which is "quicker and more direct" [S Figma]. Dorylinae already has this split. Special Elite is the expressive,
  typed voice and is used rarely. Plex Sans and Plex Mono are functional and land quickly [J].
- Physics beats default easing. In Mat Voyce's "Think Different" exercise, the type "settles with inertia, not CSS easing.
  Letters carry mass" [O abduzeedo]. The brand ease does the same thing: 73 % of the travel happens in the first 25 % of the time,
  then a long settle (see 1.6).
- GitHub's brand motion page warns that text motion should not feel like an out-of-the-box presentation effect [S GitHub].

### 1.4 Negative space

- Apple rations words and "gives them room" [O moonb]. Test the animatic on a phone held at arm's length. Generous space either
  keeps the text readable or shows you which type is too small [S moonb].
- Practice for Dorylinae [J]: in a text-led frame, at least 55 % of the frame is plain ground (void or the deep field). A hero line
  takes no more than about 60 % of the title-safe width. A terminal shot shows 1 to 8 lines, never a full screen of scrollback.

### 1.5 Camera language on UI

- **The camera does the transitions.** Lago's grammar for Linear-style films: zoom into the action, whip-pan between screens, hold the
  payoff; "the camera does every transition"; framing locked on a focal object, and an eye-trace destination declared for each shot
  [S Lago]. Screens **bleed off the frame** instead of floating like cards [S Lago]. That also fits the brand's ban on shadows.
- **Apple keynote camera.** The camera "practically never stands still". It moves smoothly and subtly while someone speaks and quickly for emphasis
  [O Galaxus]. On UI: a slow push-in during a hold keeps the frame alive [J].
- **Depth and focus.** UI layers are placed at different depths, a camera depth-of-field pass blurs what is not in focus, and
  focus distance is keyframed to pull attention from one layer to the next [S AE tutorials]. A code renderer does the same with a
  per-plate blur radius driven by distance from a focal plane [J].
- **The Raycast teaser** used "cropped, dark, dramatically lit fragments of the real interface" [O moonb]. That is the macro push-in applied
  to software: crop until one token fills the frame.
- **Our numbers [J]:** a push-in changes scale by 3 to 8 % per second during holds, and by up to 2× over 1 to 2 bars for a macro
  reveal. Use the brand ease so the move is visibly done by 44 % of its duration (see 1.6). Never roll the camera. Rolling would tilt
  the mark when it is in frame, and the brand's only angle is 21° for cuts and slashes. Moving areas larger than a quarter of the
  screen at speed are a vestibular risk [S accessibility guides], so whips are rare and short.

### 1.6 Timing curves

Measured progress of each curve at 10 / 25 / 50 / 75 / 90 % of its duration [J, computed]:

| Curve | 10 % | 25 % | 50 % | 75 % | 90 % | reaches 90 % at | reaches 99 % at |
|---|---|---|---|---|---|---|---|
| **Brand** `(0.2,0.7,0.2,1)` | 36 % | 73 % | 93 % | 99 % | 100 % | 0.44 | 0.77 |
| Material 3 emphasized decelerate `(0.05,0.7,0.1,1)` | 62 % | 83 % | 95 % | 99 % | 100 % | 0.36 | 0.74 |
| easeOutQuint `(0.23,1,0.32,1)` | 40 % | 78 % | 97 % | 100 % | 100 % | 0.36 | 0.64 |
| easeOutExpo `(0.19,1,0.22,1)` | 48 % | 84 % | 98 % | 100 % | 100 % | 0.31 | 0.59 |
| Brand sweep `(0.4,0,0.6,1)` | 2 % | 14 % | 50 % | 86 % | 98 % | 0.79 | 0.93 |
| CSS ease-out `(0,0,0.58,1)` | 16 % | 38 % | 68 % | 91 % | 98 % | 0.74 | 0.93 |

What follows from the table:

- The brand ease belongs to the same family as Material 3's emphasized decelerate and Penner's quint ease-out: fast arrival, then a long, quiet settle.
  Premium motion usually looks like this. The default After Effects Easy Ease, a 33 % influence curve shaped roughly like a sine, is what
  makes template work look soft and generic [S Creative COW thread]. A commonly cited fix is raising influence to about 60 % [S].
- **Perceived duration is about 0.44 × nominal.** A 600 ms brand move reads as arriving in about 265 ms. You can cut, or start the
  next action, at about 77 % of a move without it feeling unfinished [J].
- **No overshoot and no bounce.** GitHub says not to use slapping, rolling or bouncing unless it supports a character's movement [S].
  Apple's spring guidance treats bounce as optional and the parameter defaults are reported as duration 0.5 s, bounce 0 [S WWDC23 notes]. The brand curve has no overshoot.
- **Exits.** Other systems shorten exits by 50 to 100 ms [S Atlassian; Material 1: enter 225 ms, leave 195 ms]. The brand defines no exit
  curve. So in the film, **exits are cuts** [J]. A cut needs no new curve, and cuts on the beat read as confident.
- **Hand-made frame rate is a style choice, not a fix.** Figma's Config 2025 opener dropped from 60 to 15 fps to feel "more tactile and handmade"
  [S Figma]. Basement animated Vercel Ship 2025 "on twos" (12 fps) over a 24 fps camera [S Basement]. Dorylinae's brand is
  "quiet and exact: an instrument panel". Its only stepped motion is typing. Keep continuous motion at 60 fps and let the typed letters supply the stepped, hand-made texture [J].

### 1.7 Ideas per shot

- Linear-style films: one action at a time, one motion at a time [S Lago].
- The brand: "One accent per view. `accent` (Pheromone) marks the single thing to notice" [S DESIGN.md]. In film this becomes a
  rule: **every frame has exactly one pheromone element, and that element is where the eye goes next** (Murch's eye-trace) [J].
- Budget per shot [J]: one entrance (plates lock in), one action (a command lands, a trail travels, a row moves), and one hold.
  The hold is at least 40 % of the shot.

### 1.8 How the best films show software UI

There are three ways to show UI, and the references are clear about which ones hold up.

1. **Real UI, captured.** Lago's rules for Linear-style films: use only real pixels, never invent UI, and if a screen is missing, the film
   waits; never show a screen the design team did not draw [S Lago, paraphrased]. GitHub: show products "accurately and with minimal
   abstraction" [S]. On Apple, an anonymous WWDC presenter reports that keynote interfaces are captured from hardware on a build
   flagged for final captures [S anecdote, unverified].
2. **Simplified real UI.** Crop, enlarge, dim everything except the line that matters (the Raycast teaser [O]). Linear's walkthroughs
   keep the real interface full-screen and put the presenter in a small corner bubble [O moonb].
3. **Recreated UI.** This is allowed only if it is faithful. The cautionary case: Apple's polished WWDC 2024 films showed Siri
   features that had not shipped. In 2026 Apple agreed to a proposed $250M settlement of a false-advertising class action, with no
   admission of wrongdoing and court approval still pending [S Gizmodo, NBC, MediaPost]. Its WWDC 2026 demos then moved to pre-taped, single-take,
   phone-in-hand footage [S TechCrunch]. John Gruber describes an internal Apple rule that a keynote feature must be real and
   capturable in a single take [S, reported].

**For Dorylinae [J]:** a terminal has no pixels of its own, because every user's terminal font and colours differ. So re-typesetting
**real captured output** in the brand's terminal style (void, Plex Mono, amethyst prompt, `text-3` output) counts as real UI,
provided the characters are identical. Section 7 lists the allowed edits.

### 1.9 Sound design and music sync

- **Design for sound-off first.** LinkedIn feed video autoplays muted [S]. The often-quoted "79 % watched on mute" figure has disputed sourcing [S].
  So every claim must work as on-screen text. Sound adds feeling, never information [J].
- **A sound palette, not a sound library.** Product-sound guidance: compose sounds in the same key with the same or similar instrumentation so the set
  reads as one palette. Zelig built Vinted's UI sounds out of the sonic logo [S Listen, Zelig]. "Do no harm": branded sounds that
  add clutter build negative associations [S].
- **Mnemonic length.** Sonic logos run about 1 to 3 s, and Lenovo fixes its own at 3 s [S]. Resmed's 5 s sting includes about 1.5 s of resting
  silence and allows no copy, no cross-dissolves and no other motion graphics on screen with it [S]. The Dorylinae boot is 3.6 s, so its
  sound is a 6-beat cue at 100 BPM [J].
- **Sync without "Mickey-Mousing" [J].** Put structural cuts and key-text landings on downbeats (beat 1 of a bar). Let micro-motion
  (stagger, letter typing, settle) fall between beats. Use one strong hit per bar at most. Lead with sound: start the next shot's audio
  4 to 12 frames before the picture cut (a J-cut). Use one deliberate silence of 1 to 2 beats before the end card.
- **Figma Config** used a warm-up "orchestra" track to signal the start without a PA announcement, and sonic motifs that recur through the
  day [S Figma]. Recurring motifs, not stock hits, are what make sound feel like part of a brand.
- **Mix [S/J]:** −14 LUFS integrated with true peak at most −1 dBTP for YouTube, Vimeo and social [S]. SFX sit 6 to 12 dB under music peaks [J].
  The film has no voiceover, so no dialogue ducking is needed.

### 1.10 Transitions: cuts on the beat, not effects

- Premium films cut. When they transition, the camera does it (push through, pan) or a designed object carries it [S Lago; O Galaxus:
  Apple's product changes use designed transitions rather than plain cuts].
- Resmed's rules forbid cross-dissolving into or out of the logo [S]. GitHub warns that a glitch effect can look broken in context [S].
- **For Dorylinae [J]:** hard cuts on downbeats are the default. There are only two designed transitions: the **21° cut** (a single edge at 21°
  off vertical that crosses the frame in 600 ms with the brand ease, used for at most 2 chapter changes), and the **plate lock**
  (the next scene builds from plates). No dissolves, zoom-blurs, glitches, whips with smears, light leaks or shape wipes.

### 1.11 Colour discipline

- The ManvsMachine and Samsung "liquid" teaser was a single matte bronze form [O]. Dyson colour-codes the mechanism so the film can be followed with
  the sound off [O moonb]. Colour carries meaning.
- **For Dorylinae [S DESIGN.md]:** the palette is void, surfaces and text tokens. Amethyst is the brand, pheromone the single accent,
  and teal, amber and red appear only as presence and status, always with their shape (solid dot, half dot, bar, hollow ring).
  The sky is the only gradient. There is no colour grade or LUT on top. The film is the tokens.

### 1.12 Endings and end cards

- **Single call to action.** The Pixel 9 Pro teaser closes on one CTA, sign up for updates [O moonb]. Lenovo puts an end bumper on every
  video over 10 s, and uses a static end frame for 1 to 2 s on shorter ones [S].
- **No intro card.** Lago's rules say "no intro card" [S Lago]. The references open on the claim (Apple) or on a typed prompt over a dark screen
  (Pixel 9 Pro) [O moonb]. The Dorylinae boot belongs at the **end**.
- **Recommended Dorylinae end [J]:** the last claim, then 1 beat of ground and silence, then the boot sequence of the stacked
  lockup, unchanged at 3.6 s, on its own deep-field seed. Hold the finished lockup for 1.2 s. Then the CTA line appears **below the
  clear space** and **after** the boot ends, never during it (the Resmed rule). Hold at least 3 s, then 0.5 s of ground at the
  tail so autoplay loops do not clip it. The end lasts about 8 to 9 s. The CTA URL must come from the owner (see beta-facts §5).

---

## 2. Techniques for a code-rendered film of a CLI product with no GUI

### 2.1 Treat the terminal as a typographic object, not a window

- **Leave out window chrome.** Fake macOS traffic lights imply one OS and one app, and they are a cliché. Use the brand terminal block instead:
  "Always dark (`#07060F`), mono 14px. Prompt in amethyst, output in `text-3`, the one thing to notice in pheromone" [S DESIGN.md].
  In the film the block is a plate with an 8 px radius at the brand's zoom (see 5.1), sitting on the deep field.
- **Commands and output are one unit.** Warp's core idea is that a command and its output form "one atomic unit that we call a block" [S Warp talk].
  In Dorylinae terms each block is a plate, so blocks lock in with the plate grammar (signature move A).
- **Cropped fragments.** Show 1 to 8 lines. Dim the rest to `text-3`, or let it bleed off-frame [S Lago; O Raycast].

### 2.2 Who types and who doesn't: an honest typing grammar [J]

The product is driven by agents through their shell tool, so commands do not appear one keystroke at a time. They land as whole lines,
and output streams in. The human does type in two places in the real product: the approval code in the daemon's own terminal
(`capture/24-daemon-terminals.txt`), and what they ask their agent in plain words. So:

- **Agent command:** the whole line appears in 1 frame and settles over 120 ms (a 7-frame fade and a 4 to 8 px rise).
  Output lines follow, one every 2 to 3 frames, like real streaming.
- **Human words** (the ask, the approval code) are typed letter by letter at the boot cadence of 85 ms per letter.
  Labels may type faster for longer lines (see 5.4). Charm's VHS terminal recorder defaults to 50 ms per character [S], and
  practitioners use 30 ms for README demos [S]. Both are machine-speed demo rates. For a film, human typing should look human.
- The contrast does narrative work: people speak slowly in Special Elite, machines answer instantly in mono. It is also true to the product.

### 2.3 Structured data as visual material

- `--json` output is real and very readable when enlarged (`capture/08-status-team.txt`, `10-request-send.txt`). A JSON key can
  get a **leader line at 21°** to a specimen label that names it in plain words. Example: `"status":"relayed"` gets a label "waiting at
  the relay". Keep the JSON text unchanged [J].
- **Tables are the GUI.** The `status --team` presence table (NAME, DAEMON, AGENT, HUMAN, LAST SEEN) is the closest thing the
  product has to a dashboard. Render it big, in Plex Mono, with each cell becoming a presence chip (shape-coded dot plus word).
- **Inbox ordering** (`capture/11-inbox.txt`: PRIO 3000, 2000, 1000; the high request arrived last but sits first) should be
  shown as rows moving over 240 ms with the brand ease, keeping their identity (FLIP-style). The motion shows that the high request was re-sorted to the top.

### 2.4 Signals moving between machines

- **Proof of life.** The GitHub globe team worried that arcs "could quite as easily just be design animations instead of real work".
  Their answer was to draw only real events (pull requests opened and merged) and make each line clickable [S GitHub]. Shopify draws one arc per real purchase.
  Each arc has an age from 0 to 2: 0 to 1 is the travel, 1 to 2 is the trail dissolving through noise [S Shopify].
- **For Dorylinae [J]:** every signal on screen is an event from the script, such as request, accept, result or decision. Never show ambient
  "traffic" and never show random particles. Count them. A 60 s film has perhaps 6 to 10 signals.
- **Two machines, two skies.** Each teammate's machine is a place with its own deep-field seed (the brand: "give each
  surface its own seed"). The camera travels between them through the sky. When teammates work side by side in one frame, their
  plates sit **6 px channel apart**, like the plates of one mark [J].
- **The relay is a waypoint, not a cloud icon.** Show it as a small point with a specimen label stating what it sees: who, when, how big. The real relay
  log shows only key prefixes, envelope type, id and size: `queued ... bytes=1444`, `delivered from queue count=1`
  (`capture/25-relay-log.txt`).
- **Sealing [J]:** as the trail leaves the sender, the plain brief beside it is replaced by ciphertext in one left-to-right pass at
  sweep speed, in `text-3`, with no flicker and no random characters cycling. At the receiver it resolves back. One pass each way, monochrome.
  This shows a true fact (messages are sealed on your machine and the relay carries ciphertext) without a padlock.

### 2.5 Presence, time and waiting

- Presence states are shape-coded and pulse every 2.4 s [S DESIGN.md]. The real offline flip is in `capture/09-presence.txt` and
  `15-offline-delivery.txt`.
- **Show time as labels, not clocks.** When the film compresses a wait (offline, hours), show a collector's date in a specimen label
  (`08.x.2026 → 09.x.2026`) or a real `last seen` timestamp. No spinning clocks or time-lapse blur [J].

### 2.6 Documents as specimens

- The exported Decision is real Markdown, byte-identical on both machines (`capture/19-decision.md`). `decision verify` reports both
  signatures, and a tampered copy fails with "invalid at step 2: hash does not match canonical(decision)" (`capture/18-decision.txt`).
  This is the payoff object of the film. Show it as a specimen sheet (signature move C).
- `log --verify` gives "The audit log is intact: 101 rows checked." (`capture/22-log.txt`). That is a natural use for the **glint sweep**
  (2.7). Never caption it "tamper-proof" (product-facts §5).

### 2.7 References that translate directly

| Reference | What to take | Tag |
|---|---|---|
| Linear and Lago | real product only, one action at a time, the camera does the transitions, screens bleed off-frame, no VO, no stock, no abstract 3D, storyboard and animatic review gates, a scorecard before shipping | [S] |
| Apple iPhone Air, year recap | one line per scene, no VO, each sentence a beat, words given room | [O] |
| Google Pixel 9 Pro teaser | open on a prompt typed over a dark screen, product after the line, one CTA | [O] |
| Raycast 2025 teaser | cropped, dark fragments of the real interface | [O] |
| Warp | command plus output as one block | [S] |
| Charm VHS | terminal motion is scripted and reproducible ("tapes"); typing speed is a parameter | [S] |
| GitHub globe, Shopify globe | signals are real events; arcs have an age (travel, then trail decay) | [S] |
| GitHub brand motion | "precise yet expressive"; event promos can play, technical demos must be precise; Mona Sans Mono uses a typing effect and a shifting cursor colour to suggest several contributors | [S] |
| Figma Config 2025 | motion developed together with the identity; a motion library of examples rather than rules; motifs recur in sound | [S] |
| ManvsMachine iMac Pro | one rule set governs every form and move | [S] |
| AKQA, Alexa vs Alexa+ | each scene repeats one comparison (before, then better), which helps an audience that doesn't know the product | [S] |
| Teenage Engineering | monospaced type, exposed construction, "interfaces that celebrate limitation". Show constraints proudly (5 high-urgency requests a week, a grant that expires) | [S design guide; J] |

---

## 3. Clichés to avoid, cross-checked with the brand's don'ts

Notation: **B** = also forbidden by the brand kit (quoted source in brackets). **C** = a craft cliché the brand does not mention.

### Visual effects

| Cliché | Why | Flag |
|---|---|---|
| Glitch, RGB split, datamosh, VHS tracking noise | described as the most ubiquitous motion trend; reads as a mistake without a concept [S Blue Whale, Videobolt]; GitHub warns it can look broken in context [S] | C |
| Random particles, plexus networks, "neural" glowing nodes | stock tech backdrop [S tutorials, stock libraries]; also a **swarm** visual, and "swarm" is a banned bee word [voice.md] | B (by extension), C |
| Generic neon or cyberpunk glow, bloom on UI | "Outline it, bevel it, or add glows other than the deep-field shadow and bloom" [logo.md]; bloom is "never… applied to UI" [DESIGN.md] | B |
| Blue-to-purple or mesh gradients, gradient text | "No gradients between brand colours. The only gradient in the system is the sky." "a blue-to-purple gradient" is a listed don't [DESIGN.md] | B |
| Glassmorphism, frosted panels | "glass effects" listed [DESIGN.md] | B |
| Drop shadows on UI, floating cards | "There are no drop shadows in the interface" [DESIGN.md]; screens bleed off-frame instead [S Lago] | B |
| Lens flares, light leaks | the "blatant and non-creative use" is the problem [S Creative COW] | C |
| Stock space photo, Hubble-style backgrounds | "Never a stock space photo" [DESIGN.md]; generate the sky with `fieldSvg()` | B |
| Matrix code rain, green phosphor terminals | the terminal is void with amethyst prompt and `text-3` output [DESIGN.md] | B, C |
| Scanlines over text, scanlines finer than 2 px, CRT filter over everything | "Never over text, never finer than 2px on screen" [brand-book.md] | B |
| Animating the logo (morph, spin, assemble-from-particles, glitch-in) | "Animate it in any way other than the boot sequence and a slow scanline drift" is a don't [logo.md] | B |
| Tilting, stretching, recolouring or outlining the mark; Dutch-angle camera with the mark in frame | [logo.md, DESIGN.md] | B |
| Padlocks, shields, keys for "secure"; robots, brains, sparkles for "AI" | icon-language shortcuts; the brand has no icon set and Lucide is UI-only [brand-book.md] | C |
| Isometric illustrations, Corporate Memphis people, 3D laptop or phone mock-ups for a CLI | generic SaaS explainer look [S Magic Hour: stock-style corporate explainer animations] | C |
| Spinning globe with arcs | a cliché unless the data is real [S GitHub "proof of life"]; Dorylinae has no geography to show | C |
| Fake dashboards with random numbers, fake UI | never invent UI [S Lago]; also breaks the owner's no-invented-claims rule | B (owner), C |
| Mouse cursor clicking around in a CLI product | there is nothing to click; it implies a GUI that does not exist | C |
| Confetti, check-mark explosions | GitHub reserves celebration for real completion moments [S]; the brand's success voice "states the result once" [voice.md] | B (voice), C |
| Emoji anywhere, including in terminal output | "No emoji" [brand-book.md, voice.md, DESIGN.md] | B |
| Literal ant footage or swarming ants | ant words are "seasoning: one per screen at most" [voice.md]; the mark is the ant | B (by extension) |

### Motion and editing

| Cliché | Flag |
|---|---|
| Bounce, overshoot, elastic easing, slapping or rolling text [S GitHub] | C |
| Default Easy Ease or linear moves; floaty, overly smooth AI-style motion [S Magic Hour, paraphrased] | C |
| Kinetic type flying in from every direction; words bouncing in one by one in TikTok-caption style | C |
| Letter-spacing "tracking out" reveals, 3D extruded type, stroke text | C |
| Camera shake, handheld wobble, Dutch angles | C (brand mood "quiet and exact") |
| Whip-pan smears, zoom-blur, spin and cube transitions, dissolves into the logo [S Resmed] | C |
| Intro logo sting or "Introducing…" card at the start [S Lago "no intro card"] | C |
| 3-2-1 countdowns, loading bars as suspense | C |
| Speed ramps on UI | C |
| Flashing above 3 per second [S WCAG 2.3.1]; strobing scanlines | C (safety) |

### Sound

| Cliché | Flag |
|---|---|
| A keyboard click for every character, or for agent commands | C (agents do not type; see 2.2) |
| A whoosh on every move, risers before every cut, trailer "braams", drops | C |
| Stock "inspiring corporate" track (ukulele, claps, glockenspiel) | C |
| Robotic AI voiceover [S Magic Hour, Artlist] | C |
| A sonic logo with on-screen copy [S Resmed] | C |

### Copy on screen

| Cliché | Flag |
|---|---|
| Hype words: revolutionary, seamless, supercharge, unleash | B [voice.md] |
| Exclamation marks | B [voice.md] |
| Bee words: hive, swarm, drone | B [voice.md] |
| An invented tagline | B ("There is no tagline. Set the name alone and do not invent one." [voice.md]) |
| "Dorylinae.ai", "Dory", "DoryLinae" | B [voice.md] |
| Special Elite for paragraphs, faux bold or italic, any other typewriter face | B [DESIGN.md] |
| Centred paragraphs | B ("Left-align. Centre only the stacked logo and short hero statements.") |
| "Tamper-proof", "unbreakable", "military-grade" | B (owner; product-facts §5) |

---

## 4. The Dorylinae film language

### 4.1 Translating each ingredient to film

| Ingredient | Brand rule | Film use [J unless marked] |
|---|---|---|
| **Specimen label** | label stock `#F6F3FB`, dark ink in both themes, Special Elite, inset hairline, radius 2, one or two lines, collector dates such as 04.x.2026 [S] | Every caption that names something is a label pinned beside it, typed at 85 ms per letter. These are the film's subtitles. Date labels show compressed time. |
| **Signal / scanlines** | bands every 5.634 units (pitch 0.6758 % of mark width); signal mark only at ≥300 px; faint CRT on hero imagery; never over text; drift 1 pitch per 1.6 s [S] | Only on the end-card mark and as a faint overlay of at most 5 % on deep-field-only frames. At 1080p, keep the signal mark ≥ 600 px wide (pitch ≥ 4 px) so compression and phone downscaling do not shimmer (see 6.3). |
| **Deep field** | near-black violet with a generated nebula; each surface its own seed; the only gradient [S] | The space between machines. Each teammate's machine has its own seed. The camera travels in it with 2 to 3 parallax layers moving slowly (far dust 0.2×, stars 0.5×, plates 1×). Never put text on the nebula's rose; text sits on plates or on plain void. |
| **Trail** | pheromone dotted rule, 3 px dots every 12 px, may march while in progress; connects steps or agents [S] | The only carrier of signals between agents, and the single accent in its frame. It marches 1 dot pitch per beat (600 ms) while a request is in flight and stops when delivered. At film zoom 2×: 6 px dots, 24 px pitch. |
| **Plates and 6 px channel** | surfaces on bg, radius 8, no border or shadow; plates that belong together sit 6 px apart [S] | Every composition is plates. Teammates' terminals that share a frame sit one channel apart, like plates of one mark. At film zoom 2× the channel is 12 px. |
| **Typed Special Elite** | display only, never below 14 px, one weight; wordmark always caps [S] | The human voice: hero lines and labels, typed. Plex Sans and Plex Mono are the machine voice and land whole. |
| **21° angle** | "available for cuts and slashes: 21° off vertical" [S] | The 21° cut (chapter transition); label leader lines at 21°; slashes that separate a label's fields. Never a camera roll. tan 21° = 0.384, so a 21° edge across a 1080 px tall frame shifts 415 px horizontally. |
| **Boot sequence** | 3.6 s; plates lock in from the axis outwards; name typed [S] | Plays exactly once, at the end. Its grammar (axis-out stagger, 500 ms brand-ease lock) is reused for every UI plate in the film, so the product appears to be built from the mark's own anatomy. |
| **Presence pulse 2.4 s** | the online dot may pulse every 2.4 s [S] | The film's metronome: one pulse per bar at 100 BPM. A teammate coming online is a hollow ring filling to a solid dot on a downbeat. |
| **Motion 120 / 240 / 600 ms + ease** | [S] | 120 ms: state flips (offline to online, a chip changing word). 240 ms: entrances (lines, labels, rows). 600 ms: plate locks, camera arrivals, the 21° cut. Longer camera moves are multiples of the beat: 1.2, 2.4, 4.8 s. |
| **Two-step depth** | bg, surface, surface-2; no shadows; bloom only behind the mark [S] | Depth comes from ground steps and from focus. Out-of-focus plates blur and fall to `surface`; the focal plate is `surface-2`. Bloom appears only on the end card. |

### 4.2 The film clock

100 BPM is chosen so the brand's own numbers become the rhythm [J]:

| Unit | Duration | Frames at 60 fps | Brand meaning |
|---|---|---|---|
| beat | 600 ms | 36 | `slow` token; one plate lock; one trail dot-step |
| half beat | 300 ms | 18 | |
| bar (4/4) | 2.4 s | 144 | presence pulse |
| boot | 6 beats = 3.6 s | 216 | sonic logo length |
| shot | 1.5 to 2 bars typical | 216 to 288 | |
| letter | 85 ms | 5.1 (render at exact time) | boot typing cadence |

A code renderer evaluates every animation at the exact time t = frame / 60, so durations never need rounding. Only discrete events
(cuts, a letter appearing) snap to frames. At 60 fps the beat is exactly 36 frames, so cuts fall on beats exactly. At 24 fps a beat is
14.4 frames, which is one more reason not to use 24 [J].

### 4.3 Three signature moves only Dorylinae could do

**A. Lock-in: the product builds itself from the mark's anatomy.**
Every UI element in the film arrives the way the 23 plates of the mark arrive in the boot. Terminal blocks, presence chips, inbox
rows, the Decision sheet: each locks in from the composition's axis outwards, with a 45 ms stagger, a 500 to 600 ms brand-ease
settle, and offsets taken from `motion.ts` (sides ±46 px, top elements −18 to −30 px, bottom elements +40 px, at film zoom).
Plates that belong together close to exactly one channel apart. The mark itself never animates during the film. In the final
shot the real boot plays, unchanged, and the viewer recognises the grammar they have been watching all along.
*Why only Dorylinae:* the motion is derived from this mark's geometry and its 6 px channel. It is a quotation of the logo, not a style.
*Guardrails:* do not use it as a transition between unrelated scenes. It builds a scene, it does not change scenes. No overshoot.

**B. The pheromone trail: the only thing that moves between machines.**
Requests, results and decisions travel as the brand's dotted rule in pheromone. It is the single accent of its frame, so the viewer's eye always
knows where to go. It marches one dot pitch per beat while in flight. On arrival the receiver's presence chip pulses once and the trail fades from
its tail over one bar (the Shopify age 1 to 2 decay, done with dots) [S Shopify; J].
The offline beat is the hero: the teammate's dot is a hollow ring, so the trail **stops at the relay** and holds, still and dimmed, with
a label "waiting at the relay". The relay log line `queued … bytes=1444` sits beside it. The time label advances. The ring
fills to a solid dot on a downbeat, and the trail starts marching again into the inbox. This is true to the product: the 7-day offline queue,
and the "delivered when theo is back" output in `capture/15`.
*Why only Dorylinae:* army ants navigate by pheromone trails, the brand already defines the trail token and the "may march" rule, and the
product really does store and forward.
*Guardrails:* one trail in flight at a time. Never decorative, never looping, and never used for anything that does not happen in the product.

**C. The determination label: every claim is a typed specimen label.**
On a museum specimen, "det." names the person who identified it, and the voice doc's own example label is "Formicidae: Dorylinae. det. 2026".
In the film each statement is typed onto a label-stock card pinned beside the thing it describes, with a 21° leader line, collector-style dates
and small Plex Mono field names. The payoff: the debate's Decision appears as a specimen sheet with **two determinations**, one
per signing daemon. The real `decision verify` output confirms both signatures. Then the tampered copy's label types
"invalid at step 2" in `sting` with its bar (status never relies on colour alone), the one place in the film that colour appears.
*Why only Dorylinae:* the specimen label is the brand's own component and dating system, and a Decision really is a determination signed by the
daemons that took part (both, in the captured run; product-facts notes it can also be single-signed).
*Guardrails:* labels are one or two lines (brand rule). "Signed" does not mean "agreed", so the copy must not imply consensus
(product-facts §3.16, §6 #9). Keep each label's text inside the reading-time budget in 5.4.

**Supporting grammar** (not signature moves, but part of the language):

- **Glint sweep as verification.** The boot's 12-unit glint band (sweep curve `(0.4,0,0.6,1)`, 750 ms) passes once down
  the audit log during `log --verify`, and the line "The audit log is intact" lands as it leaves. Use it once in the film.
- **The 21° cut**, for at most two chapter changes (1.10).
- **Pulse metronome.** Presence dots pulse on beat 1 of each bar, so sound and picture share one clock even with sound off.

---

## 5. Typography in motion

### 5.1 Map brand tokens to the frame with a zoom factor, not new sizes [J]

Treat each video frame as the brand's UI canvas at a zoom. Every token then keeps its proportions: channel, radius, type steps.

| Format | Pixels | Zoom | Canvas in brand px | Why |
|---|---|---|---|---|
| 16:9 | 1920 × 1080 | 2× (hero moments 3×) | 960 × 540 | 1080p plays at about 960 to 1280 CSS px on laptops, so tokens land near their native size |
| 4:5 | 1080 × 1350 | 3× | 360 × 450 | a phone shows 1080 px across about 393 pt, so 3× puts tokens near their native point size |
| 9:16 / 1:1 | 1080 wide | 3× | 360 wide | same width as 4:5 |

Brand type at zoom (px in the video frame):

| Style | Base | 16:9 at 2× | 4:5 at 3× |
|---|---|---|---|
| display-xl (Special Elite) | 72 | 144 | 216 (too large for most lines; use display-l) |
| display-l (Special Elite) | 48 | 96 | 144 |
| display-m (Special Elite) | 32 | 64 | 96 |
| heading-l (Plex Sans 600) | 28 | 56 | 84 |
| body-l (Plex Sans) | 18 | 36 | 54 |
| body (Plex Sans) | 16 | 32 | 48 |
| code (Plex Mono) | 14 | 28 | 42 |
| label (Plex Mono caps, 0.08em) | 12 | 24 | 36 |
| specimen (Special Elite) | 14 | 28 | 42 |
| channel | 6 | 12 | 18 |
| trail dot / pitch | 3 / 12 | 6 / 24 | 9 / 36 |

### 5.2 Minimum on-screen sizes

How video pixels become points on a device [J, arithmetic; Apple HIG sizes S]. Apple's guidance gives iOS a 17 pt default and an 11 pt
minimum, and tvOS (a 1920 × 1080 canvas viewed from 8 to 12 ft) a 29 pt default and a 23 pt minimum [S Apple HIG].

| Viewing case | pt per video px | 11 pt (min) needs | 17 pt (default) needs |
|---|---|---|---|
| 16:9 inline on a portrait phone (≈393 pt wide) | 0.205 | 54 px | 83 px |
| 16:9 full-screen landscape phone (≈852 pt) | 0.444 | 25 px | 38 px |
| 16:9 on a TV (tvOS canvas 1:1) | 1.0 | 23 px (tvOS min) | 29 px (tvOS default) |
| 4:5 / 9:16 / 1:1 on a phone, full width | 0.364 | 30 px | 47 px |
| 4:5 in a LinkedIn desktop feed (≈555 px wide) | 0.514 | 21 px | 33 px |

Rules derived from the table [J]:

- **16:9:** any line that carries the story is **≥ 64 px**, and hero lines are 96 to 144 px. Text from 28 to 56 px (terminal
  output, labels) is texture. It can be read full-screen but not inline on a phone, so the camera **pushes in** until the one
  token that matters is ≥ 64 px. Nothing that must be read is below 28 px.
- **4:5:** story lines ≥ 48 px, hero lines 96 to 144 px, terminal text 36 to 42 px. Nothing that must be read is below 32 px.
- Special Elite is a distressed face. Set it one step larger than Plex for the same importance, and never below 28 px (16:9) or 42 px (4:5) [J].
  Measured metrics: Special Elite cap height 0.689 em, x-height 0.481 em; Plex cap height 0.698 em, x-height 0.516 em [J, from the font files].
- Contrast: text tokens are rated 4.5:1 on bg, surface and surface-2 [S DESIGN.md]. Video compression lowers contrast in dark scenes, so
  small text sits on a plate, not on the nebula [J].

### 5.3 Safe areas

| Format | Rule | Pixels |
|---|---|---|
| **16:9, 1920 × 1080** | title (graphics) safe 90 %, action safe 93 % [S SMPTE ST 2046-1, EBU R 095] | text inside x 96 to 1824, y 54 to 1026; action inside x 67 to 1853, y 38 to 1042. The brand's largest space token, 96, equals the title-safe side margin |
| **4:5, 1080 × 1350** (feed: Instagram, LinkedIn, X) | no broadcast standard. The Instagram profile grid crops to 3:4 (1080 × 1440 tiles), which on a 4:5 post trims about 34 px from each side [S/J]. Feed players put mute and tag icons in the bottom corners [J] | margins 64 px left and right (`space-8`), 64 px top, 128 px bottom for anything that must be read. Safe box 952 × 1158 |
| **9:16, 1080 × 1920** (Reels, Shorts, TikTok) | platform interfaces cover the top ~120 to 250 px, the bottom ~300 to 480 px and the right ~96 to 180 px [S, third-party measurements] | text inside x 64 to 900, y 250 to 1440. Keep titles also inside the central 1080 × 1350 band (y 285 to 1635), which survives the 4:5 feed crop of a Reel cover [S] |
| **1:1, 1080 × 1080** | 5 % [J] | 54 px all round; 64 px is better |

Per-format layout [J, following Lago]: do not crop 16:9 into 4:5. Re-lay each scene from the same scene graph with format-specific
constraints, using focal coordinates instead of blind crops [S Lago]. In 4:5, the two machines stack vertically with the trail running
top to bottom. In 16:9 they sit side by side.

### 5.4 Reading time

Sources [S]: average silent reading of English nonfiction is 238 wpm (range 175 to 300) [Brysbaert]. Subtitles while watching run at
160 to 180 wpm, about 0.33 to 0.375 s per word [BBC]. Netflix English allows up to 20 characters per second for adults (17 for
children), 42 characters per line, 2 lines at most, and holds between 5/6 s and 7 s [Netflix]. SSW: hold text long enough to be read
twice by a 200 wpm reader [SSW]. Rules of thumb: 2 to 3 words per second [Biteable]; a hook or title 2 to 4 s, a product benefit
2 to 3 s [Renderforest].

Our hold times [J], counted from the moment the line has **fully landed** (after typing finishes), rounded up to the half beat:

| Words | Floor: max(1.5, 0.5 + 0.35w) | Key claim: max(2.0, 0.5 + 0.6w) | On the 100 BPM grid |
|---|---|---|---|
| 2 | 1.5 s | 2.0 s | 2.1 s (3.5 beats) |
| 4 | 1.9 s | 2.9 s | 3.0 s (5 beats) |
| 6 | 2.6 s | 4.1 s | 4.2 s (7 beats) |
| 8 | 3.3 s | 5.3 s | 5.4 s (9 beats) |
| 10 | 4.0 s | 6.5 s | 6.6 s (11 beats); better to split into two lines |

- **Commands and IDs** are not words. Budget them at no more than 15 characters per second (below Netflix's adult ceiling, because
  technical strings are harder). A 40-character command needs about 2.7 s on screen after it lands. Better: highlight one token in
  pheromone and let the rest be texture [J].
- **Typing adds time.** At 85 ms per letter a 20-character label takes 1.7 s to type. So type only short labels at the boot
  cadence. Type longer hero lines at 40 to 50 ms per letter (20 to 25 characters per second, which matches Netflix's reading ceiling,
  so the eye keeps up). Or set them whole with a 240 ms entrance [J].
- **Line length:** hero lines up to about 32 characters, 2 lines at most. Supporting lines up to 42 characters (Netflix). The BBC limits
  online subtitle lines to 68 % of a 16:9 frame's width [S].
  Characters per line at the recommended sizes, using measured average advances (Special Elite 0.526 em, Plex Sans 0.435 em,
  Plex Mono 0.600 em) [J]:

| Face and size | 16:9 safe width 1728 px | 4:5 safe width 952 px |
|---|---|---|
| Special Elite 144 px | 22 chars | — |
| Special Elite 96 px | 34 | 18 |
| Special Elite 72 px | — | 25 |
| Plex Sans 64 px | 62 (keep to 42) | — |
| Plex Sans 48 px | — | 45 |
| Plex Mono 56 px | 51 | — |
| Plex Mono 42 px | — | 37 |

  Real commands run to 70 characters or more (`capture/10`). Break them at flags with a ` \` continuation, which is real shell syntax
  and is how the CLI docs print them. In 4:5 keep each line to 36 characters or fewer.
- **One text element at a time** carries the story. The label and the terminal line it describes never both ask to be read in the same beat [J].

---

## 6. Delivery and technical craft for a code-rendered film

### 6.1 Frame rate and motion blur

- **60 fps master.** 24 fps on a 60 Hz display uses a 3:2 cadence, so slow pans and scrolling text judder. 60 fps is recommended for UI, with 30 fps as a
  compromise [S Fora Soft, Nilo]. 30 divides into 60, so it shows each frame for an equal time [J].
- Motion blur: when the camera moves faster than about 1 % of the frame width per frame, integrate over half a frame (a 180° shutter
  equivalent; at 60 fps that is 1/120 s [S]) with 8 to 16 sub-samples. Text in a hold must be perfectly sharp [J].
- Never blur text during a reading hold. Depth-of-field blur applies only to plates that are not being read [J].

### 6.2 Banding and colour in compression

- Dark violet gradients (the nebula) and near-black fields band badly in 8-bit H.264. Render at 16-bit or float, add about 1 LSB
  of triangular or blue-noise dither before quantising to 8-bit, and encode at a high bitrate. Consider a 1440p or 4K upload
  (practitioners report that YouTube gives higher-resolution uploads better codecs) [J].
- 4:2:0 chroma subsampling halves colour resolution, so thin saturated pheromone details smear. Make trail dots at least 6 px at 1080p,
  and never set small text in pheromone [J].
- Keep the ground at `#07060F`, not pure black. Check that the encoder keeps full-range versus limited-range levels consistent with the
  player, so the void does not turn grey [J].

### 6.3 Scanlines and aliasing

The scanline pitch is 0.6758 % of the mark's width. Mark 300 px wide: 2.03 px pitch (the brand's floor); 600 px: 4.05 px;
900 px: 6.08 px [J, from `SCAN.pitch` 5.634 / `MARK_BOX.w` 833.74]. A 16:9 video shown inline on a phone is scaled to about
0.6 device px per video px. So a 4 px pitch becomes about 2.5 device px and can shimmer. Keep the end-card mark at 600 px or more
(16:9), and check it on a phone. If it shimmers, drop the drift and keep the scanlines static [J].

### 6.4 Accessibility

- No more than 3 flashes per second [S WCAG 2.3.1]. The glint sweep is a single pass, not a flash.
- Keep large-area fast motion and strong parallax rare. They are vestibular triggers [S W3C 2.3.3, design-system guides]. The brand's
  rule that "everything stops under reduced motion" has a film equivalent: a still poster frame and a captioned transcript on the page
  that hosts the film [J].
- Burn in all text. There is no voiceover, so the on-screen text is the whole message, and it doubles as captions [J].

### 6.5 Review process (adopted from Lago, adapted) [S/J]

1. A storyboard in machine-readable form, with every on-screen sentence tied to a line in product-facts or beta-facts.
2. An animatic with grey plates, real camera moves, cuts on the beat grid, and frame counters.
3. A contact sheet of review frames per shot: arrival, settled, action, payoff, cut.
4. A brand lint: one pheromone element per frame, no text over scanlines, sizes at or above the 5.2 floors, safe areas, no banned words.
5. Masters: 16:9 and 4:5 (plus a silent master), a poster frame, and a loudness report.

---

## 7. Honesty rules for showing the product (the owner's hard rules applied to film craft) [J]

1. Terminal text on screen is **copied from `film/capture/`**, character for character. The allowed edits are: omit whole lines, crop
   long lines at the frame edge, shorten IDs the way the CLI itself does (`t-56f7…5110`, `r-0282…`), and break long commands with ` \`.
2. **Never show:** version strings with commit hashes (`0.0.0-dev+860afe8`; this exposes the repository), file paths that expose the build
   machine (the approval prompts in `capture/24` contain a long temporary path, so crop it), `ws://127.0.0.1:8787`, `relay.dorylinae.net`,
   GitHub URLs, the Homebrew tap handle, or ticket numbers (product-facts §3.23, beta-facts §5).
3. **Harness names** appear in the real captures (`claude-code`, `codex`). Showing them is an open owner decision (product-facts §7.3).
   Until it is decided, crop them out.
4. **Desktop notifications:** the capture shows the notification test *failed* because the container is headless (`capture/14`).
   Do not draw an OS notification banner unless one is captured on a real desktop. A drawn banner would be invented UI.
5. **Time compression** is labelled (date labels, `last seen` timestamps), never hidden.
6. Diagrams (trail, relay point, plates in the sky) are clearly diagrammatic. They must not look like a GUI the product does not have.
7. Anything planned rather than built appears only on a screen that says so (product-facts §4, beta-facts §6).

---

## 8. Questions for the owner (craft choices that need permission)

The request relayed from the owner invites questions where a better route might need permission. These are the craft choices we are unsure we may make:

1. **Zooming brand tokens (5.1).** Rendering the brand's sizes at 2× (16:9) and 3× (4:5) means the trail becomes 6 px dots every 24 px
   and the channel 12 px in video pixels. The proportions are unchanged. Is that within the brand rules? (Recommended: yes.)
2. **Harness names** (`claude-code`, `codex`) visible in real output. Show them, or crop them? (product-facts §7.3)
3. **"We" on the beta card.** The voice allows "we" only where a person at Dorylinae is speaking. The invitation is that case.
   May the end card use "we"?
4. **The boot plays only at the end** (no intro card), and the mark does not appear anywhere else in the film. Is that acceptable?
5. **The CTA URL.** beta-facts §5 says the waitlist form does not exist yet. The end card needs a real destination before render.
6. **Specimen-label language as copy** ("det.", collector dates) is used for labels, which is exactly the brand's use. Confirm
   the Decision-as-determination metaphor is welcome.
7. **Original score composed in code** at 100 BPM with a small sound palette and no licensed music. Confirm there is no existing
   sonic identity we should use instead.

---

## 9. Sources

Reference films and studios
- Lago, "We killed our motion design job" (Linear-style film rules, review gates): https://getlago.com/blog/we-killed-our-motion-design-job
- moonb, "Kinetic typography" (Apple iPhone Air analysis, restraint, phone test): https://www.moonb.io/blog/kinetic-typography
- moonb, "12 product launch videos, sorted by format" (Raycast, Pixel 9 Pro, Linear, Warp, Oura, Dyson, Figma, Samsung durations): https://www.moonb.io/blog/product-launch-video
- Rishi Shah, "I watched Apple's 108-second event" (Contra): https://contra.com/p/OoccPFZm-i-watched-apples-108-second-event
- Mat Voyce, "Think Different" kinetic typography (Abduzeedo): https://www.abduzeedo.com/mat-voyces-think-different-kinetic-typography-mass
- Cannes Lions, ManvsMachine and Apple "Power to the Pro": https://www.lovethework.com/work-awards/campaigns/power-to-the-pro-602551
- It's Nice That, ManvsMachine iMac Pro film: https://www.itsnicethat.com/news/manvsmachine-algorithmic-architecture-imac-pro-film-210318
- Buck, Apple "Power to the Pro": https://buck.co/work/apple-power-to-the-pro
- Basement Studio, "Behind the Ship" (Vercel Ship 2025 opener, animation on twos): https://basement.studio/post/behind-the-ship
- Figma, "How we shaped the visual identity for Config 2025" (15 fps, motion library, sound motifs): https://www.figma.com/blog/how-we-shaped-the-visual-identity-for-config-2025/
- It's Nice That, Config 2024 identity and Relay: https://www.itsnicethat.com/features/figma-config-2024-identity-graphic-design-spotlight-090724
- GitHub Brand, motion principles: https://brand.github.com/motion/principles
- GitHub, "How we built the GitHub globe": https://github.blog/engineering/how-we-built-the-github-globe
- Shopify Engineering, "How we built Shopify's BFCM 2023 globe": https://shopify.engineering/how-we-built-shopifys-bfcm-2023-globe
- Stripe, "To design and develop an interactive globe": https://stripe.com/blog/globe
- Motionographer, "Interview: Motion design and the Google identity": https://motionographer.com/2015/11/09/interview-motion-design-and-the-google-identity/
- aescripts, Made by Google 2018 (Brand New School): https://aescripts.com/learn/made-by-google-2018
- AKQA, Amazon "Alexa does it better": https://akqa.com/work/amazon/alexa-does-it-better
- Samsung Unpacked "liquid" teaser coverage (ManvsMachine): https://jai-un-pote-dans-la.com/samsung-tech-dooh-marketing-teaser-asie-led-affichage/ ; Cannes Lions "Liquid Clues": https://www.lovethework.com/work-awards/campaigns/liquid-clues-708791
- Warp blocks (launch party transcript summary): https://plushcap.com/analysis/warp/youtube/aU9B6OyCV60 ; design guide: https://blakecrosley.com/guides/design/warp
- Teenage Engineering design guide (constraints as aesthetics): https://blakecrosley.com/pt-BR/guides/design/teenage-engineering
- Galaxus, "Apple's keynote was shot on iPhone: what that means exactly": https://www.galaxus.ch/en/page/apples-keynote-was-shot-on-iphone-what-that-means-exactly-30281
- Apple Siri settlement: https://gizmodo.com/apple-settles-alleged-false-advertising-suit-over-ai-powered-siri-2000752474 ; https://www.mediapost.com/publications/article/414896/apple-settles-250-million-ai-marketing-lawsuit.html ; WWDC 2026 demo shift: https://techcrunch.com/2026/06/08/apples-wwdc-ai-demos-looked-more-real-after-250m-false-ad-settlement/ ; keynote-demo practice discussion: https://pxlnv.com/linklog/wwdc-2025-live-demos/

Motion, editing and timing
- Walter Murch, Rule of Six (summary): https://studiobinder.com/blog/walter-murch-rule-of-six
- James Cutting, shot length (Cornell): https://news.cornell.edu/node/271563 ; i-Perception "Quicker, faster, darker": https://pmc.ncbi.nlm.nih.gov/articles/PMC3485803/
- WWDC23 "Animate with springs" notes: https://wwdcnotes.com/notes/wwdc23/10158
- Material Design 1 duration and easing: https://m1.material.io/motion/duration-easing.html ; Atlassian motion: https://atlassian.design/foundations/motion/applying-motion
- Easing curves (Flutter Curves; Creative COW Easy Ease discussions): https://api.flutter.dev/flutter/material/Easing-class.html ; https://community.adobe.com/t5/after-effects-discussions/changing-the-ease-default-values/m-p/10906650
- Charm VHS: https://github.com/charmbracelet/vhs
- Frame rate and judder: https://www.forasoft.com/learn/video-encoding/articles/frame-rate-24-60-120-fps-judder-motion-blur ; https://josephnilo.com/blog/24-vs-30-vs-60-fps/

Reading, type and safe areas
- Netflix English (USA) Timed Text Style Guide: https://partnerhelp.netflixstudios.com/hc/en-us/articles/217350977-English-USA-Timed-Text-Style-Guide ; General requirements: https://backlothelp.netflix.com/hc/en-us/articles/215758617
- BBC Subtitle Guidelines (via summaries): https://clevercast.com/bbc-subtitling-guidelines
- Brysbaert 2019, reading rate meta-analysis: https://www.Gwern.net/doc/psychology/linguistics/2019-brysbaert.pdf
- SSW rule on text reading time: https://www.ssw.com.au/rules/post-production-do-you-give-enough-time-to-read-texts-in-your-videos
- Biteable word count: https://biteable.com/blog/video-word-count/ ; Renderforest: https://www.renderforest.com/blog/how-to-add-animated-text-to-videos
- Apple HIG typography (default and minimum sizes): https://developer.apple.com/design/human-interface-guidelines/typography
- SMPTE ST 2046-1 summary (NAB): https://nab.org/xert/scitech/2010/TVTechCheck/TV031510.asp ; EBU R 095: https://tech.ebu.ch/publications/r095
- Short-form safe zones: https://reap.video/blog/short-form-video-safe-zones ; https://moda.app/tools/story-safe-zone-checker ; Instagram 3:4 grid: https://help.hopperhq.com/en/articles/10907941-instagram-s-vertical-grid
- LinkedIn video and sound-off claims: https://business.linkedin.com/en-uk/marketing-solutions/blog/posts/content-marketing/2017/Breaking-the-sound-barrier-for-B2B-video-content ; https://iabuk.com/opinions/we-cant-hear-you-engaging-your-silent-video-viewers
- Caption sizes on phones (third-party): https://blitzcutai.com/blog/best-caption-size-tiktok-2026

Sound
- Lenovo sonic identity: https://origin-brandworld.lenovo.com/sonic-identity/ ; Resmed brand sting: https://brand.resmed.com/brand-sting
- Listen, product sounds: https://wearelisten.com/what-is-sonic-branding-pt-3 ; Zelig, Vinted UI sounds: https://www.zeligsound.com/vinted-in-app-ui-sounds
- Loudness targets: https://www.forasoft.com/learn/audio-for-video/articles-audio/lufs-targets-per-platform-2026 ; https://www.soundonsound.com/sound-advice/q-how-loud-should-master-tracks-intended-streaming

Clichés and AI slop
- Magic Hour on AI slop: https://magichour.ai/blog/ai-generated-slop-is-taking-over-social-media ; Artlist: https://artlist.io/blog/what-is-ai-slop-and-why-it-matters-for-video-creators/
- Peter Quinn's cliché showreel: https://www.premiumbeat.com/blog/how-to-create-a-cliche-demo-reel-a-qa-with-peter-quinn
- Glitch overuse: https://bluewhalemedia.co.uk/top-motion-graphics-video-effects/ ; https://videobolt.net/motion-graphics-glossary/glitch-effect

Accessibility
- WCAG 2.3.1 Three Flashes: https://www.w3.org/WAI/WCAG22/Understanding/three-flashes-or-below-threshold.html ; WCAG 2.3.3: https://w3.org/WAI/WCAG21/Understanding/animation-from-interactions

Source-quality note: many figures come from vendor or agency blogs, some dated 2026 and some repeating each other. Safe-zone
pixels for social apps are third-party measurements, not published specs. No primary source was found for how Apple's, Linear's,
Raycast's or Stripe's own launch films were produced. Where this document describes those films, it relies on secondary descriptions [O].
