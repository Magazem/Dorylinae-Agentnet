package service

import (
	"errors"
	"fmt"
	"strings"
)

// TaskName is the Task Scheduler task name.
const TaskName = "Dorylinae agentnetd"

// Schtasks installs a per-user Task Scheduler task that runs at logon.
//
// A Windows service needs administrator rights to register; a task whose
// principal is the current user, run with the least-privilege interactive
// token, does not. See docs/cli/agentnetd-install.md.
type Schtasks struct{}

// Name implements Platform.
func (Schtasks) Name() string { return "schtasks" }

// Install implements Platform.
func (s Schtasks) Install(spec Spec, env Env) (Plan, error) {
	if !isWindowsAbs(spec.Executable) || !isWindowsAbs(spec.Home) {
		return Plan{}, errors.New("schtasks: executable and home must be absolute Windows paths")
	}
	if env.User == "" {
		return Plan{}, errors.New("schtasks: current user name is unknown")
	}
	xmlPath := spec.Home + `\agentnetd-task.xml`
	return Plan{Platform: s.Name(), Steps: []Step{
		{Op: OpWrite, Path: xmlPath, Content: TaskXML(spec, env), Mode: 0o600, UTF16: true},
		// /F replaces an existing task, which makes install idempotent.
		{Op: OpRun, Args: []string{"schtasks.exe", "/Create", "/TN", TaskName, "/XML", xmlPath, "/F"}},
		{Op: OpRemove, Path: xmlPath, Cleanup: true},
		// Start it now rather than waiting for the next logon.
		{Op: OpRun, Args: []string{"schtasks.exe", "/Run", "/TN", TaskName}},
	}}, nil
}

// Uninstall implements Platform.
func (s Schtasks) Uninstall(Spec, Env) (Plan, error) {
	return Plan{Platform: s.Name(), Steps: []Step{
		// schtasks messages are localised, so existence is probed by exit status.
		{Op: OpRun, Args: []string{"schtasks.exe", "/Query", "/TN", TaskName}, Probe: true},
		// End terminates the running daemon (no daemon.stop audit row; see docs).
		{Op: OpRun, Args: []string{"schtasks.exe", "/End", "/TN", TaskName}, Ignore: true},
		{Op: OpRun, Args: []string{"schtasks.exe", "/Delete", "/TN", TaskName, "/F"}},
	}}, nil
}

// TaskXML renders the Task Scheduler definition for spec and env.
func TaskXML(spec Spec, env Env) string {
	args := "run --home " + windowsQuote(spec.Home)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>AgentNet daemon (agentnetd), started at logon.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>
      <Arguments>%[3]s</Arguments>
      <WorkingDirectory>%[4]s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(env.User), xmlEscape(spec.Executable), xmlEscape(args), xmlEscape(spec.Home))
}

// windowsQuote quotes one argument for CommandLineToArgvW, which Go's runtime
// uses to split the task's Arguments string.
func windowsQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, c := range s {
		switch c {
		case '\\':
			backslashes++
			b.WriteRune(c)
			continue
		case '"':
			// Double the preceding backslashes, then escape the quote.
			b.WriteString(strings.Repeat(`\`, backslashes+1))
		}
		backslashes = 0
		b.WriteRune(c)
	}
	// A trailing run of backslashes must be doubled so it does not escape the closing quote.
	b.WriteString(strings.Repeat(`\`, backslashes))
	b.WriteByte('"')
	return b.String()
}

func isWindowsAbs(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return len(p) > 2
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z')
}
