package envelope_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// R55-F9 test 13: every Code* constant of frames.go is a known error code,
// except CodeRelayError, which only the daemon makes.
func TestKnownErrorCodeCoversEveryCode(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "frames.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s is not a string literal", name.Name)
				}
				code, _ := strconv.Unquote(lit.Value)
				n++
				want := name.Name != "CodeRelayError"
				if got := envelope.KnownErrorCode(code); got != want {
					t.Errorf("KnownErrorCode(%s = %q) = %v, want %v", name.Name, code, got, want)
				}
				if want && envelope.ErrorText(code) == envelope.ErrorText(envelope.CodeRelayError) {
					t.Errorf("%s has no daemon text of its own", name.Name)
				}
			}
		}
	}
	if n < 20 {
		t.Fatalf("found only %d Code* constants", n)
	}
	for _, c := range []string{"", "x", "internal\x1b[31m", "INTERNAL", "internal "} {
		if envelope.KnownErrorCode(c) {
			t.Errorf("KnownErrorCode(%q) = true", c)
		}
		if envelope.ErrorText(c) != "the relay refused the request" {
			t.Errorf("ErrorText(%q) = %q", c, envelope.ErrorText(c))
		}
	}
}

func TestErrorFrameErrorOmitsMessage(t *testing.T) {
	e := envelope.ErrorFrame{Code: envelope.CodeInternal, Message: "\x1b[2K relay: connected", Ref: "r"}
	if got := e.Error(); got != "relay: internal" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"env-1": true, "a.b_c:d-E9": true, strings.Repeat("a", 128): true,
		"": false, "bad ref!": false, strings.Repeat("a", 129): false, "a\x1b": false, "é": false,
	} {
		if got := envelope.ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
}
