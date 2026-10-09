// S8 · The way back · 38.400 → 40.800 (SCRIPT.md S8, §3.5). Hard cut in; holds its last frame 0.6 s under
// S9's 21° cut. The answer comes back to maya's machine and passes the relay without stopping: the opposite of S5.
FILM.shot({ id: 'S8', start: 38.4, end: 40.8, post: 0.6, build(root) {
  const F = FILM, { FMT, S, seg, LINEAR } = F;
  const P = FMT.portrait;
  const G = F.stageW.G;
  const len = G.a1 - G.a0, pitch = G.pitch, fd = 2 * G.r;

  const stage = F.stageW(root, {
    level: { maya: () => 1, theo: () => 1 },
    pulses: { maya: [38.4], theo: [38.7] },
    arrivals: { maya: [39.6] },
    relayLabel: { present: true },
    trail: {
      dir: -1,
      // the return trail draws theo → maya, one continuous line through the relay (no stop, no holding ring)
      head: (t) => (t < 38.4 ? -1e9 : seg(t, 38.4, 1.2) * len),
      // marching toward maya; stops on the arrival at 39.600
      march: (t) => (pitch * F.stageW.flow(t, 38.4, 39.6)) / 0.6,
      // 39.600 → 40.800: tail fade, linear, from theo's end
      tail: (t) => (t < 39.6 ? -1e9 : -fd + LINEAR((t - 39.6) / 1.2) * (len + 2 * fd)),
    },
    // camera s 1.00 → 1.02 about the frame centre
    cam: F.drift({ t0: 38.4, t1: 40.8, s0: 1, s1: 1.02 }),
  });

  const C11 = S.caption('The answer comes back.');       // present at the cut
  stage.screen.append(C11.el);

  // ---------------------------------------------------------------- sound (SCRIPT.md §5.3)
  F.cue(38.4, 'LUB', { desc: 'maya pulse' });
  F.cue(38.4, 'TICK', { pan: 0.5, desc: 'the way back' });
  F.cue(38.4, 'PIANO', { note: 'D3 F#3 A3 D4 E4', roll: 0.06, desc: 'D upward roll' });
  F.cue(38.7, 'DUB', { desc: 'theo pulse; last heartbeat of the film' });
  F.cue(39.0, 'TICK', { pan: 0, desc: 'passes the relay' });
  F.cue(39.6, 'DYAD', { pan: -0.5, note: 'A4 D5', desc: 'arrival at maya' });

  return (tl, t) => {
    stage.render(t);
    C11.render(t);
  };
} });
