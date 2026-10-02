package main

// Regression tests for review 90 (R55-F10 security review). Code points are
// built at run time so this file holds no raw invisible character.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
)

var rlo = string(rune(0x202E))

// esc202e is the six-character JSON escape of U+202E.
const esc202e = "\\" + "u202e"

// S90-2: an undecodable byte inside a json.RawMessage (a lone 0x9B, the 8-bit
// CSI) is written as \ufffd.
func TestWriteJSONRawMessageInvalidUTF8(t *testing.T) {
	raw := json.RawMessage("{\"t\":\"a\x9b31mb" + rlo + "c\"}")
	var buf bytes.Buffer
	writeJSON(&buf, struct {
		E json.RawMessage `json:"e"`
	}{raw})
	if bytes.IndexByte(buf.Bytes(), 0x9b) >= 0 {
		t.Errorf("raw 0x9B byte reaches --json output: %q", buf.Bytes())
	}
	if !strings.Contains(buf.String(), "mb"+esc202e+"c") {
		t.Errorf("want the u202e escape, got %q", buf.Bytes())
	}
}

// S90-1: `decision <id> --json` writes hidden runes of peer text as \uXXXX.
func TestDecisionJSONStdoutEscapesBidi(t *testing.T) {
	var res daemon.DecisionShowResult
	res.Decision = json.RawMessage("{\"problem\":{\"title\":\"ok " + rlo + "evil\",\"topic\":\"t\"}}")
	res.Hash = "00"
	data, err := signedFileBytes(res)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := writeDecisionOutput(&buf, &buf, displaytext.JSON(data), "", false); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if bytes.Contains(buf.Bytes(), []byte(rlo)) {
		t.Errorf("stdout holds a raw U+202E: %q", buf.Bytes())
	}
	if !strings.Contains(buf.String(), esc202e) {
		t.Errorf("stdout lacks the u202e escape: %q", buf.Bytes())
	}
	// The escaped output decodes to the same value.
	var back decisionSignedFile
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(back.Decision, &a) != nil || json.Unmarshal(res.Decision, &b) != nil {
		t.Fatal("decision does not parse")
	}
	if ja, jb := mustJSON(t, a), mustJSON(t, b); ja != jb {
		t.Errorf("decoded decision changed: %s vs %s", ja, jb)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
