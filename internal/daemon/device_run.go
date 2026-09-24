package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// sweepMu serialises sweeps started by kick.
	sweepMu sync.Mutex
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

// kick drops queued runs that are no longer allowed (their link ended, their
// scope was cleared or expired) at once, in the background, then wakes the
// runner. It never blocks, so it may be called inside a transaction: the
// sweep's own transaction waits for that one to end.
func (r *helperRunner) kick() {
	go func() {
		r.sweepMu.Lock()
		defer r.sweepMu.Unlock()
		ctx := context.Background()
		if _, _, err := r.take(ctx, false); err != nil && r.logger != nil {
			r.logger.Warn("device: sweep run queue", "error", err)
		}
		r.signal()
	}()
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
// (Docs/protocol/device.md §Limits).
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
	if len(dropped) == 0 && next == nil {
		return nil, device.RunPlan{}, nil
	}
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
// failed ("<name>: interrupted"), and queued runs are dropped with ws.cancel.
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
	st.Running, st.Queued = nil, nil
	if err := saveRunState(ctx, tx, st, r.ds.Time()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if running != nil {
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
func (r *helperRunner) execute(ctx context.Context, j runJob, plan device.RunPlan) {
	if v, err := r.ws.Get(ctx, j.Session); err != nil || v.State != worksession.StateOpen {
		// The controller cancelled meanwhile: nothing to run or report.
		_ = r.finish(ctx)
		return
	}
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
		// Changed on disk since the human approved it: "could not start".
		if r.logger != nil {
			r.logger.Warn("device: run target changed", "session", j.Session, "error", err)
		}
	} else {
		res = run(ctx, spec)
	}
	if ctx.Err() != nil {
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
		"duration_ms": res.Duration.Milliseconds(), "output_bytes": len(result.Output), "timed_out": res.TimedOut,
	})
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
