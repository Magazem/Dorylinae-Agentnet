package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// identityBody is the machine-readable success output of `identity --json`.
type identityBody struct {
	OK bool `json:"ok"`
	daemon.IdentityResult
}

func runIdentity(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet identity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Print this agent's signed Agent Card (name, public key, harness, skills).
The private key never leaves the daemon.

Usage:
  agentnet identity [--json]

Flags:
  --json    print machine-readable JSON on stdout:
            {"ok":true,"card":{"version","name","public_key","harness","skills","created"},
             "signature":"<base64url>","key_backend":"keychain|file",
             "fingerprint":"<20 characters, no spaces>"}

Verify the output independently with: go run ./tools/verifycard

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

	p, err := paths.Default()
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()

	var res daemon.IdentityResult
	if err := ipc.Call(ctx, p.Endpoint, "identity", nil, &res); err != nil {
		if errors.Is(err, ipc.ErrNotRunning) {
			return failJSON(*asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
				fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint))
		}
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(identityBody{OK: true, IdentityResult: res})
		return exitOK
	}
	printCard(stdout, res)
	return exitOK
}

func printCard(w io.Writer, res daemon.IdentityResult) {
	c := res.Card
	_, _ = fmt.Fprintf(w, "name:        %s\nharness:     %s\npublic key:  %s\nfingerprint: %s\ncreated:     %s\nskills:      %s\nsignature:   %s\nkey storage: %s\n",
		c.Name, c.Harness, c.PublicKey, envelope.FormatFingerprint(res.Fingerprint), c.Created, skillList(c.Skills), res.Signature, res.KeyBackend)
}

func skillList(skills []agentcard.Skill) string {
	if len(skills) == 0 {
		return "(none)"
	}
	ids := make([]string, len(skills))
	for i, s := range skills {
		ids[i] = s.ID
	}
	return strings.Join(ids, ", ")
}
