package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
)

// DefaultEnv describes the current user.
func DefaultEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("locate home directory: %w", err)
	}
	u, err := user.Current()
	if err != nil {
		return Env{}, fmt.Errorf("look up current user: %w", err)
	}
	return Env{
		HomeDir:    home,
		ConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		UID:        os.Getuid(),
		User:       u.Username,
	}, nil
}

// checkSpecText rejects control characters (a newline would start a new
// line of a unit file or task definition) in the owner-chosen strings that
// end up in a service definition (review 55 R55-095). pct also rejects '%',
// which Task Scheduler expands as an environment variable.
func checkSpecText(spec Spec, pct bool) error {
	for _, f := range []struct{ name, v string }{{"executable", spec.Executable}, {"home", spec.Home}, {"relay", spec.Relay}} {
		for _, r := range f.v {
			if r < 0x20 || r == 0x7f || (pct && r == '%') {
				return fmt.Errorf("%s path contains the character %q, which a service definition cannot carry safely", f.name, r)
			}
		}
	}
	return nil
}

// checkUnix validates the inputs shared by the launchd and systemd backends.
func checkUnix(spec Spec, env Env) error {
	if err := checkSpecText(spec, false); err != nil {
		return err
	}
	if !strings.HasPrefix(spec.Executable, "/") || !strings.HasPrefix(spec.Home, "/") {
		return errors.New("executable and home must be absolute paths")
	}
	if !strings.HasPrefix(env.HomeDir, "/") {
		return errors.New("user home directory must be an absolute path")
	}
	return nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s)) // bytes.Buffer never fails
	return b.String()
}
