package daemon

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
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

// newMailReceiver builds the receiver. keys supplies the own mailbox private
// keys (created by pairing and rotation, tickets 0.8c and 1.0b).
func newMailReceiver(db *sql.DB, log *audit.Log, ks *keystore.Store, self ed25519.PublicKey, keys mail.Keys, lg *slog.Logger) *mail.Receiver {
	dir := peerDirectory{db}
	selfKey := envelope.KeyString(self)
	return &mail.Receiver{
		Opener: &mail.Opener{
			Self: selfKey, Peers: dir, Keys: keys,
			Audit: mail.NewRejectAudit(log, lg),
		},
		DB:    db,
		Peers: dir,
		Audit: log,
		Priv: func() (ed25519.PrivateKey, error) {
			seed, _, err := ks.Load()
			if err != nil {
				return nil, err
			}
			defer clear(seed)
			if len(seed) != ed25519.SeedSize {
				return nil, errors.New("stored identity key has the wrong length")
			}
			return ed25519.NewKeyFromSeed(seed), nil
		},
		Log: lg,
	}
}

// startMail runs the receiver on its own goroutine and returns the function
// the relay read loop calls for each mail envelope, plus a stop function.
func startMail(ctx context.Context, rcv *mail.Receiver, client *relayclient.Client) (handle func(envelope.Envelope), stop func()) {
	rcv.Sender = client
	q := make(chan envelope.Envelope, mailQueue)
	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go rcv.RunPrune(mctx)
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
