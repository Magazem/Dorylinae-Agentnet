package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// approveTimeout bounds every approve call; confirming an approval may run a
// waiting action, so it gets a little more room than the plain 2 s rule.
const approveTimeout = 4 * time.Second

// approvalListBody is the machine-readable output of `approve --list --json`.
type approvalListBody struct {
	OK bool `json:"ok"`
	daemon.ApprovalListResult
}

// approvalViewBody is the machine-readable output of `approve --reject --json`.
type approvalViewBody struct {
	OK       bool          `json:"ok"`
	Approval approval.View `json:"approval"`
}

const approveUsage = `Opens, rejects or lists a pending human approval
(Docs/protocol/approval.md), used by grants, sensitive-result release, the
own-device link and their policies. The code is never given to this
command: it is typed only into the AgentNet approval window the daemon
itself opens (or, on a headless machine started with
DORYLINAE_APPROVAL=terminal, on the daemon's own terminal).

Usage:
  agentnet approve --open <a-id> [--json]   show the approval window again
  agentnet approve --reject <a-id> [--json] reject; the waiting action is dropped
  agentnet approve --list [--json]          list pending approvals (never codes)

Flags:
  --open ID     reopen the approval window for a pending approval
  --reject ID   reject a pending approval instead of approving one
  --list        list pending approvals
  --json        print machine-readable JSON on stdout

Exit codes: 0 opened/rejected/listed, 1 error (unknown approval, expired,
locked, unavailable), 2 usage, 3 daemon not running.
`

func runApprove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	list := fs.Bool("list", false, "list pending approvals")
	open := fs.String("open", "", "reopen the approval window by id")
	reject := fs.String("reject", "", "reject a pending approval by id")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, approveUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	given := 0
	for _, v := range []bool{*list, *open != "", *reject != ""} {
		if v {
			given++
		}
	}

	switch {
	case len(pos) > 0:
		// The old "agentnet approve <a-id> <code>" form put the code in
		// this command's own argv (review 26, L7). It is removed by 2.2d:
		// the code is typed only into the AgentNet approval window, or on
		// the daemon's own terminal in terminal mode.
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage",
			"the code is not given here; type it into the AgentNet approval window (or the daemon's terminal in terminal mode)")
	case given == 0:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give --open <a-id>, --reject <a-id>, or --list (see 'agentnet approve --help')")
	case given > 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--list, --open and --reject cannot be combined")
	case *list:
		return runApproveList(*asJSON, stdout, stderr)
	case *open != "":
		return runApproveOpen(*asJSON, stdout, stderr, *open)
	default:
		return runApproveReject(*asJSON, stdout, stderr, *reject)
	}
}

func runApproveList(asJSON bool, stdout, stderr io.Writer) int {
	var res daemon.ApprovalListResult
	if code := callDaemon(asJSON, stdout, stderr, approveTimeout, "approval_list", nil, &res); code != exitOK {
		return code
	}
	if asJSON {
		if res.Approvals == nil {
			res.Approvals = []approval.View{}
		}
		_ = json.NewEncoder(stdout).Encode(approvalListBody{OK: true, ApprovalListResult: res})
		return exitOK
	}
	if len(res.Approvals) == 0 {
		_, _ = fmt.Fprintln(stdout, "No pending approvals.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tKIND\tSUMMARY\tEXPIRES\tATTEMPTS LEFT")
	for _, a := range res.Approvals {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", a.ID, a.Kind, a.Summary, a.Expires, a.AttemptsLeft)
	}
	_ = tw.Flush()
	return exitOK
}

func runApproveReject(asJSON bool, stdout, stderr io.Writer, id string) int {
	var res struct {
		Approval approval.View `json:"approval"`
	}
	if code := callDaemon(asJSON, stdout, stderr, approveTimeout, "approval_reject", map[string]string{"id": id}, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(approvalViewBody{OK: true, Approval: res.Approval})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Rejected %s (%s)\n", res.Approval.Kind, res.Approval.ID)
	return exitOK
}

func runApproveOpen(asJSON bool, stdout, stderr io.Writer, id string) int {
	var res struct {
		Approval approval.View `json:"approval"`
	}
	if code := callDaemon(asJSON, stdout, stderr, approveTimeout, "approval_open", map[string]string{"id": id}, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(approvalViewBody{OK: true, Approval: res.Approval})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Approval %s: window %s\n", res.Approval.ID, res.Approval.Window)
	return exitOK
}
