package notify

import (
	"testing"
	"time"
)

func TestNextRetrySchedule(t *testing.T) {
	noJitter := func() float64 { return 1 }
	want := []time.Duration{
		10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour,
	}
	for i, w := range want {
		delay, ok := nextRetry(i+1, 0, noJitter)
		if !ok {
			t.Fatalf("attempt %d: ok = false, want true", i+1)
		}
		if delay != w {
			t.Errorf("attempt %d: delay = %v, want %v", i+1, delay, w)
		}
	}
	if _, ok := nextRetry(7, 0, noJitter); ok {
		t.Error("attempt 7 should exhaust retries (ok = false)")
	}
}

func TestNextRetryHonoursRetryAfterCappedAt6h(t *testing.T) {
	noJitter := func() float64 { return 1 }
	delay, ok := nextRetry(1, 3*time.Hour, noJitter)
	if !ok || delay != 3*time.Hour {
		t.Fatalf("delay = %v, ok = %v, want 3h, true", delay, ok)
	}
	delay, ok = nextRetry(1, 100*time.Hour, noJitter)
	if !ok || delay != retryDelayCap {
		t.Fatalf("delay = %v, ok = %v, want %v (capped), true", delay, ok, retryDelayCap)
	}
}

func TestDefaultJitterRange(t *testing.T) {
	for i := 0; i < 200; i++ {
		v := defaultJitter()
		if v < 0.9 || v >= 1.1 {
			t.Fatalf("jitter %v out of [0.9, 1.1)", v)
		}
	}
}
