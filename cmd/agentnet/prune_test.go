package main

// R55-F13 `agentnet prune` (Docs/cli/prune.md, test A14 of
// Docs/review/71-r55-f13-spec.md): usage and exit codes, the dry run by
// default, the wait for the approval, and --json totals across calls.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/retention"
)

func TestPruneUsage(t *testing.T) {
	shortHome(t)
	for _, args := range [][]string{
		{"prune"},
		{"prune", "--older-than", "34d"},
		{"prune", "--older-than", "4w"},
		{"prune", "--older-than", "35"},
		{"prune", "--older-than", "35h"},
		{"prune", "--older-than", "-40d"},
		{"prune", "--older-than", "40d", "extra"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d (%s)", args, code, exitUsage, errb.String())
		}
	}
	for _, s := range []string{"35d", "5w", "90d"} {
		if _, err := parsePruneDuration(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestPruneWithoutDaemon(t *testing.T) {
	shortHome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"prune", "--older-than", "35d"}, &out, &errb); code != exitDaemonNotFound {
		t.Fatalf("code %d, want %d", code, exitDaemonNotFound)
	}
}

// fakePrune is a data_prune handler: a dry run, then an approval that is
// pending for the first poll, then batches of the given counts.
type fakePrune struct {
	mu      sync.Mutex
	calls   []daemon.DataPruneParams
	polls   int
	batches []retention.Counts
	failAt  int // 1-based batch that fails, 0 for none
}

func (f *fakePrune) handle(_ context.Context, raw json.RawMessage) (any, error) {
	var p daemon.DataPruneParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	cutoff := "2026-08-26T10:00:00Z"
	var total retention.Counts
	for _, b := range f.batches {
		total = total.Add(b)
	}
	view := &approval.View{ID: "a-0123456789abcdef0123456789abcdef", Kind: "data_prune", State: "pending", Summary: "Remove for good ...", Window: "open"}
	switch {
	case p.DryRun:
		return daemon.DataPruneResult{Cutoff: cutoff, DryRun: true, Counts: total}, nil
	case p.Approval == "":
		return daemon.DataPruneResult{Cutoff: cutoff, Counts: total, More: true, Approval: view}, nil
	case f.polls == 0:
		f.polls++
		return daemon.DataPruneResult{Cutoff: cutoff, More: true, Approval: view}, nil
	}
	n := len(f.calls) - 3 // dry run is not called; create, one pending poll
	if f.failAt != 0 && n+1 == f.failAt {
		return nil, &ipc.Error{Code: "io_error", Message: "the database write failed; nothing of this call was removed"}
	}
	return daemon.DataPruneResult{Cutoff: cutoff, Counts: f.batches[n], More: n+1 < len(f.batches)}, nil
}

func TestPruneDryRunByDefault(t *testing.T) {
	p := shortHome(t)
	f := &fakePrune{batches: []retention.Counts{{Requests: 412, WorkSessions: 130, Debates: 3, DebateEntries: 41, InboxBlanked: 830}}}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"data_prune": f.handle})
	var out, errb bytes.Buffer
	if code := run([]string{"prune", "--older-than", "35d"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if len(f.calls) != 1 || !f.calls[0].DryRun || f.calls[0].OlderThanS.String() != "3024000" {
		t.Fatalf("calls = %+v, want one dry run of 3024000 s", f.calls)
	}
	for _, want := range []string{"would remove (finished before 2026-08-26T10:00:00Z):", "requests             412", "(+ 41 entries, 0 constraints)", "old inbox copies     830", "--yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestPruneYesWaitsForApprovalAndTotals(t *testing.T) {
	old := prunePoll
	prunePoll = time.Millisecond
	t.Cleanup(func() { prunePoll = old })
	p := shortHome(t)
	f := &fakePrune{batches: []retention.Counts{{Requests: 500, Grants: 10}, {Requests: 500}, {Requests: 200, MailInbox: 7}}}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"data_prune": f.handle})
	var out, errb bytes.Buffer
	if code := run([]string{"prune", "--older-than", "12w", "--yes", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "Approval a-0123456789abcdef0123456789abcdef pending") {
		t.Errorf("stderr = %q, want the pending approval", errb.String())
	}
	var body pruneBody
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := retention.Counts{Requests: 1200, Grants: 10, MailInbox: 7}
	if !body.OK || body.DryRun || body.Counts != want || body.Approval == "" {
		t.Fatalf("json = %+v, want totals %+v", body, want)
	}
	if f.calls[0].DryRun || f.calls[0].Approval != "" || f.calls[1].Approval == "" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestPruneErrorKeepsCounts(t *testing.T) {
	old := prunePoll
	prunePoll = time.Millisecond
	t.Cleanup(func() { prunePoll = old })
	p := shortHome(t)
	f := &fakePrune{batches: []retention.Counts{{Requests: 500}, {Requests: 3}}, failAt: 2}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"data_prune": f.handle})
	var out, errb bytes.Buffer
	if code := run([]string{"prune", "--older-than", "35d", "--yes", "--json"}, &out, &errb); code != exitError {
		t.Fatalf("code %d, want %d", code, exitError)
	}
	var body pruneErrBody
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OK || body.Error == nil || body.Error.Code != "io_error" || body.Counts.Requests != 500 {
		t.Fatalf("json = %+v", body)
	}
}
