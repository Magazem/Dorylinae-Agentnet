package idle

import (
	"context"
	"errors"
	"os"
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
