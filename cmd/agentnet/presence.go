package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// presenceBody is the machine-readable output of `presence [--json]`.
type presenceBody struct {
	OK bool `json:"ok"`
	daemon.PresenceGetResult
}

const presenceUsage = `Shows or sets who can see this machine's presence.

Usage:
  agentnet presence [--json]                        show the current mode
  agentnet presence --visible [--json]              all teams you are in (default)
  agentnet presence --invisible [--json]            nobody; teammates see last-seen only
  agentnet presence --only-team <team> [--json]     members of one team only
  agentnet presence --human on|off [--json]         share "human present"

--visible, --invisible and --only-team are mutually exclusive. --human may
be combined with any of them.

Invisible hides you from teammates. The relay operator can still see that
your daemon is connected. Going invisible, or leaving a peer's visibility,
sends that peer one "offline" message, so the peer sees you go offline at
once, with a last_seen of that moment.

Flags:
  --json    print {"ok":true,"mode":"visible"|"invisible"|"only_team",
            "team"?:{"id","name"},"human_share":true}

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

func runPresence(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet presence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	visible := fs.Bool("visible", false, "visible to every team you are in")
	invisible := fs.Bool("invisible", false, "invisible to every peer")
	onlyTeam := fs.String("only-team", "", "visible only to members of this team")
	human := fs.String("human", "", `share "human present": "on" or "off"`)
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, presenceUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	modeCount := 0
	if *visible {
		modeCount++
	}
	if *invisible {
		modeCount++
	}
	if *onlyTeam != "" {
		modeCount++
	}
	if modeCount > 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--visible, --invisible and --only-team are mutually exclusive")
	}
	if *human != "" && *human != "on" && *human != "off" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", `--human must be "on" or "off"`)
	}

	var res daemon.PresenceGetResult
	if modeCount == 0 && *human == "" {
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "presence_get", nil, &res); code != exitOK {
			return code
		}
	} else {
		p := daemon.PresenceSetParams{Visible: *visible, Invisible: *invisible, OnlyTeam: *onlyTeam}
		if *human != "" {
			p.Human = human
		}
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "presence_set", p, &res); code != exitOK {
			return code
		}
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(presenceBody{OK: true, PresenceGetResult: res})
		return exitOK
	}
	switch res.Mode {
	case "only_team":
		team := ""
		if res.Team != nil {
			team = fmt.Sprintf("%s (%s)", res.Team.Name, shortTeamID(res.Team.ID))
		}
		_, _ = fmt.Fprintf(stdout, "Presence: only team %s\n", team)
	default:
		_, _ = fmt.Fprintf(stdout, "Presence: %s\n", res.Mode)
	}
	shared := "not shared"
	if res.HumanShare {
		shared = "shared"
	}
	_, _ = fmt.Fprintf(stdout, "Human presence: %s\n", shared)
	return exitOK
}
