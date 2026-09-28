package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

const adminUsage = `Usage:
  relay admin account list [--db PATH]
  relay admin account show ACCOUNT [--db PATH]
  relay admin account suspend|unsuspend|delete ACCOUNT [--db PATH] [--security-journal PATH]
  relay admin account unbind KEY [--db PATH] [--security-journal PATH]
  relay admin group suspend|unsuspend GROUP [--db PATH] [--security-journal PATH]

Operates on the relay database directly; a running relay picks the change up
within a second and closes the connections it affects. "team" is accepted as
another name for "group" (a quota group, Docs/protocol/invites.md).
`

// runAdmin implements `relay admin account|group …` (Docs/protocol/accounts.md
// "Revocation"). Exit 0 on success, 1 on failure, 2 on a usage error.
func runAdmin(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		_, _ = fmt.Fprint(stderr, adminUsage)
		return 2
	}
	noun, verb := args[0], args[1]
	if noun == "team" {
		noun = "group"
	}
	fs := flag.NewFlagSet(name+" admin "+noun+" "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "SQLite file holding the relay database (default: relay-queue.db in the config directory)")
	journalPath := fs.String("security-journal", "", "append-only file of content-free security events (the relay's --security-journal)")
	// Flags may come before or after the one positional argument.
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	var target string
	if fs.NArg() > 0 {
		target = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
		if fs.NArg() > 0 {
			_, _ = fmt.Fprintf(stderr, "%s admin: unexpected argument %q\n", name, fs.Arg(0))
			return 2
		}
	}
	wantTarget := noun != "account" || verb != "list"
	known := map[string]bool{"account list": true, "account show": true, "account suspend": true, "account unsuspend": true,
		"account delete": true, "account unbind": true, "group suspend": true, "group unsuspend": true}
	if !known[noun+" "+verb] || wantTarget != (target != "") {
		_, _ = fmt.Fprint(stderr, adminUsage)
		return 2
	}
	if *dbPath == "" {
		p, err := paths.Default()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s admin: %v\n", name, err)
			return 1
		}
		*dbPath = p.RelayQueueDB
	}
	if _, err := os.Stat(*dbPath); err != nil { //nolint:gosec // dbPath is the operator-supplied --db, as intended
		_, _ = fmt.Fprintf(stderr, "%s admin: relay database %s: %v\n", name, *dbPath, err)
		return 1
	}
	var journal *relay.JournalWriter
	if *journalPath != "" {
		f, err := os.OpenFile(*journalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // journalPath is the operator-supplied --security-journal, as intended
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s admin: --security-journal: %v\n", name, err)
			return 1
		}
		defer func() { _ = f.Close() }()
		journal = relay.NewJournalWriter(f)
	}
	adm, err := relay.OpenAdmin(*dbPath, journal)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s admin: %v\n", name, err)
		return 1
	}
	defer func() { _ = adm.Close() }()

	switch noun + " " + verb {
	case "account list":
		err = adminList(adm, stdout)
	case "account show":
		err = adminShow(adm, target, stdout)
	case "account suspend", "account unsuspend":
		err = adm.SetAccountSuspended(target, verb == "suspend")
	case "account delete":
		err = adm.DeleteAccount(target)
	case "account unbind":
		err = adm.Unbind(target)
	case "group suspend", "group unsuspend":
		err = adm.SetGroupSuspended(target, verb == "suspend")
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s admin %s %s: %v\n", name, noun, verb, err)
		return 1
	}
	if verb != "list" && verb != "show" {
		_, _ = fmt.Fprintf(stdout, "%s %s: done\n", noun, verb)
	}
	return 0
}

func adminList(adm *relay.Admin, w io.Writer) error {
	list, err := adm.Accounts()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPROVIDER\tSTATE\tGROUP\tKEYS\tCREATED\tDISPLAY")
	for _, a := range list {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", a.ID, a.Provider, a.State, dash(a.Group), a.Keys,
			a.Created.UTC().Format(time.DateOnly), a.Display)
	}
	return tw.Flush()
}

func adminShow(adm *relay.Admin, id string, w io.Writer) error {
	a, keys, err := adm.Account(id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "account   %s\nprovider  %s\ndisplay   %s\nstate     %s\ngroup     %s\ncreated   %s\n",
		a.ID, a.Provider, a.Display, a.State, dash(a.Group), a.Created.UTC().Format(time.RFC3339))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tFINGERPRINT\tDEVICE\tOS\tBOUND\tLAST DAY")
	for _, k := range keys {
		fp, _ := envelope.KeyFingerprint(k.Key)
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", k.Key, envelope.FormatFingerprint(fp), k.Device, k.OS,
			k.BoundAt.UTC().Format(time.RFC3339), dash(k.LastDay))
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
