package device

import (
	"strings"
	"testing"
)

// Review 55 R55-097: the "#!" line names the interpreter that runs a script.
// An absolute interpreter is returned for the ownership check; a relative one
// is refused; a file that is not a "#!" script has none. For env, the program
// it runs is returned too, to be found on the run's PATH (D71); env options,
// variables and -S expansions that the check cannot follow are refused.
func TestParseShebang(t *testing.T) {
	for _, tc := range []struct {
		head, want, prog, err string
	}{
		{"\x7fELF\x02\x01", "", "", ""},
		{"#!/bin/sh\necho hi\n", "/bin/sh", "", ""},
		{"#! /opt/tools/node/bin/node --max-old-space-size=64\n", "/opt/tools/node/bin/node", "", ""},
		{"#!/usr/bin/python3\r\nprint(1)\r\n", "/usr/bin/python3", "", ""},
		{"#!/bin/sh", "/bin/sh", "", ""},
		{"#!/usr/bin/env python3\n", "/usr/bin/env", "python3", ""},
		{"#!/usr/bin/env node\n", "/usr/bin/env", "node", ""},
		{"#!/usr/bin/env -S python3 -u\n", "/usr/bin/env", "python3", ""},
		{"#!/usr/bin/env /opt/node/bin/node\n", "/usr/bin/env", "/opt/node/bin/node", ""},
		{"#!/usr/bin/env\n", "", "", "names no program"},
		{"#!/usr/bin/env -S\n", "", "", "names no program"},
		{"#!/usr/bin/env -i node\n", "", "", "option"},
		{"#!/usr/bin/env -S -u HOME node\n", "", "", "option"},
		{"#!/usr/bin/env PATH=/tmp node\n", "", "", "cannot be checked"},
		{"#!/usr/bin/env -S ${HOME}/bin/node\n", "", "", "expand"},
		{"#!/usr/bin/env ./node\n", "", "", "not an absolute path"},
		{"#!python3\n", "", "", "not an absolute path"},
		{"#!./tool\n", "", "", "not an absolute path"},
		{"#!\n", "", "", "no interpreter"},
		{"#!   \t\n", "", "", "no interpreter"},
	} {
		got, prog, err := parseShebang([]byte(tc.head))
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("parseShebang(%q): %v", tc.head, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("parseShebang(%q): err = %v, want one naming %q", tc.head, err, tc.err)
		case got != tc.want || prog != tc.prog:
			t.Errorf("parseShebang(%q) = %q, %q, want %q, %q", tc.head, got, prog, tc.want, tc.prog)
		}
	}
}

// envValue reads PATH as os/exec would: the last one set wins.
func TestEnvValue(t *testing.T) {
	if v, ok := envValue([]string{"HOME=/h", "PATH=/a", "PATH=/b:/c"}, "PATH"); !ok || v != "/b:/c" {
		t.Errorf("envValue = %q, %v", v, ok)
	}
	if _, ok := envValue([]string{"HOME=/h", "PATHX=/a"}, "PATH"); ok {
		t.Error("envValue found an unset PATH")
	}
}
