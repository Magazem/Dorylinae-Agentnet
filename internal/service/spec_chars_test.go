package service

import "testing"

// R55-095: a control character (or, for Task Scheduler, '%') in an
// owner-chosen path must be refused, not written into the definition.
func TestPlansRejectControlCharsAndPercent(t *testing.T) {
	for _, bad := range []string{"\n", "\r", "\x00", "\x7f"} {
		u := Spec{Executable: "/opt/ag" + bad + "ent/agentnetd", Home: unixSpec.Home}
		if _, err := (Systemd{}).Install(u, unixEnv); err == nil {
			t.Errorf("systemd accepted %q in the executable path", bad)
		}
		if _, err := (Launchd{}).Install(Spec{Executable: unixSpec.Executable, Home: "/home/a" + bad + "b"}, unixEnv); err == nil {
			t.Errorf("launchd accepted %q in the home path", bad)
		}
		w := Spec{Executable: winSpec.Executable, Home: `C:\Users\a` + bad + `b`}
		if _, err := (Schtasks{}).Install(w, winEnv); err == nil {
			t.Errorf("schtasks accepted %q in the home path", bad)
		}
	}
	pct := Spec{Executable: `C:\Program Files\%APPDATA%\agentnetd.exe`, Home: winSpec.Home}
	if _, err := (Schtasks{}).Install(pct, winEnv); err == nil {
		t.Error("schtasks accepted %VAR% in the executable path")
	}
	// '%' stays fine for systemd, which escapes it.
	if _, err := (Systemd{}).Install(Spec{Executable: "/opt/100%/agentnetd", Home: unixSpec.Home}, unixEnv); err != nil {
		t.Errorf("systemd rejected a quotable '%%': %v", err)
	}
	if _, err := (Schtasks{}).Install(winSpec, winEnv); err != nil {
		t.Errorf("ordinary Windows spec rejected: %v", err)
	}
}
