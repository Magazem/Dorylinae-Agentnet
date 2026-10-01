package device

import (
	"strings"
	"testing"
)

// Review 55 R55-097: the "#!" line names the interpreter that runs a script.
// An absolute interpreter is returned for the ownership check; a relative one
// or env (which finds the program through PATH) is refused; a file that is not
// a "#!" script has none.
func TestParseShebang(t *testing.T) {
	for _, tc := range []struct {
		head, want, err string
	}{
		{"\x7fELF\x02\x01", "", ""},
		{"#!/bin/sh\necho hi\n", "/bin/sh", ""},
		{"#! /opt/tools/node/bin/node --max-old-space-size=64\n", "/opt/tools/node/bin/node", ""},
		{"#!/usr/bin/python3\r\nprint(1)\r\n", "/usr/bin/python3", ""},
		{"#!/bin/sh", "/bin/sh", ""},
		{"#!/usr/bin/env python3\n", "", "PATH"},
		{"#!/usr/bin/env -S python3 -u\n", "", "PATH"},
		{"#!python3\n", "", "not an absolute path"},
		{"#!./tool\n", "", "not an absolute path"},
		{"#!\n", "", "no interpreter"},
		{"#!   \t\n", "", "no interpreter"},
	} {
		got, err := parseShebang([]byte(tc.head))
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("parseShebang(%q): %v", tc.head, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("parseShebang(%q): err = %v, want one naming %q", tc.head, err, tc.err)
		case got != tc.want:
			t.Errorf("parseShebang(%q) = %q, want %q", tc.head, got, tc.want)
		}
	}
}
