package debate

import (
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

func TestFullEntriesValid(t *testing.T) {
	for _, fe := range fullEntries {
		e, canon, err := DecodeEntry(fe.kind, clone(t, fe.full()))
		if err != nil {
			t.Fatalf("%s: %v", fe.kind, err)
		}
		if e.Kind() != fe.kind {
			t.Fatalf("%s: Kind() = %s", fe.kind, e.Kind())
		}
		if err := Validate(e); err != nil {
			t.Fatalf("%s: Validate: %v", fe.kind, err)
		}
		if _, err := ParseEntry(fe.kind, canon); err != nil {
			t.Fatalf("%s: ParseEntry(canonical): %v", fe.kind, err)
		}
	}
	if _, _, err := DecodeEntry("revision", clone(t, fullPosition())); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if ValidKind("revision") || !ValidKind(KindAnswer) {
		t.Fatal("ValidKind")
	}
}

// TestFreeTextRule is the 3.2 acceptance table: in every string of every
// entry kind, \n and \t are accepted only in argument; every other control
// character (C0, DEL, C1, U+2028, U+2029) is refused everywhere; and only
// argument may start or end with white space. The error names the field.
func TestFreeTextRule(t *testing.T) {
	chars := []struct {
		name      string
		s         string
		inArgment bool // accepted inside argument
	}{
		{"LF", "\n", true},
		{"TAB", "\t", true},
		{"CR", "\r", false},
		{"NUL", r(0x00), false},
		{"ESC", r(0x1b), false},
		{"DEL", r(0x7f), false},
		{"NEL (C1)", r(0x85), false},
		{"CSI (C1)", r(0x9b), false},
		{"U+2028", r(0x2028), false},
		{"U+2029", r(0x2029), false},
	}
	for _, fe := range fullEntries {
		base := fe.full()
		strs := 0
		for _, n := range paths(base) {
			orig, ok := n.val.(string)
			if !ok {
				continue
			}
			strs++
			isArg := n.key == "argument"
			for _, c := range chars {
				err := mutate(t, fe.kind, base, n.path, func(m node) { m.set(orig + c.s + "x") })
				if isArg && c.inArgment {
					if err != nil {
						t.Errorf("%s %s: %s refused in argument: %v", fe.kind, n.path, c.name, err)
					}
					continue
				}
				if err == nil {
					t.Errorf("%s %s: %s accepted", fe.kind, n.path, c.name)
					continue
				}
				wantField(t, err, n.path)
			}
			for _, ws := range []string{" ", "\t", r(0xa0), r(0x3000)} {
				for _, v := range []string{ws + orig, orig + ws} {
					err := mutate(t, fe.kind, base, n.path, func(m node) { m.set(v) })
					if isArg {
						if err != nil {
							t.Errorf("%s %s: white space refused in argument: %v", fe.kind, n.path, err)
						}
						continue
					}
					if err == nil {
						t.Errorf("%s %s: leading/trailing white space %q accepted", fe.kind, n.path, v)
						continue
					}
					wantField(t, err, n.path)
				}
			}
			// The empty string is never a valid value (optional strings are absent).
			err := mutate(t, fe.kind, base, n.path, func(m node) { m.set("") })
			if err == nil {
				t.Errorf("%s %s: empty string accepted", fe.kind, n.path)
			} else {
				wantField(t, err, n.path)
			}
		}
		if strs < 4 {
			t.Fatalf("%s: only %d strings walked", fe.kind, strs)
		}
	}
}

// TestInvisibleAllowedInEntries: bidi controls and zero-width characters are
// allowed in entries (rendered visible in the Markdown), unlike constraints.
func TestInvisibleAllowedInEntries(t *testing.T) {
	for _, cp := range []rune{0x200b, 0x202e, 0xfeff, 0xe0041} {
		p := clone(t, fullPosition()).(map[string]any)
		p["claim"] = "a" + r(cp) + "b"
		p["argument"] = "a" + r(cp) + "b"
		if _, _, err := DecodeEntry(KindPosition, p); err != nil {
			t.Errorf("U+%04X refused in an entry: %v", cp, err)
		}
	}
}

func TestFieldLimits(t *testing.T) {
	cases := []struct {
		kind, path string
		limit      int
	}{
		{KindPosition, "claim", 280},
		{KindPosition, "assumptions[0]", 280},
		{KindPosition, "evidence[0].ref", 1024},
		{KindPosition, "evidence[0].note", 280},
		{KindPosition, "rejected_alternatives[0].option", 120},
		{KindPosition, "rejected_alternatives[0].reason", 280},
		{KindPosition, "argument", 4000},
		{KindMove, "challenges[0].argument", 4000},
		{KindMove, "challenges[0].evidence[0].ref", 1024},
		{KindMove, "challenges[0].evidence[0].note", 280},
		{KindMove, "revision.claim", 280},
		{KindMove, "revision.argument", 4000},
		{KindProposal, "agreement.decision", 280},
		{KindProposal, "agreement.points[0]", 280},
		{KindProposal, "agreement.argument", 4000},
		{KindProposal, "remaining_disagreement[0].point", 280},
		{KindProposal, "remaining_disagreement[0].initiator", 280},
		{KindProposal, "remaining_disagreement[0].respondent", 280},
		{KindAnswer, "argument", 4000},
		{KindAnswer, "remaining_disagreement[0].respondent", 280},
	}
	for _, c := range cases {
		base := entryBase(c.kind)
		// Two-byte runes: the limit counts code points, not bytes.
		at := strings.Repeat("é", c.limit)
		if err := mutate(t, c.kind, base, c.path, func(n node) { n.set(at) }); err != nil {
			t.Errorf("%s %s at %d: %v", c.kind, c.path, c.limit, err)
		}
		err := mutate(t, c.kind, base, c.path, func(n node) { n.set(at + "é") })
		if err == nil {
			t.Errorf("%s %s at %d+1 accepted", c.kind, c.path, c.limit)
			continue
		}
		wantField(t, err, c.path)
	}
}

func TestArrayLimits(t *testing.T) {
	cases := []struct {
		kind, path string
		min, max   int
	}{
		{KindPosition, "assumptions", 1, 10},
		{KindPosition, "evidence", 1, 10},
		{KindPosition, "rejected_alternatives", 1, 5},
		{KindMove, "challenges", 0, 3},
		{KindMove, "challenges[0].targets", 1, 5},
		{KindMove, "challenges[0].evidence", 1, 5},
		{KindMove, "revision.assumptions", 1, 10},
		{KindMove, "revision.evidence", 1, 10},
		{KindProposal, "agreement.points", 1, 10},
		{KindProposal, "remaining_disagreement", 1, 10},
		{KindProposal, "affected_artifacts", 1, 20},
		{KindAnswer, "remaining_disagreement", 1, 10},
	}
	targets := []any{"claim", "argument", "assumptions/0", "evidence/0", "rejected_alternatives/0", "evidence/1"}
	fill := func(n node, count int) {
		list := make([]any, count)
		for i := range list {
			if strings.HasSuffix(n.path, "targets") {
				list[i] = targets[i]
			} else {
				list[i] = clone(t, n.val.([]any)[0])
			}
		}
		n.set(list)
	}
	for _, c := range cases {
		base := entryBase(c.kind)
		for _, count := range []int{c.min, c.max, c.max + 1, c.min - 1} {
			if count < 0 {
				continue
			}
			err := mutate(t, c.kind, base, c.path, func(n node) { fill(n, count) })
			ok := count >= c.min && count <= c.max
			if ok && err != nil {
				t.Errorf("%s %s with %d items: %v", c.kind, c.path, count, err)
			}
			if !ok {
				if err == nil {
					t.Errorf("%s %s with %d items accepted", c.kind, c.path, count)
					continue
				}
				wantField(t, err, c.path)
			}
		}
	}
}

// entryBase is the full entry of kind.
func entryBase(kind string) map[string]any {
	for _, fe := range fullEntries {
		if fe.kind == kind {
			return fe.full()
		}
	}
	panic(kind)
}

// optional reports whether the member at path may be absent.
func optional(kind, path, key string) bool {
	switch key {
	case "assumptions", "evidence", "rejected_alternatives", "note", "revision",
		"points", "remaining_disagreement", "affected_artifacts",
		"url", "path": // an artifact keeps its other member
		return true
	case "argument":
		return path == "agreement.argument" || (kind == KindAnswer && path == "argument")
	}
	return false
}

func TestMissingExtraAndNullMembers(t *testing.T) {
	for _, fe := range fullEntries {
		base := fe.full()
		for _, n := range paths(base) {
			if n.parent != nil {
				err := mutate(t, fe.kind, base, n.path, func(m node) { delete(m.parent, m.key) })
				if optional(fe.kind, n.path, n.key) {
					if err != nil {
						t.Errorf("%s: dropping optional %s: %v", fe.kind, n.path, err)
					}
				} else if err == nil {
					t.Errorf("%s: missing %s accepted", fe.kind, n.path)
				} else {
					wantField(t, err, n.path)
				}
			}
			err := mutate(t, fe.kind, base, n.path, func(m node) { m.set(nil) })
			if err == nil {
				t.Errorf("%s: null %s accepted", fe.kind, n.path)
			} else {
				wantField(t, err, n.path)
			}
			if _, isObj := n.val.(map[string]any); isObj {
				err := mutate(t, fe.kind, base, n.path, func(m node) { m.val.(map[string]any)["extra"] = "x" })
				if err == nil {
					t.Errorf("%s: extra member in %s accepted", fe.kind, n.path)
				}
			}
		}
		root := clone(t, base).(map[string]any)
		root["extra"] = "x"
		if _, _, err := DecodeEntry(fe.kind, root); err == nil {
			t.Errorf("%s: extra top-level member accepted", fe.kind)
		} else {
			wantField(t, err, "extra")
		}
		if _, _, err := DecodeEntry(fe.kind, nil); err == nil {
			t.Errorf("%s: null entry accepted", fe.kind)
		}
		if _, _, err := DecodeEntry(fe.kind, []any{}); err == nil {
			t.Errorf("%s: array entry accepted", fe.kind)
		}
	}
}

func TestWrongTypes(t *testing.T) {
	a := clone(t, fullAnswer()).(map[string]any)
	a["accept"] = "true"
	_, _, err := DecodeEntry(KindAnswer, a)
	wantField(t, err, "accept")
	m := clone(t, fullMove()).(map[string]any)
	m["challenges"] = map[string]any{}
	_, _, err = DecodeEntry(KindMove, m)
	wantField(t, err, "challenges")
	p := clone(t, fullPosition()).(map[string]any)
	p["claim"] = []any{"x"}
	_, _, err = DecodeEntry(KindPosition, p)
	wantField(t, err, "claim")
	p = clone(t, fullPosition()).(map[string]any)
	p["evidence"].([]any)[0].(map[string]any)["kind"] = "rumour"
	_, _, err = DecodeEntry(KindPosition, p)
	wantField(t, err, "evidence[0].kind")
	pr := clone(t, fullProposal()).(map[string]any)
	pr["affected_artifacts"].([]any)[0].(map[string]any)["commit"] = "XYZ"
	_, _, err = DecodeEntry(KindProposal, pr)
	wantField(t, err, "affected_artifacts[0].commit")
}

func TestEvidenceKinds(t *testing.T) {
	for _, k := range []string{"file", "commit", "url", "test", "doc", "measurement"} {
		p := clone(t, fullPosition()).(map[string]any)
		p["evidence"].([]any)[0].(map[string]any)["kind"] = k
		if _, _, err := DecodeEntry(KindPosition, p); err != nil {
			t.Errorf("kind %s: %v", k, err)
		}
	}
}

func TestTargets(t *testing.T) {
	other := &Position{
		Claim: "c", Argument: "a",
		Assumptions:          []string{"x", "y"},
		Evidence:             []Evidence{{Kind: "file", Ref: "f"}},
		RejectedAlternatives: []Alternative{{Option: "o", Reason: "r"}},
	}
	move := func(targets ...string) *Move {
		return &Move{Challenges: []Challenge{{Targets: targets, Argument: "because"}}}
	}
	for _, tg := range []string{"claim", "argument", "assumptions/0", "assumptions/1", "evidence/0", "rejected_alternatives/0"} {
		m := move(tg)
		if err := ValidateMove(m); err != nil {
			t.Fatalf("%s: %v", tg, err)
		}
		if err := CheckTargets(m, other); err != nil {
			t.Fatalf("%s: CheckTargets: %v", tg, err)
		}
		pt, _ := ParseTarget(tg)
		if pt.String() != tg {
			t.Fatalf("String() = %s, want %s", pt.String(), tg)
		}
	}
	// Syntactically valid but out of range for this position.
	for _, tg := range []string{"assumptions/2", "evidence/1", "rejected_alternatives/1", "evidence/9"} {
		m := move("claim", tg)
		if err := ValidateMove(m); err != nil {
			t.Fatalf("%s: %v", tg, err)
		}
		wantField(t, CheckTargets(m, other), "challenges[0].targets[1]")
	}
	thin := &Position{Claim: "c", Argument: "a"}
	wantField(t, CheckTargets(move("assumptions/0"), thin), "challenges[0].targets[0]")
	// Malformed.
	for _, tg := range []string{
		"", "Claim", "claim/0", "argument/0", "evidence", "evidence/", "evidence/01", "evidence/00",
		"evidence/-1", "evidence/+1", "evidence/1a", "evidence/ 1", "evidence/10", "rejected_alternatives/5",
		"assumptions/99999999999999999999", "revision", "evidence//0", "evidence/0/0",
	} {
		if _, err := ParseTarget(tg); err == nil {
			t.Errorf("ParseTarget(%q) accepted", tg)
		}
		wantField(t, ValidateMove(move("claim", tg)), "challenges[0].targets[1]")
	}
	// Duplicates.
	wantField(t, ValidateMove(move("claim", "evidence/0", "claim")), "challenges[0].targets[2]")
	// Five distinct targets pass, six do not.
	five := move("claim", "argument", "assumptions/0", "assumptions/1", "evidence/0")
	if err := ValidateMove(five); err != nil {
		t.Fatal(err)
	}
	if err := CheckTargets(five, other); err != nil {
		t.Fatal(err)
	}
	six := move("claim", "argument", "assumptions/0", "assumptions/1", "evidence/0", "rejected_alternatives/0")
	wantField(t, ValidateMove(six), "challenges[0].targets")
	wantField(t, ValidateMove(move()), "challenges[0].targets")
	// A pass: no challenges, no revision.
	if err := ValidateMove(&Move{}); err != nil {
		t.Fatal(err)
	}
	canon, err := Canonical(&Move{})
	if err != nil || string(canon) != `{"challenges":[]}` {
		t.Fatalf("pass canonical = %s, %v", canon, err)
	}
}

// TestEntrySizeCap: canonical entry of 32768 bytes is accepted, 32769 is
// entry_too_large, with every field within its cap.
func TestEntrySizeCap(t *testing.T) {
	build := func(argLen int) *Position {
		p := &Position{Claim: "c", Argument: strings.Repeat("a", argLen)}
		for i := 0; i < 10; i++ {
			p.Evidence = append(p.Evidence, Evidence{Kind: "doc", Ref: strings.Repeat("€", 1024)})
		}
		return p
	}
	canon, err := Canonical(build(1))
	if err != nil {
		t.Fatal(err)
	}
	argLen := MaxDebateEntry - len(canon) + 1
	if argLen < 1 || argLen+1 > 4000 {
		t.Fatalf("fixture cannot reach the cap: argument length %d", argLen)
	}
	for _, tc := range []struct {
		argLen int
		ok     bool
	}{{argLen, true}, {argLen + 1, false}} {
		p := build(tc.argLen)
		if err := ValidatePosition(p); err != nil {
			t.Fatalf("field caps: %v", err)
		}
		canon, err := Canonical(p)
		if err != nil {
			t.Fatal(err)
		}
		v, err := agentcard.ParseStrict(canon)
		if err != nil {
			t.Fatal(err)
		}
		_, got, err := DecodeEntry(KindPosition, v)
		if tc.ok {
			if err != nil || len(got) != MaxDebateEntry {
				t.Fatalf("size %d: %v", len(canon), err)
			}
			if _, err := ParseEntry(KindPosition, canon); err != nil {
				t.Fatalf("ParseEntry at cap: %v", err)
			}
			continue
		}
		var tl *TooLargeError
		if !errors.As(err, &tl) || tl.Size != MaxDebateEntry+1 || tl.Limit != MaxDebateEntry {
			t.Fatalf("size %d: want *TooLargeError, got %v", len(canon), err)
		}
		if _, err := ParseEntry(KindPosition, canon); !errors.As(err, &tl) {
			t.Fatalf("ParseEntry over cap: %v", err)
		}
	}
}

// TestCanonicalStable: the spec vector's position is canonical as written;
// every full entry round-trips byte for byte; non-canonical encodings of a
// valid entry are refused by ParseEntry and normalised by DecodeEntry.
func TestCanonicalStable(t *testing.T) {
	e, err := ParseEntry(KindPosition, []byte(specPosition))
	if err != nil {
		t.Fatalf("spec vector position: %v", err)
	}
	canon, err := Canonical(e)
	if err != nil || string(canon) != specPosition {
		t.Fatalf("spec vector canonical:\n%s\n%v", canon, err)
	}
	for _, fe := range fullEntries {
		_, c1, err := DecodeEntry(fe.kind, clone(t, fe.full()))
		if err != nil {
			t.Fatal(err)
		}
		e, err := ParseEntry(fe.kind, c1)
		if err != nil {
			t.Fatal(err)
		}
		c2, err := Canonical(e)
		if err != nil || string(c1) != string(c2) {
			t.Fatalf("%s: canonical not stable:\n%s\n%s", fe.kind, c1, c2)
		}
	}
	variants := []string{
		" " + specPosition,
		specPosition + "\n",
		strings.Replace(specPosition, `"claim":`, `"claim" :`, 1),
		strings.Replace(specPosition, "Use capped", string([]byte{'\\', 'u'})+"0055se capped", 1),
		strings.Replace(specPosition, `internal/mail`, `internal\/mail`, 1),
		// Members out of order.
		`{"claim":"Use capped exponential backoff for outbox retries","argument":"Retries should back off exponentially, capped at 10 minutes.","assumptions":["Clock skew between peers is under 5 s"],"evidence":[{"kind":"file","ref":"internal/mail/outbox.go"}],"rejected_alternatives":[{"option":"Fixed 30 s retry","reason":"Floods the relay after an outage"}]}`,
	}
	for _, v := range variants {
		if _, err := ParseEntry(KindPosition, []byte(v)); !errors.Is(err, ErrNotCanonical) {
			t.Errorf("non-canonical %q: got %v", v, err)
		}
		pv, err := agentcard.ParseStrict([]byte(v))
		if err != nil {
			t.Fatal(err)
		}
		_, c, err := DecodeEntry(KindPosition, pv)
		if err != nil || string(c) != specPosition {
			t.Errorf("DecodeEntry(%q) = %s, %v", v, c, err)
		}
	}
	for _, bad := range []string{`{"claim":"a","claim":"b","argument":"x"}`, `{"claim":"a"`, `{"claim":"a","argument":"x"} {}`} {
		if _, err := ParseEntry(KindPosition, []byte(bad)); err == nil {
			t.Errorf("ParseEntry(%q) accepted", bad)
		}
	}
}

func TestTypedValidate(t *testing.T) {
	wantField(t, Validate(&Position{Argument: "a"}), "claim")
	wantField(t, Validate(&Move{Revision: &Position{Claim: "c"}}), "revision.argument")
	wantField(t, Validate(&Proposal{}), "agreement.decision")
	if err := Validate(&Answer{Accept: false}); err != nil {
		t.Fatal(err)
	}
	canon, _ := Canonical(&Answer{})
	if string(canon) != `{"accept":false}` {
		t.Fatalf("answer canonical %s", canon)
	}
}

func TestConstraintText(t *testing.T) {
	ok := []string{"No new dependency", "Must stay compatible with Go 1.22", "Ümlaut and 中文 and a" + r(0xa0) + "b", strings.Repeat("é", 500)}
	for _, s := range ok {
		if err := ValidateConstraintText(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{
		"", strings.Repeat("é", 501), " lead", "trail ", "two\nlines", "tab\there",
		"esc" + r(0x1b), "del" + r(0x7f), "c1" + r(0x9b), "ls" + r(0x2028), "ps" + r(0x2029),
		"zw" + r(0x200b) + "sp", "zwj" + r(0x200d), "bom" + r(0xfeff), "rlo" + r(0x202e) + "x",
		"lri" + r(0x2066), "tag" + r(0xe0041) + r(0xe007f), "soft" + r(0xad) + "hyphen",
		"private" + r(0xe000), "unassigned" + r(0x0378),
	}
	for _, s := range bad {
		err := ValidateConstraintText(s)
		if err == nil {
			t.Errorf("%q accepted", s)
			continue
		}
		wantField(t, err, "text")
	}
}

func TestTopic(t *testing.T) {
	for _, s := range []string{"Should retries back off?\n\tDetails.", strings.Repeat("a", 16384), " leading space is fine"} {
		if err := ValidateTopic(s); err != nil {
			t.Errorf("%.20q: %v", s, err)
		}
	}
	for _, s := range []string{"", strings.Repeat("a", 16385), "a" + r(0x1b), "a" + r(0x85), "a" + r(0x2028), "a" + r(0x2029), "a\rb"} {
		err := ValidateTopic(s)
		if err == nil {
			t.Errorf("%.20q accepted", s)
			continue
		}
		wantField(t, err, "brief")
	}
}
