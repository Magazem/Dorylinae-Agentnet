package decision

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Schema validates what the Decision embeds (decision.md §Signed file step
// 1: "the same validators as the daemon"). internal/debate provides it.
type Schema struct {
	// Entry checks canon, which must be the canonical bytes of a valid
	// entry of kind (position, move, proposal or answer).
	Entry func(kind string, canon []byte) error
	// Title checks problem.title (the request title rules).
	Title func(string) error
	// Topic checks problem.topic (the debate topic rules).
	Topic func(string) error
	// Constraint checks a human decision's text.
	Constraint func(string) error
}

// Result is the outcome of Verify (decision.md §Signed file step 6).
type Result struct {
	Valid bool
	// Complete is Valid with both signatures. A valid file signed by the
	// initiator only is unconfirmed (exit 6, review 43 H1).
	Complete bool
	// Step is the failing verification step (1-5), 0 when Valid.
	Step     int
	Reason   string
	SignedBy []string
	Hash     string
	ID       string
	// Initiator and Respondent are the participants' keys (when step 1
	// passed).
	Initiator  string
	Respondent string
	// Decision is canonical(decision) (when step 1 passed).
	Decision []byte
}

func fail(r Result, step int, format string, a ...any) Result {
	r.Valid, r.Complete, r.Step, r.Reason, r.SignedBy = false, false, step, fmt.Sprintf(format, a...), nil
	return r
}

// Verify checks a signed file {"decision", "hash", "signatures"} (decision.md
// §Signed file steps 1-5).
func Verify(data []byte, schema Schema) Result {
	var res Result
	v, err := agentcard.ParseStrict(data)
	if err != nil {
		return fail(res, 1, "not strict JSON: %v", err)
	}
	file, ok := v.(map[string]any)
	if !ok {
		return fail(res, 1, "not a JSON object")
	}
	if err := members("", file, []string{"decision", "hash", "signatures"}, nil); err != nil {
		return fail(res, 1, "%v", err)
	}
	d, ok := file["decision"].(map[string]any)
	if !ok {
		return fail(res, 1, "decision must be an object")
	}
	hash, ok := file["hash"].(string)
	if !ok || !hex64.MatchString(hash) {
		return fail(res, 1, "hash must be 64 lowercase hex characters")
	}
	sigs, ok := file["signatures"].(map[string]any)
	if !ok {
		return fail(res, 1, "signatures must be an object")
	}
	if err := members("signatures", sigs, []string{RoleInitiator}, []string{RoleRespondent}); err != nil {
		return fail(res, 1, "%v (the initiator always signs first)", err)
	}
	sig := map[string]string{}
	for k, s := range sigs {
		str, ok := s.(string)
		if !ok || !sigPattern.MatchString(str) {
			return fail(res, 1, "signatures.%s must be an 86-character base64url signature", k)
		}
		sig[k] = str
	}
	if err := checkSchema(d, schema); err != nil {
		return fail(res, 1, "decision: %v", err)
	}
	parts := d["participants"].(map[string]any)
	res.Initiator, _ = parts[RoleInitiator].(string)
	res.Respondent, _ = parts[RoleRespondent].(string)
	res.ID, _ = d["id"].(string)
	canon, err := agentcard.CanonicalValue(d)
	if err != nil {
		return fail(res, 1, "decision: %v", err)
	}
	res.Decision = canon

	// Step 2: the hash.
	res.Hash = Hash(canon)
	if res.Hash != hash {
		return fail(res, 2, "hash does not match canonical(decision)")
	}
	// Step 3: each present signature.
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		s, ok := sig[role]
		if !ok {
			continue
		}
		key := res.Initiator
		if role == RoleRespondent {
			key = res.Respondent
		}
		if !VerifySignature(key, canon, s) {
			return fail(res, 3, "the %s signature does not verify", role)
		}
	}
	// Step 4: the id.
	session, _ := d["session"].(string)
	if res.ID != ID(session) {
		return fail(res, 4, "id is not derived from the session")
	}
	// Step 5: the derivation invariants that need no transcript tables.
	if err := checkInvariants(d); err != nil {
		return fail(res, 5, "%v", err)
	}
	res.Valid = true
	res.SignedBy = []string{RoleInitiator}
	if _, ok := sig[RoleRespondent]; ok {
		res.SignedBy = append(res.SignedBy, RoleRespondent)
		res.Complete = true
	}
	return res
}

var (
	hex64          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sigPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{86}$`)
	keyPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	decisionIDPat  = regexp.MustCompile(`^d-[0-9a-f]{32}$`)
	sessionPat     = regexp.MustCompile(`^s-[0-9a-f]{32}$`)
	requestPat     = regexp.MustCompile(`^r-[0-9a-f]{32}$`)
	teamPat        = regexp.MustCompile(`^t-[0-9a-f]{32}$`)
	constraintPat  = regexp.MustCompile(`^c-[0-9a-f]{32}$`)
	errNotAnObject = errors.New("must be an object")
)

// members checks that o has every required member and nothing outside
// required and optional.
func members(path string, o map[string]any, required, optional []string) error {
	name := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	for _, k := range required {
		if _, ok := o[k]; !ok {
			return fmt.Errorf("%s is required", name(k))
		}
	}
	for k := range o {
		if !contains(required, k) && !contains(optional, k) {
			return fmt.Errorf("unknown member %s", name(k))
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func object(path string, v any) (map[string]any, error) {
	o, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s %w", path, errNotAnObject)
	}
	return o, nil
}

func array(path string, v any) ([]any, error) {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array", path)
	}
	return a, nil
}

func pattern(path string, v any, p *regexp.Regexp) error {
	s, ok := v.(string)
	if !ok || !p.MatchString(s) {
		return fmt.Errorf("%s is malformed", path)
	}
	return nil
}

func intIn(path string, v any, lo, hi int64) (int64, error) {
	n, ok := v.(interface{ Int64() (int64, error) })
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", path)
	}
	i, err := n.Int64()
	if err != nil || i < lo || i > hi {
		return 0, fmt.Errorf("%s must be %d-%d", path, lo, hi)
	}
	return i, nil
}

func wireTime(path string, v any) (time.Time, error) {
	s, ok := v.(string)
	t, err := time.Parse(timeFmt, s)
	if !ok || err != nil || t.Format(timeFmt) != s {
		return time.Time{}, fmt.Errorf("%s must be RFC 3339 UTC with Z and whole seconds", path)
	}
	return t, nil
}

// entry validates v as a canonical debate entry of kind.
func entry(schema Schema, path, kind string, v any) error {
	if _, err := object(path, v); err != nil {
		return err
	}
	canon, err := agentcard.CanonicalValue(v)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if schema.Entry == nil {
		return errors.New("no entry validator")
	}
	if err := schema.Entry(kind, canon); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func checkSchema(d map[string]any, schema Schema) error {
	if err := members("", d, []string{"v", "id", "session", "request", "team", "participants", "problem", "rounds_max",
		"positions", "outcome", "reason", "opened", "closed"},
		[]string{"rounds", "converge", "final_agreement", "remaining_disagreement", "human_decisions", "affected_artifacts"}); err != nil {
		return err
	}
	if _, err := intIn("v", d["v"], 1, 1); err != nil {
		return err
	}
	for _, c := range []struct {
		k string
		p *regexp.Regexp
	}{{"id", decisionIDPat}, {"session", sessionPat}, {"request", requestPat}, {"team", teamPat}} {
		if err := pattern(c.k, d[c.k], c.p); err != nil {
			return err
		}
	}
	parts, err := object("participants", d["participants"])
	if err != nil {
		return err
	}
	if err := members("participants", parts, []string{RoleInitiator, RoleRespondent}, nil); err != nil {
		return err
	}
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		k, _ := parts[role].(string)
		raw, derr := base64.RawURLEncoding.DecodeString(k)
		if !keyPattern.MatchString(k) || derr != nil || len(raw) != 32 {
			return fmt.Errorf("participants.%s must be an identity key", role)
		}
	}
	if err := checkProblem(d["problem"], schema); err != nil {
		return err
	}
	rmax, err := intIn("rounds_max", d["rounds_max"], 1, 5)
	if err != nil {
		return err
	}
	pos, err := object("positions", d["positions"])
	if err != nil {
		return err
	}
	if err := members("positions", pos, []string{RoleInitiator, RoleRespondent}, nil); err != nil {
		return err
	}
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		p, err := object("positions."+role, pos[role])
		if err != nil {
			return err
		}
		if err := members("positions."+role, p, []string{"initial"}, []string{"final"}); err != nil {
			return err
		}
		for k, v := range p {
			if err := entry(schema, "positions."+role+"."+k, kindPosition, v); err != nil {
				return err
			}
		}
	}
	if rv, ok := d["rounds"]; ok {
		rounds, err := array("rounds", rv)
		if err != nil {
			return err
		}
		if int64(len(rounds)) > rmax {
			return errors.New("rounds has more rounds than rounds_max")
		}
		for i, r := range rounds {
			path := fmt.Sprintf("rounds[%d]", i)
			ro, err := object(path, r)
			if err != nil {
				return err
			}
			if err := members(path, ro, []string{"n"}, []string{RoleInitiator, RoleRespondent}); err != nil {
				return err
			}
			if len(ro) < 2 {
				return fmt.Errorf("%s has no move", path)
			}
			if _, err := intIn(path+".n", ro["n"], 1, 5); err != nil {
				return err
			}
			for _, role := range []string{RoleInitiator, RoleRespondent} {
				if mv, ok := ro[role]; ok {
					if err := entry(schema, path+"."+role, kindMove, mv); err != nil {
						return err
					}
				}
			}
		}
	}
	if cv, ok := d["converge"]; ok {
		c, err := object("converge", cv)
		if err != nil {
			return err
		}
		if err := members("converge", c, []string{"proposal"}, []string{"answer"}); err != nil {
			return err
		}
		if err := entry(schema, "converge.proposal", kindProposal, c["proposal"]); err != nil {
			return err
		}
		if a, ok := c["answer"]; ok {
			if err := entry(schema, "converge.answer", kindAnswer, a); err != nil {
				return err
			}
		}
	}
	if fa, ok := d["final_agreement"]; ok {
		if err := entry(schema, "final_agreement", kindProposal, map[string]any{"agreement": fa}); err != nil {
			return err
		}
	}
	if rv, ok := d["remaining_disagreement"]; ok {
		list, err := array("remaining_disagreement", rv)
		if err != nil {
			return err
		}
		for i, x := range list {
			w := map[string]any{"accept": false, "remaining_disagreement": []any{x}}
			if err := entry(schema, fmt.Sprintf("remaining_disagreement[%d]", i), kindAnswer, w); err != nil {
				return err
			}
		}
	}
	if av, ok := d["affected_artifacts"]; ok {
		list, err := array("affected_artifacts", av)
		if err != nil {
			return err
		}
		w := map[string]any{"agreement": map[string]any{"decision": "x"}, "affected_artifacts": list}
		if err := entry(schema, "affected_artifacts", kindProposal, w); err != nil {
			return err
		}
	}
	if hv, ok := d["human_decisions"]; ok {
		list, err := array("human_decisions", hv)
		if err != nil {
			return err
		}
		for i, x := range list {
			path := fmt.Sprintf("human_decisions[%d]", i)
			h, err := object(path, x)
			if err != nil {
				return err
			}
			if err := members(path, h, []string{"id", "by", "at", "text"}, nil); err != nil {
				return err
			}
			if err := pattern(path+".id", h["id"], constraintPat); err != nil {
				return err
			}
			if by, _ := h["by"].(string); by != RoleInitiator && by != RoleRespondent {
				return fmt.Errorf("%s.by must be initiator or respondent", path)
			}
			if _, err := wireTime(path+".at", h["at"]); err != nil {
				return err
			}
			text, ok := h["text"].(string)
			if !ok || schema.Constraint == nil {
				return fmt.Errorf("%s.text must be a string", path)
			}
			if err := schema.Constraint(text); err != nil {
				return fmt.Errorf("%s.text: %w", path, err)
			}
		}
	}
	outcome, _ := d["outcome"].(string)
	reason, _ := d["reason"].(string)
	// Each is checked against its enum here; whether they fit the transcript
	// (rule 6) is step 5.
	if outcome != OutcomeAgreed && outcome != OutcomeEscalated {
		return errors.New("outcome must be agreed or escalated")
	}
	if reason != ReasonAccepted && reason != ReasonRejected && reason != ReasonTimeout {
		return errors.New("reason must be accepted, rejected or timeout")
	}
	for _, k := range []string{"opened", "closed"} {
		if _, err := wireTime(k, d[k]); err != nil {
			return err
		}
	}
	return nil
}

func checkProblem(v any, schema Schema) error {
	p, err := object("problem", v)
	if err != nil {
		return err
	}
	if err := members("problem", p, []string{"title", "topic"}, []string{"context"}); err != nil {
		return err
	}
	title, ok1 := p["title"].(string)
	topic, ok2 := p["topic"].(string)
	if !ok1 || !ok2 || schema.Title == nil || schema.Topic == nil {
		return errors.New("problem.title and problem.topic must be strings")
	}
	if err := schema.Title(title); err != nil {
		return fmt.Errorf("problem.title: %w", err)
	}
	if err := schema.Topic(topic); err != nil {
		return fmt.Errorf("problem.topic: %w", err)
	}
	cv, ok := p["context"]
	if !ok {
		return nil
	}
	list, err := array("problem.context", cv)
	if err != nil {
		return err
	}
	for i, x := range list {
		path := fmt.Sprintf("problem.context[%d]", i)
		c, err := object(path, x)
		if err != nil {
			return err
		}
		if err := members(path, c, []string{"name", "bytes", "sha256"}, nil); err != nil {
			return err
		}
		name, ok := c["name"].(string)
		if !ok || name == "" || len(name) > 255 || !utf8.ValidString(name) {
			return fmt.Errorf("%s.name must be a file name", path)
		}
		for _, r := range name {
			if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || (r >= 0x80 && r <= 0x9f) {
				return fmt.Errorf("%s.name must be a base name without / \\ or control characters", path)
			}
		}
		if _, err := intIn(path+".bytes", c["bytes"], 1, 1<<20); err != nil {
			return err
		}
		if err := pattern(path+".sha256", c["sha256"], hex64); err != nil {
			return err
		}
	}
	return nil
}

// checkInvariants is step 5 (review 43 L12).
func checkInvariants(d map[string]any) error {
	var proposal, answer map[string]any
	if c, ok := d["converge"].(map[string]any); ok {
		proposal, _ = c["proposal"].(map[string]any)
		answer, _ = c["answer"].(map[string]any)
	}
	accept := false
	if answer != nil {
		accept, _ = answer["accept"].(bool)
	}
	outcome, _ := d["outcome"].(string)
	reason, _ := d["reason"].(string)
	if o, r := Expected(answer != nil, accept); o != outcome || r != reason {
		return fmt.Errorf("outcome %s/%s does not follow from converge.answer (rule 6)", outcome, reason)
	}
	fa, hasFA := d["final_agreement"]
	if (outcome == OutcomeAgreed) != hasFA {
		return errors.New("final_agreement must be present iff the outcome is agreed")
	}
	if hasFA && (proposal == nil || !canonicalEqual(fa, proposal["agreement"])) {
		return errors.New("final_agreement differs from converge.proposal.agreement")
	}
	var wantRD []any
	if proposal != nil {
		var err error
		if wantRD, err = mergeDisagreement(proposal, answer); err != nil {
			return err
		}
	}
	gotRD, hasRD := d["remaining_disagreement"]
	if hasRD != (len(wantRD) > 0) || (hasRD && !canonicalEqual(gotRD, wantRD)) {
		return errors.New("remaining_disagreement is not the proposal's followed by the answer's (rule 8)")
	}
	var wantAA any
	if proposal != nil {
		wantAA = proposal["affected_artifacts"]
	}
	gotAA, hasAA := d["affected_artifacts"]
	if hasAA != (wantAA != nil) || (hasAA && !canonicalEqual(gotAA, wantAA)) {
		return errors.New("affected_artifacts differs from the proposal's")
	}
	finals := map[string]any{}
	rounds, _ := d["rounds"].([]any)
	for i, r := range rounds {
		ro, _ := r.(map[string]any)
		n, _ := intIn("n", ro["n"], 1, 5)
		if n != int64(i+1) {
			return fmt.Errorf("rounds[%d].n must be %d", i, i+1)
		}
		for _, role := range []string{RoleInitiator, RoleRespondent} {
			if mv, ok := ro[role].(map[string]any); ok {
				if rev, ok := mv["revision"]; ok {
					finals[role] = rev
				}
			}
		}
	}
	pos, _ := d["positions"].(map[string]any)
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		p, _ := pos[role].(map[string]any)
		got, has := p["final"]
		want, wantHas := finals[role]
		if has != wantHas || (has && !canonicalEqual(got, want)) {
			return fmt.Errorf("positions.%s.final is not the last revision in rounds", role)
		}
	}
	parts, _ := d["participants"].(map[string]any)
	if parts[RoleInitiator] == parts[RoleRespondent] {
		return errors.New("participants.initiator equals participants.respondent")
	}
	opened, _ := wireTime("opened", d["opened"])
	closed, _ := wireTime("closed", d["closed"])
	if closed.Before(opened) {
		return errors.New("closed is before opened")
	}
	return nil
}
