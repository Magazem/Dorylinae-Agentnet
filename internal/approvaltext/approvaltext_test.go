package approvaltext

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func testKey(seed string) string {
	s := sha256.Sum256([]byte(seed))
	return envelope.KeyString(ed25519.NewKeyFromSeed(s[:]).Public().(ed25519.PublicKey))
}

func testPeer(seed, name string) Peer { return Peer{Key: testKey(seed), Name: name, Paired: true} }

func fpOf(key string) string {
	fp, _ := envelope.KeyFingerprint(key)
	return envelope.FormatFingerprint(fp)
}

var (
	t0 = time.Date(2026, 9, 29, 16, 45, 30, 0, time.UTC)
	t1 = t0.Add(2 * time.Hour)
)

// Every kind, built from the same peer fields, for the kind-independent
// tests (A11, A16).
func allKinds(p Peer, agent string) map[string]func() (string, error) {
	return map[string]func() (string, error){
		"grant": func() (string, error) {
			return BuildGrant(Grant{ID: "g-1", Action: "git.read", Peer: p, Path: agent, Label: agent, Branch: agent, Scope: agent,
				Nbf: t0, Exp: t1, Session: "s-1", RequestType: "task", RequestTitle: agent})
		},
		"grant_policy": func() (string, error) {
			return BuildPolicy(Policy{ID: "p-1", Action: "fs.read", Peer: p, Path: agent, Scope: agent, MaxExpires: time.Hour, Created: t0, Until: t1})
		},
		"release": func() (string, error) {
			return BuildRelease(Result{Session: "s-1", Peer: p, RequestType: "task", RequestTitle: agent, Round: 1, Status: agent, SensitiveGrants: 2})
		},
		"accept_result": func() (string, error) {
			return BuildAcceptResult(Result{Session: "s-1", Peer: p, RequestType: agent, RequestTitle: agent, Round: 1, Status: "pass"})
		},
		"device_link": func() (string, error) { return BuildLink(Link{ID: "i-1", Peer: p, Role: "helper"}) },
		"device_scope": func() (string, error) {
			return BuildScope(Scope{Peer: p, Types: []string{"task"}, Expires: t1,
				Commands: []ScopeCommand{{Name: "test", Dir: agent, Argv: []string{"/bin/go", agent}, TimeoutS: 5, Env: []string{"GOFLAGS"}}}})
		},
		"debate_constraint": func() (string, error) {
			return BuildConstraint(Constraint{Session: "s-1", Peer: p, ID: "c-1", Text: agent})
		},
	}
}

// R55-F5 A5: the grant summary states the resolved path, the label, the
// branch, the scope, PUBLIC, the duration, the UTC expiry, the session, the
// request and the peer's fingerprint first (review 55 C11-01).
func TestGrantSummary(t *testing.T) {
	p := testPeer("bob", "Bob")
	s, err := BuildGrant(Grant{ID: "g-1", Action: "git.read", Peer: p, Path: `C:\src\app`, Label: "app-3f2a", Branch: "main",
		Scope: "src/lib", Sensitive: false, Nbf: t0, Exp: t1, Session: "s-1", RequestType: "review", RequestTitle: "Check the parser"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Grant git.read to peer ` + fpOf(p.Key) + ` named "Bob" on "C:\\src\\app" (label "app-3f2a"), branch "main", only "src/lib" inside it, `,
		`for 2 h until 2026-09-29 18:45 UTC, in session s-1 (your review request "Check the parser"). `,
		`PUBLIC: you state this repository is public; results of this session are NOT quarantined. Confirm only if you asked for exactly this.`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	s, _ = BuildGrant(Grant{Action: "fs.read", Peer: p, Path: "/srv/x", Label: "x", Sensitive: true, Nbf: t0, Exp: t1, Session: "s-1", RequestType: "task"})
	if !strings.Contains(s, ", the whole folder, ") || !strings.Contains(s, "Sensitive: results of this session stay quarantined") {
		t.Errorf("fs.read: %s", s)
	}
	s, _ = BuildGrant(Grant{Action: "git.read", Peer: p, Path: "/srv/x", Label: "x", Branch: "b", Sensitive: true, Nbf: t0, Exp: t1, Session: "s-1", RequestType: "task"})
	if !strings.Contains(s, ", the whole repository, ") {
		t.Errorf("git.read: %s", s)
	}
}

// R55-F5 A6: the policy summary states the branch, the scope, PUBLIC or
// sensitive grants only, the maximum expiry, until (UTC and duration) and the
// fingerprint.
func TestPolicySummary(t *testing.T) {
	p := testPeer("bob", "Bob")
	s, err := BuildPolicy(Policy{ID: "p-1", Action: "git.read", Peer: p, Path: "/srv/app", Branch: "main", Scope: "docs",
		Public: true, MaxExpires: 90 * time.Minute, Created: t0, Until: t0.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	want := `Allow grants with no further question until 2026-12-28 16:45 UTC (90 d): git.read to peer ` + fpOf(p.Key) +
		` named "Bob" on "/srv/app", branch "main", only paths inside "docs", each grant for at most 1 h 30 min, PUBLIC grants only: results are NOT quarantined. Confirm only if you asked for exactly this.`
	if s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
	s, _ = BuildPolicy(Policy{Action: "fs.read", Peer: p, Path: "/srv/app", MaxExpires: time.Hour, Created: t0, Until: t1})
	if !strings.Contains(s, ", any path in it, ") || !strings.Contains(s, "sensitive grants only (results quarantined)") {
		t.Errorf("sensitive policy: %s", s)
	}
}

// R55-F5 A8, A15: the device-link summary names the role, shows the
// fingerprint first (right after the fixed word "peer") and asks to compare
// all five groups with 'agentnet identity'. Two peers with the same name
// differ in the fingerprint; a fingerprint copied into the name is blanked.
func TestLinkSummary(t *testing.T) {
	x, d := testPeer("x", "Desktop"), testPeer("d", "Desktop")
	sx, err := BuildLink(Link{Peer: x, Role: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	sd, _ := BuildLink(Link{Peer: d, Role: "helper"})
	if sx == sd || !strings.Contains(sx, fpOf(x.Key)) || !strings.Contains(sd, fpOf(d.Key)) {
		t.Fatal("twin names give the same summary")
	}
	for _, want := range []string{"Link this device as the helper of peer " + fpOf(x.Key) + ` named "Desktop".`, "all five groups", "'agentnet identity'"} {
		if !strings.Contains(sx, want) {
			t.Errorf("summary lacks %q: %s", want, sx)
		}
	}
	fpShape := regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{4}(?:[ -][0-9A-HJKMNP-TV-Z]{4}){4}\b`)
	fpD := fpOf(d.Key)
	for _, name := range []string{
		"Desktop (fingerprint " + fpD + ")",
		"Desktop (fingerprint " + strings.ToLower(fpD) + ")",
		"Desktop (fingerprint " + strings.ReplaceAll(fpD, " ", "-") + ")",
		"\u05d0 123 Desktop",
	} {
		twin := Peer{Key: x.Key, Name: name, Paired: true}
		s, err := BuildLink(Link{Peer: twin, Role: "controller"})
		if err != nil {
			t.Fatal(err)
		}
		if first := fpShape.FindString(s); first != fpOf(x.Key) {
			t.Errorf("name %q: first fingerprint %q, want %q", name, first, fpOf(x.Key))
		}
		if !strings.Contains(s, " of peer "+fpOf(x.Key)+" named ") {
			t.Errorf("name %q: the fingerprint does not follow the word peer: %s", name, s)
		}
		for _, g := range strings.Fields(fpD) {
			if strings.Contains(strings.ToUpper(s), g) {
				t.Errorf("name %q: a group of fp(D) shows: %s", name, s)
			}
		}
	}
	s, _ := BuildLink(Link{Peer: Peer{Key: x.Key, Name: `Bob "the" builder`, Paired: true}, Role: "helper"})
	if !strings.Contains(s, `named "Bob \"the\" builder".`) {
		t.Errorf("quote not escaped: %s", s)
	}
}

// R55-F5 A18 (review 58a M1): the scope summary starts with the command
// count and names, so a command after a long argument is named at the top.
func TestScopeSummaryHeader(t *testing.T) {
	s, err := BuildScope(Scope{Peer: testPeer("c", "laptop"), Types: []string{"task"}, Expires: t1, Commands: []ScopeCommand{
		{Name: "test", Dir: `C:\src\repo`, Argv: []string{`C:\Go\bin\go.exe`, "test", strings.Repeat("a", 3000)}, TimeoutS: 900},
		{Name: "zz", Dir: `C:\src\repo`, Argv: []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "-c", "HIDDEN-PAYLOAD"}, TimeoutS: 900},
	}})
	if err != nil {
		t.Fatal(err)
	}
	head := string([]rune(s)[:300])
	if !strings.Contains(head, "2 command(s) (test, zz)") {
		t.Errorf("header: %s", head)
	}
	if !strings.Contains(s, `"HIDDEN-PAYLOAD"]`) || !strings.HasSuffix(s, " Confirm only if you set this scope yourself.") {
		t.Error("the last command or the closing sentence is missing")
	}
}

// R55-F5 A9: release and accept-result state the peer, the request, the
// session, the round, the status and the sizes, and K or the rule-2 reason.
func TestResultSummaries(t *testing.T) {
	p := testPeer("bob", "Bob")
	f := Result{Session: "s-1", Peer: p, RequestType: "task", RequestTitle: "Fix it", Round: 2, Status: "pass", ResultBytes: 120, OutputBytes: 4000, Artifacts: 1}
	s, err := BuildRelease(f)
	if err != nil {
		t.Fatal(err)
	}
	want := "Release the quarantined result of peer " + fpOf(p.Key) + ` named "Bob" for your task request "Fix it" (session s-1, round 2): status pass, 120 bytes, 4000 bytes of output, 1 artifact(s). Your agent will then be able to read it. This peer held a sensitive grant in another session in the last 7 days. Confirm only if you mean to release this result.`
	if s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
	f.SensitiveGrants = 3
	if s, _ := BuildRelease(f); !strings.Contains(s, "This session had 3 sensitive grant(s).") {
		t.Errorf("K: %s", s)
	}
	if s, _ := BuildAcceptResult(f); !strings.HasPrefix(s, "Accept the result of peer ") || !strings.Contains(s, "This closes the request as accepted. Confirm only if you checked this result.") {
		t.Errorf("accept: %s", s)
	}
	f.Peer.Paired = false
	if s, _ := BuildRelease(f); !strings.Contains(s, "peer "+fpOf(p.Key)+" (no longer paired) for your") {
		t.Errorf("unpaired: %s", s)
	}
}

// The time forms of OD-R55F5-3.
func TestUTCAndDur(t *testing.T) {
	if got := UTC(time.Date(2026, 9, 29, 20, 45, 59, 0, time.FixedZone("CEST", 2*3600))); got != "2026-09-29 18:45 UTC" {
		t.Errorf("UTC = %q", got)
	}
	for d, want := range map[time.Duration]string{
		30 * time.Minute: "30 min", 2 * time.Hour: "2 h", 90 * time.Minute: "1 h 30 min", 7 * 24 * time.Hour: "7 d",
		52 * time.Hour: "2 d 4 h", 48*time.Hour + 30*time.Minute: "2 d", 59 * time.Second: "less than 1 min",
	} {
		if got := Dur(d); got != want {
			t.Errorf("Dur(%v) = %q, want %q", d, got, want)
		}
	}
}

// Too long is refused, never cut (Docs/protocol/approval.md §Length).
func TestTooLong(t *testing.T) {
	_, err := BuildConstraint(Constraint{Session: "s-1", Peer: testPeer("b", "b"), Text: strings.Repeat("x", 5000)})
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v", err)
	}
}

// gStrcompress is GLib's g_strcompress as zenity applies it to --text,
// followed by the C string's cut at the first NUL (from review 55's
// zz_review55_T11-02_test.go, which tests T11-01 / R55-025).
func gStrcompress(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b = append(b, s[i])
			continue
		}
		i++
		switch c := s[i]; {
		case c >= '0' && c <= '7':
			v, n := 0, 0
			for n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
				v = v*8 + int(s[i]-'0')
				i++
				n++
			}
			i--
			b = append(b, byte(v))
		case c == 'n':
			b = append(b, '\n')
		case c == 't':
			b = append(b, '\t')
		case c == 'r':
			b = append(b, '\r')
		case c == 'b':
			b = append(b, '\b')
		case c == 'f':
			b = append(b, '\f')
		case c == 'v':
			b = append(b, '\v')
		default:
			b = append(b, c)
		}
	}
	out := string(b)
	if i := strings.IndexByte(out, 0); i >= 0 {
		out = out[:i]
	}
	return out
}

// R55-F5 A16 (review 58a M2): before F6, zenity decodes '\' escapes in the
// window text, but no name can cut the window: every kind still shows the
// fingerprint and its closing "Confirm only if …" sentence, and no line
// break.
func TestZenityCannotCutSummary(t *testing.T) {
	for _, name := range []string{`Bob\0`, `Mallory\n\nApproved by IT`} {
		p := testPeer("z", name)
		for kind, build := range allKinds(p, `C:\src\0\n`) {
			s, err := build()
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			shown := gStrcompress(s)
			if !strings.Contains(shown, fpOf(p.Key)) || !strings.Contains(shown, "Confirm only if") || strings.ContainsAny(shown, "\n\r\x00") {
				t.Errorf("%s, name %q: zenity shows %q", kind, name, shown)
			}
		}
	}
}

// R55-F5 A11: with any strings in the peer and agent fields, every kind's
// output is ErrTooLong or display-safe and at most 4096 code points, and the
// builder is deterministic and independent of the time zone.
func FuzzBuild(f *testing.F) {
	f.Add("Bob\u202e code 482913", `C:\src\app`)
	f.Add("\x00\n\u200b", strings.Repeat("\u0301", 600))
	f.Add(`"`+strings.Repeat("\\", 40), "\xff\xfe")
	f.Fuzz(func(t *testing.T, name, agent string) {
		p := testPeer("fuzz", name)
		for kind, build := range allKinds(p, agent) {
			s, err := build()
			if errors.Is(err, ErrTooLong) {
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			if !displaytext.Safe(s) || utf8.RuneCountInString(s) > displaytext.MaxSummary {
				t.Fatalf("%s: not display-safe: %q", kind, s)
			}
			old := time.Local
			time.Local = time.FixedZone("X", -7*3600)
			again, _ := build()
			time.Local = old
			if again != s {
				t.Fatalf("%s: not deterministic", kind)
			}
		}
	})
}

// R55-F5 A1 boundary (review 55 C14-02): a scope summary of exactly 4096
// code points is built, 4097 is refused; neither is cut.
func TestScopeLimitBoundary(t *testing.T) {
	build := func(n int) (string, error) {
		return BuildScope(Scope{Peer: testPeer("c", "laptop"), Types: []string{"task"}, Expires: t1, Commands: []ScopeCommand{
			{Name: "test", Dir: "/srv/repo", Argv: []string{"/usr/bin/go", strings.Repeat("a", n)}, TimeoutS: 900},
			{Name: "zz", Dir: "/srv/repo", Argv: []string{"/bin/sh", "-c", "HIDDEN-PAYLOAD"}, TimeoutS: 900},
		}})
	}
	s, err := build(0)
	if err != nil {
		t.Fatal(err)
	}
	n := displaytext.MaxSummary - utf8.RuneCountInString(s)
	if s, err = build(n); err != nil || utf8.RuneCountInString(s) != displaytext.MaxSummary || !strings.Contains(s, "HIDDEN-PAYLOAD") {
		t.Fatalf("4096 code points: %d, %v", utf8.RuneCountInString(s), err)
	}
	if _, err := build(n + 1); !errors.Is(err, ErrTooLong) {
		t.Fatalf("4097 code points: err = %v, want ErrTooLong", err)
	}
}
