package relayclient

import "sync"

// seenCapacity is how many recent envelopes a Client remembers for duplicate
// suppression. The relay only redelivers envelopes it has not seen acked, so a
// window this size comfortably covers a reconnect mid-flush.
const seenCapacity = 8192

// seenSet remembers the most recent (sender, id) pairs, evicting the oldest.
type seenSet struct {
	mu   sync.Mutex
	set  map[string]struct{}
	ring []string
	next int
}

func newSeenSet(capacity int) *seenSet {
	return &seenSet{set: make(map[string]struct{}, capacity), ring: make([]string, capacity)}
}

// add records the envelope and reports whether it is new.
func (s *seenSet) add(from, id string) bool {
	k := from + "\x00" + id
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.set[k]; dup {
		return false
	}
	if old := s.ring[s.next]; old != "" {
		delete(s.set, old)
	}
	s.ring[s.next] = k
	s.next = (s.next + 1) % len(s.ring)
	s.set[k] = struct{}{}
	return true
}
