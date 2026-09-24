package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeviceUsageAndArgumentErrors(t *testing.T) {
	shortHome(t)
	for _, args := range [][]string{{"device"}, {"device", "--help"}, {"device", "link", "--help"}, {"device", "list", "-h"}, {"device", "unlink", "--help"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitOK || !strings.Contains(out.String(), "agentnet device") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out.String(), errb.String())
		}
	}
	for _, args := range [][]string{
		{"device", "nope"},
		{"device", "link"},
		{"device", "link", "@p", "--as", "controller"},
		{"device", "link", "@p", "--as", "boss", "--fingerprint", "x"},
		{"device", "list", "extra"},
		{"device", "unlink"},
		{"device", "unlink", "a", "b"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d (stderr %q)", args, code, exitUsage, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"device", "list"}, &out, &errb); code != exitDaemonNotFound {
		t.Errorf("device list without a daemon: code %d, want %d", code, exitDaemonNotFound)
	}
}
