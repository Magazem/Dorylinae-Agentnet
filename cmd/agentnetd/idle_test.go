package main

import (
	"io"
	"log/slog"
	"testing"
)

// R55-033: the production options wire the OS idle probe (behind its cache);
// without it human_present is always null.
func TestDaemonOptionsWireIdle(t *testing.T) {
	o := daemonOptions("", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if o.Idle == nil {
		t.Fatal("daemonOptions leaves Options.Idle nil, so human_present is always unknown")
	}
}
