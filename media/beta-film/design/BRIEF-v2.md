# Creative brief v2: "The envelope"

Director's brief for version 2 of the Dorylinae closed-beta film. It replaces the v1 structure. Everything in
`BRIEF.md` (owner decisions, hard rules, honesty, brand) still applies unless this file changes it.

## What the owner said about v1 (the reason for v2)

- "It shows that it went and back, but it didn't connect with me, even though I know my product." "I didn't feel
  invited." "I sometimes couldn't look at both the text and what is happening, because the whole image changed instead
  of changes inside the same card." "The presentation concept was high level, but the material quality, the details,
  weren't there." "One card revealing our best features (e2e encryption) wouldn't have been bad." "The relay doesn't read
  what's in the envelope, just the outside so it can deliver it: like the post office. It makes you use your imagination,
  and that helps you understand and connect." Music: calm but not great; a new approach will be tried.

## What the evidence says (and what v1 got wrong)

- **Split attention / contiguity.** Words placed next to, and at the same time as, the thing they describe improve
  understanding (Mayer's contiguity principle; Ginns 2006 meta-analysis, strongest for complex material). v1 cut 12
  times between different places and kept captions top-left, away from the action. v2 keeps ONE scene and puts the
  caption beside the action, changing in place.
- **Signalling.** Cues that point at the relevant part help (Schneider et al. 2018, 103 studies). The pheromone accent
  marks the one thing to look at; the caption is anchored to it.
- **Analogy for really new products.** A familiar analogy helps people understand really new products (Feiereisen,
  Wong & Broderick 2008); the first label they get shapes their model, and transfer works best when its limits are clear
  (Moreau, Markman & Lehmann 2001). Our own fact sheet already says it: "the relay is a post office that cannot open the
  envelopes" (research/product-facts.md §3.10). v2 is built on it, with its limits stated: outside = who, to whom, when,
  how big; inside = sealed.
- **Feed reality** (research/evidence.md): people decide in about 2 s and most watch muted, so the scene stays still but
  something always moves inside it; every claim is type on screen; sub-minute is best.
- **Features:** lists do badly as an opening for an unknown product, but one short recap card after the viewer has SEEN
  the mechanism is fine and asked for by the owner. At most 3 lines, each something the film just showed.
- **Invitation:** a real, personal, plain voice beats a clever format. v1's "invitation written as a brief" asked the
  viewer to decode a format and never said who is asking or what the tester gets.

## The film (target 55–65 s, 16:9 and 4:5)

1. **Cold open, the pain** (~5 s). Keep v1's idea ("You paste the brief into a chat. They paste it into their coding
   agent.") but with real material quality (larger type, a fuller frame).
2. **The scene** (~30–35 s). ONE continuous scene, no cuts: maya's machine, the relay, theo's machine. The camera may move
   gently inside it (push toward the relay for the "outside only" beat, settle back). Sequence:
   - Your agent writes the brief (real text, appears on maya's machine).
   - It seals it in an envelope. The envelope is the hero object: label-stock paper, hairline flap, the brand glyph as the
     seal, and an address label on its face (a specimen label) with what the relay really sees, from capture/25:
     the sender and recipient key prefixes, the time and the size (`from QZ_vPMFN`, `to ZhbF0Hsy`, `13:41:40 UTC`,
     `1,444 bytes`). Never the brief.
   - The envelope travels to the relay (the pheromone trail). "The relay works like a post office." "It reads only the
     outside: who, to whom, when, how big." "It cannot open the envelope." (end-to-end encrypted.)
   - Theo is offline (hollow ring): "Theo is offline. The envelope waits." It rests at the relay. Time visibly passes.
   - Theo is back (ring fills on a downbeat): the envelope travels on and is opened only on theo's machine: the brief
     appears in his inbox (real inbox line).
   - "Their agent does the work." The real result line appears on theo's machine.
   - "The answer comes back, sealed the same way." A second envelope returns to maya.
   Captions sit in ONE fixed caption slot attached to the scene, close to the envelope's path, and change in place
   (the old line leaves, the new one is typed or set in the same place). Never two captions asking to be read at once.
   Real terminal lines appear on the machines themselves (not on cut-away full-screen terminals).
3. **The card** (~6 s). Three lines, owner-requested, all true:
   - End-to-end encrypted. The relay only sees the envelope.
   - It waits while your teammate is offline.
   - Nothing reads your files unless you approve it.
   (Wording to be checked against research/product-facts.md and fact-check.md; never "secure", "audited", "private".)
4. **The invitation** (~10 s). Now WE send the viewer an envelope: addressed "To: your team", from "Dorylinae". It opens to a
   short plain note on label stock, typed: "We're opening Dorylinae to a first wave of 10 small teams. Use it on your real
   work. Tell us what breaks; it decides what we build next. Free during the beta. macOS, Linux and Windows." signed
   "The Dorylinae team". (Facts: BRIEF.md beta facts; "tell us: that is how we decide what to change first" is from
   Docs/beta/known-limitations.md.)
5. **End** (~8 s). The brand boot, then "Apply at dorylinae.net", held ≥ 4 s.

## Material quality (the owner's main craft note)

v1 took restraint too far: mostly empty sky, small flat plates, 28–42 px type, tiny dots. v2:
- **Scale.** The scene fills the frame. Machines are large terminal plates with readable type (≥ 36 px in 16:9 for any
  line that matters; ≥ 32 px in 4:5). The envelope is big: about a third of the frame width when it is the subject.
- **Real objects, made of the brand's own materials.** The brand has paper (label stock `#F6F3FB`, ink `#17122B`,
  hairlines, Special Elite typed labels), plates (`surface`, 6 px channel), the pheromone trail, presence dots, the glyph
  mark. The envelope, the address label, the postmark (collector date form, e.g. `08.x.2026`), the invitation letter are
  all built from these. Construction detail matters: hairline flap lines, inset label borders, typed text, precise
  alignment, consistent radii.
- **Light and depth within the rules.** No drop shadows on UI, no glass, no gradients between brand colours (brand rules).
  Depth comes from scale, overlap, focus (out-of-focus background plates), the three ground steps, and the deep-field
  sky behind (calm, de-dusted, as in v1).
- **Fewer things, more care.** At most three objects in the scene at once plus the caption.

## Unchanged from v1

Brand voice and type rules; one pheromone element per frame; real capture lines only (allowed edits only); no product
names of coding agents; no unbuilt features; CTA dorylinae.net; 16:9 + separately laid-out 4:5; no voice; 60 fps; the
engine in film/engine (see ENGINE.md), including motion blur windows, FILM.CAM camera ease and worldCamStable.

## Music

The owner wants to try something new: a human-composed track under a clear free licence (CC0 preferred, CC-BY with a
credit line acceptable). The timeline is fitted to the chosen track's bars. The v1 code-composed score stays as the
fallback. Sound design stays minimal and in code (a few soft accents tied to picture events), mixed under the music.

## Owner decisions after the v2 script (final)

- Signature on the invitation note: **Yazan** (exactly).
- The card does NOT carry "No independent security review yet." (SHOW_REVIEW_FOOTNOTE = false); that disclosure goes in the
  invitation, privacy note and terms.
- Music: the owner preferred the code-composed approach ("I like how it was timed"); v2 gets a new code-composed score
  with better instruments and composition, timed to the v2 picture at 84 BPM (film/audio2/). The licensed-track search
  in film/music/ is kept for reference only.
