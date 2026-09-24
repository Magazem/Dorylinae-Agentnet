package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

const grantUsage = `Give a teammate's agent scoped, expiring, revocable read access (Docs/protocol/grant.md).

Usage:
  agentnet grant @peer --session S --action fs.read|git.read --resource PATH[#BRANCH]
                 [--scope P] [--expires D] [--public] [--json]
  agentnet grant policy add|list|remove ...
  agentnet grants [--session S] [--issued|--held] [--json]
  agentnet revoke <g-id> [--json]
  agentnet fetch <g-id> <path> [--out FILE] [--json]   (the holder's side)

Run 'agentnet grant --help' and 'agentnet grant policy --help' for flags.
`

func runGrant(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "policy" {
		return runGrantPolicy(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("agentnet grant", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	sessionID := fs.String("session", "", "the work session (s-...) the grant belongs to")
	action := fs.String("action", "", "fs.read or git.read")
	resource := fs.String("resource", "", "absolute path of a directory, or PATH#BRANCH for git.read")
	scope := fs.String("scope", "", "relative path prefix inside the resource")
	expires := fs.String("expires", "", "duration, 1m to 7d (default 2h)")
	public := fs.Bool("public", false, "git.read only: the repository is not private (no result quarantine)")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Give the worker of a work session read access to one of your directories or git branches.

Usage:
  agentnet grant @peer --session S --action fs.read|git.read --resource PATH[#BRANCH]
                 [--scope P] [--expires D] [--public] [--json]

Flags:
  --session S    the work session (s-...) between you (requester) and @peer (worker)
  --action A     fs.read (a directory) or git.read (a branch of a repository)
  --resource R   an absolute path of an existing directory; for git.read it must
                 be the top of a work tree or a bare repository and needs
                 #BRANCH. The path itself is never sent to the peer.
  --scope P      only this relative path inside the resource (default: all of it)
  --expires D    a duration from 1m to 7d, default 2h
  --public       git.read only: mark the repository as not private. Every fs.read
                 grant, and every git.read grant without --public, is sensitive:
                 the session's result is quarantined until you release it.
  --json         print {"ok":true,"grant":{...},"approval":{...}}; "approval" is
                 absent when a policy approved the grant at once.

The command never asks for the approval code. Unless a policy matches, the grant
waits as "pending_approval" until you type the code shown by the AgentNet approval
window ('agentnet approve --open <approval-id>' reopens it). It is sent to the peer
once approved.

Errors: unknown_session, not_requester, bad_state, unknown_peer, unverified_peer,
forbidden_resource, bad_request, approval_limit, approval_locked, approval_unavailable.

Exit codes: 0 grant created (pending approval or issued), 1 error, 2 usage,
3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, grantUsage)
		return exitOK
	}
	if len(pos) != 1 || *sessionID == "" || *action == "" || *resource == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give @peer, --session, --action and --resource (see 'agentnet grant --help')")
	}
	params := daemon.GrantCreateParams{
		Peer: pos[0], Session: *sessionID, Action: *action, Resource: *resource,
		Scope: *scope, Expires: *expires, Public: *public,
	}
	var res struct {
		Grant    daemon.GrantView `json:"grant"`
		Approval *approval.View   `json:"approval"`
	}
	if code := callDaemon(*asJSON, stdout, stderr, approveTimeout, "grant_create", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK       bool             `json:"ok"`
			Grant    daemon.GrantView `json:"grant"`
			Approval *approval.View   `json:"approval,omitempty"`
		}{OK: true, Grant: res.Grant, Approval: res.Approval})
		return exitOK
	}
	if res.Approval != nil {
		_, _ = fmt.Fprintf(stdout, "Grant %s: pending approval %s. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open %s').\nIt is sent to %s once approved.\n",
			res.Grant.ID, res.Approval.ID, res.Approval.ID, devicePeerLabel(res.Grant.Peer))
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Grant %s issued to %s by policy (%s, expires %s).\n", res.Grant.ID, devicePeerLabel(res.Grant.Peer), res.Grant.Action, res.Grant.Exp)
	return exitOK
}

func runGrants(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet grants", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	sessionID := fs.String("session", "", "only the grants of this work session")
	issued := fs.Bool("issued", false, "only grants you gave")
	held := fs.Bool("held", false, "only grants you hold")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `List grants.

Usage:
  agentnet grants [--session S] [--issued|--held] [--json]

Flags:
  --session S   only the grants of this work session
  --issued      only grants you gave (as requester)
  --held        only grants you hold (as worker)
  --json        print {"ok":true,"grants":[{"id","direction","peer","session",
                "action","resource":{"kind","label","branch"?,"path"?},"scope"?,
                "nbf","exp","sensitive","state","revoked_at"?,"reason"?}]}.
                "path" is the local path and appears on issued grants only.

State is pending_approval, active, revoked, or expired (past its exp).

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon not running.
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *issued && *held {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--issued and --held exclude each other")
	}
	params := daemon.GrantListParams{Session: *sessionID}
	switch {
	case *issued:
		params.Direction = "issued"
	case *held:
		params.Direction = "held"
	}
	var res struct {
		Grants []daemon.GrantView `json:"grants"`
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "grant_list", params, &res); code != exitOK {
		return code
	}
	if res.Grants == nil {
		res.Grants = []daemon.GrantView{}
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK     bool               `json:"ok"`
			Grants []daemon.GrantView `json:"grants"`
		}{OK: true, Grants: res.Grants})
		return exitOK
	}
	if len(res.Grants) == 0 {
		_, _ = fmt.Fprintln(stdout, "No grants.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tDIR\tPEER\tACTION\tRESOURCE\tSCOPE\tEXPIRES\tSTATE")
	for _, g := range res.Grants {
		resName := g.Resource.Label
		if g.Resource.Branch != "" {
			resName += "#" + g.Resource.Branch
		}
		scope := g.Scope
		if scope == "" {
			scope = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.Direction, devicePeerLabel(g.Peer), g.Action, resName, scope, g.Exp, g.State)
	}
	_ = tw.Flush()
	return exitOK
}

func runRevoke(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Revoke a grant you gave. The holder's next fetch fails with "revoked".

Usage:
  agentnet revoke <g-id> [--json]

The grantor's daemon checks the grant on every fetch, so the revocation takes
effect at once; the holder is also told by mail. Revoking needs no approval.
Revoking a grant that is already revoked succeeds ("duplicate").

Flags:
  --json    print {"ok":true,"grant":{...},"mail_id":"m-..."?,"duplicate"?:true}

Errors: unknown_grant, not_grantor.

Exit codes: 0 revoked, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <g-id> (see 'agentnet revoke --help')")
	}
	var res daemon.GrantRevokeResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "grant_revoke", daemon.GrantRevokeParams{ID: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.GrantRevokeResult
		}{OK: true, GrantRevokeResult: res})
		return exitOK
	}
	if res.Duplicate {
		_, _ = fmt.Fprintf(stdout, "Grant %s was already revoked.\n", res.Grant.ID)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Revoked grant %s; %s's next fetch fails.\n", res.Grant.ID, devicePeerLabel(res.Grant.Peer))
	return exitOK
}

const grantPolicyUsage = `Grant policies let grants issue without a prompt (Docs/protocol/grant.md §Policies).

Usage:
  agentnet grant policy add @peer --action A --resource PATH[#BRANCH] --max-expires D
                 [--scope P] [--public] [--until D] [--json]
  agentnet grant policy list [--json]
  agentnet grant policy remove <p-id> [--json]

add     needs a human approval (type the code in the approval window); prints the
        approval id and exits 0. A policy matches only the exact peer, action,
        resource path and branch, a scope inside its own, the same sensitivity
        (--public covers only --public grants) and an expiry up to --max-expires.
        It ends at --until (default 30d, at most 90d). At most 50 policies.
list    show the policies.
remove  delete a policy (no approval needed).

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon not running.
`

func runGrantPolicy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, grantPolicyUsage)
		return exitOK
	}
	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, grantPolicyUsage)
		return exitOK
	case "add":
		return runGrantPolicyAdd(args[1:], stdout, stderr)
	case "list":
		return runGrantPolicyList(args[1:], stdout, stderr)
	case "remove":
		return runGrantPolicyRemove(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "agentnet grant policy: unknown subcommand %q\n\n", args[0])
	_, _ = fmt.Fprint(stderr, grantPolicyUsage)
	return exitUsage
}

func runGrantPolicyAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet grant policy add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	action := fs.String("action", "", "fs.read or git.read")
	resource := fs.String("resource", "", "absolute path, or PATH#BRANCH for git.read")
	scope := fs.String("scope", "", "relative path prefix")
	public := fs.Bool("public", false, "covers only --public git.read grants")
	maxExpires := fs.String("max-expires", "", "the longest expiry the policy covers")
	until := fs.String("until", "", "the policy's own end (default 30d, at most 90d)")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, grantPolicyUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 || *action == "" || *resource == "" || *maxExpires == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give @peer, --action, --resource and --max-expires (see 'agentnet grant policy --help')")
	}
	params := daemon.GrantPolicyAddParams{
		Peer: pos[0], Action: *action, Resource: *resource, Scope: *scope, Public: *public,
		MaxExpires: *maxExpires, Until: *until,
	}
	var res struct {
		Approval approval.View `json:"approval"`
	}
	if code := callDaemon(*asJSON, stdout, stderr, approveTimeout, "grant_policy_add", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK       bool          `json:"ok"`
			Approval approval.View `json:"approval"`
		}{OK: true, Approval: res.Approval})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Policy pending approval %s. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open %s').\n", res.Approval.ID, res.Approval.ID)
	return exitOK
}

func runGrantPolicyList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet grant policy list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, grantPolicyUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	var res struct {
		Policies []daemon.GrantPolicyView `json:"policies"`
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "grant_policy_list", nil, &res); code != exitOK {
		return code
	}
	if res.Policies == nil {
		res.Policies = []daemon.GrantPolicyView{}
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK       bool                     `json:"ok"`
			Policies []daemon.GrantPolicyView `json:"policies"`
		}{OK: true, Policies: res.Policies})
		return exitOK
	}
	if len(res.Policies) == 0 {
		_, _ = fmt.Fprintln(stdout, "No grant policies.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPEER\tACTION\tBRANCH\tSCOPE\tPUBLIC\tMAX EXPIRES\tUNTIL")
	for _, p := range res.Policies {
		branch, scope := p.Branch, p.Scope
		if branch == "" {
			branch = "-"
		}
		if scope == "" {
			scope = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\t%ds\t%s\n", p.ID, devicePeerLabel(p.Peer), p.Action, branch, scope, p.Public, p.MaxExpiresS, p.Until)
	}
	_ = tw.Flush()
	return exitOK
}

func runGrantPolicyRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet grant policy remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, grantPolicyUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <p-id> (see 'agentnet grant policy --help')")
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "grant_policy_remove", map[string]string{"id": pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
		}{OK: true})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Removed policy %s.\n", pos[0])
	return exitOK
}
