package mailbox_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// countingBackend counts keystore reads; while fail is set, every read fails.
type countingBackend struct {
	keystore.Backend
	c *readCount
}

type readCount struct {
	mu    sync.Mutex
	reads int
	fail  bool
}

func (c *readCount) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *readCount) setFail(v bool) {
	c.mu.Lock()
	c.fail = v
	c.mu.Unlock()
}

func (b countingBackend) Get() ([]byte, error) {
	b.c.mu.Lock()
	b.c.reads++
	fail := b.c.fail
	b.c.mu.Unlock()
	if fail {
		return nil, errors.New("keystore process failed")
	}
	return b.Backend.Get()
}

func (b countingBackend) Delete() error {
	if d, ok := b.Backend.(keystore.Deleter); ok {
		return d.Delete()
	}
	return nil
}

type anyPaired struct{}

func (anyPaired) IsPaired(string) bool { return true }

// forged is a mail frame from a "paired" key that names key_id id but whose
// ciphertext is garbage: step 3 looks the key up, step 4 fails.
func forged(t *testing.T, self string, id mail.KeyID, n int) envelope.Envelope {
	t.Helper()
	p := make([]byte, mail.MinPayload)
	p[0] = mail.PayloadVersion
	copy(p[1:], id[:])
	if _, err := rand.Read(p[1+mail.KeyIDLen:]); err != nil {
		t.Fatal(err)
	}
	from, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.Envelope{From: envelope.KeyString(from), To: self, Type: mail.MailType,
		ID: "m-" + string(bytes.Repeat([]byte{'0'}, 31)) + string(rune('a'+n%6)), TS: "2026-05-01T12:00:00Z", Payload: p}
}

// R55-F13 (review 55 R55-051): a mailbox private key is read from the
// keystore once and cached until the key is deleted.
func TestMailboxKeyIsLoadedOnceAndDroppedWithTheKey(t *testing.T) {
	dir := testutil.TempDir(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	k, pub := newKeys(t, dir, func() time.Time { return now })
	c := &readCount{}
	mailbox.WrapBackends(k, func(b keystore.Backend) keystore.Backend { return countingBackend{b, c} })
	raw, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	self := envelope.KeyString(pub)
	id := keyIDOf(t, raw, self, now)
	op := &mail.Opener{Self: self, Peers: anyPaired{}, Keys: k, Now: func() time.Time { return now }}

	before := c.get()
	for i := 0; i < 1000; i++ {
		_, err := op.Open(forged(t, self, id, i))
		if mail.ReasonOf(err) != mail.ReasonDecrypt {
			t.Fatalf("frame %d: %v, want a decrypt failure", i, err)
		}
	}
	if n := c.get() - before; n != 1 {
		t.Fatalf("1000 forged frames naming a live key_id: %d keystore reads, want 1", n)
	}

	// A caller wiping the bytes it got does not touch the cached key.
	priv, ok := k.MailboxKey(id)
	if !ok {
		t.Fatal("live key not found")
	}
	want := priv.Bytes()
	clear(priv.Bytes())
	again, ok := k.MailboxKey(id)
	if !ok || !bytes.Equal(again.Bytes(), want) || bytes.Equal(want, make([]byte, len(want))) {
		t.Fatal("cached key changed after a caller cleared its copy")
	}

	// Rotation 21 days later deletes the key: the cache is empty and a frame
	// naming it is a key miss.
	// Its successor is made by the 7-day rotation: a key is deleted by age only
	// once a newer key has been current for 7 days (R55-F28).
	now = now.Add(mailbox.RotateAfter)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(14 * 24 * time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if keyFileExists(dir, id.String()) {
		t.Fatal("the rotation did not delete the key")
	}
	if n := mailbox.CachedKeys(k); n != 0 {
		t.Fatalf("%d keys cached after the deletion, want 0", n)
	}
	if _, err := op.Open(forged(t, self, id, 0)); mail.ReasonOf(err) != mail.ReasonKeyMiss {
		t.Fatalf("frame naming the deleted key: %v, want key_miss", err)
	}
}

// A failed keystore read is not cached, but the key is read at most once per
// second.
func TestMailboxKeyLoadErrorRetriedOncePerSecond(t *testing.T) {
	dir := testutil.TempDir(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	k, pub := newKeys(t, dir, func() time.Time { return now })
	c := &readCount{}
	mailbox.WrapBackends(k, func(b keystore.Backend) keystore.Backend { return countingBackend{b, c} })
	raw, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	id := keyIDOf(t, raw, envelope.KeyString(pub), now)

	c.setFail(true)
	before := c.get()
	for i := 0; i < 100; i++ {
		if _, ok := k.MailboxKey(id); ok {
			t.Fatal("key returned while the keystore fails")
		}
	}
	if n := c.get() - before; n != 1 {
		t.Fatalf("%d reads in the same second, want 1", n)
	}
	now = now.Add(999 * time.Millisecond)
	k.MailboxKey(id)
	if n := c.get() - before; n != 1 {
		t.Fatalf("%d reads within a second, want 1", n)
	}
	now = now.Add(time.Millisecond)
	c.setFail(false)
	if _, ok := k.MailboxKey(id); !ok {
		t.Fatal("key not loaded after a second")
	}
	for i := 0; i < 100; i++ {
		k.MailboxKey(id)
	}
	if n := c.get() - before; n != 2 {
		t.Fatalf("%d reads, want 2 (one failure, one load)", n)
	}
}
