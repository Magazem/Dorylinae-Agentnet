//go:build linux

package notify

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type fakeDBusNotifier struct {
	calls    int
	summary  string
	body     string
	closed   []uint32
	returnID uint32
	failNext bool
}

func (f *fakeDBusNotifier) Notify(_ context.Context, appName string, replacesID uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expireMS int32) (uint32, error) {
	f.calls++
	f.summary, f.body = summary, body
	if f.failNext {
		return 0, context.DeadlineExceeded
	}
	f.returnID++
	return f.returnID, nil
}

func (f *fakeDBusNotifier) CloseNotification(_ context.Context, id uint32) error {
	f.closed = append(f.closed, id)
	return nil
}

// TestShowApprovalInProcessNoSubprocess proves the Linux approval path never
// spawns a process (no gdbus/notify-send fallback for approvals,
// Docs/protocol/approval.md §Delivering the code): it goes entirely through
// the notifyIface function-call boundary, so a code held only in Go string
// arguments never touches any argv.
func TestShowApprovalInProcessNoSubprocess(t *testing.T) {
	orig := notifyIface
	defer func() { notifyIface = orig }()
	fake := &fakeDBusNotifier{}
	notifyIface = fake
	origRun := run
	defer func() { run = origRun }()
	run = func(_ context.Context, name string, _ []string, _ []string) error {
		t.Fatalf("approval path spawned %s", name)
		return nil
	}

	expires := time.Now().Add(10 * time.Minute)
	if err := showApproval(context.Background(), "a-1", expires, "AgentNet approval", "summary Code 482913"); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1", fake.calls)
	}
	if fake.body != "summary Code 482913" {
		t.Fatalf("body = %q", fake.body)
	}
	removeApproval(context.Background(), "a-1")
	if len(fake.closed) != 1 || fake.closed[0] != 1 {
		t.Fatalf("closed = %v, want [1]", fake.closed)
	}
}

func TestShowApprovalFailurePropagates(t *testing.T) {
	orig := notifyIface
	defer func() { notifyIface = orig }()
	notifyIface = &fakeDBusNotifier{failNext: true}
	if err := showApproval(context.Background(), "a-2", time.Now().Add(time.Minute), "t", "b"); err == nil {
		t.Fatal("expected an error, no fallback for approvals")
	}
}

// TestShowApprovalEscapesMarkup: peer-supplied text in the body cannot use
// notification markup to hide the real code or show a fake one (review 26,
// M-3).
func TestShowApprovalEscapesMarkup(t *testing.T) {
	orig := notifyIface
	defer func() { notifyIface = orig }()
	fake := &fakeDBusNotifier{}
	notifyIface = fake
	body := `grant to <span size="1">bob</span> & co? Code 482913`
	if err := showApproval(context.Background(), "a-3", time.Now().Add(time.Minute), "AgentNet approval", body); err != nil {
		t.Fatal(err)
	}
	want := `grant to &lt;span size="1"&gt;bob&lt;/span&gt; &amp; co? Code 482913`
	if fake.body != want {
		t.Fatalf("body = %q, want %q", fake.body, want)
	}
}
