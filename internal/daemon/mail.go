package daemon

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// mailQueue is how many mail envelopes may wait for the receiver. The relay
// read loop must not block; a dropped envelope is resent by its sender.
const mailQueue = 256

// peerDirectory answers the mail package's Peers and PeerKeys questions from
// the peers table.
type peerDirectory struct{ db *sql.DB }

func (d peerDirectory) IsPaired(key string) bool {
	var one int
	return d.db.QueryRow(`SELECT 1 FROM peers WHERE public_key = ?`, key).Scan(&one) == nil
}

// MailboxPub returns the pub of the newest stored announcement of a peer.
func (d peerDirectory) MailboxPub(peer string) ([]byte, bool) {
	var raw string
	if err := d.db.QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, peer).Scan(&raw); err != nil {
		return nil, false
	}
	var anns []struct {
		Announcement struct {
			Pub string `json:"pub"`
		} `json:"announcement"`
	}
	if json.Unmarshal([]byte(raw), &anns) != nil || len(anns) == 0 {
		return nil, false
	}
	pub, err := base64.RawURLEncoding.DecodeString(anns[0].Announcement.Pub)
	if err != nil {
		return nil, false
	}
	return pub, true
}

// PeersWithKeys returns the paired peers that have a mailbox key, for the
// rotation push.
func (d peerDirectory) PeersWithKeys(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT public_key FROM peers WHERE mailbox_keys <> '[]' ORDER BY public_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ownKeys is what the daemon needs of its own mailbox keys beyond mail.Keys
// (implemented by *mailbox.Keys): the current announcement, the rotation hook
// and the rotation job. Options.MailboxKeys without it gets no rotation.
type ownKeys interface {
	mail.Keys
	Announcement() ([]byte, error)
	OnRotate(func(announcement []byte))
	Run(ctx context.Context)
}

// newMailReceiver builds the receiver and the pusher of keys mail. keys supplies
// the own mailbox private keys (created by pairing and rotation, tickets 0.8c
// and 1.0b). Both need Sender, which startMail sets.
func newMailReceiver(db *sql.DB, log *audit.Log, ks *keystore.Store, self ed25519.PublicKey, keys mail.Keys, lg *slog.Logger, ts *team.Store) (*mail.Receiver, *mail.Pusher) {
	dir := peerDirectory{db}
	selfKey := envelope.KeyString(self)
	priv := func() (ed25519.PrivateKey, error) {
		seed, _, err := ks.Load()
		if err != nil {
			return nil, err
		}
		defer clear(seed)
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New("stored identity key has the wrong length")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	pusher := &mail.Pusher{Priv: priv, Peers: dir, List: dir.PeersWithKeys, Log: lg}
	rcv := &mail.Receiver{
		Opener: &mail.Opener{
			Self: selfKey, Peers: dir, Keys: keys,
			Audit: mail.NewRejectAudit(log, lg),
		},
		DB:    db,
		Peers: dir,
		Audit: log,
		Priv:  priv,
		Log:   lg,
		Kinds: map[string]mail.Kind{
			// startMail replaces the nil hook with the outbox re-seal (1.0e).
			"keys": mail.KeysKind(peers.MergeMailboxKeysTx, nil),
		},
	}
	if ts != nil {
		rcv.Kinds["team.roster"] = ts.RosterKind()
		rcv.Kinds["team.join"] = ts.JoinKind()
		rcv.Kinds["team.leave"] = ts.LeaveKind()
	}
	if os.Getenv(mail.DebugEnv) == "1" {
		// Debug only: lets `agentnet mail send --kind note` exercise the mail
		// path before Phase 1 brings real kinds. Stored to the inbox, nothing else.
		rcv.Kinds[mail.DebugKind] = mail.Kind{Inbox: true}
	}
	if rot, ok := keys.(ownKeys); ok {
		km := &mail.KeyMiss{Pusher: pusher, Announcement: rot.Announcement, Log: lg}
		rcv.OnKeyMiss = func(ctx context.Context, env envelope.Envelope) { km.Note(ctx, env.From, env.ID) }
	}
	return rcv, pusher
}

// startMail runs the receiver on its own goroutine and returns the function
// the relay read loop calls for each mail envelope, plus a stop function.
func startMail(ctx context.Context, rcv *mail.Receiver, pusher *mail.Pusher, client *relayclient.Client, keys mail.Keys, ob *mail.Outbox) (handle func(envelope.Envelope), stop func()) {
	rcv.Sender = client
	pusher.Sender = client
	ob.Sender = client
	rcv.OnAck = ob.OnAck
	rcv.Kinds["keys"] = mail.KeysKind(peers.MergeMailboxKeysTx, ob.Retry)
	pusher.Outbox = func(ctx context.Context, peer string, body map[string]any) error {
		_, err := ob.Submit(ctx, peer, "keys", body)
		return err
	}
	q := make(chan envelope.Envelope, mailQueue)
	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go rcv.RunPrune(mctx)
	if rot, ok := keys.(ownKeys); ok {
		// A new key is pushed to every peer. The job starts after the hook is set.
		rot.OnRotate(func(ann []byte) { go pusher.PushAll(mctx, ann) })
		go rot.Run(mctx)
	}
	go func() {
		defer close(done)
		for {
			select {
			case <-mctx.Done():
				return
			case e := <-q:
				_ = rcv.Handle(mctx, e) // rejections are audited by the receiver
			}
		}
	}()
	return func(e envelope.Envelope) {
			select {
			case q <- e:
			default: // full: dropped, the sender resends
			}
		}, func() {
			cancel()
			<-done
		}
}

// newOutbox builds the sender outbox. Its Sender is set by startMail once the
// relay client exists; until then rows stay queued.
func newOutbox(db *sql.DB, log *audit.Log, ks *keystore.Store, lg *slog.Logger) *mail.Outbox {
	dir := peerDirectory{db}
	return &mail.Outbox{
		DB:    db,
		Peers: dir,
		Audit: log,
		Log:   lg,
		Priv:  identityPriv(ks),
	}
}

// identityPriv returns a function that loads the daemon's identity private
// key from ks, for callers (the outbox, the mail receiver, the presence
// sender) that each need their own copy to clear after use.
func identityPriv(ks *keystore.Store) func() (ed25519.PrivateKey, error) {
	return func() (ed25519.PrivateKey, error) {
		seed, _, err := ks.Load()
		if err != nil {
			return nil, err
		}
		defer clear(seed)
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New("stored identity key has the wrong length")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
}
