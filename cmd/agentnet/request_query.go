package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// requestShowBody is `request show --json`'s output.
type requestShowBody struct {
	OK bool `json:"ok"`
	daemon.RequestShowResult
}

func runRequestShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet request show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	from := fs.String("from", "", "pick the sender when the id matches requests from several peers")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet request --help')")
	}

	var res daemon.RequestShowResult
	params := map[string]any{"id": pos[0]}
	if *from != "" {
		params["from"] = *from
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_show", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(requestShowBody{OK: true, RequestShowResult: res})
		return exitOK
	}
	printRequestShow(stdout, res.Request)
	return exitOK
}

func printRequestShow(stdout io.Writer, v daemon.RequestView) {
	_, _ = fmt.Fprintf(stdout, "%s  %s to %s (team %s): %s\n", v.ID, v.Type, v.Peer.Name, v.Team.Name, v.Title)
	stateAt := ""
	if v.StateAt != nil {
		stateAt = " " + *v.StateAt
	}
	delivery := v.Delivery
	if delivery == "" {
		_, _ = fmt.Fprintf(stdout, "  state %s%s\n", v.State, stateAt)
	} else {
		_, _ = fmt.Fprintf(stdout, "  state %s%s, delivery %s\n", v.State, stateAt, delivery)
	}
	if v.Reason != "" {
		_, _ = fmt.Fprintf(stdout, "  reason: %s\n", v.Reason)
	}
	if v.Note != "" {
		_, _ = fmt.Fprintf(stdout, "  note: %s\n", v.Note)
	}
	if v.Cancel != "" {
		_, _ = fmt.Fprintf(stdout, "  cancel: %s\n", v.Cancel)
	}
	for _, a := range v.Artifacts {
		_, _ = fmt.Fprintf(stdout, "  artifact: %s\n", formatArtifact(a))
	}
	if v.Result != nil {
		r := v.Result
		exit := ""
		if r.ExitCode != nil {
			exit = fmt.Sprintf(", exit %d", *r.ExitCode)
		}
		summary := ""
		if r.Summary != "" {
			summary = ": " + r.Summary
		}
		_, _ = fmt.Fprintf(stdout, "  result: %s%s%s\n", r.Status, exit, summary)
		for _, a := range r.Artifacts {
			_, _ = fmt.Fprintf(stdout, "  artifact: %s\n", formatArtifact(a))
		}
		if r.Output != "" {
			_, _ = fmt.Fprintf(stdout, "  output (%d bytes):\n", r.OutputBytes)
			_, _ = fmt.Fprintf(stdout, "    %s\n", r.Output)
		}
	}
}

func formatArtifact(a daemon.RequestArtifact) string {
	var parts []string
	if a.URL != "" {
		parts = append(parts, "url="+a.URL)
	}
	if a.Branch != "" {
		parts = append(parts, "branch="+a.Branch)
	}
	if a.Commit != "" {
		parts = append(parts, "commit="+a.Commit)
	}
	if a.Path != "" {
		parts = append(parts, "path="+a.Path)
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// requestListBody is `request list --json`'s output.
type requestListBody struct {
	OK bool `json:"ok"`
	daemon.RequestListResult
}

func runRequestList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet request list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	state := fs.String("state", "", "pending, accepted, declined, deferred, completed or cancelled")
	team := fs.String("team", "", "filter by team")
	peer := fs.String("peer", "", "filter by peer")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "request list takes no positional arguments")
	}

	var res daemon.RequestListResult
	params := map[string]any{}
	if *state != "" {
		params["state"] = *state
	}
	if *team != "" {
		params["team"] = *team
	}
	if *peer != "" {
		params["peer"] = *peer
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_list", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(requestListBody{OK: true, RequestListResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintln(stdout, "ID                                  TO   TYPE    URGENCY  STATE      RESULT  DELIVERY   CREATED")
	for _, v := range res.Requests {
		result := "-"
		if v.Result != nil {
			result = v.Result.Status
		}
		_, _ = fmt.Fprintf(stdout, "%s  %s  %s  %s  %s  %s  %s  %s\n",
			v.ID, v.Peer.Name, v.Type, v.Urgency, v.State, result, v.Delivery, v.Created)
	}
	return exitOK
}

// requestResendBody is `request resend --json`'s output.
type requestResendBody struct {
	OK bool `json:"ok"`
	daemon.RequestResendResult
}

func runRequestResend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet request resend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet request --help')")
	}

	var res daemon.RequestResendResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_resend", map[string]any{"id": pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(requestResendBody{OK: true, RequestResendResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Resent %s (mail %s)\n", res.ID, res.MailID)
	return exitOK
}

// requestCancelBody is `request cancel --json`'s output.
type requestCancelBody struct {
	OK bool `json:"ok"`
	daemon.RequestCancelResult
}

func runRequestCancel(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet request cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	reason := fs.String("reason", "", "optional, 1-500 characters, shown to the recipient")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet request --help')")
	}

	var res daemon.RequestCancelResult
	params := map[string]any{"id": pos[0]}
	if *reason != "" {
		params["reason"] = *reason
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_cancel", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(requestCancelBody{OK: true, RequestCancelResult: res})
		return exitOK
	}
	switch {
	case res.Duplicate && res.Request.State == "cancelled":
		_, _ = fmt.Fprintf(stdout, "%s is already cancelled\n", pos[0])
	case res.Duplicate:
		_, _ = fmt.Fprintf(stdout, "Cancel already sent for %s\n", pos[0])
	default:
		_, _ = fmt.Fprintf(stdout, "Cancel sent for %s to %s (waiting for %s's daemon to confirm)\n", pos[0], res.Request.Peer.Name, res.Request.Peer.Name)
	}
	return exitOK
}
