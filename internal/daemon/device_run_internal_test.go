package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// routeEnv is a helper runner over a bare store: an active helper link with
// controller "C" and a scope allowing task/"test".
type routeEnv struct {
	db  *sql.DB
	r   *helperRunner
	now time.Time
}

func newRouteEnv(t *testing.T) *routeEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ds := &device.Store{DB: db, Self: "H", Now: func() time.Time { return now }}
	const link = "l-00000000000000000000000000000009"
	if _, err := db.Exec(`INSERT INTO device_links (id, peer, role, state, nonce, created, activated_at, updated) VALUES (?, 'C', 'helper', 'active', '00112233445566778899aabbccddeeff', '2026-10-01T08:00:00.000Z', '2026-10-01T08:00:00.000Z', '2026-10-01T08:00:00.000Z')`, link); err != nil {
		t.Fatal(err)
	}
	prog := "/usr/bin/true"
	if runtime.GOOS == "windows" {
		prog = `C:\Windows\System32\whoami.exe`
	}
	sc := device.Scope{Types: []string{"task"}, Repos: []device.Repo{{Label: "r", Path: filepath.Dir(prog)}},
		Commands: []device.Command{{Name: "test", Repo: "r", Argv: []string{prog}, TimeoutS: 10}}, Expires: now.Add(time.Hour).Format(time.RFC3339)}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.SetScopeTx(ctx, tx, link, sc, "a-1", now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return &routeEnv{db: db, r: newHelperRunner(db, ds, nil, audit.New(db), "H", nil), now: now}
}

func (e *routeEnv) route(t *testing.T, from, id, run string) bool {
	t.Helper()
	ctx := context.Background()
	req := &request.Request{ID: id, From: from, Type: "task", Created: e.now}
	if run != "" {
		req.Run = &request.Run{Command: run}
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	accept, after, err := e.r.RouteTx(ctx, tx, req, e.now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after != nil {
		after(ctx)
	}
	return accept
}

func (e *routeEnv) checkOf(t *testing.T, id string) string {
	t.Helper()
	var detail string
	err := e.db.QueryRow(`SELECT detail FROM audit_events WHERE action = 'device.out_of_scope' AND detail LIKE ?`, "%"+id+"%").Scan(&detail)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]string
	_ = json.Unmarshal([]byte(detail), &d)
	return d["check"]
}

// At most 60 runs per controller per 24 h: the 61st is out of scope with
// check "queue" (Docs/protocol/device.md §Limits), and the window slides.
func TestHelperRoutePerDayLimit(t *testing.T) {
	e := newRouteEnv(t)
	st := runState{Recent: map[string][]string{}}
	for i := 0; i < maxRunsPerDay; i++ {
		st.Recent["C"] = append(st.Recent["C"], e.now.Add(-time.Duration(i)*time.Minute).Format(runTimeFmt))
	}
	tx, _ := e.db.Begin()
	if err := saveRunState(context.Background(), tx, st, e.now); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	if e.route(t, "C", "r-61", "test") {
		t.Fatal("the 61st run in 24 h was accepted")
	}
	if c := e.checkOf(t, "r-61"); c != device.CheckQueue {
		t.Fatalf("check = %q, want queue", c)
	}
	// A day later the oldest runs fall out of the window.
	e.now = e.now.Add(24*time.Hour - 30*time.Minute)
	e.r.ds.Now = func() time.Time { return e.now }
	sc := `UPDATE device_scopes SET scope = json_set(scope, '$.expires', ?)`
	if _, err := e.db.Exec(sc, e.now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if !e.route(t, "C", "r-62", "test") {
		t.Fatalf("a run after the window slid was refused (check %q)", e.checkOf(t, "r-62"))
	}
}

// Routing: a normal request from a peer that is not this device's controller
// is left alone (no audit); anything from the controller or with run is
// checked, and an in-scope one is queued.
func TestHelperRouteAuditsOnlyDeviceRequests(t *testing.T) {
	e := newRouteEnv(t)
	if e.route(t, "X", "r-plain", "") || e.checkOf(t, "r-plain") != "" {
		t.Fatal("a normal request from another peer was routed or audited")
	}
	if e.route(t, "X", "r-x-run", "test") || e.checkOf(t, "r-x-run") != device.CheckLink {
		t.Fatalf("a run request from a non-controller: check %q", e.checkOf(t, "r-x-run"))
	}
	if e.route(t, "C", "r-c-plain", "") || e.checkOf(t, "r-c-plain") != device.CheckCommand {
		t.Fatalf("a request without run from the controller: check %q", e.checkOf(t, "r-c-plain"))
	}
	if !e.route(t, "C", "r-c-run", "test") {
		t.Fatalf("an in-scope request was not accepted (check %q)", e.checkOf(t, "r-c-run"))
	}
	var raw string
	if err := e.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, deviceRunsKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"request":"r-c-run"`) {
		t.Fatalf("the run was not queued: %s", raw)
	}
}

// The runner's result: pass only on exit 0, "timed out" and "could not
// start" are failures, and the summary never holds more than the name.
func TestRunResultShapes(t *testing.T) {
	cmd := device.Command{Name: "test", TimeoutS: 5}
	for _, tc := range []struct {
		res     device.RunResult
		status  string
		summary string
		code    *int64
	}{
		{device.RunResult{Started: true, ExitCode: 0, Duration: 1500 * time.Millisecond}, "pass", "test: exit 0 in 1.5s", ptr(0)},
		{device.RunResult{Started: true, ExitCode: 3, Duration: 200 * time.Millisecond}, "fail", "test: exit 3 in 200ms", ptr(3)},
		{device.RunResult{Started: true, TimedOut: true, ExitCode: 1}, "fail", "test: timed out after 5 s", nil},
		{device.RunResult{Started: false}, "fail", "test: could not start", nil},
	} {
		r := runResult(cmd, tc.res)
		if r.Status != tc.status || r.Summary != tc.summary || r.Verification != "none" ||
			(tc.code == nil) != (r.ExitCode == nil) || (tc.code != nil && *tc.code != *r.ExitCode) {
			t.Errorf("runResult(%+v) = %+v", tc.res, r)
		}
	}
}

func ptr(v int64) *int64 { return &v }

// Review 40 M1: a bidi override or zero-width character in a repo path or an
// argv reaches the approval summary escaped, never raw, so the human reads
// what will run.
func TestScopeSummaryEscapesInvisibleCharacters(t *testing.T) {
	sc := device.Scope{
		Types:    []string{"task"},
		Repos:    []device.Repo{{Label: "r", Path: "/srv/re" + string(rune(0x200b)) + "po"}},
		Commands: []device.Command{{Name: "t", Repo: "r", Argv: []string{"/bin/tool", "--x" + string(rune(0x202e)) + "-- fr- mr"}, TimeoutS: 5}},
		Expires:  "2026-10-01T00:00:00Z",
	}
	s := scopeSummary("laptop", sc)
	if strings.ContainsRune(s, 0x200b) || strings.ContainsRune(s, 0x202e) {
		t.Fatalf("summary holds a raw invisible character: %q", s)
	}
	if !strings.Contains(s, "\"/srv/re\\u200bpo\"") || !strings.Contains(s, "[\"/bin/tool\",\"--x\\u202e-- fr- mr\"]") {
		t.Fatalf("summary does not show the escaped path and argv: %s", s)
	}
}
