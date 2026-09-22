package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// teamBody is the machine-readable success output of `team create|remove|rename|leave|delete --json`.
type teamBody struct {
	OK bool `json:"ok"`
	daemon.TeamResult
}

// teamListBody is the machine-readable output of `team list --json`.
type teamListBody struct {
	OK bool `json:"ok"`
	daemon.TeamListResult
}

// teamShowBody is the machine-readable output of `team show --json`.
type teamShowBody struct {
	OK bool `json:"ok"`
	daemon.TeamShowResult
}

func runTeam(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "create":
			return runTeamCreate(args[1:], stdout, stderr)
		case "list":
			return runTeamList(args[1:], stdout, stderr)
		case "show":
			return runTeamShow(args[1:], stdout, stderr)
		case "remove":
			return runTeamRemove(args[1:], stdout, stderr)
		case "rename":
			return runTeamRename(args[1:], stdout, stderr)
		case "leave":
			return runTeamLeave(args[1:], stdout, stderr)
		case "delete":
			return runTeamDelete(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("agentnet team", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, teamUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(false, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "agentnet: unknown team subcommand %q\n\n", args[0])
		_, _ = fmt.Fprint(stdout, teamUsage)
		return exitUsage
	}
	_, _ = fmt.Fprint(stdout, teamUsage)
	return exitOK
}

const teamUsage = `Creates and manages teams: named sets of paired agents that share presence
and requests.

Usage:
  agentnet team create <name> [--json]
  agentnet team list [--all] [--json]
  agentnet team show <team> [--json]
  agentnet team remove <team> <peer> [--json]      owner
  agentnet team rename <team> <new-name> [--json]  owner
  agentnet team leave <team> [--json]              member (not the owner)
  agentnet team delete <team> [--json]             owner: dissolve the team

<team> is a team id (t-...) or a team name that is unique on this machine.
<peer> is a peer name or public key, with an optional leading '@'.

Run 'agentnet team <subcommand> --help' for subcommand flags.

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon not running.
`

func runTeamCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Create a new team, owned by this agent.

Usage:
  agentnet team create <name> [--json]

<name> must match ^[a-z0-9][a-z0-9-]{0,31}$ and must not equal the name of
another active local team.

Flags:
  --json    print {"ok":true,"team":{"id","name","owner","epoch","state","role","members"}}
            (or {"ok":false,"error":{"code","message"}})

Exit codes: 0 created, 1 error, 2 usage, 3 daemon not running.
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
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <name> (see 'agentnet team create --help')")
	}
	var res daemon.TeamResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_create", daemon.TeamCreateParams{Name: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamBody{OK: true, TeamResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Created team %s (%s). You are the owner.\n", res.Team.Name, res.Team.ID)
	return exitOK
}

func runTeamList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	all := fs.Bool("all", false, "also list teams that were left, removed or dissolved")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `List teams on this machine.

Usage:
  agentnet team list [--all] [--json]

Flags:
  --all     also list teams that were left, removed or dissolved
  --json    print {"ok":true,"teams":[{"id","name","owner","epoch","state","role","members"}]}

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
	var res daemon.TeamListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_list", daemon.TeamListParams{All: *all}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamListBody{OK: true, TeamListResult: res})
		return exitOK
	}
	if len(res.Teams) == 0 {
		_, _ = fmt.Fprintln(stdout, "No teams yet. Run 'agentnet team create <name>' to start.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tID\tROLE\tMEMBERS\tSTATE")
	for _, t := range res.Teams {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", t.Name, t.ID, t.Role, t.Members, t.State)
	}
	_ = tw.Flush()
	return exitOK
}

func runTeamShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Show a team's members.

Usage:
  agentnet team show <team> [--json]

Flags:
  --json    print {"ok":true,"team":{"id","name","owner","epoch","state","role",
            "members":[{"name","public_key","fingerprint","added","owner","self"}]}}

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon not running.
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
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <team> (see 'agentnet team show --help')")
	}
	var res daemon.TeamShowResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_show", daemon.TeamRefParams{Team: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamShowBody{OK: true, TeamShowResult: res})
		return exitOK
	}
	t := res.Team
	ownerName := t.Owner
	for _, m := range t.Members {
		if m.PublicKey == t.Owner {
			ownerName = m.Name
			break
		}
	}
	_, _ = fmt.Fprintf(stdout, "%s (%s), owner %s, epoch %d\n", t.Name, shortTeamID(t.ID), ownerName, t.Epoch)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tROLE\tADDED\tFINGERPRINT")
	for _, m := range t.Members {
		role := "member"
		if m.Owner {
			role = "owner"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Name, role, m.Added, envelope.FormatFingerprint(m.Fingerprint))
	}
	_ = tw.Flush()
	return exitOK
}

// shortTeamID shows a team id as "t-0123...cdef": the "t-" prefix, the first
// 4 hex characters, an ellipsis, and the last 4.
func shortTeamID(id string) string {
	const prefix = len("t-") + 4
	if len(id) <= prefix+4 {
		return id
	}
	return id[:prefix] + "…" + id[len(id)-4:]
}

func runTeamRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Remove a member from a team you own.

Usage:
  agentnet team remove <team> <peer> [--json]

Flags:
  --json    print {"ok":true,"team":{...}} (or {"ok":false,"error":{...}})

Exit codes: 0 removed, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 2 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <team> and <peer> (see 'agentnet team remove --help')")
	}
	var res daemon.TeamResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_remove", daemon.TeamRemoveParams{Team: pos[0], Peer: pos[1]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamBody{OK: true, TeamResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Removed %s from %s (epoch %d)\n", pos[1], res.Team.Name, res.Team.Epoch)
	return exitOK
}

func runTeamRename(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team rename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Rename a team you own.

Usage:
  agentnet team rename <team> <new-name> [--json]

Flags:
  --json    print {"ok":true,"team":{...}} (or {"ok":false,"error":{...}})

Exit codes: 0 renamed, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 2 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <team> and <new-name> (see 'agentnet team rename --help')")
	}
	var res daemon.TeamResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_rename", daemon.TeamRenameParams{Team: pos[0], Name: pos[1]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamBody{OK: true, TeamResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Renamed team to %s (%s, epoch %d)\n", res.Team.Name, res.Team.ID, res.Team.Epoch)
	return exitOK
}

func runTeamLeave(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team leave", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Leave a team you are a member of (not the owner).

Usage:
  agentnet team leave <team> [--json]

Flags:
  --json    print {"ok":true,"team":{...}} (or {"ok":false,"error":{...}})

Exit codes: 0 left, 1 error, 2 usage, 3 daemon not running.
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
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <team> (see 'agentnet team leave --help')")
	}
	var res daemon.TeamResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_leave", daemon.TeamRefParams{Team: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamBody{OK: true, TeamResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Left %s (%s)\n", res.Team.Name, res.Team.ID)
	return exitOK
}

func runTeamDelete(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet team delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Dissolve a team you own.

Usage:
  agentnet team delete <team> [--json]

Flags:
  --json    print {"ok":true,"team":{...}} (or {"ok":false,"error":{...}})

Exit codes: 0 deleted, 1 error, 2 usage, 3 daemon not running.
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
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <team> (see 'agentnet team delete --help')")
	}
	var res daemon.TeamResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "team_delete", daemon.TeamRefParams{Team: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(teamBody{OK: true, TeamResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Deleted %s (%s)\n", res.Team.Name, res.Team.ID)
	return exitOK
}
