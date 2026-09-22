package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// notifyTestTimeout bounds `agentnet notify --test`: it waits for a real
// desktop mechanism (Docs/protocol/notify.md §Desktop, a 3 s per-call cap),
// longer than the plain get/set round trip.
const notifyTestTimeout = 4 * time.Second

// notifyBody is the machine-readable output of `notify [--json]`.
type notifyBody struct {
	OK bool `json:"ok"`
	daemon.NotifyGetResult
}

// notifyTestBody is the machine-readable output of `notify --test [--json]`.
type notifyTestBody struct {
	OK bool `json:"ok"`
	daemon.NotifyTestResult
}

var knownNotifyEvents = []string{"request.received", "request.accepted", "request.declined", "request.deferred", "request.completed", "request.cancelled"}

const notifyUsage = `Configures desktop notifications.

Usage:
  agentnet notify [--json]                        show the settings
  agentnet notify --desktop on|off [--json]
  agentnet notify --event <event>=on|off [--json]  repeatable
  agentnet notify --test [--json]

The events are request.received, request.accepted, request.declined,
request.cancelled (on by default), request.deferred and request.completed
(off by default).

Flags:
  --desktop on|off        turn desktop notifications on or off
  --event EVENT=on|off    turn one event on or off; may be repeated
  --test                  show a test notification
  --json                  print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

type eventFlags map[string]bool

func (e eventFlags) String() string { return "" }

func (e eventFlags) Set(s string) error {
	name, val, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("--event must be EVENT=on|off, got %q", s)
	}
	var on bool
	switch val {
	case "on":
		on = true
	case "off":
		on = false
	default:
		return fmt.Errorf("--event %s: value must be on or off, got %q", name, val)
	}
	valid := false
	for _, ev := range knownNotifyEvents {
		if ev == name {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("--event: unknown event %q", name)
	}
	e[name] = on
	return nil
}

func runNotify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	desktop := fs.String("desktop", "", `turn desktop notifications "on" or "off"`)
	test := fs.Bool("test", false, "show a test notification")
	events := make(eventFlags)
	fs.Var(events, "event", "EVENT=on|off, repeatable")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, notifyUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *desktop != "" && *desktop != "on" && *desktop != "off" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", `--desktop must be "on" or "off"`)
	}
	if *test && (*desktop != "" || len(events) > 0) {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--test cannot be combined with --desktop or --event")
	}

	if *test {
		var res daemon.NotifyTestResult
		if code := callDaemon(*asJSON, stdout, stderr, notifyTestTimeout, "notify_test", nil, &res); code != exitOK {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(notifyTestBody{OK: true, NotifyTestResult: res})
			return exitOK
		}
		_, _ = fmt.Fprintf(stdout, "Desktop: %s\n", res.Desktop)
		_, _ = fmt.Fprintf(stdout, "Webhook: %s\n", res.Webhook)
		return exitOK
	}

	var res daemon.NotifyGetResult
	if *desktop == "" && len(events) == 0 {
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "notify_get", nil, &res); code != exitOK {
			return code
		}
	} else {
		p := daemon.NotifySetParams{Events: map[string]bool(events)}
		if *desktop != "" {
			on := *desktop == "on"
			p.Desktop = &on
		}
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "notify_set", p, &res); code != exitOK {
			return code
		}
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(notifyBody{OK: true, NotifyGetResult: res})
		return exitOK
	}
	desktopState := "off"
	if res.Desktop {
		desktopState = "on"
	}
	_, _ = fmt.Fprintf(stdout, "Desktop:  %s\n", desktopState)
	var on []string
	for ev, enabled := range res.Events {
		if enabled {
			on = append(on, ev)
		}
	}
	sort.Strings(on)
	_, _ = fmt.Fprintf(stdout, "Events:   %s\n", strings.Join(on, ", "))
	_, _ = fmt.Fprintln(stdout, "Webhook:  none")
	return exitOK
}
