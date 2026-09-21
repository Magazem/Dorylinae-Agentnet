// Package mailbox holds the daemon's own mailbox keys (Docs/protocol/mail.md
// §Mailbox keys). The private halves live in the keystore, one secret per key.
// The public halves, their signed announcements and their lifecycle live in the
// mailbox_keys_own table (migration 6). Keys rotate every 7 days, a retired key
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
	"strings"
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
	acctHex  string
	mode     string
	identity ed25519.PublicKey
	sign     func(msg []byte) ([]byte, error)
	now      func() time.Time

	mu       sync.Mutex
	db       *sql.DB
	audit    AuditSink
	log      *slog.Logger
	onRotate func(announcement []byte)
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
		acctHex:  strings.TrimPrefix(keystore.AccountFor(configDir), "identity-"),
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
	file := keystore.NewFile(filepath.Join(k.dir, keyID+".key"))
	switch k.mode {
	case "", "auto":
		return keystore.New(keystore.NewKeychain("mailbox-"+k.acctHex+"-"+keyID), file), nil
	case "file":
		return keystore.New(file), nil
	default:
		return nil, fmt.Errorf("mailbox: keystore mode must be \"auto\" or \"file\", got %q", k.mode)
	}
}

// row is one mailbox_keys_own row.
type row struct {
	keyID     string
	created   time.Time
	notAfter  time.Time
	retired   bool
	announced []byte
}

func (k *Keys) errNoDB() error { return errors.New("mailbox: no database attached") }

// live returns the rows whose private key has not been deleted, oldest first.
func (k *Keys) live(ctx context.Context) ([]row, error) {
	if k.db == nil {
		return nil, k.errNoDB()
	}
	rs, err := k.db.QueryContext(ctx, `SELECT key_id, created, not_after, retired IS NOT NULL, announcement
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
		if err := rs.Scan(&r.keyID, &c, &n, &r.retired, &an); err != nil {
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
	ctx := context.Background()
	k.mu.Lock()
	now := k.now()
	rows, err := k.live(ctx)
	if err != nil {
		k.mu.Unlock()
		return nil, err
	}
	if cur := currentOf(rows); cur != nil {
		ok, err := k.usable(cur, now)
		if err != nil {
			k.mu.Unlock()
			return nil, err
		}
		if ok {
			k.mu.Unlock()
			return cur.announced, nil
		}
	}
	ann, hook, err := k.createLocked(ctx, now, rows)
	k.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(ann)
	}
	return ann, nil
}

// usable reports whether r is not about to expire and its private key is in
// the keystore and matches. A keystore failure other than "not found" is an
// error: rotating then would orphan a key that may still be there (review L5).
func (k *Keys) usable(r *row, now time.Time) (bool, error) {
	if !r.notAfter.After(now.Add(renewBefore)) {
		return false, nil
	}
	priv, err := k.load(r.keyID)
	switch {
	case errors.Is(err, keystore.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	pub, _ := k.rowPub(r)
	return pub != nil && string(priv.PublicKey().Bytes()) == string(pub), nil
}

func (k *Keys) rowPub(r *row) ([]byte, error) {
	ann, _, err := mail.ParseAnnouncement(r.announced, envelope.KeyString(k.identity), r.created)
	if err != nil {
		return nil, err
	}
	return ann.Pub, nil
}

// load reads the private key of keyID from the keystore.
func (k *Keys) load(keyID string) (*ecdh.PrivateKey, error) {
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return nil, err
	}
	seed, _, err := ks.Load()
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	return ecdh.X25519().NewPrivateKey(seed)
}

// MailboxKey implements mail.Keys: the private half of the live key with this
// key_id. A key that is deleted, or past not_after + 7 d, is not live.
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
		priv, err := k.load(r.keyID)
		if err != nil {
			return nil, false
		}
		return priv, true
	}
	return nil, false
}

// Rotate is one run of the rotation job: it creates a new current key if there
// is none, the current one is 7 d old or unusable, deletes the private keys
// that are 21 d old (not_after + 7 d), and deletes the oldest early if more
// than 3 would be live. It returns the announcement of the newly created key,
// or nil if none was created.
func (k *Keys) Rotate(ctx context.Context) ([]byte, error) {
	k.mu.Lock()
	now := k.now()
	rows, err := k.live(ctx)
	if err != nil {
		k.mu.Unlock()
		return nil, err
	}
	var (
		ann  []byte
		hook func([]byte)
		cerr error
	)
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
	k.mu.Unlock()
	if hook != nil && ann != nil {
		hook(ann)
	}
	return ann, cerr
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
	if _, _, err := ks.Save(seed); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_keys_own (key_id, pub, created, not_after, announcement) VALUES (?, ?, ?, ?, ?)`,
		keyID, b64(pub), ann.Created.UTC().Format(timeFmt), ann.NotAfter.UTC().Format(timeFmt), string(signed)); err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: record key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = ks.Delete()
		return nil, nil, fmt.Errorf("mailbox: record key: %w", err)
	}
	if k.audit != nil {
		if aerr := k.audit.Append(ctx, actorDaemon, ActionRotate, map[string]string{"key_id": keyID, "retired": prev}); aerr != nil {
			k.log.Warn("mailbox: audit failed", "event", "mailbox_error", "error", aerr)
		}
	}
	// Sweep with the new key included. A failed deletion is retried by the next run.
	rows = append(retire(rows), row{keyID: keyID, created: ann.Created, notAfter: ann.NotAfter, announced: signed})
	if err := k.sweepLocked(ctx, now, rows); err != nil {
		k.log.Warn("mailbox: sweep failed", "event", "mailbox_error", "error", err)
	}
	return signed, k.onRotate, nil
}

func retire(rows []row) []row {
	out := make([]row, len(rows))
	for i, r := range rows {
		r.retired = true
		out[i] = r
	}
	return out
}

// sweepLocked deletes the private key of every key with not_after + 7 d <= now,
// then of the oldest keys while more than MaxLive are live. rows is the live
// list, oldest first.
func (k *Keys) sweepLocked(ctx context.Context, now time.Time, rows []row) error {
	var errs []error
	var keep []row
	for _, r := range rows {
		if r.notAfter.Add(DeleteGrace).After(now) {
			keep = append(keep, r)
			continue
		}
		if err := k.deleteKey(ctx, r.keyID, now); err != nil {
			errs = append(errs, err)
			keep = append(keep, r) // still there, so it still counts as live
		}
	}
	for len(keep) > MaxLive {
		if err := k.deleteKey(ctx, keep[0].keyID, now); err != nil {
			errs = append(errs, err)
			break
		}
		keep = keep[1:]
	}
	return errors.Join(errs...)
}

// deleteKey removes the private key from the keystore and marks the row. The
// row stays for audit.
func (k *Keys) deleteKey(ctx context.Context, keyID string, now time.Time) error {
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return err
	}
	if err := ks.Delete(); err != nil {
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
	if priv, err := k.load(keyID); err != nil || string(priv.PublicKey().Bytes()) != string(ann.Pub) {
		if err != nil && !errors.Is(err, keystore.ErrNotFound) {
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
