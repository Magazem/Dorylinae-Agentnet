package keystore

import (
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 87 finding 87-02 (evidence; fails while the finding stands): Store.Delete with the
// keychain only locked reports success, so the mailbox marks the key deleted
// and never retries, while the private key stays in the keychain (mail.md:
// the key is deleted at not_after + 7 d). Before R55-F21 the raw keychain
// error failed Delete and the hourly job retried.
func TestReview87DeleteWhileLockedLeavesKeyAndReportsSuccess(t *testing.T) {
	kc := &deletingKeychain{}
	st := New(kc, NewFile(filepath.Join(testutil.TempDir(t), "m.key")))
	if b, _, err := st.Save([]byte("mailbox-private")); err != nil || b != "keychain" {
		t.Fatalf("save: %s %v", b, err)
	}
	kc.down = true // locked, not absent
	if err := st.Delete(); err != nil {
		t.Fatalf("fixed? Delete now fails while the keychain is locked: %v", err)
	}
	kc.down = false
	if kc.v == nil {
		t.Fatal("fixed? the key is gone")
	}
	t.Fatalf("Delete returned nil but the keychain still holds %q", kc.v)
}
