package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// peerBody is the machine-readable success output of `peers verify|remove --json`.
type peerBody struct {
	OK bool `json:"ok"`
	daemon.PeerResult
}

func runPeers(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "verify":
			return runPeersVerify(args[1:], stdout, stderr)
		case "remove":
			return runPeersRemove(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("agentnet peers", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `List the agents this machine has paired with.

Usage:
  agentnet peers [--json]
  agentnet peers verify <peer> <fingerprint> [--json]
  agentnet peers remove <peer> [--json]

Flags:
  --json    print machine-readable JSON on stdout:
            {"ok":true,"peers":[{"public_key","name","harness",
             "skills":[{"id","name","description"}],"paired_at",
             "trust":"relay|code|fingerprint","fingerprint":"<20 characters>"}]}
            "peers" is an empty array when nothing is paired; paired_at is
            RFC 3339 UTC.

Subcommands:
  verify   mark a peer as verified after comparing its fingerprint with the
           one shown on the other machine ('agentnet identity'). A wrong
           fingerprint exits non-zero and changes nothing.
  remove   delete a peer; its later requests are rejected as unpaired.
  <peer> is a public key or a unique peer name.

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
	_, _ = fmt.Fprintln(tw, "NAME\tHARNESS\tSKILLS\tPAIRED\tTRUST\tFINGERPRINT\tPUBLIC KEY")
	for _, p := range res.Peers {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Harness, skillList(p.Skills), p.PairedAt,
			p.Trust, envelope.FormatFingerprint(p.Fingerprint), p.PublicKey)
	}
	_ = tw.Flush()
	return exitOK
}

func runPeersVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet peers verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Mark a peer as verified by its key fingerprint.

Usage:
  agentnet peers verify <peer> <fingerprint> [--json]

Compare the fingerprint shown by 'agentnet identity' on the peer's machine
(over a channel you trust, such as in person or a call). <peer> is a public
key or a unique peer name. The fingerprint may be written with or without
spaces or dashes, in any case.

A match sets the peer's trust to "fingerprint". A mismatch exits 1 with error
code "fingerprint_mismatch" and changes nothing.

Flags:
  --json    print {"ok":true,"peer":{...}} (or {"ok":false,"error":{...}})

Exit codes: 0 verified, 1 error or mismatch, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) < 2 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <peer> and <fingerprint> (see 'agentnet peers verify --help')")
	}
	// A fingerprint typed with spaces arrives as several arguments.
	params := daemon.PeerVerifyParams{Peer: pos[0], Fingerprint: strings.Join(pos[1:], " ")}
	var res daemon.PeerResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "peers_verify", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(peerBody{OK: true, PeerResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Verified %s (%s): trust is now %s\n", res.Peer.Name, envelope.FormatFingerprint(res.Peer.Fingerprint), res.Peer.Trust)
	return exitOK
}

func runPeersRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet peers remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Remove a paired peer.

Usage:
  agentnet peers remove <peer> [--json]

<peer> is a public key or a unique peer name. The peer is deleted; requests
from its key are rejected as "unpaired" until you pair again.

Flags:
  --json    print {"ok":true,"peer":{...the removed peer...}}
            (or {"ok":false,"error":{...}})

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
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <peer> (see 'agentnet peers remove --help')")
	}
	var res daemon.PeerResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "peers_remove", daemon.PeerRemoveParams{Peer: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(peerBody{OK: true, PeerResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Removed %s (%s)\n", res.Peer.Name, envelope.FormatFingerprint(res.Peer.Fingerprint))
	return exitOK
}
