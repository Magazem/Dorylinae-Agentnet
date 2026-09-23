package daemon

import (
	"bytes"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestWebhookKeystoreScopedPerHome guards against the bug found by the Phase 1
// smoke script: two daemon homes sharing one machine's keychain backend must
// not overwrite each other's webhook secret (Docs/protocol/notify.md
// §Secret), the same way the identity key is scoped by keystore.AccountFor.
func TestWebhookKeystoreScopedPerHome(t *testing.T) {
	keyring.MockInit()
	dirA := testutil.TempDir(t)
	dirB := testutil.TempDir(t)

	ksA := webhookKeystore(dirA, "auto")
	ksB := webhookKeystore(dirB, "auto")

	secretA := bytes.Repeat([]byte{0xAA}, 32)
	secretB := bytes.Repeat([]byte{0xBB}, 32)

	if backend, _, err := ksA.Save(secretA); err != nil || backend != "keychain" {
		t.Fatalf("save A: %q %v", backend, err)
	}
	if backend, _, err := ksB.Save(secretB); err != nil || backend != "keychain" {
		t.Fatalf("save B: %q %v", backend, err)
	}

	gotA, backend, err := ksA.Load()
	if err != nil || backend != "keychain" || !bytes.Equal(gotA, secretA) {
		t.Fatalf("load A: %q %v %x", backend, err, gotA)
	}
	gotB, backend, err := ksB.Load()
	if err != nil || backend != "keychain" || !bytes.Equal(gotB, secretB) {
		t.Fatalf("load B: %q %v %x", backend, err, gotB)
	}
}
