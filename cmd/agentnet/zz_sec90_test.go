package main

// Review 90 (R55-F10 security review): probes of writeJSON and the decision
// --json output. Created by the reviewer; see
// Docs/review/90-r55-f10-security.md. Code points are built with rune(...)
// so this file holds no raw invisible character.

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

var rlo = string(rune(0x202E))

// S90-2: a json.RawMessage holding invalid UTF-8 (a lone 0x9B byte, the
// 8-bit CSI) is copied by encoding/json's compact and then by
// displaytext.JSON as is.
func TestSec90RawMessageInvalidUTF8(t *testing.T) {
	raw := json.RawMessage("{\"t\":\"a\x9b31mb" + rlo + "c\"}")
	var buf bytes.Buffer
	writeJSON(&buf, struct {
		E json.RawMessage `json:"e"`
	}{raw})
	if bytes.IndexByte(buf.Bytes(), 0x9b) >= 0 {
		t.Errorf("S90-2: raw 0x9B byte reaches --json output: %q", buf.Bytes())
	}
}

// S90-1: `agentnet decision <id> --json` without --out writes
// signedFileBytes to stdout as is, so a Decision text member holding U+202E
// (debate and request text allow Cf) reaches the terminal raw.
func TestSec90DecisionJSONStdoutRawBidi(t *testing.T) {
	var res daemon.DecisionShowResult
	res.Decision = json.RawMessage("{\"problem\":{\"title\":\"ok " + rlo + "evil\",\"topic\":\"t\"}}")
	res.Hash = "00"
	data, err := signedFileBytes(res)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := writeDecisionOutput(&buf, &buf, data, "", false); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if bytes.Contains(buf.Bytes(), []byte(rlo)) {
		t.Errorf("S90-1: decision --json stdout holds a raw U+202E: %q", buf.Bytes())
	}
}
