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

const approveUsage = `Confirms or rejects a pending human approval
(Docs/protocol/approval.md), used by grants, sensitive-result release, the
own-device link and their policies.

Usage:
  agentnet approve <a-id> <code> [--json]   confirm with the code from the
                                             desktop notification
  agentnet approve --reject <a-id> [--json] reject; the waiting action is dropped
  agentnet approve --list [--json]          list pending approvals (never codes)

The code is shown only on the desktop notification, never in this command's
own output, the daemon log or the audit log. 3 wrong codes reject the
approval; it also expires 10 minutes after it was created, or immediately if
the daemon restarts meanwhile.

Flags:
  --reject ID   reject a pending approval instead of confirming one
  --list        list pending approvals
  --json        print machine-readable JSON on stdout

Exit codes: 0 confirmed/rejected/listed, 1 error (wrong code, expired,
locked, unavailable, or the waiting action's own error), 2 usage,
3 daemon not running.
`

func runApprove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	list := fs.Bool("list", false, "list pending approvals")
	reject := fs.String("reject", "", "reject a pending approval by id")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, approveUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}

	switch {
	case *list && (*reject != "" || len(pos) > 0):
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--list takes no id or code and cannot be combined with --reject")
	case *list:
		return runApproveList(*asJSON, stdout, stderr)
	case *reject != "":
		if len(pos) > 0 {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--reject takes no code")
		}
		return runApproveReject(*asJSON, stdout, stderr, *reject)
	case len(pos) == 2:
		return runApproveConfirm(*asJSON, stdout, stderr, pos[0], pos[1])
	default:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <a-id> and <code>, --reject <a-id>, or --list (see 'agentnet approve --help')")
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

func runApproveConfirm(asJSON bool, stdout, stderr io.Writer, id, code string) int {
	var res map[string]json.RawMessage
	if exitCode := callDaemon(asJSON, stdout, stderr, approveTimeout, "approval_confirm", map[string]string{"id": id, "code": code}, &res); exitCode != exitOK {
		return exitCode
	}
	var view approval.View
	if raw, ok := res["approval"]; ok {
		_ = json.Unmarshal(raw, &view)
	}
	if asJSON {
		out := make(map[string]json.RawMessage, len(res)+1)
		for k, v := range res {
			out[k] = v
		}
		okRaw, _ := json.Marshal(true)
		out["ok"] = okRaw
		_ = json.NewEncoder(stdout).Encode(out)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Approved %s (%s)\n", view.Kind, view.ID)
	return exitOK
}
