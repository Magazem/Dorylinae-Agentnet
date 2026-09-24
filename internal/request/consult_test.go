package request

// Ticket 2.5 acceptance, request side (Docs/protocol/consult.md §Request
// object additions, §Size limits): context caps on the submit path
// (FieldError / TooLargeError) and on the receive path (bad_body), and that
// context text is never audited.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

func questionWithContext(files ...ContextFile) *Request {
	r := validRequest()
	r.Type = TypeQuestion
	r.Context = files
	return r
}

func ctxFiles(n int, text string) []ContextFile {
	out := make([]ContextFile, n)
	for i := range out {
		out[i] = ContextFile{Name: "f" + itoa(i) + ".go", Text: text}
	}
	return out
}

func wantContextField(t *testing.T, err error, field string) {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want *FieldError for %q", err, field)
	}
	if fe.Field != field {
		t.Fatalf("field = %q, want %q (%v)", fe.Field, field, fe)
	}
}

func TestContextFileCaps(t *testing.T) {
	// 8 files accepted, 9 refused.
	if err := Validate(questionWithContext(ctxFiles(8, "x")...)); err != nil {
		t.Fatalf("8 files: %v", err)
	}
	wantContextField(t, Validate(questionWithContext(ctxFiles(9, "x")...)), "context")
	// An empty array is not the same as absent.
	wantContextField(t, Validate(questionWithContext([]ContextFile{}...)), "context")

	// 65536 bytes per text accepted, 65537 refused.
	if err := Validate(questionWithContext(ContextFile{Name: "a.txt", Text: strings.Repeat("a", 65536)})); err != nil {
		t.Fatalf("65536-byte text: %v", err)
	}
	wantContextField(t, Validate(questionWithContext(ContextFile{Name: "a.txt", Text: strings.Repeat("a", 65537)})), "context[0].text")
	// The index in the field is the offending file's.
	wantContextField(t, Validate(questionWithContext(
		ContextFile{Name: "a.txt", Text: "ok"},
		ContextFile{Name: "b.txt", Text: strings.Repeat("a", 65537)},
	)), "context[1].text")
	wantContextField(t, Validate(questionWithContext(ContextFile{Name: "a.txt", Text: ""})), "context[0].text")

	// Control characters other than \n and \t are refused; \n and \t are fine.
	if err := Validate(questionWithContext(ContextFile{Name: "a.txt", Text: "a\n\tb\n"})); err != nil {
		t.Fatalf("newline and tab: %v", err)
	}
	for _, bad := range []string{"a\x1bb", "a\x00b", "a\rb", "a\x7fb", "a\xffb"} {
		wantContextField(t, Validate(questionWithContext(ContextFile{Name: "a.txt", Text: bad})), "context[0].text")
	}
}

func TestContextFileNames(t *testing.T) {
	for _, ok := range []string{"a", "outbox.go", "my file.md", strings.Repeat("n", 255)} {
		if err := Validate(questionWithContext(ContextFile{Name: ok, Text: "x"})); err != nil {
			t.Errorf("name %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("n", 256), ".", "..", "a/b", `a\b`, "a\x00b", "a\nb", "a\x1bb", "a\u009bb", "a\u0085b"} {
		wantContextField(t, Validate(questionWithContext(ContextFile{Name: bad, Text: "x"})), "context[0].name")
	}
	// C1 controls (U+009B is CSI) are refused in the text too (R-2.5 L1).
	wantContextField(t, Validate(questionWithContext(ContextFile{Name: "a.txt", Text: "a\u009b31mb"})), "context[0].text")
}

// TestShowKeyExactRow: R-2.5 M1. FindKey and ShowKey address one row by
// (direction, peer, id), so an in request that reuses an out request's id is
// never shown in its place.
func TestShowKeyExactRow(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := questionWithContext(ContextFile{Name: "a.txt", Text: "in-row"})
	req.Created = time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A fake out row with the same id to another peer.
	for _, q := range []string{
		`CREATE TEMP TABLE dup AS SELECT * FROM requests`,
		`UPDATE dup SET direction = 'out', peer = 'other'`,
		`INSERT INTO requests SELECT * FROM dup`,
	} {
		if _, err := s.DB.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE id = '`+testID+`'`); n != 2 {
		t.Fatalf("rows with the id = %d, want 2", n)
	}
	// Show by id alone prefers the out row: the ambiguity ShowKey avoids.
	if v, err := s.Show(t.Context(), testID, ""); err != nil || v.Direction != "out" {
		t.Fatalf("Show = %s, %v; want the out row", v.Direction, err)
	}
	k, err := s.FindKey(t.Context(), func(k Key) bool { return k.Direction == "in" })
	if err != nil || k.Peer != testFrom || k.ID != testID {
		t.Fatalf("FindKey = %+v, %v", k, err)
	}
	v, err := s.ShowKey(t.Context(), k)
	if err != nil || v.Direction != "in" || v.Peer != testFrom {
		t.Fatalf("ShowKey = %s/%s, %v; want the in row", v.Direction, v.Peer, err)
	}
	if _, err := s.FindKey(t.Context(), func(Key) bool { return false }); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("FindKey no match = %v, want ErrUnknownRequest", err)
	}
}

func TestContextOnlyOnQuestion(t *testing.T) {
	for _, typ := range []string{TypeReview, TypeTask} {
		r := questionWithContext(ContextFile{Name: "a.txt", Text: "x"})
		r.Type = typ
		wantContextField(t, Validate(r), "context")
	}
	if err := Validate(questionWithContext(ContextFile{Name: "a.txt", Text: "x"})); err != nil {
		t.Fatalf("question with context: %v", err)
	}
}

// questionOfSize returns a valid question whose canonical form is exactly
// total bytes: four full 65536-byte files and a fifth tuned to fit.
func questionOfSize(t *testing.T, total int) *Request {
	t.Helper()
	build := func(n int) *Request {
		files := ctxFiles(4, strings.Repeat("a", 65536))
		files = append(files, ContextFile{Name: "tuned.go", Text: strings.Repeat("a", n)})
		return questionWithContext(files...)
	}
	canon, err := Canonical(build(1))
	if err != nil {
		t.Fatal(err)
	}
	n := 1 + total - len(canon)
	if n < 1 || n > 65536 {
		t.Fatalf("cannot reach %d bytes with a tuned file of %d bytes", total, n)
	}
	r := build(n)
	if err := Validate(r); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	canon, err = Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(canon) != total {
		t.Fatalf("canonical size = %d, want %d", len(canon), total)
	}
	return r
}

func TestQuestionTotalBodyCap(t *testing.T) {
	at := questionOfSize(t, MaxQuestionBody)
	canon, _ := Canonical(at)
	if err := CheckSizeFor(at, canon); err != nil {
		t.Fatalf("CheckSizeFor(327680) = %v, want accept", err)
	}
	over := questionOfSize(t, MaxQuestionBody+1)
	canon, _ = Canonical(over)
	var tl *TooLargeError
	if err := CheckSizeFor(over, canon); !errors.As(err, &tl) {
		t.Fatalf("CheckSizeFor(327681) = %v, want *TooLargeError", err)
	} else if tl.Size != MaxQuestionBody+1 || tl.Limit != MaxQuestionBody {
		t.Fatalf("TooLargeError = %+v", tl)
	}

	// The larger cap is only for a question WITH context.
	big := make([]byte, MaxRequestBody+1)
	plain := validRequest()
	plain.Type = TypeQuestion
	if err := CheckSizeFor(plain, big); !errors.As(err, &tl) || tl.Limit != MaxRequestBody {
		t.Fatalf("question without context over 65536 = %v, want limit %d", err, MaxRequestBody)
	}
	if err := CheckSizeFor(questionWithContext(ContextFile{Name: "a", Text: "x"}), big); err != nil {
		t.Fatalf("question with context at 65537 = %v, want accept", err)
	}
}

func TestContextCanonicalRoundTrip(t *testing.T) {
	r := questionWithContext(
		ContextFile{Name: "outbox.go", Text: "package mail\n\tfunc \"x\" {}\n<é>\n"},
		ContextFile{Name: "notes.md", Text: "hello"},
	)
	canon, err := Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(canon)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(v.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Context) != 2 || got.Context[0] != r.Context[0] || got.Context[1] != r.Context[1] {
		t.Fatalf("round trip = %+v, want %+v", got.Context, r.Context)
	}
	canon2, _ := Canonical(got)
	if string(canon) != string(canon2) {
		t.Fatal("canonical form changed across a round trip")
	}
}

func TestDecodeContextStrict(t *testing.T) {
	base := func() map[string]any {
		r := questionWithContext(ContextFile{Name: "a", Text: "x"})
		canon, _ := Canonical(r)
		v, err := agentcard.ParseStrict(canon)
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)
	}
	for name, mutate := range map[string]func(m map[string]any){
		"not an array":   func(m map[string]any) { m["context"] = "x" },
		"element string": func(m map[string]any) { m["context"] = []any{"x"} },
		"extra member": func(m map[string]any) {
			m["context"] = []any{map[string]any{"name": "a", "text": "x", "mode": "0644"}}
		},
		"missing text": func(m map[string]any) { m["context"] = []any{map[string]any{"name": "a"}} },
		"empty array":  func(m map[string]any) { m["context"] = []any{} },
		"text number":  func(m map[string]any) { m["context"] = []any{map[string]any{"name": "a", "text": 1}} },
	} {
		m := base()
		mutate(m)
		if _, err := Decode(m); err == nil {
			t.Errorf("%s: Decode accepted", name)
		}
	}
}

// TestContextReceivePath: the recipient refuses every context violation and an
// oversized question with bad_body (nothing stored), accepts the largest one,
// and audits sizes only.
func TestContextReceivePath(t *testing.T) {
	deliver := func(req *Request) (*Store, *fakeAudit, error) {
		s, _, al := newTestStore(t, testTo, &policy{})
		req.Created = time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
		return s, al, deliverRequest(t, s, req, time.Now())
	}
	file := func(text string) ContextFile { return ContextFile{Name: "a.txt", Text: text} }

	for name, req := range map[string]*Request{
		"9 files":        questionWithContext(ctxFiles(9, "x")...),
		"text 65537":     questionWithContext(file(strings.Repeat("a", 65537))),
		"control char":   questionWithContext(file("a\x1bb")),
		"bad name":       questionWithContext(ContextFile{Name: "../a", Text: "x"}),
		"context review": func() *Request { r := questionWithContext(file("x")); r.Type = TypeReview; return r }(),
		"total 327681":   questionOfSize(t, MaxQuestionBody+1),
	} {
		s, _, err := deliver(req)
		if !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("%s: err = %v, want bad_body", name, err)
		}
		if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 {
			t.Errorf("%s: %d rows stored, want none", name, n)
		}
	}

	secret := "SECRET-CONTEXT-TEXT-" + strings.Repeat("z", 64)
	s, al, err := deliver(questionOfSize(t, MaxQuestionBody))
	if err != nil {
		t.Fatalf("total 327680: %v", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'in' AND type = 'question'`); n != 1 {
		t.Fatalf("stored rows = %d, want 1", n)
	}
	if _, err := s.Show(t.Context(), testID, ""); err != nil {
		t.Fatalf("Show: %v", err)
	}

	s2, al2, err := deliver(questionWithContext(file(secret), ContextFile{Name: "b.txt", Text: "12345"}))
	if err != nil {
		t.Fatal(err)
	}
	v, err := s2.Show(t.Context(), testID, testFrom)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Context) != 2 || v.Context[0].Text != secret {
		t.Fatalf("view context = %+v", v.Context)
	}
	for _, e := range append(al.entries, al2.entries...) {
		if strings.Contains(e.detail, "SECRET-CONTEXT") || strings.Contains(e.detail, "aaaaaaaa") {
			t.Fatalf("audit %s leaks context text: %.200s", e.action, e.detail)
		}
	}
	found := false
	for _, e := range al2.entries {
		if e.action == "request.in" {
			found = true
			if !strings.Contains(e.detail, `"context_files":2`) || !strings.Contains(e.detail, `"context_bytes":`+itoa(len(secret)+5)) {
				t.Fatalf("request.in detail = %s, want context_files 2 and context_bytes %d", e.detail, len(secret)+5)
			}
		}
	}
	if !found {
		t.Fatal("no request.in audit row")
	}
}
