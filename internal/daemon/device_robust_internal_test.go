package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 R55-098: a sweep that cannot re-check the running command (here
// the scope table cannot be read) stops it, as a revocation would, instead
// of letting it run to its timeout.
func TestHelperSweepStopsRunWhenCheckFails(t *testing.T) {
	e := newRouteEnv(t)
	ctx := context.Background()
	if !e.route(t, "C", "r-1", "test") {
		t.Fatal("not routed")
	}
	job, plan, err := e.r.take(ctx, true)
	if err != nil || job == nil {
		t.Fatalf("take: %v %v", job, err)
	}
	runCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	e.r.curMu.Lock()
	e.r.cur = &activeRun{session: job.Session, stop: stop, plan: plan, expires: plan.Expires}
	e.r.curMu.Unlock()

	e.r.sweep(ctx)
	if runCtx.Err() != nil {
		t.Fatal("a sweep that confirmed the run stopped it")
	}
	if _, err := e.db.Exec(`ALTER TABLE device_scopes RENAME TO device_scopes_gone`); err != nil {
		t.Fatal(err)
	}
	e.r.sweep(ctx)
	if !errors.Is(context.Cause(runCtx), errRunRevoked) {
		t.Fatalf("a sweep that could not check the run left it running: %v", context.Cause(runCtx))
	}
}

// Review 55 R55-099: a job left marked running by a finish that failed after
// its result does not wedge the queue: the next take starts the next queued
// run. While a command really runs, take still starts none.
func TestHelperTakeClearsRunLeftMarkedRunning(t *testing.T) {
	e := newRouteEnv(t)
	ctx := context.Background()
	for _, id := range []string{"r-1", "r-2"} {
		if !e.route(t, "C", id, "test") {
			t.Fatalf("%s not routed", id)
		}
	}
	first, _, err := e.r.take(ctx, true)
	if err != nil || first == nil || first.Request != "r-1" {
		t.Fatalf("first take: %+v %v", first, err)
	}
	// r-1 executes: nothing else starts.
	e.r.curMu.Lock()
	e.r.cur = &activeRun{session: first.Session, stop: func(error) {}}
	e.r.curMu.Unlock()
	if j, _, err := e.r.take(ctx, true); err != nil || j != nil {
		t.Fatalf("take while r-1 runs: %+v %v, want nothing", j, err)
	}
	// r-1 ended, but its finish failed: it is still marked running.
	e.r.curMu.Lock()
	e.r.cur = nil
	e.r.curMu.Unlock()
	next, _, err := e.r.take(ctx, true)
	if err != nil || next == nil || next.Request != "r-2" {
		t.Fatalf("take after a failed finish: %+v %v, want r-2", next, err)
	}
}

// Review 55 R55-145: the daemon waits for the sweeps kick started, and a
// kick once it stops starts none.
func TestHelperWaitAwaitsKickSweeps(t *testing.T) {
	e := newRouteEnv(t)
	e.r.sweepMu.Lock() // holds the kicked sweep
	e.r.kick()
	waited := make(chan struct{})
	go func() { defer close(waited); e.r.wait() }()
	select {
	case <-waited:
		e.r.sweepMu.Unlock()
		t.Fatal("wait returned while a kicked sweep was still running")
	case <-time.After(100 * time.Millisecond):
	}
	e.r.sweepMu.Unlock()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("wait did not return after the sweep ended")
	}
	e.r.sweepMu.Lock()
	defer e.r.sweepMu.Unlock()
	e.r.kick() // would block on sweepMu if it started a sweep
	done := make(chan struct{})
	go func() { defer close(done); e.r.wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a kick after wait started a sweep")
	}
}

// deviceIPC serves device_* and device_scope_* over a grant harness's
// stores, with this device the helper of the harness's peer.
type deviceIPC struct {
	*grantHarness
	ds       *device.Store
	endpoint string
}

func newDeviceIPC(t *testing.T) *deviceIPC {
	t.Helper()
	h, peer := newGrantHarness(t, "code", false)
	if _, err := h.db.Exec(`INSERT INTO device_links (id, peer, role, state, nonce, created, activated_at, updated)
		VALUES ('l-00000000000000000000000000000009', ?, 'helper', 'active', ?, '2026-10-01T08:00:00.000Z', '2026-10-01T08:00:00.000Z', '2026-10-01T08:00:00.000Z')`, peer, testNonce); err != nil {
		t.Fatal(err)
	}
	ds := &device.Store{DB: h.db, Self: h.self}
	dir := testutil.TempDir(t)
	p, err := paths.In(filepath.Join(dir, "ep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	runner := newHelperRunner(h.db, ds, h.ws, h.log, h.self, nil)
	t.Cleanup(runner.wait)
	pending := newScopeApprovals()
	srv := ipc.NewServer()
	registerDevice(srv, ds, h.appr, h.ps, h.ob, h.log, false, runner, pending, deviceHooks{})
	registerDeviceScope(srv, ds, h.appr, h.ps, runner, p.Dir, pending)
	sctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	return &deviceIPC{grantHarness: h, ds: ds, endpoint: p.Endpoint}
}

func (d *deviceIPC) call(method string, params, out any) error {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ipc.Call(ctx, d.endpoint, method, params, out)
}

// Review 55 R55-085: concurrent device_scope_set calls supersede each other
// in turn: however they interleave, one approval stays pending.
func TestDeviceScopeSetConcurrentSupersedes(t *testing.T) {
	d := newDeviceIPC(t)
	prog := privateProgram(t)
	repo := testutil.TempDir(t)
	scope := func(i int) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"types": []string{"task"}, "repos": []map[string]string{{"label": "r", "path": repo}},
			"commands": []map[string]any{{"name": fmt.Sprintf("c%d", i), "repo": "r", "argv": []string{prog}, "timeout_s": 10}},
			"expires":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var res DeviceScopeSetResult
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			errs[i] = ipc.Call(ctx, d.endpoint, "device_scope_set", DeviceScopeSetParams{Peer: d.peer, Scope: scope(i)}, &res)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("device_scope_set %d: %v", i, err)
		}
	}
	list, err := d.appr.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, v := range list {
		if v.Kind == approval.KindDeviceScope {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("%d device_scope approvals pending after %d concurrent sets, want 1", pending, n)
	}
}

// privateProgram copies a system program into a private directory, where
// the ownership check (review 40 L11) accepts it on every OS.
func privateProgram(t *testing.T) string {
	t.Helper()
	src, name := "/usr/bin/true", "true"
	if runtime.GOOS == "windows" {
		src, name = `C:\Windows\System32\whoami.exe`, "whoami.exe"
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(testutil.PrivateDir(t), name)
	if err := os.WriteFile(prog, raw, 0o700); err != nil { //nolint:gosec // a test program in the test's own private dir
		t.Fatal(err)
	}
	return prog
}

// Review 55 R55-085: take waits for a device_scope_set under way, so a clear
// or an unlink sees the approval it records.
func TestScopeApprovalsTakeWaitsForSet(t *testing.T) {
	s := newScopeApprovals()
	unlock := s.lock("C")
	got := make(chan string, 1)
	go func() { got <- s.take("C") }()
	select {
	case id := <-got:
		unlock()
		t.Fatalf("take returned %q while a set held the lock", id)
	case <-time.After(100 * time.Millisecond):
	}
	s.put("C", "a-new")
	unlock()
	select {
	case id := <-got:
		if id != "a-new" {
			t.Fatalf("take = %q, want the approval the set recorded", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("take did not return after the set released its lock")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sets) != 0 {
		t.Fatalf("set locks left behind: %d", len(s.sets))
	}
}

// Review 55 R55-161: a local device_unlink moves the peer's offer watermark,
// so an offer that peer made before the unlink and that arrives later cannot
// complete a re-link.
func TestDeviceLocalUnlinkMovesOfferWatermark(t *testing.T) {
	d := newDeviceIPC(t)
	ctx := context.Background()
	before := d.ds.Time().Add(-time.Minute).Truncate(time.Second)
	var res DeviceUnlinkResult
	if err := d.call("device_unlink", DeviceUnlinkParams{Peer: d.peer}, &res); err != nil {
		t.Fatal(err)
	}
	// The human re-links at once: a new helper intent, approved.
	l, err := d.ds.CreateIntent(ctx, d.peer, device.RoleHelper)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ds.ApproveTx(ctx, tx, l.ID, d.ds.Time()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	link := deviceLinkKind(d.ds, d.self, deviceHooks{})
	deliver := func(at time.Time) {
		t.Helper()
		body := map[string]any{"at": at.UTC().Format(time.RFC3339), "controller": d.peer, "helper": d.self, "nonce": testNonce, "role": "controller"}
		op := &mail.Opened{Msg: mail.Msg{From: d.peer, Kind: device.KindLink, Body: body}}
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := link.Apply(ctx, tx, op); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	active := func() int {
		t.Helper()
		var n int
		if err := d.db.QueryRow(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deliver(before) // made before the unlink, delivered late
	if n := active(); n != 0 {
		t.Fatal("an offer made before the local unlink completed the re-link")
	}
	deliver(d.ds.Time().Add(time.Second)) // made after it
	if n := active(); n != 1 {
		t.Fatal("an offer made after the local unlink did not complete the re-link")
	}
}
