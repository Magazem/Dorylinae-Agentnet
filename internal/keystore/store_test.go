package keystore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// flakyKeychain is a keychain backend that can be made unavailable, keeping
// what it stored (a locked keychain keeps its entries).
type flakyKeychain struct {
	v    []byte
	down bool
}

func (*flakyKeychain) Name() string { return "keychain" }

func (k *flakyKeychain) Get() ([]byte, error) {
	if k.down {
		return nil, ErrUnavailable
	}
	if k.v == nil {
		return nil, ErrNotFound
	}
	return append([]byte(nil), k.v...), nil
}

func (k *flakyKeychain) Set(s []byte) error {
	if k.down {
		return ErrUnavailable
	}
	k.v = append([]byte(nil), s...)
	return nil
}

// deletingKeychain is a flakyKeychain that can also delete its entry.
type deletingKeychain struct{ flakyKeychain }

func (k *deletingKeychain) Delete() error {
	if k.down {
		return ErrUnavailable
	}
	if k.v == nil {
		return ErrNotFound
	}
	k.v = nil
	return nil
}

// Review 55 R55-092 (T3-01): a secret saved to the file while the keychain is
// unavailable is not shadowed by the older keychain copy once the keychain is
// back (the webhook would sign with the pre-rotation secret).
func TestSaveDuringOutageIsNotShadowed(t *testing.T) {
	kc := &flakyKeychain{}
	f := NewFile(filepath.Join(testutil.TempDir(t), "webhook.key"))
	st := New(kc, f)
	if _, _, err := st.Save([]byte("S1-old-secret")); err != nil {
		t.Fatal(err)
	}
	kc.down = true
	if b, _, err := st.Save([]byte("S2-rotated")); err != nil || b != "file" {
		t.Fatalf("rotate: backend=%q err=%v", b, err)
	}
	kc.down = false
	got, backend, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "S2-rotated" || backend != "file" {
		t.Fatalf("after rotation Load returns %q from %s, want the rotated secret from file", got, backend)
	}
	// The next Save with the keychain back puts the secret there and removes
	// the file copy, so it is not read again.
	if b, _, err := st.Save([]byte("S3-again")); err != nil || b != "keychain" {
		t.Fatalf("save: backend=%q err=%v", b, err)
	}
	if got, backend, err := st.Load(); err != nil || string(got) != "S3-again" || backend != "keychain" {
		t.Fatalf("Load = %q from %s, %v", got, backend, err)
	}
	if _, err := f.Get(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file copy survived a keychain save: %v", err)
	}
}

// A fallback save removes the more preferred copy when it can.
func TestFallbackSaveRemovesPreferredCopy(t *testing.T) {
	kc := &deletingKeychain{}
	st := New(kc, NewFile(filepath.Join(testutil.TempDir(t), "k.key")))
	if _, _, err := st.Save([]byte("old")); err != nil {
		t.Fatal(err)
	}
	// The keychain refuses this write but can still delete.
	refuse := &refusingSet{Backend: kc, del: kc}
	st.backends[0] = refuse
	if b, _, err := st.Save([]byte("new")); err != nil || b != "file" {
		t.Fatalf("save: backend=%q err=%v", b, err)
	}
	if kc.v != nil {
		t.Fatalf("the older keychain copy %q was left behind", kc.v)
	}
}

type refusingSet struct {
	Backend
	del Deleter
}

func (*refusingSet) Set([]byte) error { return errors.New("entry too large") }
func (r *refusingSet) Delete() error  { return r.del.Delete() }

// Review 55 R55-092 (T3-02): on a machine without a keychain service,
// Delete succeeds once the file copy is gone (mailbox keys are marked
// deleted, "--webhook off" completes).
func TestDeleteWithoutKeychain(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: no session bus"))
	defer keyring.MockInit()
	f := NewFile(filepath.Join(testutil.TempDir(t), "k.key"))
	if err := f.Set([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := New(NewKeychain("review55"), f).Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.Get(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file copy survived: %v", err)
	}
	if err := NewKeychain("review55").Delete(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Keychain.Delete without a service = %v, want ErrUnavailable", err)
	}
}
