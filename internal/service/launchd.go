package service

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// LaunchdLabel is the launchd job label.
const LaunchdLabel = "dev.dorylinae.agentnetd"

// Launchd installs a per-user launchd agent (~/Library/LaunchAgents).
type Launchd struct{}

// Name implements Platform.
func (Launchd) Name() string { return "launchd" }

func (Launchd) plistPath(env Env) string {
	return path.Join(env.HomeDir, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

func launchdTarget(env Env) string {
	return "gui/" + strconv.Itoa(env.UID) + "/" + LaunchdLabel
}

// Install implements Platform.
func (l Launchd) Install(spec Spec, env Env) (Plan, error) {
	if err := checkUnix(spec, env); err != nil {
		return Plan{}, err
	}
	if env.UID < 0 {
		return Plan{}, fmt.Errorf("launchd: invalid uid %d", env.UID)
	}
	plist := l.plistPath(env)
	return Plan{Platform: l.Name(), Steps: []Step{
		{Op: OpWrite, Path: plist, Content: LaunchdPlist(spec), Mode: 0o644},
		// bootout first so re-installing replaces a loaded job; harmless if not loaded.
		{Op: OpRun, Args: []string{"launchctl", "bootout", launchdTarget(env)}, Ignore: true},
		// bootstrap loads the job; RunAtLoad starts it immediately.
		{Op: OpRun, Args: []string{"launchctl", "bootstrap", "gui/" + strconv.Itoa(env.UID), plist}},
	}}, nil
}

// Uninstall implements Platform.
func (l Launchd) Uninstall(_ Spec, env Env) (Plan, error) {
	if err := checkUnix(Spec{Executable: "/x", Home: "/x"}, env); err != nil {
		return Plan{}, err
	}
	return Plan{Platform: l.Name(), Steps: []Step{
		{Op: OpRun, Args: []string{"launchctl", "bootout", launchdTarget(env)}, Ignore: true},
		{Op: OpRemove, Path: l.plistPath(env)},
	}}, nil
}

// LaunchdPlist renders the launchd property list for spec.
func LaunchdPlist(spec Spec) string {
	logPath := path.Join(spec.Home, "agentnetd.log")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + LaunchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(spec.Executable) + `</string>
		<string>run</string>
		<string>--home</string>
		<string>` + xmlEscape(spec.Home) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
</dict>
</plist>
`)
	return b.String()
}
