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
// The line is split on spaces and tabs only, a control character in it is
// refused, and on Linux (linux) env without -S names one word (review 86b
// M2).
func TestParseShebang(t *testing.T) {
	for _, tc := range []struct {
		head, want, prog, err string
		linux                 bool
	}{
		{"\x7fELF\x02\x01", "", "", "", false},
		{"#!/bin/sh\necho hi\n", "/bin/sh", "", "", false},
		{"#! /opt/tools/node/bin/node --max-old-space-size=64\n", "/opt/tools/node/bin/node", "", "", false},
		{"#!/usr/bin/python3\r\nprint(1)\r\n", "", "", "control character", false},
		{"#!/usr/bin/env node\r\n", "", "", "control character", true},
		{"#!/usr/bin/env -S node\v--x\n", "", "", "control character", false},
		{"#!/usr/bin/env no\x00de\n", "", "", "control character", false},
		{"#!/usr/bin/env node\n", "/usr/bin/env node", "", "", false},
		{"#!/usr/bin/env node --x\n", "/usr/bin/env", "node --x", "", true},
		{"#!/usr/bin/env\tnode \t\n", "/usr/bin/env", "node", "", true},
		{"#!/usr/bin/env node --flag\n", "/usr/bin/env", "node", "", false},
		{"#!/usr/bin/env node --flag\n", "", "", "one program name", true},
		{"#!/usr/bin/env node\t--flag\n", "", "", "one program name", true},
		{"#!/usr/bin/env -S node --flag\n", "/usr/bin/env", "node", "", true},
		{"#! /opt/tools/node/bin/node --max-old-space-size=64 -x\n", "/opt/tools/node/bin/node", "", "", true},
		{"#!/bin/sh", "/bin/sh", "", "", false},
		{"#!/usr/bin/env python3\n", "/usr/bin/env", "python3", "", false},
		{"#!/usr/bin/env node\n", "/usr/bin/env", "node", "", false},
		{"#!/usr/bin/env -S python3 -u\n", "/usr/bin/env", "python3", "", false},
		{"#!/usr/bin/env /opt/node/bin/node\n", "/usr/bin/env", "/opt/node/bin/node", "", false},
		{"#!/usr/bin/env\n", "", "", "names no program", false},
		{"#!/usr/bin/env -S\n", "", "", "names no program", false},
		{"#!/usr/bin/env -i node\n", "", "", "option", false},
		{"#!/usr/bin/env -S -u HOME node\n", "", "", "option", false},
		{"#!/usr/bin/env PATH=/tmp node\n", "", "", "cannot be checked", false},
		{"#!/usr/bin/env -S ${HOME}/bin/node\n", "", "", "expand", false},
		{"#!/usr/bin/env ./node\n", "", "", "not an absolute path", false},
		{"#!python3\n", "", "", "not an absolute path", false},
		{"#!./tool\n", "", "", "not an absolute path", false},
		{"#!\n", "", "", "no interpreter", false},
		{"#!   \t\n", "", "", "no interpreter", false},
	} {
		got, prog, err := parseShebang([]byte(tc.head), tc.linux)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("parseShebang(%q, %v): %v", tc.head, tc.linux, err)
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
