//go:build linux

package notify

import (
	"context"
	"testing"
)

// R55-125 (T8): the Linux window check, with its three lookups injected.
func TestLinuxCheck(t *testing.T) {
	ctx := context.Background()
	has := func(names ...string) func(string) (string, bool) {
		return func(n string) (string, bool) {
			for _, w := range names {
				if w == n {
					return "/usr/bin/" + n, true
				}
			}
			return "", false
		}
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	sysd := func(m map[string]string) func(context.Context) map[string]string {
		return func(context.Context) map[string]string { return m }
	}
	for _, tc := range []struct {
		name string
		find func(string) (string, bool)
		env  map[string]string
		sysd map[string]string
		ok   bool
		fix  string
	}{
		{"no dialog program", has(), map[string]string{"DISPLAY": ":0"}, nil, false, "install zenity (or kdialog)"},
		{"zenity and a display", has("zenity"), map[string]string{"DISPLAY": ":0"}, nil, true, ""},
		{"kdialog and wayland", has("kdialog"), map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, nil, true, ""},
		{"no display anywhere", has("zenity"), nil, nil, false, "no desktop session"},
		{"display only in the systemd user manager", has("zenity"), nil, map[string]string{"DISPLAY": ":1"}, true, ""},
	} {
		ok, fix := linuxCheck(ctx, tc.find, env(tc.env), sysd(tc.sysd))
		if ok != tc.ok || fix != tc.fix {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.name, ok, fix, tc.ok, tc.fix)
		}
	}
}
