package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// offerEnv is the helper side ("H") of a link attempt with controller "C",
// driven through the device kinds directly: a relay that delays or reorders
// mail cannot be staged in the e2e harness.
type offerEnv struct {
	t      *testing.T
	db     *sql.DB
	ds     *device.Store
	now    time.Time
	mu     sync.Mutex
	linked []string // "peer/role" from the onLinked hook
}

func newOfferEnv(t *testing.T) *offerEnv {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &offerEnv{t: t, db: st.DB(), now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	e.ds = &device.Store{DB: st.DB(), Self: "H", Now: func() time.Time { return e.now }}
	return e
}

func (e *offerEnv) hooks() deviceHooks {
	return deviceHooks{onLinked: func(_ context.Context, peer, role string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.linked = append(e.linked, peer+"/"+role)
	}}
}

// intent creates a waiting helper intent with C.
func (e *offerEnv) intent() {
	e.t.Helper()
	ctx := context.Background()
	l, err := e.ds.CreateIntent(ctx, "C", device.RoleHelper)
	if err != nil {
		e.t.Fatal(err)
	}
	tx, _ := e.db.BeginTx(ctx, nil)
	if _, err := e.ds.ApproveTx(ctx, tx, l.ID, e.now); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

// deliver applies one mail from C through kind k, then its After hook.
func (e *offerEnv) deliver(k mail.Kind, kind string, body map[string]any) {
	e.t.Helper()
	ctx := context.Background()
	op := &mail.Opened{Msg: mail.Msg{From: "C", Kind: kind, Body: body}}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := k.Apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		e.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
	if k.After != nil {
		k.After(ctx, op)
	}
}

func (e *offerEnv) offer(at time.Time) map[string]any {
	return map[string]any{"at": at.UTC().Format(time.RFC3339), "controller": "C", "helper": "H", "nonce": testNonce, "role": "controller"}
}

func (e *offerEnv) count(q string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// D22 (review 36 L4): an offer delayed past its sender's 10 minutes is
// neither kept nor used, even though the local intent is alive; a fresh one
// activates and the activation is announced (L5).
func TestDeviceDelayedOfferIgnored(t *testing.T) {
	e := newOfferEnv(t)
	e.intent()
	k := deviceLinkKind(e.ds, "H", e.hooks())
	e.deliver(k, device.KindLink, e.offer(e.now.Add(-device.IntentTTL-time.Second)))
	if n := e.count(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`); n != 0 {
		t.Fatal("a delayed offer activated the link")
	}
	if n := e.count(`SELECT COUNT(*) FROM device_offers`); n != 0 {
		t.Fatal("a delayed offer was kept")
	}
	e.deliver(k, device.KindLink, e.offer(e.now.Add(-time.Minute)))
	if n := e.count(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`); n != 1 {
		t.Fatal("a fresh offer did not activate")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.linked) != 1 || e.linked[0] != "C/helper" {
		t.Fatalf("onLinked calls = %v, want one for C as helper", e.linked)
	}
}

// D22 (review 36 L4): an offer made before the last device.unlink received
// from that peer is ignored, even when it arrives after the unlink and a new
// local intent exists.
func TestDeviceOfferBeforeUnlinkIgnored(t *testing.T) {
	e := newOfferEnv(t)
	link := deviceLinkKind(e.ds, "H", e.hooks())
	unlink := deviceUnlinkKind(e.ds, nil)
	offerAt := e.now.Add(-2 * time.Minute)
	e.deliver(unlink, device.KindUnlink, map[string]any{"at": e.now.Add(-time.Minute).UTC().Format(time.RFC3339)})
	e.intent()
	e.deliver(link, device.KindLink, e.offer(offerAt)) // reordered: made before the unlink
	if n := e.count(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`); n != 0 {
		t.Fatal("an offer from before the unlink activated the link")
	}
	if n := e.count(`SELECT COUNT(*) FROM device_offers`); n != 0 {
		t.Fatal("an offer from before the unlink was kept")
	}
	e.deliver(link, device.KindLink, e.offer(e.now)) // made after it
	if n := e.count(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`); n != 1 {
		t.Fatal("an offer made after the unlink did not activate")
	}
}
