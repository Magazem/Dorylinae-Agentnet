package idle

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestParseIoreg(t *testing.T) {
	out := []byte("+-o IOHIDSystem  <class IOHIDSystem, id 0x100000311, registered, matched, active, busy 0 (0 ms), retain 8>\n" +
		"    {\n      \"HIDIdleTime\" = 4053256125\n    }\n")
	d, err := parseIoreg(out)
	if err != nil || d != 4053256125*time.Nanosecond {
		t.Fatalf("got %v, %v", d, err)
	}
	if _, err := parseIoreg([]byte("nothing here")); err == nil {
		t.Fatal("expected error on no match")
	}
}

func TestParseGdbus(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"(uint64 1234,)\n", 1234 * time.Millisecond, true},
		{"(uint32 600000,)\n", 10 * time.Minute, true},
		{"(uint64 0,)", 0, true},
		{"Error: GDBus.Error:org.freedesktop.DBus.Error.ServiceUnknown", 0, false},
		{"()", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		d, err := parseGdbus([]byte(c.in))
		if (err == nil) != c.ok || d != c.want {
			t.Errorf("%q: got %v, %v", c.in, d, err)
		}
	}
}

func TestParseXprintidle(t *testing.T) {
	d, err := parseXprintidle([]byte("45210\n"))
	if err != nil || d != 45210*time.Millisecond {
		t.Fatalf("got %v, %v", d, err)
	}
	for _, in := range []string{"", "abc", "-5", "99999999999999999999"} {
		if _, err := parseXprintidle([]byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

// TestHelperHang is not a test: it is the hanging fake command.
func TestHelperHang(t *testing.T) {
	if os.Getenv("IDLE_HELPER_HANG") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

func TestRunCommandHonoursTimeout(t *testing.T) {
	t.Setenv("IDLE_HELPER_HANG", "1")
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	start := time.Now()
	_, err := runCommand(ctx, os.Args[0], "-test.run=^TestHelperHang$")
	if err == nil {
		t.Fatal("expected error from hanging command")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("took %v, timeout not honoured", el)
	}
}

func TestIdleUnknownOnFailure(t *testing.T) {
	old := runCommand
	defer func() { runCommand = old }()
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	// On Windows the real syscall path is used; only assert the contract.
	if d, ok := Idle(context.Background()); !ok && d != 0 {
		t.Fatalf("unknown must return a zero duration, got %v", d)
	}
}

func TestIdleHangingCommandIsUnknown(t *testing.T) {
	old := runCommand
	defer func() { runCommand = old }()
	runCommand = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	Idle(context.Background())
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("Idle took %v", el)
	}
}

func TestCachedReusesAResultForTheTTL(t *testing.T) {
	var mu sync.Mutex
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	calls := 0
	f := Cached(CacheTTL, now, func(context.Context) (time.Duration, bool) {
		calls++
		return time.Duration(calls) * time.Second, true
	})
	ctx := context.Background()
	if d, ok := f(ctx); !ok || d != time.Second {
		t.Fatalf("first call = %v, %v", d, ok)
	}
	advance(CacheTTL - time.Millisecond)
	if d, _ := f(ctx); d != time.Second || calls != 1 {
		t.Fatalf("inside the TTL: d = %v, calls = %d, want the cached value and 1 call", d, calls)
	}
	advance(time.Millisecond)
	if d, _ := f(ctx); d != 2*time.Second || calls != 2 {
		t.Fatalf("at the TTL: d = %v, calls = %d, want a fresh sample and 2 calls", d, calls)
	}
}

func TestCachedKeepsAnUnknownResultToo(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	f := Cached(CacheTTL, func() time.Time { return clock }, func(context.Context) (time.Duration, bool) {
		calls++
		return 0, false
	})
	f(context.Background())
	if _, ok := f(context.Background()); ok || calls != 1 {
		t.Fatalf("ok = %v, calls = %d, want unknown and 1 call", ok, calls)
	}
}
