# v2 score: the instrument palette (sources, licences, measurements, decisions)

Film: Dorylinae closed-beta film v2, 84 BPM (beat 0.714286 s, bar 2.857143 s), 64.2857 s. Owner on v1's coded score: "the
coded one grew on me, I like how it was timed; if only it could have more suitable sounds or a better composition".
This page covers the sounds. Everything here is composed and rendered in code. Every sample is **CC0**, so the film needs no
credit line. Nobody has listened to any of it, me included. Every claim below comes from a measurement or a picture that
this folder regenerates.

Folder: `film/audio2/`. Code is in `palette/`, numbers in `research/`, downloads in `samples/` and licence texts in `licences/`.

## 0. The palette in one table

| Role | Instrument | Source / licence | Why (measured) |
|---|---|---|---|
| **Motif and chords** (lead) | **Osiris Piano**: a worn Yamaha C2 recorded with the una corda pedal down, the lid at half-stick, at very low dynamics; sustain-pedal-down set, mic B (edge), one mic of the pair per note, panned by pitch | `github.com/sfzinstruments/Osiris_Piano` @ 18c6afc (Versilian Studios + Karoryfer, KVRDC 2026), **CC0** | It is a real felt-like piano, so it needs no low-pass filter to imitate one. Mid-register centroid 297 to 583 Hz. Partials 3 and up sit 30 to 90 dB under the fundamental. Hammer energy above 2 kHz is −38 to −48 dB. It has the best noise figures of any piano tested: median SNR 67 dB, worst 57 dB, pre-hammer noise 55 dB under the note. Native 48 kHz. Tails fade to digital zero (104/104 files). The sfz gives per-note damper times. 2 layers, white keys only. |
| **"Tre corde" opening** (the return, the lift) | **VCSL Kawai grand**, layers v2 to v4, with its key-up release samples, one mic per note | `github.com/sgossner/VCSL` @ c1ea7bc, **CC0** | Natural grand: 3 usable layers, 84 release samples. In the same register its 1 to 4 kHz share is −6 to −27 dB, against −25 to −37 dB for Osiris. Layered 6 dB under Osiris at the lift, it opens the tone: the piano stem's 1 to 4 kHz share rises from −30 dB (bar 2) to −25 dB (bar 5), and its centroid from 320 to 373 Hz. |
| **"Paper" colour** | **VCSL vibraphone, soft mallets** | VCSL, **CC0** | Pitch is near-perfect: median 0.1 cent off, every note in the motif register within 6.5 cents. SNR 67 dB, L/R correlation 0.8, 2 layers. |
| **"Seal" and "back" bell** | **VCSL hand chimes** | VCSL, **CC0** | Mellow bell (centroid 743 Hz). Best SNR measured (median 89.9 dB). A note every 1 to 3 semitones from C4 to C7, median 2.8 cents off. |
| **Sparkle** (sparingly) | **Celesta** by stamperadam (Freesound pack 6166), edited for Virtual Playing Orchestra 3; hard layer, left channel only, high-passed | `github.com/open-soundfonts/Virtual_Playing_Orchestra_3` @ b0afa0f `libs/stamperadam`, **CC0** (the pack's Freesound licence file) | It is the only CC0 celesta found, and it has defects that the sampler works around (§3.3). |
| **Sustained bed** | **Designed pad** (additive, in code) + **VCSL bowed vibraphone** (for swells) + **VSCO-2 CE cello and viola sections**, soft layer (warmth at the lift and the return) | pad: our code; bowed vibes: VCSL, **CC0**; strings: `github.com/sgossner/VSCO-2-CE` @ 4403009, **CC0** | The pad has exact pitch and no noise, holds any length and has a spectral envelope that can "open" on a cue. Bowed vibes are within 1.4 cents, 15.8 s long, with a natural 2.3 s swell. The string sections are the only CC0 bowed sections found; they have vibrato only, so they sit low in the mix. |
| **Low end** (the heartbeat) | **VCSL Bass Drum 2** (felt beater, pp and mp, 2 takes each, mono) layered with a **tuned sub** (sine falling from +7 semitones onto D1 in about 40 ms, a little tanh); plus Osiris bass notes voiced softly | VCSL, **CC0**; sub: our code | The drum gives a real felt body (centroid 47 Hz, SNR 69 dB, L/R correlation 1.0). The sub fixes the pitch to the key. |
| **Space** | Two **generated** impulse responses: "chamber" (T30 1.35 s at 1 kHz) and "hall" (T30 2.6 s at 1 kHz), used as sends | our code (`palette/reverb.py`) | No third-party impulse response, so no licence question. T30 lands within 6% of design in every octave. Late L/R coherence goes from 0.8 at 63 Hz to about 0 above 2 kHz, so the bass is mono-safe and the top is wide. |

Demo: `palette/out/palette-demo.wav` (15.0 s, 48 kHz/24-bit, −16.0 LUFS, −1.7 dBTP, LRA 4.2 LU), stems in
`palette/out/stems/`, and the picture `palette/out/palette-demo-qa.png`.

## 1. Where I looked (reachability and licence verdicts)

github.com works through `git clone`, either `--filter=blob:none --sparse` or a blob-less clone and checking out only the
folders needed. The GitHub API answers only for repositories enabled for this session, but code and repository search
through the GitHub connector work. freepats.zenvoid.org works. Freesound and Pixabay are blocked. OpenAIR (the impulse
response library) failed TLS because its certificate has expired, so I did not use it.

| Source | Licence (read from the file in the repository or on the page) | Verdict |
|---|---|---|
| **VCSL** (Versilian Community Sample Library) @ c1ea7bc | CC0 1.0 (`licences/VCSL-LICENSE-CC0.txt`) | Used: Kawai, vibraphone (soft and bowed), hand chimes, bass drum. Also measured: Steinway B (v1), Knight and Yamaha uprights, glockenspiel, marimba, wine glasses, frame drum. There is **no celesta, no bowed strings and no felt piano** in VCSL. |
| **VSCO-2 CE** @ 4403009 | CC0 1.0 (`licences/VSCO-2-CE-LICENSE-CC0.txt`) | Used: cello and viola sections. Measured: violin section, contrabass non-vibrato, and the 2 uprights (`Keys/Upright Nr1` and `Keys/Upright Piano` are the raw sources of VCSL's Yamaha and Knight, which supersede them). No celesta. The section strings are vibrato only. |
| **Osiris Piano** (sfzinstruments, Versilian + Karoryfer, 2026) @ 18c6afc | CC0 1.0 (`licences/OsirisPiano-LICENSE-CC0.txt`; changelog: "Beta 0.923: CC0 license added") | **Used** as the lead piano. |
| Virtual Playing Orchestra 3 (open-soundfonts) @ b0afa0f | The repository is GPL-3 for its sfz files; each library has its own licence. The stamperadam celesta is **CC0** (its Freesound licence file, copied to `licences/`). Other parts are CC-BY-SA 4.0, Iowa "without restrictions", Philharmonia and SSO (Sampling Plus) | Used: the celesta samples only, not VPO's sfz. The rest is not CC0, so I did not use it. |
| FreePats Upright Piano KW (2022-02-21) | CC0 (`cc0.txt` in the archive) | Measured and rejected (§3.1). |
| FreePats Salamander Grand V3 | CC-BY 3.0 | Not needed: CC0 covers every role. |
| sfzinstruments Splendid Grand (Akai) | "released as public domain in early 2000 by Akai", with no dedication document | Excluded: the licence is not clear. |
| Maestro Concert Grand / Headroom Piano / Greg Sullivan E-Pianos | Custom permission / CC-BY 4.0 / CC-BY 3.0 | Not needed. |
| jRhodes3d (Rhodes) | Samples CC-BY-NC for redistribution; "musicians using this to make music: CC0" | Usable for music, but a Rhodes is not in the brief. Noted as an option. |
| Karoryfer/Bigcat cello (CC0), dsmolken double bass | CC0 / not checked | Not needed: a solo cello is too expressive for a bed. |

## 2. Method: what was measured, and how

`palette/analysis.py` measures one file and `palette/survey.py` runs it over 1,419 files in 26 candidate sets. The output
is `research/measurements/<id>.json` per set, `summary.json`, and the table `research/survey-table.md`. Each file gets:

- **format**: sample rate, bit depth, channels, duration; **velocity layers** and **round robins** come from the file
  names (and were checked: a "rr2" that is the only take of a note is not a round robin).
- **noise**: SNR (the loudest 50 ms RMS against the 5th percentile of the 50 ms RMS), plus, for struck sounds, the
  pre-roll noise against the sustain (`noise.py`). For sustained samples neither number is a noise floor, because the
  files start on the bow and their tails are faded. Those are judged from their pre-roll and their stereo.
- **tuning**: YIN decides the octave. A second pass forces the library's own octave convention on files whose YIN octave
  disagreed (YIN locks onto the 2nd partial of a weak bass fundamental). The fundamental partial is then tracked frame by
  frame (Hann window, 4× zero-padding, parabolic peak). When the fundamental is more than 12 dB under the strongest partial,
  an inharmonic partial fit f_h = h·f0·√(1+Bh²) takes over. A file more than 55 cents from its label (judged only on a
  clear fundamental) is re-labelled. Output: cents from 12-TET (A4 = 440 Hz) per file, plus spread (vibrato or drift).
- **attack**: leading silence, first-sample level (a non-zero start means the file was cut inside the attack), 10 to 90%
  rise, and the time from −40 dB, −30 dB and −20 dB to −6 dB of peak.
- **tails**: T30-style decay, level of the last 20 ms against peak, and whether the file ends in digital zero (truncation).
- **stereo**: zero-lag L/R correlation, side/mid ratio, balance, mono-sum loss and inter-channel delay.
- **colour**: spectral centroid (first 400 ms), share of energy above 4 kHz in the first 150 ms; for pianos,
  `timbre.py` gives partials 2 to 8, the 1 to 4 kHz share and the attack energy above 2 kHz, measured on rendered notes.

![tuning](research/figs/tuning.png)
![survey](research/figs/survey.png)

## 3. Results by role

### 3.1 Piano (the lead)

| Candidate | Layers / RR | Sampling | SNR med / worst | Pitch \|c\| median (max C3 to B5) | L/R corr median (p10) | Mono loss | Tails | Verdict |
|---|---|---|---|---|---|---|---|---|
| **Osiris, sustain down, mic B** | 2 / none | white keys, 48 kHz/16 bit FLAC | 66.8 / 57.3 dB | 5.9 c (7.5) | 0.6 (−0.1) | −1.2 dB median, −4.7 dB worst | all fade to zero | **Lead** |
| Osiris mic A (inside) | 2 / none | same | 67.5 / 54.4 | 6.0 (9.3) | 0.1 (**−0.6**) | worst −11.0 dB (42 of 104 files worse than −3 dB) | zero | Rejected for stereo |
| Osiris mic C (outside) | 2 / none | same | 70.6 / 57.5 | 6.3 (7.6) | 0.5 (−0.2) | worst −10.2 dB; arrives 1.3 ms late (comb filter if summed) | zero | Our room replaces it |
| VCSL Steinway B (v1) | 3 / none | every 2 st, 44.1 kHz/24 bit, peak-normalised | 63.2 / 50.5 | 3.9 (6.0) | 0.4 (−0.4) | −1.6 | faded to −103 dB | Fallback |
| VCSL Kawai | 4 (v1 unusable) / none | every 3 st, 44.1 kHz/16 bit, natural levels, octave labelled one low | 62.0 / 21.7 (v1) | 5.2 (10.7) | 0.4 (−0.3) | −1.7 | faded | **Opening layer** (v2 to v4) |
| VCSL Yamaha upright | 3 / **3** | **C and G only** (shifts up to ±3.5 st) | **78.1** / 54.0 | 3.6 (8.9) | **0.1 (−0.7)**; mid register −0.63 | **−7.2 dB** in the motif register | ok | Rejected: too sparse, anti-phase |
| VCSL Knight upright | 2 / none | every 2 st | 50.5 / 36.1 | 8.1 (9.6) | 0.7 | −1.0 | **28 tails cut at −35 to −44 dB** | Rejected |
| FreePats Upright Piano KW | 2 / none | every 3 st | **39.7** / 23.4 (living room, Zoom H1) | 6.5 (13.3) | 0.8 | −0.6 | 34 cut | Rejected |

Timbre, measured on rendered notes (`research/piano-timbre.json`); partials are in dB under the fundamental:

| Note, velocity | Osiris B | Kawai v2 to v4 | Steinway B (unfiltered) |
|---|---|---|---|
| D4, 0.3: partials 2 / 3 / 4, centroid, 1 to 4 kHz share, attack > 2 kHz | −19 / −40 / −32, 297 Hz, −31.8 dB, −47.6 dB | −7 / −20 / −18, 386 Hz, −14.4, −29.3 | −6 / −13 / −12, 428 Hz, −12.3, −19.8 |
| A4, 0.7 | −13 / −30 / −49, 457 Hz, −30.1, −41.3 | −7 / −11 / −28, 598 Hz, −11.4, −13.8 | −22 / −13 / −30, 494 Hz, −13.1, −11.8 |
| D5, 0.7 | −25 / −44 / −51, 583 Hz, −25.3, −39.0 | −23 / −28 / −31, 599 Hz, −20.3, −16.0 | −7 / −20 / −26, 768 Hz, −6.6, −17.5 |

What the numbers say:

- Osiris is what v1 had to fake with a 3.8 kHz low-pass on a Steinway: a soft, felted tone with almost no hammer. It is
  10 to 30 dB lower in the 2 kHz attack band than either grand. It is also very dark. On its own it could vanish on a phone.
  The bus therefore gets presence EQ: low shelf −3 dB at 160 Hz, +4 dB at 2.4 kHz (Q 0.6), +3 dB shelf at 6.5 kHz. Bass
  notes are voiced 3 dB softer, because a low Osiris note carries about 10 times the energy of a mid one. Through a modelled
  phone speaker (2nd-order 350 Hz high-pass, 9 kHz low-pass), the demo loses 3.9 LU. v1 lost 3.0 to 4.6 LU over comparable
  passages. Exposed motif notes lead everything else through that speaker by 5.7 to 12.6 LU.
- **Stereo defect found**: Osiris was recorded with spaced pairs, so on these near-sine notes some pitches sit close to
  anti-phase between L and R. Mic B: E4 −0.55, A4 −0.38, 13 files worse than −3 dB in mono. Mic A: G4 −0.89, which is
  −11 dB in mono. Aligning the pair on the attack, which I measured, does not fix it: the sustained phase is set by the
  soundboard's modes, not by a single delay (negative files: 14 before, 10 after). The sampler therefore plays **one mic of
  the pair** per note: the louder one, L for 50 notes and R for 54, with the other channel a median 1.4 dB quieter. A single
  mic cannot cancel itself. Width comes from pitch panning (±0.35, low notes left as seen from the bench) and from the rooms.
  Demo result: mono minus stereo −0.12 LU; per-band mono loss ≤ 1.1 dB; L/R correlation never below 0.56 in any 400 ms
  window.
- Names: the Osiris and Kawai files are labelled one octave below the pitch they sound. The sfz `pitch_keycenter` confirms
  it for Osiris (`A-1` file = key 21), and f0 confirms both. Osiris tuning keeps the piano's own stretch: a robust cubic is
  fitted to the measured cents, A4 is pinned to 440 Hz, and only each note's deviation from the curve is removed. The treble
  stays +20 to +43 cents above 12-TET, which is normal piano stretch.
- Kawai v1 (pp) is 34 dB under v2 with a worst SNR of 21.7 dB, so it is excluded. The grand uses v2 to v4.

### 3.2 Sustained bed

| Candidate | Notes / layers | Pitch | Stereo | Character | Verdict |
|---|---|---|---|---|---|
| **Designed pad** (`synth.py`) | any | exact (12-TET, ±1.2 cent seeded drift per voice by design) | 3 voices panned ±0.7 | additive, harmonically locked, Gaussian spectral envelope that can open (600 → 2,200 Hz over 1.6 s at the lift), per-partial shimmer, band-limited (no partial above 9 kHz) | **Foundation** |
| **VCSL bowed vibraphone** | 6 notes a fifth apart (sounding A3 to E6), 15.8 s | median 0.8 cent, max 1.4 | corr 0.7 (p10 0.2) | 2.3 s natural rise; ideal for swells into a cue | **Swells** |
| **VSCO-2 cello section** (susvib) | every 3 to 4 st, C2 to F5, 2 layers | median 2.7 c; vibrato spread about 19 c (low register up to 140 c, so ±10 c uncertainty there) | corr 0.5 (p10 0.1) | warm, centroid 409 Hz | **Warmth**, soft layer, under the pad |
| **VSCO-2 viola section** | every 2 to 4 st, C3 to D6 | 2.2 c | corr 0.7 | centroid 915 Hz | **Warmth** (soft layer only; layer 2 starts loud: its quietest 20 ms in the first 300 ms is only 19 dB under the sustain) |
| VSCO-2 violin section | every 4 st | 3.3 c | **corr 0.0 (p10 −0.1)**, mono −3.1 dB | 16 bit, thin | Not used |
| VSCO-2 contrabass, non-vibrato | every 4 st | 1.2 c | 0.3 | rumble in the pre-roll | Optional low pedal; not needed |
| VCSL wine glasses | 4 notes | 18 c off | corr −0.2 | median SNR 21.8 dB, 8 of 8 tails cut | Rejected |

The demo uses the sustain extension on the bowed vibraphone (equal-power crossfaded segments, §5).

### 3.3 Mallet colour

| Candidate | Notes / layers / RR | Pitch | SNR | Stereo | Verdict |
|---|---|---|---|---|---|
| **VCSL vibraphone, soft mallets** | 11 notes (F3 to E6, every 3 to 4 st), 2 layers, 1 take | median 0.1 c, max 6.5 c (mid) | 67.2 | corr 0.8 | **Paper / seal** |
| **VCSL hand chimes** | 19 notes, C4 to C7 | median 2.8 c, max 13.8 c (corrected) | **89.9** | corr 0.8 | **Seal / back bell** (replaces v1's glass sine) |
| **Celesta (stamperadam)** | 9 usable notes (hard layer: C4, E4, G#4, E5, G#5, C6, E6, C7, E7), 2 layers | several files mislabelled: every "g" file is really **G#** (+97 c, re-labelled automatically); top register +24 to +41 c (corrected); `g7` excluded (fundamental 60 dB under another partial) | soft layer normalised to −1 dBFS, which lifts its floor to −47 to −58 dBFS: only the **hard** layer is used | **L/R correlation down to −0.85** (near anti-phase), so the **left channel only** is used | **Sparkle, sparingly**; high-passed at 140 Hz (its noise is mostly below 120 Hz) |
| VCSL glockenspiel | C and G only, 3 layers | median 13.7 c off (correctable) | 57.7 | corr 0.9 | Too bright for "calm" (centroid 3.2 kHz, > 4 kHz share −1.5 dB); not used |
| VCSL marimba, soft | 10 notes | 2.1 c | 56.8 | corr 0.2 (p10 −0.6) | Option for the LAND / CLOCK wood ticks (centroid 222 Hz); play one channel |

### 3.4 Low end

VCSL Bass Drum 2, pp and mp: 2 takes per dynamic, mono (correlation 1.0), centroid 47 Hz, SNR 69 dB, 4.1 s decay. The
pre-roll has rumble 37 dB under the hit, almost all below 120 Hz. The clip starts 30 ms before the attack and the bus has a
28 Hz high-pass. The drum is layered with the code sub on D1 (26) for pitch. In the demo, each LUB is 10 to 16 LU under the
piano's nearby maximum, each DUB 15 to 20 LU under, and the seal's LUB 9.9 LU under. The exception is bar 1, where the
piano plays only a soft low D (4.7 LU). The VCSL frame drum (HitMuted, centroid 187 Hz, 2 takes) is the alternative for the
SEAL "felt stamp".

## 4. Reverb: generated, measured

`palette/reverb.py` builds a stereo impulse response from two parts:

1. Image-source early reflections of a shoebox room up to order 3, as heard by an ORTF-like cardioid pair. Each bounce is
   darker than the last, and the early part is high-passed at 250 Hz: below that a room's early field is modal, not
   discrete, and the first build, with full-band spikes, made the bass EDT collapse to 10 to 50 ms.
2. A late tail of Gaussian noise in 9 octave bands, each band with its own T60 and its own designed L/R coherence, fading
   in over the mixing time.

Measured with Schroeder integration (`research/reverb-ir.json`, `research/figs/ir.png`):

| IR | Pre-delay | T30 at 125 / 500 / 1k / 2k / 4k / 8k Hz (design) | EDT at 1 kHz | Late coherence 63 Hz → 8 kHz | C80 | Echo density reaches 0.9 at |
|---|---|---|---|---|---|---|
| chamber | 5.7 ms (+ER) | 1.76 / 1.48 / 1.35 / 1.15 / 0.91 / 0.62 s (1.7 / 1.45 / 1.35 / 1.15 / 0.9 / 0.6) | 1.42 s | 0.81 → 0.01 | +1.0 dB | 30 ms |
| hall | 21.6 ms | 3.25 / 2.79 / 2.59 / 2.15 / 1.72 / 1.15 s (3.2 / 2.8 / 2.6 / 2.2 / 1.7 / 1.1) | 2.75 s | 0.80 → 0.07 | −6.1 dB | 60 ms |

Use: IRs scaled to unit energy, so a send level reads as the wet/dry ratio, with mono-in, stereo-out convolution (scipy
`fftconvolve`, exact and deterministic). **Sends are high-passed at 160 Hz.** In the first render the pedal-down bass notes
made the chamber return almost as loud as the dry piano (−14 against −11 dB at bar 4). Final demo: the chamber return peaks
9.5 LU under the dry piano, the hall 12 LU under. pedalboard's `Convolution` was not used, because it normalises the IR
(TOOLING §2.3).

![IRs](research/figs/ir.png)

## 5. The sampler (`palette/sampler2.py`)

About 590 lines of numpy and scipy, with no sfz engine. Measurements are cached in `palette/cache/<instrument>.json`,
keyed by a file hash and the measurement version.

- **Any layout**: folder + glob + a filename regex with groups `note`, `layer`, `rr`. Layers are ordered by name (pp < mf
  < f, soft < medium < loud, L < H, 1 < 2 …).
- **Velocity**: hard layer switching, because crossfading two unrelated takes comb-filters. Level follows **one continuous
  curve per note**: anchors at each layer's own measured level, 12 dB below the softest layer at velocity 0, 3 dB above
  the loudest at velocity 1. Natural libraries keep their recorded levels; normalised ones get a table. The sweep in
  `research/sampler-proof.json` is monotonic for every instrument tested. Maximum step per 0.05 of velocity: 2.4 dB
  (Osiris), 3.6 dB (Kawai), 2.4 dB (vibes), 1.2 dB (celesta). Range over 0.05 to 1.0: 20, 31, 31 and 14 dB.
- **Round robins**: cycled per (sampled note, layer) in render order. Proof: Yamaha upright G3 vl3 plays rr1, rr2, rr1,
  rr2; bass drum pp plays take 1, 2, 1, 2. Kawai, Osiris and the vibraphone have **no** real round robins: a single "rr2"
  file is just the only take.
- **Tuning correction**: f0 is measured per sample (two-pass, as in §2), and each note is resampled by
  (target − sounding root − measured cents/100) semitones. `tune="et"` puts every note on 12-TET. `tune="stretch"` (the
  pianos) keeps the instrument's smooth stretch curve with A4 on 440 Hz. Mislabelled files are mapped to the pitch they
  really sound. Proof (`palette/test_sampler.py`, rendered notes re-measured, error against target):

  | Instrument | Notes | Pitch error max / median | Attack at 30.00 ms ± |
  |---|---|---|---|
  | Osiris (pedal down) | 26 | 1.34 / 0.18 cent | 0.04 ms |
  | Osiris (damped) | 4 | 0.07 / 0.01 | 0.02 |
  | Kawai | 21 | 0.49 / 0.12 | 0.04 |
  | vibraphone | 12 | 0.15 / 0.01 | 0.06 |
  | hand chimes | 4 | 0.01 / 0.00 | 0.04 |
  | celesta | 6 | 0.03 / 0.01 | 0.10 |
  | bowed vibraphone | 6 | 0.05 / 0.03 | 0.06 |
  | cello section | 4 | 10.5 / 0.25 (D2: vibrato spread 140 c, so the low cello pitch is uncertain to about ±10 c either way) | 0.04 |
  | viola section | 4 | 5.2 / 0.5 (vibrato) | slow bow: −9 ms |

- **Resampling**: one polyphase step (`resample_poly`) with our own Kaiser FIR (β = 10, 96 taps per phase, cutoff 0.90 ×
  Nyquist). The rational ratio is accurate to < 0.003 cent. Measured: passband ripple 0.0001 dB up to 18 kHz, images below
  −103 dB, gain error below 0.0001 dB.
- **Timing**: each sample's **attack point** is measured. For struck sounds it is where the 1 ms envelope reaches peak −
  15 dB, a proxy for the perceived attack. Osiris has 8 to 21 ms of key and hammer noise before its tone, and aligning at
  −30 dB would have put that noise on the beat and the tone late. For sustained sounds it is −30 dB, where the bow starts.
  Each clip is cut so that this point sits exactly `PREROLL` = 30 ms after the clip start, keeping the pre-noise; the cut
  gets a 2 ms fade (20 ms for sustained sounds). `place(buf, clip, t)` puts the attack point on t.
- **Release**: with `dur=None`, a pedal-down sample rings out. With `dur`, an exponential damper falls to −60 dB at the
  release time. Osiris uses its own per-note sfz times (for example D4 0.54 s, A2 0.70 s). Where the library has key-up
  release samples (Kawai), one is mixed in at note-off and scaled by how far the note has decayed: −25 dB re the note for
  D4, −20 dB for A2. No step at note-off reaches the click threshold (max 2nd-difference ratio 11 to 20 dB against 29.5 dB),
  and every clip ends at exactly 0.
- **Sustain extension**: a held sustained note is lengthened with equal-power crossfaded segments (0.8 s fades) taken from
  the steady part at seeded offsets. There is no loop point, so there is no loop click.
- **Stereo modes**: `keep`, `mono`, `left`, `best` (the louder mic of the pair, mono-safe), `align` (attack-aligned mid,
  kept for the record: it does not fix modal anti-phase), plus `width` and pitch panning.
- **One-shots** (`OneShot`): unpitched drums with round robins, the same attack alignment and an optional high-pass.

## 6. The 15-second demo (`palette/demo.py`, `palette/qa.py`)

The demo is a miniature of the film's first five scene bars, so every role is heard in context: bed and heartbeat; the v1
motif A4–F#4–D4 on Osiris; Bm7 with the "paper" colour (vibraphone F#5, celesta A6/F#6); Gmaj9 → Asus4 with the bowed
vibraphone swelling and the strings entering; **the lift** (bar 5, 11.429 s), with the felt drum and sub, hand chime D6 +
vibraphone D5 as the "seal", the Kawai layered under the Osiris Dmaj9 roll, the pad opening, the strings blooming, and the
motif answering D5 and A4. 74 events, all on the 84 BPM grid.

| Check | Result |
|---|---|
| Loudness / true peak / LRA (ffmpeg ebur128) | −16.0 LUFS / −1.7 dBTP / 4.2 LU. The limiter did no gain reduction (0.0 dB): one static gain of +0.66 dB. |
| First and last sample; DC | 0 and 0; −102 dBFS |
| Clicks (2nd difference of a 6 kHz high-pass ≥ 30× its 50 ms median, excluding each note's own attack) | none, in the mix and in all 6 dry stems |
| Pitch of every melodic note ≥ C4, measured in its stem | median 0.26, p90 2.6, max 5.8 cents. The max is F#4 over a ringing D2, whose stretched 5th partial sits 7 cents away (a just third against an equal-tempered one); in isolation the same note is 0.2 cents off. |
| Attack timing (isolated events) | within 0.34 ms of the grid; the sampler proof gives 30.00 ± 0.10 ms for every struck instrument |
| Mono | mono − stereo −0.12 LU; band losses −0.36 / −0.28 / −1.08 / −0.38 dB (< 200 / 200 Hz to 2 kHz / 2 to 8 kHz / > 8 kHz); minimum 400 ms L/R correlation 0.56; side/mid −11.4 dB |
| Phone speaker (350 Hz to 9 kHz model) | −3.9 LU (v1: −3.0 to −4.6). Exposed motif notes lead by 5.7 to 12.6 LU. |
| Balance | bed (pad + bowed + strings) 11 to 15 LU under the piano's peak in each bar; mallet accents 8 to 10 LU under the nearby piano; reverb returns 9.5 LU (chamber) and 12 LU (hall) under the dry piano |
| Spectrum | centroid 304 Hz (v1's whole mix: 336 Hz); 1 to 4 kHz share −20 dB (v1 −13.5). Warmer than v1, by design; at the lift the piano opens to −25 dB with a 373 Hz centroid. |
| Determinism | two renders give identical audio (sample md5 6c3964eba8c3, report md5 835769cb…). The float stem files differ only in libsndfile's PEAK-chunk timestamp. |
| Render time | about 30 s on 4 vCPU (the first build of the measurement cache takes about 40 s more) |

![demo QA](palette/out/palette-demo-qa.png)

## 7. Decisions, and what the composer gets

1. **Lead: Osiris (mic B, one mic per note, pitch-panned)**, with the presence EQ above and bass voiced softly.
   **The return/lift: add the Kawai 6 dB under**: the una corda "opens", which fits "Theo is back" and the seal lift.
   Steinway B (v1) remains the fallback.
2. **Bed: the designed pad, plus bowed vibraphone swells into cues, plus soft cello and viola sections at the lift and
   the return only.** The strings never carry the bed alone, because they are vibrato-only and have few notes.
3. **Colour: vibraphone (soft) for "paper", hand chimes for "seal" and "back"** (this replaces v1's glass sine), and the
   celesta for one or two sparkles per section, hard layer, left channel, near its sampled roots (C4, E4, G#4, E5, G#5, C6,
   E6, C7, E7).
4. **Low end: felt bass drum + tuned sub** for the heartbeat; Osiris bass notes for harmony; reverb sends high-passed at
   160 Hz.
5. **Space: generated chamber + hall**, used as sends.
6. **Licences: all CC0** (VCSL, VSCO-2 CE, Osiris Piano, stamperadam's Freesound pack). No credit line is required. An
   optional courtesy line for the description: "Samples: Versilian Studios (VCSL, VSCO 2 CE), Versilian Studios &
   Karoryfer Samples (Osiris Piano), stamperadam (celesta), all CC0."

Use from the score:

```python
import sys; sys.path.insert(0, "<film>/audio2/palette")      # run with tools/.venv/bin/python -I
import instruments as I
from sampler2 import place, SR
from synth import pad, sub
P = I.palette()       # piano, piano_damped, grand, vibes, chimes, celesta, bowed, cellos, violas, bassdrum
clip = P["piano"].note(69, 0.55)                 # A4; dur=None rings (pedal down); dur=x damps with the sfz time
place(buf, clip, t)                              # the attack point lands on t (clips start PREROLL = 30 ms earlier)
P["bowed"].note(69, 0.6, dur=5.2, attack=0.4)    # held any length (crossfaded extension)
P["bassdrum"].hit("pp", gain_db=-10)             # round robins cycle
```

Ranges and maximum shifts: Osiris A0 to C8 (±1 st); Kawai about A0 to C8 (±1.5 st); vibes F3 to E6 (±2); chimes C4 to C7
(±1.5); celesta C4 to E7 (up to ±4: stay near the roots); bowed vibes A3 to E6 (±3.5); cellos C2 to F5; violas C3 to D6.

## 8. Limits and risks

- **Nobody has listened.** Timbre judgements ("felt-like", "warm") rest on measured centroid, partials and attack energy
  and on how they compare with v1, not on hearing. The piano choice is the one to audition first: `palette/out/stems/piano.wav`.
- Osiris has 2 layers and white keys only: black keys are their neighbours shifted by one semitone (as the library's own sfz
  does). With no round robins, repeated fast notes at the same velocity repeat the same sample, so the composer should vary
  the velocity.
- The una corda tone is dark by nature. The presence EQ cannot add partials that are not there; the Kawai layer and the
  mallet doubling are the tools for clarity.
- The celesta is a weak sample set: 16 bit, 9 notes, near-anti-phase stereo, mislabelled files. All of that is handled,
  but keep it sparse.
- The VSCO-2 sections have vibrato (spread about 20 cents), so their low-register pitch is uncertain to ±10 cents. Keep
  them soft and under the pad.
- The demo is mastered to −16 LUFS as a timbre demo. The film master stays at −14 LUFS / −1 dBTP (TOOLING §2.2), with the
  same limiter.

## 9. Files

```
audio2/
  PALETTE.md                this page
  palette/
    analysis.py             per-file measurements (YIN + partial tracking/fit, attack, noise, tails, stereo, colour)
    survey.py               runs analysis over the 26 candidate sets -> research/measurements/
    noise.py, timbre.py     noise detail; piano timbre on rendered notes
    sampler2.py             the sampler (layers, round robins, tuning, attack alignment, release, sustain extension, OneShot)
    instruments.py          the recommended palette as ready instruments
    synth.py                designed pad and heartbeat sub
    reverb.py               generated chamber/hall IRs + their measurement
    test_sampler.py         sampler proof -> research/sampler-proof.json
    demo.py, qa.py, plot.py the 15 s demo, its measurements, the pictures
    manifest.py             every sample file the palette can play, with md5 -> research/manifest.json
    out/                    palette-demo.wav, stems/, demo-events.json, demo-qa.json, palette-demo-qa.png, ir-*.wav
    cache/                  per-instrument measurement cache (rebuilt automatically)
  research/                 measurements/*.json, summary.json, survey-table.md, sampler-proof.json, reverb-ir.json,
                            osiris-align.json, piano-timbre.json, manifest.json, figs/*.png, licence notes, file trees
  licences/                 CC0 texts of VCSL, VSCO-2 CE, Osiris, FreePats; the celesta pack's licence file
  samples/                  sparse clones (vcsl, vsco2, Osiris_Piano), the celesta files, FreePats Upright KW (about 2.9 GB)
  .venv-plot/               matplotlib only, for plot.py (the audio code runs in tools/.venv)
```

Rebuild: `cd audio2/palette && PY=../../tools/.venv/bin/python && $PY -I survey.py && $PY -I test_sampler.py && $PY -I demo.py && $PY -I qa.py && ../.venv-plot/bin/python -I plot.py`.
