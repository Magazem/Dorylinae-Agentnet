package capability

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Wire constants from Docs/protocol/grant.md.
const (
	// domain is prepended to the canonical grant before signing.
	domain = "dorylinae-grant-v1\n"
	// MaxTokenBytes is the largest wire-form token: canonical({"grant":…,"sig":…}).
	MaxTokenBytes = 2048

	// MinExpiry and MaxExpiry bound exp relative to nbf.
	MinExpiry = time.Minute
	MaxExpiry = 7 * 24 * time.Hour

	timeFmt = "2006-01-02T15:04:05Z"
)

// Action values (Docs/protocol/grant.md §Grant object).
const (
	ActionFSRead  = "fs.read"
	ActionGitRead = "git.read"
)

// Resource kinds.
const (
	KindFS  = "fs"
	KindGit = "git"
)

var (
	b64u = base64.RawURLEncoding.Strict() // rejects non-canonical trailing bits

	idPattern      = regexp.MustCompile(`^g-[0-9a-f]{32}$`)
	keyPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	sessionPattern = regexp.MustCompile(`^s-[0-9a-f]{32}$`)
	labelPattern   = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

	// branchBad holds the characters Docs/protocol/request.md §Artifacts
	// disallows in a branch name, beyond control characters and spaces.
	branchBad = "~^:?*[\\"
)

// Resource identifies the resource a grant covers.
type Resource struct {
	Kind   string // "fs" or "git"
	Label  string
	Branch string // only for KindGit
}

// Grant is the signed body of a capability token (Docs/protocol/grant.md
// §Grant object).
type Grant struct {
	V         int
	ID        string // "g-" + 32 lowercase hex
	Iss       string // grantor identity key, wire form
	Aud       string // holder identity key, wire form
	Session   string // "s-" + 32 lowercase hex
	Action    string
	Resource  Resource
	Scope     string // "" means absent (the whole resource)
	Nbf       time.Time
	Exp       time.Time
	Sensitive bool
}

// Token is the wire object: {"grant": …, "sig": …}.
type Token struct {
	Grant Grant
	Sig   string // base64url, no padding, 64 bytes
}

// NewID returns a fresh grant id: "g-" and 32 lowercase hex characters.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "g-" + hex.EncodeToString(b)
}

// grantMap builds the generic canonical form of g (Docs/protocol/grant.md
// §Grant object), suitable for agentcard.CanonicalValue.
func grantMap(g Grant) map[string]any {
	m := map[string]any{
		"v":         json.Number("1"),
		"id":        g.ID,
		"iss":       g.Iss,
		"aud":       g.Aud,
		"session":   g.Session,
		"action":    g.Action,
		"resource":  resourceMap(g.Resource),
		"nbf":       g.Nbf.UTC().Truncate(time.Second).Format(timeFmt),
		"exp":       g.Exp.UTC().Truncate(time.Second).Format(timeFmt),
		"sensitive": g.Sensitive,
	}
	if g.Scope != "" {
		m["scope"] = g.Scope
	}
	return m
}

func resourceMap(r Resource) map[string]any {
	m := map[string]any{"kind": r.Kind, "label": r.Label}
	if r.Kind == KindGit {
		m["branch"] = r.Branch
	}
	return m
}

// Canonical returns the wire form of t: canonical({"grant": …, "sig": …}).
func Canonical(t Token) ([]byte, error) {
	return agentcard.CanonicalValue(map[string]any{"grant": grantMap(t.Grant), "sig": t.Sig})
}

// Sign validates g, signs it with priv (whose public half must match g.Iss)
// and returns the token. Ed25519 is deterministic, so the result is
// reproducible for fixed inputs.
func Sign(priv ed25519.PrivateKey, g Grant) (Token, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Token{}, errors.New("capability: bad private key length")
	}
	if err := checkFormats(g); err != nil {
		return Token{}, fmt.Errorf("capability: %w", err)
	}
	if b64u.EncodeToString(priv.Public().(ed25519.PublicKey)) != g.Iss {
		return Token{}, errors.New("capability: grant.iss does not match the signing key")
	}
	canon, err := agentcard.CanonicalValue(grantMap(g))
	if err != nil {
		return Token{}, fmt.Errorf("capability: canonicalize grant: %w", err)
	}
	sig := ed25519.Sign(priv, append([]byte(domain), canon...))
	tok := Token{Grant: g, Sig: b64u.EncodeToString(sig)}
	wire, err := Canonical(tok)
	if err != nil {
		return Token{}, fmt.Errorf("capability: canonicalize token: %w", err)
	}
	if len(wire) > MaxTokenBytes {
		return Token{}, fmt.Errorf("capability: token is %d bytes, over the %d limit", len(wire), MaxTokenBytes)
	}
	return tok, nil
}

// checkFormats validates every format rule of Docs/protocol/grant.md §Grant
// object and §Verification step 2, given a structurally decoded Grant (exact
// members and JSON types already checked by decodeGrant). It is shared by
// Sign (so a caller cannot sign a malformed grant) and Verify's step 2.
func checkFormats(g Grant) error {
	if g.V != 1 {
		return errors.New("v must be 1")
	}
	if !idPattern.MatchString(g.ID) {
		return errors.New("id must be \"g-\" followed by 32 lowercase hex characters")
	}
	if !validKey(g.Iss) {
		return errors.New("iss must be a 43-character base64url identity key")
	}
	if !validKey(g.Aud) {
		return errors.New("aud must be a 43-character base64url identity key")
	}
	if g.Iss == g.Aud {
		return errors.New("aud must differ from iss")
	}
	if !sessionPattern.MatchString(g.Session) {
		return errors.New("session must be \"s-\" followed by 32 lowercase hex characters")
	}
	switch g.Action {
	case ActionFSRead, ActionGitRead:
	default:
		return errors.New("action must be fs.read or git.read")
	}
	if !labelPattern.MatchString(g.Resource.Label) {
		return errors.New("resource.label must be 1-64 characters from [a-z0-9._-]")
	}
	switch g.Resource.Kind {
	case KindFS:
		if g.Resource.Branch != "" {
			return errors.New("resource.branch must be absent for kind fs")
		}
	case KindGit:
		if err := checkBranch(g.Resource.Branch); err != nil {
			return err
		}
	default:
		return errors.New("resource.kind must be fs or git")
	}
	if g.Scope != "" && !ValidScopePath(g.Scope) {
		return errors.New("scope is not a valid path")
	}
	if g.Nbf.IsZero() {
		return errors.New("nbf is required")
	}
	if g.Exp.Before(g.Nbf.Add(MinExpiry)) || g.Exp.After(g.Nbf.Add(MaxExpiry)) {
		return errors.New("exp must be 1 minute to 7 days after nbf")
	}
	return nil
}

func validKey(s string) bool {
	if !keyPattern.MatchString(s) {
		return false
	}
	k, err := b64u.DecodeString(s)
	return err == nil && len(k) == ed25519.PublicKeySize
}

func checkBranch(s string) error {
	if s == "" {
		return errors.New("resource.branch is required for kind git")
	}
	if !utf8.ValidString(s) || len(s) < 1 || len(s) > 255 {
		return errors.New("resource.branch must be 1-255 bytes of UTF-8")
	}
	if strings.Contains(s, " ") || hasControl(s) || strings.ContainsAny(s, branchBad) {
		return errors.New("resource.branch must not contain spaces, control characters or ~ ^ : ? * [ \\")
	}
	if strings.Contains(s, "..") {
		return errors.New(`resource.branch must not contain ".."`)
	}
	return nil
}

// hasControl reports whether s has a control character (U+0000-U+001F,
// U+007F) or invalid UTF-8.
func hasControl(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError || r <= 0x1F || r == 0x7F {
			return true
		}
	}
	return false
}

// ValidScopePath reports whether s is a valid grant scope or fetch path
// (Docs/protocol/grant.md §Paths): "" (the resource root) or a relative,
// slash-separated path of 1-1024 UTF-8 bytes with no ".", ".." or empty
// segment, no leading/trailing slash, no backslash or colon, no control
// character, and no segment that is a Windows reserved name or ends in "."
// or a space.
func ValidScopePath(s string) bool {
	if !utf8.ValidString(s) || len(s) > 1024 {
		return false
	}
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return false
	}
	if strings.ContainsAny(s, `\:`) || hasControl(s) {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if !validSegment(seg) {
			return false
		}
	}
	return true
}

func validSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return false
	}
	return !isWindowsReserved(seg)
}

func isWindowsReserved(seg string) bool {
	name := seg
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		name = seg[:i]
	}
	name = strings.ToUpper(name)
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
		return true
	}
	return false
}

// parseTime parses a "time"-typed member: a JSON string, RFC 3339 UTC with Z
// and whole seconds, round-tripping exactly (rejects a fractional second
// time.Parse would otherwise accept).
func parseTime(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.New("must be a string")
	}
	t, err := time.Parse(timeFmt, s)
	if err != nil || t.Format(timeFmt) != s {
		return time.Time{}, errors.New("must be RFC 3339 UTC with Z and whole seconds")
	}
	return t, nil
}
