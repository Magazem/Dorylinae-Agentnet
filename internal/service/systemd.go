package service

import (
	"path"
	"strings"
)

// SystemdUnit is the user unit's file name.
const SystemdUnit = "agentnetd.service"

// Systemd installs a systemd user unit (~/.config/systemd/user).
type Systemd struct{}

// Name implements Platform.
func (Systemd) Name() string { return "systemd" }

func (Systemd) unitPath(env Env) string {
	cfg := env.ConfigHome
	if cfg == "" {
		cfg = path.Join(env.HomeDir, ".config")
	}
	return path.Join(cfg, "systemd", "user", SystemdUnit)
}

// Install implements Platform.
func (s Systemd) Install(spec Spec, env Env) (Plan, error) {
	if err := checkUnix(spec, env); err != nil {
		return Plan{}, err
	}
	return Plan{Platform: s.Name(), Steps: []Step{
		{Op: OpWrite, Path: s.unitPath(env), Content: SystemdUnitFile(spec), Mode: 0o644},
		{Op: OpRun, Args: []string{"systemctl", "--user", "daemon-reload"}},
		{Op: OpRun, Args: []string{"systemctl", "--user", "enable", "--now", SystemdUnit}},
	}}, nil
}

// Uninstall implements Platform.
func (s Systemd) Uninstall(_ Spec, env Env) (Plan, error) {
	if err := checkUnix(Spec{Executable: "/x", Home: "/x"}, env); err != nil {
		return Plan{}, err
	}
	return Plan{Platform: s.Name(), Steps: []Step{
		// disable fails when the unit is unknown; that is the "already gone" case.
		{Op: OpRun, Args: []string{"systemctl", "--user", "disable", "--now", SystemdUnit}, Ignore: true},
		{Op: OpRemove, Path: s.unitPath(env)},
		{Op: OpRun, Args: []string{"systemctl", "--user", "daemon-reload"}, Ignore: true},
	}}, nil
}

// SystemdUnitFile renders the unit for spec.
func SystemdUnitFile(spec Spec) string {
	return `[Unit]
Description=AgentNet daemon (agentnetd)

[Service]
Type=simple
ExecStart=` + systemdQuote(spec.Executable) + ` run --home ` + systemdQuote(spec.Home) + `
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
}

// systemdQuote quotes one ExecStart word. Inside double quotes systemd honours
// C-style escapes, and it expands %specifiers and $VARIABLES everywhere.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}
