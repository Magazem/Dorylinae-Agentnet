package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// countingKeystore counts identity key reads; while fail is set they fail.
type countingKeystore struct {
	keystore.Backend
	mu    sync.Mutex
	reads int
	fail  bool
}

func (c *countingKeystore) Get() ([]byte, error) {
	c.mu.Lock()
	c.reads++
	fail := c.fail
	c.mu.Unlock()
	if fail {
		return nil, errors.New("keystore process failed")
	}
	return c.Backend.Get()
}

func (c *countingKeystore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *countingKeystore) setFail(v bool) {
	c.mu.Lock()
	c.fail = v
	c.mu.Unlock()
}

func countingIdentity(t *testing.T) (string, *countingKeystore, *keystore.Store, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.TempDir(t)
	file := keystore.NewFile(filepath.Join(dir, "identity.key"))
	if err := file.Set(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	c := &countingKeystore{Backend: file}
	return dir, c, keystore.New(c), pub
}

// R55-F13 (review 55 R55-051): the identity key is read from the keystore
// once, shared by the mail receiver, the pusher, the outbox and the signer,
// and a caller clearing its copy does not wipe the cached key.
func TestIdentityKeyLoadedOnce(t *testing.T) {
	dir, c, ks, pub := countingIdentity(t)
	idKey := newIdentityKey(dir, ks, pub)
	rcv, pusher := newMailReceiver(nil, nil, idKey, pub, nil, nil, nil, nil, nil, nil)
	ob := newOutbox(nil, nil, idKey, nil)
	loaders := []func() (ed25519.PrivateKey, error){rcv.Priv, pusher.Priv, ob.Priv, idKey.Priv}
	msg := []byte("challenge")
	for i := 0; i < 1000; i++ {
		priv, err := loaders[i%len(loaders)]()
		if err != nil {
			t.Fatal(err)
		}
		if !priv.Public().(ed25519.PublicKey).Equal(pub) {
			t.Fatalf("call %d: wrong key", i)
		}
		clear(priv) // what Outbox.Retry, Pusher and Receiver.ack do
		sig, err := idKey.Sign(msg)
		if err != nil || !ed25519.Verify(pub, msg, sig) {
			t.Fatalf("call %d: signature does not verify after a caller cleared its copy: %v", i, err)
		}
	}
	if n := c.count(); n != 1 {
		t.Fatalf("identity key read %d times, want 1", n)
	}
}

// A failed read is not cached, but is retried at most once per second; a
// stored key that does not match the card is refused.
func TestIdentityKeyLoadErrorRetriedOncePerSecond(t *testing.T) {
	dir, c, ks, pub := countingIdentity(t)
	idKey := newIdentityKey(dir, ks, pub)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	idKey.now = func() time.Time { return now }
	c.setFail(true)
	for i := 0; i < 100; i++ {
		if _, err := idKey.Priv(); err == nil {
			t.Fatal("key returned while the keystore fails")
		}
	}
	if n := c.count(); n != 1 {
		t.Fatalf("%d reads within a second, want 1", n)
	}
	now = now.Add(time.Second)
	c.setFail(false)
	if _, err := idKey.Priv(); err != nil {
		t.Fatal(err)
	}
	if n := c.count(); n != 2 {
		t.Fatalf("%d reads, want 2", n)
	}

	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := newIdentityKey(dir, ks, other).Sign([]byte("x")); err == nil {
		t.Fatal("a stored key that does not match the card signed")
	}
}
