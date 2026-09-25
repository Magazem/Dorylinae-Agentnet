// Package version holds build metadata and the shared --help/--version
// handling used by every Dorylinae binary.
package version

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"time"
)

// Version is the release version. It is overridden at build time with
// -ldflags "-X dorylinae/internal/version.Version=...".
//
// When no such override is given (a plain "go build", or "make build" with
// the default dev VERSION), init derives an identifiable dev version from the
// VCS stamp Go embeds automatically when building inside a checkout, e.g.
// "0.0.0-dev+a1b2c3d (2026-09-25, dirty)".
var Version = "0.0.0-dev"

func init() {
	if Version != "0.0.0-dev" {
		return // overridden by -ldflags with a real release version
	}
	if v, ok := devVersion(debug.ReadBuildInfo()); ok {
		Version = v
	}
}

// devVersion derives a dev build's version from its embedded VCS settings.
// It reports false when info has no revision to report (no build info, or
// building from outside a VCS checkout, or with -buildvcs=false).
func devVersion(info *debug.BuildInfo, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	var revision, vcsTime string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		return "", false
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	v := "0.0.0-dev+" + revision
	date := ""
	if t, err := time.Parse(time.RFC3339, vcsTime); err == nil {
		date = t.Format("2006-01-02")
	}
	switch {
	case date != "" && dirty:
		v += fmt.Sprintf(" (%s, dirty)", date)
	case date != "":
		v += fmt.Sprintf(" (%s)", date)
	case dirty:
		v += " (dirty)"
	}
	return v, true
}

// String returns "<name> <version>".
func String(name string) string {
	return name + " " + Version
}

// versionBody is the machine-readable output of the "version" subcommand.
type versionBody struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Command handles a "<name> version [--json]" subcommand, shared by every
// Dorylinae binary. Output matches --version, plus a --json form.
func Command(name string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "Print the version and exit.\n\nUsage:\n  %s version [--json]\n\nFlags:\n", name)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		fs.SetOutput(stderr)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "%s version: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(versionBody{OK: true, Name: name, Version: Version})
		return 0
	}
	_, _ = fmt.Fprintln(stdout, String(name))
	return 0
}

// Main parses args for a binary that has no behaviour beyond --help and
// --version. It returns the process exit code.
func Main(name, summary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %s [--help] [--version]\n\nFlags:\n", summary, name)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		fs.SetOutput(stderr)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, String(name))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, String(name))
	_, _ = fmt.Fprintf(stdout, "Run '%s --help' for usage.\n", name)
	return 0
}
