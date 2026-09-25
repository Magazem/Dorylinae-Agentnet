package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// --- Decision (Docs/protocol/decision.md §Signing, §Signed file, §Vector) ---
//
//	id            = "d-" ++ hex(SHA-256("dorylinae-decision-id-v1\n" ++ session)[0:16])
//	msg           = "dorylinae-decision-v1\n" ++ canonical(decision)
//	decision_hash = hex(SHA-256(msg))
//	sig           = base64url(Ed25519-Sign(key, msg))
//
// with the initiator = seed 00..1f and the respondent = seed 20..3f of
// pairing.md. The negatives run through verifyDecision below, this file's
// own reading of §Signed file steps 1-5 (not internal/decision).

type decisionVectors struct {
	Session       string `json:"session"`
	Canonical     string `json:"canonical"`
	ID            string `json:"id"`
	HashHex       string `json:"hash_hex"`
	SigInitiator  string `json:"sig_initiator"`
	SigRespondent string `json:"sig_respondent"`
}

func decisionSeed(start byte) ed25519.PrivateKey {
	s := make([]byte, 32)
	for i := range s {
		s[i] = start + byte(i)
	}
	return ed25519.NewKeyFromSeed(s)
}

func decisionMsg(canon []byte) []byte {
	return append([]byte("dorylinae-decision-v1\n"), canon...)
}

func decisionID(session string) string {
	h := sha256.Sum256([]byte("dorylinae-decision-id-v1\n" + session))
	return "d-" + hex.EncodeToString(h[:16])
}

// toMembers turns decoded maps into this file's []member form for writeValue.
func toMembers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		ms := make([]member, 0, len(t))
		for k, x := range t {
			ms = append(ms, member{k, toMembers(x)})
		}
		return ms
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = toMembers(x)
		}
		return out
	}
	return v
}

func canonOfValue(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeValue(&b, toMembers(v)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// decodeJSON parses strictly (canonical() refuses duplicates and trailing
// data) and returns maps and []any with json.Number.
func decodeJSON(raw []byte) (any, error) {
	if _, err := canonical(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	return v, dec.Decode(&v)
}

var (
	decisionRequired = []string{"v", "id", "session", "request", "team", "participants", "problem", "rounds_max",
		"positions", "outcome", "reason", "opened", "closed"}
	decisionOptional = []string{"rounds", "converge", "final_agreement", "remaining_disagreement", "human_decisions", "affected_artifacts"}
)

func hasOnly(o map[string]any, required, optional []string) bool {
	for _, k := range required {
		if _, ok := o[k]; !ok {
			return false
		}
	}
	for k := range o {
		found := false
		for _, a := range append(append([]string{}, required...), optional...) {
			if k == a {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// freeTextOK walks v: only "argument" (and the topic) may hold \n and \t;
// no string holds another C0 control, DEL or a C1 control (debate.md
// §Messages, the free-text rule).
func freeTextOK(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if !freeTextOK(x, k) {
				return false
			}
		}
	case []any:
		for _, x := range t {
			if !freeTextOK(x, key) {
				return false
			}
		}
	case string:
		multi := key == "argument" || key == "topic"
		for _, r := range t {
			switch {
			case multi && (r == '\n' || r == '\t'):
			case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
				return false
			}
		}
	}
	return true
}

// verifyDecision returns the failing step of §Signed file (1-5), or 0 and
// whether both parties signed.
func verifyDecision(raw []byte) (step int, complete bool) {
	v, err := decodeJSON(raw)
	if err != nil {
		return 1, false
	}
	file, ok := v.(map[string]any)
	if !ok || !hasOnly(file, []string{"decision", "hash", "signatures"}, nil) {
		return 1, false
	}
	d, ok1 := file["decision"].(map[string]any)
	hash, ok2 := file["hash"].(string)
	sigs, ok3 := file["signatures"].(map[string]any)
	if !ok1 || !ok2 || !ok3 || !hasOnly(sigs, []string{"initiator"}, []string{"respondent"}) ||
		!hasOnly(d, decisionRequired, decisionOptional) || !freeTextOK(d, "") {
		return 1, false
	}
	parts, _ := d["participants"].(map[string]any)
	if o, _ := d["outcome"].(string); o != "agreed" && o != "escalated" {
		return 1, false
	}
	canon, err := canonOfValue(d)
	if err != nil {
		return 1, false
	}
	sum := sha256.Sum256(decisionMsg(canon))
	if hex.EncodeToString(sum[:]) != hash {
		return 2, false
	}
	for role, s := range sigs {
		key, _ := parts[role].(string)
		pub, err1 := base64.RawURLEncoding.DecodeString(key)
		str, _ := s.(string)
		sig, err2 := base64.RawURLEncoding.DecodeString(str)
		if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, decisionMsg(canon), sig) {
			return 3, false
		}
	}
	if session, _ := d["session"].(string); d["id"] != decisionID(session) {
		return 4, false
	}
	// Step 5, the invariants the vector's negatives exercise: rule 6 and
	// final_agreement (present iff agreed, equal to the proposal's).
	conv, _ := d["converge"].(map[string]any)
	proposal, _ := conv["proposal"].(map[string]any)
	answer, hasAnswer := conv["answer"].(map[string]any)
	want := [2]string{"escalated", "timeout"}
	if hasAnswer {
		want = [2]string{"escalated", "rejected"}
		if answer["accept"] == true {
			want = [2]string{"agreed", "accepted"}
		}
	}
	if d["outcome"] != want[0] || d["reason"] != want[1] {
		return 5, false
	}
	fa, hasFA := d["final_agreement"]
	if hasFA != (want[0] == "agreed") {
		return 5, false
	}
	if hasFA {
		a, err1 := canonOfValue(fa)
		b, err2 := canonOfValue(proposal["agreement"])
		if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
			return 5, false
		}
	}
	if parts["initiator"] == parts["respondent"] {
		return 5, false
	}
	_, complete = sigs["respondent"]
	return 0, complete
}

func decisionVector(c *checker, v *vectors) {
	dv := &v.Decision
	canon, err := canonical([]byte(dv.Canonical))
	if err != nil {
		c.ok("decision canonicalises", false, err.Error())
		return
	}
	c.eqs("decision is canonical", string(canon), dv.Canonical)
	c.eqs("decision id", decisionID(dv.Session), dv.ID)
	c.ok("decision id member", strings.Contains(dv.Canonical, `"id":"`+dv.ID+`"`), "id member differs")
	sum := sha256.Sum256(decisionMsg(canon))
	c.eq("decision hash", sum[:], mustHex(c, "decision hash_hex", dv.HashHex))
	privI, privR := decisionSeed(0x00), decisionSeed(0x20)
	sigI := base64.RawURLEncoding.EncodeToString(ed25519.Sign(privI, decisionMsg(canon)))
	sigR := base64.RawURLEncoding.EncodeToString(ed25519.Sign(privR, decisionMsg(canon)))
	c.eqs("decision sig initiator", sigI, dv.SigInitiator)
	c.eqs("decision sig respondent", sigR, dv.SigRespondent)

	base := func() map[string]any {
		x, err := decodeJSON(canon)
		if err != nil {
			panic(err)
		}
		return x.(map[string]any)
	}
	file := func(d map[string]any, hash string, sigs map[string]any) []byte {
		b, err := canonOfValue(map[string]any{"decision": d, "hash": hash, "signatures": sigs})
		if err != nil {
			panic(err)
		}
		return b
	}
	resigned := func(d map[string]any) []byte {
		cd, err := canonOfValue(d)
		if err != nil {
			panic(err)
		}
		s := sha256.Sum256(decisionMsg(cd))
		return file(d, hex.EncodeToString(s[:]), map[string]any{
			"initiator":  base64.RawURLEncoding.EncodeToString(ed25519.Sign(privI, decisionMsg(cd))),
			"respondent": base64.RawURLEncoding.EncodeToString(ed25519.Sign(privR, decisionMsg(cd))),
		})
	}
	both := map[string]any{"initiator": dv.SigInitiator, "respondent": dv.SigRespondent}

	step, complete := verifyDecision(file(base(), dv.HashHex, both))
	c.ok("decision file verifies with two signatures", step == 0 && complete, fmt.Sprintf("step %d complete %v", step, complete))
	step, complete = verifyDecision(file(base(), dv.HashHex, map[string]any{"initiator": dv.SigInitiator}))
	c.ok("decision without the respondent signature: valid, unconfirmed (exit 6)", step == 0 && !complete,
		fmt.Sprintf("step %d complete %v", step, complete))

	negs := []struct {
		name string
		file []byte
		step int
	}{
		{"respondent signature moved to initiator", file(base(), dv.HashHex, map[string]any{"initiator": dv.SigRespondent}), 3},
		{"outcome escalated", func() []byte { d := base(); d["outcome"] = "escalated"; return file(d, dv.HashHex, both) }(), 2},
		{"outcome escalated, hash recomputed", func() []byte {
			d := base()
			d["outcome"] = "escalated"
			cd, _ := canonOfValue(d)
			s := sha256.Sum256(decisionMsg(cd))
			return file(d, hex.EncodeToString(s[:]), both)
		}(), 3},
		{"member added", func() []byte { d := base(); d["note"] = "x"; return file(d, dv.HashHex, both) }(), 1},
		{"id of another session", func() []byte {
			d := base()
			d["id"] = decisionID("s-00000000000000000000000000000000")
			return resigned(d)
		}(), 4},
		{"claim with a newline", func() []byte {
			d := base()
			d["positions"].(map[string]any)["respondent"].(map[string]any)["initial"].(map[string]any)["claim"] = "Add full\njitter"
			return resigned(d)
		}(), 1},
		{"final_agreement changed, resigned", func() []byte {
			d := base()
			d["final_agreement"] = map[string]any{"decision": "Fixed 30 s retry"}
			return resigned(d)
		}(), 5},
	}
	for _, n := range negs {
		step, _ := verifyDecision(n.file)
		c.ok("decision negative fails at step "+fmt.Sprint(n.step)+": "+n.name, step == n.step, fmt.Sprintf("failed at step %d", step))
	}
}
