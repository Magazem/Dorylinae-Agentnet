package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Chain constants (Docs/protocol/audit.md §The chain).
const (
	genesisLabel = "dorylinae-audit-genesis-v1"
	rowLabel     = "dorylinae-audit-v1\n"
)

// verifyPage is the most rows one read of Verify holds the connection for
// (review 43 M8).
const verifyPage = 2000

// row is one audit_events row as stored.
type row struct {
	id                        int64
	ts, actor, action, detail string
	hash                      *string // nil = NULL
}

func genesis() []byte {
	g := sha256.Sum256([]byte(genesisLabel))
	return g[:]
}

// rowCanonical is row_c(r): canonical JSON of the stored values, detail as the
// stored text (a string), not re-parsed.
func rowCanonical(r row) ([]byte, error) {
	return agentcard.CanonicalValue(map[string]any{
		"action": r.action,
		"actor":  r.actor,
		"detail": r.detail,
		"id":     json.Number(strconv.FormatInt(r.id, 10)),
		"ts":     r.ts,
	})
}

// chainHash is SHA-256(rowLabel ‖ prev (32 raw bytes) ‖ row_c(r)).
func chainHash(prev []byte, r row) ([]byte, error) {
	c, err := rowCanonical(r)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte(rowLabel))
	h.Write(prev)
	h.Write(c)
	return h.Sum(nil), nil
}

func encodeHash(h []byte) string { return hex.EncodeToString(h) }

func decodeHash(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size || encodeHash(b) != s {
		return nil, fmt.Errorf("malformed hash %q", s)
	}
	return b, nil
}

// Anchor is a head recorded earlier, checked with Verify (audit.md §Anchors).
type Anchor struct {
	ID   int64
	Hash string
}

// ParseAnchor parses "ID:HASH".
func ParseAnchor(s string) (Anchor, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return Anchor{}, fmt.Errorf("%w: anchor %q is not ID:HASH", ErrBadAnchor, s)
	}
	id, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil || id <= 0 {
		return Anchor{}, fmt.Errorf("%w: anchor id %q", ErrBadAnchor, s[:i])
	}
	if _, err := decodeHash(s[i+1:]); err != nil {
		return Anchor{}, fmt.Errorf("%w: anchor %d: %w", ErrBadAnchor, id, err)
	}
	return Anchor{ID: id, Hash: s[i+1:]}, nil
}

// ErrBadAnchor marks an anchor the caller got wrong (IPC bad_request): not
// ID:HASH, or an ID that is a legacy row, which has no stored hash.
var ErrBadAnchor = errors.New("audit: bad anchor")

// Verification failure reasons (audit.md §Verification).
const (
	ReasonGap            = "gap"
	ReasonUnchained      = "unchained"
	ReasonChainStart     = "chain_start"
	ReasonHashMismatch   = "hash_mismatch"
	ReasonMalformed      = "malformed"
	ReasonAnchorMismatch = "anchor_mismatch"
	ReasonAnchorMissing  = "anchor_missing"
)

// Verify statuses.
const (
	StatusOK     = "ok"
	StatusBroken = "broken"
)

// Head is the newest row a Verify run read.
type Head struct {
	ID   int64  `json:"id"`
	Hash string `json:"hash"` // "" for a legacy (unchained) head
	TS   string `json:"ts"`
}

// VerifyResult is the "verify" object of audit_verify.
type VerifyResult struct {
	Status      string `json:"status"`
	Rows        int64  `json:"rows"`
	LegacyRows  int64  `json:"legacy_rows"`
	ChainedFrom int64  `json:"chained_from"`
	Head        *Head  `json:"head,omitempty"`
	FirstBad    int64  `json:"first_bad,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// verifyState carries the walk across pages.
type verifyState struct {
	res     VerifyResult
	prev    []byte
	lastID  int64 // previous row's id
	started bool  // audit.chain_start seen
	anchors map[int64][]string
	seen    map[int64]bool
	// legacyAnchor is the first anchored row the walk found without a
	// stored hash; Verify decides at the end whether that is the caller's
	// mistake or tampering.
	legacyAnchor int64
}

func (s *verifyState) fail(id int64, reason string) {
	s.res.Status, s.res.FirstBad, s.res.Reason = StatusBroken, id, reason
}

// afterPage, when set by tests, runs after each page's read has released the
// connection.
var afterPage func()

// Verify walks every row up to the current head in id order and reports the
// first failure. It reads the head once and then pages of at most verifyPage
// rows, each a short read that releases the daemon's single connection
// before the next, so other work proceeds while a large log is checked.
// Rows appended during the walk are after Head and not checked in this run.
//
// An error is returned only for a failed read or a bad anchor (ErrBadAnchor);
// tampering is a result with Status "broken". An anchor on a row without a
// stored hash is ErrBadAnchor only when the walk is otherwise clean and the
// row lies before audit.chain_start: a NULL hash after the chain start is
// "unchained", and one in a log with no chain start at all is
// "anchor_mismatch" (an anchor's hash only ever comes from a stored row), so
// nulling hashes cannot turn tampering into a caller error (review 44 M1).
func (l *Log) Verify(ctx context.Context, anchors ...Anchor) (*VerifyResult, error) {
	st := &verifyState{prev: genesis(), anchors: map[int64][]string{}, seen: map[int64]bool{}}
	st.res.Status = StatusOK
	for _, a := range anchors {
		st.anchors[a.ID] = append(st.anchors[a.ID], a.Hash)
	}

	var head Head
	var headHash *string
	err := l.db.QueryRowContext(ctx, `SELECT id, ts, hash FROM audit_events ORDER BY id DESC LIMIT 1`).
		Scan(&head.ID, &head.TS, &headHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		st.checkAnchorsMissing()
		return &st.res, nil
	case err != nil:
		return nil, fmt.Errorf("audit: verify: read head: %w", err)
	}
	if headHash != nil {
		head.Hash = *headHash
	}
	st.res.Head = &head

	after := int64(-1 << 63)
	for {
		page, err := l.readPage(ctx, after, head.ID)
		if err != nil {
			return nil, err
		}
		if afterPage != nil {
			afterPage()
		}
		for _, r := range page {
			if !st.check(r) {
				return &st.res, nil
			}
		}
		if len(page) < verifyPage {
			break
		}
		after = page[len(page)-1].id
	}
	st.checkAnchorsMissing()
	if st.res.Status == StatusOK && st.legacyAnchor != 0 {
		if st.started {
			return nil, fmt.Errorf("%w: row %d is a legacy row without a stored hash", ErrBadAnchor, st.legacyAnchor)
		}
		st.fail(st.legacyAnchor, ReasonAnchorMismatch)
	}
	return &st.res, nil
}

// readPage reads the rows with after < id <= head, at most verifyPage of them,
// and closes the query before returning.
func (l *Log) readPage(ctx context.Context, after, head int64) ([]row, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, ts, actor, action, detail, hash FROM audit_events
		WHERE id > ? AND id <= ? ORDER BY id LIMIT ?`, after, head, verifyPage)
	if err != nil {
		return nil, fmt.Errorf("audit: verify: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]row, 0, verifyPage)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ts, &r.actor, &r.action, &r.detail, &r.hash); err != nil {
			return nil, fmt.Errorf("audit: verify: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: verify: %w", err)
	}
	return out, nil
}

// check applies the checks of audit.md §Verification to one row in the order
// of its table; it returns false at the first failure.
func (s *verifyState) check(r row) bool {
	s.res.Rows++
	defer func() { s.lastID = r.id }()

	if !s.started {
		if r.hash == nil { // legacy row, chained virtually; gaps allowed
			if !wellFormed(r) {
				s.fail(r.id, ReasonMalformed)
				return false
			}
			h, err := chainHash(s.prev, r)
			if err != nil {
				s.fail(r.id, ReasonMalformed)
				return false
			}
			s.prev = h
			s.res.LegacyRows++
			if _, ok := s.anchors[r.id]; ok {
				s.seen[r.id] = true
				if s.legacyAnchor == 0 {
					s.legacyAnchor = r.id
				}
			}
			return true
		}
		if !s.isChainStart(r) {
			s.fail(r.id, ReasonChainStart)
			return false
		}
		s.started = true
		s.res.ChainedFrom = r.id
	} else {
		if r.id != s.lastID+1 {
			s.fail(r.id, ReasonGap)
			return false
		}
		if r.hash == nil {
			s.fail(r.id, ReasonUnchained)
			return false
		}
	}
	h, err := chainHash(s.prev, r)
	if err != nil || encodeHash(h) != *r.hash {
		s.fail(r.id, ReasonHashMismatch)
		return false
	}
	if !wellFormed(r) {
		s.fail(r.id, ReasonMalformed)
		return false
	}
	s.prev = h
	if wants, ok := s.anchors[r.id]; ok {
		s.seen[r.id] = true
		for _, want := range wants {
			if want != *r.hash {
				s.fail(r.id, ReasonAnchorMismatch)
				return false
			}
		}
	}
	return true
}

// isChainStart checks the first stored row against the legacy rows before it.
func (s *verifyState) isChainStart(r row) bool {
	if r.action != ActionChainStart || r.actor != ActorDaemon {
		return false
	}
	var d chainStartDetail
	dec := json.NewDecoder(bytes.NewReader([]byte(r.detail)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return false
	}
	lastLegacy := int64(0)
	if s.res.LegacyRows > 0 {
		lastLegacy = s.lastID
	}
	return d.LegacyRows == s.res.LegacyRows && d.LegacyLastID == lastLegacy
}

func (s *verifyState) checkAnchorsMissing() {
	if s.res.Status != StatusOK {
		return
	}
	var missing int64
	for id := range s.anchors {
		if !s.seen[id] && (missing == 0 || id < missing) {
			missing = id
		}
	}
	if missing != 0 {
		s.fail(missing, ReasonAnchorMissing)
	}
}

// wellFormed: detail is valid JSON, ts parses, and every text field is valid
// UTF-8 (canonical JSON would otherwise fold distinct invalid bytes together).
func wellFormed(r row) bool {
	if !json.Valid([]byte(r.detail)) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, r.ts); err != nil {
		return false
	}
	return utf8.ValidString(r.ts) && utf8.ValidString(r.actor) && utf8.ValidString(r.action) && utf8.ValidString(r.detail)
}
