package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
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
	legacy := fs.Bool("v1", false, "redeem a legacy 10-character (v1) code")
	statusID := fs.String("status", "", "show the state of the pairing with this ID")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Pair with an agent on another machine through the relay.

Usage:
  agentnet pair --new [--json]         issue a one-time code (valid 10 minutes)
  agentnet pair <code> [--json]        redeem a code shown by the other machine
  agentnet pair --v1 <code> [--json]   redeem a legacy 10-character code
  agentnet pair --status <id> [--json] check a pairing that was still pending

Flags:
  --new        issue a one-time pairing code (15 characters, LLLLL-SSSSS-SSSSS)
               and return; enter the code on the other machine with
               'agentnet pair <code>'
  --v1         allow redeeming a legacy 10-character code. The peer is stored
               with trust "relay", which a hostile relay could have forged
  --status ID  show the state of a pairing by its ID
  --json       print machine-readable JSON on stdout:
               {"ok":true,"pairing_id","role":"issuer|redeemer",
                "state":"pending|complete|failed",
                "code","expires"           (issuer, while pending),
                "peer":{"public_key","name","harness","skills","paired_at",
                        "trust","fingerprint"} (complete),
                "error":{"code","message"} (failed)}
               Failures that stop the request itself print
               {"ok":false,"error":{"code","message"}}.

The command returns in under 2 seconds. If the exchange is not finished by
then, state is "pending" and the pairing ID can be polled with --status. Both
daemons verify the other's signed Agent Card and mailbox key and prove they know
the code; if anything was altered on the way the pairing fails and nothing is
stored. Codes are single use. List the result with 'agentnet peers'.

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
	case *newCode && (*statusID != "" || len(pos) > 0 || *legacy):
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--new takes no code and cannot be combined with --status or --v1")
	case *newCode:
		method = "pair_new"
	case *statusID != "":
		if len(pos) > 0 {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--status takes no code")
		}
		if *legacy {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--v1 applies only to redeeming a code")
		}
		method, params = "pair_status", daemon.PairStatusParams{PairingID: *statusID}
	case len(pos) == 1:
		method, params = "pair_redeem", daemon.PairRedeemParams{Code: pos[0], V1: *legacy}
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
		_, _ = fmt.Fprintf(w, "Paired with %s (%s)\n  public key:  %s\n  fingerprint: %s\n  trust:       %s\n",
			st.Peer.Name, st.Peer.Harness, st.Peer.PublicKey, envelope.FormatFingerprint(st.Peer.Fingerprint), st.Peer.Trust)
	case st.State == peers.StatePending && st.Role == peers.RoleIssuer && st.Code != "":
		_, _ = fmt.Fprintf(w, "Pairing code: %s\nExpires:      %s\nPairing ID:   %s\n\nOn the other machine run: agentnet pair %s\nThen check here with:     agentnet peers\n",
			formatCode(st.Code), st.Expires, st.ID, formatCode(st.Code))
	case st.State == peers.StatePending:
		_, _ = fmt.Fprintf(w, "Pairing %s is still in progress.\nCheck it with: agentnet pair --status %s\n", st.ID, st.ID)
	}
}

// formatCode shows a code as LLLLL-SSSSS-SSSSS (v2), or XXXXX-XXXXX (v1). The
// daemon already formats v2 codes, so a code that has dashes is left alone.
func formatCode(c string) string {
	if len(c) == 10 {
		return c[:5] + "-" + c[5:]
	}
	return c
}

// parseInterspersed parses flags that may appear before, after or mixed in
// with positional arguments, and returns the positional arguments in order.
//
// A token is treated as a flag when it names one of fs's defined flags (or
// -h/-help/--help). A token that starts with '-' but is not a defined flag
// is still positional if it decodes as a base64url Ed25519 public key: peer
// public keys are base64url, so about 1 in 32 of them start with '-' and
// would otherwise be mistaken for a flag (see Docs/cli/peers.md). Any other
// unrecognized '-'-prefixed token is left for fs.Parse to reject, the same
// as an actual flag typo. "--" still works too: everything after it is
// positional, whatever it looks like.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	names := map[string]bool{"-h": true, "--h": true, "-help": true, "--help": true}
	boolFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		names["-"+f.Name] = true
		names["--"+f.Name] = true
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			boolFlags["-"+f.Name] = true
			boolFlags["--"+f.Name] = true
		}
	})

	var pos, flagArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		name, hasValue := a, false
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name, hasValue = a[:eq], true
		}
		switch {
		case a == "-":
			pos = append(pos, a)
		case strings.HasPrefix(a, "-") && names[name]:
			flagArgs = append(flagArgs, a)
			if !hasValue && !boolFlags[name] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		case strings.HasPrefix(a, "-") && looksLikePeerKey(a):
			pos = append(pos, a)
		case strings.HasPrefix(a, "-"):
			// Not a defined flag and not a key-shaped value: let fs.Parse
			// reject it, the same as it would reject an actual typo.
			flagArgs = append(flagArgs, a)
		default:
			pos = append(pos, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return pos, nil
}

// looksLikePeerKey reports whether s decodes as a base64url Ed25519 public
// key, so a leading '-' that came from key bytes (not a flag) can still be
// accepted as a positional argument without "--".
func looksLikePeerKey(s string) bool {
	_, err := envelope.ParseKey(s)
	return err == nil
}

// callDaemon makes one IPC call and reports failures as the command's output.
// It returns exitOK when res is filled in.
func callDaemon(asJSON bool, stdout, stderr io.Writer, timeout time.Duration, method string, params, res any) int {
	code, errCode, msg := callDaemonRaw(timeout, method, params, res)
	if code != exitOK {
		return failJSON(asJSON, stdout, stderr, code, errCode, msg)
	}
	return exitOK
}

// callDaemonRaw makes one IPC call without printing anything, so a caller
// that wants to enrich the error message (debate's self-explaining errors,
// DX-2) can do so before it reaches failJSON. code is exitOK with errCode
// and msg both "" on success.
func callDaemonRaw(timeout time.Duration, method string, params, res any) (code int, errCode, msg string) {
	p, err := paths.Default()
	if err != nil {
		return exitError, "daemon_error", err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := ipc.Call(ctx, p.Endpoint, method, params, res); err != nil {
		var ie *ipc.Error
		switch {
		case errors.Is(err, ipc.ErrNotRunning):
			return exitDaemonNotFound, "daemon_not_running", fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint)
		case errors.As(err, &ie):
			return exitError, ie.Code, ie.Message
		case errors.Is(err, context.DeadlineExceeded):
			return exitError, "timeout", "the daemon did not answer in time"
		default:
			return exitError, "daemon_error", err.Error()
		}
	}
	return exitOK, "", ""
}
