package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Own-device helper runs (Docs/protocol/device.md §Running, §Limits): the
// in-scope checks on a received request, the queue, and the runner that
// executes one allowlisted command at a time and submits its D14 result
// through the work session.

// Limits (Docs/protocol/device.md §Limits).
const (
	maxQueuedRuns = 8
	maxRunsPerDay = 60
	runWindow     = 24 * time.Hour
)

// runWatchEvery bounds the time between two re-checks of the running
// command's scope, besides the one at its expiry, so that a changed clock is
// noticed too (review 40 L1).
const runWatchEvery = time.Minute

// errRunRevoked is the cause of a running command's context when its link
// ended or its scope was cleared, replaced without it, or expired.
var errRunRevoked = errors.New("device: the run is no longer allowed")

// activeRun is the command being run: its session, what stops it, what it
// runs and when its scope expires (kept current by each sweep, so a scope
// replaced with a later expiry moves the watch's next check).
type activeRun struct {
	session string
	stop    context.CancelCauseFunc
	plan    device.RunPlan
	expires time.Time
}

// deviceRunsKey is the settings row holding the run queue, so that a restart
// can drop queued runs and report an executing one as interrupted.
const deviceRunsKey = "device.runs"

const runTimeFmt = "2006-01-02T15:04:05.000Z"

// runJob is one queued or running helper run. Command is the command name;
// what it runs is looked up in the scope again when the run starts, so a
// scope replaced or cleared in between is honoured.
type runJob struct {
	Peer    string `json:"peer"`
	Request string `json:"request"`
	Session string `json:"session"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Created string `json:"created"`
}

// runState is the stored queue. Recent holds, per controller, the times of
// the runs accepted in the last 24 hours.
type runState struct {
	Running *runJob             `json:"running,omitempty"`
	Queued  []runJob            `json:"queued"`
	Recent  map[string][]string `json:"recent,omitempty"`
}

func loadRunState(ctx context.Context, tx *sql.Tx) (runState, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, deviceRunsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return runState{}, nil
	}
	if err != nil {
		return runState{}, fmt.Errorf("device: read run queue: %w", err)
	}
	var st runState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		// An unreadable row must not block receiving requests (the mail
		// apply would fail and be retried forever): start from an empty
		// queue, which the next save overwrites.
		return runState{}, nil
	}
	return st, nil
}

func saveRunState(ctx context.Context, tx *sql.Tx, st runState, now time.Time) error {
	if st.Queued == nil {
		st.Queued = []runJob{}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated = excluded.updated`,
		deviceRunsKey, string(raw), now.UTC().Format(runTimeFmt)); err != nil {
		return fmt.Errorf("device: write run queue: %w", err)
	}
	return nil
}

// pruneRecent drops run times older than runWindow.
func (st *runState) pruneRecent(now time.Time) {
	cut := now.Add(-runWindow)
	for peer, times := range st.Recent {
		var keep []string
		for _, s := range times {
			if t, err := time.Parse(runTimeFmt, s); err == nil && t.After(cut) {
				keep = append(keep, s)
			}
		}
		if len(keep) == 0 {
			delete(st.Recent, peer)
		} else {
			st.Recent[peer] = keep
		}
	}
}

// helperRunner routes received requests and runs in-scope ones.
type helperRunner struct {
	db   *sql.DB
	ds   *device.Store
	ws   *worksession.Store
	log  *audit.Log
	self string
	// exec runs one command; tests may replace it. nil uses device.Run.
	exec func(context.Context, device.RunSpec) device.RunResult
	// lookupEnv reads the daemon's environment; nil uses os.LookupEnv.
	lookupEnv func(string) (string, bool)
	logger    *slog.Logger

	wake chan struct{}
	// sweepMu serialises sweeps.
	sweepMu sync.Mutex
	// watchEvery overrides runWatchEvery (tests).
	watchEvery time.Duration
	// curMu guards cur, the command running now (nil when none).
	curMu sync.Mutex
	cur   *activeRun
}

func newHelperRunner(db *sql.DB, ds *device.Store, ws *worksession.Store, log *audit.Log, self string, logger *slog.Logger) *helperRunner {
	return &helperRunner{db: db, ds: ds, ws: ws, log: log, self: self, logger: logger, wake: make(chan struct{}, 1)}
}

// signal wakes the runner loop without blocking.
func (r *helperRunner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// kick sweeps at once, in the background: queued runs that are no longer
// allowed (their link ended, their scope was cleared or expired) are dropped
// and a running one is stopped. It never blocks, so it may be called inside
// a transaction: the sweep's own transaction waits for that one to end.
func (r *helperRunner) kick() {
	go r.sweep(context.Background())
}

// sweep drops the queued runs that are no longer allowed, stops the running
// one if it is no longer allowed, and wakes the runner.
func (r *helperRunner) sweep(ctx context.Context) {
	r.sweepMu.Lock()
	defer r.sweepMu.Unlock()
	if _, _, err := r.take(ctx, false); err != nil && r.logger != nil {
		r.logger.Warn("device: sweep run queue", "error", err)
	}
	r.signal()
}

// stopRunning stops the running command if it is still the one of session:
// its context ends with errRunRevoked, and device.Run kills its process tree.
func (r *helperRunner) stopRunning(session string) {
	r.curMu.Lock()
	defer r.curMu.Unlock()
	if r.cur != nil && r.cur.session == session {
		r.cur.stop(errRunRevoked)
	}
}

// stillRunning applies a sweep's plan for the running command of session: a
// scope replaced so that the command's name now stands for another program,
// arguments, environment or repo stops it (the human approved the new
// command, not the one running, review 41 L2); otherwise its expiry is
// updated for watch.
func (r *helperRunner) stillRunning(session string, plan device.RunPlan) {
	r.curMu.Lock()
	defer r.curMu.Unlock()
	if r.cur == nil || r.cur.session != session {
		return
	}
	old := r.cur.plan
	if old.Dir != plan.Dir || !slices.Equal(old.Command.Argv, plan.Command.Argv) || !slices.Equal(old.Command.Env, plan.Command.Env) {
		r.cur.stop(errRunRevoked)
		return
	}
	r.cur.expires = plan.Expires
}

// runExpires is when the running command's scope expires, as of the last
// sweep.
func (r *helperRunner) runExpires() time.Time {
	r.curMu.Lock()
	defer r.curMu.Unlock()
	if r.cur == nil {
		return time.Time{}
	}
	return r.cur.expires
}

// RouteTx implements request.HelperRouter (Docs/protocol/device.md §Running):
// a request is in scope when the sender is this device's controller, the
// scope allows its type and names its command, it was created after the link
// became active, and the limits allow one more run. A request that is not in
// scope stays in the normal inbox. The out-of-scope reason is audited only
// for a request that names a command or comes from this device's controller;
// any other request is simply a normal request.
func (r *helperRunner) RouteTx(ctx context.Context, tx *sql.Tx, req *request.Request, _ time.Time) (bool, func(context.Context), error) {
	now := r.ds.Time()
	run := ""
	if req.Run != nil {
		run = req.Run.Command
	}
	if run == "" {
		if _, ok, err := r.ds.ActiveHelperLinkTx(ctx, tx, req.From); err != nil || !ok {
			return false, nil, err
		}
	}
	plan, check, err := r.ds.CheckRunTx(ctx, tx, req.From, req.Type, run, req.Created, now)
	if err != nil {
		return false, nil, err
	}
	var st runState
	if check == "" {
		if st, err = loadRunState(ctx, tx); err != nil {
			return false, nil, err
		}
		st.pruneRecent(now)
		if len(st.Queued) >= maxQueuedRuns || len(st.Recent[req.From]) >= maxRunsPerDay {
			check = device.CheckQueue
		}
	}
	if check != "" {
		reqID, peer := req.ID, req.From
		return false, func(ctx context.Context) {
			r.audit(ctx, "device.out_of_scope", map[string]any{"request": reqID, "peer": peer, "check": check})
		}, nil
	}
	job := runJob{
		Peer: req.From, Request: req.ID, Session: worksession.DeriveID(req.From, r.self, req.ID),
		Type: req.Type, Command: plan.Command.Name, Created: req.Created.UTC().Format(time.RFC3339),
	}
	st.Queued = append(st.Queued, job)
	if st.Recent == nil {
		st.Recent = map[string][]string{}
	}
	st.Recent[req.From] = append(st.Recent[req.From], now.UTC().Format(runTimeFmt))
	if err := saveRunState(ctx, tx, st, now); err != nil {
		return false, nil, err
	}
	return true, func(context.Context) { r.signal() }, nil
}

func (r *helperRunner) audit(ctx context.Context, action string, detail map[string]any) {
	if r.log != nil {
		_ = r.log.Append(ctx, audit.ActorDaemon, action, detail)
	}
}

// valid re-applies the in-scope checks to a queued job through tx and
// returns what it runs now.
func (r *helperRunner) valid(ctx context.Context, tx *sql.Tx, j runJob, now time.Time) (device.RunPlan, bool, error) {
	created, err := time.Parse(time.RFC3339, j.Created)
	if err != nil {
		return device.RunPlan{}, false, nil
	}
	plan, check, err := r.ds.CheckRunTx(ctx, tx, j.Peer, j.Type, j.Command, created, now)
	if err != nil {
		return device.RunPlan{}, false, err
	}
	return plan, check == "", nil
}

// take drops every queued job that is no longer allowed and, if start is set
// and nothing is running, moves the first allowed one to running. Dropped
// jobs whose session is still open get a ws.cancel with no reason
// (Docs/protocol/device.md §Limits). A running job that is no longer allowed
// is stopped (stopRunning); execute then reports it the same way.
func (r *helperRunner) take(ctx context.Context, start bool) (*runJob, device.RunPlan, error) {
	now := r.ds.Time()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, device.RunPlan{}, fmt.Errorf("device: begin run queue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	st, err := loadRunState(ctx, tx)
	if err != nil {
		return nil, device.RunPlan{}, err
	}
	stop := ""
	var runningPlan device.RunPlan
	if st.Running != nil {
		p, ok, err := r.valid(ctx, tx, *st.Running, now)
		if err != nil {
			return nil, device.RunPlan{}, err
		}
		if !ok {
			stop = st.Running.Session
		}
		runningPlan = p
	}
	var keep, dropped []runJob
	var next *runJob
	var plan device.RunPlan
	for _, j := range st.Queued {
		p, ok, err := r.valid(ctx, tx, j, now)
		if err != nil {
			return nil, device.RunPlan{}, err
		}
		switch {
		case !ok:
			dropped = append(dropped, j)
		case start && next == nil && st.Running == nil:
			jj := j
			next, plan = &jj, p
		default:
			keep = append(keep, j)
		}
	}
	if len(dropped) > 0 || next != nil {
		st.Queued = keep
		if next != nil {
			st.Running = next
		}
		if err := saveRunState(ctx, tx, st, now); err != nil {
			return nil, device.RunPlan{}, err
		}
		if err := tx.Commit(); err != nil {
			return nil, device.RunPlan{}, fmt.Errorf("device: commit run queue: %w", err)
		}
	} else {
		_ = tx.Rollback()
	}
	switch {
	case stop != "":
		r.stopRunning(stop)
	case st.Running != nil && next == nil:
		r.stillRunning(st.Running.Session, runningPlan)
	}
	for _, j := range dropped {
		r.cancel(ctx, j)
	}
	return next, plan, nil
}

// cancel sends ws.cancel (no reason) for a dropped run whose session is
// still open, so the controller's daemon closes it.
func (r *helperRunner) cancel(ctx context.Context, j runJob) {
	v, err := r.ws.Get(ctx, j.Session)
	if err != nil || v.State != worksession.StateOpen {
		return
	}
	if _, _, _, err := r.ws.SubmitCancel(ctx, j.Session, ""); err != nil && r.logger != nil {
		r.logger.Warn("device: cancel dropped run", "session", j.Session, "error", err)
	}
}

// finish clears the running job.
func (r *helperRunner) finish(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := loadRunState(ctx, tx)
	if err != nil {
		return err
	}
	st.Running = nil
	if err := saveRunState(ctx, tx, st, r.ds.Time()); err != nil {
		return err
	}
	return tx.Commit()
}

// recoverAfterRestart applies Docs/protocol/device.md §Limits to what a
// previous run of the daemon left: a run that was executing is reported as
// failed ("<name>: interrupted"), unless it is no longer allowed (then
// ws.cancel, like a run stopped on revoke), and queued runs are dropped with
// ws.cancel.
func (r *helperRunner) recoverAfterRestart(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := loadRunState(ctx, tx)
	if err != nil {
		return err
	}
	running, queued := st.Running, st.Queued
	if running == nil && len(queued) == 0 {
		return nil
	}
	revoked := false
	if running != nil {
		// Stopped because it was no longer allowed, but the daemon stopped
		// before reporting it: report it as such (review 41 L1).
		_, ok, err := r.valid(ctx, tx, *running, r.ds.Time())
		if err != nil {
			return err
		}
		revoked = !ok
	}
	st.Running, st.Queued = nil, nil
	if err := saveRunState(ctx, tx, st, r.ds.Time()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	switch {
	case running != nil && revoked:
		r.cancel(ctx, *running)
	case running != nil:
		res := &worksession.Result{Status: "fail", Summary: running.Command + ": interrupted", Verification: worksession.VerificationNone}
		if _, _, err := r.ws.SubmitResult(ctx, running.Peer, running.Request, res); err != nil && r.logger != nil {
			r.logger.Warn("device: report interrupted run", "session", running.Session, "error", err)
		}
	}
	for _, j := range queued {
		r.cancel(ctx, j)
	}
	return nil
}

// loop runs queued jobs one at a time until ctx ends.
func (r *helperRunner) loop(ctx context.Context) {
	for {
		for ctx.Err() == nil {
			job, plan, err := r.take(ctx, true)
			if err != nil {
				if r.logger != nil {
					r.logger.Warn("device: run queue", "error", err)
				}
				break
			}
			if job == nil {
				break
			}
			r.execute(ctx, *job, plan)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}
	}
}

// execute runs one job and submits its result. A run cut short by the daemon
// stopping is left marked running, so the next start reports it interrupted.
// A run stopped because it is no longer allowed (review 40 L1) is reported
// like a dropped queued run: ws.cancel with no reason, and no result.
func (r *helperRunner) execute(ctx context.Context, j runJob, plan device.RunPlan) {
	if v, err := r.ws.Get(ctx, j.Session); err != nil || v.State != worksession.StateOpen {
		// The controller cancelled meanwhile: nothing to run or report.
		_ = r.finish(ctx)
		return
	}
	runCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	r.curMu.Lock()
	r.cur = &activeRun{session: j.Session, stop: stop, plan: plan, expires: plan.Expires}
	r.curMu.Unlock()
	watched := make(chan struct{})
	go func() { defer close(watched); r.watch(ctx, runCtx) }()
	defer func() {
		stop(nil)
		<-watched
		r.curMu.Lock()
		r.cur = nil
		r.curMu.Unlock()
	}()
	cmd := plan.Command
	spec := device.RunSpec{
		Path: cmd.Argv[0], Args: append([]string(nil), cmd.Argv...), Dir: plan.Dir,
		Env: device.MinimalEnv(cmd.Env, r.lookupEnv), Timeout: time.Duration(cmd.TimeoutS) * time.Second,
	}
	run := r.exec
	if run == nil {
		run = device.Run
	}
	var res device.RunResult
	if err := device.CheckTarget(spec.Path, spec.Dir); err != nil {
		// Changed on disk since the human approved it, or others can now
		// change the program: "could not start".
		if r.logger != nil {
			r.logger.Warn("device: run target changed", "session", j.Session, "error", err)
		}
	} else if runCtx.Err() == nil {
		res = run(runCtx, spec)
	}
	if ctx.Err() != nil {
		return
	}
	if errors.Is(context.Cause(runCtx), errRunRevoked) {
		if err := r.finish(ctx); err != nil && r.logger != nil {
			r.logger.Warn("device: finish run", "error", err)
		}
		r.cancel(ctx, j)
		r.audit(ctx, "device.run", map[string]any{
			"link": plan.Link.ID, "request": j.Request, "session": j.Session,
			"duration_ms": res.Duration.Milliseconds(), "output_bytes": 0, "timed_out": false, "cancelled": true,
		})
		return
	}
	result := runResult(cmd, res)
	for {
		_, _, err := r.ws.SubmitResult(ctx, j.Peer, j.Request, result)
		var tl *worksession.TooLargeResultError
		if errors.As(err, &tl) && result.Output != "" {
			result.Output = trimFront(result.Output)
			continue
		}
		if err != nil && r.logger != nil {
			r.logger.Warn("device: submit run result", "session", j.Session, "error", err)
		}
		break
	}
	if err := r.finish(ctx); err != nil && r.logger != nil {
		r.logger.Warn("device: finish run", "error", err)
	}
	r.audit(ctx, "device.run", map[string]any{
		"link": plan.Link.ID, "request": j.Request, "session": j.Session,
		"duration_ms": res.Duration.Milliseconds(), "output_bytes": len(result.Output), "timed_out": res.TimedOut, "cancelled": false,
	})
}

// watch re-checks the running command's scope until runCtx ends: at once
// (a revocation that committed just before the run was registered in cur),
// when the scope expires, and at least every watchEvery. A check that fails
// stops the run (sweep → take → stopRunning). The expiry is the one the last
// sweep saw (stillRunning): with the plan's own, a scope replaced with a
// later expiry made every wait after the old one 10 ms (review 41 M3). An
// expiry already past that the sweep did not act on (a failed sweep) waits
// at most a second, never less.
func (r *helperRunner) watch(ctx, runCtx context.Context) {
	every := r.watchEvery
	if every <= 0 {
		every = runWatchEvery
	}
	for {
		r.sweep(ctx)
		wait := every
		switch d := r.runExpires().Sub(r.ds.Time()); {
		case d <= 0:
			wait = min(every, time.Second)
		case d < wait:
			// Just past the expiry, so the check sees it expired.
			wait = d + time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-runCtx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// runResult builds the D14 result of a run (Docs/protocol/device.md
// §Running): pass on exit 0, fail otherwise, never partial.
func runResult(cmd device.Command, res device.RunResult) *worksession.Result {
	out := &worksession.Result{Verification: worksession.VerificationNone, Output: res.Output}
	switch {
	case !res.Started:
		out.Status = "fail"
		out.Summary = cmd.Name + ": could not start"
	case res.TimedOut:
		out.Status = "fail"
		out.Summary = fmt.Sprintf("%s: timed out after %d s", cmd.Name, cmd.TimeoutS)
	default:
		code := int64(res.ExitCode)
		if code < 0 {
			code = 1
		}
		if code > 4294967295 {
			code = 4294967295
		}
		out.ExitCode = &code
		out.Status = "fail"
		if code == 0 {
			out.Status = "pass"
		}
		out.Summary = fmt.Sprintf("%s: exit %d in %s", cmd.Name, code, res.Duration.Round(100*time.Millisecond))
	}
	return out
}

// trimFront drops the first quarter of s at a UTF-8 boundary, keeping the
// tail, for an output that is within 32 KiB but whose JSON escaping makes
// the whole result too large.
func trimFront(s string) string {
	cut := len(s) / 4
	if cut == 0 {
		return ""
	}
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return strings.Clone(s[cut:])
}
