// S2 · With Dorylinae · 3.600 → 9.600 (SCRIPT.md S2, §3.5). Entered by 21° cut #1.
// Name and definition in one sentence; two machines, one hand-off; who it is for. Poster frame at 7.200.
FILM.shot({ id: 'S2', start: 3.6, end: 9.6, enter: 'cut21', build(root) {
  const F = FILM, { C, FMT, S, seg, LINEAR, clamp } = F;
  const P = FMT.portrait;
  const T0 = 3.6, T1 = 9.6;
  const G = F.stageW.G;
  const len = G.a1 - G.a0, pitch = G.pitch, fd = 2 * G.r;

  const stage = F.stageW(root, {
    // 3.900: plate groups lock in, maya then theo 45 ms apart (500 ms). Both online.
    lock: { maya: 3.9, theo: 3.945 },
    relayDotIn: 3.9,
    level: { maya: () => 1, theo: () => 1 },
    pulses: { maya: [4.8, 7.2], theo: [5.1, 7.5] },
    arrivals: { theo: [6.0] },
    trail: {
      dir: 1,
      // 4.800: the head draws maya → theo through the relay point over 1.2 s (brand ease)
      head: (t) => (t < 4.8 ? -1e9 : seg(t, 4.8, 1.2) * len),
      // marching one pitch per beat, stopping at the arrival (6.000, 120 ms)
      march: (t) => (pitch * F.stageW.flow(t, 4.8, 6.0)) / 0.6,
      // 7.200 → 9.600: tail fade, linear, from maya's end
      tail: (t) => (t < 7.2 ? -1e9 : -fd + LINEAR((t - 7.2) / 2.4) * (len + 2 * fd)),
    },
    // camera s 1.00 → 1.03 over the shot about the frame centre (brand ease)
    cam: F.drift({ t0: T0, t1: T1, s0: 1, s1: 1.03 }),
  });

  // C3: present in the incoming frame. The word "Dorylinae" is amethyst.
  const B = C.brand;
  const C3 = P
    ? [S.caption('With Dorylinae,', { line: 0, spans: [{ text: 'With ' }, { text: 'Dorylinae', colour: B }, { text: ',' }] }),
      S.caption('your agent hands it', { line: 1 }),
      S.caption('to theirs.', { line: 2 })]
    : [S.caption('With Dorylinae, your agent', { line: 0, spans: [{ text: 'With ' }, { text: 'Dorylinae', colour: B }, { text: ', your agent' }] }),
      S.caption('hands it to theirs.', { line: 1 })];
  // C4 enters at 6.000 (240 ms, rise 12 / 18 px), lands 6.240
  const C4 = S.caption('For small teams.', { baseline: P ? 410 : 360, enter: { at: 6.0 } });
  for (const c of [...C3, C4]) stage.screen.append(c.el);

  // ---------------------------------------------------------------- sound (SCRIPT.md §5.3)
  F.cue(3.5, 'PAD', { note: 'Dsus2 D2 A2 E3 F#3', attack: 1.2, desc: 'PAD in, product colour (6-frame pre-lap into S2)' });
  F.cue(3.5, 'PIANO', { note: 'Dadd9 D3 A3 E4 F#4', roll: 0.03, desc: 'pre-lap (J-cut) into S2' });
  F.cue(3.6, 'THUMP', { note: 'D2', desc: '21° cut #1' });
  F.cue(3.9, 'THOCK', { desc: 'plates lock' });
  F.cue(4.8, 'LUB', { desc: 'maya pulse; trail leaves maya' });
  F.cue(4.8, 'TICK', { pan: -0.4 });
  F.cue(5.1, 'DUB', { desc: 'theo pulse' });
  F.cue(5.4, 'TICK', { pan: 0 });
  F.cue(6.0, 'DYAD', { pan: 0.4, note: 'A4 D5', desc: 'arrival at theo' });
  F.cue(7.2, 'LUB', { desc: 'maya pulse' });
  F.cue(7.2, 'PIANO', { note: 'Gmaj7/D D3 G3 B3 F#4', roll: 0.03 });
  F.cue(7.5, 'DUB', { desc: 'theo pulse' });

  return (tl, t) => {
    stage.render(t);
    for (const c of C3) c.render(t);
    C4.render(t);
  };
} });
