// Package decision builds, signs and verifies Decision records
// (Docs/protocol/decision.md, ticket 3.3a): the deterministic derivation of
// a Decision from a debate transcript, its canonical form, the id and hash,
// the Ed25519 signatures of both daemons, and the offline verification of a
// signed file.
//
// It is a leaf package: internal/debate derives and signs through it inside
// its closing transactions, so it cannot import internal/debate. The debate
// message validators the verifier needs are passed in as a Schema.
package decision

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Domain strings (decision.md §Derivation rule 1, §Signing).
const (
	signDomain = "dorylinae-decision-v1\n"
	idDomain   = "dorylinae-decision-id-v1\n"
)

// MaxDecision caps len(canonical(decision)) (decision.md §Size). Exceeding it
// is a bug, not a peer error.
const MaxDecision = 786432

// Roles of the two parties (the "participants" members and "by").
const (
	RoleInitiator  = "initiator"
	RoleRespondent = "respondent"
)

// Outcomes and reasons a Decision can carry (rule 6).
const (
	OutcomeAgreed    = "agreed"
	OutcomeEscalated = "escalated"
	ReasonAccepted   = "accepted"
	ReasonRejected   = "rejected"
	ReasonTimeout    = "timeout"
)

// Entry kinds, as in internal/debate.
const (
	kindPosition = "position"
	kindMove     = "move"
	kindProposal = "proposal"
	kindAnswer   = "answer"
)

const timeFmt = "2006-01-02T15:04:05Z"

// ID is rule 1: "d-" ‖ lowercase-hex(SHA-256("dorylinae-decision-id-v1\n" ‖ session)[0:16]).
func ID(session string) string {
	h := sha256.Sum256([]byte(idDomain + session))
	return "d-" + hex.EncodeToString(h[:16])
}

// Msg is the signed message: "dorylinae-decision-v1\n" ‖ canonical(decision).
func Msg(canon []byte) []byte {
	m := make([]byte, 0, len(signDomain)+len(canon))
	return append(append(m, signDomain...), canon...)
}

// Hash is decision_hash: lowercase-hex SHA-256(Msg(canon)).
func Hash(canon []byte) string {
	h := sha256.Sum256(Msg(canon))
	return hex.EncodeToString(h[:])
}

// Sign returns base64url(Ed25519-Sign(priv, Msg(canon))).
func Sign(priv ed25519.PrivateKey, canon []byte) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, Msg(canon)))
}

// VerifySignature reports whether sig (base64url) is key's (a wire-form
// identity key) signature over Msg(canon).
func VerifySignature(key string, canon []byte, sig string) bool {
	pub, err := b64.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	s, err := b64.DecodeString(sig)
	if err != nil || len(s) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), Msg(canon), s)
}

// Entry is one transcript slot, its canonical entry bytes as stored.
type Entry struct {
	Slot   int
	Author string // RoleInitiator or RoleRespondent
	Kind   string // position, move, proposal or answer
	Canon  []byte
}

// Constraint is one human constraint listed in A's close (rule 9).
type Constraint struct {
	ID     string
	Author string // RoleInitiator or RoleRespondent
	At     string // wire form, as stored
	Text   string
}

// Input is everything the derivation reads (rules 1-12): no local name,
// clock or configuration.
type Input struct {
	Session    string
	Initiator  string // identity keys, wire form
	Respondent string
	// Request is the stored canonical request object (its id, team, title,
	// brief, created and context are read from it).
	Request   []byte
	RoundsMax int
	// Entries are the transcript entries; only those with Slot < Count are
	// used (rule 12).
	Entries []Entry
	Count   int
	// Constraints are those listed in A's close, in any order (rule 9 sorts).
	Constraints []Constraint
	Outcome     string
	Reason      string
	Closed      string // the at of A's debate.close
}

// ErrInconsistent is an outcome and reason that rule 6 does not give for the
// transcript (a close that claims agreement without the respondent's accept,
// or a timeout although the answer is in).
var ErrInconsistent = errors.New("decision: outcome is inconsistent with the transcript")

// ErrIncomplete is a transcript without both positions: there is no Decision.
var ErrIncomplete = errors.New("decision: the transcript lacks a position")

// ErrClosedBeforeOpened is a close whose at is earlier than the request's
// created: the Decision would fail verification step 5 (opened <= closed),
// so none is derived (review 47 M1).
var ErrClosedBeforeOpened = errors.New("decision: closed is before opened")

// b64 decodes keys and signatures strictly: the unused low bits of the last
// character must be zero, so each value has one text form (review 47 L1).
var b64 = base64.RawURLEncoding.Strict()

// TooLargeError is a derived Decision over MaxDecision.
type TooLargeError struct{ Size int }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("decision: canonical Decision is %d bytes, over MaxDecision (%d)", e.Size, MaxDecision)
}

// Expected is rule 6: the outcome and reason the answer gives (answer nil
// when there is none).
func Expected(hasAnswer, accept bool) (outcome, reason string) {
	switch {
	case !hasAnswer:
		return OutcomeEscalated, ReasonTimeout
	case accept:
		return OutcomeAgreed, ReasonAccepted
	}
	return OutcomeEscalated, ReasonRejected
}

// Derive builds canonical(decision) from in (decision.md §Derivation).
func Derive(in Input) ([]byte, error) {
	req, err := parseObject(in.Request)
	if err != nil {
		return nil, fmt.Errorf("decision: stored request: %w", err)
	}
	entries := map[int]Entry{}
	slots := []int{}
	for _, e := range in.Entries {
		if e.Slot < 0 || e.Slot >= in.Count {
			continue
		}
		if _, dup := entries[e.Slot]; dup {
			return nil, fmt.Errorf("decision: slot %d twice", e.Slot)
		}
		entries[e.Slot] = e
		slots = append(slots, e.Slot)
	}
	sort.Ints(slots)
	values := map[int]map[string]any{}
	for _, s := range slots {
		v, err := parseObject(entries[s].Canon)
		if err != nil {
			return nil, fmt.Errorf("decision: entry %d: %w", s, err)
		}
		values[s] = v
	}
	p0, ok0 := entries[0]
	p1, ok1 := entries[1]
	if !ok0 || !ok1 || p0.Kind != kindPosition || p1.Kind != kindPosition ||
		p0.Author != RoleInitiator || p1.Author != RoleRespondent {
		return nil, ErrIncomplete
	}

	finals := map[string]any{}
	type round struct{ moves map[string]any }
	rounds := map[int]*round{}
	var roundNums []int
	var proposal, answer map[string]any
	for _, s := range slots {
		e, v := entries[s], values[s]
		switch e.Kind {
		case kindMove:
			n := s / 2
			r, ok := rounds[n]
			if !ok {
				r = &round{moves: map[string]any{}}
				rounds[n] = r
				roundNums = append(roundNums, n)
			}
			r.moves[e.Author] = v
			if rev, ok := v["revision"]; ok {
				finals[e.Author] = rev
			}
		case kindProposal:
			proposal = v
		case kindAnswer:
			answer = v
		}
	}
	accept := false
	if answer != nil {
		accept, _ = answer["accept"].(bool)
	}
	if o, r := Expected(answer != nil, accept); o != in.Outcome || r != in.Reason {
		return nil, ErrInconsistent
	}
	created, _ := req["created"].(string)
	opened, err1 := time.Parse(timeFmt, created)
	closed, err2 := time.Parse(timeFmt, in.Closed)
	if err1 != nil || err2 != nil {
		return nil, errors.New("decision: opened or closed is not a wire time")
	}
	if closed.Before(opened) {
		return nil, ErrClosedBeforeOpened
	}

	d := map[string]any{
		"v":       json.Number("1"),
		"id":      ID(in.Session),
		"session": in.Session,
		"request": req["id"],
		"team":    req["team"],
		"participants": map[string]any{
			RoleInitiator: in.Initiator, RoleRespondent: in.Respondent,
		},
		"rounds_max": json.Number(fmt.Sprint(in.RoundsMax)),
		"outcome":    in.Outcome,
		"reason":     in.Reason,
		"opened":     req["created"],
		"closed":     in.Closed,
	}
	problem := map[string]any{"title": req["title"], "topic": req["brief"]}
	if ctxFiles, ok := req["context"].([]any); ok && len(ctxFiles) > 0 {
		refs := make([]any, len(ctxFiles))
		for i, f := range ctxFiles {
			fo, _ := f.(map[string]any)
			name, _ := fo["name"].(string)
			text, _ := fo["text"].(string)
			sum := sha256.Sum256([]byte(text))
			refs[i] = map[string]any{"name": name, "bytes": json.Number(fmt.Sprint(len(text))), "sha256": hex.EncodeToString(sum[:])}
		}
		problem["context"] = refs
	}
	d["problem"] = problem

	positions := map[string]any{}
	for role, v := range map[string]map[string]any{RoleInitiator: values[0], RoleRespondent: values[1]} {
		p := map[string]any{"initial": v}
		if f, ok := finals[role]; ok {
			p["final"] = f
		}
		positions[role] = p
	}
	d["positions"] = positions

	if len(roundNums) > 0 {
		list := make([]any, len(roundNums))
		for i, n := range roundNums {
			o := map[string]any{"n": json.Number(fmt.Sprint(i + 1))}
			for role, mv := range rounds[n].moves {
				o[role] = mv
			}
			list[i] = o
		}
		d["rounds"] = list
	}
	if proposal != nil {
		c := map[string]any{"proposal": proposal}
		if answer != nil {
			c["answer"] = answer
		}
		d["converge"] = c
		if in.Outcome == OutcomeAgreed {
			d["final_agreement"] = proposal["agreement"]
		}
		rd, err := mergeDisagreement(proposal, answer)
		if err != nil {
			return nil, err
		}
		if len(rd) > 0 {
			d["remaining_disagreement"] = rd
		}
		if aa, ok := proposal["affected_artifacts"].([]any); ok && len(aa) > 0 {
			d["affected_artifacts"] = aa
		}
	}
	if len(in.Constraints) > 0 {
		cs := append([]Constraint(nil), in.Constraints...)
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].At != cs[j].At {
				return cs[i].At < cs[j].At
			}
			return cs[i].ID < cs[j].ID
		})
		list := make([]any, len(cs))
		for i, c := range cs {
			list[i] = map[string]any{"id": c.ID, "by": c.Author, "at": c.At, "text": c.Text}
		}
		d["human_decisions"] = list
	}
	canon, err := agentcard.CanonicalValue(d)
	if err != nil {
		return nil, fmt.Errorf("decision: canonical: %w", err)
	}
	if len(canon) > MaxDecision {
		return nil, &TooLargeError{Size: len(canon)}
	}
	return canon, nil
}

// mergeDisagreement is rule 8: the proposal's list followed by the answer's,
// exact duplicates (same canonical bytes) dropped after the first.
func mergeDisagreement(proposal, answer map[string]any) ([]any, error) {
	var out []any
	seen := map[string]bool{}
	for _, src := range []map[string]any{proposal, answer} {
		if src == nil {
			continue
		}
		list, _ := src["remaining_disagreement"].([]any)
		for _, d := range list {
			c, err := agentcard.CanonicalValue(d)
			if err != nil {
				return nil, fmt.Errorf("decision: disagreement: %w", err)
			}
			if seen[string(c)] {
				continue
			}
			seen[string(c)] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func parseObject(b []byte) (map[string]any, error) {
	v, err := agentcard.ParseStrict(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return o, nil
}

// canonicalEqual reports whether a and b have the same canonical bytes.
func canonicalEqual(a, b any) bool {
	ca, err1 := agentcard.CanonicalValue(a)
	cb, err2 := agentcard.CanonicalValue(b)
	return err1 == nil && err2 == nil && bytes.Equal(ca, cb)
}
