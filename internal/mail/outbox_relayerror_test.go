package mail

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F9 test 12: an error frame whose code the relay client converted to
// relay_error (an unknown code) leaves the row relayed; a listed temporary
// code still moves it back to queued.
func TestOutboxIgnoresRelayError(t *testing.T) {
	sender, recip, _ := fixture(t)
	st := openStore(t, filepath.Join(testutil.TempDir(t), "o.db"))
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	ob := &Outbox{
		DB: db, Peers: &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}}, Sender: &sentBox{},
		Priv: func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
		Now:  func() time.Time { return vectorNow },
	}
	res, err := ob.Submit(context.Background(), recip.key, "note", map[string]any{"text": "x"})
	if err != nil {
		t.Fatal(err)
	}
	state := func() string {
		t.Helper()
		var s string
		if err := db.QueryRow(`SELECT state FROM outbox WHERE id = ?`, res.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := db.Exec(`UPDATE outbox SET state = 'relayed' WHERE id = ?`, res.ID); err != nil {
		t.Fatal(err)
	}
	ob.HandleError(envelope.ErrorFrame{Code: envelope.CodeRelayError, Message: "no such code", Ref: res.ID})
	if s := state(); s != StateRelayed {
		t.Fatalf("after relay_error: state %s, want %s", s, StateRelayed)
	}
	ob.HandleError(envelope.ErrorFrame{Code: envelope.CodeQueueFull, Ref: res.ID})
	if s := state(); s != StateQueued {
		t.Fatalf("after queue_full: state %s, want %s", s, StateQueued)
	}
}
