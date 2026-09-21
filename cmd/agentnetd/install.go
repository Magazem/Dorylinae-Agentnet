package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"dorylinae/internal/audit"
	"dorylinae/internal/paths"
	"dorylinae/internal/service"
	"dorylinae/internal/store"
)

// serviceDeps are the machine-facing pieces of install/uninstall, replaceable in tests.
type serviceDeps struct {
	platform service.Platform
	runner   service.Runner
	env      service.Env
	exe      string
}

var newServiceDeps = defaultServiceDeps

func defaultServiceDeps() (serviceDeps, error) {
	pl, err := service.Current()
	if err != nil {
		return serviceDeps{}, err
	}
	env, err := service.DefaultEnv()
	if err != nil {
		return serviceDeps{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return serviceDeps{}, fmt.Errorf("locate agentnetd binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return serviceDeps{}, fmt.Errorf("resolve agentnetd binary: %w", err)
	}
	return serviceDeps{platform: pl, runner: service.ExecRunner{}, env: env, exe: exe}, nil
}

type serviceAuditDetail struct {
	Platform   string `json:"platform"`
	Executable string `json:"executable,omitempty"`
	Home       string `json:"home"`
	Changed    bool   `json:"changed"`
}

// runService implements `agentnetd install` and `agentnetd uninstall`.
func runService(ctx context.Context, verb string, args []string, stdout, stderr io.Writer) int {
	install := verb == "install"
	name := "agentnetd " + verb
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "config directory (default: $"+paths.HomeEnv+" or the user config dir)")
	dryRun := fs.Bool("dry-run", false, "print what would be written and run, then exit without changing anything")
	fs.Usage = func() {
		what := "Register agentnetd as a per-user service that starts at login (and start it now).\n" +
			"Re-running replaces the existing definition."
		if !install {
			what = "Stop agentnetd and remove its per-user service. Safe to run when nothing is installed."
		}
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  agentnetd %s [--home DIR] [--dry-run]\n\nFlags:\n", what, verb)
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
		_, _ = fmt.Fprintf(stderr, "%s: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}

	p, err := resolvePaths(*home)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	deps, err := newServiceDeps()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	spec := service.Spec{Executable: deps.exe, Home: p.Dir}

	build := deps.platform.Uninstall
	if install {
		build = deps.platform.Install
	}
	plan, err := build(spec, deps.env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}

	if *dryRun {
		_, _ = fmt.Fprintf(stdout, "dry run (%s): nothing was changed. Would do:\n", plan.Platform)
		plan.Describe(stdout)
		return 0
	}

	if err := p.Ensure(); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	res, err := plan.Apply(ctx, deps.runner)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}

	action, detail := audit.ActionServiceUninstall, serviceAuditDetail{Platform: plan.Platform, Home: p.Dir, Changed: res.Changed}
	if install {
		action, detail.Executable = audit.ActionServiceInstall, spec.Executable
	}
	if err := recordAudit(ctx, p, action, detail); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: done, but the audit event could not be recorded: %v\n", name, err)
		return 1
	}

	switch {
	case install:
		_, _ = fmt.Fprintf(stdout, "agentnetd installed (%s) and started; it will start at every login.\n", plan.Platform)
	case res.Changed:
		_, _ = fmt.Fprintf(stdout, "agentnetd service removed (%s).\n", plan.Platform)
	default:
		_, _ = fmt.Fprintf(stdout, "agentnetd service was not installed (%s); nothing to do.\n", plan.Platform)
	}
	return 0
}

func recordAudit(ctx context.Context, p paths.Paths, action string, detail any) (err error) {
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := st.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()
	return audit.New(st.DB()).Append(ctx, audit.ActorCLI, action, detail)
}

func resolvePaths(home string) (paths.Paths, error) {
	if home != "" {
		return paths.In(home)
	}
	return paths.Default()
}
