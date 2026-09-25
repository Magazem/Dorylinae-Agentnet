package capability

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Reject reasons, see Docs/protocol/grant.md §Verification.
const (
	ReasonMalformed      = "malformed"
	ReasonWrongIssuer    = "wrong_issuer"
	ReasonBadSignature   = "bad_signature"
	ReasonWrongAudience  = "wrong_audience"
	ReasonNotYetValid    = "not_yet_valid"
	ReasonExpired        = "expired"
	ReasonUnknownSession = "unknown_session"
	ReasonSessionNotOpen = "session_not_open"
)

// VerifyError is returned by Verify when a step fails. Reason is the wire
// error code; Step is the number in Docs/protocol/grant.md §Verification.
// GrantID is set only when the failure comes after step 4 (signature), so the
// id is trustworthy (review 28 L9, grant.orphan audits {grant, peer}).
type VerifyError struct {
	Reason  string
	Step    int
	Err     error
	GrantID string
}

func (e *VerifyError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("capability: rejected at step %d (%s): %v", e.Step, e.Reason, e.Err)
	}
	return fmt.Sprintf("capability: rejected at step %d (%s)", e.Step, e.Reason)
}

func (e *VerifyError) Unwrap() error { return e.Err }

// ReasonOf returns the reject reason carried by err, or "" if it is not a
// *VerifyError.
func ReasonOf(err error) string {
	var ve *VerifyError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return ""
}

// GrantIDOf returns the trustworthy grant id carried by err (a *VerifyError
// rejected after step 4), or "" if none.
func GrantIDOf(err error) string {
	var ve *VerifyError
	if errors.As(err, &ve) {
		return ve.GrantID
	}
	return ""
}

func reject(step int, reason string, err error) error {
	return &VerifyError{Reason: reason, Step: step, Err: err}
}

// Role distinguishes which party is running Verify: the check for the
// expected issuer (step 3) and for the audience (step 5) each read one field
// from Self and one from Counterparty, in opposite order.
type Role int

const (
	// RoleHolder verifies as the token's audience: Counterparty is the
	// expected issuer (the carrying grant mail's msg.from, or a previously
	// stored grant's iss); Self must equal the grant's aud.
	RoleHolder Role = iota
	// RoleGrantor verifies as the token's issuer: Self must equal the
	// grant's iss; Counterparty is the identity the Noise session
	// authenticated for this fetch, and must equal the grant's aud.
	RoleGrantor
)

// SessionOpen reports whether id is a known work session between requester
// and worker (identity keys in wire form) and, if so, whether it is open.
// Verify does not depend on internal/worksession: the caller (the holder's
// mirror lookup, or the grantor's own store) supplies this. It is required:
// a nil SessionOpen fails step 7 (unknown_session), so a caller that forgets
// to wire it fails closed.
type SessionOpen func(id, requester, worker string) (known, open bool)

// VerifyParams parameterises Verify (Docs/protocol/grant.md §Verification).
type VerifyParams struct {
	Role         Role
	Self         string // this party's own identity key, wire form
	Counterparty string // see Role
	Now          time.Time
	SessionOpen  SessionOpen
}

// Verify runs steps 1-8 of Docs/protocol/grant.md §Verification against the
// wire-form token raw. The first failure is returned as a *VerifyError; on
// success it returns the verified grant. Verify does not run steps 9-10 (the
// grantor's stored-token and scope checks): those need the grants store
// (ticket 2.2c) and are layered on top of Verify's result.
func Verify(raw []byte, p VerifyParams) (*Grant, error) {
	// Step 1: strict parse; grant has exactly the members of §Grant object
	// with their JSON types; optional scope absent rather than empty.
	if len(raw) > MaxTokenBytes {
		return nil, reject(1, ReasonMalformed, fmt.Errorf("token is %d bytes, over the %d limit", len(raw), MaxTokenBytes))
	}
	gen, tok, err := parseToken(raw)
	if err != nil {
		return nil, reject(1, ReasonMalformed, err)
	}
	g := tok.Grant

	// Step 2: v, id, keys, session, label, branch, scope, action formats.
	if err := checkFormats(g); err != nil {
		return nil, reject(2, ReasonMalformed, err)
	}

	// Step 3: iss is the expected grantor.
	var expectedIss string
	switch p.Role {
	case RoleHolder:
		expectedIss = p.Counterparty
	case RoleGrantor:
		expectedIss = p.Self
	default:
		return nil, reject(3, ReasonWrongIssuer, errors.New("unknown role"))
	}
	if g.Iss != expectedIss {
		return nil, reject(3, ReasonWrongIssuer, nil)
	}

	// Step 4: sig verifies under iss over the domain-prefixed canonical
	// grant, as parsed generically (as for cards).
	issKey, err := b64u.DecodeString(g.Iss)
	if err != nil || len(issKey) != ed25519.PublicKeySize {
		return nil, reject(4, ReasonBadSignature, errors.New("bad issuer key"))
	}
	if !sigPattern.MatchString(tok.Sig) {
		return nil, reject(4, ReasonBadSignature, errors.New("bad signature encoding"))
	}
	sig, err := b64u.DecodeString(tok.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, reject(4, ReasonBadSignature, errors.New("bad signature encoding"))
	}
	canonGrant, err := agentcard.CanonicalValue(gen)
	if err != nil {
		return nil, reject(4, ReasonBadSignature, err)
	}
	if !ed25519.Verify(issKey, append([]byte(domain), canonGrant...), sig) {
		return nil, reject(4, ReasonBadSignature, nil)
	}

	// Step 5: aud.
	expectedAud := p.Self
	if p.Role == RoleGrantor {
		expectedAud = p.Counterparty
	}
	if g.Aud != expectedAud {
		return nil, reject(5, ReasonWrongAudience, nil)
	}

	// Step 6: nbf <= now + 10 min, and now < exp.
	if g.Nbf.After(p.Now.Add(10 * time.Minute)) {
		return nil, reject(6, ReasonNotYetValid, nil)
	}
	if !p.Now.Before(g.Exp) {
		return nil, reject(6, ReasonExpired, nil)
	}

	// Step 7: session names a known work session between iss (requester)
	// and aud (worker), in state open.
	if p.SessionOpen == nil {
		return nil, reject(7, ReasonUnknownSession, errors.New("no session lookup configured"))
	}
	known, open := p.SessionOpen(g.Session, g.Iss, g.Aud)
	if !known {
		return nil, &VerifyError{Reason: ReasonUnknownSession, Step: 7, GrantID: g.ID}
	}
	if !open {
		return nil, reject(7, ReasonSessionNotOpen, nil)
	}

	// Step 8: action matches resource.kind.
	if err := checkActionKind(g); err != nil {
		return nil, reject(8, ReasonMalformed, err)
	}

	verified := g
	return &verified, nil
}

// parseToken performs the structural half of step 1: a strict JSON parse,
// exactly "grant" and "sig" at the top level, and exactly the §Grant object
// members with their JSON types. It returns the grant as parsed generically
// (the form the signature covers, gen) together with the decoded Token.
func parseToken(raw []byte) (gen map[string]any, tok Token, err error) {
	doc, err := agentcard.ParseStrict(raw)
	if err != nil {
		return nil, Token{}, err
	}
	top, ok := doc.(map[string]any)
	if !ok || len(top) != 2 {
		return nil, Token{}, errors.New("token must be an object with exactly grant and sig")
	}
	gen, ok = top["grant"].(map[string]any)
	if !ok {
		return nil, Token{}, errors.New("missing grant object")
	}
	sigStr, ok := top["sig"].(string)
	if !ok {
		return nil, Token{}, errors.New("missing sig")
	}
	g, err := decodeGrant(gen)
	if err != nil {
		return nil, Token{}, err
	}
	return gen, Token{Grant: g, Sig: sigStr}, nil
}

// decodeGrant checks that gen has exactly the §Grant object members (scope
// optionally present, never null or empty) with their JSON types, and
// decodes it. It performs no format validation beyond that (checkFormats
// does): a syntactically wrong-typed or extra/missing member is step 1
// (malformed); a present-but-badly-formatted value is step 2.
func decodeGrant(gen map[string]any) (Grant, error) {
	_, hasScope := gen["scope"]
	want := 10
	if hasScope {
		want = 11
	}
	if len(gen) != want {
		return Grant{}, errors.New("grant must have exactly the members of Docs/protocol/grant.md §Grant object")
	}

	var g Grant
	vNum, ok := gen["v"].(json.Number)
	if !ok {
		return Grant{}, errors.New("v must be an integer")
	}
	vi, err := vNum.Int64()
	if err != nil || int64(int(vi)) != vi { // no wrap-around to 1 on a 32-bit int
		return Grant{}, errors.New("v must be an integer")
	}
	g.V = int(vi)

	strs := map[string]*string{"id": &g.ID, "iss": &g.Iss, "aud": &g.Aud, "session": &g.Session, "action": &g.Action}
	for k, dst := range strs {
		s, ok := gen[k].(string)
		if !ok {
			return Grant{}, fmt.Errorf("%s must be a string", k)
		}
		*dst = s
	}

	if hasScope {
		s, ok := gen["scope"].(string)
		if !ok || s == "" {
			return Grant{}, errors.New("scope must be a non-empty string when present")
		}
		g.Scope = s
	}

	sensitive, ok := gen["sensitive"].(bool)
	if !ok {
		return Grant{}, errors.New("sensitive must be a boolean")
	}
	g.Sensitive = sensitive

	if g.Nbf, err = parseTime(gen["nbf"]); err != nil {
		return Grant{}, fmt.Errorf("nbf: %w", err)
	}
	if g.Exp, err = parseTime(gen["exp"]); err != nil {
		return Grant{}, fmt.Errorf("exp: %w", err)
	}

	res, ok := gen["resource"].(map[string]any)
	if !ok {
		return Grant{}, errors.New("resource must be an object")
	}
	kind, ok := res["kind"].(string)
	if !ok {
		return Grant{}, errors.New("resource.kind must be a string")
	}
	g.Resource.Kind = kind
	switch kind {
	case KindFS:
		if len(res) != 2 {
			return Grant{}, errors.New("resource must have exactly kind and label for kind fs")
		}
		label, ok := res["label"].(string)
		if !ok {
			return Grant{}, errors.New("resource.label must be a string")
		}
		g.Resource.Label = label
	case KindGit:
		if len(res) != 3 {
			return Grant{}, errors.New("resource must have exactly kind, label and branch for kind git")
		}
		label, ok := res["label"].(string)
		if !ok {
			return Grant{}, errors.New("resource.label must be a string")
		}
		branch, ok := res["branch"].(string)
		if !ok {
			return Grant{}, errors.New("resource.branch must be a string")
		}
		g.Resource.Label = label
		g.Resource.Branch = branch
	default:
		return Grant{}, errors.New("resource.kind must be fs or git")
	}
	return g, nil
}
