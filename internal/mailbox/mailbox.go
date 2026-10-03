// Package mailbox holds the daemon's own mailbox keys (Docs/protocol/mail.md
// §Mailbox keys). The private halves live in the keystore, one secret per key.
// The public halves, their signed announcements and their lifecycle live in the
// mailbox_keys_own table (migrations 6 and 26). Keys rotate every 7 days, a retired key
// still decrypts, and its private key is deleted 21 days after creation, with
// never more than 3 keys live.
package mailbox

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

const (
	// Dir is the directory, inside the config dir, of the mailbox key files.
	Dir = "mailbox"
	// legacyFile is the announcement file 0.8c wrote before the table existed.
	legacyFile = "current.json"
	// renewBefore is how long before not_after the current key stops being offered.
	renewBefore = time.Hour

	// RotateAfter is the age at which the current key is replaced.
	RotateAfter = 7 * 24 * time.Hour
	// DeleteGrace is how long after not_after the private key is kept: the
	// 7 d queue TTL of mail sealed to it.
	DeleteGrace = 7 * 24 * time.Hour
	// MaxLive is the most keys whose private half may exist at once.
	MaxLive = 3
	// RunInterval is how often the rotation job runs.
	RunInterval = time.Hour
	// loadRetry is how long after a failed keystore read MailboxKey leaves
	// that key alone (Docs/protocol/mail.md §Mailbox keys lifecycle).
	loadRetry = time.Second

	// ActionRotate is the audit action for a new current key.
	ActionRotate = "mailbox.rotate"

	actorDaemon = "daemon"
	timeFmt     = mail.StoreTimeFmt
)

// AuditSink is the part of audit.Log that Keys needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Keys is the daemon's own mailbox key store.
type Keys struct {
	dir      string
	keychain keystore.KeychainEntries
	mode     string
	identity ed25519.PublicKey
	sign     func(msg []byte) ([]byte, error)
	now      func() time.Time

	mu       sync.Mutex
	db       *sql.DB
	audit    AuditSink
	log      *slog.Logger
	onRotate func(announcement []byte)
	// cache holds the private keys MailboxKey loaded, by key_id, until the
	// key is deleted; failed is the time of the last failed keystore read
	// per key_id (R55-F13, review 55 R55-051). Both are guarded by mu.
	cache  map[string]*ecdh.PrivateKey
	failed map[string]time.Time
	// wrap, if set, wraps every keystore backend (tests count reads).
	wrap func(keystore.Backend) keystore.Backend
}

// New returns the mailbox keys stored under configDir. mode is "auto" (OS
// keychain, then file) or "file" and selects the private key storage like
// DORYLINAE_KEYSTORE does for the identity. sign signs with the identity key
// whose public half is identity. A nil now means time.Now. Attach must be
// called before any other method.
func New(configDir, mode string, identity ed25519.PublicKey, sign func([]byte) ([]byte, error), now func() time.Time) *Keys {
	if now == nil {
		now = time.Now
	}
	return &Keys{
		dir:      filepath.Join(configDir, Dir),
		keychain: keystore.KeychainEntriesFor("mailbox-", configDir),
		mode:     mode,
		identity: identity,
		sign:     sign,
		now:      now,
		log:      slog.Default(),
	}
}

// Attach connects k to the migrated database and imports the current.json that
// 0.8c wrote, once. audit may be nil; log may be nil.
func (k *Keys) Attach(ctx context.Context, db *sql.DB, audit AuditSink, log *slog.Logger) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.db, k.audit = db, audit
	if log != nil {
		k.log = log
	}
	return k.importLegacy(ctx)
}

// OnRotate registers f, called after every newly created current key with its
// canonical signed announcement. Ticket 1.0b's distribution hooks in here. f
// runs without the internal lock but on the caller's goroutine, so it must not
// block for long.
func (k *Keys) OnRotate(f func(announcement []byte)) {
	k.mu.Lock()
	k.onRotate = f
	k.mu.Unlock()
}

func (k *Keys) keystoreFor(keyID string) (*keystore.Store, error) {
	var backends []keystore.Backend
	file := keystore.NewFile(filepath.Join(k.dir, keyID+".key"))
	switch k.mode {
	case "", "auto":
		backends = []keystore.Backend{k.keychain.Entry("-" + keyID), file}
	case "file":
		backends = []keystore.Backend{file}
	default:
		return nil, fmt.Errorf("mailbox: keystore mode must be \"auto\" or \"file\", got %q", k.mode)
	}
	if k.wrap != nil {
		for i, b := range backends {
			backends[i] = k.wrap(b)
		}
	}
	return keystore.New(backends...), nil
}

// row is one mailbox_keys_own row.
type row struct {
	keyID     string
	created   time.Time
	notAfter  time.Time
	retired   bool
	announced []byte
	backend   string // where Save put the private key; "" when not recorded
}

func (k *Keys) errNoDB() error { return errors.New("mailbox: no database attached") }

// live returns the rows whose private key has not been deleted, oldest first.
func (k *Keys) live(ctx context.Context) ([]row, error) {
	if k.db == nil {
		return nil, k.errNoDB()
	}
	rs, err := k.db.QueryContext(ctx, `SELECT key_id, created, not_after, retired IS NOT NULL, announcement, COALESCE(key_backend, '')
FROM mailbox_keys_own WHERE deleted IS NULL ORDER BY created, key_id`)
	if err != nil {
		return nil, fmt.Errorf("mailbox: list keys: %w", err)
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var (
			r        row
			c, n, an string
		)
		if err := rs.Scan(&r.keyID, &c, &n, &r.retired, &an, &r.backend); err != nil {
			return nil, fmt.Errorf("mailbox: scan key: %w", err)
		}
		var e1, e2 error
		r.created, e1 = time.Parse(timeFmt, c)
		r.notAfter, e2 = time.Parse(timeFmt, n)
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("mailbox: key %s has bad times", r.keyID)
		}
		r.announced = []byte(an)
		out = append(out, r)
	}
	return out, rs.Err()
}

// currentOf returns the newest live, not retired row, or nil.
func currentOf(rows []row) *row {
	var cur *row
	for i := range rows {
		if !rows[i].retired && (cur == nil || rows[i].created.After(cur.created)) {
			cur = &rows[i]
		}
	}
	return cur
}

// Announcement returns the canonical signed announcement of the current
// mailbox key. If there is no usable key (none yet, the private key is gone,
// or the announcement expires within the hour) it creates a new one first.
// The private key is in the keystore and the row is in the table before the
// announcement is returned.
func (k *Keys) Announcement() ([]byte, error) {
	ann, hook, err := k.announce(context.Background())
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(ann)
	}
	return ann, nil
}

// announce is Announcement's work under k.mu; hook, if set, is run by the
// caller without the lock. k.mu is unlocked by defer here and in rotate:
// both do crypto and DB work and are reached from IPC handlers, so a
// recovered panic must not leave it held (review 77b).
func (k *Keys) announce(ctx context.Context) ([]byte, func([]byte), error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	rows, err := k.live(ctx)
	if err != nil {
		return nil, nil, err
	}
	if cur := currentOf(rows); cur != nil {
		ok, err := k.usable(cur, now)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			return cur.announced, nil, nil
		}
	}
	return k.createLocked(ctx, now, rows)
}

// usable reports whether r is not about to expire and its private key is in
// the keystore and matches. A keystore failure other than "not found" is an
// error: rotating then would orphan a key that may still be there (review L5).
// An unavailable keystore (a locked keychain) counts as usable: the key is
// most likely still there, and a needless rotation would announce a new key
// to every peer (review 55 R55-092). A key that is really gone is then
// replaced at the latest by the scheduled rotation. A host without a keychain
// service is not "unavailable": the key can only be in the file, so a missing
// file means a lost key (review 87 L2).
//
// A key created after now + mail.MaxSkew is not usable either: every peer
// refuses its announcement (mail.md §Announcement check 5). It was made while
// the clock was stepped forward, so a new key is made at the true time
// (R55-F28, review 55 R55-103).
func (k *Keys) usable(r *row, now time.Time) (bool, error) {
	if !r.notAfter.After(now.Add(renewBefore)) || r.created.After(now.Add(mail.MaxSkew)) {
		return false, nil
	}
	pub, err := k.rowPub(r)
	if err != nil {
		return false, nil
	}
	_, err = k.load(r.keyID, pub)
	switch {
	case errors.Is(err, keystore.ErrNotFound), errors.Is(err, keystore.ErrMismatch):
		return false, nil
	case errors.Is(err, keystore.ErrUnavailable):
		k.log.Warn("mailbox: keystore unavailable, keeping the current key", "event", "mailbox_error", "key_id", r.keyID, "error", err)
		return true, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

func (k *Keys) rowPub(r *row) ([]byte, error) {
	ann, _, err := mail.ParseAnnouncement(r.announced, envelope.KeyString(k.identity), r.created)
	if err != nil {
		return nil, err
	}
	return ann.Pub, nil
}

// load reads the private key of keyID whose public key is pub from the
// keystore. A copy with another public key (a key file planted by a process
// that cannot reach the keychain) is ignored (review 87 M1).
func (k *Keys) load(keyID string, pub []byte) (*ecdh.PrivateKey, error) {
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return nil, err
	}
	seed, _, err := ks.LoadMatching(func(seed []byte) bool {
		priv, err := ecdh.X25519().NewPrivateKey(seed)
		return err == nil && string(priv.PublicKey().Bytes()) == string(pub)
	})
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	return ecdh.X25519().NewPrivateKey(seed)
}

// MailboxKey implements mail.Keys: the private half of the live key with this
// key_id. A key that is deleted, or past not_after + 7 d, is not live. The key
// is read from the keystore once and then cached until it is deleted; after a
// failed read the key is not read again for a second (R55-F13).
func (k *Keys) MailboxKey(id mail.KeyID) (*ecdh.PrivateKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	rows, err := k.live(context.Background())
	if err != nil {
		k.log.Warn("mailbox: cannot list keys", "event", "mailbox_error", "error", err)
		return nil, false
	}
	now := k.now()
	for _, r := range rows {
		if r.keyID != id.String() || !r.notAfter.Add(DeleteGrace).After(now) {
			continue
		}
		if priv, ok := k.cache[r.keyID]; ok {
			return priv, true
		}
		if t, ok := k.failed[r.keyID]; ok && now.Sub(t) < loadRetry && !now.Before(t) {
			return nil, false
		}
		pub, err := k.rowPub(&r)
		var priv *ecdh.PrivateKey
		if err == nil {
			priv, err = k.load(r.keyID, pub)
		}
		if err != nil {
			if k.failed == nil {
				k.failed = map[string]time.Time{}
			}
			k.failed[r.keyID] = now
			return nil, false
		}
		delete(k.failed, r.keyID)
		if k.cache == nil {
			k.cache = map[string]*ecdh.PrivateKey{}
		}
		k.cache[r.keyID] = priv
		return priv, true
	}
	return nil, false
}

// forgetLocked drops the cached private key of keyID. An *ecdh.PrivateKey
// cannot be wiped in place (its Bytes returns a copy), so dropping the only
// reference is all that can be done.
func (k *Keys) forgetLocked(keyID string) {
	delete(k.cache, keyID)
	delete(k.failed, keyID)
}

// Rotate is one run of the rotation job: it creates a new current key if there
// is none, the current one is 7 d old or unusable, deletes the private keys
// that are 21 d old (not_after + 7 d) and have a successor at least 7 d old,
// both measured at the mail_seen basis (expired, sweepLocked), and deletes a
// retired key early if more than 3 would be live (see capVictim). It returns
// the announcement of the newly created key, or nil if none was created.
func (k *Keys) Rotate(ctx context.Context) ([]byte, error) {
	ann, hook, err := k.rotate(ctx)
	if hook != nil && ann != nil {
		hook(ann)
	}
	return ann, err
}

// rotate is Rotate's work under k.mu; hook, if set, is run by the caller
// without the lock.
func (k *Keys) rotate(ctx context.Context) (ann []byte, hook func([]byte), cerr error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	rows, err := k.live(ctx)
	if err != nil {
		return nil, nil, err
	}
	cur := currentOf(rows)
	need := cur == nil || !cur.created.Add(RotateAfter).After(now)
	if !need {
		ok, err := k.usable(cur, now)
		need = err == nil && !ok
		cerr = err
	}
	if need && cerr == nil {
		ann, hook, cerr = k.createLocked(ctx, now, rows)
	} else if cerr == nil {
		cerr = k.sweepLocked(ctx, now, rows)
	}
	return ann, hook, cerr
}

// createLocked makes a new current key, retires the previous one and then
// sweeps. The private key is stored before the row, so a crash never leaves
// an announced key without its secret. rows is the live list before the call.
func (k *Keys) createLocked(ctx context.Context, now time.Time, rows []row) ([]byte, func([]byte), error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("mailbox: generate key: %w", err)
	}
	seed := priv.Bytes()
	defer clear(seed)
	pub := priv.PublicKey().Bytes()
	signed, err := mail.SignAnnouncement(k.identity, k.sign, pub, now)
	if err != nil {
		return nil, nil, err
	}
	ann, _, err := mail.ParseAnnouncement(signed, envelope.KeyString(k.identity), now)
	if err != nil {
		return nil, nil, fmt.Errorf("mailbox: own announcement does not verify: %w", err)
	}
	keyID := ann.KeyID.String()
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("mailbox: create %s: %w", k.dir, err)
	}
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return nil, nil, err
	}
	backend, _, err := ks.Save(seed)
	if err != nil {
		return nil, nil, fmt.Errorf("mailbox: store private key: %w", err)
	}
	at := now.UTC().Format(timeFmt)
	prev := ""
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if cur := currentOf(rows); cur != nil {
		prev = cur.keyID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mailbox_keys_own SET retired = ? WHERE retired IS NULL AND deleted IS NULL`, at); err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: retire: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_keys_own (key_id, pub, created, not_after, announcement, key_backend) VALUES (?, ?, ?, ?, ?, ?)`,
		keyID, b64(pub), ann.Created.UTC().Format(timeFmt), ann.NotAfter.UTC().Format(timeFmt), string(signed), backend); err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: record key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: record key: %w", err)
	}
	if k.audit != nil {
		_ = k.audit.Append(ctx, actorDaemon, ActionRotate, map[string]string{"key_id": keyID, "retired": prev}) // logged once, centrally, by internal/audit
	}
	// Sweep with the new key included. A failed deletion is retried by the next run.
	rows = append(retire(rows), row{keyID: keyID, created: ann.Created, notAfter: ann.NotAfter, announced: signed, backend: backend})
	if err := k.sweepLocked(ctx, now, rows); err != nil {
		k.log.Warn("mailbox: sweep failed", "event", "mailbox_error", "error", err)
	}
	return signed, k.onRotate, nil
}

// expired reports whether r may be deleted by age, measured at basis (see
// sweepLocked): not_after + 7 d <= basis, and a newer key was created at least
// DeleteGrace before basis. Peers seal to r until they accept a newer key, and
// mail sealed to r lives at most the 7 d queue TTL after that. On a steady
// clock the second condition holds a week before the first (R55-F28, review
// 55 R55-103).
func expired(r row, rows []row, basis time.Time) bool {
	if r.notAfter.Add(DeleteGrace).After(basis) {
		return false
	}
	for _, n := range rows {
		if n.created.After(r.created) && !n.created.Add(DeleteGrace).After(basis) {
			return true
		}
	}
	return false
}

// capVictim returns the index in rows of the key the MaxLive cap deletes
// next, or -1: a retired key created after now + mail.MaxSkew first (made
// while the clock was stepped forward; every peer refused its announcement),
// else the retired key with the earliest created. The current key is never a
// candidate: after a backward clock step it can be older by created than the
// key before it (R55-F28, review 100 L1).
func capVictim(rows []row, now time.Time) int {
	i := -1
	for j, r := range rows {
		if !r.retired {
			continue
		}
		if r.created.After(now.Add(mail.MaxSkew)) {
			return j
		}
		if i < 0 || r.created.Before(rows[i].created) {
			i = j
		}
	}
	return i
}

func retire(rows []row) []row {
	out := make([]row, len(rows))
	for i, r := range rows {
		r.retired = true
		out[i] = r
	}
	return out
}

// sweepLocked deletes the private key of every expired key (see expired),
// then of retired keys (see capVictim) while more than MaxLive are live. rows
// is the live list.
//
// Age is measured at mail.SeenBasis, the earlier of now and the newest
// mail_seen stamp, which is bounded by the peers' signed created: a run whose
// clock is stepped forward cannot age out a key that peers still seal to
// (R55-F28, review 100 M1; the same basis as the mail_seen prune, D75). With
// no mail received there is no basis and nothing is deleted by age; MaxLive
// then bounds the live keys, and on the 7-day rotation it deletes each key
// when it is 21 days old, as the age rule would. A clock that stays stepped
// for 14 days or more of runtime can let the cap delete the last key peers
// accepted (mail.md §Lifecycle; owner decision D76).
func (k *Keys) sweepLocked(ctx context.Context, now time.Time, rows []row) error {
	var errs []error
	basis, aged, err := mail.SeenBasis(ctx, k.db, now)
	if err != nil {
		errs = append(errs, fmt.Errorf("mailbox: key age basis: %w", err))
		aged = false
	}
	var keep []row
	for _, r := range rows {
		if !aged || !expired(r, rows, basis) {
			keep = append(keep, r)
			continue
		}
		if err := k.deleteKey(ctx, r, now); err != nil {
			errs = append(errs, err)
			keep = append(keep, r) // still there, so it still counts as live
		}
	}
	for len(keep) > MaxLive {
		i := capVictim(keep, now)
		if i < 0 {
			break
		}
		if err := k.deleteKey(ctx, keep[i], now); err != nil {
			errs = append(errs, err)
			break
		}
		keep = append(keep[:i], keep[i+1:]...)
	}
	return errors.Join(errs...)
}

// deleteKey removes the private key from the keystore and the cache and marks
// the row. The row stays for audit. A key saved to the keychain is not marked
// deleted while this process sees no keychain service: the key may still be
// in the user's keychain, so the row stays live and the delete is retried
// (review 87b N3).
func (k *Keys) deleteKey(ctx context.Context, r row, now time.Time) error {
	keyID := r.keyID
	k.forgetLocked(keyID)
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return err
	}
	if err := ks.DeleteSaved(r.backend); err != nil {
		return fmt.Errorf("mailbox: delete key %s: %w", keyID, err)
	}
	if _, err := k.db.ExecContext(ctx, `UPDATE mailbox_keys_own SET deleted = ? WHERE key_id = ?`,
		now.UTC().Format(timeFmt), keyID); err != nil {
		return fmt.Errorf("mailbox: mark key %s deleted: %w", keyID, err)
	}
	return nil
}

// Run runs Rotate at start and then hourly until ctx is cancelled. New keys
// are pushed to peers through the OnRotate hook.
func (k *Keys) Run(ctx context.Context) {
	t := time.NewTicker(RunInterval)
	defer t.Stop()
	for {
		if _, err := k.Rotate(ctx); err != nil && ctx.Err() == nil {
			k.log.Warn("mailbox: rotation failed", "event", "mailbox_error", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// importLegacy moves the key that 0.8c kept in current.json into the table
// (review L5) and then removes the file, so it happens once. A key whose
// private half is gone, or that is already past its deletion time, is not
// imported; its leftover secret is deleted.
func (k *Keys) importLegacy(ctx context.Context) error {
	path := filepath.Join(k.dir, legacyFile)
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the daemon config dir
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mailbox: read %s: %w", path, err)
	}
	now := k.now()
	var head struct {
		Announcement struct {
			Created string `json:"created"`
		} `json:"announcement"`
	}
	terr := json.Unmarshal(raw, &head)
	var created time.Time
	if terr == nil {
		created, terr = time.Parse("2006-01-02T15:04:05Z", head.Announcement.Created)
	}
	// The announcement is checked at its own creation time: an expired one is still ours.
	ann, canon, perr := mail.ParseAnnouncement(raw, envelope.KeyString(k.identity), created)
	if terr != nil || perr != nil {
		k.log.Warn("mailbox: ignoring unreadable current.json", "event", "mailbox_error", "path", path)
		return os.Remove(path)
	}
	keyID := ann.KeyID.String()
	if !ann.NotAfter.Add(DeleteGrace).After(now) {
		if ks, err := k.keystoreFor(keyID); err == nil {
			_ = ks.Delete()
		}
		return os.Remove(path)
	}
	if _, err := k.load(keyID, ann.Pub); err != nil {
		if !errors.Is(err, keystore.ErrNotFound) && !errors.Is(err, keystore.ErrMismatch) {
			return fmt.Errorf("mailbox: import current.json: %w", err) // keep the file, try again
		}
		return os.Remove(path)
	}
	var retired any
	if !ann.NotAfter.After(now) {
		retired = now.UTC().Format(timeFmt)
	}
	if _, err := k.db.ExecContext(ctx, `INSERT OR IGNORE INTO mailbox_keys_own
(key_id, pub, created, not_after, retired, announcement) VALUES (?, ?, ?, ?, ?, ?)`,
		keyID, b64(ann.Pub), ann.Created.UTC().Format(timeFmt), ann.NotAfter.UTC().Format(timeFmt), retired, string(canon)); err != nil {
		return fmt.Errorf("mailbox: import current.json: %w", err)
	}
	return os.Remove(path)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
