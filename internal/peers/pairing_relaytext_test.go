package peers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// R55-F9: every pairing failure passes one choke point (Docs/protocol/pairing.md
// §Logging, audit) and a relay error shows only daemon text (OD-R55F9-10).

// failAudit returns the reason and code of the one pair.fail row for id.
func failAudit(t *testing.T, log *audit.Log, id string) (code, reason string) {
	t.Helper()
	evs, err := log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Action != ActionPairFail {
			continue
		}
		var d map[string]string
		if err := json.Unmarshal(ev.Detail, &d); err != nil {
			t.Fatal(err)
		}
		if d["id"] == id {
			return d["code"], d["reason"]
		}
	}
	t.Fatalf("no pair.fail row for %s", id)
	return "", ""
}

// clean reports whether s is one display-safe line of at most n bytes.
func clean(s string, n int) bool {
	if len(s) > n {
		return false
	}
	for _, r := range s {
		if displaytext.Hidden(r) {
			return false
		}
	}
	return true
}

func TestRelayErrorShowsDaemonTextAndAuditsRelayMessage(t *testing.T) {
	m, _, log, _ := newRevManager(t, nil)
	_, id := startRevIssuer(t, m)
	// The frame as relayclient hands it over for code "x\x1b" and this message.
	relayMsg := "\x1b[2K\rPaired with alice"
	m.HandleError(envelope.ErrorFrame{Code: envelope.CodeRelayError, Message: displaytext.Line(relayMsg, 200), Ref: id})

	st, _ := m.Get(id)
	if st.State != StateFailed || st.Error == nil {
		t.Fatalf("status = %+v", st)
	}
	if st.Error.Code != envelope.CodeRelayError || st.Error.Message != "the relay refused the request" {
		t.Fatalf("status error = %+v, want relay_error with the daemon's fixed text", *st.Error)
	}
	code, reason := failAudit(t, log, id)
	if code != envelope.CodeRelayError || !clean(reason, maxReasonLen) || !strings.Contains(reason, "Paired with alice") {
		t.Fatalf("pair.fail code %q reason %q: want relay_error and the sanitised relay message", code, reason)
	}
}

// Even an unconverted frame (a caller that skips relayclient) cannot put an
// escape or a line break into the status or the audit row.
func TestRawRelayErrorIsCleanedAtTheChokePoint(t *testing.T) {
	m, _, log, _ := newRevManager(t, nil)
	_, id := startRevIssuer(t, m)
	m.HandleError(envelope.ErrorFrame{Code: "x\x1b", Message: "\x1b[2K\rPaired with alice", Ref: id})

	st, _ := m.Get(id)
	if st.Error == nil || !clean(st.Error.Code, maxCodeLen) || !clean(st.Error.Message, maxReasonLen) {
		t.Fatalf("status error = %+v", st.Error)
	}
	if strings.Contains(st.Error.Message, "alice") {
		t.Fatalf("status shows relay text: %q", st.Error.Message)
	}
	code, reason := failAudit(t, log, id)
	if !clean(code, maxCodeLen) || !clean(reason, maxReasonLen) {
		t.Fatalf("pair.fail code %q reason %q not clean", code, reason)
	}
}

// Test 15: a bad_card reason quoting a key name made of U+202E and U+3164
// (the card verifier's %q keeps U+3164), and a local failure with an
// over-long message.
func TestPairingFailuresPassTheChokePoint(t *testing.T) {
	m, _, log, _ := newRevManager(t, func(c *Config) { c.Wait = 50 * time.Millisecond })
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	rst, err := m.Redeem(context.Background(), code, false)
	if err != nil {
		t.Fatal(err)
	}
	peer := newRevIdent(t, "peer")
	name := "\u202e\u3164\u3164evil"
	card, _ := json.Marshal(name)
	bad := []byte(`{` + string(card) + `:1,` + string(card) + `:2}`)
	m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, Ref: rst.ID, PublicKey: peer.key, Card: bad, Mbox: peer.mbox})
	revWait(t, "bad_card failure", func() bool {
		st, _ := m.Get(rst.ID)
		return st.State == StateFailed
	})
	st, _ := m.Get(rst.ID)
	// The reason quotes the key name (so the verifier text reached the
	// choke point), minus its hidden runes.
	if st.Error.Code != FailBadCard || !strings.Contains(st.Error.Message, `"\u202eevil"`) ||
		!clean(st.Error.Message, maxReasonLen) || !clean(st.Error.Code, maxCodeLen) {
		t.Fatalf("status error = %+v", *st.Error)
	}
	if _, reason := failAudit(t, log, rst.ID); !clean(reason, maxReasonLen) {
		t.Fatalf("pair.fail reason %q not clean", reason)
	}

	_, id := startRevIssuer(t, m)
	long := strings.Repeat("\u3164é", 300) + strings.Repeat("x", 1000)
	m.finish(id, StateFailed, nil, &Failure{Code: strings.Repeat("c", 100) + "\u202e", Message: long})
	st, _ = m.Get(id)
	if !clean(st.Error.Code, maxCodeLen) || !clean(st.Error.Message, maxReasonLen) || !strings.HasSuffix(st.Error.Message, "…") {
		t.Fatalf("status error = %q / %q", st.Error.Code, st.Error.Message)
	}
	if code, reason := failAudit(t, log, id); !clean(code, maxCodeLen) || reason != st.Error.Message {
		t.Fatalf("pair.fail code %q reason %q, want the status's", code, reason)
	}
	for _, r := range st.Error.Message {
		if r == 0x3164 || unicode.Is(unicode.Cf, r) {
			t.Fatalf("hidden rune %U kept", r)
		}
	}
}
