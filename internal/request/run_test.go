package request

import (
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// The "run" member (Docs/protocol/request.md §Request object,
// Docs/protocol/device.md §Running): {"command": "<name>"}, a name only,
// round-tripped through the canonical form and the wire body.
func TestRunMember(t *testing.T) {
	r := validRequest()
	r.Run = &Run{Command: "go-test.v1_x"}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	canon, err := Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canon), `"run":{"command":"go-test.v1_x"}`) {
		t.Fatalf("canonical form lacks run: %s", canon)
	}
	body, err := agentcard.ParseStrict(canon)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(body.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	if back.Run == nil || back.Run.Command != "go-test.v1_x" {
		t.Fatalf("decoded run = %+v", back.Run)
	}
	wire := WireBody(r)["request"].(map[string]any)
	if run, ok := wire["run"].(map[string]string); !ok || run["command"] != "go-test.v1_x" {
		t.Fatalf("wire body run = %#v", wire["run"])
	}
	// Absent stays absent.
	plain := validRequest()
	canon, _ = Canonical(plain)
	if strings.Contains(string(canon), `"run"`) {
		t.Fatal("run present on a request without it")
	}
}

func TestRunMemberRules(t *testing.T) {
	for _, name := range []string{"a", strings.Repeat("z", 64), "0.9_x-y"} {
		r := validRequest()
		r.Run = &Run{Command: name}
		if err := Validate(r); err != nil {
			t.Errorf("run.command %q: %v", name, err)
		}
	}
	for _, name := range []string{"", strings.Repeat("z", 65), "Test", "go test", "a/b", "é"} {
		r := validRequest()
		r.Run = &Run{Command: name}
		var fe *FieldError
		if err := Validate(r); !errors.As(err, &fe) || fe.Field != "run.command" {
			t.Errorf("run.command %q: err = %v, want a run.command field error", name, err)
		}
	}
	base := func() map[string]any {
		canon, _ := Canonical(validRequest())
		b, _ := agentcard.ParseStrict(canon)
		return b.(map[string]any)
	}
	for name, run := range map[string]any{
		"not an object":  "test",
		"extra member":   map[string]any{"command": "test", "args": []any{"-v"}},
		"missing name":   map[string]any{},
		"number command": map[string]any{"command": 5.0},
		"null":           nil,
	} {
		b := base()
		b["run"] = run
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}
