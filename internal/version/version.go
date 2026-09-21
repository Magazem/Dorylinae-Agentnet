// Package version holds build metadata and the shared --help/--version
// handling used by every Dorylinae binary.
package version

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// Version is the release version. It is overridden at build time with
// -ldflags "-X dorylinae/internal/version.Version=...".
var Version = "0.0.0-dev"

// String returns "<name> <version>".
func String(name string) string {
	return name + " " + Version
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
