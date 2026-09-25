package debate

import (
	"bytes"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// The fuzz targets feed arbitrary bytes to ParseEntry (and, when they parse
// as JSON, to DecodeEntry). They check that nothing panics and that an
// accepted entry is canonical, within MaxDebateEntry, re-parses to the same
// bytes, and passes an oracle written independently of the validators: no
// null, no empty optional array, and the debate text and free-text rules on
// every string.

func FuzzPosition(f *testing.F) { fuzzKind(f, KindPosition, fullPosition()) }
func FuzzMove(f *testing.F)     { fuzzKind(f, KindMove, fullMove()) }
func FuzzProposal(f *testing.F) { fuzzKind(f, KindProposal, fullProposal()) }
func FuzzAnswer(f *testing.F)   { fuzzKind(f, KindAnswer, fullAnswer()) }

func fuzzKind(f *testing.F, kind string, full map[string]any) {
	seed, err := agentcard.CanonicalValue(full)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add(bytes.Replace(seed, []byte(`"`), []byte(` "`), 1))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(specPosition))
	f.Add([]byte(`{"challenges":[]}`))
	f.Add([]byte(`{"accept":false}`))
	f.Add([]byte(`{"agreement":{"decision":"d"}}`))
	f.Add([]byte(`{"challenges":[{"targets":["evidence/01"],"argument":"a"}]}`))
	f.Add([]byte(`{"claim":"a\nb","argument":"x"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if e, err := ParseEntry(kind, data); err == nil {
			checkAccepted(t, kind, e, data)
		}
		v, err := agentcard.ParseStrict(data)
		if err != nil {
			return
		}
		e, canon, err := DecodeEntry(kind, v)
		if err != nil {
			return
		}
		checkAccepted(t, kind, e, canon)
		if !bytes.Equal(canon, data) {
			if _, err := ParseEntry(kind, data); err == nil {
				t.Fatalf("ParseEntry accepted a non-canonical encoding:\n%q\ncanonical:\n%q", data, canon)
			}
		}
	})
}

func checkAccepted(t *testing.T, kind string, e Entry, canon []byte) {
	t.Helper()
	if e.Kind() != kind {
		t.Fatalf("kind %s decoded as %s", kind, e.Kind())
	}
	if len(canon) > MaxDebateEntry {
		t.Fatalf("accepted %d bytes", len(canon))
	}
	again, err := Canonical(e)
	if err != nil || !bytes.Equal(again, canon) {
		t.Fatalf("canonical not stable: %v\n%q\n%q", err, canon, again)
	}
	if err := Validate(e); err != nil {
		t.Fatalf("accepted entry fails Validate: %v", err)
	}
	e2, err := ParseEntry(kind, canon)
	if err != nil {
		t.Fatalf("canonical form refused: %v", err)
	}
	if c2, _ := Canonical(e2); !bytes.Equal(c2, canon) {
		t.Fatalf("re-parse changed the entry")
	}
	v, err := agentcard.ParseStrict(canon)
	if err != nil {
		t.Fatalf("canonical not parseable: %v", err)
	}
	oracle(t, v, "")
}

// oracle checks every value below v; key is the member name v sits under.
func oracle(t *testing.T, v any, key string) {
	t.Helper()
	switch x := v.(type) {
	case nil:
		t.Fatalf("accepted null under %q", key)
	case map[string]any:
		for k, c := range x {
			oracle(t, c, k)
		}
	case []any:
		if len(x) == 0 && key != "challenges" {
			t.Fatalf("accepted empty array %q", key)
		}
		for _, c := range x {
			oracle(t, c, key)
		}
	case string:
		oracleString(t, key, x)
	}
}

func oracleString(t *testing.T, key, s string) {
	t.Helper()
	if s == "" || !utf8.ValidString(s) {
		t.Fatalf("accepted empty or invalid %q: %q", key, s)
	}
	for _, c := range s {
		switch {
		case c == '\n' || c == '\t':
			if key != "argument" {
				t.Fatalf("accepted %q in %q: %q", c, key, s)
			}
		case c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) || c == 0x2028 || c == 0x2029:
			t.Fatalf("accepted control U+%04X in %q", c, key)
		}
	}
	if key == "argument" {
		if utf8.RuneCountInString(s) > 4000 {
			t.Fatalf("accepted an argument of %d code points", utf8.RuneCountInString(s))
		}
		return
	}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		t.Fatalf("accepted leading or trailing white space in %q: %q", key, s)
	}
	if utf8.RuneCountInString(s) > 1024 {
		t.Fatalf("accepted %d code points in %q", utf8.RuneCountInString(s), key)
	}
}
