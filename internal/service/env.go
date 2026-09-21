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

// checkUnix validates the inputs shared by the launchd and systemd backends.
func checkUnix(spec Spec, env Env) error {
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
