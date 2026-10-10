# SCRIPT v2: "The envelope" (final, build-ready)

Director's script for version 2 of the Dorylinae closed-beta film. It follows `BRIEF-v2.md`, keeps every rule in
`BRIEF.md`, and applies the three reviews of the first v2 draft (summary in section 13). Facts come from
`research/product-facts.md`, `research/fact-check.md` (which wins where they disagree) and `research/beta-facts.md`.
Every terminal line is from `capture/`. The v1 script (`SCRIPT.md`) and its shot files stay untouched; v2 is a new set
of shots.

**Runtime: 90 beats at 84 BPM = 64.286 s = 3857 frames (frames 0 → 3856).** The timeline is fitted to the recommended
track, "Places Unseen" by Lee Rosevere, edit B (`music/MUSIC.md`, `music/grid-recommended.json`). Four cuts. One
continuous scene.

Text widths were measured in Chromium with the engine's own fonts (`engine/fonts/`), with the tracking stated. If the
build measures a width more than 2 % different, keep the left edge (or the centre, for centred text) and the baseline,
and report it.

**Both owner decisions are made (final; `BRIEF-v2.md`, "Owner decisions after the v2 script (final)"), and the build
follows them:**

| # | Decision | Final (as built) | What it changed |
|---|---|---|---|
| O-1 | The card's footnote `No independent security review yet.` (section 5.3; risk 7) | **No footnote.** The disclosure goes in the invitation, the privacy note and the terms. | The line is deleted (K-4 is not in the film); the card ends under line 3 and is centred again (section 5.3). |
| O-2 | The note is signed by a person (section 5.4) | **`Yazan`**, exactly | The signature types 5 letters from G76 (last at 54.63 s); same place, same timing rule. |

---

## 0. How to read this file

- **Clock: beats of the track.** 84.000 BPM, 4/4. One beat = 5/7 s = 0.714286 s = 42.857 frames. One bar = 20/7 s =
  2.857143 s = 171.43 frames. Every 7 beats is exactly 5 s. `G` is the global beat index from frame 0 (G0 = 0 s), so
  **t = G × 5/7 s** and **frame = G × 300/7**. `bar:beat` is 1-indexed: bar = floor(G/4) + 1, beat = (G mod 4) + 1.
  `G16` = `5:1` = 11.429 s. Discrete events (cuts, one-frame ink, clock ticks) snap to the nearest whole frame, listed
  in section 3.
- **Grid marks.** `DOWNBEAT` = beat 1 of a bar. `beat` = on a beat. `½`, `¼`, `⅛` = subdivisions. The track's
  snare-like hits are on beats 2 and 4.
- **Music.** Film frame 0 is the downbeat of source bar 9 of the track (25.157 s into the file). The music is silent
  from 63.857 s (G89.4); the last 0.429 s of the film is the address in silence. Section 2.
- **Formats.** 16:9 is 1920 × 1080 (title safe x 96 → 1824, y 54 → 1026). 4:5 is 1080 × 1350 (safe x 64 → 1016,
  y 64 → 1222). Each section is laid out separately for each format.
- **World and screen.** Sections II and IV are a world seen by a camera. **World coordinates are the screen
  coordinates of the camera state CAM-R0** (16:9: camera centre c = (960, 540), s = 1; 4:5: c = (540, 675), s = 1).
  The engine rule applies: screen = (W/2, H/2) + s × (world − c). So in 16:9 at CAM-M (c = (−320, 540), s = 1) a world
  point shows at screen (x + 1280, y). Captions, the card, the note and the end card are screen elements.
- **Text positions** are the left edge and the baseline, or the centre and the baseline for centred text.
- **Ease.** UI elements (captions, chips, pointers, the clock) use the brand ease `cubic-bezier(0.2, 0.7, 0.2, 1)` and
  the brand's 120 / 240 ms fades. **Paper never fades or scales**: it slides, folds, rotates on a hinge and is hidden
  by other objects. Paper slides, envelope travel and every camera move use `FILM.CAM` (the sweep curve
  `(0.4, 0, 0.6, 1)`, peak speed 1.667 × average). **Ink appears in one frame** (seal, label, letter lines, note
  lines, card lines). Linear only for the trail march and tail fades. All cameras use `FILM.worldCamStable`.
- **Speed rule.** Anything that peaks above 19 px per frame gets a `FILM.blur(t0, t1)` window (section 4.10).
- **Brand tokens only.** Dark tokens on the ground, light-theme tokens on paper (section 4.1). No shadows, no glass, no
  gradients between brand colours; the sky is the only gradient. Plates are opaque.

---

## 1. The film in one page

### 1.1 Logline

You paste a brief into a chat, and your teammate pastes it into their coding agent. With Dorylinae, your agent hands
it to theirs: your machine seals the brief in an envelope. The relay works like a post office: it reads the outside
(who, to whom, when and how big) and it cannot open the envelope. Theo is offline, so the envelope waits at the
counter while the clock runs. Theo comes back on the music's return, and his machine opens it: the same brief, in his
inbox. Theo's agent does the work, and the answer comes back, sealed the same way. Then a card names what you saw, and
we send you an envelope of our own: a short note, signed by a person, that ends with where to apply.

### 1.2 Structure

| # | Section | Beats (G) | Bars | Seconds | Frames | Into it |
|---|---|---|---|---|---|---|
| I | Cold open: the pain | G0 → G8 | 1:1 → 3:1 | 0.000 → 5.714 | 0 → 343 | film start; music already playing (filtered intro) |
| II | The scene: maya, the relay, theo | G8 → G56 | 3:1 → 15:1 | 5.714 → 40.000 | 343 → 2400 | **cut 1: 21° cut**, DOWNBEAT |
| III | The card | G56 → G65 | 15:1 → 17:2 | 40.000 → 46.429 | 2400 → 2786 | **cut 2: hard cut**, DOWNBEAT |
| IV | The invitation | G65 → G80 | 17:2 → 21:1 | 46.429 → 57.143 | 2786 → 3428 | **cut 3: hard cut**, beat (snare) |
| V | Boot and address | G80 → G90 | 21:1 → 23:3 | 57.143 → 64.286 | 3428 → 3857 | **cut 4: hard cut**, DOWNBEAT (on frame 3428, see section 3) |

Four cuts. Inside the scene there are no cuts, only camera moves (section 5.2.3). Midpoint 32.14 s (G45): "Theo's agent
does the work."

### 1.3 What changed from v1, and why

| Owner said about v1 | v2 answer |
|---|---|
| "I couldn't look at both the text and what is happening, because the whole image changed." | One scene, no cuts inside it. One caption slot under the envelope's path, changing in place. Never two captions at once, never two text events in the same half beat (contiguity: Mayer; Ginns 2006). The proof of each caption sits next to it: theo's chip beside the envelope at the relay, the answer sheet at theo's dock. |
| "It didn't connect with me." "The relay is like the post office." | The post-office analogy is the spine, in words and picture, with its limits stated (Feiereisen et al. 2008; Moreau et al. 2001). The relay is a counter with a sorting rack; the address label is a form whose fields are what the relay's own log shows. |
| "The material quality, the details, weren't there." | Real paper objects built from brand materials: a wallet envelope (two paper tones, a straight flap, side seams, a seal tab, an address form), letters that come out of the machines, fold on a hinge and go into envelopes, a post-office rack made of plates. Type at ≥ 36 px (16:9) / ≥ 32 px (4:5) when read. |
| "One card revealing our best features (e2e encryption)." | Section III: `End-to-end encrypted.` as the card's display line, two lines under it, and an honest footnote (O-1). |
| "I didn't feel invited." | Section IV: we send you an envelope `FROM Dorylinae` / `TO your team`; it opens to a note that says "like yours", asks you something, and is signed by a person (O-2), ending with where to apply. |
| Music "calm but not great" | A human-composed track (CC BY 4.0), with the film cut to its bars: the seal on its lift, the wait on its breakdown, theo's return on its return. |

### 1.4 Product ideas

Three ideas, each shown before it is named: (1) your agent hands work to your teammate's agent; (2) the relay carries a
sealed envelope, reads its outside and cannot open it (end-to-end encryption); (3) when your teammate is offline it
waits, and it arrives when they are back. The card adds one fact the scene does not show
(you approve what their agent can read; risk 5).

### 1.5 The analogy, exactly

| Post office | Dorylinae (source) | Shown as |
|---|---|---|
| You write a letter | Your agent writes the brief (product-facts §3.5) | the white letter comes out of maya's machine and its lines appear |
| You seal it at home | Your daemon signs it and seals it to theo's key before it leaves (§3.10) | the letter goes into maya's machine, comes back inside an envelope, the flap closes, the seal |
| The address on the outside | The relay sees who sends to whom, when and how big (§3.10; `capture/25`) | the form label: FROM `QZ_vPMFN`, TO `ZhbF0Hsy`, TIME `13:41:40 UTC`, SIZE `1,444 bytes` |
| The post office cannot read the letter | The relay carries ciphertext; it cannot see titles, briefs or results (§3.10) | the counter never opens it; "It cannot open the envelope." |
| It holds mail for someone who is away | The relay queues the envelope while the peer is offline and delivers it on reconnect (§3.9; `capture/25` queued 13:41:40, delivered 13:41:45) | the envelope waits on the counter; the counter's clock runs |
| Only the addressee opens it | Only theo's machine holds the key that opens it (§1a, §3.10) | the envelope goes into theo's machine and the brief comes out |

**Where the analogy stops** (said in the copy only as far as it is true; the rest is in risk 1): the relay sees senders
and recipients as keys (its own log prints 8-character prefixes); **on the hosted relay each key is bound to an account,
so "who" there is a named person**. It also sees a random message id, the generic type `mail`, when each machine is
connected, and client IP addresses (kept up to 14 days). It holds an envelope for up to 7 days, not forever (not
claimed). Once opened, the brief is stored readable on both machines (end-to-end covers transit and the relay, not your
disk; fact-check C3). The film never says the relay "knows nothing", never calls the label anonymous, and never says
"keys, not names".

---

## 2. Music fit

**Track:** "Places Unseen" by Lee Rosevere, CC BY 4.0, **edit B** (`music/edits/01-places-unseen-edit-B-63s.wav`,
63.857 s): source bars 9–12 | 33–36 | 55–64 | 81–84, plus a 1.0 s tail, fade 59.571 → 63.857. Film t = edit t.
Credit line (post text and description only, never on screen): `Music: "Places Unseen" by Lee Rosevere
(freemusicarchive.org/music/lee-rosevere), licensed under CC BY 4.0 (creativecommons.org/licenses/by/4.0/). Edited for
length.`

| Film bar | G | t (s) | Frame | Music (edit B) | Picture anchored to it |
|---|---|---|---|---|---|
| 1 | 0 | 0.000 | 0 | intro, filtered (source 9) | cold open, frame 0 |
| 3 | 8 | 5.714 | 343 | intro builds (source 11) | **21° cut** into the scene |
| 4 | 12 | 8.571 | 514 | | the letter folds |
| **5** | **16** | **11.429** | **686** | **LIFT: filter opens, +4 dB (splice 12→33)** | **the seal** |
| 6 | 20 | 14.286 | 857 | groove | the envelope lands on the counter |
| 7 | 24 | 17.143 | 1029 | | pointer on `TIME` ("when,") |
| 8 | 28 | 20.000 | 1200 | | "It cannot open the envelope." |
| **9** | **32** | **22.857** | **1371** | **BREAKDOWN, low-passed (splice 36→55)** | **"Theo is offline. The envelope waits."** |
| 10 | 36 | 25.714 | 1543 | riser from ~28.2 s | the wait; the clock runs in real time |
| **11** | **40** | **28.571** | **1714** | **RETURN, full band (the track's own 56→57)** | **theo's ring fills; "Theo is back. His machine opens it."** |
| 12 | 44 | 31.429 | 1886 | groove | the brief comes out of theo's machine |
| 13 | 48 | 34.286 | 2057 | | the pointer under the answer |
| 14 | 52 | 37.143 | 2229 | | the answer's seal |
| 15 | 56 | 40.000 | 2400 | | **cut 2: the card** |
| 17 | 64 | 45.714 | 2743 | | (card holds; cut 3 at G65 = 46.429, beat 2) |
| **19** | **72** | **51.429** | **3086** | **outro begins (splice 64→81)** | note: "That decides what we change first." |
| 20 | 76 | 54.286 | 3257 | | the signature types |
| **21** | **80** | **57.143** | **3428** | outro | **cut 4: the boot** (frame 3428 = 57.133 s, see section 3) |
| 21:4.4 | 83.4 | 59.571 | 3574 | fade begins | |
| **22** | **84** | **60.000** | **3600** | | **"Apply at dorylinae.net" enters** |
| | 89.4 | 63.857 | 3831 | silence | |
| | 90 | 64.286 | 3857 | | end (last frame 3856) |

The cut at 22.857 s (bar 9) is the one splice that is not one of the track's own transitions; the picture motivates it
(theo is offline) and no hard picture change lands there, only a caption change. If the owner dislikes the track after
listening, the fallback order is in `MUSIC.md` §7; a 100 BPM track (candidate 4, or v1's code score) needs the refit in
section 12.4.

**Engine clock.** `lib/core.js` hard-codes 100 BPM (`BEAT = 0.6`, `BAR = 2.4`) and `lib/` is off limits. v2 defines its
own clock in `shots-v2/common-v2.js`: `const BEAT = 5 / 7, BAR = 20 / 7, T = (G) => G * BEAT`. Every v2 shot uses `T(G)`.
The presence pulse runs on the bar (2.857 s), not on the brand's 2.4 s.

---

## 3. Master timeline

Every event, both formats unless marked. "Frame" is the frame on which a discrete event happens.

| G | bar:beat | t (s) | Frame | Grid | Event |
|---|---|---|---|---|---|
| 0 | 1:1 | 0.000 | 0 | DOWNBEAT | Plate YOU with the brief; caption line 1 `You paste the brief into a chat.` |
| 2 | 1:3 | 1.429 | 86 | beat | Plate THEY locks in; caption line 2 `They paste it into their coding agent.` |
| 7.5 | 2:4.5 | 5.357 | 321 | ½ | Cold-open caption out (ends here); **S-1** enters, drawn above the coming wipe |
| 8 | 3:1 | 5.714 | 343 | DOWNBEAT | **Cut 1 (21°)** to the scene at CAM-M; maya's machine, her chip, an empty dock |
| 8.5 | 3:1.5 | 6.071 | 364 | ½ | The letter slides out of maya's machine (0.90 s) |
| 9.75 → 10.625 | 3:2.75 → 3:3.625 | 6.964 → 7.589 | 418 … 455 | ⅛ | The 8 brief lines appear, one every ⅛ beat |
| 11 | 3:4 | 7.857 | 471 | beat | The agent's command lands on maya's terminal |
| 12 | 4:1 | 8.571 | 514 | DOWNBEAT | The letter folds in half (hinge, 0.28 s) |
| 12.5 | 4:1.5 | 8.929 | 536 | ½ | The folded letter slides up into maya's machine (0.45 s) |
| 13 | 4:2 | 9.286 | 557 | beat | **S-2** `Your machine seals it.` |
| 13.25 | 4:2.25 | 9.464 | 568 | ¼ | The open envelope slides out of maya's machine with the letter inside (0.75 s) |
| 14.5 | 4:3.5 | 10.357 | 621 | ½ | The letter drops the last 24 px into the envelope |
| 15.25 | 4:4.25 | 10.893 | 654 | ¼ | The address label is printed (one frame) |
| ≈15.6 | | 11.150 | 669 → 685 | | The flap closes (rotateX, 17 frames) |
| **16** | **5:1** | **11.429** | **686** | **DOWNBEAT (LIFT)** | **The seal** (one frame) |
| 16.25 | 5:1.25 | 11.607 | 696 | ¼ | Output `Queued normal task request r-97ae… to theo (team atlas)` |
| 16.75 | 5:1.75 | 11.964 | 718 | ¾ | The envelope lifts and travels to the relay; trail; camera move 1. **S-3** `The relay works like a post office.` |
| **20** | **6:1** | **14.286** | **857** | **DOWNBEAT** | **It lands on the counter.** Trail tail fades to G22 |
| 20.25 → 21.75 | | 14.464 → 15.536 | | | 16:9 push CAM-R0 → CAM-R+; 4:5 tilt +100 |
| 21.5 | 6:2.5 | 15.357 | 921 | ½ | **S-4** line 1 `It reads the outside:` |
| 22, 23, 24, 25 | 6:3 → 7:2 | 15.714 → 17.857 | 943, 986, 1029, 1071 | beat | Line 2 builds `who,` / `to whom,` / `when,` / `how big.`; the pointer steps FROM → TO → TIME → SIZE |
| 28 | 8:1 | 20.000 | 1200 | DOWNBEAT | **S-5** `It cannot open the envelope.` Pointer under the seal tab at G28.5 |
| 29 → 31 | 8:2 → 8:4 | 20.714 → 22.143 | | beat | Camera settles back to CAM-R0 |
| **32** | **9:1** | **22.857** | **1371** | **DOWNBEAT (BREAKDOWN)** | **S-6** `Theo is offline. The envelope waits.` 16:9 drift / 4:5 tilt over the wait |
| 33 | 9:2 | 23.571 | 1414 | beat | The counter's clock enters: `NOW 13:41:40 UTC` |
| | | 24.571, 25.571, 26.571, 27.571 | 1474, 1534, 1594, 1654 | (real seconds) | Clock `:41`, `:42`, `:43`, `:44` |
| **40** | **11:1** | **28.571** | **1714** | **DOWNBEAT (RETURN)** | Clock `:45`. **Theo is back:** his ring fills. **S-7** `Theo is back. His machine opens it.` |
| 40.25 | 11:1.25 | 28.750 | 1725 | ¼ | The envelope lifts and travels to theo's dock; camera move 4 |
| 43 | 11:4 | 30.714 | 1843 | beat | It sets down at theo's dock and slides into his machine (0.70 s; up in 16:9, down in 4:5) |
| 44 | 12:1 | 31.429 | 1886 | DOWNBEAT | The brief slides out of theo's machine, unfolded (0.90 s) |
| 44.5 | 12:1.5 | 31.786 | 1907 | ½ | theo's inbox lands on his terminal |
| 45 | 12:2 | 32.143 | 1929 | beat | **S-8** `Theo’s agent does the work.` |
| 45.75 | 12:2.75 | 32.679 | 1961 | ¾ | The result command lands on theo's terminal |
| 46.25 | 12:3.25 | 33.036 | 1982 | ¼ | theo's brief slides back into his machine (0.45 s; in by 33.486 s): his machine keeps it |
| 46.875 | 12:3.875 | 33.482 | 2009 | ⅛ | The answer sheet slides out of theo's machine (0.60 s), once the brief has gone (two sheets never cross) |
| 48 | 13:1 | 34.286 | 2057 | DOWNBEAT | Pointer under `Cause found: two tests share` / `one ledger row` |
| 49 | 13:2 | 35.000 | 2100 | beat | **S-9** `The answer comes back, sealed the same way.` |
| 49.75 | 13:2.75 | 35.536 | 2132 | ¾ | The answer sheet folds |
| 50.25 | 13:3.25 | 35.893 | 2154 | ¼ | It slides into theo's machine (0.40 s; up in 16:9, down in 4:5) |
| 50.75 | 13:3.75 | 36.250 | 2175 | ¾ | The answer envelope slides out of theo's machine, flap open (0.75 s); the flap closes, frames 2212 → 2228 |
| **52** | **14:1** | **37.143** | **2229** | **DOWNBEAT** | **The answer's seal and label** (one frame) |
| 52.5 | 14:1.5 | 37.500 | 2250 | ½ | It lifts and travels back to maya; camera move 5 (track with a dip to s 0.7) |
| 55 | 14:4 | 39.286 | 2357 | beat | It sets down at maya's dock; her arrival pulse |
| **56** | **15:1** | **40.000** | **2400** | **DOWNBEAT** | **Cut 2** to the card: `End-to-end encrypted.` |
| 57, 58 | 15:2, 15:3 | 40.714, 41.429 | 2443, 2486 | beat | Card lines 2 and 3 |
| 59.5 | 15:4.5 | 42.500 | 2550 | ½ | (no event: the card's footnote is cut, O-1) |
| 65 | 17:2 | 46.429 | 2786 | beat | **Cut 3** to the invitation: our envelope travels in |
| 67.5 | 17:4.5 | 48.214 | 2893 | ½ | A slit opens along its top fold |
| 68 | 18:1 | 48.571 | 2914 | DOWNBEAT | The letter rises out; the envelope falls away |
| 69 | 18:2 | 49.286 | 2957 | beat | The letter unfolds |
| 69.5, 71, 72, 73.5, 75 | | 49.643 → 53.571 | 2979, 3043, 3086, 3150, 3214 | | Note lines (one frame each) |
| 76 | 20:1 | 54.286 | 3257 | DOWNBEAT | The signature types (85 ms a letter) |
| **80** | **21:1** | **57.143** | **3428** | **DOWNBEAT** | **Cut 4** to the bare sky; the boot plays unchanged (3.6 s). The cut is on frame 3428 (57.133 s), the frame G80 falls in, an even frame so the 30 fps delivery's frame pairs (2k, 2k + 1) never blend sections IV and V (director's notes, v2 QA; sec4-invite.js / sec5-end.js CUT4); frame 3427 is the invitation's last |
| **84** | **22:1** | **60.000** | **3600** | **DOWNBEAT** | `Apply at dorylinae.net` enters, held to the last frame |
| 90 | 23:3 | 64.286 | 3857 | | End |

---

## 4. Materials and build specification

### 4.1 Colour tokens

Dark ground (DESIGN.md dark theme):

| Token | Hex | Used for |
|---|---|---|
| `bg` | `#07060F` | Cold open ground; terminal blocks; pigeonhole recesses; behind every sky |
| `surface` | `#0E0C1F` | Machine plates, cold-open plates, the relay's rack wall |
| `surface-2` | `#1A1733` | The relay counter, presence chips |
| `text` | `#EFE9FB` | Captions, chip names, the address |
| `text-2` | `#A79FC4` | Commands, cold-open brief, clock time |
| `text-3` | `#8C85AB` | Terminal output, chip state words, the offline ring, clock "NOW" |
| `brand` (amethyst) | `#A678E6` | Prompts `maya$` `theo$`; the word "Dorylinae" in S-1 |
| `accent` (pheromone) | `#D653E1` | Trails on the ground |
| `online` | `#5CE6C8` | Solid presence dot and its halo |

On paper (DESIGN.md light theme):

| Token | Hex | Used for |
|---|---|---|
| label stock (light `bg`) | `#F6F3FB` | The envelope flaps, seal tabs, address forms, the relay sign, the card |
| paper white (light `surface`) | `#FFFFFF` | The letters, the answer sheet |
| light `surface-2` | `#ECE6F7` | Envelope bodies; the inside face of an open flap |
| ink (light `text`) | `#17122B` | Text on paper; inset hairlines at 55 % (the brand's specimen-label rule) |
| ink meta (light `text-3`) | `#665E88` | Letter headers, form field captions, the card footnote |
| brand ink (light `brand`) | `#6B3FB8` | Field names `What:` `Why:` `Done when:` (and the mark files' own ink) |
| accent ink (light `accent`) | `#A021AC` | Pointers on paper (label fields, the seal tab, the answer) |
| hairline (light `line-strong`) | `#877CAE` | The flap's edge |
| crease (light `line`) | `#D5CBE8` | Envelope side seams, letter rules, the letter's crease after it has been folded |

Engine `C` already has `stock`, `ink`, `inkMeta`. Add the others in `common-v2.js`, not in `lib/`.

**Colour arc.** The cold open is monochrome (`bg`, `surface`, `text`, `text-2`). Amethyst, teal, paper and pheromone
arrive with the cut to the scene.

**Paper tooth (optional, needs the brand owner's yes).** A static, seeded fibre noise of at most 2 % luminance on paper
surfaces, never animated. Default: off (flat paper).

### 4.2 Type roles

Sizes are screen px at s = 1. In 16:9 the stage is built at s = 1, so world size = screen size at CAM-M, CAM-R0 and
CAM-T; at CAM-R+ (s 1.3) the label reads 30 % larger.

| Role | Face | 16:9 | 4:5 | Colour |
|---|---|---|---|---|
| CAPTION | Special Elite 400, tracking −0.01 em | 64 px, centred | 48 px, centred | `text` |
| TERMINAL | IBM Plex Mono 400 | 36 px, pitch 46 | 32 px, pitch 46 | prompt `brand`, command `text-2`, output `text-3` |
| CHIP | Plex Mono 500 | 36 px | 32 px | name `text`, state `text-3` |
| LETTER / ANSWER text | Plex Mono 400 | 36 px | 32 px | values ink, field names brand ink |
| LETTER header | Plex Mono 400 | 26 px | 24 px | ink meta |
| FORM field caption | Plex Mono 500, uppercase, tracking 0.08 em (brand `label` style) | 20 px (26 at CAM-R+) | 20 px | ink meta |
| FORM value | Plex Mono 500 | 34 px (44.2 at CAM-R+) | 32 px | ink |
| RELAY SIGN | Special Elite 400 on a specimen label | 40 px | 36 px | ink on label stock |
| CLOCK | Plex Mono 500, uppercase, tracking 0.08 em | 32 px | 28 px | `NOW` `text-3`, time `text-2` |
| CARD display | Special Elite 400, −0.01 em | 88 px | 64 px | ink |
| CARD lines | Plex Sans 500 | 64 px | 40 px | ink |
| CARD footnote | Plex Sans 500 | 40 px | 32 px | ink meta |
| OUR FORM (IV) | caption Plex Mono 500 caps 0.08 em / value Plex Mono 500 | 26 / 52 px | 24 / 48 px | ink meta / ink |
| NOTE | Plex Sans 500 | 44 px (46.6 after the push) | 40 px | ink |
| SIGNATURE | Special Elite 400 | 56 px | 48 px | ink |
| ADDRESS (end) | Special Elite 400, −0.01 em | 64 px | 64 px | `text` |

Apostrophes in captions, the card and the note are typographic (`’`). Terminal lines keep the capture's straight
quotes. IDs and data (keys, times, sizes) are set in Plex Mono, never Special Elite (whose zero reads as the letter O).
Special Elite is used only for single display lines, the sign and the signature.

**Labels and the size floor (final polish).** The floor for type that carries meaning (≥ 36 px in 16:9, ≥ 32 px in 4:5,
BRIEF-v2 "Scale") applies to lines that are read as sentences or data: terminal lines, letter and answer text, form
values, chips, the clock, captions, the card and the note. Small-caps **labels** are exempt: the address form's field
captions `FROM` `TO` `TIME` `SIZE` (Plex Mono 500 caps, 22 / 24 px as built) and the sheet headers `flaky-brief.md` and
`status pass` (Plex Mono 400, 28 / 26 px as built). They name the value or the document beside them, as a printed form's
captions do, and are read with it; they stay at label size so the values they label keep the hierarchy.

### 4.3 The caption slot

One slot for the cold open and the scene. The card and the invitation have no caption.

| | 16:9 | 4:5 |
|---|---|---|
| Box | centred on x 960; x 210 → 1710 (S-1 alone is 1498 px), y 840 → 990 | centred on x 540; x 64 → 1016, y 1095 → 1212 |
| Size | Special Elite 64 px, −0.01 em, `text`, max line 1500 | 48 px, −0.01 em, `text`, max line 952 |
| One line | baseline 970 (cap top 926, descenders to 985) | baseline 1196 (cap top 1162) |
| Two lines | baselines 894 / 970 (cap top 850) | baselines 1138 / 1196 (cap top 1104) |
| Two lines used | cold open; S-4 (at CAM-R+) | cold open, S-1, S-4, S-9 |
| Clearance | **Nothing of the stage enters the text block** (cap top to descenders). Closest edges, one line: the letter's bottom at CAM-M / CAM-T, 863 (63 px above 926); the counter at CAM-R0, 859 (67 px). Two lines: the counter at CAM-R+, 794.8 (55 px above 850). The box itself may overlap a dark plate edge at the frame's right or left side at CAM-M / CAM-T; no glyph does. | At CAM-M maya's chip ends at 1060 (44 px above the two-line cap top); at CAM-R0 theo's chip ends at 930; at CAM-T theo's plate ends at 966. During moves dark plates pass behind the box; the caption stays on top. |
| Sky under it | **A de-dusted band**: within 40 px of any caption glyph, stars no larger than 1 px and no brighter than 30 %. Never on the nebula's rose (contact-sheet every caption). | same |

**Change grammar.** At slot time T the old line fades out over 120 ms (opacity only), ending at T; the new line enters
at T over 240 ms (opacity 0 → 1, rise 12 px in 16:9, 18 px in 4:5), brand ease. Bottom-anchored. Never two captions at
once.

**Building line (S-4 only).** Line 1 enters with the change grammar. Line 2 is laid out once, centred, as four spans
(`who,` `to whom,` `when,` `how big.`); each span fades 0 → 1 over 120 ms on its beat, in place (no re-centring, no
rise).

**Hold rule.** Landed = 240 ms after entry. (S-1 is drawn above the 21° wipe, so it is readable from its entry like any caption.) Hold = landed
→ start of the out-fade (or the cut). Floor: max(1.5, 0.5 + 0.35 × words). Key claim: max(2.0, 0.5 + 0.6 × words). All
holds are in section 6.

### 4.4 The envelope (the hero object)

The back of a real wallet envelope: two paper tones, a straight flap, side seams, a seal tab, an address form. Three
envelopes share the construction: the brief (maya → theo), the answer (theo → maya) and ours (Dorylinae → you).

**Layers, back to front:** far panel (the open flap's inside shows only while open) → the letter, when inside → body
(near panel, `#ECE6F7`) → side seams → address form → closed flap (`#F6F3FB`) with its edge hairline → seal tab.

Scene envelopes (local coordinates, origin top-left):

| Part | 16:9 (760 × 392) | 4:5 (700 × 372) | Material |
|---|---|---|---|
| Body | 760 × 392, radius 4 | 700 × 372, radius 6 | light `surface-2` `#ECE6F7`; no border, no shadow |
| Flap (closed) | full width, y 0 → 118 (30 %) | y 0 → 112 | label stock `#F6F3FB`; its bottom edge is a 1.6 / 2 px `#877CAE` hairline at y 118 / 112. Two flat paper tones, no gradient |
| Side seams | `#D5CBE8` 1.6 px: (0, 392) → (68, 324) and (760, 392) → (692, 324) | 2 px: (0, 372) → (64, 308), (700, 372) → (636, 308) | they never reach the form |
| Seal tab | a specimen-label tab straddling the flap edge: 104 × 94 at x 328 → 432, y 71 → 165; label stock, inset hairline 5 px in (ink 55 %, 1.6), radius 3 | 104 × 94 at x 298 → 402, y 65 → 159; inset 5, 2 px | |
| Seal mark | `compact-on-light.svg`, 73 wide (61.4 tall), centred on (380, 118): 73 px at s 1, 95 px at CAM-R+ (inside the compact 40–96 range) | 73 wide, centred on (350, 112) | the file's own ink `#6B3FB8`; never recoloured, scaled apart, rotated or animated. Clear space to the tab's hairline ≥ 10.5 px (13 % of 73 = 9.5) |
| Address form | label stock, inset hairline 5 px in, ink 55 %, radius 3: x 100 → 660, y 197 → 355 (32 px of air under the tab, 37 px bottom margin) | x 86 → 614, y 191 → 336 (32 / 36) | a 2 × 2 form |
| Form text | captions at x 124 / 409, baselines 228 and 296; values at x 124 / 409, baselines 260 and 328 | captions at x 110 / 376, baselines 220 and 282; values baselines 250 and 312 | captions FROM, TO (row 1) / TIME, SIZE (row 2): Plex Mono 500 20 px caps 0.08 em ink meta; values Plex Mono 500 34 / 32 px ink |

**What the forms say** (the relay's own log, `capture/25-relay-log.txt`; section 7):

```
the brief                        the answer
FROM      TO                     FROM      TO
QZ_vPMFN  ZhbF0Hsy               ZhbF0Hsy  QZ_vPMFN
TIME          SIZE               TIME          SIZE
13:41:40 UTC  1,444 bytes        13:43:54 UTC  1,328 bytes
```

The captions map the caption words "who, to whom, when, how big" one to one and give the pointer four clear targets.
No postmark (cut: the post office stamps postmarks, and the time is already on the form). **The inside never shows in
transit.**

**Our envelope (section IV)**, same construction, larger:

| Part | 16:9 (1000 × 460, at x 460 → 1460, y 260 → 720) | 4:5 (900 × 414, at x 90 → 990, y 420 → 834) |
|---|---|---|
| Flap | y 0 → 138, edge hairline 1.6 | y 0 → 124, 2 px |
| Seams | (0, 460) → (90, 370), (1000, 460) → (910, 370) | (0, 414) → (82, 332), (900, 414) → (818, 332) |
| Seal tab | 142 × 128 at x 429 → 571, y 74 → 202 | 140 × 128 at x 380 → 520, y 60 → 188 |
| Seal mark | `mark-on-light.svg` 104 wide (87 tall), centred (500, 138) | `mark-on-light.svg` 100 wide, centred (450, 124) |
| Form | one row, x 150 → 850, y 232 → 360: captions FROM / TO at x 186 / 531, baseline 275 (26 px); values `Dorylinae` / `your team` baseline 330 (52 px) | x 130 → 770, y 216 → 336: captions at x 162 / 477, baseline 256 (24 px); values baseline 307 (48 px) |

The same grammar as the scene's label: the outside the relay saw as keys shows people when we write. Seal: the full
mark at 104 / 100 px (96–108 range); clear space ≥ 14 px each side (as built, the full mark's ink to the tab's inset
hairline: 16:9 14.1 / 14.1 / 15.1 / 14.1 px left / right / top / bottom, 4:5 14.4 / 15.4 / 14.4 / 15.4, world px; the tab
was 142 × 124 / 136 × 120 until the final review, 12.3 / 10.6 px at its tightest).

**Animation states (every envelope)**

| State | How |
|---|---|
| Comes out of a machine (open) | It slides out from under the plate's bottom edge (layered behind the plate), `FILM.CAM`, 0.75 s, until its top fold is 100 px below the slot. Its flap is open: rotated 180° about the top fold, pointing up, inside face `#ECE6F7`; the flap's top 18 px (16:9) / 12 px (4:5) stays tucked behind the plate. The folded letter rides inside, its top edge 24 px above the top fold (in front of the open flap's inside, behind the body). Exception: in 4:5 the answer rises out of theo's slot (below his dock) already closed. |
| Letter drops in | The letter moves down 24 px in 120 ms (brand ease) and is hidden by the body. |
| Flap closes | A paper panel rotating about the top fold: CSS `rotateX` from 180° to 0°, `perspective: 1600px`, 17 frames (283 ms), brand ease. Its fill switches from `#ECE6F7` (inside) to `#F6F3FB` (outside) at 90°, where it is edge-on; while > 90° it is layered behind the plate, below 90° in front of the body. Flat fills only. |
| Ink (label, seal) | Present from one frame to the next: no fade, no scale, no move. The seal lands on the frame after the flap settles. The mark is never removed, split or animated afterwards. |
| Lift / set down | Before it leaves a dock or the counter it rises 8 px in 120 ms (brand ease); in the last 120 ms of a trip it settles 8 px down onto the counter or dock (16:9 only: in 4:5 the lane is vertical, so there is no lift). |
| Travel | Translate along the lane with `FILM.CAM`. No rotation, no scale, no tilt. |
| Goes into a machine | It slides up behind the plate's bottom edge (16:9) / down behind theo's plate's top edge (4:5), `FILM.CAM`, 0.70 s. |
| Opened by us (IV only) | A slit opens along the top fold (a 2 px `#D5CBE8` gap widening from the centre to the full width in 120 ms); the flap and seal stay whole. Then the letter rises out and the envelope falls away (section 5.4). |

### 4.5 The letters (paper that comes out of the machines)

The brief is shown as what it is, a document: `flaky-brief.md`, word for word, set on a white sheet. It is not terminal
output, so wrapping it is not a re-flow of output (risk 4).

**The brief** (white `#FFFFFF`, radius 4 / 6):

| | 16:9 (724 × 560) | 4:5 (660 × 520) |
|---|---|---|
| Header | `flaky-brief.md`, Plex Mono 26 ink meta, x 49, baseline 76 | 24 px, x 51, baseline 70 |
| Rule | `#D5CBE8` 1.6 px at y 94, x 49 → 675 | 2 px at y 86, x 51 → 609 |
| Lines (x 49 / 51) | What: 142, 184, 226 · Why: 318, 360 · Done when: 414, 456, 498 | What: 132, 170, 208 · Why: 296, 334 · Done when: 382, 420, 458 |
| Margins | sides 49 (widest line 626 px), top 56, bottom 54 | sides 51 (widest 557), bottom 55 |
| Fold | in half, about y 280: 46 px of air above (226 + 8), 11 px below the crease to `Why:`'s cap top | about y 260: 45 / 12 px |
| Folded | 724 × 280 (fits the 760 × 392 envelope) | 660 × 260 (fits 700 × 372) |

```
What: Find out why the
payments integration test
fails about one run in ten.
Why: It blocks merges twice a
day and nobody owns it yet.
Done when: You name the
cause, or rule out the three
usual suspects.
```

Field names in brand ink, values in ink. Wrap at 29 characters, field by field. **The crease hairline (`#D5CBE8`,
1 px) is drawn only after the letter has been folded once**, so maya's letter shows none and theo's (the same paper,
after transit) shows it, in the gap between `What` and `Why`.

**Fold (280 ms, brand ease):** the half farther from the slot rotates about the crease (`rotateX`, perspective 1600 px)
and lies over the half next to the slot, showing its blank white back; its face switches from text to blank at 90°. So
the folded block always sits against the slot it is about to go into: at maya's machine (both formats) and at theo's in
16:9 the bottom half folds up; at theo's in 4:5 (slot below) the answer sheet's top half folds down. **Unfold** is the
exact reverse. **Lines appear** whole, in one frame each (ink).

**The answer sheet** (white, 16:9 724 × 340 / 4:5 660 × 320): header `status pass` (Plex Mono 26 / 24, ink meta,
baseline 76 / 70), rule at 94 / 86, then the summary in Plex Mono 36 / 32 ink:
`Cause found: two tests share` (baseline 222 / 206) and `one ledger row` (264 / 244). Folds in half about y 170 / 160
(25 / 22 px clear of the summary). Source: `capture/21` (`--status pass --summary 'Cause found: two tests share one
ledger row'`; maya's `session` prints `result: status pass` / `summary: Cause found: …`).

**Our letter (section IV)**, white, folded in half: 16:9 960 × 820, crease at y 410; 4:5 880 × 760, crease at y 380.
Layout in section 5.4.

### 4.6 The machines

Each machine is two plates: an opaque `surface` plate and, inset in it, the brand's terminal block (always `#07060F`).
The plate's bottom edge is the machine's **slot**: paper comes out of it and goes into it, always layered behind the
plate. (16:9: both machines are above the lane. 4:5: maya's is above her dock, theo's below his, so theo's slot is his
plate's top edge.)

| | 16:9 (world) | 4:5 (world) |
|---|---|---|
| maya plate | x −1300 → 500, y −40 → 287, radius 16 (bleeds off the left edge at CAM-M) | x −60 → 1140 (bleeds both sides), y −922 → −672, radius 24 |
| maya block | x −1288 → 488, y −28 → 275, radius 8 | x −48 → 1128, y −910 → −684, radius 12 |
| maya text | x −918 (screen 362 at CAM-M; final polish: at CAM-R0 her lines cross the frame edge in a word space, `test' \` / `(team atlas)` show dimmed); baselines 100, 146, 192, 238 | x 64; baselines −863, −817, −771, −725 |
| theo plate | x 1420 → 3220, y −40 → 287 (bleeds off the right edge at CAM-T) | x −60 → 1140, y 1486 → 1736 |
| theo block | x 1432 → 3208, y −28 → 275 | x −48 → 1128, y 1498 → 1724 |
| theo text | x 1468 (screen 188 at CAM-T); baselines 100, 146, 192, 238. The 75-character inbox row ends at screen 1808. At CAM-R0 his first line reads `theo$ agentnet` (the cut word is held back until move 4 brings it whole into the frame) | x 64; baselines 1546, 1592, 1638, 1684 |
| Slot | y 287 (both) | maya y −672 (bottom edge), theo y 1486 (top edge) |
| Dock (envelope centre) | maya (−320, 583), theo (2240, 583) | maya (540, −386), theo (540, 1148) |

**Terminal window: 4 lines.** A command lands whole in one frame and fades in over 120 ms (linear); its continuation
lines follow 42 ms apart. When the window is full, older lines leave by the top in the same frame (a real terminal
scroll). A dimmed line drops to 40 % over 120 ms. Contents per moment in section 7.

**Focus by dimming, never by blur.** Plates and chips stay opaque and sharp. To shift attention, text dims: chip text to
60 %, terminal text to `text-3` at 40 % (240 ms, brand ease). Schedule: both chips' text to 60 % at G20.25 (the
push); theo's back to 100 % at G32 (S-6 is about him); maya's back to 100 % at G55 (the answer arrives). theo's terminal
text is at 40 % whenever it shows at CAM-R0 (cropped by the frame edge, texture only) and returns to 100 % at G43.

### 4.7 Presence chips

A pill on the sky that hangs beside each machine's dock **on the relay side**, so at the relay both chips sit near the
envelope (16:9: at its top corners). `surface-2`, height 52, radius 26, padding 22, dot Ø 22, gap 12.

| | 16:9 (world) | 4:5 (world) |
|---|---|---|
| maya | `● maya (you)` x 96 → 390, y 395 → 447; dot centre (129, 421); text x 152, baseline 434 | `● maya (you)` x 64 → 334, y −120 → −68 (below her dock); text x 120 |
| theo | `○ theo offline` / `● theo online`, x 1493 → 1824 (fixed width), y 395 → 447; dot (1526, 421); `theo` x 1548, state x 1650, baseline 434 | x 64 → 367, y 878 → 930 (above his dock); `theo` x 120, state x 211 |
| At CAM-M | maya's at screen 1376 → 1670 (36 px right of the envelope) | maya's at screen 1008 → 1060 |
| At CAM-R0 | maya's at 96 → 390, theo's at 1493 → 1824, both under their machines' corners | theo's at 878 → 930, between the counter and the caption (maya's is above the frame) |
| At CAM-T | theo's at screen 213 → 544 (36 px left of the envelope) | theo's at screen 108 → 160 |

maya's chip uses the product's own marker `(you)` (`capture/15`, `status --team`) and no state word: her solid dot says
online and she never changes. theo's state words are the product's (`online` / `offline`, the DAEMON column).

**Presence (shape-coded).** Online: solid `online` dot. Offline: hollow ring, 3 px `text-3`. **Pulse:** a halo from
the dot, radius ×1 → ×2.6, 3 px `online`, opacity 0.5 → 0 over 1.2 s, brand ease; maya pulses on every downbeat while
in frame; theo on beat 3 of each bar once he is back. **Arrival pulse:** the same halo from opacity 0.8. **Ring fill**
(G40): 120 ms, fill 0 → 1 and stroke `text-3` → `online`; the state word cross-fades `offline` → `online` (120 ms).

### 4.8 The relay: a counter and a sorting rack

The one post-office construction, built from the brand's three ground steps. No icons, no clerk, no window. (Final
polish, director: a few cells hold other sealed mail, flat paper edges a step darker than our envelope, no text, no seal:
3 – 4 cells in 16:9, 3 in 4:5, so the rack reads as a sorting rack without the caption; common-v2.js GEO.mail.)

| Part | 16:9 (world) | 4:5 (world) |
|---|---|---|
| Rack wall | `surface`, x 520 → 1400, y 96 → 779, radius 16 | x 140 → 940, y 120 → 778, radius 24 |
| Sign | specimen label `Relay` (label stock, Special Elite 40 ink, inset hairline 5 px, ink 55 %): 187 × 106 at x 866 → 1053, y 108 → 214, on the wall's top band | 36 px: 169 × 95 at x 456 → 625, y 132 → 227 |
| Pigeonholes | `bg` recesses, 5 columns × 4 rows, channel 12, radius 8, inside x 544 → 1376, y 226 → 755 (cells 156.8 × 123.3) | 4 × 4, channel 18, radius 12, inside x 164 → 916, y 245 → 754 (cells 174.5 × 113.8) |
| Counter | `surface-2`, x 480 → 1440, y 779 → 859, radius 16: its top edge is the ledge the envelope rests on | x 110 → 970, y 778 → 854 |
| Envelope at rest | x 580 → 1340, y 387 → 779 (centre (960, 583); 760 px = 40 % of the frame at s 1, 51 % at CAM-R+) | x 190 → 890, y 406 → 778 (centre (540, 592)) |
| Clock | right-aligned at x 1416, baseline 830, on the counter's front: `NOW` (`text-3`) + ` 13:41:40 UTC` (`text-2`), 348 px | 28 px, right-aligned at x 946, baseline 825 (305 px) |

At CAM-R0 (16:9) the composition is three plates across the top (maya's machine to x 500, the rack 520 → 1400, theo's
machine from 1420), the envelope on the counter in the centre, a chip under each machine, the caption below. At CAM-M
and CAM-T only plain edges of the rack and counter show at the frame's far side; the sign is never in those frames.

**The clock** exists only during the wait (G33 → G41). It enters at G33 with the label's time, `13:41:40`, and then runs
**in real time**: one second per second of film (the 5 s between the relay's `queued` 13:41:40 and theo's reconnect
13:41:45 take 5.0 s on screen). Seconds digits change in place, flipping hard on their tick frames like ink (final polish; was a 120 ms cross-fade) at 24.571, 25.571, 26.571, 27.571 s
and reach `:45` at 28.571 s = G40, the music's return. `NOW` makes it the counter's clock, distinct from the form's
`TIME`. It fades out at G41 (240 ms). One clock second is 1.4 beats, so the ticks run against the beat on purpose; the
breakdown is quiet enough to carry it.

### 4.9 Trails, pointers and the pheromone budget

**One pheromone element per frame**, always. `accent` `#D653E1` on the ground, accent ink `#A021AC` on paper.

| Element | Spec |
|---|---|
| Trail | The brand's dotted rule along the lane (16:9 y 583; 4:5 x 540), behind the envelope. Dots 6 / pitch 24 (16:9 at s 1), 9 / 36 (4:5). Head hidden under the envelope; it shows from the departure dock to the envelope. Marches one pitch per beat, linear, while it moves; stops on arrival; the tail fades linearly as listed. `FILM.ui.trail`. |
| Pointer (underline) | Dots 6 / pitch 24 px on screen (16:9), 9 / 36 (4:5), 16 / 18 px under the baseline, drawn left to right in 240 ms, each dot fading in over 60 ms; out 120 ms. A new pointer appears only after the old one has gone. On the 16:9 label at CAM-R+ the pointer is a world element sized 4.6 / 18.5 so it reads 6 / 24 on screen. |
| Pointer targets | FROM value world x 704 → 867, TO 989 → 1152, TIME 704 → 949, SIZE 989 → 1213 (16:9; row 1 at y 659.3, row 2 at 727.3). Seal tab: x 908 → 1012, y 566 (in the air between the tab and the form). Answer summary: under both lines, row 1 then row 2 within the 240 ms. 4:5 targets are the same fields at their 4:5 positions. |

Budget: trail G16.75 → G20, tail to G22 · label pointers G22 → G27.75 · seal-tab pointer G28.5 → G31.5 · none in the wait ·
trail G40.25 → G43, tail to G45 · answer pointer G48 → G49.75 · return trail G52.5 → G55, tail to the cut G56 · card:
none · our trail G65 → G66, tail to G67.5 · note: none. Never two at once.

### 4.10 Motion grammar and blur windows

| Move | Duration | Curve |
|---|---|---|
| Plate lock-in (cold open only) | 500 ms, opacity 1 at 62 %, offset 16:9 x +92 / 4:5 y +120 | brand ease |
| Caption change | out 120 ms, in 240 ms + rise | brand ease |
| Terminal line | lands whole, 120 ms | linear |
| Ink (seal, labels, letter lines, note and card lines) | one frame | none |
| Paper slide (out of / into a machine) | as listed (0.40 – 0.90 s) | `FILM.CAM` |
| Fold / unfold | 280 ms, `rotateX` about the crease | brand ease |
| Flap close | 283 ms (17 frames), `rotateX` about the top fold | brand ease |
| Envelope travel | as listed | `FILM.CAM` |
| Camera | as listed | `FILM.CAM`, `worldCamStable` |
| Trail march / tail fade | 1 pitch per beat / as listed | linear |
| Signature | 85 ms per letter, a space advances 0.6 × step, jitter 0, letters fade 60 ms, no caret | linear |

Peak speeds (measured, px per frame) and the blur windows they need (`FILM.blur(t0, t1, fmt)`):

| Move | G | 16:9 peak | 4:5 peak | Blur window |
|---|---|---|---|---|
| Letter out of maya's machine (16:9 576 / 4:5 536 px, 0.90 s) | 8.5 | 17.8 | 16.5 | none |
| Folded letter into the machine (296 / 276 px, 0.45 s) | 12.5 | 18.3 | 17.0 | none |
| Envelope out (492 / 472 px, 0.75 s) | 13.25 | 18.2 | 17.5 | none |
| Move 1, M → R0 (1280 / 3.25 beats) | 16.75 → 20 | 15.3 | 13.5 | none |
| Push / tilt to CAM-R+ | 20.25 → 21.75 | 9.9 | 2.6 | none |
| Settle back | 29 → 31 | 7.3 | 1.9 | none |
| Move 4, R0 → T (1280 / 2.75 beats) | 40.25 → 43 | 18.5 | 10.5 | none |
| Envelope into theo's machine (492 / 524 px, 0.70 s) | 43 | 19.5 | 20.8 | **30.714 → 31.414** (both) |
| Brief out of theo's machine (576 / 536 px, 0.90 s) | 44 | 17.8 | 16.5 | none |
| Answer sheet out (356 / 336 px, 0.60 s) / folded sheet in (186 / 176 px, 0.40 s) | 46.75 / 50.25 | 16.5 / 12.9 | 15.6 / 12.2 | none |
| Answer envelope out (16:9 492 px, 0.75 s; 4:5 524 px, 0.80 s) | 50.75 | 18.2 | 18.2 | none |
| **Move 5, the return** (2560 / 2.5 beats, dip to s 0.7) | 52.5 → 55 | 30.6 | 24.2 | **37.500 → 39.286** (both) |
| Our envelope in (600 / 1 beat) | 65 → 66 | 23.3 | 23.3 | **46.429 → 47.143** (both) |
| Our letter out, envelope falls (16:9 860, 4:5 930 px, 1 beat) | 68 → 69 | 33.4 | 36.2 | **48.571 → 49.286** (both) |

No ink event, caption change or cut falls inside a window (the answer's seal is at 37.143, before the return's window).
Captions are static screen elements and stay sharp under the accumulated sub-frames.

### 4.11 Skies (brand `fieldSvg`, seeded, de-dusted as in v1)

| Place | Seed | Nebula / vignette | Display size 16:9 / 4:5 | Asset |
|---|---|---|---|---|
| Cold open | none: flat `bg` | | | |
| Scene | 97 (from `r-97ae…`) | 1.0 / 0.55 | 3400 × 1500 / 1500 × 2900 | **new**: 2× PNG via `fieldSvg`, then `tools/dedust.py`; covers every camera state at follow 0.3, including the return's dip (sky scale 0.91) |
| Card and invitation | 10 (v1) | 0.6 / 0.55 | 2016 × 1134 / 1134 × 1418 | v1 `sky-inv` (one sky for both sections) |
| End | 1444 (v1) | 0.6 / 0.55 | 1920 × 1080 / 1080 × 1350 | v1 `sky-end` |

The sky follows the camera at 0.3 × (scale 1 + 0.3 × (s − 1), translation 0.3 × camera translation). De-dust the caption
band (4.3). If a caption lands on the rose, mirror the sky or re-seed (seed + 1000).

---

## 5. Section by section

### 5.1 I. Cold open: the pain · G0 → G8 (1:1 → 3:1) · 0.000 → 5.714 s

**Purpose.** A recognisable pain in frame 0: the same brief pasted twice by hand. Music: the track's filtered intro
(bars 1–2), already playing under it.

| Element | 16:9 | 4:5 |
|---|---|---|
| Ground | flat `bg` | flat `bg` |
| Plate YOU | `surface`, radius 16, x 96 → 954, y 170 → 650 | radius 24, x −24 → 1104, y 140 → 520 |
| Plate THEY | x 966 → 1824, y 170 → 650 (one 12 px channel to the right) | y 538 → 918 (one 18 px channel below) |
| Brief text | Plex Mono 36, `text-2`, x +48 inside each plate (144 / 1014), baselines 248, 298, 348, 398, 448, 498, 548; wrapped greedily at 35 characters (7 lines, widest 734 px) | Plex Mono 32, `text-2`, x 64, baselines 200, 246, 292, 338, 384, 430 (YOU) and +398 (THEY); wrapped at 46 (6 lines, widest 883 px) |
| Caption | two lines, 894 / 970 | two lines, 1138 / 1196 (line 2 is 927 px in a 952 box) |

16:9 wrap: `What: Find out why the payments` / `integration test fails about one` / `run in ten.` /
`Why: It blocks merges twice a day` / `and nobody owns it yet.` / `Done when: You name the cause, or` /
`rule out the three usual suspects.` 4:5 wrap: `What: Find out why the payments integration` /
`test fails about one run in ten.` / `Why: It blocks merges twice a day and nobody` / `owns it yet.` /
`Done when: You name the cause, or rule out the` / `three usual suspects.` No brand colour yet.

| G | bar:beat | t | Grid | Event |
|---|---|---|---|---|
| 0 | 1:1 | 0.000 | DOWNBEAT | Plate YOU and caption line 1 `You paste the brief into a chat.` present at frame 0 (a complete thought for autoplay). |
| 2 | 1:3 | 1.429 | beat | Plate THEY locks in (500 ms, from x +92 / y +120): the second paste. Caption line 2 `They paste it into their coding agent.` enters (240 ms). |
| 0 → 8 | | | | Camera drift s 1.00 → 1.02 about the caption slot's centre (960, 930) / (540, 1167), `FILM.CAM`. |
| 7.5 | 2:4.5 | 5.357 | ½ | Both caption lines out (120 ms, ending here); **S-1** enters on the caption layer, above both shots, so it bridges the cut. "Dorylinae" is `text` until 6.314 s (the wipe complete), then cross-fades to `brand` (120 ms). |
| 8 | 3:1 | 5.714 | DOWNBEAT | Cut 1: the 21° cut (`FILM.cut21`, 0.6 s, no stroke, glow or blur on the edge). |

Sound: no accent but one LAND at G2 (the second paste).

### 5.2 II. The scene · G8 → G56 (3:1 → 15:1) · 5.714 → 40.000 s

#### 5.2.1 Objects

maya's machine with her chip, the relay, theo's machine with his chip, the paper (one envelope at a time; a letter or
sheet at a dock), the caption. No arrows, icons or extra labels. Each camera state is built around at most three
objects plus the caption; others show only as plain edges.

#### 5.2.2 Stage at a glance (world)

```
16:9 (lane at world y 583; every camera keeps it near screen y 583)

 maya plate x -1300 … 500 (y -40 … 287)   rack x 520 … 1400 (y 96 … 779)   theo plate x 1420 … 3220 (y -40 … 287)
 [maya dock (-320, 583)] [chip 96…390]   [counter 480…1440, envelope (960, 583)]   [chip 1493…1824] [theo dock (2240, 583)]
                                          caption slot (screen, under the lane)

4:5 (lane at world x 540)
 maya plate y -922 … -672 · letter / dock (540, -386) · chip y -120 … -68
 rack y 120 … 778 · envelope (540, 592) · counter 778 … 854
 theo chip y 878 … 930 · dock (540, 1148) · brief out to y 950 … 1470 · plate y 1486 … 1736
 caption slot (screen, bottom)
```

#### 5.2.3 Camera states and moves

| State | 16:9 | 4:5 | Used |
|---|---|---|---|
| CAM-M (maya) | c (−320, 540), s 1: screen = world + (1280, 0). maya's text from x 140; letter 598 → 1322 × 303 → 863; envelope 580 → 1340 × 387 → 779; chip 1376 → 1670 | c (540, −453): screen y = world + 1128. Plate 206 → 456 (text 265 … 403); letter 472 → 992; envelope 556 → 928; chip 1008 → 1060 | G8 → G16.75, and the end (G55 → G56) |
| CAM-R0 (relay) | c (960, 540), s 1 (world = screen) | c (540, 675) (world = screen) | G20, G31 → G40.25 |
| CAM-R+ (closer) | s 1.3 with the form's centre (960, 663) at screen (960, 540), i.e. c (960, 663): envelope 466 → 1454 × 181 → 691; form values 44.2 px; counter bottom 794.8 | pure tilt: c (540, 775) (everything 100 px higher; no scaling) | G21.75 → G29 |
| Wait drift | s 1.00 → 1.02 about the envelope's centre (960, 583), G32 → G40 | tilt c y 675 → 699, G32 → G40 | the wait |
| CAM-T (theo) | c (2240, 540), s 1: screen = world − (1280, 0). theo's text from 184; brief 598 → 1322 × 303 → 863; chip 213 → 544 | c (540, 1445): screen y = world − 770. Chip 108 → 160; brief 180 → 700; plate 716 → 966 (text 776 … 914) | G43 → G52.5 |

| Move | G (t) | 16:9 | 4:5 |
|---|---|---|---|
| 1 | 16.75 → 20 (11.964 → 14.286) | truck M → R0, Δx 1280, locked to the envelope (it stays at screen x 960) | tilt c y −453 → 675; the envelope travels 978, so it drifts 150 px up the screen |
| 2 | 20.25 → 21.75 | push R0 → R+ | tilt +100 |
| 3 | 29 → 31 | settle R+ → R0 | tilt back |
| 4 | 40.25 → 43 (28.750 → 30.714) | truck R0 → T, Δx 1280 (from the drift's s 1.02 back to 1.00), locked | tilt c y 699 → 1445; envelope travels 556 |
| 5 | 52.5 → 55 (37.500 → 39.286) | **the return**: the camera keeps the answer envelope at screen (960, 583) while it travels world x 2240 → −320 (`FILM.CAM`); scale s(τ) = 1 − 0.3 sin(πτ), τ = linear time in the move; c = (x_env, 583 − 43 / s). At mid-move (s 0.7) the frame shows the whole route and the envelope skims the counter. Lands on CAM-M | the same: the envelope travels world y 1148 → −386; its screen y moves 378 → 742 (`FILM.CAM`), s(τ) = 1 − 0.3 sin(πτ), c y = y_env − (y_screen − 675) / s. Lands on CAM-M |

All moves `FILM.CAM`, `worldCamStable`. Blur windows in 4.10.

#### 5.2.4 Beat table

| G | bar:beat | t | Grid | Picture | Caption slot |
|---|---|---|---|---|---|
| 8 | 3:1 | 5.714 | DOWNBEAT | 21° cut. CAM-M. maya's machine: terminal `maya$ agentnet identity` / `public key:  QZ_vPMFN…` (dim) / `maya$`. Her chip `● maya (you)`. The dock is empty. maya pulse. | **S-1** (entered G7.5) `With Dorylinae, your agent hands it to theirs.` 16:9 one line (1498 px); 4:5 `With Dorylinae, your agent` / `hands it to theirs.` |
| 8.5 | 3:1.5 | 6.071 | ½ | The letter slides out of maya's slot (0.90 s, `FILM.CAM`) to world y 303 → 863 (16:9) / −656 → −136 (4:5): white, with its header `flaky-brief.md`, no lines yet. PAPER. | |
| 9.75 → 10.625 | | 6.964 → 7.589 | ⅛ | The 8 brief lines appear, one every ⅛ beat (frames 418, 423, 429, 434, 439, 445, 450, 455): the agent writes. | |
| 11 | 3:4 | 7.857 | beat | The agent's command lands on maya's terminal (4 lines; the window scrolls the identity lines out). LAND. | |
| 12 | 4:1 | 8.571 | DOWNBEAT | The letter folds in half (280 ms). PAPER. maya pulse. | |
| 12.5 | 4:1.5 | 8.929 | ½ | The folded letter slides up into maya's machine (0.45 s): her machine takes it. | |
| 13 | 4:2 | 9.286 | beat | | S-1 out; **S-2** `Your machine seals it.` |
| 13.25 | 4:2.25 | 9.464 | ¼ | The open envelope slides out of the slot (0.75 s) to the dock: flap open and tucked, the folded letter's top edge showing in its mouth. PAPER. | |
| 14.5 | 4:3.5 | 10.357 | ½ | The letter drops in (24 px, 120 ms). | |
| 15.25 | 4:4.25 | 10.893 | ¼ | The address form is printed (frame 654): FROM `QZ_vPMFN`, TO `ZhbF0Hsy`, TIME `13:41:40 UTC`, SIZE `1,444 bytes`. LAND. | |
| ≈15.6 | | 11.150 | | The flap closes (frames 669 → 685). | |
| **16** | **5:1** | **11.429** | **DOWNBEAT** | **The seal** (frame 686), on the music's lift. SEAL. maya pulse. | |
| 16.25 | 5:1.25 | 11.607 | ¼ | Output `Queued normal task request r-97ae… to theo (team atlas)` lands. LAND. | |
| 16.75 | 5:1.75 | 11.964 | ¾ | The envelope lifts (8 px) and travels to the relay (G16.75 → G20); the trail draws behind it and marches. Camera move 1. | S-2 out; **S-3** `The relay works like a post office.` |
| **20** | **6:1** | **14.286** | **DOWNBEAT** | **It sets down on the counter** in front of the rack. ARRIVE (root). Trail stops; tail fades G20 → G22. | |
| 20.25 → 21.75 | | 14.464 → 15.536 | | Move 2 (16:9 push to CAM-R+; 4:5 tilt). Chip text to 60 %. | |
| 21.5 | 6:2.5 | 15.357 | ½ | The form fills the eye: 44 px values (16:9), 32 px (4:5). | S-3 out; **S-4** line 1 `It reads the outside:` |
| 22 | 6:3 | 15.714 | beat | Pointer under `QZ_vPMFN` (FROM). | line 2: `who,` |
| 23 | 6:4 | 16.429 | beat | Pointer → `ZhbF0Hsy` (TO). | `to whom,` |
| 24 | 7:1 | 17.143 | DOWNBEAT | Pointer → `13:41:40 UTC` (TIME). | `when,` |
| 25 | 7:2 | 17.857 | beat | Pointer → `1,444 bytes` (SIZE); it stays to G27.75. | `how big.` |
| **28** | **8:1** | **20.000** | **DOWNBEAT** | The sealed envelope, still. | S-4 out; **S-5** `It cannot open the envelope.` |
| 28.5 | 8:1.5 | 20.357 | ½ | Pointer under the seal tab (to G31.5). | |
| 29 → 31 | | 20.714 → 22.143 | | Move 3, settle to CAM-R0. Both chips come back into frame. | |
| **32** | **9:1** | **22.857** | **DOWNBEAT** | **Breakdown.** theo's chip `○ theo offline` returns to full text, beside the envelope's top-right corner (16:9) / under the counter (4:5). The wait drift begins. | S-5 out; **S-6** `Theo is offline. The envelope waits.` |
| 33 | 9:2 | 23.571 | beat | The clock enters on the counter: `NOW 13:41:40 UTC`. | |
| | | 24.571 → 27.571 | | Clock `:41`, `:42`, `:43`, `:44`, one per real second. CLOCK each. Nothing else moves. | |
| **40** | **11:1** | **28.571** | **DOWNBEAT** | **Return.** Clock `:45`. theo's ring fills (120 ms), `offline` → `online`, arrival pulse. CLOCK + BACK. | S-6 out; **S-7** `Theo is back. His machine opens it.` |
| 40.25 | 11:1.25 | 28.750 | ¼ | Cause, then effect: the envelope lifts and travels to theo's dock (G40.25 → G43); trail; move 4. Clock fades at G41. | |
| 43 | 11:4 | 30.714 | beat | It sets down at theo's dock, under his terminal's `public key:  ZhbF0Hsy…`, and at once slides into his machine (0.70 s). ARRIVE (fifth). theo's terminal text to 100 %. Trail tail fades G43 → G45. | |
| **44** | **12:1** | **31.429** | **DOWNBEAT** | **Opened.** The brief slides out of theo's machine, unfolded (0.90 s): the same text, now with its crease. PAPER. | |
| 44.5 | 12:1.5 | 31.786 | ½ | theo's inbox lands (3 rows, 42 ms apart). LAND. | |
| 45 | 12:2 | 32.143 | beat | (brief readable) | S-7 out; **S-8** `Theo’s agent does the work.` |
| 45.75 | 12:2.75 | 32.679 | ¾ | The result command lands (16:9 3 lines; the inbox row stays at full until the answer comes out at G46.875, then dims to 40 %. 4:5 4 lines; the inbox scrolls out). LAND. | |
| 46.25 | 12:3.25 | 33.036 | ¼ | theo's brief slides back into his machine (0.45 s, blur window): his machine keeps it. | |
| 46.875 | 12:3.875 | 33.482 | ⅛ | The answer sheet slides out of theo's machine (0.60 s) once the brief is in: `status pass` / `Cause found: two tests share` / `one ledger row`. PAPER. | |
| **48** | **13:1** | **34.286** | **DOWNBEAT** | Pointer (accent ink) under the summary. | |
| 49 | 13:2 | 35.000 | beat | | S-8 out; **S-9** `The answer comes back, sealed the same way.` 16:9 one line (1396 px); 4:5 `The answer comes back,` / `sealed the same way.` |
| 49.75 | 13:2.75 | 35.536 | ¾ | The answer sheet folds (pointer out 120 ms first). PAPER. | |
| 50.25 | 13:3.25 | 35.893 | ¼ | It slides into theo's machine (0.40 s; up in 16:9, down in 4:5). | |
| 50.75 | 13:3.75 | 36.250 | ¾ | The answer envelope slides out (0.75 s), flap open; the flap closes as it settles (frames 2212 → 2228). PAPER. 4:5: it rises out of theo's slot (0.80 s) with its flap already closed, because an open flap would cross theo's chip; the ink still lands on G52. | |
| **52** | **14:1** | **37.143** | **DOWNBEAT** | **The answer's seal and form** (frame 2229): FROM `ZhbF0Hsy`, TO `QZ_vPMFN`, TIME `13:43:54 UTC`, SIZE `1,328 bytes`. SEAL. | |
| 52.5 | 14:1.5 | 37.500 | ½ | It lifts and travels back (G52.5 → G55), move 5: the frame opens to the whole route at mid-move (the envelope skims the counter without stopping: both are online), then closes on maya's machine. Trail behind it. theo's dock is empty: his copy went back into his machine at G46.25. | |
| 55 | 14:4 | 39.286 | beat | It sets down at maya's dock (CAM-M, the scene's first frame). ARRIVE (root); maya's arrival pulse; her chip text to 100 %. It stays sealed. | |
| **56** | **15:1** | **40.000** | **DOWNBEAT** | **Cut 2.** | (cut) |

#### 5.2.5 Key frames

- **K1, G10.75 (7.68 s), CAM-M.** maya's wide terminal across the top; under it the white letter with the eight brief
  lines; her chip to its right; the rack's plain edge at the right frame edge. Caption `With Dorylinae, your agent
  hands it to theirs.`
- **K2, G15.5 (11.07 s), CAM-M.** The envelope at the dock, flap swinging closed, the form printed. Terminal: the
  4-line command. Caption `Your machine seals it.`
- **K3, G26 (18.57 s), CAM-R+.** The envelope at 51 % of the frame on the counter; the form at 44 px; the pointer under
  `1,444 bytes`. Caption `It reads the outside:` / `who, to whom, when, how big.`
- **K4, G38 (27.14 s), CAM-R0.** The poster frame (section 9).
- **K5, G44.9 (32.1 s), CAM-T.** theo's terminal with the inbox; the unfolded brief at his dock with its crease; his
  chip `● theo online`. Caption `Theo’s agent does the work.`
- **K6, G48.5 (34.64 s), CAM-T.** The answer sheet alone at theo's dock (his brief went back into his machine at
  G46.25), the summary underlined; the result command above.
  Caption `Theo’s agent does the work.`
- **K7, G53.75 (38.39 s), move 5 at mid-flight.** The whole route at s ≈ 0.7: maya's machine, the rack and counter
  with the answer passing over it, theo's machine with his dock empty. Caption `The answer comes back, sealed the same way.`

### 5.3 III. The card · G56 → G65 (15:1 → 17:2) · 40.000 → 46.429 s

**Purpose.** Name what the viewer just saw, with hierarchy, plus the one fact about control. (The honest limit, the
footnote, is cut by the owner, O-1: it goes in the invitation, privacy note and terms.) The table is as built.

| Element | 16:9 | 4:5 |
|---|---|---|
| Ground | sky `inv` (seed 10) | same |
| Card | label stock, x 192 → 1728, y 313 → 767 (centred in the frame without the footnote), radius 16; inset hairline 12 px in, ink 55 %, 2 px | x 64 → 1016, y 355 → 995 (centred), radius 24; inset 10 px, 2 px |
| Display | `End-to-end encrypted.` Special Elite 88, −0.01 em, ink, x 288, baseline 455 (1020 px) | 72 px, x 112, baseline 517 |
| Line 2 | `Requests wait while your teammate is offline.` Plex Sans 500 64, ink, x 288, baseline 585 (1332 px; inner edge 1632) | 52 px, x 112, two lines `Requests wait while` / `your teammate is offline.`, baselines 635 / 701 |
| Line 3 | `You approve what their agent can read.` baseline 677 (1143 px) | `You approve what` / `their agent can read.`, baselines 805 / 871 |
| Footnote (O-1) | none (owner decision: cut) | none |
| Camera | none: the card holds still; its sky alone drifts 0.4 px/s left to its rest place at G65 | same |

| G | bar:beat | t | Grid | Event |
|---|---|---|---|---|
| 56 | 15:1 | 40.000 | DOWNBEAT | Hard cut. The card with its display line. |
| 57 | 15:2 | 40.714 | beat | Line 2 appears (one frame). |
| 58 | 15:3 | 41.429 | beat | Line 3 appears. |
| 59.5 | 15:4.5 | 42.500 | ½ | (no footnote: cut, O-1) |
| 65 | 17:2 | 46.429 | beat | Hard cut. |

No pointer, no moving underline, no icons, no logo. 17 words in 6.43 s.

### 5.4 IV. The invitation · G65 → G80 (17:2 → 21:1) · 46.429 → 57.143 s

**Purpose.** The people at Dorylinae send you an envelope addressed to your team. It opens to a short note in their own
voice: who is asking, that it is for teams like yours, what we ask, what you get, where to apply, signed by a person.

| Element | 16:9 | 4:5 |
|---|---|---|
| Ground | sky `inv` (seed 10), the card's sky | same |
| Envelope | section 4.4 (ours): rest x 460 → 1460, y 260 → 720 | rest x 90 → 990, y 420 → 834 |
| Trail | y 490, from the left frame edge to the envelope's left edge | x 540, from the top edge to the envelope's top edge |
| Camera | push s 1.00 → 1.06 about the form's centre (960, 556), G66 → G68, then held | s 1.00 → 1.04 about (540, 696), held |
| Letter | white, 960 × 820 at x 480 → 1440, y 130 → 950 (world, s 1): 1018 × 869 on screen after the push, y 104 → 974 | 880 × 760 at x 100 → 980, y 295 → 1055 (world) |
| Note | Plex Sans 500 44, ink, x 552 (72 px margins; measure 816) | 40 px, x 164 (64 px margins; measure 752) |
| Signature | Special Elite 56, ink, x 552 | 48 px, x 164 |

Note layout (world baselines; each paragraph sits in one fold panel, the crease falls in a gap):

| Line | 16:9 baseline | 4:5 baseline | Width 16:9 / 4:5 |
|---|---|---|---|
| `We’re inviting a first wave` | 235 | 391 | 514 / 468 |
| `of 10 small teams, like yours.` | 293 | 445 | 587 / 533 |
| `Tell us what breaks.` | 383 | 525 | 400 / 364 |
| `That decides what we change first.` | 441 | 579 | 699 / 635 |
| (crease) | 540 | 675 | 89 / 87 px below the line above |
| `Free during the closed beta.` | 620 | 751 | 562 / 511 |
| `Apply at dorylinae.net` | 710 | 833 | 446 / 405 (no period: the same words as the end card) |
| `Yazan` (O-2, final) | 830 | 945 | 157 / 135 |
| Bottom margin | 107 (≥ 96) | 98 | |

| G | bar:beat | t | Grid | Event |
|---|---|---|---|---|
| 65 | 17:2 | 46.429 | beat | Hard cut. Our envelope is travelling in along its trail (from x −140 / y −180, 600 px, 1 beat, `FILM.CAM`, blur window) and settles at G66. Its form reads `FROM Dorylinae` · `TO your team`; its seal is our mark. PAPER. Trail tail fades G66 → G67.5. |
| 66 → 68 | | 47.143 → 48.571 | | The camera comes slowly toward it (push), onto `TO your team`. |
| 67.5 | 17:4.5 | 48.214 | ½ | A slit opens along the top fold (120 ms). The flap and seal stay whole. PAPER. |
| 68 | 18:1 | 48.571 | DOWNBEAT | The letter rises out of the slit (16:9 folded block from y 290 → 700 up to 130 → 540; 4:5 from 437 → 817 up to 295 → 675) while the envelope falls away below the frame (16:9 down 860, 4:5 down 930), both 1 beat, `FILM.CAM`, blur window. |
| 69 | 18:2 | 49.286 | beat | The letter unfolds about its crease (280 ms) to full height. PAPER. |
| 69.5 | 18:2.5 | 49.643 | ½ | `We’re inviting a first wave` / `of 10 small teams, like yours.` (one frame). |
| 71 | 18:4 | 50.714 | beat | `Tell us what breaks.` |
| 72 | 19:1 | 51.429 | DOWNBEAT | `That decides what we change first.` (the music's outro begins) |
| 73.5 | 19:2.5 | 52.500 | ½ | `Free during the closed beta.` |
| 75 | 19:4 | 53.571 | beat | `Apply at dorylinae.net` |
| 76 | 20:1 | 54.286 | DOWNBEAT | The signature `Yazan` types at 85 ms a letter (O-2; 5 letters, the last at 54.63 s). TYPE per letter. The only typed line in the film: a hand signing. |
| 80 | 21:1 | 57.143 | DOWNBEAT | Hard cut. |

29 words of note plus the signature in 7.5 s. The letter's folded block (16:9 960 × 410, 4:5 880 × 380) fits inside its
envelope (1000 × 460 / 900 × 414), so it is the same paper that unfolds.

### 5.5 V. Boot and address · G80 → G90 (21:1 → 23:3) · 57.143 → 64.286 s

As v1 S11 and S12 (`SCRIPT.md` section 3 and 6), unchanged except the times.

| | 16:9 | 4:5 |
|---|---|---|
| Ground | sky `end` (seed 1444) | same |
| Boot container (`FILM.boot`) | left 552.25, top 85.23, width 815.5; mark 600 wide, top y 150; lockup bottom 768.0 | left 105.07, top 160.91, width 869.9; mark 640 wide, top y 230; lockup bottom 889.2 |
| Address | `Apply at dorylinae.net`, Special Elite 64, −0.01 em, `text`, centred x 960, baseline 930 (732 px) | 64 px, centred x 540, baseline 1060 |

| G | bar:beat | t | Grid | Event |
|---|---|---|---|---|
| 80 | 21:1 | 57.143 | DOWNBEAT | Cut to the bare sky. The boot plays unchanged (`render(t − 57.143)`): plates, glint, signal, the name typed (its last letter at +2.58 s = 59.72 s); nominal end 60.743. Scanlines static. TYPE ×9 at −38 dBFS on the boot's own letter times. |
| 84 | 22:1 | 60.000 | DOWNBEAT | The address enters (240 ms, rise 12 / 18 px), in the boot's final hold (the lockup is complete); landed 60.240. |
| 90 | 23:3 | 64.286 | | End. The address is held 4.05 s, to the last frame; the music is silent from 63.857. |

---

## 6. Copy deck: every on-screen sentence

Holds from "landed" to the start of the out-fade (or to the cut). Ink lines (card, note) are landed on their frame.

| # | Where | Verbatim | Words | Rule (s) | Window (G) | Hold (s) | Source |
|---|---|---|---|---|---|---|---|
| C-1 | Cold open | You paste the brief into a chat. | 7 | floor 2.95 | 0 → 7.5 | 5.24 | product-facts §1a ("you paste the context into a chat"), §2 (PLAN:11); v1 amendment 1 |
| C-2 | Cold open | They paste it into their coding agent. | 7 | floor 2.95 | 2 → 7.5 | 3.57 | product-facts §2 (PLAN:11); BRIEF ("your coding agent") |
| S-1 | Scene | With Dorylinae, your agent hands it to theirs. | 8 | floor 3.30 | 7.5 → 13 | 3.57 | product-facts §1a ("your assistant sends the request straight to your teammate's assistant"); v1 S2 |
| S-2 | Scene | Your machine seals it. | 4 | floor 1.90 | 13 → 16.75 | 2.32 | product-facts §3.10, §6 #6 ("Messages are sealed on your machine."); fact-check A |
| S-3 | Scene | The relay works like a post office. | 7 | floor 2.95 | 16.75 → 21.5 | 3.03 | product-facts §3.10 ("the relay is a post office that cannot open the envelopes") |
| S-4 | Scene | It reads the outside: who, to whom, when, how big. | 10 | floor 4.00 | 21.5 → 28 | 4.28 (last word 1.90) | product-facts §3.10 (the relay can see from, to, type, id, timestamp, size); `capture/25`; fact-check review (no "only": the list is not exhaustive) |
| S-5 | Scene | It cannot open the envelope. | 5 | floor 2.25 | 28 → 32 | 2.50 | product-facts §3.10 (cannot see titles, briefs, results …), §5 #3 |
| S-6 | Scene | Theo is offline. The envelope waits. | 6 | **key 4.10** | 32 → 40 | 5.35 | `capture/15` ("theo is offline … it will be delivered when theo is back."); `capture/25` (queued 13:41:40 → delivered 13:41:45); fact-check B1 |
| S-7 | Scene | Theo is back. His machine opens it. | 7 | floor 2.95 | 40 → 45 | 3.21 | `capture/25` (`peer connected` 13:41:45, `delivered from queue`); product-facts §1a ("only your teammate's machine can open them"), §3.10 |
| S-8 | Scene | Theo’s agent does the work. | 5 | floor 2.25 | 45 → 49 | 2.50 | v1 amendment 2; product-facts §1a ("They accept it, do the work and send back an answer"); `capture/21` |
| S-9 | Scene | The answer comes back, sealed the same way. | 8 | floor 3.30 | 49 → 56 (cut) | 4.76 | product-facts §3.11, §3.10 (every application message is signed, sealed mail); `capture/25` (theo → maya mail 13:43:54.425Z, 1328 bytes) |
| K-1 | Card | End-to-end encrypted. | 3 | **key 2.30** | 56 → 65 | 6.43 | product-facts §5 #18 (safe wording "End-to-end encrypted."), §3.10 |
| K-2 | Card | Requests wait while your teammate is offline. | 7 | **key 4.70** | 57 → 65 | 5.71 | product-facts §3.9; fact-check B1 ("The request waits, and arrives when they are back."). No duration claimed |
| K-3 | Card | You approve what their agent can read. | 7 | **key 4.70** | 58 → 65 | 5.00 | product-facts §3.12, §3.13 (a grant waits as `pending_approval` until you approve in the daemon's window, or a policy you made matches); fact-check B4 (boundary, risk 5) |
| K-4 | Card footnote (O-1) | ~~No independent security review yet.~~ **Not in the film** (owner decision, final: the disclosure goes in the invitation, privacy note and terms) | — | — | — | — | `Docs/beta/known-limitations.md` ("This private beta has not had an independent outside security review"); HANDOFF D41; fact-check C10 |
| I-0 | Our envelope | FROM Dorylinae · TO your team | 3 values | floor 1.55 | 65 → 68 | ~1.9 (readable 46.7 → 48.6) | BRIEF-v2; v1 amendment 3 |
| N-1 | Note | We’re inviting a first wave of 10 small teams, like yours. | 11 | **key 7.10** | 69.5 → 80 | 7.50 | BRIEF owner decisions (first wave opens with 10 teams; "small teams"); beta-facts §2 Waves; fact-check B18 (the owner's team is one of the 10) |
| N-2 | Note | Tell us what breaks. | 4 | key 2.90 | 71 → 80 | 6.43 | `known-limitations.md` lines 3–5 ("If one of them gets in your way, tell us: that is how we decide what to change first"); an accepted stretch from "what gets in your way" to "what breaks" |
| N-3 | Note | That decides what we change first. | 6 | key 4.10 | 72 → 80 | 5.71 | the same lines |
| N-4 | Note | Free during the closed beta. | 5 | key 3.50 | 73.5 → 80 | 4.64 | BRIEF beta facts ("Closed beta, by invitation"; "Free during the beta"); beta-facts §5 (must not imply instant access) |
| N-5 | Note | Apply at dorylinae.net | 3 | key 2.30 | 75 → 80 | 3.57 | BRIEF owner decision (CTA, one destination repeated identically) |
| N-6 | Signature (O-2) | `Yazan` (owner decision, final) | 1 | floor 1.55 from the first letter | 76 → 80 | 2.86 from the first letter; 2.51 after the last (54.63 s) | BRIEF ("We" only where Dorylinae, the people, speak); reviews (a person signs an invitation); BRIEF-v2 owner decisions (final) |
| E-1 | End | Apply at dorylinae.net | 3 | end card ≥ 4.00 | 84 → 90 | 4.05 | BRIEF owner decision (CTA) |

S-1 enters 0.36 s before the cut, above the wipe, so it is read across the cut (the review's own proposal). The two
end-to-end captions S-5 and S-7 meet the floor, not the key rule; the card repeats the claim for 6.4 s.

**Labels and in-world text** (not captions):

| Text | Where | Source |
|---|---|---|
| `maya (you)`, `theo`, `online`, `offline` | chips | `capture/15` `status --team` (`(you)` marker, DAEMON column); `capture/INDEX.md` |
| `Relay` | rack sign | product-facts §3.0; `capture/25` |
| `NOW 13:41:40 UTC` … `13:41:45 UTC` | counter clock | `capture/25`: queued `13:41:40.228Z`, `peer connected` `13:41:45.885Z`, `delivered from queue` `13:41:45.886Z` (milliseconds dropped) |
| FROM `QZ_vPMFN` TO `ZhbF0Hsy` TIME `13:41:40 UTC` SIZE `1,444 bytes` | the brief's form | `capture/25` line 61: `time=2026-10-08T13:41:40.228Z … msg=queued … from=QZ_vPMFN to=ZhbF0Hsy type=mail id=m-b2d3dc4566beac6d9631949d221555c0 bytes=1444`; the same mail as maya's request (`mail_id` in `capture/15`) |
| FROM `ZhbF0Hsy` TO `QZ_vPMFN` TIME `13:43:54 UTC` SIZE `1,328 bytes` | the answer's form | `capture/25` line 122: `time=2026-10-08T13:43:54.425Z … msg=routed … id=m-f3d08acefcbd1651076e5a354b160649 bytes=1328`. **Matched by id:** maya's daemon logged the same mail id as `kind="ws.result"` (`capture/22b` line 96). The relay itself cannot tell what is inside |
| `public key:  QZ_vPMFN…` / `public key:  ZhbF0Hsy…` | the machines' terminals | `capture/04` (`agentnet identity`), keys shortened, other lines omitted |
| `flaky-brief.md` and the eight brief lines | the letter | `capture/run/work/flaky-brief.md` (= the `brief` in `capture/20-grant.txt`), word for word |
| `status pass` / `Cause found: two tests share one ledger row` | the answer sheet | `capture/21` |
| FROM `Dorylinae` TO `your team` | our envelope | our own words |

**Totals.** 131 words of copy (74 in captions, 22 on the card, 3 on our form, 29 in the note, 3 on the end card), plus
the signature. Voice: short declarative
sentences, present tense, "you" for the reader, numerals, no exclamation marks, no hype, no tagline. "We" and "us" only
in the note. No ant word on any screen. Coding agents stay generic. The wordmark DORYLINAE appears only in the boot's
lockup. Names: captions write `Theo` (a name in running text); chips and prompts keep the handles `maya` and `theo`.

---

## 7. Real capture lines and every edit

Allowed edits (BRIEF): omit lines, crop, shorten ids (re-pad an aligned column to match, whitespace only), break long
commands with ` \`, swap the scratchpad path, drop versions and harness names. Nothing else. No path, port, version,
hostname or harness name appears.

| Machine, moment | File | Lines shown (16:9) | 4:5 differences | Edits |
|---|---|---|---|---|
| maya, G8 | `capture/04-identity.txt` | `maya$ agentnet identity` · `public key:  QZ_vPMFN…` · `maya$` | same | `name`, `harness`, `fingerprint`, `created`, `skills`, `signature`, `key storage` lines omitted (the harness line must never show); key `QZ_vPMFNEulOn5SiRBLGiBTeUQnF9mbhFg4Pw9DZGvk` → `QZ_vPMFN…`. The public key line dims to 40 % |
| maya, G11 / G16.25 | `capture/15-offline-delivery.txt` | `maya$ agentnet request theo task \` · `    --title 'Track down the flaky payments test' \` · `    --brief-from-file flaky-brief.md \` · `    --idempotency-key demo-flaky-1` · then `Queued normal task request r-97ae… to theo (team atlas)` (the window scrolls the first command line out) | 2-space continuation indent (the widest line is 922 px, ending at x 986, inside safe); the output line is cropped by the right frame edge after 52 characters (`… to theo (team at`) | the one-line command broken at its flags with ` \` (every flag kept, in order); `r-97ae9a37feaf51fcf0c3c8246ae57be1` → `r-97ae…`; the second output line (`theo is offline …`) omitted. No pointer or camera move lands on the `demo-flaky-1` line |
| theo, idle (CAM-R0 dimmed, CAM-T full) | `capture/04-identity.txt` | `theo$ agentnet identity` · `public key:  ZhbF0Hsy…` · `theo$` | same | as for maya. At CAM-R0 the lines are cropped by the right frame edge and dimmed to 40 % (texture) |
| theo, G44.5 | `capture/15-offline-delivery.txt` | `theo$ agentnet inbox` · `ID       FROM  TYPE  URGENCY  PRIO  AGE  TITLE` · `r-97ae…  maya  task  normal   2000  0m   Track down the flaky payments test` (75 characters, ends at screen 1804) | the row is cropped by the right frame edge after 52 characters (`… 0m   Track down t`); the title is read on the letter | the other row (`r-d0d8…`) omitted; id shortened; the ID column re-padded to the short id (whitespace only, 34 spaces after `ID` to 7; characters unchanged; part of the allowed edit "shorten ids (re-pad an aligned column to match)", BRIEF; fix-r3: it cited `capture/INDEX.md`, which has no such line) |
| theo, G45.75 | `capture/21-quarantine-release.txt` | `theo$ agentnet result s-b309… --status pass \` · `    --summary 'Cause found: two tests share one ledger row' \` · `    --file flaky-finding.md` (window: the dimmed inbox row above it) | `theo$ agentnet result s-b309… --status pass \` · `  --summary \` · `  'Cause found: two tests share one ledger row' \` · `  --file flaky-finding.md` (941 px widest, ends at x 1005) | `s-b3098b5fd25dd5ec2aa2b78fe588fc03` → `s-b309…`; command broken with ` \` (4:5 also between `--summary` and its quoted value, valid shell); the output `Submitted result for s-b309… (open)` omitted (the answer sheet coming out of the machine stands for it) |
| the letter | `capture/run/work/flaky-brief.md` | the three fields, wrapped on the sheet | same | none to the words; set as a document (4.5) |
| the answer sheet | `capture/21` | `status pass` · `Cause found: two tests share` / `one ledger row` | same | the result's real status and summary, set as a document and wrapped at 29 characters like the brief |
| the forms, the clock | `capture/25-relay-log.txt` | values only (section 6) | same | typeset as fields: key prefixes as the relay logs them, time to the second with UTC, size with a thousands comma |

**What happened between the shown lines** (omitted, allowed, never contradicted): between the inbox and the result,
theo accepted (13:43:22); maya granted read access to `payments-ci` and approved it with a code typed into her daemon's
own window (`capture/20`, `capture/24`); theo's agent fetched `logs/run-07.log`. After the answer arrived, maya's daemon
held it in quarantine until she released it with a second approval (`capture/21`). The film shows the answer arriving
sealed at maya's machine and never shows it opened, so it implies nothing automatic (risk 6).

---

## 8. Sound accents (in code, under the music)

A few soft physical sounds tied to picture events, generated in code (numpy/scipy, as v1) and registered with
`FILM.cue(t, KIND)` so picture and sound share times. Pitched accents use the track's key (F minor, with A♭ / D♭ major
colour): root F, fifth C. Accents sit 6 to 12 dB under the music's momentary loudness; only SEAL and BACK duck the music
by 2 dB for 300 ms. Master −14 LUFS integrated, true peak ≤ −1 dBTP (the source peaks at +0.3 dBTP: leave gain to the
final limiter). No whooshes, risers, braams, keyboard clicks, voices.

| Kind | Sound | Level |
|---|---|---|
| PAPER | soft paper slide or fold, 80 to 150 ms, band 800 Hz to 6 kHz | −32 dBFS |
| LAND | dry wood tick, 20 ms: a command lands, ink is printed (never per character) | −32 dBFS |
| SEAL | felt mallet thump, low, 120 ms (a stamp, not a hit) | −22 dBFS |
| ARRIVE | felt piano note, ~2 s decay: F (counter, maya) or C (theo) | −24 dBFS |
| CLOCK | soft clock tick, 25 ms, wood | −34 dBFS |
| BACK | glass bell, C an octave up, 1.5 s decay | −22 dBFS |
| TYPE | paper tap, 6 ms, band 1 to 3 kHz, one per typed letter (spaces silent) | −36 dBFS (the boot's at −38) |

| G | t | Cue | Picture |
|---|---|---|---|
| 2 | 1.429 | LAND | the second paste |
| 8.5 | 6.071 | PAPER | the letter comes out |
| 11 | 7.857 | LAND | command |
| 12 | 8.571 | PAPER | fold |
| 13.25 | 9.464 | PAPER | the envelope comes out |
| 15.25 | 10.893 | LAND | the form is printed |
| 16 | 11.429 | SEAL | seal (the music's lift) |
| 16.25 | 11.607 | LAND | output |
| 20 | 14.286 | ARRIVE (F) | on the counter |
| | 24.571, 25.571, 26.571, 27.571 | CLOCK ×4 | the wait, real seconds |
| 40 | 28.571 | CLOCK + BACK | theo is back (the music's return) |
| 43 | 30.714 | ARRIVE (C) | theo's dock |
| 44 | 31.429 | PAPER | the brief comes out |
| 44.5 | 31.786 | LAND | inbox |
| 45.75 | 32.679 | LAND | result |
| 46.75 | 33.393 | PAPER | the answer sheet |
| 49.75 | 35.536 | PAPER | it folds |
| 50.75 | 36.250 | PAPER | the answer envelope |
| 52 | 37.143 | SEAL | its seal |
| 55 | 39.286 | ARRIVE (F) | maya's dock |
| 65 | 46.429 | PAPER | our envelope |
| 67.5 | 48.214 | PAPER | the slit |
| 68, 69 | 48.571, 49.286 | PAPER ×2 | pulled out, unfolds |
| 76 → | 54.286 → | TYPE per letter | signature |
| 80 + boot letter times | 59.04 → 59.72 | TYPE ×9 at −38 dBFS | DORYLINAE typed by the boot |

About 30 cues. The v1 code score stays the fallback for the whole film; its cue grammar maps onto the same events.

---

## 9. Poster frame

**G38, 27.143 s, CAM-R0, 16:9.** Across the top, three plates: maya's machine (left), the post-office rack with its
`Relay` sign (centre), theo's machine (right, his terminal text dimmed and cut by the frame). Under the machines, the
chips: `● maya (you)` and `○ theo offline`, the second beside the envelope's top-right corner. In the centre the sealed
envelope lies on the counter in front of the pigeonholes: two paper tones, the flap, the seal tab with the mark, the
form FROM `QZ_vPMFN` TO `ZhbF0Hsy` TIME `13:41:40 UTC` SIZE `1,444 bytes`. On the counter `NOW 13:41:43 UTC`. Under it
`Theo is offline. The envelope waits.` In one still: two people, their machines, a post office that holds a sealed
envelope and reads its outside, and the wait. No pheromone in the frame.

**4:5, same moment:** the rack, the envelope on the counter, the clock, `○ theo offline` under the counter, the caption.
maya is above the frame (her chip is not in the 4:5 poster).

**Alternate (the analogy close-up): G26, 18.57 s, CAM-R+ (16:9).** The form at 44 px with the pointer under
`1,444 bytes` and `It reads the outside:` / `who, to whom, when, how big.` Deliver both as PNG in both formats.

---

## 10. Risks

1. **The analogy's limits.** The form shows four facts; the relay sees a little more: the full keys (it logs 8-character
   prefixes), a random message id, the type `mail`, when each machine is connected (even when invisible to teammates),
   client IPs (kept up to 14 days), and on the hosted relay the account behind each key. None of it is content. The
   caption says "the outside", with no "only"; the card says nothing about what the relay sees.
2. **Key prefixes look like noise.** The capture's relay is a loopback relay that only has keys; the hosted relay also
   knows the account behind each key. The field captions FROM / TO and the word "who," arriving on top of `QZ_vPMFN`
   carry the meaning. **The film never says the relay cannot tell who you are, and no caption, label or post text may
   say "anonymous", "keys, not names" or "the relay knows nothing".** On theo's machine the TO value matches his own
   `public key:` line, which shows that the address is a key.
3. **Five seconds of offline.** The clock runs in real time over the real 5 s wait. A viewer may still read offline as
   brief; the card's "Requests wait while your teammate is offline." carries the general claim. "Up to 7 days" is not
   claimed (it depends on the relay's setting). If the owner confirms the beta relay keeps the 7-day default, the card's
   line 2 could become "Requests wait up to 7 days while your teammate is offline." (+3 words: re-check the hold).
4. **The paper metaphor.** The brief is the real file, word for word, set as a document; the v1 rule "no re-flow of
   output" may be invoked. Fallback: unwrapped lines cropped by the sheet's right edge (an allowed crop), at the cost of
   legibility.
5. **Card line 3 is not shown in the scene.** "You approve what their agent can read." is true by design: a grant waits
   as `pending_approval` until you type the code in your daemon's own window, or a standing policy you made matches;
   only the session's requester can grant. Boundary (fact-check B4): an agent that runs arbitrary programs as you can
   defeat the window; the mitigation is harness confinement. Never write "only", "never" or "cannot" in that line.
6. **Omitted steps.** Accept, the read grant, its approval, the fetch and the quarantine release happened and are not
   shown. The answer arrives sealed and is never opened on maya's machine, so nothing implies automatic reading. A sharp
   viewer may ask how theo's agent found the cause without the logs; the 90–120 s walkthrough should show the approval.
7. **Security review (O-1).** The film now says "End-to-end encrypted." The docs require testers to be told the beta has
   had no independent security review (HANDOFF D41; `known-limitations.md`). Default: the card's footnote says so, next
   to the claim it qualifies. If the owner declines, delete the line; nothing else moves. Never "secure", "private",
   "audited" or "zero-knowledge".
8. **"A first wave of 10 small teams."** The owner's own team is one of the 10 (fact-check B18). If capacity changes,
   change the numeral only.
9. **The call needs a live page.** "Apply at dorylinae.net" promises an application path on publish day (beta-facts §5:
   no waitlist form exists today). If the page is not live, the film must not ship. The address now first appears at
   53.6 s (in the note), not 60 s.
10. **One machine shown as two; scripted agents.** Two daemons and a loopback relay ran on one host and the capture was
    driven by a script (`capture/INDEX.md`). Captions say "your agent hands it to theirs" and "Theo's agent does the
    work", the documented behaviour; never caption it "real agents, unedited".
11. **4:5 crops.** maya's output line is cut by the frame edge after "(team at"; theo's inbox row inside its title
    ("Track down t"). Both are allowed crops; QA checks they read as a terminal running past the frame, not as damage.
12. **The glyph rides on moving paper.** The mark is printed on a seal tab; it is never animated, scaled out of
    proportion, rotated or taken apart, and appears in single frames. During the return's dip (s 0.7) the compact mark is
    51 px, still inside its 40–96 range.
13. **Bright paper on a dark film.** The darker envelope body `#ECE6F7` and the flap's lighter tone keep the 760 px
    envelope from glaring. Check banding and the jump in average luminance at G8.5 (the white letter), at the card and at
    the invitation on a phone at full brightness.
14. **Physical paper can look cheap if it is wrong.** The hinge folds and the flap need correct perspective, layer order
    and the tone switch at 90°; the slides must start hidden behind the plate (no pop). QA steps frame by frame through
    G12 → G16.5 and G49.75 → G52.
15. **Sky.** Seed 97 is new at these sizes. Contact-sheet every caption; mirror or re-seed (4.11).
16. **Music licence and content claims.** CC BY: the credit goes in the post text and description; archive the track's
    licence page with the delivery. Lee Rosevere distributes to streaming services, so a third-party claim is possible
    on YouTube or Meta: upload privately first there. X, LinkedIn and Reddit do not use Content ID.
17. **Nobody has listened yet.** The track was ranked by measurement (`MUSIC.md` §6). The owner's listen to edit B
    decides. If it is rejected, section 12.4 gives the refit.

---

## 11. Where this script departs from BRIEF-v2 or a review, and why

| Source | Asked | This script | Why |
|---|---|---|---|
| BRIEF-v2 | Postmark `08.x.2026` | none | The post office applies postmarks; maya's machine stamping one is the wrong world. The form already says when. |
| BRIEF-v2 | Card line 1 "The relay only sees the envelope." | `End-to-end encrypted.` alone as the display line | "Only" overclaims (fact-check B16). The scene already showed "cannot open"; 5 fewer words keep the card at 3.4 words a second. |
| BRIEF-v2 | Card line 3 "Nothing reads your files unless you approve it." | "You approve what their agent can read." | "Nothing" is false (your own agent reads your files; B4). Review 1's wording has an actor and an object. |
| BRIEF-v2 | Note "Use it on your real work." / "macOS, Linux and Windows." | dropped | No doc states a tester commitment (beta-facts §2). The platforms line lost to reading time (29 note words in 7.5 s is the limit); the owner can swap it in for `That decides what we change first.` |
| BRIEF-v2 | Note typed | note lines appear whole, in one frame each; only the signature types | Reading pace; ink appears in one frame. |
| Review 1, M5 | Disclosure in the note | on the card, as a footnote | It qualifies "End-to-end encrypted." in the same frame (contiguity), and the note had no reading time left. |
| Review 1, M2 (optional) | Chips in the plates' bottom rows | chips on the sky beside each dock, on the relay side | Same goal, closer: at CAM-R0 theo's chip sits by the envelope's top-right corner; in 4:5 it sits between the counter and the caption, which a plate row could not do. |
| Review 2 | Chip in each machine's header row | as above | as above |
| Review 2 | At theo's machine, slit the envelope and lift the letter out | the envelope goes into theo's machine and the brief comes out of it, unfolded | The exact mirror of maya's side (her letter went in, the envelope came out). Slitting at the dock would leave an empty envelope that must then vanish, and the unfolded brief needs the space the envelope occupies. The seal is never touched on screen. Our envelope (IV) is slit, as asked. |
| Review 2 | Bookend: track the answer back and land on CAM-M | done, with a scale dip to 0.7 at mid-move | The route is 2560 px; the dip shows the pass over the counter (Review 2's mid-wide idea) and lowers the peak speed to 30.6 px a frame (blur window). |
| Review 2 | Match cut from the cold open to the letter's lines | not done | No letter exists at the cut; its lines are written after it. |
| Review 2 | Card lines stepping to ink meta / a pointer | no pointer, no state change | With one-beat staggers there is no time to read line by line; hierarchy carries the card. |
| Review 3 | Card line 3 "Their file access needs your approval." | Review 1's "You approve what their agent can read." | Both are true; this one has a clear actor (you) and object. |
| Review 3 | Real-time clock or beat ticks | real time | 5 s on screen = 5 s in the log, and the last tick lands exactly on the music's return. |
| Review 3 | 0.6 s of bare sky after the address | the address holds to the last frame; the last 0.43 s is silent | The 4 s address hold and the music's end fix the length; a loop restarts on frame 0's complete caption. |

---

## 12. Notes for the build

### 12.1 Files

- **Do not edit v1 files or `lib/`.** v2 lives in `engine/shots-v2/`: `common-v2.js` (the 84 BPM clock `T(G)`, v2
  colour tokens, type roles, the caption slot, the envelope / letter / machine / chip / relay builders), one file per
  section (I to V), `manifest.js` with `FILM.duration = 64.2857` (3857 frames).
- **Copy, do not load:** `FILM.endStage` lives in `shots/s11.js`, which also registers v1's S11; copy the function into a
  v2 file.
- **New skies:** scene seed 97 at 3400 × 1500 (16:9) and 1500 × 2900 (4:5), 2× PNGs via `fieldSvg`, then
  `tools/dedust.py`, then the caption-band check. Card and invitation reuse `sky-inv`; the end reuses `sky-end`.
- **Logo files:** copy `compact-on-light.svg` and `mark-on-light.svg` from `brand/dorylinae-brand/dist/logo/svg/` into
  `engine/assets/` and add them to the asset bundle.

### 12.2 Components to build (all in v2 files)

1. Caption slot with the change grammar and the building line (4.3).
2. Envelope with every state in 4.4: flap as a `rotateX` panel with perspective and the 90° tone switch; the slit.
3. Letter and answer sheet with hinge fold / unfold and the post-fold crease (4.5).
4. Machine: plate + terminal block, 4-line window with scroll and dim (custom: `ui.termText` has no scroll), the slot
   as a clip edge so paper is hidden behind the plate.
5. Chip (custom: `ui.chip` is 28 px tall for 12 px text): 52 px pill, dot, ring fill, pulses (`ui.dot`, `FILM.levelFill`).
6. Relay: rack with pigeonholes, counter, sign (specimen label from the brand formula: pad 0.9 em × 0.7 em, lead
   1.25 em, with its own measurement), clock with in-place digit changes.
7. Address form (custom; the engine's `ui.label` is a 2-line Special Elite label, not a form).
8. Camera state machine (4.2.3 / 5.2.3): `FILM.push` and `FILM.drift` take an `ease` argument, so pass `FILM.CAM`; the
   trucks, tilts and the return's dip are a small custom camera function on `worldCamStable`.
9. Trail (`ui.trail`) and pointers (`ui.underline` with a colour parameter for accent ink).
10. Reused as is: `FILM.cut21`, `FILM.boot`, `ui.typed` (signature, `jitter: 0`), the v1 end-card layout.

### 12.3 Checks before review

- Stills of K1–K7 and section 9 in both formats; a contact sheet every 0.25 s across G8 → G56.
- Frame-by-frame through each paper action (G8.5 → G16.75, G43 → G44.9, G46.75 → G52.5, G65 → G69.5): no pop at a
  layer change, hinge edges exact, the seal one frame after the flap settles.
- Caption clearance in every camera state (4.3); one pheromone element per frame; no text on the rose; the de-dusted
  caption band.
- Measure peak px per frame for every move and confirm the blur windows (4.10).
- `FILM.cue` export per format; picture and music aligned to the grid in section 2 (a `--debug` still shows bar:beat).

### 12.4 If the track changes

1. Keep the anchors on the new track's bars: the 21° cut, the seal, the arrival at the counter, the breakdown (S-6), the
   return (theo back), the card cut, the boot and the address on downbeats; everything else keeps its beat offset from
   the anchor before it.
2. Paper actions, folds, the flap, the 21° cut and the boot keep their millisecond durations; only their start times
   move. Re-check every hold in seconds (section 6) and every peak speed (4.10).
3. At 100 BPM (candidate 4 or v1's score) the same 90 beats last 54 s: add half beats in this order: the wait, the
   brief's hold at theo's, the card, the note, then the address hold, to reach 60 to 64 s.

---

## 13. Changes from review

All must-fix items are applied; should-fix items are applied unless section 11 says why not.

- **Clock and music.** Refitted from a provisional 96 BPM to "Places Unseen" edit B at 84 BPM: 90 beats, 64.286 s. The
  21° cut, the seal, the arrival, the breakdown, the return, the card, the boot and the address sit on the track's
  downbeats; the seal lands on its lift and theo's return on its return.
- **What it is by 5 s (M1).** S-1 is now "With Dorylinae, your agent hands it to theirs.", entering half a beat before
  the cut and bridging it.
- **Proof next to the caption (M2, M3, S5–S7, review 2).** "Theo is back. His machine opens it." lands on the return
  with the ring fill, and the envelope leaves a quarter beat later. Chips hang beside the docks, so theo's sits by the
  envelope at the relay. A white answer sheet carries the real summary and its pointer at theo's dock. `maya (you)`.
  4:5 framings lowered; 4:5 has a tilt toward the form and a slow tilt in the wait.
- **The invitation (M4, S4, review 2, review 3).** The note says "like yours", "Tell us what breaks. That decides what
  we change first.", "Free during the closed beta." and ends with `Apply at dorylinae.net`; it is laid out as a letter
  (creases between paragraphs, 72 / 107 px margins) and signed by a person (O-2). Our envelope uses the same form
  grammar (`FROM Dorylinae` · `TO your team`), comes in along a trail, and is slit open.
- **Security review (M5).** An owner decision (O-1), defaulting to a card footnote.
- **Truth (review 3).** "only" removed from S-4 and the logline; the analogy's limits and risk 2 now say the hosted
  relay binds keys to accounts; the answer's 1,328 bytes is matched by mail id (`capture/22b` line 96); K-2 has no
  duration.
- **The label mapping (S1).** S-4 builds "who, to whom, when, how big." word by word as the pointer steps across the
  form's FROM / TO / TIME / SIZE fields.
- **The wait (S2).** A real-time counter clock `NOW 13:41:4x UTC`, reaching `:45` on the return.
- **Card (S3, review 2, review 3).** Hierarchy: `End-to-end encrypted.` as a Special Elite display line, two Plex Sans
  lines at 64 px (16:9), no moving underline; "Requests wait …" avoids a second "It".
- **Materials (review 2).** The envelope rebuilt as a wallet envelope back (two tones, straight flap, seams, seal tab,
  Plex Mono form, no postmark); paper never fades: it comes out of and goes into the machines, folds on hinges, the flap
  rotates closed; ink appears in one frame. The relay is a counter with a 5 × 4 sorting rack. Machines are a plate with
  an inset terminal block, showing each owner's public key. Focus by dimming text, never by blurring plates. One sky for
  the card and invitation. Captions at −0.01 em on a de-dusted band. Sounds cut to seven kinds.
- **Rhythm (review 2).** theo's part runs one place at a time, no two text events in the same half beat, the unfolded
  brief held 1.16 s (1.6 beats) before the answer covers it, the answer sealed on a downbeat.
- **Geometry and build (review 3).** New measured layouts for both formats; 4:5 2-space continuation indent; letter
  creases drawn only after a fold; blur windows measured; engine gaps listed (12.1–12.2).
