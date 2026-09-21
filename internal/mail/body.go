package mail

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Step 12 body checks for the kinds this package understands.

const maxIDList = 256

var errBadAnnouncement = errors.New("bad mailbox announcement")

// checkIDList validates an optional 1-256 element list of mail ids.
func checkIDList(name string, v any) error {
	list, ok := v.([]any)
	if !ok || len(list) < 1 || len(list) > maxIDList {
		return fmt.Errorf("%s must hold 1-%d ids", name, maxIDList)
	}
	for _, e := range list {
		if s, ok := e.(string); !ok || !ValidID(s) {
			return fmt.Errorf("%s holds a malformed id", name)
		}
	}
	return nil
}

// checkAckBody: only ids and unsupported, at least one present.
func checkAckBody(body map[string]any) error {
	if len(body) == 0 {
		return errors.New("ack needs ids or unsupported")
	}
	for k, v := range body {
		if k != "ids" && k != "unsupported" {
			return fmt.Errorf("ack has unknown member %q", k)
		}
		if err := checkIDList(k, v); err != nil {
			return err
		}
	}
	return nil
}

// checkKeysBody: announcement (required, verified) and retry (optional).
func checkKeysBody(body map[string]any, from string, now time.Time) error {
	for k := range body {
		if k != "announcement" && k != "retry" {
			return fmt.Errorf("keys has unknown member %q", k)
		}
	}
	if v, ok := body["retry"]; ok {
		if err := checkIDList("retry", v); err != nil {
			return err
		}
	}
	ann, ok := body["announcement"]
	if !ok {
		return errors.New("keys needs an announcement")
	}
	if err := verifyAnnouncement(ann, from, now); err != nil {
		return fmt.Errorf("%w: %v", errBadAnnouncement, err)
	}
	return nil
}

// verifyAnnouncement runs the checks of Docs/protocol/mail.md §Announcement on
// a generically parsed signed announcement whose identity must be identity.
func verifyAnnouncement(v any, identity string, now time.Time) error {
	top, ok := v.(map[string]any)
	if !ok || len(top) != 2 {
		return errors.New("must be an object with exactly announcement and signature")
	}
	ann, ok := top["announcement"].(map[string]any)
	if !ok {
		return errors.New("missing announcement object")
	}
	sigStr, ok := top["signature"].(string)
	if !ok {
		return errors.New("missing signature")
	}
	if got, _ := ann["identity"].(string); got != identity {
		return errors.New("identity is not the sender")
	}
	idKey, err := decodeKey(identity)
	if err != nil {
		return err
	}
	sig, err := b64u.DecodeString(sigStr)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("signature must be 64 bytes")
	}
	canon, err := canonical(ann)
	if err != nil {
		return err
	}
	if !ed25519.Verify(idKey, append([]byte(annTag), canon...), sig) {
		return errors.New("signature does not verify")
	}

	if len(ann) != 6 {
		return errors.New("announcement must have exactly created, identity, key_id, not_after, pub, v")
	}
	if n, ok := ann["v"].(json.Number); !ok || n.String() != "1" {
		return errors.New("v must be 1")
	}
	pubStr, _ := ann["pub"].(string)
	pub, err := b64u.DecodeString(pubStr)
	if err != nil || len(pub) != encLen {
		return errors.New("pub must be 32 bytes, base64url without padding")
	}
	if kid, _ := ann["key_id"].(string); kid != KeyIDOf(pub).String() {
		return errors.New("key_id does not match pub")
	}
	created, err := parseTime(ann["created"])
	if err != nil {
		return fmt.Errorf("created: %w", err)
	}
	notAfter, err := parseTime(ann["not_after"])
	if err != nil {
		return fmt.Errorf("not_after: %w", err)
	}
	if created.After(now.Add(MaxSkew)) || !created.Before(notAfter) ||
		notAfter.After(created.Add(30*24*time.Hour)) || !notAfter.After(now) {
		return errors.New("validity window is out of range")
	}
	return nil
}

func parseTime(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.New("must be a string")
	}
	// time.Parse accepts a fractional second even though the layout has none,
	// so require the string to round-trip exactly.
	t, err := time.Parse(timeFmt, s)
	if err != nil || t.Format(timeFmt) != s {
		return time.Time{}, errors.New("must be RFC 3339 UTC with Z and whole seconds")
	}
	return t, nil
}
