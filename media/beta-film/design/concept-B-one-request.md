# Concept B: When theo is back

## 1. Title and logline

**When theo is back**

One real request, `r-97ae…` ("Track down the flaky payments test"), travels from your agent across the deep field to your
teammate's agent. It waits, sealed, at the relay while he is offline, lands when he is back, and brings the answer home. Then
the film invites small teams to apply.

The title is the product's own words. The CLI printed them in the capture:
`it will be delivered when theo is back.` (`capture/15-offline-delivery.txt`).

---

## 2. Strategy

### 2.1 Why one request suits people who know nothing about Dorylinae

A stranger cannot learn a category ("local-first coordination layer") in 60 seconds, but they can follow an object.
A request is easy to follow. It has a sender and a receiver, and you can read what it asks: a flaky test that nobody owns.
It meets an obstacle (the receiver is offline), waits, and resolves (it is delivered and answered). Every product idea in the
film is a stage on that one journey, so the viewer learns the product by watching the request move. Nobody has to read a
feature list.

The film rests on one fact in the captures. Exactly one request in `capture/` made the whole journey in one continuous run:
`r-97ae…`.

| Stage | Real evidence | File |
|---|---|---|
| Teammate offline | `theo  offline  -  -  2026-10-08T13:41:37Z` | `15-offline-delivery.txt` |
| Sent with a 3-line brief | `Queued normal task request r-97ae… to theo (team atlas)`, then `it will be delivered when theo is back.` | `15`, brief text in `20` (JSON `brief`) and `run/work/flaky-brief.md` |
| Waits at the relay | `msg=queued … type=mail id=m-b2d3… bytes=1444` (the same `mail_id` as maya's `--json` output in `15`) | `25-relay-log.txt` |
| Theo comes back | `peer connected … ZhbF0Hsy`, `delivered from queue … count=1` | `25` |
| In theo's inbox | `r-97ae…  maya  task  normal  2000  0m  Track down the flaky payments test` | `15` |
| Accepted | `Accepted r-97ae… from maya` | `20-grant.txt` |
| Answer comes back | `result: pass: Cause found: two tests share one ledger row` | `21-quarantine-release.txt` |

So when the film says "one request", that is literally true. Every terminal line belongs to the same id, and every trail on
screen stands for a real event in that run. This is craft.md §2.4's "proof of life" rule: signals are real events, never
ambient traffic.

### 2.2 What the evidence asks for, and how this concept meets it

- **Length.** Evidence says to keep the master under a minute, at 60 s (55 to 65), because about half the viewers are gone
  by the midpoint and the proposition must be clear by second 40 (evidence.md, Implications §1). This film runs **62.4 s**
  (26 bars). By 9.6 s the viewer knows what Dorylinae does, by 21.6 s how to apply, and by 40.8 s they have seen the whole
  journey.
- **Hook** (evidence.md Q3 and Implications §2: a 1.7 s mobile glance, at least two shots or states in the first 5 s, the
  brand by second 3 to 5). The opening is a direct address a developer recognises in one glance: *"You have a coding
  agent."* At 2.4 s the camera pulls back to reveal a second machine (*"So does your teammate."*). At 4.8 s the cut lands
  the name (*"Dorylinae carries work between your agents."*). That gives two framings plus a cut in the first 5 s, with the
  name fully in by 5.04 s. The hook also qualifies the audience: people who do not work with a coding agent on a team drop
  out, which evidence §5.3 asks for.
- **Explain, then show** (Q4). Lee, Chu and Roy (2024) found that radically new products need a benefit-level statement
  first, so the film opens with one plain sentence. Ringler et al. (2024) found that a step-by-step process demonstration
  beats an outcome-only one, and Frontiers (2022) found that showing the product in use beats showing how it looks, so the
  rest of the film is the process itself, in real output. Developers asked for "no translator needed" and "show real
  output" (Mighty & True), so every frame of product is captured CLI text, typeset.
- **Silent-first** (Q2, Implications §3). There is no voice. Every claim is burned-in type. Sound adds feeling only: a
  two-heartbeat presence motif and the trail's pulse support what the picture already says. Wordmarks are kept out of the
  caption zones (Verizon/Publicis).
- **Call to action** (Q5, Implications §5). There is one destination, `dorylinae.net`, repeated identically:
  - a quiet persistent tag, **CLOSED BETA ⟋ dorylinae.net**, from 21.6 s, which signals the call before the midpoint
    (Wistia: mid-roll CTAs 22.1 % against 16.9 % post-roll);
  - the beta card;
  - the end card, held about 4 s.

  Scarcity is stated as a fact ("The first wave opens with 10 teams."), never as a timer. Barton et al. (2022) found
  scarcity helps most for unfamiliar brands (δ = 0.41), and Mathur et al. (2019) show that fake urgency is a dark pattern.
- **Trust for a sceptical audience** (Q5.3: 46 % of developers distrust AI accuracy). Trust is shown inside the story
  rather than in a separate block: the request waits **sealed**, and the relay sees only a line of routing data that ends
  in `bytes=1444`. That is the film's signature moment (section 5).

### 2.3 Balance: explaining, showing, calling

| Block | Evidence target (Implications §2) | This film | Share |
|---|---|---|---|
| Hook | 0–4 s, ~7 % | 0.0–4.8 s | 8 % |
| What it is, for whom | 4–20 s, ~27 % | 4.8–9.6 s (one sentence), then a plain caption on every step to 16.8 s | 19 % pure explanation |
| Demonstration | 20–46 s, ~43 % | 9.6–45.6 s, one continuous journey | 58 % |
| Trust and control | 46–52 s, ~10 % | inside the demo, 21.6–33.6 s (the sealed wait at the relay) | (19 % of the film, counted in the demo) |
| Call | 52–60 s, ~13 % | tag from 21.6 s; beta card 45.6–52.8 s; end card 52.8–62.4 s | 27 % (3.6 s of it is the brand boot) |

The demonstration takes a bigger share than evidence suggests, for a reason. In a story-led film the explaining happens
*inside* the demonstration: each step carries one plain caption that names what the viewer is seeing ("Your agent sees who is
online.", "Your agent writes the brief.", "It waits at the relay, up to 7 days."). So by word count, about 35 % of the film
explains, which matches evidence. It just never stops the story to do so.

The call comes at the end because the story needs to finish first. Once the answer is home, the viewer has watched a team
like theirs use the product, and the beta card arrives as "here is how your team gets this". The mid-film tag keeps the
destination visible for the half of viewers who leave early.

### 2.4 Craft.md rules this concept leans on

- **§0 spec.** 100 BPM grid, 60 fps, 16:9 at 2× zoom and 4:5 at 3×, reading-time rules from §5.4, safe areas from §5.3.
- **§1.2 / §1.7.** One sentence carries one scene; one accent per view; one moving thing plus at most one settling thing.
- **§1.10.** Hard cuts on beats. The 21° cut is used exactly twice, for chapter changes: into the deep field at 21.6 s and
  into the call at 45.6 s.
- **§2.2 typing grammar.** Agent commands land whole; no human typing appears in this cut (see Risks for the optional
  approval beat).
- **§2.4 / §2.5.** The trail is the only thing that moves between machines; the relay is a waypoint, not a cloud. No clock
  is shown, and the only times on screen are real timestamps.
- **§4.3 signature moves.**
  - **B (pheromone trail)** is the hero.
  - **A (lock-in)** builds the brief from three plates in the boot's own order and stagger, and the boot closes the film.
  - **C (specimen labels)** is used lightly, for the names `maya`, `theo` and `relay`.
- **§7 honesty.** Every terminal character comes from `capture/`, and every edit is one of the allowed ones (listed per
  block).

### 2.5 Where this concept departs from the direction, and why

1. **"Lands at the top of the inbox" became "lands in the inbox".** The only request with a real end-to-end journey
   (`r-97ae…`) is `normal` urgency. It lands **second**, under an older request of the same priority (`15`). The high-urgency
   request that does sit at the top (`r-0282…`, `11`) was sent while theo was online and never went through the relay queue.
   Splicing the two would be a composite of two different requests, so the film shows the inbox as captured and makes no
   ordering claim. If the owner wants the "most urgent first" beat, the fix is a one-take re-capture (a `high` request to an
   offline teammate), not an edit.
2. **No human approval beat.** The real journey of `r-97ae…` includes a read grant approved on maya's daemon terminal
   (`24`: `a-1a9f03 248765`, then `approved`) and a quarantine release. Showing it would add a fifth product idea and about
   5 s. Craft.md §0 caps a film at 4 ideas, and evidence asks for 3 capabilities at most. The four kept are presence, a
   request with a brief, the sealed offline wait, and accept-to-answer. The omitted steps happened between lines that are
   shown, and omitting whole lines is an allowed edit. Risks (§7) gives an insert-ready variant.

---

## 3. The film, shot by shot

### 3.0 Build constants (apply to every shot)

**Clock.** 100 BPM. 1 beat = 0.6 s = 36 frames at 60 fps. 1 bar = 2.4 s = 144 frames. Notation `B:b` = bar B (1-based),
beat b (1–4); `+½` = 0.3 s later. Shot end times are exclusive: the start of the next shot.

**Total: 62.4 s = 26 bars, 1:1 → 27:1.**

| Shot | Name | Start s | End s | Start B:b | End B:b | Dur s |
|---|---|---|---|---|---|---|
| S1 | Two agents | 0.0 | 4.8 | 1:1 | 3:1 | 4.8 |
| S2 | What it is | 4.8 | 9.6 | 3:1 | 5:1 | 4.8 |
| S3 | Presence | 9.6 | 13.2 | 5:1 | 6:3 | 3.6 |
| S4 | The brief | 13.2 | 16.8 | 6:3 | 8:1 | 3.6 |
| S5 | Sent | 16.8 | 21.6 | 8:1 | 10:1 | 4.8 |
| S6 | Into the field | 21.6 | 26.4 | 10:1 | 12:1 | 4.8 |
| S7 | The wait (signature) | 26.4 | 33.6 | 12:1 | 15:1 | 7.2 |
| S8 | Inbox, accepted | 33.6 | 38.4 | 15:1 | 17:1 | 4.8 |
| S9 | The way back | 38.4 | 40.8 | 17:1 | 18:1 | 2.4 |
| S10 | The answer | 40.8 | 45.6 | 18:1 | 20:1 | 4.8 |
| S11 | Beta card | 45.6 | 52.8 | 20:1 | 23:1 | 7.2 |
| S12 | Boot and call | 52.8 | 62.4 | 23:1 | 27:1 | 9.6 |

Average shot length is 5.2 s. That is slightly above craft's 3.6 to 4.8 s, because S7 (the payoff) and S12 (the end card) are
long holds, which §1.2 allows. No shot exceeds 9.6 s.

**Colours** (DESIGN.md tokens only):
- ground `bg #07060F`;
- terminal blocks `#07060F`, document plates `surface #0E0C1F`;
- command text `text-2 #A79FC4`, prompt `brand #A678E6`, output `text-3 #8C85AB`;
- the one thing to notice: `accent #D653E1` (pheromone);
- presence: `online #5CE6C8` solid dot, offline as a hollow ring in `text-3`;
- a trail paused at the relay turns `line-strong #6E64A8`.

The sky is the only gradient. There is no grade or LUT.

**Pheromone budget.** At most one pheromone element per frame. S1, S2, S4 and S11 have none; S12 has only the logo's own
antenna ribbon.

**Skies.** `fieldSvg()` via `film-gen/gen-assets.ts`. Each place has its own seed:

| Place | Seed | Origin of the number |
|---|---|---|
| The deep field between machines (wides) | 97 | from `r-97ae` |
| Maya's machine (close-ups) | 4968 | maya's pid in `08` |
| Theo's machine (close-ups) | 6333 | theo's pid after the restart in `15` |
| End card | 1444 | the envelope size |

Use nebula 1.0 in wides and 0.6 in close-ups, vignette 0.55. Place each seed's bright core away from the caption bands
(upper right in 16:9, upper third in 4:5), and check every caption frame in the brand lint: text never sits on the nebula's
rose. Parallax only during camera moves: far dust 0.2×, stars 0.5×, plates 1×.

**Type roles** (zoom: 16:9 at 2×, 4:5 at 3×):

| Role | Face | 16:9 | 4:5 | Used for |
|---|---|---|---|---|
| Statement | Special Elite 400 | 96 px | 96 px | C1, C2, C3, the signature line (S7) |
| Invitation | Special Elite 400 | 72 px | 72 px | beta card lines, "Apply at" |
| Step caption | IBM Plex Sans 500, `text` | 64 px | 54 px | one plain sentence per step |
| Terminal | IBM Plex Mono 400 | 42 px base (36–40 where stated), key token pushed to 64 px | 42 px base (36–40 where stated) | captured output |
| URL | IBM Plex Mono 500 | 64 px (end card), 28 px (tag) | 72 px (end card), 36 px (tag) | `dorylinae.net` only |
| Name label | Special Elite 400 on label stock `#F6F3FB`, ink `#17122B`, radius 4/6, inset hairline | 28 px | 42 px | `maya`, `theo`, `relay` |
| Small print | IBM Plex Sans 400, `text-2` | 36 px | 36 px | end-card facts |

Every line is set in sentence case and left-aligned, except the end card, which centres the stacked lockup and its CTA.

**Entrances, exits, holds.**
- Captions land whole: opacity 0→1 and an 8 px rise over **240 ms**, brand ease `cubic-bezier(0.2,0.7,0.2,1)`.
- Agent commands land in 1 frame and settle over **120 ms** (a 7-frame fade, 6 px rise). Output lines stream one every
  2–3 frames.
- Exits are cuts on the beat.
- Name labels type at **85 ms per letter**.
- Holds are counted from the moment a line has fully landed. They are listed in §4 and checked against craft.md §5.4.

**Wide layout, 16:9** (camera scale 1, centre 960,540):
- Maya's terminal plate: x −240→600, y 330→750, radius 16. It bleeds off the left edge.
- Theo's plate: x 1320→2160, y 330→750. It bleeds off the right edge.
- Presence mark: 12 px, maya's at (628, 470), theo's at (1292, 470).
- Name labels sit on the field side of each mark: `maya` starts at x 648; `theo` ends at x 1272.
- Relay point (from S6 on): a 10 px `text-2` dot at (960, 540), with its `relay` label centred below at y 580–624.
- Trail: horizontal at y = 540. Segment A runs x 600→960, segment B runs x 960→1320. Dots are 6 px every 24 px.
- Captions: x = 192. One line sits on baseline 960; two lines on baselines 868 and 972.

**Wide layout, 4:5** (camera scale 1):
- Maya's plate: x 560→1400, y −200→300. It bleeds off the top and right edges.
- Theo's plate: x 560→1400, y 1050→1550. It bleeds off the bottom and right edges.
- Trail: vertical at x = 860. Segment A runs y 300→675, segment B runs y 675→1050. Dots are 9 px every 36 px.
- Relay point: a 15 px dot at (860, 675), with its `relay` label to the right, at x 892.
- Presence marks: 18 px, maya's at (884, 340), theo's at (884, 1010). Each label sits to the right of its mark.
- Captions: a left column, x 64→780, in the band y 420→1000. Before S6 there is no trail, so captions may use the full
  width, 952 px.

**Close-up layout** (terminal or document on one machine's sky):

| Format | Plate (fixed viewport, bleeds left and right) | Terminal text starts at | Captions |
|---|---|---|---|
| 16:9 | x −40→1960, y 140→800 | x = 192 | baseline 960 (one line), 884/972 (two) |
| 4:5 | x −40→1120, y 160→900 | x = 64 | baselines 1010/1090 |

The "camera" inside a close-up zooms and pans the content within the fixed plate, so long lines crop at the plate edge,
which is also the frame edge.

**Persistent tag** (21.6 → 45.6 s): **CLOSED BETA** in Plex Mono 500 caps, tracked 0.08em, `text-3`, then a 1.5 px
hairline slash at 21°, then **dorylinae.net** in Plex Mono 400, `text-2`.
- 16:9: 28 px, right-aligned at x 1824, baseline 92.
- 4:5: 36 px, left-aligned at x 64, baseline 1200.

It is part of the scene revealed by the first 21° cut and leaves with the second. It is an overlay: camera moves never scale
it.

**Presence pulse.** A ring expands from the online dot: radius ×1→×2.6, 2 px stroke in `online`, opacity 0.5→0, over
2 beats with the brand ease. Maya's dot pulses on every downbeat. Theo's pulses half a beat later (+0.3 s) whenever he is
online. Together they make a lub-dub "two machines" heartbeat. The offline ring is still. Nothing else moves on its own.

**Trail mechanics** (signature move B):
- **Extension.** The head position follows the brand ease over the stated duration. Each dot fades in over 120 ms as the
  head passes it.
- **March.** While in flight, the dot pattern shifts one pitch per beat, linearly.
- **Pause at the relay.** The march freezes. Dots go from pheromone to `line-strong` at 0.7 opacity over 240 ms. A 1 px
  `line-strong` ring (radius 14 px in 16:9, 21 px in 4:5) appears around the relay dot to mark that it is holding.
- **Resume.** Dots return to pheromone over 120 ms, the march restarts, and the holding ring is removed in 120 ms.
- **Arrival.** The trail decays from its tail over one bar: each dot fades over 240 ms, staggered linearly along the trail.
- **Count.** Exactly three trail events in the film: out to the relay, on to theo, and back to maya. No other dots, ever.

**21° cut** (S6 and S11 only). A single edge 21° off vertical crosses the frame from left to right in 600 ms with the brand
ease and reveals the next scene. No drawn line, no glow. The edge travels x −415→2335 in 16:9 and x −518→1598 in 4:5.

**Motion blur.** When the camera moves faster than 1 % of frame width per frame, use a 180° shutter (1/120 s) with 12
sub-samples. Never blur text during a hold. In wides, terminal texture inside the plates gets a 6 px depth blur.

**Score palette** (`tools/audio-poc/score.py` layers; key D major, felt piano):

| Name | Layer and notes |
|---|---|
| maya chord | piano, Dadd9 rolled (D3 A3 E4 F♯4) |
| theo chord | piano, Aadd9 (A2 E3 B3 C♯4) |
| two hearts | sub thump with a 40 ms pitch drop, plus a −30 dB glass tick, locked to the visual pulses: maya on the downbeat, theo +0.3 s |
| trail | `pulse` 8ths on D through the moving low-pass, gated on only while a trail is in flight |
| wait | `pad` drone on A (unresolved) |
| command | one `tick` per agent command landing, −24 dB. Never per character. |

Mix to −14 LUFS integrated, −1 dBTP. SFX sit 6–12 dB under music. Chapter cuts J-cut the audio 8 frames early.
The score never carries meaning.

---

### S1. Two agents. 0.0 → 4.8 s (1:1 → 3:1), 4.8 s

**On screen, 16:9.**
- Opening framing: camera scale 1.4 centred on world (420, 540), so maya's terminal plate fills the left two thirds of the
  frame (screen y ≈ 246→834).
- The plate is an empty terminal: `maya$` in `brand`, no cursor.
- Maya's presence dot (solid teal) and her `maya` label sit on the field to the plate's right. The label is present at
  frame 0 (no typing).
- The deep field, seed 97, surrounds everything.
- C1 sits in the caption band.
- At 2.4 s the camera pulls back to scale 1.0 (the wide) and reveals the empty field and, on the right, theo's plate
  (`theo$`), his teal dot and his `theo` label. The `theo` label types at 85 ms per letter from 2.7 s.
- C2 lands under C1 on the second line.

**4:5 changes.**
- The opening frames maya's plate at the top: scale 1.4 around (860, 300).
- C1 and C2 use the full width: Special Elite 96, block top at y 560. C1 breaks "You have a / coding agent.", C2 breaks
  "So does your / teammate."
- The pull-back reveals theo's plate at the bottom.

**Copy.**
- C1 (0.0–0.24 land): "You have a coding agent."
- C2 (2.4–2.64 land): "So does your teammate."

**Terminal.** Prompts `maya$` and `theo$` only. This is the capture convention, `who$` (`capture/INDEX.md`).

**Motion.**
- C1 enters at frame 0 (240 ms).
- Pull-back: 2.4→3.0 s, 1 beat, brand ease, parallax on, motion blur during the move.
- C2 settles while the camera moves (one moving thing, one settling thing).
- Slow drift from 3.0 s: scale 1.0→1.03 by 4.8 s.
- Both presence dots pulse: maya at 0.0 and 2.4; theo at 2.7.

**Sound.**
- 0.0: maya chord, plus the first "lub".
- 2.4: theo chord, a fifth up, J-cut 8 frames early; the "dub" joins at 2.7.
- Nothing else plays: no music bed yet.

---

### S2. What it is. 4.8 → 9.6 s (3:1 → 5:1), 4.8 s

**On screen, 16:9.** The same wide, after a hard cut of the caption only on the downbeat: C1 and C2 cut out and C3 lands.
Both plates show their empty prompts with a 6 px depth blur. Both dots pulse lub-dub. The field between the machines is empty:
the relay is not introduced yet. The camera keeps drifting (scale 1.03→1.06).

**4:5 changes.** C3 uses the full width in the middle band at Special Elite 96, broken "Dorylinae carries / work between
your / agents."

**Copy.**
- C3 (4.8–5.04 land): "Dorylinae carries work between your agents." 16:9 breaks it into two lines: "Dorylinae carries
  work" / "between your agents."

**Terminal.** None.

**Motion.** The C3 entrance and the presence pulses (4.8 / 5.1, 7.2 / 7.5). Nothing else moves. This is also the poster
frame: use 9.0 s as the thumbnail, because the name and the claim are both visible (evidence §Implications 4).

**Sound.**
- 4.8: the `pad` enters on D.
- The piano moves maya chord → theo chord → Bm(add11) at 4.8, 7.2 and 8.4.
- The two hearts continue.
- One soft `tick` at 9.6 as the next shot's command lands, J-cut 8 frames.

---

### S3. Presence. 9.6 → 13.2 s (5:1 → 6:3), 3.6 s

**On screen, 16:9.**
- Hard cut to a close-up of maya's machine (sky seed 4968).
- At 9.6 the command lands whole and the output streams.
- 9.6→10.2: the content zooms and pans so the row `theo  offline` reaches 64 px at y ≈ 470. The rest of that row bleeds off
  the right edge, and the rows above dim to 40 %.
- `offline` is the pheromone token.
- The step caption lands at 10.2.

**4:5 changes.**
- Base size is 42 px. Rows wider than 37 characters bleed off the right edge.
- The push takes `theo  offline` to 54 px.
- The caption breaks "Your agent sees / who is online."

**Copy.** "Your agent sees who is online." (lands 10.2–10.44)

**Terminal** (`capture/15-offline-delivery.txt`, `maya$ agentnet status --team atlas`):
```
maya$ agentnet status --team atlas
team atlas (t-56f7…5110), owner maya
NAME  DAEMON   AGENT   HUMAN    LAST SEEN
maya  online   active  unknown  now  (you)
theo  offline  -       -        2026-10-08T13:41:37Z
```
Edits:
- Omitted the seven lines between the command and `team atlas`: daemon, pid, uptime, outbox, presence, relay, and the
  version line, which must never show.
- Nothing else is changed. The team id is already shortened by the CLI itself.
- `(you)` on maya's row is visible in the first framing and quietly tells the viewer that maya is "you".

**Motion.**
- Command: 1 frame plus a 120 ms settle.
- Output lines: one every 3 frames.
- Content zoom and pan: 1 beat, brand ease.
- Caption: 240 ms.

**Sound.**
- 9.6: command `tick`.
- The theo "dub" **stops** from this bar on (theo is offline). The maya "lub" continues.
- `pulse` enters very low, as a working machine.
- Piano: a single F♯4 at 10.2 with the caption.

---

### S4. The brief. 13.2 → 16.8 s (6:3 → 8:1), 3.6 s

**On screen, 16:9.**
- Cut to a document plate on maya's sky (seed 4968): the brief, built from **three sub-plates**, one per line, each on
  `surface`.
- Each sub-plate: x 144→1776, height 96, separated by the 12 px channel, block top at y 260.
- Above the block: the file name `flaky-brief.md`, Plex Mono 28 px, `text-3`.
- Brief text: Plex Mono 32 px. The keys `What:`, `Why:` and `Done when:` are in `text`; the rest in `text-2`.
- This shot is texture plus structure. The caption is what the viewer reads.

**4:5 changes.**
- Sub-plates run full-bleed (x −20→1100) at 36 px. Each wraps onto 2–3 lines at word boundaries, like a document viewer.
  No characters change.
- The block is centred at y 520.
- The caption breaks "Your agent writes / the brief."

**Copy.** "Your agent writes the brief." (lands 13.8–14.04)

**File content** (`capture/run/work/flaky-brief.md`, byte-identical to the `brief` field of
`capture/20-grant.txt`, `maya$ agentnet request show r-97ae… --json`):
```
What: Find out why the payments integration test fails about one run in ten.
Why: It blocks merges twice a day and nobody owns it yet.
Done when: You name the cause, or rule out the three usual suspects.
```

**Motion: signature move A, lock-in.**
- The three sub-plates lock in with the boot's own order, stagger and offsets from `src/motion.ts`, at film zoom.
- Order:

  | Plate | Starts at | Moves from | Boot equivalent |
  |---|---|---|---|
  | `Why` (centre) | 13.35 | y −36 px | centre plate, −18 |
  | `Done when` (lower) | 13.395 | y +80 px | collar, +40 |
  | `What` (top) | 13.44 | y −60 px | antenna, −30 |

- Each plate fades 0→1 and moves over 500 ms with the brand ease. All three are settled by 13.94.
- Done this way, the brief "boots" like the mark, and the end card later pays it off.

**Sound.**
- One felt-piano note per plate lock (B3, D4, F♯4), each 45 ms apart, very soft.
- The caption lands at 13.8 with the "lub".

---

### S5. Sent. 16.8 → 21.6 s (8:1 → 10:1), 4.8 s

**On screen, 16:9.**
- Cut to maya's terminal close-up (seed 4968).
- At 16.8 the four-line command lands whole and the two output lines stream.
- The first framing holds for 1 beat. Here the second output line bleeds off the right edge after "…it will be deliver".
- 17.4→18.6: the content pans right and zooms from 42 px to 64 px until **`it will be delivered when theo is back.`**
  spans x ≈ 192→1690 at y ≈ 470.
- The head of that line (`theo is offline, last seen 2026-10-08T13:41:37Z;`) bleeds off the left edge.
- That phrase is the pheromone token, and it is the shot's only reading text.

**4:5 changes.** Same choreography. The final framing sets the phrase at 40 px so its 39 characters (936 px) fit x 72→1008.

**Copy.** No caption. The product speaks: "it will be delivered when theo is back." (landed ≈ 17.93)

**Terminal** (`capture/15-offline-delivery.txt`):
```
maya$ agentnet request theo task \
        --title 'Track down the flaky payments test' \
        --brief-from-file flaky-brief.md \
        --idempotency-key demo-flaky-1
Queued normal task request r-97ae… to theo (team atlas)
  theo is offline, last seen 2026-10-08T13:41:37Z; it will be delivered when theo is back.
```
Edits:
- The one-line command is broken at its flags with ` \`.
- `r-97ae9a37feaf51fcf0c3c8246ae57be1` is shortened to `r-97ae…`.
- The left and right crops come from the frame edge.

**Motion.**
- Command: 1 frame plus a 120 ms settle.
- Output: one line every 3 frames.
- Pan and zoom: 2 beats, brand ease, visibly done at about 17.93 (44 %).
- Hold from 18.6 to 21.6.

**Sound.**
- 16.8: command `tick`.
- 17.4: the piano plays the theo chord, suspended and *unresolved* (Asus2), as the promise.
- `pulse` swells slightly at 21.0. J-cut into S6 8 frames before 21.6.

---

### S6. Into the field. 21.6 → 26.4 s (10:1 → 12:1), 4.8 s

**On screen, 16:9.**
- **21° cut** (21.6→22.2) reveals the wide (seed 97).
- Maya's plate shows S5's output as blurred texture.
- Theo's mark is a **hollow ring**: still, no pulse.
- The relay point appears at 21.6 (240 ms) and its label types `relay` at 85 ms per letter (21.6→22.03).
- The **persistent tag** is part of the revealed scene from 21.6.
- **Trail, segment A:** extends from maya's plate (600, 540) to the relay (960, 540), 21.6→22.8 (2 beats, brand ease),
  marching.
- At **22.8** (10:3) the trail **stops**:
  - the march freezes;
  - the dots dim to `line-strong`;
  - the holding ring appears around the relay dot.
- At the same moment the relay's log line appears beside the relay as small texture: Plex Mono 20 px, `text-3`, baseline
  y 670, tail ending at x 1220 (text below). It is the same element S7 enlarges 2×, which puts it at 40 px, baseline 800,
  tail at x 1480.
- Theo's segment is not drawn. The caption lands at 22.2.

**4:5 changes.**
- The trail runs down the right third (x 860) from maya's plate to the relay (y 300→675).
- The caption is in the left column at Plex Sans 54: "It waits at the / relay, up to / 7 days."
- The log texture sits left of the relay: 21 px, baseline y 758, tail at x 833. S7's 1.5× push maps it to 32 px,
  baseline 800, tail at x 820.

**Copy.** "It waits at the relay, up to 7 days." (lands 22.2–22.44)

**Terminal** (`capture/25-relay-log.txt`, as texture here, read in S7):
```
time=2026-10-08T13:41:40.228Z level=INFO msg=queued event=queue from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444
```
Edits:
- `m-b2d3dc4566beac6d9631949d221555c0` is shortened to `m-b2d3…`.
- The head is cropped at the frame edge.
- This is the same `mail_id` maya's own `--json` output returned for this request (`15`), so it is provably the same
  envelope.

**Motion.**
- The 21° edge: 600 ms.
- The trail: one moving thing; the caption is the settling thing.
- After 22.8 nothing moves except maya's pulse (24.0, 26.4). That stillness is deliberate.

**Sound.**
- 21.6: the `pulse` gate opens and marches with the trail; the maya chord rolls.
- **22.8: hard stop.** Pulse, piano and sub hit all stop on the beat. What remains is the `pad` drone on A and maya's lone
  "lub" on each downbeat.

---

### S7. The wait (signature). 26.4 → 33.6 s (12:1 → 15:1), 7.2 s

**On screen, 16:9.**
- 26.4→27.6: macro push-in, scale 1.0→2.0 around the relay, 2 beats, brand ease. The relay stays at screen centre.
- The frame then holds (relay label → screen 864–1056, y 620–708). On screen:

  | Element | Screen position |
  |---|---|
  | Maya's plate edge | x 240 |
  | Her pulsing dot and `maya` label | x 296–520, y 356–444 |
  | The relay dot and its holding ring | centre |
  | Theo's hollow ring and `theo` label | x 1400–1624, y 356–444 |
  | Theo's plate edge | x 1680 |

- The paused trail runs from x 240 to the relay.
- The relay log line is now 40 px on screen, baseline y 800, tail at x 1480, cropped at the left frame edge. It shows at
  least `from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444`.
- **`bytes=1444`** turns pheromone at 26.4. The paused trail has no pheromone, so this is the frame's only accent.
- The signature caption lands at 26.7, in the **top** band of this shot: baselines 170 and 274, x 192.
- **31.2 (14:1), on the downbeat:**
  - theo's hollow ring fills to a solid teal dot (120 ms) and starts pulsing at 31.5;
  - the holding ring disappears;
  - the log line swaps in place (120 ms) to the delivery line, `text-3`, with no accent;
  - the caption cuts to "Theo is back." (Plex Sans 64 in the same top band).
- **31.8:** the trail returns to pheromone. Segment B extends from the relay to theo's plate edge (screen 960→1680),
  31.8→33.0, marching. It begins its tail decay at 33.0.

**4:5 changes.**
- The push is 1.0→1.5 around the relay, which stays at (860, 675).
  - Maya's label and dot land near y 172, theo's near y 1177 (inside safe).
- Signature caption: Special Elite 96 in the left column (x 64→780), top at y 300. Breaks: "Sealed." / "The relay sees" /
  "1444 bytes."
- Log line: 32 px, baseline y 800. `bytes=1444` ends at x 820, and at least `to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444`
  is visible.
- "Theo is back.": Plex Sans 54 at the same position as the signature caption.
- Segment B runs down x 860 to theo's plate.

**Copy.**
- Signature (lands 26.7–26.94): "Sealed. The relay sees 1444 bytes." In 16:9 it sets as two lines: "Sealed." / "The relay sees
  1444 bytes."
- Then (lands 31.2–31.44): "Theo is back."

**Terminal** (`capture/25-relay-log.txt`):
```
time=2026-10-08T13:41:40.228Z level=INFO msg=queued event=queue from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444
time=2026-10-08T13:41:45.886Z level=INFO msg="delivered from queue" event=queue_flush peer=ZhbF0Hsy count=1
```
Edits: the id is shortened; the head is cropped at the frame edge. The second line *replaces* the first in place: the relay
logs the event; it does not rewrite history.

**Motion.**

| Time | Event |
|---|---|
| 26.4–27.6 | Push-in |
| 26.4 | Accent on the token |
| 26.7 | Signature caption |
| 27.6–31.2 | Absolute stillness. Only maya's pulse moves, at 28.8. This is the held breath. |
| 31.2 | Ring fill, log swap, caption swap: one event, on the downbeat |
| 31.8–33.0 | Trail B |

**Sound.**
- 26.4: the `pad` A drone continues, and a single high piano E5 sounds as `1444` lights.
- 26.4–31.2: only the drone and maya's "lub".
- **31.2:** the theo chord resolves to the maya chord (A → D, home). At 31.5 the "dub" returns: two hearts again.
- 31.8: the `pulse` gate opens with the trail.
- J-cut into S8 8 frames early.

---

### S8. Inbox, accepted. 33.6 → 38.4 s (15:1 → 17:1), 4.8 s

**On screen, 16:9.**
- Cut to theo's machine (sky seed 6333): terminal close-up at **36 px** base, so each whole row fits.
- 33.6: the inbox command and output land.
- The `r-97ae…` row's title **`Track down the flaky payments test`** is the pheromone token. The other row is `text-3` at
  60 %.
- **34.8** (15:3): the accept command lands and its output streams. The pheromone moves to **`Accepted`**.
- The caption lands at 35.1.
- No zoom. The caption carries the meaning, and the terminal is proof.

**4:5 changes.**
- 36 px base. Rows are cropped at the right edge after the AGE column (`…2000  0m`), so the pheromone token is the
  id **`r-97ae…`**: the same id the viewer saw queued in S5.
- The caption breaks "Theo's agent accepts / it from the inbox."

**Copy.** "Theo's agent accepts it from the inbox." (lands 35.1–35.34)

**Terminal** (inbox from `capture/15-offline-delivery.txt`; accept from `capture/20-grant.txt`):
```
theo$ agentnet inbox
ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE
r-d0d8…  maya  task  normal   2000  1m   Add queue settings to the staging runbook
r-97ae…  maya  task  normal   2000  0m   Track down the flaky payments test
theo$ agentnet accept r-97ae…
Accepted r-97ae… from maya
```
Edits:
- Two ids are shortened.
- The column whitespace is re-aligned after shortening, as `capture/INDEX.md` instructs for typesetting. Content is
  unchanged.
- The real 1 min 37 s between the two commands (other captures ran in between) is compressed by omitting those lines.

**Motion.**
- The two command blocks land 1 frame each, plus a 120 ms settle; output streams at 3 frames.
- The accent moves by a 120 ms state change.
- Caption: 240 ms.

**Sound.**
- 33.6: command `tick`, plus a three-note piano figure F♯–A–B.
- 34.8: command `tick`.
- The two hearts continue.

---

### S9. The way back. 38.4 → 40.8 s (17:1 → 18:1), 2.4 s

**On screen, 16:9.**
- Cut to the wide (seed 97). Both dots pulse lub-dub, the tag is in place, and the relay has no holding ring.
- **Trail, return:** one continuous line from theo's plate edge (1320, 540) through the relay to maya's plate edge
  (600, 540), 38.4→39.6 (2 beats, brand ease), marching. It passes the relay **without stopping**: both are online. That
  contrast with S6 is the point.
- Tail decay from 39.6.
- The caption lands at 38.4.

**4:5 changes.** The trail runs up x 860 from theo (y 1050) to maya (y 300). The caption is in the left column at Plex Sans
54: "The answer / comes back."

**Copy.** "The answer comes back." (lands 38.4–38.64)

**Terminal.** None. This is a diagram of the result mail. The capture's relay shows routed mail theo→maya at 13:43:54,
but the film does not label it.

**Motion.** The trail moves; the caption settles.

**Sound.** The `pulse` gate opens. The maya chord rolls *upward*: the return is brighter than the outbound.

---

### S10. The answer. 40.8 → 45.6 s (18:1 → 20:1), 4.8 s

**On screen, 16:9.**
- Cut to maya's terminal close-up (seed 4968) at **40 px** base.
- 40.8: the command lands and the output streams.
- 41.4→42.0: zoom and pan until **`Cause found: two tests share one ledger row`** is 64 px (43 characters, ≈ 1651 px,
  x ≈ 120→1771) at y ≈ 470.
- `result: pass: ` bleeds off the left edge. Lines below dim to `text-3` at 40 %.
- The result phrase is the pheromone token and the only reading text.

**4:5 changes.** Same choreography. The final framing holds the phrase at **36 px** (929 px, x 72→1001), the terminal floor
for 4:5 (craft.md §5.2).

**Copy.** No caption. The product's answer is the payoff: "Cause found: two tests share one ledger row" (landed ≈ 41.9).

**Terminal** (`capture/21-quarantine-release.txt`, maya's final `request show`):
```
maya$ agentnet request show r-97ae…
r-97ae…  task to theo (team atlas): Track down the flaky payments test
  state completed 2026-10-08T13:44:05Z, delivery delivered
  result: pass: Cause found: two tests share one ledger row
  output (209 bytes):
    Cause: run 07 timed out waiting for the ledger lock.
```
Edits:
- The id is shortened.
- The last two output lines are omitted (`TestCapture and TestRefund…` / `one waits past…`).
- Crops come from the frame edge.

**Motion.** Command plus 120 ms; stream; zoom and pan 1 beat; hold 42.0→45.6.

**Sound.**
- 40.8: command `tick`.
- 41.4: a single glass ping as the phrase reaches size, plus the cadence: Gmaj9 rolled → Dadd9.
- This is the quiet climax.
- J-cut into S11 8 frames early.

---

### S11. Beta card. 45.6 → 52.8 s (20:1 → 23:1), 7.2 s

**On screen, 16:9.**
- **21° cut** (45.6→46.2) to the wide (seed 97), reframed: camera scale 0.75, centre (960, 700).
- In that framing the plates sit small in the upper half (screen y ≈ 262→577), both dots pulse lub-dub, the relay dot is
  visible with no ring, and there is no trail.
- The persistent tag is gone, because this card *is* the call.
- B1 lands as the edge passes, at x 192, Special Elite 72, two lines on baselines 724/816.
- B2 lands at 48.9, one line on baseline 932.
- **52.2:** cut to empty ground, the end-card sky (seed 1444, no lockup yet), for one beat.

**4:5 changes.** The plates sit at the top and bottom, small (scale 0.75). B1 and B2 use the full width in the middle band
(y 470→870): "Closed beta for small" / "teams, by invitation." then "The first wave opens" / "with 10 teams."

**Copy.**
- B1 (lands 45.6–45.84; the left of frame is revealed first): "Closed beta for small teams, by invitation."
  16:9 breaks it as "Closed beta for small teams," / "by invitation."
- B2 (lands 48.9–49.14): "The first wave opens with 10 teams."

**Terminal.** None.

**Motion.** The 21° edge, two caption entrances, and the presence pulses. Nothing else.

**Sound.**
- 45.6: sub and `pulse` drop out.
- Piano plays Bm7 (45.6), Gmaj7 (48.0) and Asus4 (50.4), left unresolved.
- The two hearts continue under it, very low.
- **52.2–52.8: one beat of complete silence** (craft.md §1.9).

---

### S12. Boot and call. 52.8 → 62.4 s (23:1 → 27:1), 9.6 s

**On screen, 16:9.**
- Deep field, seed 1444.
- **52.8→56.4:** the unchanged `bootSvg()` boot sequence of the **stacked lockup**, signal style, mark 600 px wide (so the
  scanline pitch is about 4 px), centred, top at y 90.
- The boot is untouched: plates lock in from the axis, the sweep runs at +1.2 s, the signal at +1.35 s, and the letters of
  DORYLINAE from +1.9 s every 85 ms.
- 56.4→57.6: the finished lockup holds. The scanline drift starts: 1 pitch per 1.6 s, linear, end card only.
- **57.6:** the CTA lands centred below the clear space (78 px), baseline y 870.
- **58.2:** the small print lands, baseline y 950, centred.
- **61.8:** cut to ground (sky only) for the 0.6 s tail.

**4:5 changes.**
- The mark is 640 px wide (scanline pitch ≈ 4.3 px), top at y 150.
- CTA baseline 970, at Special Elite 72 and Plex Mono 72.
- Small print baseline 1050, broken "Free during the beta." / "macOS, Linux and Windows." on two lines (baselines
  1050/1100).

**Copy.**
- CTA: "Apply at dorylinae.net". "Apply at" is Special Elite 72; `dorylinae.net` is Plex Mono 500, 64 px in 16:9 and 72 px
  in 4:5, in `text`.
- Small print: "Free during the beta. macOS, Linux and Windows." (Plex Sans 400, 36 px, `text-2`)

**Terminal.** None.

**Motion.** Only the boot, then the scanline drift, then two caption entrances. No copy appears during the boot (the Resmed
rule).

**Sound.** The boot's 6-beat sonic logo, timed to the boot:

| Boot time | Sound |
|---|---|
| plate locks | 23 felt-piano plinks in D-major pentatonic, rising, one per plate, 45 ms apart |
| +1.2 s | a soft filtered-air sweep under the glint, 0.75 s |
| +1.35 s | a `pad` bloom on D |
| from +1.9 s | nine faint `tick`s, one per letter of DORYLINAE |

Dadd9 rings out. At 57.6 the CTA lands over the decaying chord with one low D sub note and nothing else. Fade to digital
silence by 62.4.

---

## 4. Copy deck

Hold rules (craft.md §5.4):
- floor = max(1.5, 0.5 + 0.35 × words);
- key claim = max(2.0, 0.5 + 0.6 × words);
- both rounded up to the half beat (0.3 s);
- counted from "fully landed".

Terminal tokens are budgeted at 15 characters a second or less.

| # | On screen (s) | Sentence | Words | Rule | Required | Actual hold | Source |
|---|---|---|---|---|---|---|---|
| 1 | 0.0–4.8 | You have a coding agent. | 5 | floor | 2.4 | 4.56 | Audience: PLAN:11 via product-facts §2 ("each running their own agent harness"); BRIEF "your coding agent" |
| 2 | 2.4–4.8 | So does your teammate. | 4 | floor | 2.1 | 2.16 | Same; BRIEF "each of you keeps your own agent" |
| 3 | 4.8–9.6 | Dorylinae carries work between your agents. | 6 | key | 4.2 | 4.56 | product-facts §1a ("your assistant sends the request straight to your teammate's assistant"), §2 PLAN:7 ("hand each other work") |
| 4 | 10.2–13.2 | Your agent sees who is online. | 6 | floor | 2.7 | 2.76 | product-facts §6 true sentence #1; capture `15` status table |
| 5 | 13.8–16.8 | Your agent writes the brief. | 5 | floor | 2.4 | 2.76 | product-facts §3.5 and §5 #25 ("Your agent writes the brief."); `run/work/flaky-brief.md` |
| 6 | 16.8–21.6 | it will be delivered when theo is back. (terminal) | 8 | floor | 3.3 | 3.67 | `capture/15-offline-delivery.txt` |
| 7 | 22.2–26.4 | It waits at the relay, up to 7 days. | 9 | floor | 3.9 | 3.96 | product-facts §3.9 (relay `--queue-ttl 168h`, confirmed in fact-check A 3.9); `capture/02-request-help.txt` ("holds it for up to 7 days while the peer is offline"); `25` (`msg=queued`) |
| 8 | 26.7–31.2 | Sealed. The relay sees 1444 bytes. | 6 | key | 4.2 | 4.26 | product-facts §6 #6 and §3.10 (sealed with HPKE; relay sees from, to, size); `25` (`bytes=1444`, `from=`, `to=` visible) |
| 9 | 31.2–33.6 | Theo is back. | 3 | floor | 1.8 | 2.16 | `25` (`peer connected`, `delivered from queue … count=1`); `15` (theo's daemon restarted) |
| 10 | 34.8–38.4 | Accepted r-97ae… from maya (terminal) | 26 chars | ≤15 cps | 1.8 | 3.55 | `capture/20-grant.txt` |
| 11 | 35.1–38.4 | Theo's agent accepts it from the inbox. | 7 | floor | 3.0 | 3.06 | product-facts §3.6 (inbox, accept); `15` inbox; `20` accept |
| 12 | 38.4–40.8 | The answer comes back. | 4 | floor | 2.1 | 2.16 | product-facts §3.11 (structured result returns to the requester); `21` |
| 13 | 40.8–45.6 | Cause found: two tests share one ledger row (terminal) | 8 | floor | 3.3 | 3.67 | `capture/21-quarantine-release.txt` |
| 14 | 45.6–52.2 | Closed beta for small teams, by invitation. | 7 | floor | 3.0 | 3.06 before B2 lands (6.36 on screen) | BRIEF owner decisions; beta-facts §2 (Access) |
| 15 | 48.9–52.2 | The first wave opens with 10 teams. | 7 | floor | 3.0 | 3.06 | BRIEF (exact phrasing given); beta-facts §2 (Waves), fact-check B18 |
| 16 | 57.6–61.8 | Apply at dorylinae.net | 3 | end card ≥ 3.0 (craft.md §1.12) | 3.0 | 3.96 | BRIEF (CTA); beta-facts §5 |
| 17 | 58.2–61.8 | Free during the beta. macOS, Linux and Windows. | 8 | floor | 3.3 | 3.36 | BRIEF; beta-facts §2 (Free tier, Platforms); product-facts §3.21 |
| T | 21.6–45.6 | CLOSED BETA ⟋ dorylinae.net (persistent tag) | 3 | persistent | — | 24.0 | BRIEF; evidence Implications §5 |

Labels, all real names from the captures:

| Label | First shown | Typed |
|---|---|---|
| `maya` | 0.0 (present at frame 0) | no |
| `theo` | 2.7 | 85 ms per letter |
| `relay` | 21.6 | 85 ms per letter |

Terminal texture whose single pheromone token is read at a glance: `offline` (S3) and `Track down the flaky payments
test` (S8 in 16:9; in 4:5 the token is `r-97ae…`).

There are 17 reading elements in 62.4 s, and the most words on screen at once is 9. No ant word appears anywhere, so the
"one per screen" limit is met with room to spare.

---

## 5. The signature moment: the held trail (21.6 → 33.6 s)

The pheromone trail leaves maya's machine, marches across the deep field and **stops dead at the relay**. Its dots go grey-violet
and still, theo's ring stays hollow and silent, and the music drops to a single drone and one heartbeat. The camera pushes
into the relay. All it knows of the request is one log line that ends in a single lit token, **`bytes=1444`**, and the line
reads *"Sealed. The relay sees 1444 bytes."* Then, on a downbeat, theo's ring fills, the second heartbeat comes back, the log
line becomes `delivered from queue … count=1`, and the trail floods back to pheromone and walks on into his machine.

Why people remember it and share it:

- **It is the product's whole promise in one image.** It shows the request waiting, sealed, while the person is away, and
  being delivered when he returns.
- **It is literal and checkable.** The `m-b2d3…` id and the 1444 bytes are in the real relay log, and the same id is in
  maya's own output. A developer who pauses the frame finds real data, not decoration.
- **Only Dorylinae could make it.** Army ants navigate by pheromone trails, the brand defines the trail token and its "may
  march" rule, and the product really does store and forward (craft.md §4.3 B).
- **It cuts down well.** The 12 s from 21.6 to 33.6 is a self-contained loop for the 15 s cutdown (S2 claim → S6–S7 → end
  card) and a GIF-able post on its own.

---

## 6. Self-check against BRIEF.md hard rules

| Rule (BRIEF.md) | How this concept complies |
|---|---|
| One CTA, `dorylinae.net`, repeated identically; no GitHub, repo, handles or hashtags | `dorylinae.net` appears in the tag (21.6–45.6) and on the end card, always in Plex Mono. No other address, handle or path appears anywhere. |
| 16:9 master and a separately laid-out 4:5; machines side by side in 16:9, stacked in 4:5 | Every shot has a 4:5 re-layout from the same scene graph (§3.0 layouts, "4:5 changes" per shot). In 16:9 the trail runs horizontally; in 4:5 it runs vertically down the right third. |
| No voice-over; silent-first; the score never carries meaning | No voice. Every claim is a burned-in line (§4). The two-heartbeat motif doubles the visible pulses and adds no information. |
| No people; no drawn OS notification banners; no GUI the product does not have | No people or hands. Plates are bare terminal blocks with no window chrome. Presence is a diagram on the field (a dot and a specimen label, outside any plate). The relay is a point. No notification is drawn, even though the product has them, because the capture's desktop test failed (`14`). |
| Coding agents generic; never name a harness; crop harness names | Copy says "coding agent" and "your agents". No shown line contains `claude-code` or `codex`. The lines that carry them (`05`, `04`) are not used. |
| Beta facts allowed, phrased as a start | "Closed beta for small teams, by invitation." (no size number); "The first wave opens with 10 teams." (exact owner phrasing); "Free during the beta." (nothing about later pricing); "macOS, Linux and Windows." |
| Nothing planned but not built | No `setup`, `login`, `feedback`, `BETA-…` codes, install script, Homebrew, relay hostname or account page. Every command shown is in the dispatch table: `status`, `request`, `inbox`, `accept`, `request show`. The relay log comes from the local relay; its address is never shown. |
| No forbidden claims (audited, secure, zero-knowledge, tamper-proof, open source, works with every agent, live shared sessions, group debates, "everything is private", "every action is logged") | None is made. "Sealed" is the mechanism (HPKE), not "secure". The relay's own line shows it sees `from`, `to` and size, so the film never implies it knows nothing. "Signed" is not used. |
| "We" only in the beta invitation | "We" is not used anywhere. |
| Brand voice: field notes, present tense, numerals, no "!", no emoji, no hype, "you" for the reader, at most one ant word per screen, sentence case, wordmark DORYLINAE, no tagline | Every line is short, declarative and present tense, with numerals (7, 1444, 10). Zero ant words. DORYLINAE appears only in the boot lockup; "Dorylinae" appears in running text in C3. C3 is said once as a description and never paired with the logo, so it is not a tagline. |
| Special Elite only for display lines and labels, never paragraphs | Special Elite is used for single statement lines, the two invitation lines, "Apply at", and the name labels. Step captions are Plex Sans; terminal text is Plex Mono. |
| Logo: never redraw, stretch, tilt, outline or take apart; its boot is the only assembly | The mark appears once, through the unchanged `bootSvg()` at 3.6 s, on its own sky, with 13 % clear space and no copy during the boot. The camera never rolls. The 21° appears only as a cut edge and the tag's slash. |
| No AI slop: glitch, particles, neon, stock transitions, emoji, hype words, blue-to-purple gradients, swooshes, lens flares, HUD | The only motion vocabulary is the brand-ease lock-in, the trail, the presence pulse, two 21° cuts and the boot. There are no particles (only three real trail events), no glow on UI, no gradient but the generated sky, and no whooshes in the score. |
| Every terminal line copied from `capture/` with only the allowed edits | Each block lists its source file and its edits: omit lines, crop at the frame edge, shorten ids (`r-97ae…`, `r-d0d8…`, `m-b2d3…`), break a command with ` \`. No version string, path, port or harness name appears. Two presentation notes: inbox columns are re-aligned after id shortening (per `capture/INDEX.md`), and the brief *file* (a document, not terminal output) word-wraps in 4:5 without changing a character. |
| Cuts land on beats; 100 BPM; 1 beat = 0.6 s | Every cut is on a beat. Chapter cuts (21.6, 45.6), the trail stop (22.8, beat 3), the ring fill (31.2) and the boot (52.8) are on beats; the ring fill and the boot are on downbeats. Text landings fall on beats or half-beats. |
| Total 55–65 s | 62.4 s. |
| Do not modify anything outside `design/`; no commit or push | This file is the only output. |

---

## 7. Risks, and what would make it fail

1. **The pace may feel slow to scrollers.** Three trails, a 7.2 s held shot and few cuts are a deliberate rhythm. If the
   S6–S7 stillness reads as a stall rather than suspense, the film loses people at about 25 s, before the tag has done its
   work.
   - *Mitigation:* the hold is the music's held breath, and the presence pulse keeps the frame alive.
   - *Test:* an animatic watched muted on a phone at arm's length. If more than one of five reviewers says "nothing is
     happening", shorten caption 7 to "It waits at the relay." (5 words, 2.4 s) and S6 to 1.5 bars (−1.2 s).
     Keep S7 intact: the signature line needs its full 4.2 s.
2. **The hook is generic.** "You have a coding agent. So does your teammate." is clear but could read as ad grammar.
   - *Fallback hook, real material:* open on the brief's `Why:` line typed in Special Elite, "It blocks merges twice a day
     and nobody owns it yet." That is a pain every developer knows. Then cut to C3.
   - *Cost:* about 1 bar, and the name lands at about 7 s, past evidence's 5 s target.
3. **No human approval on screen.** The trust story rests on sealing alone. A sceptical developer's first question, "can
   theo's agent read my files?", is unanswered.
   - *Insert-ready variant:* after S8, add 2 bars on maya's daemon terminal from `capture/24-daemon-terminals.txt` /
     `20-grant.txt`. Crop the approval line to `Grant fs.read to peer … named "theo" on "~/work/payments-ci" … for 1 h …`,
     replacing the scratchpad path as BRIEF allows. Type `a-1a9f03 248765` at 85 ms per letter (the film's only human
     typing). End on `approved`. Caption: "No command takes the approval code. You type it." (fact-check B4 wording).
   - *Cost:* +4.8 s, taking the film to 67.2 s, over the 65 s limit. It only fits if the risk 1 trim (−1.2 s) is applied
     and S3 and S4 merge into one 2-bar shot (−2.4 s), giving 63.6 s.
4. **"Top of the inbox" is gone.** The urgency ordering, a real differentiator, is not shown. Only a re-capture fixes this
   honestly (§2.5). A composite of two requests would break the film's central truth.
5. **One machine pretending to be two.** The captures ran two daemons and a loopback relay on one machine (`capture/INDEX.md`).
   The film draws two machines. The output is identical to what two machines print, and the owner's two-machine run passed
   (product-facts §3.20, D29), so the depiction is fair. A strict reviewer may still want the hero journey re-captured across
   two real machines.
6. **The scripted capture.** The commands were run by a capture script, not live agents. The film says "your agent writes
   the brief / accepts it", which is the product's documented behaviour (PLAN:89, snippet). Do not caption anything as "real
   agents, unedited".
7. **The 1444-byte line could be read as "the relay knows nothing".** The visible `from=` / `to=` fields keep it honest, but
   only for viewers who read texture.
   - *If the owner wants it explicit:* replace caption 8 with "The relay sees who, when and how big." (8 words, needs
     5.4 s, +1.2 s).
   - *Also an owner option:* beta-facts §4's closing-card disclosure, "Not yet independently security-reviewed."
8. **Small terminal type in 4:5.** The payoff line in S10 is 36 px (about 13 pt on a phone), the floor, not comfortable.
   - *Fallback if the phone test fails:* a Plex Sans 54 step caption, "Cause found." (2 words, 1.8 s), placed in S10
     *after* the terminal line has had its 3.3 s. This extends S10 by half a bar (+1.2 s).
9. **The tag as clutter.** A persistent bug can feel un-Apple. It is kept small, `text-3`, top corner, and removed for the
   call. If the panel rejects it, replace it with a one-beat mid-roll cue at 26.4 and accept the weaker mid-video call.
10. **The 7-day line depends on configuration.** "Up to 7 days" is the relay's default queue (`--queue-ttl 168h`). If the
    hosted relay ships with a different TTL, caption 7 must change before render.
11. **Reading load.** 17 reading elements in 62 s is near the ceiling for silent viewing. If the QA timing review finds any
    hold under its §5.4 budget after animation tuning, cut caption 5 ("Your agent writes the brief.") first: the
    three-plate brief can stand alone as texture.
