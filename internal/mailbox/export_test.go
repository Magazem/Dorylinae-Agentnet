package mailbox

import "github.com/Magazem/Dorylinae-Agentnet/internal/keystore"

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
