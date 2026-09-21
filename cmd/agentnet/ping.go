package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
)

// pingBody is the machine-readable output of `ping --json`; ok is false when the ping failed.
type pingBody struct {
	OK bool `json:"ok"`
	daemon.PingStatus
}

func runPing(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet ping", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	statusID := fs.String("status", "", "show the state of the ping with this ID")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Send an end-to-end encrypted ping to a paired agent and report the round trip.

Usage:
  agentnet ping @peer [--json]          ping a peer by name or public key
  agentnet ping --status <id> [--json]  check a ping that was still pending

Flags:
  --status ID  show the state of a ping by its ID
  --json       print machine-readable JSON on stdout:
               {"ok":true,"ping_id","peer":{"public_key","name"},
                "state":"pending|complete|failed",
                "rtt_ms"                   (complete: encrypted round trip, ms),
                "handshake"                (true if a new session was set up),
                "error":{"code","message"} (failed: timeout, peer_offline,
                                            handshake_failed, send_failed)}
               Failures that stop the request itself print
               {"ok":false,"error":{"code","message"}} (unknown_peer,
               ambiguous_peer, no_relay, relay_unavailable, unknown_ping).

The peer must be paired (see 'agentnet peers'). The ping travels inside a
Noise XX session through the relay, which sees only ciphertext. The command
returns in under 2 seconds; if the pong has not arrived by then, state is
"pending" and the ping ID can be polled with --status. A ping with no answer
fails after 10 seconds.

Exit codes: 0 ok (including pending), 1 error or ping failed, 2 usage,
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

	var method string
	var params any
	switch {
	case *statusID != "" && len(pos) > 0:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--status takes no peer")
	case *statusID != "":
		method, params = "ping_status", daemon.PingStatusParams{PingID: *statusID}
	case len(pos) == 1:
		method, params = "ping", daemon.PingParams{Peer: pos[0]}
	case len(pos) > 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[1]))
	default:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give @peer or --status <id> (see 'agentnet ping --help')")
	}

	var res daemon.PingStatus
	if code := callDaemon(*asJSON, stdout, stderr, pairTimeout, method, params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(pingBody{OK: res.State != session.StateFailed, PingStatus: res})
	}
	switch res.State {
	case session.StateComplete:
		if !*asJSON && res.RTTMillis != nil {
			_, _ = fmt.Fprintf(stdout, "pong from %s: rtt %.1f ms (encrypted)\n", peerLabel(res.Peer), *res.RTTMillis)
		}
	case session.StatePending:
		if !*asJSON {
			_, _ = fmt.Fprintf(stdout, "Ping %s to %s is still in flight.\nCheck it with: agentnet ping --status %s\n", res.ID, peerLabel(res.Peer), res.ID)
		}
	case session.StateFailed:
		if !*asJSON {
			msg := "ping failed"
			if res.Error != nil {
				msg = fmt.Sprintf("ping to %s failed: %s (%s)", peerLabel(res.Peer), res.Error.Message, res.Error.Code)
			}
			_, _ = fmt.Fprintln(stderr, "agentnet: "+msg)
		}
		return exitError
	}
	return exitOK
}

func peerLabel(p session.PeerRef) string {
	if p.Name != "" {
		return "@" + p.Name
	}
	return p.PublicKey
}
