package relay

import (
	"testing"
	"time"
)

// A presence flood may use at most half of a recipient's send buffer, so mail
// and control frames still get through when the buffer is otherwise busy.
func TestDirectEphemeralLeavesHalfBufferForMail(t *testing.T) {
	// cap=8, half=4: directEphemeral checks occupancy before enqueueing, so it
	// admits while occupancy is at most half (5 sends: 0,1,2,3,4 all <=4), and
	// refuses once occupancy exceeds half (the 6th call, at occupancy 5).
	c := newConn(nil, "k", 8)
	for i := range 5 {
		if !c.directEphemeral([]byte("p")) {
			t.Fatalf("ephemeral frame %d refused with the buffer at most half full", i)
		}
	}
	if c.directEphemeral([]byte("p")) {
		t.Fatal("ephemeral frame accepted with the buffer over half full")
	}
	c.mu.Lock()
	c.draining = false
	c.mu.Unlock()
	if res := c.direct([]byte("mail")); res != directSent {
		t.Fatalf("mail direct result = %v, want directSent while presence is being dropped", res)
	}
}

func TestEphemeralLimiterWindow(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l := newEphemeralLimiter(3, func() time.Time { return now })
	for i := range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("envelope %d refused", i)
		}
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("4th envelope allowed")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("limit is per key, but b was refused")
	}
	now = now.Add(time.Minute)
	ok, report := l.allow("a")
	if !ok || report != 1 {
		t.Fatalf("new window: ok=%v report=%d, want true and 1 dropped reported", ok, report)
	}
}
