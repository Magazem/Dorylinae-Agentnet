package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

func runPeers(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet peers", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `List the agents this machine has paired with.

Usage:
  agentnet peers [--json]

Flags:
  --json    print machine-readable JSON on stdout:
            {"ok":true,"peers":[{"public_key","name","harness",
             "skills":[{"id","name","description"}],"paired_at"}]}
            "peers" is an empty array when nothing is paired; paired_at is
            RFC 3339 UTC.

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

	var res daemon.PeersResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "peers", nil, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(peersBody{OK: true, PeersResult: res})
		return exitOK
	}
	if len(res.Peers) == 0 {
		_, _ = fmt.Fprintln(stdout, "No peers paired yet. Run 'agentnet pair --new' to start.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tHARNESS\tSKILLS\tPAIRED\tPUBLIC KEY")
	for _, p := range res.Peers {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Harness, skillList(p.Skills), p.PairedAt, p.PublicKey)
	}
	_ = tw.Flush()
	return exitOK
}
