package main

// Ticket R55-F13: `agentnet prune` (Docs/cli/prune.md,
// Docs/protocol/retention.md). A dry run by default; with --yes the removal
// waits for a human approval (owner decision D57), then runs IPC data_prune
// in bounded batches until nothing is left.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/retention"
)

// pruneCallTimeout bounds one data_prune call; the daemon keeps each call
// within the 2-second rule of Docs/protocol/ipc.md, a dry run may read more.
const pruneCallTimeout = 30 * time.Second

// prunePoll is how often the CLI asks whether the human has decided; a
// variable so tests can shorten it.
var prunePoll = time.Second

// pruneWait bounds the wait for the human: an approval expires after 10
// minutes (Docs/protocol/approval.md §Object).
var pruneWait = 11 * time.Minute

const pruneUsage = `Removes finished requests, work sessions, debates, grants and inbox records
older than a cutoff from the daemon's database (Docs/cli/prune.md). Without
--yes it only counts. With --yes the removal waits for your approval in the
AgentNet approval window (or on the daemon's terminal in terminal mode).

Usage:
  agentnet prune --older-than DURATION [--yes] [--json]

Flags:
  --older-than D  required: only items finished before now - D; D is a
                  number with the unit d (days) or w (weeks), at least 35d
  --yes           actually remove (after your approval); without it, a dry run
  --json          print machine-readable JSON on stdout

Never removed: anything still open, Decisions, the audit log, peers, teams
and keys. Removal cannot be undone.

Exit codes: 0 done (or counted), 1 error (including a rejected or expired
approval), 2 usage, 3 daemon not running.
`

// pruneBody is the machine-readable output of `prune --json`.
type pruneBody struct {
	OK       bool             `json:"ok"`
	DryRun   bool             `json:"dry_run"`
	Cutoff   string           `json:"cutoff"`
	Approval string           `json:"approval,omitempty"`
	Counts   retention.Counts `json:"counts"`
}

// pruneErrBody is the machine-readable error with what was already removed.
type pruneErrBody struct {
	OK     bool             `json:"ok"`
	Error  *ipc.Error       `json:"error"`
	Counts retention.Counts `json:"counts"`
}

// parsePruneDuration parses "35d" or "12w" (Docs/cli/prune.md).
func parsePruneDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, errors.New("--older-than: give a number with the unit d or w, for example 90d")
	}
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if unit == 0 || err != nil || n <= 0 || n > 36500 || strings.HasPrefix(s, "+") {
		return 0, errors.New("--older-than: give a number with the unit d or w, for example 90d")
	}
	d := time.Duration(n) * unit
	if d < retention.MinOlderThan {
		return 0, errors.New("--older-than: the minimum is 35d")
	}
	return d, nil
}

func runPrune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	olderThan := fs.String("older-than", "", "only items finished before now - D (d or w, at least 35d)")
	yes := fs.Bool("yes", false, "actually remove, after your approval")
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, pruneUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "prune takes no arguments (see 'agentnet prune --help')")
	}
	if *olderThan == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--older-than is required (see 'agentnet prune --help')")
	}
	d, err := parsePruneDuration(*olderThan)
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	secs := json.Number(strconv.FormatInt(int64(d/time.Second), 10))
	params := daemon.DataPruneParams{OlderThanS: &secs, DryRun: !*yes}
	var res daemon.DataPruneResult
	if code := callDaemon(*asJSON, stdout, stderr, pruneCallTimeout, "data_prune", params, &res); code != exitOK {
		return code
	}
	if !*yes {
		return printPrune(stdout, *asJSON, res.Cutoff, "", true, res.Counts)
	}
	if res.Approval == nil {
		// Nothing to remove: no approval was asked.
		return printPrune(stdout, *asJSON, res.Cutoff, "", false, res.Counts)
	}
	aid := res.Approval.ID
	where := "the AgentNet approval window on your desktop (code in the notification)"
	if strings.HasPrefix(res.Approval.Window, "terminal") {
		where = "the daemon's terminal"
	}
	_, _ = fmt.Fprintf(stderr, "Approval %s pending: approve it in %s. %s\nWaiting for your answer...\n", aid, where, res.Approval.Summary)
	params = daemon.DataPruneParams{OlderThanS: &secs, Approval: aid}
	var total retention.Counts
	deadline := time.Now().Add(pruneWait)
	for {
		var r daemon.DataPruneResult
		code, errCode, msg := callDaemonRaw(pruneCallTimeout, "data_prune", params, &r)
		if code != exitOK {
			return prunePartial(stdout, stderr, *asJSON, code, errCode, msg, total)
		}
		if r.Approval != nil {
			if time.Now().After(deadline) {
				return prunePartial(stdout, stderr, *asJSON, exitError, "approval_expired", "no answer to approval "+aid+"; nothing was removed", total)
			}
			time.Sleep(prunePoll)
			continue
		}
		total = total.Add(r.Counts)
		if !r.More {
			return printPrune(stdout, *asJSON, r.Cutoff, aid, false, total)
		}
	}
}

// prunePartial reports an error with the counts of what was already removed
// (Docs/cli/prune.md §--json output).
func prunePartial(stdout, stderr io.Writer, asJSON bool, code int, errCode, msg string, total retention.Counts) int {
	if asJSON {
		writeJSON(stdout, pruneErrBody{Error: &ipc.Error{Code: errCode, Message: msg}, Counts: total})
		return code
	}
	_, _ = fmt.Fprintln(stderr, "agentnet: "+displaytext.Term(msg))
	if !total.Zero() {
		printPruneTable(stderr, "already removed:", total)
	}
	return code
}

func printPrune(stdout io.Writer, asJSON bool, cutoff, approvalID string, dryRun bool, c retention.Counts) int {
	if asJSON {
		writeJSON(stdout, pruneBody{OK: true, DryRun: dryRun, Cutoff: cutoff, Approval: approvalID, Counts: c})
		return exitOK
	}
	if c.Zero() {
		_, _ = fmt.Fprintf(stdout, "nothing to remove (finished before %s)\n", cutoff)
		return exitOK
	}
	head := "removed (finished before " + cutoff + "):"
	if dryRun {
		head = "would remove (finished before " + cutoff + "):"
	}
	printPruneTable(stdout, head, c)
	if dryRun {
		_, _ = fmt.Fprintln(stdout, "run again with --yes to remove them (you will be asked to approve)")
	}
	return exitOK
}

func printPruneTable(w io.Writer, head string, c retention.Counts) {
	_, _ = fmt.Fprintln(w, head)
	_, _ = fmt.Fprintf(w, "  requests           %5d\n", c.Requests)
	_, _ = fmt.Fprintf(w, "  work sessions      %5d\n", c.WorkSessions)
	_, _ = fmt.Fprintf(w, "  grants             %5d\n", c.Grants)
	_, _ = fmt.Fprintf(w, "  debates            %5d  (+ %d entries, %d constraints)\n", c.Debates, c.DebateEntries, c.DebateConstraints)
	_, _ = fmt.Fprintf(w, "  experience records %5d\n", c.ExperienceRecords)
	_, _ = fmt.Fprintf(w, "  inbox records      %5d\n", c.MailInbox)
	_, _ = fmt.Fprintf(w, "  old inbox copies   %5d\n", c.InboxBlanked)
}
