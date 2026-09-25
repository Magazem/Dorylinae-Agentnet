package main

// Ticket 1.6b acceptance (Docs/review/11-phase1-tickets.md §1.6b,
// Docs/cli/inbox.md): inbox/accept/decline/defer/complete usage, output and
// exit codes, and the complete result flags (CRLF, ANSI stripping, control
// characters).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// startFakeDaemon serves the given handlers at p.Endpoint until the test ends.
// shortHome (main_test.go) points DORYLINAE_HOME at a fresh temp dir.
func startFakeDaemon(t *testing.T, p paths.Paths, handlers map[string]ipc.HandlerFunc) {
	t.Helper()
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer()
	for method, h := range handlers {
		srv.Handle(method, h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("fake daemon did not stop")
		}
	})
}

// --- inbox ---

func TestInboxHumanAndJSON(t *testing.T) {
	p := shortHome(t)
	prio := 3000
	view := daemon.RequestView{
		ID: "r-0123456789abcdef0123456789abcdef", Direction: "in",
		Peer: daemon.RequestPeerRef{Name: "alice"}, Type: "review", Title: "Review retry change",
		Urgency: "high", UrgencyDeclared: "high", State: "pending",
		ReceivedAt: time.Now().Add(-4 * time.Minute).UTC().Format("2006-01-02T15:04:05Z"),
		Priority:   &prio, MailID: "m-1",
	}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"inbox_list": func(context.Context, json.RawMessage) (any, error) {
			return daemon.RequestListResult{Requests: []daemon.RequestView{view}}, nil
		},
	})

	var out, errb bytes.Buffer
	if code := run([]string{"inbox"}, &out, &errb); code != exitOK {
		t.Fatalf("inbox: code %d, stderr %q", code, errb.String())
	}
	for _, want := range []string{"ID", "FROM", "TYPE", "URGENCY", "PRIO", "AGE", "TITLE", view.ID, "alice", "high", "3000", "Review retry change"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("inbox human output lacks %q; got %q", want, out.String())
		}
	}

	out.Reset()
	if code := run([]string{"inbox", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("inbox --json: code %d", code)
	}
	var body inboxBody
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.Requests) != 1 || body.Requests[0].ID != view.ID {
		t.Errorf("inbox --json body = %+v", body)
	}
}

func TestInboxEmpty(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"inbox_list": func(context.Context, json.RawMessage) (any, error) {
			return daemon.RequestListResult{}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"inbox"}, &out, &errb); code != exitOK {
		t.Fatalf("inbox: code %d", code)
	}
	if strings.TrimSpace(out.String()) != "Inbox empty." {
		t.Errorf("inbox empty output = %q", out.String())
	}
}

func TestInboxUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"inbox", "extra"}, &out, &errb); code != exitUsage {
		t.Errorf("inbox with a positional arg: code %d, want %d", code, exitUsage)
	}
}

// --- accept / decline / defer / complete: usage before the daemon is contacted ---

func TestAcceptDeclineDeferUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"accept no id", []string{"accept"}},
		{"accept two ids", []string{"accept", "r-1", "r-2"}},
		{"decline no id", []string{"decline", "--reason", "no"}},
		{"decline no reason", []string{"decline", "r-1"}},
		{"defer no id", []string{"defer", "--until", "2h"}},
		{"defer no until", []string{"defer", "r-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(c.args, &out, &errb); code != exitUsage {
				t.Errorf("%v: code %d, want %d (stderr %q)", c.args, code, exitUsage, errb.String())
			}
		})
	}
}

// TestCompleteResultFlagRequiresStatus: a result flag without --status is a
// usage error, exit 2 (Docs/cli/inbox.md §Result).
func TestCompleteResultFlagRequiresStatus(t *testing.T) {
	cases := [][]string{
		{"complete", "r-1", "--summary", "done"},
		{"complete", "r-1", "--exit-code", "1"},
		{"complete", "r-1", "--artifact", "branch=main"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d (stderr %q)", args, code, exitUsage, errb.String())
		}
	}
}

func TestCompleteNoteWithoutStatusIsFine(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"request_complete": func(_ context.Context, params json.RawMessage) (any, error) {
			var got completeParams
			if err := json.Unmarshal(params, &got); err != nil {
				t.Fatal(err)
			}
			if got.Note != "done" || got.Result != nil {
				t.Errorf("params = %+v, want note-only", got)
			}
			return daemon.RequestLifecycleResult{Request: daemon.RequestView{ID: "r-1", State: "completed", Peer: daemon.RequestPeerRef{Name: "bob"}}}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"complete", "r-1", "--note", "done"}, &out, &errb); code != exitOK {
		t.Fatalf("complete: code %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Completed r-1 from bob") {
		t.Errorf("complete human output = %q", out.String())
	}
}

// TestCompleteWithResult round-trips every result flag through the params
// sent to the daemon.
func TestCompleteWithResult(t *testing.T) {
	p := shortHome(t)
	dir := t.TempDir()
	logPath := dir + "/log.txt"
	if err := os.WriteFile(logPath, []byte("line one\r\nline two\x1b[31mred\x1b[0m\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen completeParams
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"request_complete": func(_ context.Context, params json.RawMessage) (any, error) {
			if err := json.Unmarshal(params, &seen); err != nil {
				t.Fatal(err)
			}
			return daemon.RequestLifecycleResult{Request: daemon.RequestView{ID: "r-1", State: "completed", Peer: daemon.RequestPeerRef{Name: "bob"}}}, nil
		},
	})
	var out, errb bytes.Buffer
	code := run([]string{
		"complete", "r-1", "--status", "fail", "--summary", "3 of 212 failed",
		"--exit-code", "1", "--output-from-file", logPath, "--artifact", "branch=fix/retry commit=1a2b3c4",
	}, &out, &errb)
	if code != exitOK {
		t.Fatalf("complete: code %d, stderr %q", code, errb.String())
	}
	if seen.Result == nil {
		t.Fatal("no result sent")
	}
	if seen.Result.Status != "fail" || seen.Result.Summary != "3 of 212 failed" {
		t.Errorf("result = %+v", seen.Result)
	}
	if seen.Result.ExitCode == nil || *seen.Result.ExitCode != 1 {
		t.Errorf("exit_code = %v", seen.Result.ExitCode)
	}
	if seen.Result.Output != "line one\nline twored\n" {
		t.Errorf("output = %q, want CRLF->LF and ANSI stripped", seen.Result.Output)
	}
	if len(seen.Result.Artifacts) != 1 || seen.Result.Artifacts[0].Branch != "fix/retry" || seen.Result.Artifacts[0].Commit != "1a2b3c4" {
		t.Errorf("artifacts = %+v", seen.Result.Artifacts)
	}
}

func TestCompleteBadStateIsError(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"request_complete": func(context.Context, json.RawMessage) (any, error) {
			return nil, &ipc.Error{Code: "bad_state", Message: "r-1 is pending"}
		},
	})
	var out, errb bytes.Buffer
	code := run([]string{"complete", "r-1", "--json"}, &out, &errb)
	if code != exitError {
		t.Fatalf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(out.String(), `"bad_state"`) {
		t.Errorf("output = %q, want bad_state", out.String())
	}
}

func TestDaemonNotRunningExitCode(t *testing.T) {
	shortHome(t) // no fake daemon started
	var out, errb bytes.Buffer
	if code := run([]string{"inbox"}, &out, &errb); code != exitDaemonNotFound {
		t.Errorf("code = %d, want %d", code, exitDaemonNotFound)
	}
}

// --- readOutputFile ---

func TestReadOutputFile(t *testing.T) {
	old := completeStdin
	t.Cleanup(func() { completeStdin = old })

	completeStdin = strings.NewReader("a\r\nb\x1b[2Kc\n")
	got, err := readOutputFile("-")
	if err != nil || got != "a\nbc\n" {
		t.Fatalf("readOutputFile(stdin) = %q, %v", got, err)
	}

	completeStdin = strings.NewReader("bad\x01byte")
	if _, err := readOutputFile("-"); err == nil {
		t.Error("a control character other than tab: want an error")
	}

	completeStdin = strings.NewReader("caf\xe9") // Latin-1, not UTF-8
	if _, err := readOutputFile("-"); err == nil {
		t.Error("invalid UTF-8: want an error")
	}

	completeStdin = strings.NewReader("tabs\tare\tfine\n")
	if got, err := readOutputFile("-"); err != nil || got != "tabs\tare\tfine\n" {
		t.Errorf("tabs = %q, %v", got, err)
	}

	dir := t.TempDir()
	p := dir + "/out.txt"
	if err := os.WriteFile(p, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readOutputFile(p); err != nil || got != "from a file\n" {
		t.Errorf("file output = %q, %v", got, err)
	}
	if _, err := readOutputFile(dir + "/missing.txt"); err == nil {
		t.Error("missing file: want an error")
	}
}

// TestLifecycleFromForwardedVerbatim: --from (a name, a key, a key starting
// with '-') reaches the daemon unchanged, which resolves it; a daemon error
// such as ambiguous_peer or unknown_peer is reported, not swallowed.
func TestLifecycleFromForwardedVerbatim(t *testing.T) {
	cases := []struct {
		verb string
		args []string
	}{
		{"accept", nil},
		{"decline", []string{"--reason", "no"}},
		{"defer", []string{"--until", "2h"}},
	}
	froms := []struct{ name, from, errCode string }{
		{"name", "bob", ""},
		{"key", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ""},
		{"dash key", dashKey, ""},
		{"unknown name", "nobody", "unknown_peer"},
		{"ambiguous name", "twin", "ambiguous_peer"},
	}
	for _, c := range cases {
		for _, f := range froms {
			t.Run(c.verb+"/"+f.name, func(t *testing.T) {
				p := shortHome(t)
				var mu sync.Mutex
				var got map[string]any
				startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
					"request_" + c.verb: func(_ context.Context, params json.RawMessage) (any, error) {
						mu.Lock()
						defer mu.Unlock()
						if err := json.Unmarshal(params, &got); err != nil {
							t.Error(err)
						}
						if f.errCode != "" {
							return nil, &ipc.Error{Code: f.errCode, Message: "no such peer"}
						}
						return daemon.RequestLifecycleResult{Request: daemon.RequestView{ID: "r-1", Peer: daemon.RequestPeerRef{Name: "bob"}}}, nil
					},
				})
				args := append([]string{c.verb, "r-1", "--from", f.from, "--json"}, c.args...)
				var out, errb bytes.Buffer
				code := run(args, &out, &errb)
				mu.Lock()
				defer mu.Unlock()
				if got["from"] != f.from {
					t.Errorf("daemon got from = %v, want %q", got["from"], f.from)
				}
				if f.errCode == "" && code != exitOK {
					t.Errorf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
				}
				if f.errCode != "" && (code != exitError || !strings.Contains(out.String(), f.errCode)) {
					t.Errorf("code %d, stdout %q; want exit %d with %s", code, out.String(), exitError, f.errCode)
				}
			})
		}
	}
}
