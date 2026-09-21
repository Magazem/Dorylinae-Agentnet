package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// debugEnabled reports whether debug-only commands are on (DORYLINAE_DEBUG=1).
func debugEnabled() bool { return os.Getenv(mail.DebugEnv) == "1" }

// mailBody is the machine-readable success output of `mail send --json`.
type mailBody struct {
	OK bool `json:"ok"`
	daemon.MailSubmitResult
}

// runMail implements the debug-only `agentnet mail` command. Without
// DORYLINAE_DEBUG=1 the command does not exist.
func runMail(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "send" {
		return runMailSend(args[1:], stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		mailUsage(stdout)
		return exitOK
	}
	mailUsage(stderr)
	return exitUsage
}

func mailUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Debug-only mail commands (DORYLINAE_DEBUG=1).

Usage:
  agentnet mail send @peer --kind K --text T [--json]
`)
}

func runMailSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet mail send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	kind := fs.String("kind", "", "mail kind, e.g. note")
	text := fs.String("text", "", "text of the mail; sent as the body {\"text\": T}")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Queue a mail to a paired agent (debug only, DORYLINAE_DEBUG=1).

Usage:
  agentnet mail send @peer --kind K --text T [--json]

The mail is signed, sealed to the peer's mailbox key and stored in the outbox
as "queued"; the daemon sends and resends it until the peer acks. The command
returns at once with the mail id.

Flags:
  --kind K   mail kind. The receiving daemon must understand it: "note" is
             built in when it runs with DORYLINAE_DEBUG=1; other kinds are
             acked as unsupported.
  --text T   text of the mail; the body is {"text": T}
  --json     print {"ok":true,"id","state":"queued"}
             (or {"ok":false,"error":{"code","message"}}; codes unknown_peer,
             ambiguous_peer, unpaired, no_mailbox_key, bad_request)

Exit codes: 0 queued, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	switch {
	case len(pos) == 0:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give @peer (see 'agentnet mail send --help')")
	case len(pos) > 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[1]))
	case *kind == "":
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--kind is required")
	}
	body, err := json.Marshal(map[string]string{"text": *text})
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "usage", err.Error())
	}
	var res daemon.MailSubmitResult
	params := daemon.MailSubmitParams{To: pos[0], Kind: *kind, Body: body}
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "mail_submit", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(mailBody{OK: true, MailSubmitResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "queued mail %s to %s (%s)\n", res.ID, pos[0], res.State)
	return exitOK
}
