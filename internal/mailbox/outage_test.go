package mailbox_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 R55-092 (C05-01): in "auto" mode a keychain that is merely
// unavailable (locked, timed out) is not "private key not found": the
// current key is kept, not replaced and pushed to every peer.
func TestKeychainOutageDoesNotRotate(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	db := newDB(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Identity key in the file backend (it fell back at first run), mailbox keys in "auto".
	idks := keystore.New(keystore.NewFile(filepath.Join(dir, "identity.key")))
	if _, _, err := idks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	sign := relayclient.NewKeystoreSigner(idks, pub).Sign
	k := mailbox.New(dir, "auto", pub, sign, func() time.Time { return now })
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	pushes := 0
	k.OnRotate(func([]byte) { pushes++ })
	a, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	pushes = 0
	now = now.Add(time.Hour)
	keyring.MockInitWithError(errors.New("keychain locked"))
	ann, err := k.Rotate(context.Background())
	if err != nil || ann != nil {
		t.Fatalf("Rotate during an outage: announcement=%v err=%v", ann != nil, err)
	}
	b, err := k.Announcement()
	if err != nil || string(a) != string(b) || pushes != 0 || liveCount(t, db) != 1 {
		t.Fatalf("key rotated because the keychain was unavailable: err=%v pushes=%d live=%d", err, pushes, liveCount(t, db))
	}
}
