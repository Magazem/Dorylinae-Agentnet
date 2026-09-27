package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestDBFlagTakesPrecedenceOverQueueDBAlias: --queue-db is kept only as a
// backward-compatible alias (Docs/protocol/relay-hosted.md §3); when both
// are given, --db wins.
func TestDBFlagTakesPrecedenceOverQueueDBAlias(t *testing.T) {
	dir := testutil.TempDir(t)
	wantPath := filepath.Join(dir, "want.db")
	aliasPath := filepath.Join(dir, "alias.db")

	_, stop := startRelay(t, "--db", wantPath, "--queue-db", aliasPath)
	stop()

	assertFileExists(t, wantPath)
	assertFileMissing(t, aliasPath)
}

// TestQueueDBAliasStillWorksAlone: with no --db, --queue-db alone still
// selects the database file (unchanged CLI behaviour).
func TestQueueDBAliasStillWorksAlone(t *testing.T) {
	dir := testutil.TempDir(t)
	aliasPath := filepath.Join(dir, "alias-only.db")

	_, stop := startRelay(t, "--queue-db", aliasPath)
	stop()

	assertFileExists(t, aliasPath)
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want it untouched", path)
	}
}
