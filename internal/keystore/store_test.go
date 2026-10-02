package keystore

import (
	"errors"
	"path/filepath"
	"strings"
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

func equals(want string) func([]byte) bool {
	return func(b []byte) bool { return string(b) == want }
}

// Review 55 R55-092 (T3-01): a secret saved to the file while the keychain is
// unavailable is not shadowed by the older keychain copy once the keychain is
// back (the webhook would sign with the pre-rotation secret). Review 87 M1:
// nor does the file copy win by backend order; the caller's check picks it,
// and without one the two copies are a conflict.
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
	got, backend, err := st.LoadMatching(equals("S2-rotated"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "S2-rotated" || backend != "file" {
		t.Fatalf("after rotation Load returns %q from %s, want the rotated secret from file", got, backend)
	}
	if got, _, err := st.LoadMatching(equals("S1-old-secret")); err != nil || string(got) != "S1-old-secret" {
		t.Fatalf("the check picks the keychain copy: %q, %v", got, err)
	}
	if _, _, err := st.Load(); !errors.Is(err, ErrConflict) {
		t.Fatalf("Load without a check = %v, want ErrConflict", err)
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
	keyring.MockInitWithError(errors.New("dbus: couldn't determine address of session bus"))
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
	if err := NewKeychain("review55").Delete(); !errors.Is(err, ErrNoService) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Keychain.Delete without a service = %v, want ErrNoService", err)
	}
}

// Review 87 M2: Delete while the keychain is only locked fails, so the
// mailbox keeps the key row live and retries, rather than marking a key
// deleted that is still in the keychain.
func TestDeleteWhileLockedFails(t *testing.T) {
	kc := &deletingKeychain{}
	st := New(kc, NewFile(filepath.Join(testutil.TempDir(t), "m.key")))
	if b, _, err := st.Save([]byte("mailbox-private")); err != nil || b != "keychain" {
		t.Fatalf("save: %s %v", b, err)
	}
	kc.down = true // locked, not absent
	if err := st.Delete(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete while locked = %v, want an ErrUnavailable error", err)
	}
	kc.down = false
	if err := st.Delete(); err != nil || kc.v != nil {
		t.Fatalf("Delete once unlocked: %v, left %q", err, kc.v)
	}
}

// Review 87 M2, L2: only the go-keyring and godbus failures that say the host
// has no keychain service count as such; a locked keychain does not.
func TestNoServiceClassification(t *testing.T) {
	for msg, want := range map[string]bool{
		"dbus: couldn't determine address of session bus":                                true,
		"The name org.freedesktop.secrets was not provided by any .service files":        true,
		"org.freedesktop.DBus.Error.ServiceUnknown":                                      true,
		"exec: \"dbus-launch\": executable file not found in $PATH":                      true,
		"failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'": false,
		"keychain call timed out":                                                        false,
		"exit status 51: User interaction is not allowed.":                               false,
	} {
		if got := errors.Is(unavailable(errors.New(msg)), ErrNoService); got != want {
			t.Errorf("%q: no service = %v, want %v", msg, got, want)
		}
	}
	if !errors.Is(unavailable(keyring.ErrUnsupportedPlatform), ErrNoService) {
		t.Error("an unsupported platform is not reported as no service")
	}
}

// Review 87 L2: on a host without a keychain service a secret missing from
// the file is not found, not "unavailable" (the key is lost, not locked).
func TestLoadWithoutKeychainIsNotFound(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: couldn't determine address of session bus"))
	defer keyring.MockInit()
	st := New(NewKeychain("review87"), NewFile(filepath.Join(testutil.TempDir(t), "k.key")))
	if _, _, err := st.Load(); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("Load = %v, want ErrNotFound", err)
	}
}

// Review 87 M1: a copy is chosen only by the caller's check, never by
// backend order; with copies but none matching, Load says so.
func TestLoadMatchingIgnoresOtherCopies(t *testing.T) {
	kc := &flakyKeychain{v: []byte("real")}
	f := NewFile(filepath.Join(testutil.TempDir(t), "k.key"))
	if err := f.Set([]byte("planted")); err != nil {
		t.Fatal(err)
	}
	st := New(kc, f)
	if got, b, err := st.LoadMatching(equals("real")); err != nil || string(got) != "real" || b != "keychain" {
		t.Fatalf("LoadMatching = %q from %s, %v", got, b, err)
	}
	if _, _, err := st.LoadMatching(equals("other")); !errors.Is(err, ErrMismatch) {
		t.Fatalf("no match = %v, want ErrMismatch", err)
	}
	kc.down = true
	if _, _, err := st.LoadMatching(equals("real")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("matching copy in a locked keychain = %v, want ErrUnavailable", err)
	}
}

// Review 87 L3: a broken leftover key file does not hide a copy the check
// accepts; without a check, the error names the backend that holds a copy.
func TestBrokenFileDoesNotBlockMatchingCopy(t *testing.T) {
	kc := &flakyKeychain{v: []byte("real")}
	st := New(kc, brokenBackend{})
	if got, _, err := st.LoadMatching(equals("real")); err != nil || string(got) != "real" {
		t.Fatalf("LoadMatching = %q, %v", got, err)
	}
	_, _, err := st.Load()
	if err == nil || !strings.Contains(err.Error(), "k.key") || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("Load = %v, want an error naming the file and the keychain copy", err)
	}
}

type brokenBackend struct{}

func (brokenBackend) Name() string { return "file" }
func (brokenBackend) Get() ([]byte, error) {
	return nil, errors.New("key file k.key has mode 0644; it must not be accessible by group or others (chmod 600)")
}
func (brokenBackend) Set([]byte) error { return errors.New("read-only") }

// Review 87b N3: "no keychain service" is what this process sees, not the
// host. DeleteSaved skips it only for a backend the secret was not saved to.
func TestDeleteSavedKeepsKeychainSecretWithoutService(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: couldn't determine address of session bus"))
	defer keyring.MockInit()
	st := New(NewKeychain("review87b"), NewFile(filepath.Join(testutil.TempDir(t), "k.key")))
	if err := st.DeleteSaved("keychain"); !errors.Is(err, ErrNoService) {
		t.Fatalf("DeleteSaved(keychain) without a service = %v, want an ErrNoService error", err)
	}
	for _, saved := range []string{"file", ""} {
		if err := st.DeleteSaved(saved); err != nil {
			t.Fatalf("DeleteSaved(%q) without a service = %v, want nil", saved, err)
		}
	}
}

// Review 87b N2: without a check, differing copies are a conflict that names
// where each copy lives.
func TestConflictNamesEachCopy(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	path := filepath.Join(testutil.TempDir(t), "k.key")
	kc := NewKeychain("review87b")
	if err := kc.Set([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := NewFile(path).Set([]byte("two")); err != nil {
		t.Fatal(err)
	}
	_, _, err := New(kc, NewFile(path)).Load()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Load = %v, want ErrConflict", err)
	}
	for _, want := range []string{"keychain entry " + Service + "/review87b", "file " + path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("conflict error does not name %q: %v", want, err)
		}
	}
}
