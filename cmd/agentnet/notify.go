package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
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
	daemon.NotifySetResult
}

// notifyTestBody is the machine-readable output of `notify --test [--json]`.
type notifyTestBody struct {
	OK bool `json:"ok"`
	daemon.NotifyTestResult
}

var knownNotifyEvents = []string{"request.received", "request.accepted", "request.declined", "request.deferred", "request.completed", "request.cancelled", "session.quarantined", "device.linked"}

const notifyUsage = `Configures desktop notifications and the outgoing webhook.

Usage:
  agentnet notify [--json]                                  show the settings
  agentnet notify --desktop on|off [--json]
  agentnet notify --event <event>=on|off [--json]           repeatable
  agentnet notify --webhook URL [--format generic|slack|discord] [--webhook-title on|off] [--json]
  agentnet notify --webhook off [--json]
  agentnet notify --rotate-secret [--json]
  agentnet notify --test [--json]

The events are request.received, request.accepted, request.declined,
request.cancelled (on by default), request.deferred and request.completed
(off by default).

The webhook URL must be https:// (http:// only to localhost). When a webhook
is first set, or on --rotate-secret, the signing secret is printed once
(whsec_...). The body never contains the brief; it contains the title only
with --webhook-title on.

Flags:
  --desktop on|off             turn desktop notifications on or off
  --event EVENT=on|off         turn one event on or off; may be repeated
  --webhook URL|off            set or remove the webhook
  --format generic|slack|discord  webhook payload format (default generic)
  --webhook-title on|off       include the request title in the webhook (default off)
  --rotate-secret               generate and print a new webhook signing secret
  --test                       show a test notification and queue a test webhook
  --json                        print machine-readable JSON on stdout

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
	webhook := fs.String("webhook", "", `webhook URL, or "off" to remove it`)
	format := fs.String("format", "", "webhook payload format: generic, slack or discord")
	webhookTitle := fs.String("webhook-title", "", `include the request title in the webhook, "on" or "off"`)
	rotateSecret := fs.Bool("rotate-secret", false, "generate and print a new webhook signing secret")
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
	if *webhookTitle != "" && *webhookTitle != "on" && *webhookTitle != "off" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", `--webhook-title must be "on" or "off"`)
	}
	if *format != "" && *format != "generic" && *format != "slack" && *format != "discord" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", `--format must be "generic", "slack" or "discord"`)
	}
	webhookSet := *webhook != "" || *format != "" || *webhookTitle != "" || *rotateSecret
	if *test && (*desktop != "" || len(events) > 0 || webhookSet) {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--test cannot be combined with other flags")
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

	var res daemon.NotifySetResult
	if *desktop == "" && len(events) == 0 && !webhookSet {
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "notify_get", nil, &res.NotifyGetResult); code != exitOK {
			return code
		}
	} else {
		p := daemon.NotifySetParams{Events: map[string]bool(events), RotateSecret: *rotateSecret}
		if *desktop != "" {
			on := *desktop == "on"
			p.Desktop = &on
		}
		if *webhook != "" {
			hook := *webhook
			if hook == "off" {
				hook = ""
			}
			p.WebhookURL = &hook
		}
		if *format != "" {
			p.Format = format
		}
		if *webhookTitle != "" {
			on := *webhookTitle == "on"
			p.Title = &on
		}
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "notify_set", p, &res); code != exitOK {
			return code
		}
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(notifyBody{OK: true, NotifySetResult: res})
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
	if res.Webhook == nil {
		_, _ = fmt.Fprintln(stdout, "Webhook:  none")
	} else {
		title := "off"
		if res.Webhook.Title {
			title = "on"
		}
		_, _ = fmt.Fprintf(stdout, "Webhook:  %s (%s, title %s), %d pending, %d failed in 7 days\n",
			elideWebhookURL(res.Webhook.URL), res.Webhook.Format, title, res.Webhook.Pending, res.Webhook.Failed7d)
	}
	if res.Secret != "" {
		_, _ = fmt.Fprintf(stdout, "Secret:   %s\n", res.Secret)
	}
	return exitOK
}

// elideWebhookURL shows only the scheme and host of the webhook URL in the
// human output, "https://hooks.example.com/…" (Docs/cli/notify.md): the path
// of a Slack or Discord webhook URL is a bearer secret (review 12, L12).
// --json still carries the full URL.
func elideWebhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		out += "/…"
	}
	return out
}
