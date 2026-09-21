package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// pairTimeout bounds the whole pair call (acceptance: under 2 s). The daemon
// answers within one second and hands back a pairing ID to poll otherwise.
const pairTimeout = 1900 * time.Millisecond

// pairBody is the machine-readable output of `pair --json`; ok is false when the pairing failed.
type pairBody struct {
	OK bool `json:"ok"`
	daemon.PairStatus
}

// peersBody is the machine-readable success output of `peers --json`.
type peersBody struct {
	OK bool `json:"ok"`
	daemon.PeersResult
}

func runPair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet pair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	newCode := fs.Bool("new", false, "issue a one-time pairing code")
	statusID := fs.String("status", "", "show the state of the pairing with this ID")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Pair with an agent on another machine through the relay.

Usage:
  agentnet pair --new [--json]         issue a one-time code (valid 10 minutes)
  agentnet pair <code> [--json]        redeem a code shown by the other machine
  agentnet pair --status <id> [--json] check a pairing that was still pending

Flags:
  --new        issue a one-time pairing code and return; enter the code on the
               other machine with 'agentnet pair <code>'
  --status ID  show the state of a pairing by its ID
  --json       print machine-readable JSON on stdout:
               {"ok":true,"pairing_id","role":"issuer|redeemer",
                "state":"pending|complete|failed",
                "code","expires"           (issuer, while pending),
                "peer":{"public_key","name","harness","skills","paired_at"} (complete),
                "error":{"code","message"} (failed)}
               Failures that stop the request itself print
               {"ok":false,"error":{"code","message"}}.

The command returns in under 2 seconds. If the exchange is not finished by
then, state is "pending" and the pairing ID can be polled with --status. Both
daemons verify the other's signed Agent Card before storing the peer; a card
with a bad signature aborts the pairing and nothing is stored. Codes are
single use. List the result with 'agentnet peers'.

Exit codes: 0 ok (including pending), 1 error or pairing failed, 2 usage,
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
	case *newCode && (*statusID != "" || len(pos) > 0):
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--new takes no code and cannot be combined with --status")
	case *newCode:
		method = "pair_new"
	case *statusID != "":
		if len(pos) > 0 {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--status takes no code")
		}
		method, params = "pair_status", daemon.PairStatusParams{PairingID: *statusID}
	case len(pos) == 1:
		method, params = "pair_redeem", daemon.PairRedeemParams{Code: pos[0]}
	case len(pos) > 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[1]))
	default:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give --new, a code, or --status <id> (see 'agentnet pair --help')")
	}

	var res daemon.PairStatus
	if code := callDaemon(*asJSON, stdout, stderr, pairTimeout, method, params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(pairBody{OK: res.State != peers.StateFailed, PairStatus: res})
	} else {
		printPair(stdout, res)
	}
	if res.State == peers.StateFailed {
		if !*asJSON {
			msg := "pairing failed"
			if res.Error != nil {
				msg = fmt.Sprintf("pairing failed: %s (%s)", res.Error.Message, res.Error.Code)
			}
			_, _ = fmt.Fprintln(stderr, "agentnet: "+msg)
		}
		return exitError
	}
	return exitOK
}

func printPair(w io.Writer, st daemon.PairStatus) {
	switch {
	case st.State == peers.StateComplete && st.Peer != nil:
		_, _ = fmt.Fprintf(w, "Paired with %s (%s)\n  public key: %s\n", st.Peer.Name, st.Peer.Harness, st.Peer.PublicKey)
	case st.State == peers.StatePending && st.Role == peers.RoleIssuer && st.Code != "":
		_, _ = fmt.Fprintf(w, "Pairing code: %s\nExpires:      %s\nPairing ID:   %s\n\nOn the other machine run: agentnet pair %s\nThen check here with:     agentnet peers\n",
			formatCode(st.Code), st.Expires, st.ID, formatCode(st.Code))
	case st.State == peers.StatePending:
		_, _ = fmt.Fprintf(w, "Pairing %s is still in progress.\nCheck it with: agentnet pair --status %s\n", st.ID, st.ID)
	}
}

// formatCode shows a 10-character code as XXXXX-XXXXX.
func formatCode(c string) string {
	if len(c) == 10 {
		return c[:5] + "-" + c[5:]
	}
	return c
}

// parseInterspersed parses flags that may appear before or after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// callDaemon makes one IPC call and reports failures as the command's output.
// It returns exitOK when res is filled in.
func callDaemon(asJSON bool, stdout, stderr io.Writer, timeout time.Duration, method string, params, res any) int {
	p, err := paths.Default()
	if err != nil {
		return failJSON(asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := ipc.Call(ctx, p.Endpoint, method, params, res); err != nil {
		var ie *ipc.Error
		switch {
		case errors.Is(err, ipc.ErrNotRunning):
			return failJSON(asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
				fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint))
		case errors.As(err, &ie):
			return failJSON(asJSON, stdout, stderr, exitError, ie.Code, ie.Message)
		case errors.Is(err, context.DeadlineExceeded):
			return failJSON(asJSON, stdout, stderr, exitError, "timeout", "the daemon did not answer in time")
		default:
			return failJSON(asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
		}
	}
	return exitOK
}
