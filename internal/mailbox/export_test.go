package mailbox

import (
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// WrapBackends makes k wrap every keystore backend with w (tests count reads).
func WrapBackends(k *Keys, w func(keystore.Backend) keystore.Backend) {
	k.mu.Lock()
	k.wrap = w
	k.mu.Unlock()
}

// CachedKeys is the number of private keys k holds in memory.
func CachedKeys(k *Keys) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.cache)
}

// ReceivedMailAt records a received mail stamped at in mail_seen, the basis
// that key deletion by age measures from (R55-F28, review 100 M1).
func ReceivedMailAt(k *Keys, at time.Time) error {
	_, err := k.db.Exec(`INSERT INTO mail_seen (from_key, id, received_at) VALUES ('test-peer', ?, ?)`,
		at.UTC().Format(time.RFC3339Nano), at.UTC().Format(mail.StoreTimeFmt))
	return err
}
