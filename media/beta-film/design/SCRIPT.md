# SCRIPT: Dorylinae closed-beta film (final)

Director's final script and storyboard. The motion, sound and QA teams build from this file and nothing else.
Source rules are in `BRIEF.md`. Every number below was checked against `research/craft.md` (sections 0, 4, 5 and 7),
the captures in `capture/`, and the brand fonts. Special Elite, Plex Sans and Plex Mono widths were measured with
opentype.js from `engine/fonts/`. If the build measures a width that differs by more than 2 %, keep the left edge and
the baseline, and report it.

Working title: **When theo is back**. Runtime **63.0 s** (105 beats at 100 BPM). 12 shots in three chapters.

---

## DIRECTOR'S AMENDMENTS (binding; they override anything below)

Decided after the fact check (`SCRIPT-factcheck.md`) and voice check (`SCRIPT-voicecheck.md`).

1. **S1 C1** reads `You paste the brief into a chat.` (not "it"). 16:9 72 px, 4:5 60 px, same position.
2. **S7 C10** reads `Their agent does the work.` (the docs' own description: "They accept it, do the work and send back
   an answer", product-facts 1a). Do not claim the agent "finds the cause"; the token `Cause found: two tests share one
   ledger row` stays as the product's real output.
3. **S10 sender label** reads `From Dorylinae`.
4. **Specimen labels are capitalised** (voice: "Taxonomic, lower case after the first word"): `Relay`, `When`, `Who`,
   `How big`. Beta label `Closed beta` / `by invitation` stays.
5. **Collector labels** (`maya · 13:41 UTC` etc.) are set in the brand `label` style instead of Special Elite:
   IBM Plex Mono 500, uppercase, tracking 0.08 em, `text-3`, 28 px (16:9) / 36 px (4:5), same baseline:
   `MAYA · 13:41 UTC`, `THEO · 13:41 UTC`, `THEO · 13:43 UTC`.
6. **The demo brief stays verbatim** (`one run in ten`, `twice a day`, `three usual suspects`): it is in-world text
   from the real capture, not Dorylinae copy. No re-capture.
7. **21° cut geometry** (fact-check item 6): the edge is exactly 21° off vertical. Clip polygon on the incoming shot:
   (−10, −10), (xe(−10), −10), (xe(H+10), H+10), (−10, H+10) with xe(y) = m(t) + (H/2 − y) · tan 21°; m runs from
   −(H/2 + 10) · tan21° − 10 to W + (H/2 + 10) · tan21° + 10 over 0.6 s, brand ease.
8. **Motion blur**: not used (60 fps, brand ease). QA decides if any push strobes; if so we add sub-frame blur there only.

---

## 0. How to read this file

- **Clock.** 100 BPM, 4/4. 1 beat = 0.600 s = 36 frames at 60 fps. 1 bar = 2.400 s. Times are seconds from the first
  frame, to 3 decimals. `bar:beat` is 1-indexed: t = (bar − 1) × 2.4 + (beat − 1) × 0.6. `6:3.5` = 13.500 s.
- **Formats.** 16:9 is 1920 × 1080, title-safe x 96 to 1824 and y 54 to 1026. 4:5 is 1080 × 1350, safe x 64 to 1016,
  y 64 to 1222 (craft 5.3). Plates may bleed off the frame (Lago). Text that must be read stays inside safe.
- **Coordinates** are screen pixels of the format named. Text positions are the **left edge and the baseline** (centred
  text gives the centre x). Build captions as SVG `<text x y>` or measure the ascent in the build so baselines are exact.
- **World vs screen.** Some shots have a camera. "World" elements move with the camera. "Screen" elements (captions,
  the tag, collector labels, determination cards) do not. At camera scale 1 and the default centre, world = screen.
  The engine camera puts world point `c` at the frame centre: screen = (W/2, H/2) + s × (world − c).
- **Ease.** Unless stated, every move uses the brand ease `cubic-bezier(0.2, 0.7, 0.2, 1)`. Linear is used only for
  the trail march, the trail's tail fade and the 60 ms letter fade (craft 0).
- **Typing.** `FILM.ui.typed` with `step 0.085`, **`jitter 0`**, letters fade in over 60 ms linear, and a space advances
  0.6 × step. No caret. The engine's `label()` must pass `jitter: 0` too. Audio ticks are generated from each
  component's own letter times (`FILM.cue`), so picture and sound always agree.

---

## 1. Logline, structure, time shares and evidence

### 1.1 Logline

You paste the same brief into a chat, then into your teammate's coding agent. With Dorylinae your agent hands it to
theirs. We follow one real request, `r-97ae…` "Track down the flaky payments test", from maya's agent to theo's. Theo
is offline, so the request waits at the relay. The relay can see who, when and how big, but not what. Theo comes
back, the request lands in his inbox, his agent finds the cause, and the answer comes back. Then the people at
Dorylinae send their own brief to you. Its *Done when* line is typed by hand: "Your team applies."

### 1.2 Where it comes from

The film starts from **concept D** (*Done when*). D had the best combined score (23.6) and the best truth score (8). We
keep its call architecture: the invitation written in the product's own brief grammar (What / Why / Done when), the
one typed line "Your team applies.", one call worded identically everywhere ("Apply at dorylinae.net"), the poster
frame, the "From the people at Dorylinae" label as the only place "we" is used, and its two 21° cuts.

Fixing D's must-fix list needed the judges' own grafts:

| Graft | From | Why it is in |
|---|---|---|
| Hook: the same brief pasted twice by hand, from frame 0 | A | Newcomer and craft: show a recognisable pain in the first 1 to 2 s. D opened on an announcement. |
| One real request, followed end to end: `r-97ae…` | B | Newcomer ("one object to follow") and truth (it is the only capture that went through the offline queue). |
| The held trail at the relay, `it will be delivered when theo is back.` as the proof token, and the hollow ring that fills on a downbeat | B | Truth: D lacked the product's best differentiator. Craft: D's middle had no signature beat. |
| `who` / `when` / `how big` determination labels on the real queued log line, then "Not what." | A | Truth and craft: honest, checkable, screenshot-able. Replaces B's false-by-implication "The relay sees 1444 bytes." |
| Legibility: terminal base 42 px (16:9) / 36 px (4:5), the one token pushed to 64 px / ≥ 36 px | B | Truth must-fix on sizes for every concept. |
| A felt payoff at hero size: "Cause found: two tests share one ledger row" | B | Newcomer: a lead converts on an outcome. It also meets the brief's own *Done when*. |
| "For small teams." under the definition | C, A | Newcomer: say who it is for by about 10 s. |
| The colour arc: monochrome until the name, then amethyst, then teal and pheromone | A | Craft: a quiet premium arc that costs nothing. |
| Captions top-left, editorial (never the subtitle band) | A, D | Craft must-fix for B. |
| The motif left open and resolved later; the boot cue sounds its notes as a chord | D, C | Craft graft. |

Left out on purpose: C's debate, signatures and tamper beat; the human approval and quarantine-release steps; the
urgency ordering of the inbox. They are true and strong, but they would take the film past 3 capabilities and 4 ideas
(craft 0; evidence Implications §2). They belong in the 90 to 120 s walkthrough.

The film has **three product ideas**: (1) your agent hands work to your teammate's agent, with a brief, (2) if they
are offline it waits at the relay and arrives when they are back, (3) the relay sees who, when and how big, not what.
The answer coming back closes idea 1.

### 1.3 Structure

| Chapter | Time | Shots | Job |
|---|---|---|---|
| I. Before and after | 0.000 to 9.600 | S1, S2 | The pain, then the name, what it does and who it is for. |
| II. One request | 9.600 to 40.800 | S3 to S8 | `r-97ae…`: the brief, sent while theo is offline, held at the relay, delivered, answered, returned. |
| III. Our brief to you | 40.800 to 63.000 | S9 to S12 | The beta terms, the invitation brief completed by hand, the boot, the address. |

### 1.4 Time shares, and the evidence behind them

| Block | evidence.md Implications §2 | This film | Share |
|---|---|---|---|
| Hook | 0 to 4 s (~7 %), a real situation, ≥ 2 changes of shot or state, brand by 3 to 5 s | S1, 0.000 to 3.600. State change at 1.800 (second paste), shot change at 3.600, name legible at ~3.70 s | 5.7 % |
| What it is, for whom | 4 to 20 s (~27 %) | S2, 3.600 to 9.600. Definition at ~3.70, "For small teams." at 6.24, call tag from 9.600 | 9.5 % |
| Demonstration | 20 to 46 s (~43 %) | S3, S4, S6, S7, S8. One request, step by step, each step with one plain sentence | 32.4 % |
| Trust and control | 46 to 52 s (~10 %) | S5, 19.200 to 30.000. The relay wait, inside the demo | 17.1 % |
| The call | 52 to 60 s (~13 %) | S9 to S12, 40.800 to 63.000: terms, invitation brief, boot (3.6 s, fixed), address (held 4.56 s) | 35.2 % |

Rolled up the way evidence.md does it: **explain-led 15 %, show-led 50 % (of which trust 17 %), call-led 35 %.**

Why the balance differs from evidence.md's 35 / 45 / 20, and why that is right here:

- **Explaining is spread, not front-loaded.** Lee, Chu and Roy (2024) ask for a plain benefit line before any feature
  (S2 does it by 3.7 s). Every demo shot then carries one plain sentence ("Your agent writes the brief.", "It waits at
  the relay.", "It lands in their inbox." ...). By word count, 79 of the film's 134 on-screen words (59 %) explain
  or narrate the product, which is close to evidence.md's "explaining about 35 %, demonstrating about 45 %" combined.
- **Showing gets the most time.** Usage beats appearance (Frontiers 2022) and process beats outcome (Ringler 2024).
  Developers judge tools by trying them (Stack Overflow: free trial 75 %). The trust beat sits inside the demo, because
  46 % of developers distrust AI tools and reward honesty about limits (Stack Overflow 2025; evidence Q5.3).
- **The call is longer because three of its parts are mandatory.** The boot plays unchanged (3.6 s), the address is
  held for at least 4 s (owner and craft 1.12), and the owner's beta facts must be readable (S9). The call is also
  **signalled before the midpoint**: "For small teams." at 6.2 s and the tag "Apply at dorylinae.net" from 9.6 s to
  19.2 s (Wistia: mid-roll CTAs 22.1 % against 16.9 % post-roll; under one minute, first quarter). Viewers who leave at
  the midpoint (31.2 s) have seen what it is, who it is for, the address, and the relay beat.
- **Length 63.0 s** is inside the 55 to 65 s brief (evidence Implications §1: under a minute holds the most viewers;
  half are gone by the midpoint, so what, for whom and the call all land by 19.2 s).
- **Silent-first** (evidence Q2): every claim is burned-in type and no voice is used. The score adds feeling only.

Average shot length is 5.3 s over S1 to S10. The rhythm is deliberately uneven (3.6, 6.0, 3.6, 6.0, 10.8, 3.6, 4.8,
2.4, 5.4, 6.6 s). This answers the craft judge's "identical 4.8 s cadence" note on A. The one long shot (S5) is the
payoff hold that craft 1.2 allows.

### 1.5 Must-fix ledger (every judge item, and where it is fixed)

| Judge, concept | Item | Fixed by |
|---|---|---|
| Newcomer A | Audience before midpoint | "For small teams." lands 6.24 s (S2). |
| Newcomer A, D | One call worded identically, before 20 s; tag shorter | "Apply at dorylinae.net" in the tag (9.6 to 19.2 s, 46.2 to 52.8 s) and on the end card. Tag on screen 16.2 s (26 %). |
| Newcomer A | Hook actor explicit | "They paste it into their coding agent." |
| Newcomer A | No notebook dressing | No Obs. numbers, no date at frame 0. Collector labels give machine and time only ("theo · 13:43 UTC"). |
| Newcomer A, craft B | Felt payoff at hero size | S7: "Cause found: two tests share one ledger row" pushed to 64 px with a pheromone underline. |
| Newcomer D, craft D, truth D | Product, pain and name in the first 5 s | Real brief at frame 0; "Dorylinae" legible at ~3.70 s; trail moving at 4.8 s. |
| Newcomer D | Placeholders only after the real brief | The template placeholder appears once, at 46.2 s, after 3 views of the real brief. |
| Newcomer B, truth B | BRIEF relay sentence | "The relay sees / who, when and how big. / Not what." with labels on `time=`, `from= to=` and `bytes=`. |
| Newcomer B | Relay stillness ≤ 1 bar, with a reading event | Stillness 27.106 to 28.800 s (1.7 s), while the labels and caption are read. |
| Newcomer B, truth B | Free and platforms promoted from small print | S9: Plex Sans 500 at 64 px (16:9) / 48 px (4:5). |
| Craft B | Concrete hook | Paste pain over the real brief. |
| Craft B | Captions out of the subtitle band | All captions top-left, first baseline 160 (16:9) / 170 (4:5). |
| Craft B | Limit in-plate zoom and pan | Only S4 and S7 (the two product-speaks moments). S5's push is a camera move in the field. |
| Craft B, D, truth D | Razor-hard 21° edge | §3.6: no stroke, glow, blur or motion blur on the edge. |
| Craft B | Rebuild the beta card | S10 is the invitation brief. |
| Craft D | Demo needs a signature beat | S5. |
| Craft D, truth D | Thin Special Elite in the poster | Why values in Plex Sans 500. Special Elite carries only What (2 lines) and the typed line. |
| Craft A | Type hero lines whole; break the 4.8 s rhythm; no 5-line list; end shot ≤ 9.6 s; drop Obs. card | Only 4 typed moments (labels, "Not what.", "Your team applies.", the boot's own letters). Uneven shot lengths. S9 has one hero line plus two Plex Sans facts. S11 4.8 s, S12 5.4 s. |
| Truth A | One accent per frame | Paused trail turns `line-strong`. Underlines are replaced, never doubled (§3.7). |
| Truth A | No invented live re-sort; no brief inside a terminal with no command | Inbox shown exactly as captured. Briefs appear only as the file plate (S3) or plain pasted plates (S1). |
| Truth A | No re-flow of output | Only crops at a frame or plate edge, and pans (S5 4:5). |
| Truth A, B, D | Sizes | Terminal ≥ 28 px (16:9) / ≥ 32 px (4:5), tokens 64 px / ≥ 36 px, story text ≥ 64 / ≥ 48 px, address 64 px. Exceptions are listed in §8. |
| Truth A, B | "Up to 7 days" depends on relay TTL | Not claimed. |
| Truth B | Spliced inbox and accept | Inbox and result are separate shots, each with its capture time ("13:41 UTC", "13:43 UTC"). |
| Truth B | Answer not implied as automatic | The answer is shown on theo's side. The return trail ends at maya's machine. Nothing shows maya's agent reading it (§8, risk 4). |
| Truth B | Frame 0 not empty | Frame 0 shows a full sentence over the real brief. |
| Truth D | `time=` and `from=` / `to=` visible and labelled; key hold for the trust claim | Both formats (S5); caption held 7.56 s. |
| Truth D | Time compression labelled | Collector labels 13:41 / 13:43 UTC; real timestamps in the relay log. |
| Truth D | Underline pitch constant | No pull-back. Screen underlines stay 24 px / 36 px pitch. World trails scale as a whole with the S5 push (§3.7). |
| Truth D, A, B | "By invitation"; live page | S9 label "Closed beta / by invitation". Live page is an owner dependency (§8). |

---

## 2. Master timeline

| Shot | Name | Start | End | Start bar:beat | End bar:beat | Dur | Into the shot | Purpose |
|---|---|---|---|---|---|---|---|---|
| S1 | Paste | 0.000 | 3.600 | 1:1 | 2:3 | 3.6 | film start | The pain: the same brief pasted twice by hand. Monochrome. |
| S2 | With Dorylinae | 3.600 | 9.600 | 2:3 | 5:1 | 6.0 | **21° cut #1** | Name, definition, two machines, first trail, "For small teams." |
| S3 | The brief | 9.600 | 13.200 | 5:1 | 6:3 | 3.6 | hard cut | Your agent writes the brief (the same one). Plants *Done when*. Tag in. |
| S4 | Offline | 13.200 | 19.200 | 6:3 | 9:1 | 6.0 | hard cut | The request goes out; the product says theo is away. |
| S5 | The relay | 19.200 | 30.000 | 9:1 | 13:3 | 10.8 | hard cut | Signature: it waits; who, when, how big, not what; theo is back. |
| S6 | Inbox | 30.000 | 33.600 | 13:3 | 15:1 | 3.6 | hard cut | It lands in theo's inbox. Midpoint 31.2 s falls here. |
| S7 | The cause | 33.600 | 38.400 | 15:1 | 17:1 | 4.8 | hard cut | Their agent finds the cause: the payoff at 64 px. |
| S8 | The way back | 38.400 | 40.800 | 17:1 | 18:1 | 2.4 | hard cut | The answer comes back, passing the relay without stopping. |
| S9 | The terms | 40.800 | 46.200 | 18:1 | 20:2 | 5.4 | **21° cut #2** | Closed beta, by invitation; 10 teams; free; 3 OS. |
| S10 | Our brief to you | 46.200 | 52.800 | 20:2 | 23:1 | 6.6 | hard cut | The invitation brief; "Your team applies." typed; poster frame. |
| S11 | Boot | 52.800 | 57.600 | 23:1 | 25:1 | 4.8 | hard cut | One beat of silence, then the unchanged boot. |
| S12 | Address | 57.600 | 63.000 | 25:1 | 27:2 | 5.4 | no cut (state change) | "Apply at dorylinae.net", held 4.56 s; 0.6 s of ground. |

Midpoint 31.200 (14:1). Total **63.000 s**.

---

## 3. Build specification

### 3.1 Colour tokens (dark theme, DESIGN.md; no other colours)

| Token | Hex | Used for |
|---|---|---|
| `bg` (void) | `#07060F` | Ground of S1, behind every sky |
| `surface` | `#0E0C1F` | Terminal plates, S1 paste plates, relay-log plate |
| `surface-2` | `#1A1733` | Brief file sub-plates (S3) |
| `line-strong` | `#6E64A8` | Paused trail (S5), relay holding ring |
| `text` | `#EFE9FB` | Captions, the one noticed terminal token, names |
| `text-2` | `#A79FC4` | Commands, brief values, S1 plate text, tag, Why values, S9 facts |
| `text-3` | `#8C85AB` | Terminal output, collector labels, relay dot, offline ring, placeholder |
| `brand` (amethyst) | `#A678E6` | Prompts `maya$` `theo$`, field names `What:` `Why:` `Done when:`, the word "Dorylinae" in S2 |
| `accent` (pheromone) | `#D653E1` | Trails and underlines only, one per frame |
| `online` | `#5CE6C8` | Solid presence dot and its pulse halo |
| label stock / ink | `#F6F3FB` / `#17122B` | Specimen labels |

S1 uses no colour token but `bg`, `surface` and `text`/`text-2` (the world before the product). Amethyst enters with
the name at 3.6 s, teal at 3.9 s, pheromone at 4.8 s.

### 3.2 Type roles (brand tokens at film zoom, craft 5.1; measured widths)

| Role | Face | 16:9 | 4:5 | Colour | Arrives |
|---|---|---|---|---|---|
| CAPTION | Special Elite 400 | 72 px, pitch 80, x 96, first baseline 160 | 60 px, pitch 68, x 64, first baseline 170 | `text` | Present on a hard cut; otherwise enter 240 ms (opacity 0→1, rise 12 px / 18 px) |
| TERMINAL | IBM Plex Mono 400 | 42 px, pitch 64 (advance 25.2) | 36 px, pitch 54 (advance 21.6) | prompt `brand`, command `text-2`, output `text-3`, token `text` | Agent command: whole line in 1 frame, opacity 0→1 over 120 ms linear. Output: one line every 42 ms, same fade |
| TERMINAL texture | IBM Plex Mono 400 | 28 to 34 px as stated | 32 to 36 px as stated | as above | as above |
| FILE | IBM Plex Mono 400 | 36 px | 36 px | field name `brand`, value `text-2` | with its plate |
| NAME | IBM Plex Mono 500 | 28 px (world) | 32 px | `text` | with its plate group |
| COLLECTOR | Special Elite 400 | 28 px | 42 px | `text-3` | present on the cut |
| SPECIMEN LABEL | Special Elite 400 on label stock, ink, inset hairline 1 px at 55 % ink, radius 4 (16:9) / 6 (4:5), padding 0.9 em × 0.7 em, line-height 1.25 em | relay 28 px (world), cards 40 px, sender and beta 28 px | relay, cards, sender and beta 42 px | ink `#17122B` | typed (relay, cards) or plate lock (sender, beta) |
| TAG | Special Elite 400 | 52 px, right-aligned x 1824, baseline 104 (width 606) | 48 px, left x 64, baseline 1206 (width 560) | `text-2` | present on the cut |
| SHEET field | IBM Plex Mono 400 | 28 px | 32 px | `brand` | present |
| SHEET value | Special Elite 400 | 64 px, pitch 72 | 56 px, pitch 64 | `text` | present / typed |
| SHEET Why | IBM Plex Sans 500 | 64 px, pitch 72 | 48 px, pitch 58 | `text-2` | present |
| TERMS hero | Special Elite 400 | 96 px, pitch 104 | 72 px, pitch 84 | `text` | in the cut frame |
| TERMS facts | IBM Plex Sans 500 | 64 px, pitch 84 | 48 px, pitch 64 | `text-2` | enter 240 ms |
| ADDRESS | Special Elite 400 | 64 px, centred x 960 (width 746) | 64 px, centred x 540 | `text` | enter 240 ms |

Special Elite cap height is 0.689 em and Plex 0.698 em (measured; the font's OS/2 cap field is wrong, so do not use it).
Special Elite is never below 28 px (16:9) or 42 px (4:5), and is used only for single lines and labels.

### 3.3 Plates

- Terminal plates and paste plates: `surface`, radius 16 (16:9) / 24 (4:5), no border, no shadow, no window chrome.
- File sub-plates: `surface-2`, same radius, one channel apart: 12 px (16:9) / 18 px (4:5).
- **Plate lock** (signature move A): opacity 0→1 (reaching 1 at 62 % of the move) and translate from an offset to rest
  over 500 ms, brand ease, 45 ms stagger from the composition's axis outwards. Offsets 16:9: left element x −92, right
  element x +92, centre top y −36, antenna-like top y −60, bottom y +80. 4:5: −138 / +138 / −54 / −90 / +120.
  (`FILM.ui.lockIn` with brand px dx/dy: ±46, −18, −30, +40.)

### 3.4 Presence marks, the relay, names

- Online: solid `online` dot, Ø 12 px (16:9 world) / 18 px (4:5).
- Offline: hollow ring, same Ø, stroke 2 px / 3 px `text-3`.
- **Pulse:** a halo ring from the dot, radius × 1 → × 2.6, stroke 2 px / 3 px `online`, opacity 0.5 → 0 over 1.2 s, brand
  ease. Maya pulses on the listed downbeats; theo 0.3 s later when online. An **arrival pulse** is the same halo starting
  at opacity 0.8.
- **Ring fill** (offline → online): 120 ms. The ring's fill goes 0 → 1 `online` and its stroke goes `text-3` → `online`.
- Relay point: solid `text-3` dot, Ø 10 px (16:9 world) / 15 px (4:5). **Holding ring** (S5 only): circle r 14 px world,
  1 px `line-strong` (16:9; 28 px and 2 px at the 2× push) / r 21, 1.5 px (4:5). It enters 240 ms and leaves 120 ms.

### 3.5 Stage W: the two machines and the relay (used by S2, S5 and S8)

One layout, built once per format, framed by a camera.

**16:9 (world coordinates; at camera s = 1 and c = (960, 540) world = screen)**

| Element | Geometry |
|---|---|
| maya plate | x −240 → 540, y 470 → 580, `surface`, radius 16. Prompt `maya$` (Plex Mono 28 px, `brand`) at x 96, baseline 535 |
| theo plate | x 1380 → 2160, y 470 → 580. Prompt `theo$` at x 1408, baseline 535 |
| maya dot | centre (568, 525) |
| theo dot | centre (1352, 525) |
| relay dot | centre (960, 525) |
| name `maya` | Plex Mono 500 28 px `text`, left x 552, baseline 503 |
| name `theo` | right-aligned to x 1368, baseline 503 |
| trail line | y 525. Segment A x 580 → 948, segment B x 972 → 1340. Dots within 12 px of the relay dot centre are never drawn |
| relay label (S5, S8) | specimen label "relay", Special Elite 28 px, centred x 960, card x 895.3 → 1024.7, y 432.8 → 507.0 |
| relay-log plate (S5) | x 380 → 1600, y 640 → 698, `surface`, radius 16. Line A baseline 660, line C baseline 682, Plex Mono 14 px, letter-spacing −0.175 px (−0.0125 em, advance 8.225), first character of each line at x 396.55 |

**16:9 pushed frame (S5, camera s = 2, c = (960, 525)).** Screen = (960, 540) + 2 × (world − (960, 525)). The
plates sit at screen x ≤ 120 and ≥ 1800, y 430 → 650. Dots at (176, 540) and (1744, 540), relay (960, 540). Names'
baseline 496 (56 px). Relay label at x 830.6 → 1089.4, y 355.6 → 504.0 (56 px type). Log plate y 770 → 886. Line A
baseline 810 and line C baseline 854, both at 28 px with advance 16.45. Line A's character 16 sits at x 96.3 and its
end at x 1823.6. Characters 0 to 15 (`time=2026-10-08T`) lie left of x 96: those left of x 0 are cropped by the frame
edge, and the last few (`0-08T`) show in the margin as texture. The readable time is `13:41:40.228Z`.

**4:5 (world = screen; no push in S5)**

| Element | Geometry |
|---|---|
| maya plate | x 780 → 1300 (bleeds right), y −200 → 360, radius 24. Prompt `maya$` (Plex Mono 32 px `brand`) at x 828, baseline 320 |
| theo plate | x 780 → 1300, y 1160 → 1550 (bleeds bottom). Prompt `theo$` at x 828, baseline 1214 |
| maya dot | (940, 392), Ø 18 |
| relay dot | (940, 760), Ø 15 |
| theo dot | (940, 1128) |
| names | Plex Mono 500 32 px `text`, right-aligned to x 914: `maya` baseline 403, `theo` baseline 1139 |
| trail line | x 940 (vertical). Segment A y 404 → 748, segment B y 772 → 1116. Dots 9 px, pitch 36 |
| relay label (S5, S8) | Special Elite 42 px, card right edge x 912, x 717.9 → 912.0, y 704.4 → 815.6 |
| relay-log plate (S5) | x −24 → 920, y 568 → 692, radius 24, clips its text. Line A baseline 610, line C baseline 664 (at 36 px); see S5 for the strip |

The 4:5 caption column (x 64 to ~870, y 128 to 426) never meets the trail at x 940.

### 3.6 Transitions

- **Hard cut** on the stated beat. Exits are cuts. Nothing dissolves (craft 1.10).
- **Camera move in place of a cut**: none. Each shot is entered by a cut.
- **21° cut** (used twice: 3.600 and 40.800). A single hard edge 21° off vertical, top end to the right. The incoming
  shot is everything left of the edge. The edge line is x(y) = m(t) + (H/2 − y) × tan 21° (tan 21° = 0.38386). m(t) =
  m0 + (m1 − m0) × ease((t − t0) / 0.6). In 16:9, m0 = −207.29 and m1 = 2127.29; in 4:5, m0 = −259.11 and m1 = 1339.11.
  Implement it as a clip-path polygon on the incoming shot: (−10, −10), (x(0), −10), (x(H), H + 10), (−10, H + 10).
  The outgoing shot holds its last frame under it. **No stroke, glow, blur, smear or motion blur on the edge.**
  Anti-aliasing at native resolution only.

### 3.7 Pheromone budget and trail mechanics

- **At most one pheromone element per frame.** It is either a trail or an underline. The S5 paused trail is
  `line-strong`, not pheromone. When an underline moves to a new field, the old one fades out (120 ms) **before** the new
  one draws.
- **Screen underline:** 6 px dots, pitch 24, centre 12 px under the baseline (16:9); 9 px, pitch 36, 16 px under (4:5).
  It draws left to right over 240 ms (brand ease); each dot fades in over 60 ms when the head reaches it. Dot count =
  floor(width / pitch) + 1.
- **Trail (world):** 6 px dots, pitch 24 (16:9 world) / 9 px, 36 (4:5). Engine `FILM.ui.trail` with:
  - `draw(t)` = ease of the head over the stated time;
  - `march(t)` = pitch × (t − t0) / 0.6, linear, while in flight (one pitch per beat), frozen when stopped;
  - `tailFade(t)` = linear over the stated time.
  World trails scale with the camera. At the S5 2× push they render as 12 px dots on a 48 px pitch, which is the brand
  trail at 4× zoom with its proportions unchanged.
- **Stop at the relay** (S5): the march freezes, colour pheromone → `line-strong` at 0.7 opacity over 240 ms.
  **Resume:** colour back over 120 ms, march restarts.

### 3.8 Deep-field skies (brand `fieldSvg` through `film-gen/gen-assets.ts sky <seed> <w> <h> <name> <nebula> <vignette>`)

| Place | Seed (source of the number) | Nebula | Vignette | Render size 16:9 / 4:5 |
|---|---|---|---|---|
| S1 | none: flat `bg` | | | |
| Stage W: S2, S5, S8 | 97 (from `r-97ae`) | 1.0 | 0.55 | 2496 × 1404 / 1134 × 1418 |
| maya's machine: S3, S4 | 4968 (maya's pid, capture/08) | 0.6 | 0.55 | 2304 × 1296 / 1296 × 1620 |
| theo's machine: S6, S7 | 6333 (theo's pid after restart, capture/15) | 0.6 | 0.55 | 2304 × 1296 / 1296 × 1620 |
| Invitation: S9, S10 | 10 (the first wave) | 0.6 | 0.55 | 2016 × 1134 / 1134 × 1418 |
| End: S11, S12 | 1444 (the envelope's bytes, capture/25) | 0.6 | 0.55 | 1920 × 1080 / 1080 × 1350 |

The sky is one layer behind everything. It follows the camera at 0.3×: sky scale = 1 + 0.3 × (s − 1), and sky
translation = 0.3 × camera translation. **Text never sits on the nebula's rose.** Check every caption frame on a contact
sheet. If a seed puts rose under text, mirror the PNG horizontally or vertically (it is generated texture, not the
mark). Failing that, use seed + 1000. No scanlines anywhere except the signal mark's own, in the boot.

### 3.9 Motion blur and depth

- When the camera moves faster than 19 px per frame, use a 180° shutter (1/120 s) with 12 sub-samples (craft 6.1).
  Never on a reading hold, and never on the 21° edge.
- During S5's push, the two machine plates blur 0 → 4 px (screen) because they are not read. Nothing else is blurred.

---

### S1 · Paste · 0.000 → 3.600 (1:1 → 2:3), 3.6 s

**Purpose.** The pain a viewer already has, shown with the real brief, in the first frame. Frame 0 is a complete
thought, so it also works as the autoplay frame and a fallback thumbnail.

**16:9 layout (flat `bg`, no stars)**

| Element | Type | Geometry | Text |
|---|---|---|---|
| C1 | CAPTION, `text` | x 96, baseline 160 (width 895) | `You paste it into a chat.` |
| C2 | CAPTION, `text` | x 96, baseline 240 (width 1418) | `They paste it into their coding agent.` |
| Plate A | `surface` plate | x 96 → 1680, y 400 → 600 | brief, 3 lines |
| Plate A text | Plex Mono 32 px `text-2` | x 144, baselines 464, 512, 560 | see below |
| Plate B | `surface` plate | x 240 → 1824, y 612 → 812 (one channel below A, offset 144 right) | same brief |
| Plate B text | Plex Mono 32 px `text-2` | x 288, baselines 676, 724, 772 | see below |

**4:5 layout**

| Element | Geometry | Text |
|---|---|---|
| C1 | x 64, baseline 170 (746) | `You paste it into a chat.` |
| C2 | x 64, baselines 238 / 306 | `They paste it into` / `their coding agent.` |
| Plate A | x −24 → 1104 (bleeds both sides), y 400 → 600; text Plex Mono 32 px `text-2` at x 64, baselines 464, 512, 560; lines crop at the right frame edge (52 characters) | brief |
| Plate B | x −24 → 1104, y 618 → 818; text at x 160, baselines 682, 730, 778 | same brief |

**Brief text** (both plates, verbatim, `capture/run/work/flaky-brief.md`; byte-identical to the `brief` field in
`capture/20-grant.txt`). Field names are in `text-2` here: no brand colour exists yet.

```
What: Find out why the payments integration test fails about one run in ten.
Why: It blocks merges twice a day and nobody owns it yet.
Done when: You name the cause, or rule out the three usual suspects.
```

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 0.000 | 1:1 | C1 and Plate A are present at frame 0. |
| 0.300 | 1:1.5 | C2 enters (240 ms) and lands at 0.540. |
| 1.800 | 1:4 | Plate B locks in from x +92 (4:5: +138), 500 ms. It is the second paste, the shot's one moving thing. |
| 0.000 → 3.600 | | Camera: s 1.000 → 1.030, brand ease over the whole shot. The world point (96, 160) (4:5: (64, 170)) stays fixed on screen. |
| 3.600 | 2:3 | 21° cut #1 to S2. |

Pheromone: none. Sound: room tone; the paste click at 1.800 (§5).

---

### S2 · With Dorylinae · 3.600 → 9.600 (2:3 → 5:1), 6.0 s

**Purpose.** Name and definition in one plain sentence. Two machines, one hand-off. Who it is for. This is the
alternate poster frame at **7.200 s** (definition, audience and the full trail in one still).

**Layout.** Stage W (§3.5), sky seed 97. Camera s 1.00 → 1.03 over the shot, brand ease, about the frame centre. No
relay label in this shot: the relay dot is there, unlabelled. Names `maya` and `theo` appear with their plates.

| Element | 16:9 | 4:5 | Text |
|---|---|---|---|
| C3 | CAPTION, x 96, baselines 160 / 240 (widths 1003 / 695) | x 64, baselines 170 / 238 / 306 | 16:9: `With Dorylinae, your agent` / `hands it to theirs.` · 4:5: `With Dorylinae,` / `your agent hands it` / `to theirs.` The word `Dorylinae` is `brand`; the rest `text`. |
| C4 | CAPTION, x 96, baseline 360 (598) | x 64, baseline 410 (498) | `For small teams.` |
| Plates, dots, names, relay dot | Stage W | Stage W | `maya$`, `theo$`, `maya`, `theo` |

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 3.600 | 2:3 | 21° cut #1 (600 ms). C3 is part of the incoming frame. It is fully revealed by ~3.70 s, so the name is legible at 3.7 s. |
| 3.900 | 2:3.5 | Plate group locks in: maya plate + `maya` name + maya dot from x −92 (4:5: from y −54), theo's group from x +92 (4:5: y +120), 45 ms apart, 500 ms. Relay dot fades in 240 ms. Both dots are online (solid teal). |
| 4.800 | 3:1 | Trail head draws maya dot → theo dot through the relay point (16:9: x 580 → 1340; 4:5: y 404 → 1116) over 1.2 s, brand ease, marching. It passes the relay without stopping (both are online). 16:9: 32 dot positions (one skipped at the relay). 4:5: 20 positions. Maya pulse 4.800, theo pulse 5.100. |
| 6.000 | 3:3 | Arrival: march stops (120 ms); theo arrival pulse. C4 enters (240 ms), lands 6.240. |
| 7.200 | 4:1 | Pulses (maya 7.200, theo 7.500). Trail tail fade begins: linear 7.200 → 9.600. **Poster frame 7.200.** |
| 9.600 | 5:1 | Hard cut to S3. |

Pheromone: the trail only (4.8 → 9.6).

---

### S3 · The brief · 9.600 → 13.200 (5:1 → 6:3), 3.6 s

**Purpose.** "Your agent writes the brief." It is the same text the viewer saw pasted at frame 0, now in the
product's form. The `Done when:` underline plants the film's through-line. The call tag enters.

**16:9 layout (sky seed 4968)**

| Element | Type | Geometry | Text |
|---|---|---|---|
| C5 | CAPTION, present at cut | x 96, baseline 160 (1041) | `Your agent writes the brief.` |
| Tag | TAG | right-aligned x 1824, baseline 104 (x 1218 → 1824) | `Apply at dorylinae.net` |
| Collector | COLLECTOR | x 96, baseline 290 (222) | `maya · 13:41 UTC` |
| File name | Plex Mono 28 px `text-3` | x 144, baseline 470 | `flaky-brief.md` |
| Sub-plate What | `surface-2` | x 96 → 1944 (bleeds right), y 498 → 582 | |
| Sub-plate Why | `surface-2` | x 96 → 1944, y 594 → 678 | |
| Sub-plate Done when | `surface-2` | x 96 → 1944, y 690 → 774 | |
| Brief lines | FILE 36 px, x 144 | baselines 554, 650, 746 (line 1 ends x 1786) | the 3 lines of `flaky-brief.md` |

**4:5 layout**

| Element | Geometry | Text |
|---|---|---|
| C5 | x 64, baseline 170 (867) | `Your agent writes the brief.` |
| Tag | left x 64, baseline 1206 | `Apply at dorylinae.net` |
| Collector | x 64, baseline 290 (334) | `maya · 13:41 UTC` |
| File name | Plex Mono 32 px `text-3`, x 64, baseline 520 | `flaky-brief.md` |
| Sub-plates | x −24 → 1104, heights 96, one 18 px channel apart: What y 550 → 646, Why 664 → 760, Done when 778 → 874 | |
| Brief lines | FILE 36 px, x 64, baselines 610, 724, 838; crop at the right frame edge (47 characters) | |

Brief text: identical to S1 (field names now `brand`, values `text-2`).

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 9.600 | 5:1 | Cut. C5, tag, collector and file name are present. |
| 9.750 | 5:1.25 | Sub-plates lock in the boot's own order: **Why** (centre) 9.750 from y −36 (4:5 −54); **Done when** (lower) 9.795 from y +80 (4:5 +120); **What** (top) 9.840 from y −60 (4:5 −90). 500 ms each, with their text. All settled by 10.340. |
| 12.000 | 6:1 | Pheromone underline under `Done when:` (16:9: x 144 → 360, y 758, 10 dots; 4:5: x 64 → 280, y 854, 7 dots), 240 ms. |
| 9.600 → 13.200 | | Camera s 1.00 → 1.02 about (960, 640) (4:5: (540, 700)). |
| 13.200 | 6:3 | Hard cut to S4. |

---

### S4 · Offline · 13.200 → 19.200 (6:3 → 9:1), 6.0 s

**Purpose.** The request leaves while theo is away, and the product itself says so. No caption: the product speaks.

**16:9 layout (sky seed 4968, same place as S3)**

| Element | Geometry |
|---|---|
| Tag | as S3 (screen) |
| Collector | x 96, baseline 290: `maya · 13:41 UTC` (screen) |
| Terminal plate | x 64 → 1984 (bleeds right), y 330 → 1110 (bleeds bottom), `surface`, radius 16 (world) |
| Terminal text | TERMINAL 42 px, x 112, baselines 410, 474, 538, 602 (command), 666, 730 (output) (world) |

**4:5 layout**

| Element | Geometry |
|---|---|
| Tag | left x 64, baseline 1206 |
| Collector | x 64, baseline 290 |
| Terminal plate | x −24 → 1104, y 340 → 1150 |
| Terminal text | TERMINAL 36 px, x 64, baselines 410, 464, 518, 572, 626, 680; lines crop at the right frame edge (47 characters) |

**Terminal** (`capture/15-offline-delivery.txt`. Edits: command broken at its flags with ` \` and a 4-space indent;
`r-97ae9a37feaf51fcf0c3c8246ae57be1` shortened to `r-97ae…`. Every flag is kept.)

```
maya$ agentnet request theo task \
    --title 'Track down the flaky payments test' \
    --brief-from-file flaky-brief.md \
    --idempotency-key demo-flaky-1
Queued normal task request r-97ae… to theo (team atlas)
  theo is offline, last seen 2026-10-08T13:41:37Z; it will be delivered when theo is back.
```

Noticed token: `it will be delivered when theo is back.` (characters 51 to 89 of the last line; colour `text`).

**Motion and camera**

| t | bar:beat | Event |
|---|---|---|
| 13.200 | 6:3 | Cut. Plate present with an empty `maya$` prompt. |
| 13.500 | 6:3.5 | The 4-line command lands whole (120 ms). |
| 13.800 | 6:4 | Output lines stream: 13.800 and 13.842. |
| 14.400 | 7:1 | **Push** (2 beats, brand ease, to 15.600). The token's start, world (1397.2, 730), moves from its screen position to screen (96, 600) as s goes 1.000 → 1.5238. Interpolate the screen position Q(t) and s(t) with the same ease; c(t) = P − (Q(t) − (960, 540)) / s(t), final c = (1964.2, 690.6). Token renders at 64 px, x 96 → 1594. 4:5: P = (1165.6, 680) → Q = (72, 700), s 1.000 → 1.1111, final c = (1586.8, 657.5). Token at 40 px, x 72 → 1008. Motion blur on. All other lines go to 40 % opacity over 120 ms at 14.400. |
| 15.600 | 7:3 | Pheromone underline under the token: 16:9 x 96 → 1584, y 612, 63 dots; 4:5 x 72 → 1008, y 716, 27 dots. |
| 15.600 → 19.200 | | Slow drift: s 1.5238 → 1.555 (4:5: 1.1111 → 1.125) about the token's centre. |
| 19.200 | 9:1 | Hard cut to S5. |

---

### S5 · The relay (signature) · 19.200 → 30.000 (9:1 → 13:3), 10.8 s

**Purpose.** The film's held breath. The request stops at the relay. The relay's own log line is labelled with what
it can see. "Not what." Stillness. On a downbeat theo's ring fills, the log records delivery, and the trail walks on.
It is honest (the BRIEF sentence, on the real line) and screenshot-able (the 16:9 frame from 27.2 s on).

**Elements.** Stage W (§3.5), sky seed 97. Theo's dot is a **hollow ring** at the cut. Screen elements:

| Element | 16:9 | 4:5 | Text |
|---|---|---|---|
| C6 (caption A) | x 96, baseline 160 (781) | x 64, baseline 170 (651) | `It waits at the relay.` |
| C7 (caption B) | x 96, baselines 160 / 240 (525 / 836) | x 64, baselines 170 / 238 | `The relay sees` / `who, when and how big.` |
| C8 | x 96, baseline 320 (341), typed | x 64, baseline 306, typed | `Not what.` |
| Relay label | world, Stage W | world, Stage W | `relay` (typed) |
| Log line A | world, Stage W log plate | 4:5 strip (below) | see Terminal |
| Log line C | world, from 28.800 | strip, from 28.800 | see Terminal |
| Cards | screen, below | screen, below | `when`, `who`, `how big` (typed) |

**Terminal** (`capture/25-relay-log.txt`, the relay's own verbose log. Edits: `m-b2d3dc4566beac6d9631949d221555c0`
shortened to `m-b2d3…`; the lines between are omitted; the start of each line is cropped by the frame edge, or by the
plate edge in 4:5. This is the same envelope as maya's request: its `mail_id` in `capture/15` is
`m-b2d3dc4566beac6d9631949d221555c0`.)

```
time=2026-10-08T13:41:40.228Z level=INFO msg=queued event=queue from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444
time=2026-10-08T13:41:45.886Z level=INFO msg="delivered from queue" event=queue_flush peer=ZhbF0Hsy count=1
```

Line A is 121 characters. Field indices (0-based): `13:41:40.228Z` 16 to 28 (when); `from=QZ_vPMFN to=ZhbF0Hsy`
64 to 88 (who); `bytes=1444` 111 to 120 (how big). Line C is 107 characters.

**16:9 cards (screen, Special Elite 40 px on label stock, height 106, bottom y 740).** Leader: 2 px `text-2`, 21° off
vertical, from the card's bottom centre to y 782.5 (8 px above line A's cap top). Each leader leans so that the card
sits toward the frame centre from its anchor (Δx = 16.3).

| Card | Anchor on line A (screen) | Card centre x | Card x | Underline (y 822) |
|---|---|---|---|---|
| `when` | x 280 (in `…40.228Z`) | 296.3 | 210.9 → 381.7 | x 96.3 → 310.1, 9 dots |
| `who` | x 1091.5 (centre of `from=… to=…`) | 1075.2 | 1001.6 → 1148.8 | x 885.9 → 1297.1, 18 dots |
| `how big` | x 1690 (in `bytes=1444`) | 1673.7 | 1560.0 → 1787.5 | x 1659.0 → 1823.5, 7 dots |

**4:5 strip.** In 4:5, line A cannot be both whole and legible (121 characters). The log text is a strip clipped by
the log plate (x −24 → 920). It moves only by pans, which are allowed edits (crop or pan; no re-wrap). Strip transform:
character i sits at x = X + i × a, with line A's baseline fixed at 610.

| Phase | X | Advance a (font) | What is legible |
|---|---|---|---|
| W1 (when) | −281.6 | 21.6 (36 px) | characters 16 to 54: `13:41:40.228Z level=INFO msg=queued ev…` |
| W2 (who) | −1170.4 | 21.6 | characters 55 to 96: `…queue from=QZ_vPMFN to=ZhbF0Hsy type=m…` |
| W3 (how big) | −1713.6 | 21.6 | characters 80 to 120, ending at x 900: `…sy type=mail id=m-b2d3… bytes=1444` |
| Overview | −63.4 | 7.962 (13.27 px) | the whole of characters 16 to 120 (texture), with all three cards |

Line C's baseline is 610 + 54 × (a / 21.6) (664 at W phases, 629.9 at overview). 4:5 cards: Special Elite 42 px,
height 111.3, bottom y 551. Leader end y 576.9 at W phases (Δx 9.9) and 592.7 at overview (Δx 16.0). Cards are
anchored to a strip character, so they ride with the strip and leave the frame when it pans past them.

| Card | Anchor character | W-phase card x | Overview card x | Underline (y 626, W phase only) |
|---|---|---|---|---|
| `when` (leans right) | 28.32 | W1: 250.3 → 429.7 | 88.4 → 267.8 | x 64 → 345, 8 dots |
| `who` (leans left) | 76.5 | W2: 394.9 → 549.4 | 452.5 → 606.9 | x 212 → 752, 16 dots |
| `how big` (leans left) | 111.4 | W3: 563.3 → 802.2 | 688.1 → 927.1 | x 684 → 900, 7 dots |

**Motion (shared by both formats unless marked)**

| t | bar:beat | Event |
|---|---|---|
| 19.200 | 9:1 | Cut. C6 present. Maya online (pulse 19.200), theo hollow ring. Relay label types `relay` (letters 19.200, 19.285, 19.370, 19.455, 19.540; lands 19.600); the card locks from y −8 world, 240 ms. Trail **segment A** head draws maya → relay (16:9: x 580 → 948; 4:5: y 404 → 748) over 1.2 s, brand ease, marching. 16 / 10 dots. |
| 20.400 | 9:3 | **Stop.** March freezes; trail → `line-strong` at 0.7 opacity (240 ms); holding ring enters (240 ms). Log plate and line A land (120 ms). 16:9 line A is 14 px world (texture until the push). 4:5 strip at W1, 36 px. |
| 21.600 | 10:1 | Maya pulse. **16:9 push:** s 1 → 2, c (960, 540) → (960, 525), 1.2 s, brand ease, motion blur on; machine plates blur 0 → 4 px. 4:5: no push. |
| 22.200 | 10:2 | C6 cuts out. C7 enters (240 ms), lands 22.440. |
| 22.800 | 10:3 | **4:5 only:** `when` card. The leader draws over 120 ms while the card locks (240 ms, from y −8). Text types 22.800 → 23.115; underline under the when field from 22.920. |
| 23.400 | 10:4 | **16:9:** `when` card, same grammar, types 23.400 → 23.715; underline 23.520 → 23.760. **4:5:** underline fades (120 ms); strip pans W1 → W2 (1 beat, brand ease, to 24.000). |
| 24.000 | 11:1 | Maya pulse (the last before the held breath). **4:5:** `who` card types 24.000 → 24.230; underline 24.120 → 24.360. |
| 24.600 | 11:2 | **16:9:** `when` underline fades (24.600 → 24.720). `who` card types 24.600 → 24.830; underline 24.720 → 24.960. |
| 25.200 | 11:3 | **4:5:** underline fades; strip pans W2 → W3 (to 25.800). |
| 25.800 | 11:4 | Both formats: `how big` card types 25.800 → 26.336 (16:9 underline fades first, new one 25.920 → 26.160; 4:5 underline 25.920 → 26.160). |
| 26.400 | 12:1 | Underline fades (120 ms). **C8 `Not what.` types** at 85 ms per letter: 26.400 → 27.106. No pulse on this downbeat. **4:5:** strip zooms to the overview (X −1713.6 → −63.4, a 21.6 → 7.962) over 1 beat to 27.000. The cards re-pin and the `when` and `who` cards slide back into frame. |
| 27.106 → 28.800 | | **Stillness**, 1.7 s. Nothing moves: no march, no pulse, camera still. 16:9 frame from 27.2 s is the relay screenshot. |
| 28.800 | 13:1 | **Theo is back** (one event on the downbeat): theo's ring fills (120 ms) and he pulses at 29.100; the holding ring leaves (120 ms); line C lands under line A (120 ms) (16:9 visible from `13:41:45.886Z`, ending at x 1593.2; 4:5 overview texture); segment A returns to pheromone (120 ms) and marches; **segment B** draws relay → theo (16:9: x 972 → 1340; 4:5: y 772 → 1116) over 1.2 s, marching. Maya pulse. Blur on the plates returns to 0 (240 ms). |
| 30.000 | 13:3 | Hard cut to S6 as segment B reaches theo. |

Holds: C6 3.0 s; C7 7.56 s; labels 3.7 to 6.3 s (16:9) or ≥ 3.0 s at the overview (4:5); `Not what.` 2.89 s.

---

### S6 · Inbox · 30.000 → 33.600 (13:3 → 15:1), 3.6 s

**Purpose.** It lands in theo's inbox: the same title the viewer saw in maya's command. The midpoint falls here.

**16:9 layout (sky seed 6333)**

| Element | Geometry | Text |
|---|---|---|
| C9 | CAPTION, x 96, baseline 160 (891), present at cut | `It lands in their inbox.` |
| Collector | x 96, baseline 290 | `theo · 13:41 UTC` |
| Terminal plate | x 184 → 1984 (bleeds right), y 400 → 664 | |
| Terminal text | Plex Mono 32 px, x 232, baselines 470, 520, 570, 620. Row 1 in `text-3` at 60 %; row 2's title in `text` | |
| Trail in | y 612, from x −24 to x 172 (9 dot positions), pheromone | |

**4:5 layout**

| Element | Geometry | Text |
|---|---|---|
| C9 | x 64, baseline 170 (742) | `It lands in their inbox.` |
| Collector | x 64, baseline 290 | `theo · 13:41 UTC` |
| Terminal plate | x −24 → 1104, y 380 → 640 | |
| Terminal text | TERMINAL 36 px, x 64, baselines 440, 494, 548, 602; rows crop at the frame edge (47 characters) | |
| Trail in | x 940, from y −36 to y 368 (12 positions) | |

**Terminal** (`capture/15-offline-delivery.txt`, theo's inbox right after his daemon restarted. Edits: ids shortened
to `r-d0d8…` and `r-97ae…`; the ID column is re-padded to the shortened width (characters unchanged, as
`capture/INDEX.md` allows); order exactly as captured. Same-priority requests list oldest first, so no ordering claim
is made.)

```
theo$ agentnet inbox
ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE
r-d0d8…  maya  task  normal   2000  1m   Add queue settings to the staging runbook
r-97ae…  maya  task  normal   2000  0m   Track down the flaky payments test
```

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 30.000 | 13:3 | Cut. C9 and collector present; empty `theo$` plate. Trail head enters from the frame edge to the plate edge over 1 beat, marching. |
| 30.600 | 13:4 | Arrival: march stops (120 ms); trail tail fade linear 30.600 → 31.200. `theo$ agentnet inbox` lands; the 3 output lines stream at 30.720, 30.762, 30.804. |
| 31.200 | 14:1 | Midpoint. Pheromone underline: 16:9 under `Track down the flaky payments test` (x 1019.2 → 1672.0, y 632, 28 dots); 4:5 under `r-97ae…` (x 64 → 208, y 618, 5 dots: the id the viewer saw in S4's output). |
| 30.000 → 33.600 | | Camera s 1.00 → 1.02 about the title (16:9 (1345, 612); 4:5 (136, 602)). |
| 33.600 | 15:1 | Hard cut to S7. |

---

### S7 · The cause · 33.600 → 38.400 (15:1 → 17:1), 4.8 s

**Purpose.** The payoff: their agent finds the cause. It meets the brief's own *Done when* ("You name the cause").

**16:9 layout (sky seed 6333)**

| Element | Geometry | Text |
|---|---|---|
| C10 | CAPTION, x 96, baseline 160 (1047), present at cut | `Their agent finds the cause.` |
| Collector | x 96, baseline 290 (screen) | `theo · 13:43 UTC` |
| Terminal plate | x 64 → 1984, y 330 → 1110 (world) | |
| Terminal text | TERMINAL 42 px, x 112, baselines 410, 474, 538 (command), 602 (output) (world) | |

**4:5 layout**: C10 x 64, baseline 170 (873); collector x 64, baseline 290; plate x −24 → 1104, y 340 → 1150;
TERMINAL 36 px, x 64, baselines 410, 464, 518, 572.

**Terminal** (`capture/21-quarantine-release.txt`, theo's agent submitting its result. Edits:
`s-b3098b5fd25dd5ec2aa2b78fe588fc03` shortened to `s-b309…`; command broken with ` \`.)

```
theo$ agentnet result s-b309… --status pass \
    --summary 'Cause found: two tests share one ledger row' \
    --file flaky-finding.md
Submitted result for s-b309… (open)
```

Noticed token: `Cause found: two tests share one ledger row` (characters 15 to 57 of line 2; colour `text`).

**Motion and camera**

| t | bar:beat | Event |
|---|---|---|
| 33.600 | 15:1 | Cut. C10, collector, empty `theo$` plate. |
| 33.900 | 15:1.5 | Command lands whole (120 ms); output at 34.020. |
| 34.200 | 15:2 | **Push** 2 beats to 35.400. 16:9: P = (490, 474) → Q = (96, 600), s 1.000 → 1.5238, final c = (1057.0, 434.6); token at 64 px, x 96 → 1747; the quote marks fall at x 58 and 1747 → 1786. 4:5: pan only, s 1.0, P = (388, 464) → Q = (72, 700), final c = (856, 439); token at 36 px, x 72 → 1001. Other lines to 40 % (120 ms). Motion blur on. |
| 35.400 | 15:4 | Pheromone underline under the token: 16:9 x 96 → 1728, y 612, 69 dots; 4:5 x 72 → 972, y 716, 26 dots. |
| 35.400 → 38.400 | | Drift s 1.5238 → 1.55 (4:5 1.00 → 1.015) about the token centre. |
| 38.400 | 17:1 | Hard cut to S8. |

---

### S8 · The way back · 38.400 → 40.800 (17:1 → 18:1), 2.4 s

**Purpose.** The answer comes back to maya's machine and passes the relay without stopping: the visual opposite of S5.

**Layout.** Stage W at s 1, sky seed 97, relay label present, both dots online. Camera s 1.00 → 1.02 about the centre.

| Element | 16:9 | 4:5 | Text |
|---|---|---|---|
| C11 | x 96, baseline 160 (840), present at cut | x 64, baseline 170 (700) | `The answer comes back.` |

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 38.400 | 17:1 | Cut. Maya pulse 38.400, theo 38.700. Return trail head draws theo → maya, one continuous line through the relay (16:9: x 1340 → 580; 4:5: y 1116 → 404) over 1.2 s, brand ease, marching toward maya. No stop and no holding ring. |
| 39.600 | 17:3 | Arrival at maya: march stops; maya arrival pulse. Tail fade linear 39.600 → 40.800 (from theo's end). |
| 40.800 | 18:1 | 21° cut #2 to S9. |

---

### S9 · The terms · 40.800 → 46.200 (18:1 → 20:2), 5.4 s

**Purpose.** What the beta is, stated as facts, in the owner's phrasing. Readable sizes (no small print).

**16:9 layout (sky seed 10, no plates)**

| Element | Type | Geometry | Text |
|---|---|---|---|
| Beta label | specimen label, Special Elite 28 px, two lines, plate-locked (not typed) | card x 96 → 347, y 260 → 369 | `Closed beta` / `by invitation` |
| Hero | TERMS hero 96 px `text` | x 96, baselines 500 / 604 (widths 1012 / 698) | `The first wave opens` / `with 10 teams.` |
| Facts | TERMS facts, Plex Sans 500 64 px `text-2` | x 96, baselines 740 / 824 (widths 612 / 828) | `Free during the beta.` / `macOS, Linux and Windows.` |

**4:5 layout**: label Special Elite 42 px, card x 64 → 441, y 200 → 364; hero 72 px at x 64, baselines 520 / 604
(759 / 523); facts Plex Sans 500 48 px `text-2` at x 64, baselines 730 / 794 (459 / 621).

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 40.800 | 18:1 | 21° cut #2. Label and hero are in the incoming frame; the hero is fully revealed by ~40.89. |
| 42.600 | 18:4 | Both fact lines enter together (240 ms), land 42.840. |
| 40.800 → 46.200 | | Camera s 1.00 → 1.03 about (96, 540) (4:5: (64, 640)). |
| 46.200 | 20:2 | Hard cut to S10. |

Pheromone: none.

---

### S10 · Our brief to you · 46.200 → 52.800 (20:2 → 23:1), 6.6 s

**Purpose.** The people at Dorylinae write to you in the product's own grammar. The open field is filled by hand.
The last frame is the **poster** (thumbnail and repost image): what, for whom, what it does, the call, the address.

**16:9 layout (sky seed 10; the sheet is typography on the sky, no card behind it)**

| Element | Type | Geometry | Text |
|---|---|---|---|
| Sender label | specimen label, Special Elite 28 px, present | card x 96 → 564, y 150 → 224 | `From the people at Dorylinae` |
| Field | SHEET field | x 96, baseline 300 | `What:` |
| Value | SHEET value 64 px | x 96, baselines 376 / 448 (932 / 525) | `We are opening a closed beta` / `for small teams.` |
| Field | SHEET field | x 96, baseline 534 | `Why:` |
| Value | SHEET Why, Plex Sans 500 64 px `text-2` | x 96, baselines 610 / 682 (1222 / 1397) | `Each of you keeps your own coding agent.` / `Dorylinae lets the agents hand each other work.` |
| Field | SHEET field | x 96, baseline 768 | `Done when:` |
| Placeholder | Plex Mono 28 px `text-3` | x 96, baseline 844 | `<how the other side knows it is finished>` |
| Typed value | SHEET value 64 px | x 96, baseline 844 (605) | `Your team applies.` |
| Underline | pheromone, y 856, x 96 → 696 | 26 dots | |
| Tag | TAG | right x 1824, baseline 104 | `Apply at dorylinae.net` |

**4:5 layout**

| Element | Geometry | Text |
|---|---|---|
| Sender label | Special Elite 42 px, card x 64 → 766, y 150 → 261 | `From the people at Dorylinae` |
| `What:` | Plex Mono 32 px `brand`, x 64, baseline 340 | |
| What value | Special Elite 56 px, x 64, baselines 410 / 474 (815 / 459) | `We are opening a closed beta` / `for small teams.` |
| `Why:` | baseline 560 | |
| Why value | Plex Sans 500 48 px `text-2`, x 64, baselines 628 / 686 / 744 (916 / 545 / 491) | `Each of you keeps your own coding agent.` / `Dorylinae lets the agents` / `hand each other work.` |
| `Done when:` | baseline 830 | |
| Placeholder | Plex Mono 32 px `text-3`, x 64, baseline 902 (787) | `<how the other side knows it is finished>` |
| Typed value | Special Elite 56 px, x 64, baseline 902 (530) | `Your team applies.` |
| Underline | y 918, x 64 → 568, 15 dots | |
| Tag | left x 64, baseline 1206 | `Apply at dorylinae.net` |

Product text: the field names `What:` `Why:` `Done when:` and the placeholder `<how the other side knows it is
finished>` are copied character for character from `capture/02-request-help.txt` (the brief template). Field names
sit above their values instead of inline (a layout choice; this is the people's own sheet, not terminal output).

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 46.200 | 20:2 | Hard cut. Everything except the typed value is present. One and a half bars to read What and Why. |
| 47.880 | 20:4.8 | The placeholder clears (120 ms, opacity → 0), done by 48.000. |
| 48.000 | 21:1 | `Your team applies.` types at 85 ms per letter, no caret. Letters start at 48.000, 48.085, 48.170, 48.255, 48.391, 48.476, 48.561, 48.646, 48.782, 48.867, 48.952, 49.037, 49.122, 49.207, 49.292 and 49.377. Lands 49.437. It is the only full line typed by hand. |
| 49.800 | 21:4 | Pheromone underline (240 ms). |
| 46.200 → 52.800 | | Camera s 1.00 → 1.02 about (96, 540) (4:5: (64, 640)). |
| 52.200 | 22:4 | **Poster frame** (deliver as PNG, both formats). |
| 52.800 | 23:1 | Hard cut to S11. |

---

### S11 · Boot · 52.800 → 57.600 (23:1 → 25:1), 4.8 s

**Layout.** Sky seed 1444 only. The tag is gone. Nothing else on screen.

| Element | 16:9 | 4:5 |
|---|---|---|
| Boot container (`FILM.boot`, `assets/boot.svg`, viewBox −566.59 −90 1133.19 1042.27) | left 552.25, top 85.23, width 815.5 (height 750.1) | left 105.07, top 160.91, width 869.9 (height 800.1) |
| Signal mark | 600 px wide (scanline pitch 4.05 px), top y 150 | 640 px wide (pitch 4.33 px), top y 230 |
| Wordmark DORYLINAE | baseline ≈ 764.1, cap top ≈ 669.8 | baseline ≈ 885.0, cap top ≈ 784.4 |
| Lockup bottom | 768.0 | 889.2 |
| Clear space (13 % of mark width) | 78 px | 83 px |

**Motion**

| t | bar:beat | Event |
|---|---|---|
| 52.800 | 23:1 | Cut to the bare sky. One beat of silence. |
| 53.400 | 23:2 | The boot plays **unchanged** for 3.6 s (`render(t − 53.400)`): plates from +0.15 s with 45 ms stagger and 500 ms brand ease; glint sweep +1.2 s for 0.75 s on the sweep curve (0.4, 0, 0.6, 1); signal +1.35 s; letters from +1.9 s every 85 ms (55.300 → 55.980). |
| 57.000 | 24:4 | Boot complete. The lockup holds alone for one beat. Scanlines are **static** (craft 6.3 phone shimmer risk; drift only if the phone test passes). No bloom, no shadow, no copy. |
| 57.600 | 25:1 | S12 (state change, no cut). |

The mark never animates anywhere else, the camera never rolls, and nothing enters the clear space.

---

### S12 · Address · 57.600 → 63.000 (25:1 → 27:2), 5.4 s

See §6.

---

## 4. Copy deck

Rules (craft 5.4): floor = max(1.5, 0.5 + 0.35 × words); key claim = max(2.0, 0.5 + 0.6 × words); both rounded up to
the half beat and counted from the moment the line has fully landed. The address counts as one word.

| # | Shot | On screen (verbatim) | Words | Floor / key | Lands | Leaves | Held | Met | Source |
|---|---|---|---|---|---|---|---|---|---|
| 1 | S1 | You paste it into a chat. | 6 | 2.7 / 4.2 | 0.000 | 3.600 | 3.60 | floor | product-facts §1a ("you paste the context into a chat"); §2 (PLAN:11 "pasting context into Slack or WhatsApp") |
| 2 | S1 | They paste it into their coding agent. | 7 | 3.0 / 4.8 | 0.540 | 3.600 | 3.06 | floor | §2 (PLAN:11 "each running their own agent harness"); BRIEF ("your coding agent") |
| 3 | S2 | With Dorylinae, your agent hands it to theirs. | 8 | 3.3 / 5.4 | ~3.700 | 9.600 | 5.90 | key | §1a ("your assistant sends the request straight to your teammate's assistant") |
| 4 | S2 | For small teams. | 3 | 1.8 / 2.4 | 6.240 | 9.600 | 3.36 | key | BRIEF beta facts; product-facts §5 #27 ("For small teams.") |
| 5 | S2, S5, S8 | maya · theo (names) | 1 | 1.5 / 2.1 | 4.400 | per shot | ≥ 2.4 | key | `capture/INDEX.md` (the two demo identities) |
| 6 | S3–S4 | Apply at dorylinae.net (tag) | 3 | 1.8 / 2.4 | 9.600 | 19.200 | 9.60 | key | BRIEF owner decision (CTA); beta-facts §5 ("the CTA is 'apply'") |
| 7 | S3 | Your agent writes the brief. | 5 | 2.4 / 3.6 | 9.600 | 13.200 | 3.60 | key | product-facts §5 #25 ("Your agent writes the brief."), §3.5; PLAN:89 |
| 8 | S3–S4 | maya · 13:41 UTC (collector) | label | 1.5 | 9.600 | 19.200 | 9.60 | key | capture/15 (request created 13:41:40Z in `request list`) |
| 9 | S3 | flaky-brief.md + 3 brief lines | texture | n/a | 10.340 | 13.200 | 2.86 | n/a (read at frame 0 too) | `capture/run/work/flaky-brief.md` |
| 10 | S4 | it will be delivered when theo is back. (token) | 8 | 3.3 / 5.4 | ~14.930 (push 90 %) | 19.200 | 4.27 | floor | `capture/15-offline-delivery.txt` |
| 11 | S5 | It waits at the relay. | 5 | 2.4 / 3.6 | 19.200 | 22.200 | 3.00 | floor | fact-check B1 (waits in the outbox and relay queue); `capture/25` (`msg=queued`); `capture/02` ("holds it … while the peer is offline") |
| 12 | S5 | relay (label) | 1 | 1.5 / 2.1 | 19.600 | 30.000 | 10.40 | key | `capture/25` (the relay's own log); product-facts §3.0 |
| 13 | S5 | The relay sees who, when and how big. | 8 | 3.3 / 5.4 | 22.440 | 30.000 | 7.56 | key | BRIEF ("The relay sees who, when and how big; it cannot read content"); product-facts §3.10 |
| 14 | S5 | when (16:9 / 4:5) | 1 | 1.5 / 2.1 | 23.715 / 23.115 | 30.000 | 6.29 / ≥ 3.0 at overview | key | `capture/25` `time=2026-10-08T13:41:40.228Z` |
| 15 | S5 | who (16:9 / 4:5) | 1 | 1.5 / 2.1 | 24.830 / 24.230 | 30.000 | 5.17 / ≥ 3.0 | key | `capture/25` `from=QZ_vPMFN to=ZhbF0Hsy` |
| 16 | S5 | how big | 2 | 1.5 / 2.1 | 26.336 | 30.000 | 3.66 | key | `capture/25` `bytes=1444` |
| 17 | S5 | Not what. | 2 | 1.5 / 2.1 | 27.106 | 30.000 | 2.89 | key | BRIEF ("it cannot read content"); product-facts §3.10 ("cannot see … titles, briefs, results"), §5 #3 |
| 18 | S5 | relay log lines A and C | evidence | ≤ 15 cps | 22.130 (16:9 push 90 %) | 30.000 | 7.87 | met (16:9) | `capture/25-relay-log.txt` |
| 19 | S6 | It lands in their inbox. | 5 | 2.4 / 3.6 | 30.000 | 33.600 | 3.60 | key | product-facts §3.6; `capture/15` (theo's inbox after restart) |
| 20 | S6 | theo · 13:41 UTC (collector) | label | 1.5 | 30.000 | 33.600 | 3.60 | key | `capture/25` (delivered 13:41:45.886Z); `capture/16` header (next step at 13:42:00Z) |
| 21 | S7 | Their agent finds the cause. | 5 | 2.4 / 3.6 | 33.600 | 38.400 | 4.80 | key | product-facts §3.11 (structured result); `capture/21` |
| 22 | S7 | theo · 13:43 UTC (collector) | label | 1.5 | 33.600 | 38.400 | 4.80 | key | `capture/21` header (13:43:54Z); `capture/25` (theo → maya mail at 13:43:54.425Z) |
| 23 | S7 | Cause found: two tests share one ledger row (token) | 8 | 3.3 / 5.4 | ~34.730 | 38.400 | 3.67 | floor | `capture/21-quarantine-release.txt` |
| 24 | S8 | The answer comes back. | 4 | 2.1 / 3.0 | 38.400 | 40.800 | 2.40 | floor | product-facts §3.11; `capture/25` (routed theo → maya, 13:43:54Z) |
| 25 | S9 | Closed beta / by invitation (label) | 4 | 2.1 / 3.0 | 40.800 | 46.200 | 5.40 | key | BRIEF beta facts ("Closed beta, by invitation") |
| 26 | S9 | The first wave opens with 10 teams. | 7 | 3.0 / 4.8 | 40.890 | 46.200 | 5.31 | key | BRIEF (exact owner wording); beta-facts §2 (Waves); fact-check B18 |
| 27 | S9 | Free during the beta. / macOS, Linux and Windows. | 8 (together) | 3.3 / 5.4 | 42.840 | 46.200 | 3.36 | floor | BRIEF beta facts; beta-facts §2; product-facts §3.21 |
| 28 | S10 | From the people at Dorylinae (label) | 5 | 2.4 / 3.6 | 46.200 | 52.800 | 6.60 | key | BRIEF ("We" only where Dorylinae, the people, speak); voice.md |
| 29 | S10 | What: We are opening a closed beta for small teams. | 9 | 3.9 / 6.0 | 46.200 | 52.800 | 6.60 | key | BRIEF beta facts; `capture/02` (field name) |
| 30 | S10 | Why: Each of you keeps your own coding agent. | 8 | 3.3 / 5.4 | 46.200 | 52.800 | 6.60 | key | BRIEF ("each of you keeps your own agent"); product-facts §2 |
| 31 | S10 | Dorylinae lets the agents hand each other work. | 8 | 3.3 / 5.4 | 46.200 | 52.800 | 6.60 | key | product-facts §2 (PLAN:7 "hand each other work safely") |
| 32 | S10 | Done when: <how the other side knows it is finished> | template | n/a | 46.200 | 48.000 | 1.80 | n/a (form, not claim) | `capture/02-request-help.txt` |
| 33 | S10 | Your team applies. | 3 | 1.8 / 2.4 | 49.437 | 52.800 | 3.36 | key | beta-facts §5 ("the CTA is 'apply' … must not imply instant access") |
| 34 | S10 | Apply at dorylinae.net (tag) | 3 | 1.8 / 2.4 | 46.200 | 52.800 | 6.60 | key | BRIEF owner decision |
| 35 | S12 | Apply at dorylinae.net | 3 | 1.8 / 2.4, and ≥ 4 s end card | 57.840 | 62.400 | 4.56 | key + end-card rule | BRIEF owner decision |

**Totals.** 134 on-screen words in 63.0 s: 118 in captions and labels, plus the two 8-word product tokens (79 in
Chapters I and II, 55 in Chapter III). The densest moment is the hook (13 words in 3.6 s; line 1 is readable from
frame 0). The S10 sheet holds 30 words for 6.6 s. Its lines repeat the S1 and S2 claims, and the frame is designed to be
paused (poster).

**Voice check.** Short declarative sentences, present tense, "you" for the reader, numerals ("10", "13:41"). No
exclamation marks, no emoji, no hype words. "We" appears once, under the sender label. No ant word appears, so the
one-per-screen limit is met. No tagline: the definition appears once and never with the logo. DORYLINAE appears only
in the lockup. Sentence case throughout. Coding agents stay generic. No harness names, versions, paths, ports, relay
hostname, GitHub, handles or hashtags anywhere.

---

## 5. Sound plan

Composed in code (TOOLING.md: numpy/scipy synthesis plus the VCSL Steinway B sampler, CC0). Key D major, 100 BPM, the
same grid as the picture. The score supports and never carries meaning. **One audio track serves both formats**,
except the S5 label ticks, which come from each format's own `FILM.cue` times (marked 16:9 / 4:5 below).

### 5.1 Palette

| Name | Sound | Level (peak unless RMS) |
|---|---|---|
| ROOM | band-passed noise 200 Hz to 2 kHz, "paper room" | −52 dBFS RMS |
| PASTE | dry plastic click, 8 ms: the only unmusical sound in the film | −24 dBFS |
| THUMP | low felt-mallet thump on D2, 120 ms | −20 dBFS |
| PAD | D2 + A2 + E3 (Dsus2), + F#3 in the product world; low-pass 1.2 kHz (2 kHz in the product world); attack 1.2 s | −30 dBFS RMS |
| PIANO | felt piano (soft, una corda, low-passed 4 kHz), chords rolled 30 ms per note | −20 dBFS |
| MOTIF | felt piano single notes, ~3 s natural decay: **A4 → F#4 → D4**, falling 5-3-1, one note per *Done when* moment | −18 dBFS |
| LUB / DUB | presence heartbeat: sub thump on D1 (60 ms, 40 ms pitch drop) + glass tick (8 ms, 2.6 kHz). LUB = maya, on her visible pulses. DUB = theo, 0.3 s later, only while he is online. DUB is 2 dB under LUB. In close-ups both run at −6 dB. | −24 dBFS (tick −36) |
| TICK | muted marimba A4, 180 ms decay, once per beat while a trail is in flight, panned along its travel | −28 dBFS |
| DYAD | felt piano A4 + D5, on a trail's arrival | −22 dBFS |
| LAND | dry wood tick, 20 ms, when an agent command lands (never per character) | −30 dBFS |
| PLINK | felt piano B3, D4, F#4, one per sub-plate lock (S3) | −30 dBFS |
| THOCK | felt low-mid thock, 80 ms, one per plate-lock group | −26 dBFS |
| LABEL | paper tap, 6 ms, band 1 to 3 kHz, one per typed letter of a **label** (relay, when, who, how big, the wordmark). Spaces are silent. | −36 dBFS |
| DRONE | the pad thinned to one A2 voice: the wait | −32 dBFS RMS |
| LOW | felt piano D3, 4 s decay | −18 dBFS |
| GLASS | sine E6, 0.4 s | −34 dBFS |

The two lines typed by people ("Not what.", "Your team applies.") are **silent**: they are read, not heard. Agent
commands never tick per character (craft 3).

### 5.2 Sections

| Section | Time | Music |
|---|---|---|
| Before | 0.000 → 3.600 | ROOM only. No music: the world before the product. |
| Definition | 3.600 → 9.600 | Music begins with the name: PAD and a Dadd9 roll. The two hearts start at 4.800. |
| The request | 9.600 → 19.200 | Bm7 → G → Asus4. At 14.400 (offline revealed) the pad darkens and theo's DUB has already stopped. |
| The wait | 19.200 → 28.800 | Trail ticks stop dead at 20.400. DRONE on A, maya's LUB at a distance, ROOM faintly back. No LUB at 26.400: the held breath. |
| Back and answered | 28.800 → 40.800 | A resolves to D at 28.800; DUB returns at 29.100; G → D cadence at 36.000 on the payoff; a bright upward roll on the return. |
| Our brief | 40.800 → 52.800 | Open fifth, ROOM, no hearts. D → Bm7 → Gmaj7 → Asus4, held unresolved under the typing. The motif's last note D4 lands at 49.800. |
| Silence | 52.800 → 53.400 | Everything off (craft 1.9: one deliberate silence before the end card). |
| Sonic logo | 53.400 → 57.000 | The boot cue, 6 beats. |
| Address | 57.600 → 62.400 | One low note; decay to digital silence by 62.400; silence to 63.000. |

Downbeat accents: one strong hit per bar at most, on bar 1 of the bar. Structural cuts and key landings sit on
beats; micro-motion (letters, settles) falls between them.

### 5.3 Cue list

| t | bar:beat | Cue | Notes |
|---|---|---|---|
| 0.000 | 1:1 | ROOM on | until 3.550 |
| 1.800 | 1:4 | PASTE | Plate B locks in |
| 3.500 | 2:2.83 | PAD in (1.2 s attack); PIANO Dadd9 roll (D3 A3 E4 F#4) | 6-frame pre-lap (J-cut) |
| 3.600 | 2:3 | THUMP | 21° cut #1 |
| 3.900 | 2:3.5 | THOCK | plates lock |
| 4.800 | 3:1 | LUB; TICK pan −0.4 | trail leaves maya |
| 5.100 | 3:1.5 | DUB | |
| 5.400 | 3:2 | TICK pan 0 | |
| 6.000 | 3:3 | DYAD pan +0.4 | arrival at theo |
| 7.200 | 4:1 | LUB; PIANO Gmaj7/D (D3 G3 B3 F#4) | |
| 7.500 | 4:1.5 | DUB | |
| 9.500 | 4:4.83 | PIANO Bm7 (B2 F#3 A3 D4) | pre-lap |
| 9.600 | 5:1 | LUB (−6 dB) | close-up |
| 9.750 / 9.795 / 9.840 | 5:1.25 | PLINK B3 / D4 / F#4 | Why, Done when, What lock |
| 9.900 | 5:1.5 | DUB (−6 dB) | |
| 12.000 | 6:1 | LUB; **MOTIF A4**; PIANO G (G2 D3 B3) | `Done when:` underline |
| 12.300 | 6:1.5 | DUB, **the last before theo is shown away** | |
| 13.500 | 6:3.5 | LAND | the command lands |
| 14.400 | 7:1 | LUB; PIANO Asus4 (A2 E3 D4), held; PAD low-pass 2 kHz → 900 Hz over 1 bar | push to "it will be delivered when theo is back." |
| 16.800 | 8:1 | LUB | |
| 19.200 | 9:1 | LUB; TICK pan −0.5; LABEL ×5 (19.200, 19.285, 19.370, 19.455, 19.540) | trail to the relay; `relay` typed |
| 19.800 | 9:2 | TICK pan −0.25 | |
| 20.400 | 9:3 | **hard stop**: no tick; PAD → DRONE on A2 (150 ms release); ROOM back at −56 dBFS | the trail stops |
| 21.600 | 10:1 | LUB (−4 dB) | push in |
| 22.800 | 10:3 | 4:5: LABEL ×4 (22.800, 22.885, 22.970, 23.055) | `when` (4:5) |
| 23.400 | 10:4 | 16:9: LABEL ×4 (23.400, 23.485, 23.570, 23.655) | `when` (16:9) |
| 24.000 | 11:1 | LUB (−6 dB), the last before the held breath. 4:5: LABEL ×3 (24.000, 24.085, 24.170) | `who` (4:5) |
| 24.600 | 11:2 | 16:9: LABEL ×3 (24.600, 24.685, 24.770) | `who` (16:9) |
| 25.800 | 11:4 | LABEL ×6 (25.800, 25.885, 25.970, 26.106, 26.191, 26.276) | `how big` (both) |
| 26.400 | 12:1 | nothing: no LUB on the downbeat | "Not what." typed in silence |
| 27.000 | 12:2 | **LOW D3** | as "Not what." completes (27.106) |
| 27.106 → 28.800 | | DRONE + ROOM + the D3 tail only | stillness |
| 28.800 | 13:1 | PIANO Dadd9 (D3 A3 E4 F#4): the A drone resolves to D; LUB; TICK pan 0; PAD back to the product colour (0.6 s attack) | theo is back |
| 29.100 | 13:1.5 | **DUB returns** | |
| 29.400 | 13:2 | TICK pan +0.5 | |
| 30.000 | 13:3 | TICK pan +0.7 | S6 trail entering |
| 30.600 | 13:4 | DYAD pan +0.6; LAND | arrival; inbox command |
| 31.200 | 14:1 | LUB | midpoint, underline |
| 31.500 | 14:1.5 | DUB | |
| 33.500 | 14:4.83 | PIANO Gmaj9 (G2 D3 A3 B3 F#4) | pre-lap |
| 33.600 | 15:1 | LUB | |
| 33.900 | 15:1.5 | LAND; DUB | result command |
| 35.400 | 15:4 | **MOTIF F#4**; GLASS E6 | the cause is underlined: the brief's *Done when* is met |
| 36.000 | 16:1 | LUB; PIANO Dadd9 | the G → D cadence |
| 36.300 | 16:1.5 | DUB | |
| 38.400 | 17:1 | LUB; TICK pan +0.5; PIANO D upward roll (D3 F#3 A3 D4 E4, 60 ms per note) | the way back |
| 38.700 | 17:1.5 | DUB (last heartbeat of the film) | |
| 39.000 | 17:2 | TICK pan 0 | passes the relay |
| 39.600 | 17:3 | DYAD pan −0.5 | arrival at maya |
| 40.700 | 17:4.83 | PAD to open fifth D2 + A2; PIANO D (D2 A2 D3) | pre-lap |
| 40.800 | 18:1 | THUMP; ROOM on (−52) | 21° cut #2 |
| 43.200 | 19:1 | PIANO Bm7 (soft, −24) | |
| 45.600 | 20:1 | PIANO Gmaj7 (−24) | |
| 48.000 | 21:1 | PIANO Asus4 (A2 E3 D4), held | typing starts (silent) |
| 49.800 | 21:4 | **MOTIF D4**: A4 – F#4 – D4 completes | "Your team applies." underlined |
| 52.650 | 22:4.75 | everything releases (150 ms) | |
| 52.800 → 53.400 | 23:1 | **silence** | |
| 53.550 → 54.540 | 23:2.25 | boot ripple: felt-mallet D2, A2, D3, F#3, A3, D4 at 53.550, 53.750, 53.950, 54.150, 54.350 and 54.540 (−28 dBFS) | follows the 23 plate locks |
| 54.600 → 55.350 | 23:4 | soft filtered-air sweep, band 6 → 2 kHz, envelope on the sweep curve (−34 dBFS) | glint sweep |
| 54.750 | 23:4.25 | **bloom**: D3 F#3 A3 E4 (the motif's three notes sounded together, upward, plus add9), 1.2 s attack, −20 dBFS | signal mark |
| 55.300 → 55.980 | 24:1.17 | LABEL ×9 (−38 dBFS) | DORYLINAE typed |
| 57.600 | 25:1 | felt D2 (−22 dBFS) | address |
| 62.400 → 63.000 | 27:1 | digital silence | |

Mix: −14 LUFS integrated, true peak ≤ −1 dBTP. Effects sit 6 to 12 dB under music peaks. No keyboard clicks, whooshes,
risers, braams, stock hits or voices. The film has no Decision, so it has no "decision signed" cue. The motif notes at
12.000, 35.400 and 49.800 are its resolution cues: the brief's *Done when*, met, and ours.

---

## 6. End card (S12 · 57.600 → 63.000 · 25:1 → 27:2 · 5.4 s)

| | 16:9 | 4:5 |
|---|---|---|
| Ground | sky seed 1444, nebula 0.6 | same, 1080 × 1350 |
| Lockup | stacked lockup from the finished boot, mark 600 px wide, centred x 960, mark top y 150, lockup bottom y 768.0 | mark 640 px wide, centred x 540, mark top y 230, lockup bottom y 889.2 |
| Address | `Apply at dorylinae.net`, Special Elite 64 px, `text`, centred x 960, baseline 930 (x 586.9 → 1333.1; cap top 885.9, 117.9 px below the lockup: clear space 78 respected) | same text, 64 px, centred x 540, baseline 1060 (x 166.9 → 913.1; cap top 1015.9, 126.7 px below the lockup: clear space 83 respected); bottom safe 1222 |
| Anything else | nothing: no small print, no tag, no logo animation | nothing |

| t | bar:beat | Event |
|---|---|---|
| 57.600 | 25:1 | The address enters: 240 ms, opacity 0 → 1 and a rise of 12 px (4:5: 18 px). Lands 57.840. |
| 57.840 → 62.400 | | **Hold 4.56 s** (≥ 4 s). The lockup has been on screen since 57.000 (5.4 s complete). Scanlines static. |
| 62.400 | 27:1 | Hard cut to the bare sky. 0.6 s of ground so an autoplay loop never clips the address. |
| 63.000 | 27:2 | End. |

The address is worded exactly like the tag. It never appears during the boot (the Resmed rule, craft 1.12) and never
inside the clear space.

---

## 7. Real capture lines used, and every edit applied

Allowed edits (BRIEF): omit lines, crop, shorten ids, break long commands with ` \`, swap the scratchpad path, drop
versions and harness names. Nothing else was changed. No version string, path, port, hostname, harness name or GitHub
reference appears.

| Shot | File | Lines used (as shown) | Edits |
|---|---|---|---|
| S1, S3 | `capture/run/work/flaky-brief.md` (= the `brief` field of `request show --json` in `capture/20-grant.txt`) | `What: Find out why the payments integration test fails about one run in ten.` / `Why: It blocks merges twice a day and nobody owns it yet.` / `Done when: You name the cause, or rule out the three usual suspects.` | none in 16:9; 4:5 crops at the frame edge |
| S2, S5, S8 | capture convention (`capture/INDEX.md`) | prompts `maya$`, `theo$`; names `maya`, `theo` | none |
| S4 | `capture/15-offline-delivery.txt` | `maya$ agentnet request theo task \` / `    --title 'Track down the flaky payments test' \` / `    --brief-from-file flaky-brief.md \` / `    --idempotency-key demo-flaky-1` / `Queued normal task request r-97ae… to theo (team atlas)` / `  theo is offline, last seen 2026-10-08T13:41:37Z; it will be delivered when theo is back.` | one-line command broken at flags with ` \`; `r-97ae9a37feaf51fcf0c3c8246ae57be1` → `r-97ae…`; frame-edge crop in 4:5 and after the push |
| S5 | `capture/25-relay-log.txt` | `time=2026-10-08T13:41:40.228Z level=INFO msg=queued event=queue from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3… bytes=1444` / `time=2026-10-08T13:41:45.886Z level=INFO msg="delivered from queue" event=queue_flush peer=ZhbF0Hsy count=1` | `m-b2d3dc4566beac6d9631949d221555c0` → `m-b2d3…`; intervening lines omitted; left crop at the frame edge (16:9) or plate edge (4:5); 4:5 pans. 16:9 tracking −0.0125 em (characters unchanged) |
| S6 | `capture/15-offline-delivery.txt` | `theo$ agentnet inbox` / `ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE` / `r-d0d8…  maya  task  normal   2000  1m   Add queue settings to the staging runbook` / `r-97ae…  maya  task  normal   2000  0m   Track down the flaky payments test` | `r-d0d85221604ee5293d79869159039519` → `r-d0d8…`, `r-97ae…` as above; ID column re-padded after shortening (`capture/INDEX.md`); 4:5 frame-edge crop |
| S7 | `capture/21-quarantine-release.txt` | `theo$ agentnet result s-b309… --status pass \` / `    --summary 'Cause found: two tests share one ledger row' \` / `    --file flaky-finding.md` / `Submitted result for s-b309… (open)` | `s-b3098b5fd25dd5ec2aa2b78fe588fc03` → `s-b309…` (twice); command broken with ` \`; frame-edge crop after the push |
| S10 | `capture/02-request-help.txt` | field names `What:`, `Why:`, `Done when:`; placeholder `<how the other side knows it is finished>` | field names set above their values (sheet layout) |
| S3, S4, S6, S7 | time labels | `maya · 13:41 UTC` (capture/15: request created 13:41:40Z); `theo · 13:41 UTC` (capture/25 delivered 13:41:45.886Z; capture/16 starts 13:42:00Z); `theo · 13:43 UTC` (capture/21 header 13:43:54Z; capture/25 route theo → maya 13:43:54.425Z) | minute precision; these are the film's labels, not terminal lines |

**What happened between the shown lines** (omitted, allowed, and never contradicted):
- between S6 and S7: theo accepted (13:43:22); maya granted read access to `payments-ci`, approved by a human code
  in her daemon's window; theo read `logs/run-07.log`;
- after S8: the result was quarantined on maya's machine until she released it with an approval (capture/21).

No caption claims these steps did not happen, or that they are automatic (§8, risk 4).

---

## 8. Open risks for the build team

1. **The call needs a live page.** "Apply at dorylinae.net" promises an application on publish day (beta-facts §5). The
   owner must confirm the page is live before render. If it is not, the film must not ship.
2. **4:5 relay legibility.** The 121-character log line cannot be both whole and legible at 1080 px. The 4:5 cut reads
   each field at 36 px during the pans (when about 3.0 s, who about 1.5 s, how big about 0.9 s), then shows the whole
   line as 13 px texture with the three labels. The labels and caption carry the claim; the 16:9 master carries the
   full legible evidence. Fallback if the fact-checker objects: end on W2 (the `from= to=` window) instead of the
   overview, and keep the `when` and `how big` cards off-frame after their pans.
3. **Small text, listed honestly.** These sit under the 64 px story floor (16:9) by design:
   - the tag at 52 px (4:5: 48 px, which meets the floor);
   - the 16:9 log at 28 px with −0.0125 em tracking, and the cards at 40 px Special Elite;
   - the S6 inbox at 32 px;
   - the S2 names at 28 px.
   Each is above the 28 px texture floor and none is the only carrier of a claim. Phone-test at arm's length; if the tag
   fails, raise it to 56 px (its left edge then sits 61 px from the S3 caption).
4. **Omitted steps.** Between the inbox and the result the real run had an accept, a human-approved read grant and a
   fetch. After the return, the result was held until a human released it. The film shows the answer on theo's machine
   and the return trail ending at maya's machine, and never shows maya's agent reading it, so nothing implies it is
   automatic. A sharp viewer may still ask. The 90 to 120 s walkthrough should include the approval line (capture/24:
   `a-1a9f03 248765`, then `approved`, with the path swapped for `~/work/payments-ci`).
5. **Scripted capture.** The captures were driven by a script, not live agents. Captions say "your agent writes" and
   "their agent finds", which is the product's documented behaviour (PLAN:89, the agent snippet). Never caption anything
   "real agents, unedited".
6. **One machine shown as two.** Two daemons and a loopback relay ran on one host. The output is identical to a
   two-machine run, and the owner's two-machine run passed (product-facts §3.20, D29). A strict reviewer may want a
   re-capture across two machines.
7. **"it" in the hook.** "You paste it into a chat." relies on the brief plate being read as the thing pasted. Test with
   5 people who have never seen the product. Fallback, same timing: "You paste the brief into a chat." (6 words).
8. **Line length.** "They paste it into their coding agent." is 1418 px at 72 px in 16:9 (82 % of safe width,
   above the 60 % guideline). It is kept on one line because a break would weaken the actor. If review dislikes it, set
   S1's captions at 64 px (1261 px).
9. **Seeds and the nebula.** No seed is checked yet. Run the contact sheet first; mirror or re-seed as §3.8 allows.
   Text must never sit on the rose.
10. **The 21° edge.** It must be razor-hard: no motion blur, stroke or glow, or it reads as a stock wipe.
11. **The typed lines.** No caret and no sound on "Not what." or "Your team applies.". A caret or a click turns the one
    human moment into a gimmick.
12. **Engine changes needed.**
    - `ui.typed` and `ui.label` need `jitter: 0` passed through.
    - `ui.terminal` colours commands `text`; this script asks for `text-2`.
    - Terminal rows need a dim-to-40 % state.
    - S5 4:5 needs a strip transform with anchored cards.
    - The 21° clip-path needs building.
    - Per-format `FILM.cue` export is needed for the S5 label ticks.
13. **Special Elite glyphs.** The middle dot `·` and `…` exist in all three fonts (checked). If a renderer substitutes a
    font, it fails the font gate in `film.html`.
14. **Scarcity wording.** "The first wave opens with 10 teams." is the owner's own example wording, and the owner's team
    counts among the 10 (fact-check B18). If capacity changes before publish, change the numeral only.
15. **No security claim is made**, and the film never says "secure", "private", "encrypted" or "sealed". If the owner
    wants beta-facts §4's line "Not yet independently security-reviewed." it belongs on the landing page, not on the
    end card (the end card carries the address alone).
16. **S2 is a diagram, not a capture.** Its trail (maya to theo through the relay, both online) illustrates the
    definition. It shows no terminal output and claims only "your agent hands it to theirs". The hero request's real
    journey, with the offline wait, starts in S3.
17. **Length.** 63.0 s. If a platform needs 60.0 s, cut S9 to 4.2 s (facts enter at 41.4 s) and S10 to 5.4 s, giving
    60.6 s, and re-check every hold in §4.
