package main

// R55-F10 acceptance A1 and A6-A9 (CLI side): every print site of the
// O-100 sweep (Docs/review/82-r55-f10-spec.md) renders peer-chosen text
// through displaytext.Term or Block, --json escapes hidden runes, and
// failJSON and log escape the daemon's text.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// evilText is A6's peer-chosen field: an SGR escape, an RLO, a Hangul filler
// and a line break.
const evilText = "x\x1b[31my\u202ez\u3164\nw"

// A1 (inverted review 55 T11-04): the "graphic but invisible" runes that
// termSafe let through are escaped, and termSafe is gone.
func TestReview55T11_04Inverted(t *testing.T) {
	for in, want := range map[string]string{
		"report\u3164.txt": `report\u{3164}.txt`, // Hangul filler (Lo)
		"report\u115f.txt": `report\u{115F}.txt`, // Hangul choseong filler (Lo)
		"report\ufe0f.txt": `report\u{FE0F}.txt`, // variation selector (Mn)
		"report\u2800.txt": `report\u{2800}.txt`, // braille blank (So)
		"report\u200b.txt": `report\u{200B}.txt`, // zero-width space (Cf)
		"report\u202e.txt": `report\u{202E}.txt`, // RLO (Cf)
	} {
		got := displaytext.Term(in)
		if got != want {
			t.Errorf("Term(%+q) = %q, want %q", in, got, want)
		}
		if strings.IndexFunc(got, displaytext.Hidden) >= 0 {
			t.Errorf("Term(%+q) = %+q holds a hidden rune", in, got)
		}
	}
	src, err := os.ReadFile("fetch.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("termSafe")) {
		t.Fatal("fetch.go still has termSafe")
	}
}

// checkEscaped is A6's check on a command's human output.
func checkEscaped(t *testing.T, name, out string) {
	t.Helper()
	if strings.ContainsAny(out, "\x1b\u202e\u3164") {
		t.Errorf("%s: output holds a raw ESC, U+202E or U+3164: %q", name, out)
	}
	if !strings.Contains(out, `\u{202E}`) || !strings.Contains(out, `\u{3164}`) {
		t.Errorf("%s: output lacks the escapes: %q", name, out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "w") {
			t.Errorf("%s: the field broke the line: %q", name, out)
		}
	}
}

func handler(result any) ipc.HandlerFunc {
	return func(context.Context, json.RawMessage) (any, error) { return result, nil }
}

func evilPeer() map[string]any {
	return map[string]any{"name": evilText, "public_key": "k", "fingerprint": "ABCDEFGHJKMNPQRSTVWX"}
}

// sweepHandlers serves a result for every method a sweep row calls; every
// peer-chosen field holds evilText.
func sweepHandlers() map[string]ipc.HandlerFunc {
	E := evilText
	peer := map[string]any{"public_key": "k", "name": E, "harness": E,
		"skills":    []any{map[string]any{"id": E, "name": "n", "description": ""}},
		"paired_at": "2026-10-01T00:00:00Z", "trust": "tofu", "fingerprint": "ABCDEFGHJKMNPQRSTVWX"}
	team := map[string]any{"id": "t-0123456789abcdef", "name": E, "owner": "k", "epoch": 1, "state": "active", "role": "owner", "members": 2}
	teamShow := map[string]any{"id": "t-0123456789abcdef", "name": E, "owner": "k", "epoch": 1, "state": "active", "role": "owner",
		"members": []any{map[string]any{"name": E, "public_key": "k", "fingerprint": "ABCDEFGHJKMNPQRSTVWX", "added": "t", "owner": true}}}
	pairDone := map[string]any{"pairing_id": "pair-1", "role": "redeemer", "state": "complete", "peer": peer}
	gpeer := map[string]any{"name": E, "public_key": "k"}
	link := map[string]any{"id": "l-1", "peer": gpeer, "role": "helper", "state": "active"}
	grant := map[string]any{"id": "g-1", "direction": "issued", "peer": gpeer, "session": "s-1", "action": "git.read",
		"resource": map[string]any{"kind": "git", "label": E, "branch": E}, "scope": E, "nbf": "t", "exp": "t", "state": "active"}
	art := map[string]any{"url": E, "branch": E, "commit": E, "path": E}
	request := map[string]any{"id": "r-1", "direction": "out", "peer": evilPeer(), "team": map[string]any{"id": "t-1", "name": E},
		"type": "task", "title": E, "urgency": "normal", "state": "done", "reason": E, "note": E, "cancel": E,
		"artifacts": []any{art}, "context": []any{map[string]any{"name": E, "text": "abc"}},
		"result":  map[string]any{"status": "pass", "summary": E, "output": E, "output_bytes": 3, "artifacts": []any{art}},
		"mail_id": "m-1"}
	submit := map[string]any{"id": "r-1", "mail_id": "m-1", "status": "queued", "team": map[string]any{"id": "t-1", "name": E},
		"urgency": "normal", "urgency_note": E, "peer": map[string]any{"name": E, "public_key": "k", "daemon_online": false}, "session": "s-1"}
	sessionView := map[string]any{"id": "s-1", "role": "requester", "peer": evilPeer(), "team": map[string]any{"id": "t-1", "name": E},
		"request": map[string]any{"id": "r-1", "type": "task", "title": E}, "state": "closed", "outcome": E, "round": 1,
		"result": map[string]any{"status": "pass", "summary": E}, "cancel": E, "grants": []any{}}
	debateView := map[string]any{"session": "s-1", "request": map[string]any{"id": "r-1", "title": E}, "role": "initiator",
		"peer": evilPeer(), "team": map[string]any{"id": "t-1", "name": E}, "phase": "closed", "outcome": "agreed", "reason": E,
		"rounds": map[string]any{"current": 1, "max": 3}, "turn": "none", "expect": E, "waiting": E, "topic": E,
		"transcript": []any{map[string]any{"slot": 1, "author": E, "kind": "position", "at": "t", "entry": map[string]any{}}}}
	decisionDoc, _ := json.Marshal(map[string]any{"id": E, "outcome": E, "reason": E})
	return map[string]ipc.HandlerFunc{
		"peers":        handler(map[string]any{"peers": []any{peer}}),
		"peers_remove": handler(map[string]any{"peer": peer}),
		"pair_status":  handler(pairDone),
		"team_create":  handler(map[string]any{"team": team}),
		"team_list":    handler(map[string]any{"teams": []any{team}}),
		"team_show":    handler(map[string]any{"team": teamShow}),
		"team_join":    handler(pairDone),
		"team_remove":  handler(map[string]any{"team": team}),
		"team_rename":  handler(map[string]any{"team": team}),
		"team_leave":   handler(map[string]any{"team": team}),
		"team_delete":  handler(map[string]any{"team": team}),
		"status": handler(map[string]any{"version": "0.0.0", "presence": map[string]any{"mode": "visible", "relay": E},
			"team": map[string]any{"id": "t-1", "name": E, "owner": "k", "state": "active",
				"members": []any{map[string]any{"name": E, "public_key": "k"}, map[string]any{"name": E, "public_key": "j"}}}}),
		"presence_get":       handler(map[string]any{"mode": "only_team", "team": map[string]any{"id": "t-1", "name": E}}),
		"device_link":        handler(map[string]any{"approval": map[string]any{"id": "a-1"}, "link": link}),
		"device_list":        handler(map[string]any{"links": []any{link}}),
		"device_unlink":      handler(map[string]any{"link": link}),
		"device_scope_show":  handler(map[string]any{"scope": map[string]any{"types": []any{E}, "repos": []any{}, "commands": []any{map[string]any{"name": E, "repo": E, "argv": []any{"go"}, "timeout_s": 1, "env": []any{E}}}, "expires": "t"}}),
		"device_scope_clear": handler(map[string]any{"link": link}),
		"ping":               handler(map[string]any{"ping_id": "p-1", "peer": gpeer, "state": "complete", "rtt_ms": 1.5}),
		"grant_create":       handler(map[string]any{"grant": grant}),
		"grant_list":         handler(map[string]any{"grants": []any{grant}}),
		"grant_revoke":       handler(map[string]any{"grant": grant, "mail_id": "m-1"}),
		"grant_policy_list": handler(map[string]any{"policies": []any{map[string]any{"id": "p-1", "peer": gpeer, "action": "git.read",
			"branch": E, "scope": E, "max_expires_s": 60, "until": "t"}}}),
		"identity": handler(map[string]any{"card": map[string]any{"version": 1, "name": E, "public_key": "k", "harness": E,
			"skills": []any{map[string]any{"id": E, "name": "n", "description": ""}}, "created": "t"}, "signature": "s", "key_backend": "file", "fingerprint": "ABCDEFGHJKMNPQRSTVWX"}),
		"inbox_list":     handler(map[string]any{"requests": []any{request}}),
		"request_accept": handler(map[string]any{"request": request, "mail_id": "m-1"}),
		"request_submit": handler(submit),
		"request_show":   handler(map[string]any{"request": request}),
		"request_list":   handler(map[string]any{"requests": []any{request}}),
		"request_cancel": handler(map[string]any{"request": request}),
		"ws_list":        handler(map[string]any{"sessions": []any{sessionView}}),
		"ws_show":        handler(map[string]any{"session": sessionView}),
		"debate_show":    handler(map[string]any{"debate": debateView}),
		"debate_list":    handler(map[string]any{"debates": []any{debateView}}),
		"decision_show": handler(map[string]any{"decision": json.RawMessage(decisionDoc), "hash": "h", "state": "signed",
			"peer_names": map[string]any{"initiator": E, "respondent": E}}),
		"decision_list": handler(map[string]any{"decisions": []any{map[string]any{"id": "d-1", "peer": E, "title": E, "outcome": "agreed", "state": "signed"}}}),
		"audit_list": handler(map[string]any{"events": []any{map[string]any{"id": 1, "ts": "2026-10-01T00:00:00Z", "actor": E, "action": "x.y",
			"detail": map[string]any{E: E}}}}),
	}
}

// A6: the print-site sweep.
func TestPrintSitesEscapePeerText(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, sweepHandlers())
	posFile := filepath.Join(testutil.TempDir(t), "pos.json")
	if err := os.WriteFile(posFile, []byte(`{"claim":"c","argument":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sid := "s-0123456789abcdef0123456789abcdef"
	rid := "r-0123456789abcdef0123456789abcdef"
	for _, args := range [][]string{
		{"peers"},                           // 1, 30
		{"peers", "remove", "k"},            // 2
		{"pair", "--status", "pair-1"},      // 3
		{"team", "create", "t"},             // 5
		{"team", "list"},                    // 6
		{"team", "show", "t"},               // 7, 8
		{"team", "join", "ABCD-EFGH"},       // 9
		{"team", "remove", "t", evilText},   // 10
		{"team", "rename", "t", "n"},        // 11
		{"team", "leave", "t"},              // 11
		{"team", "delete", "t"},             // 11
		{"status", "--team", "t"},           // 12, 13, 14
		{"presence"},                        // 68
		{"device", "list"},                  // 18
		{"device", "unlink", "k"},           // 19
		{"device", "scope", "k", "--show"},  // 20, 21
		{"device", "scope", "k", "--clear"}, // 22
		{"ping", "@bob"},                    // 23
		{"grants"},                          // 25
		{"revoke", "g-1"},                   // 24
		{"grant", "policy", "list"},         // 26
		{"identity"},                        // 29
		{"inbox"},                           // 31
		{"accept", rid},                     // 32
		{"request", "@bob", "task", "--title", "t", "--brief", "b"}, // 33-35
		{"request", "show", rid},                                    // 36-42
		{"request", "list"},                                         // 43
		{"request", "cancel", rid},                                  // 44
		{"consult", "@bob", "--question", "q"},                      // 45
		{"sessions"},                                                // 47
		{"session", sid},                                            // 48-51
		{"debate", "@bob", "--topic", "x", "--position-file", posFile}, // 53
		{"debate", sid},     // 54-58
		{"debates"},         // 59
		{"decision", "d-1"}, // 60, 61
		{"decisions"},       // 62
		{"log"},             // 64
	} {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		name := strings.Join(args, " ")
		if code != exitOK {
			t.Errorf("%s: code %d, stderr %q", name, code, errb.String())
			continue
		}
		checkEscaped(t, name, out.String()+errb.String())
	}
}

// A6, Block rows: continuation lines start with the stated indent.
func TestPrintSitesBlockIndent(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, sweepHandlers())
	for _, tc := range []struct {
		args   []string
		indent string
		n      int // Block fields in the output
	}{
		{[]string{"request", "show", "r-0123456789abcdef0123456789abcdef"}, "    w", 2}, // reason, output
		{[]string{"debate", "s-0123456789abcdef0123456789abcdef"}, "           w", 1},   // topic
	} {
		var out, errb bytes.Buffer
		if code := run(tc.args, &out, &errb); code != exitOK {
			t.Fatalf("%v: code %d, stderr %q", tc.args, code, errb.String())
		}
		// Each Block field ends its first line at the escaped filler, and its
		// continuation line starts with the indent.
		if got := strings.Count(out.String(), `\u{3164}`+"\n"+tc.indent+"\n"); got != tc.n {
			t.Errorf("%v: %d indented continuation lines %q, want %d, in %q", tc.args, got, tc.indent, tc.n, out.String())
		}
	}
}

// A7: --json escapes hidden runes and decodes to the exact value.
func TestJSONOutputEscapesHiddenRunes(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, sweepHandlers())
	for _, args := range [][]string{
		{"peers", "--json"},
		{"inbox", "--json"},
		{"request", "show", "r-0123456789abcdef0123456789abcdef", "--json"},
		{"team", "show", "t", "--json"},
		{"debate", "s-0123456789abcdef0123456789abcdef", "--json"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitOK {
			t.Fatalf("%v: code %d, stderr %q", args, code, errb.String())
		}
		s := out.String()
		if strings.ContainsAny(s, "\u202e\u3164\u0085") || !strings.Contains(s, `\u202e`) || !strings.Contains(s, `\u3164`) {
			t.Errorf("%v: %q", args, s)
		}
		var v any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !holds(v, evilText) {
			t.Errorf("%v: no field decodes to the exact value", args)
		}
	}
}

// holds reports whether a decoded JSON value holds the string s.
func holds(v any, s string) bool {
	switch x := v.(type) {
	case string:
		return x == s
	case []any:
		for _, e := range x {
			if holds(e, s) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if holds(e, s) {
				return true
			}
		}
	}
	return false
}

// A8: failJSON's human branch escapes the daemon's message.
func TestFailJSONEscapesDaemonMessage(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"peers": func(context.Context, json.RawMessage) (any, error) {
			return nil, &ipc.Error{Code: "bad", Message: "no peer \x1b[2K\u202eevil"}
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"peers"}, &out, &errb); code == exitOK {
		t.Fatal("peers succeeded")
	}
	if got := errb.String(); strings.ContainsAny(got, "\x1b\u202e") || !strings.Contains(got, `no peer \u{1B}[2K\u{202E}evil`) {
		t.Fatalf("stderr = %q", got)
	}
	out.Reset()
	errb.Reset()
	_ = run([]string{"peers", "--json"}, &out, &errb)
	if got := out.String(); strings.ContainsAny(got, "\x1b\u202e") || !strings.Contains(got, `\u202e`) {
		t.Fatalf("--json = %q", got)
	}
}

// A9: log escapes an audit row's actor and detail through the daemon (the
// sweep's "log" row) and through the read-only direct path.
func TestLogDirectPathEscapes(t *testing.T) {
	p := shortHome(t)
	s, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.New(s.DB()).Append(context.Background(), evilText, "x.y", map[string]string{"name": evilText}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	code, out, errs := runLogCmd("--action", "x.")
	if code != exitOK || !strings.Contains(errs, "reading the database directly") {
		t.Fatalf("log: %d %q %q", code, out, errs)
	}
	checkEscaped(t, "log (direct)", out)
	if !strings.Contains(out, `\u{1B}`) {
		t.Errorf("log (direct): ESC not escaped: %q", out)
	}
}
