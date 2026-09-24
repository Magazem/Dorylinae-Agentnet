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
)

const deviceUsage = `Link two of your own devices: a controller and a helper (Docs/protocol/device.md).

Usage:
  agentnet device link <peer> --as controller|helper --fingerprint FP [--json]
  agentnet device list [--json]
  agentnet device unlink <peer> [--json]
  agentnet device scope <controller> (--types ... --repo ... --command ...
                        --expires D | --from-file F | --clear | --show) [--json]

Subcommands:
  link     start a link with a paired device of yours. Run it on BOTH devices:
           on the helper with --as helper, on the controller with --as
           controller, each with the OTHER device's fingerprint. Each device
           asks its human to confirm in the AgentNet approval window.
  list     show links and link attempts on this device.
  unlink   end the link with <peer>, on either device. The other device is told
           even if this one holds no active link.
  scope    on the helper: set (with your approval), clear or show what the
           controller may run here.

A link does nothing by itself: no request runs on a helper until its owner sets
a scope on the helper.

Run 'agentnet device <subcommand> --help' for flags.
`

func runDevice(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, deviceUsage)
		return exitOK
	}
	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, deviceUsage)
		return exitOK
	case "link":
		return runDeviceLink(args[1:], stdout, stderr)
	case "list":
		return runDeviceList(args[1:], stdout, stderr)
	case "unlink":
		return runDeviceUnlink(args[1:], stdout, stderr)
	case "scope":
		return runDeviceScope(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "agentnet device: unknown subcommand %q\n\n", args[0])
	_, _ = fmt.Fprint(stderr, deviceUsage)
	return exitUsage
}

func runDeviceLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet device link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	as := fs.String("as", "", "this device's role: controller or helper")
	fp := fs.String("fingerprint", "", "the OTHER device's fingerprint")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Start linking this device with another device of yours.

Usage:
  agentnet device link <peer> --as controller|helper --fingerprint FP [--json]

<peer> is the other device (a public key, a unique name or @name); it must
already be paired. Run the command on BOTH devices, each with the other
device's fingerprint ('agentnet identity' on the other machine). Then confirm
the code in the AgentNet approval window on each device. The link is active
once both have confirmed, in either order, within 10 minutes.

  --as ROLE        controller (this device may ask the other to run things) or
                   helper (this device runs things for the other, after you set
                   a scope on it). A device is either a controller or a helper,
                   never both, and a helper has one controller.
  --fingerprint FP the other device's 20-character fingerprint, with or without
                   spaces. A mismatch exits 1 with "fingerprint_mismatch" and
                   changes nothing. A match verifies the peer, like
                   'agentnet peers verify'.
  --json           print {"ok":true,"approval":{...},"link":{...}}
                   (or {"ok":false,"error":{...}})

Errors: fingerprint_mismatch, bad_fingerprint, device_cycle (the reverse link
or a chain), unverified_peer, unknown_peer, approval_limit, approval_locked,
approval_unavailable.

Exit codes: 0 approval created, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 || *as == "" || *fp == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <peer>, --as controller|helper and --fingerprint (see 'agentnet device link --help')")
	}
	if *as != "controller" && *as != "helper" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--as must be controller or helper")
	}
	params := daemon.DeviceLinkParams{Peer: pos[0], As: *as, Fingerprint: *fp}
	var res daemon.DeviceLinkResult
	if code := callDaemon(*asJSON, stdout, stderr, approveTimeout, "device_link", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DeviceLinkResult
		}{OK: true, DeviceLinkResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Approval %s pending to link as %s with %s. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open %s').\nThe link is active once the other device has confirmed too.\n",
		res.Approval.ID, res.Link.Role, devicePeerLabel(res.Link.Peer), res.Approval.ID)
	return exitOK
}

func devicePeerLabel(p daemon.GrantPeerRef) string {
	if p.Name != "" {
		return p.Name
	}
	return p.PublicKey
}

func runDeviceList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet device list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `List device links and link attempts on this device.

Usage:
  agentnet device list [--json]

Flags:
  --json    print {"ok":true,"links":[{"id","peer":{"name","public_key"},
            "role":"controller|helper","state":"pending_approval|waiting|active|revoked",
            "activated_at"?}]}. "links" is an empty array when there are none.
            "role" is this device's role; "waiting" means this device confirmed
            and awaits the other.

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
	var res daemon.DeviceListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "device_list", nil, &res); code != exitOK {
		return code
	}
	if res.Links == nil {
		res.Links = []daemon.DeviceLinkView{}
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DeviceListResult
		}{OK: true, DeviceListResult: res})
		return exitOK
	}
	if len(res.Links) == 0 {
		_, _ = fmt.Fprintln(stdout, "No device links. Run 'agentnet device link' on both devices to start one.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPEER\tROLE\tSTATE\tACTIVATED")
	for _, l := range res.Links {
		at := "-"
		if l.ActivatedAt != "" {
			at = l.ActivatedAt
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.ID, devicePeerLabel(l.Peer), l.Role, strings.ReplaceAll(l.State, "_", " "), at)
	}
	_ = tw.Flush()
	return exitOK
}

func runDeviceUnlink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet device unlink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `End the link with another device, on either device.

Usage:
  agentnet device unlink <peer> [--json]

<peer> is a public key, a unique name or @name. Every link, link attempt and
kept offer with that peer ends, its scope is deleted, and the peer is told. The
peer is told even if this device holds no active link with it (one side can be
active while the other's attempt lapsed). No approval is needed: removing
authority is always allowed. A run that is already executing on a helper
finishes; queued runs are dropped.

Flags:
  --json    print {"ok":true,"link":{...},"mail_id":"..."}; "link" is absent
            when this device held no link or attempt with the peer.

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
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
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <peer> (see 'agentnet device unlink --help')")
	}
	var res daemon.DeviceUnlinkResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "device_unlink", daemon.DeviceUnlinkParams{Peer: pos[0]}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DeviceUnlinkResult
		}{OK: true, DeviceUnlinkResult: res})
		return exitOK
	}
	if res.Link != nil {
		_, _ = fmt.Fprintf(stdout, "Unlinked %s (%s); the other device was told.\n", devicePeerLabel(res.Link.Peer), res.Link.ID)
	} else {
		_, _ = fmt.Fprintln(stdout, "No link with that device here; it was told to end any link on its side.")
	}
	return exitOK
}
