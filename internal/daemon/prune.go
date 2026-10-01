package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/retention"
)

// CodeApprovalRejected is data_prune's answer for an approval the human
// rejected (Docs/protocol/retention.md §IPC).
const CodeApprovalRejected = "approval_rejected"

// pruneAuthTTL is how long an approved prune may run its calls
// (Docs/protocol/retention.md §Approval): long enough for the CLI's loop, and
// in memory only, so a restart ends it.
const pruneAuthTTL = time.Hour

// DataPruneParams are the params of "data_prune".
type DataPruneParams struct {
	OlderThanS *json.Number `json:"older_than_s"`
	DryRun     bool         `json:"dry_run,omitempty"`
	Approval   string       `json:"approval,omitempty"`
}

// DataPruneResult is the result of "data_prune". Approval is set while the
// removal waits for the human; Counts is then what the approval shows.
type DataPruneResult struct {
	Cutoff   string           `json:"cutoff"`
	DryRun   bool             `json:"dry_run"`
	Counts   retention.Counts `json:"counts"`
	More     bool             `json:"more"`
	Approval *approval.View   `json:"approval,omitempty"`
}

// pruneAuth is one prune the human approved (or is asked to): the cutoff is
// fixed when the command runs, so every call of the loop removes against the
// same instant the human saw.
type pruneAuth struct {
	olderThan time.Duration
	cutoff    time.Time
	counts    retention.Counts // what the approval shows
	removed   retention.Counts // what the calls removed so far
	approved  bool
	until     time.Time // set on approval
}

// pruneAuths holds the prunes by approval id, in memory only. run
// serialises the batches, so each reads what the earlier ones removed.
type pruneAuths struct {
	mu  sync.Mutex
	run sync.Mutex
	m   map[string]*pruneAuth
}

func (a *pruneAuths) get(id string) (pruneAuth, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.m[id]
	if !ok {
		return pruneAuth{}, false
	}
	return *p, true
}

func (a *pruneAuths) set(id string, p *pruneAuth) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[id] = p
}

func (a *pruneAuths) approve(id string, until time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.m[id]; ok {
		p.approved, p.until = true, until
	}
}

func (a *pruneAuths) addRemoved(id string, c retention.Counts) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.m[id]; ok {
		p.removed = p.removed.Add(c)
	}
}

func (a *pruneAuths) drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.m, id)
}

func pruneFacts(olderThan time.Duration, cutoff time.Time, c retention.Counts) approvaltext.Prune {
	return approvaltext.Prune{
		OlderThan: olderThan, Cutoff: cutoff, Requests: c.Requests, WorkSessions: c.WorkSessions,
		Grants: c.Grants, Debates: c.Debates, DebateEntries: c.DebateEntries,
		DebateConstraints: c.DebateConstraints, ExperienceRecords: c.ExperienceRecords,
		MailInbox: c.MailInbox, InboxBlanked: c.InboxBlanked,
	}
}

// olderThanParam validates older_than_s: an integer number of seconds, at
// least 35 days (Docs/protocol/retention.md §IPC).
func olderThanParam(n *json.Number) (time.Duration, error) {
	bad := &ipc.Error{Code: ipc.CodeBadRequest, Message: "older_than_s must be an integer number of seconds, at least 3024000 (35 days)"}
	if n == nil {
		return 0, bad
	}
	s, err := n.Int64()
	if err != nil || s < int64(retention.MinOlderThan/time.Second) || s > int64((100*365*24*time.Hour)/time.Second) {
		return 0, bad
	}
	return time.Duration(s) * time.Second, nil
}

// plainInteger reports whether a JSON token is an unquoted run of digits.
func plainInteger(tok json.RawMessage) bool {
	if len(tok) == 0 {
		return false
	}
	for _, c := range tok {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func newPruneSubject() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "n-" + hex.EncodeToString(b[:]), nil
}

// registerPrune wires "data_prune" (Docs/protocol/retention.md §IPC). A dry
// run only counts. A removal needs a human approval first (owner decision
// D57): the first call counts, creates a data_prune approval and returns it;
// the caller then repeats the call with the approval id, which answers
// pending until the human decides and, once approved, removes one bounded
// batch per call against the cutoff the human saw.
func registerPrune(srv *ipc.Server, db *sql.DB, apprStore *approval.Store, now func() time.Time) {
	auths := &pruneAuths{m: map[string]*pruneAuth{}}
	srv.Handle("data_prune", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DataPruneParams
		var raw struct {
			OlderThanS json.RawMessage `json:"older_than_s"`
		}
		dec := json.NewDecoder(bytes.NewReader(params))
		dec.UseNumber()
		if err := dec.Decode(&p); err != nil || json.Unmarshal(params, &raw) != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "bad params"}
		}
		if !plainInteger(raw.OlderThanS) {
			p.OlderThanS = nil // a quoted, fractional or exponent form is refused
		}
		olderThan, err := olderThanParam(p.OlderThanS)
		if err != nil {
			return nil, err
		}
		t := now()
		if p.DryRun {
			if p.Approval != "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "dry_run takes no approval"}
			}
			cutoff := retention.Cutoff(t, olderThan)
			c, err := retention.DryRun(ctx, db, cutoff, t)
			if err != nil {
				return nil, err
			}
			return DataPruneResult{Cutoff: wireTimeStr(cutoff), DryRun: true, Counts: c}, nil
		}
		if p.Approval == "" {
			return createPruneApproval(ctx, db, apprStore, auths, olderThan, t)
		}
		return runPrune(ctx, db, apprStore, auths, p.Approval, olderThan, t)
	})
}

// createPruneApproval counts what the prune removes and asks the human.
// Nothing to remove needs no approval.
func createPruneApproval(ctx context.Context, db *sql.DB, apprStore *approval.Store, auths *pruneAuths, olderThan time.Duration, t time.Time) (any, error) {
	cutoff := retention.Cutoff(t, olderThan)
	c, err := retention.DryRun(ctx, db, cutoff, t)
	if err != nil {
		return nil, err
	}
	if c.Zero() {
		return DataPruneResult{Cutoff: wireTimeStr(cutoff), Counts: c}, nil
	}
	facts := pruneFacts(olderThan, cutoff, c)
	summary, err := approvaltext.BuildPrune(facts)
	if err != nil {
		return nil, err
	}
	subject, err := newPruneSubject()
	if err != nil {
		return nil, err
	}
	var approvalID string
	var idMu sync.Mutex
	aid := func() string {
		idMu.Lock()
		defer idMu.Unlock()
		return approvalID
	}
	action := approval.Action{
		// The counts are bound to the approval as counted here, not re-counted
		// in the confirm transaction on the daemon's only connection (review
		// 81 L1). The rebuild still compares the summary from these facts
		// with the one shown. A change after counting can only take items
		// out of the set: an item is in it only if its updated is before the
		// fixed cutoff, and every change sets updated to now. Only mail_inbox
		// rows whose mail_seen row ages out can join, and the batches never
		// remove more of a table than these counts (retention.md §Approval).
		Rebuild: rebuildWith(facts, func(context.Context, *sql.Tx) (approvaltext.Prune, error) {
			return facts, nil
		}, approvaltext.BuildPrune),
		// Approval only allows the removal: the calls that follow remove in
		// bounded batches, each in its own transaction (retention.md §IPC).
		Perform: func(context.Context, *sql.Tx) (any, error) {
			id := aid()
			return afterCommitResult{after: func(context.Context) {
				auths.approve(id, time.Now().Add(pruneAuthTTL))
			}}, nil
		},
		OnReject: func(context.Context) { auths.drop(aid()) },
	}
	view, aerr := apprStore.Create(ctx, approval.KindDataPrune, subject, summary, action)
	if aerr != nil {
		return nil, approvalError(aerr)
	}
	idMu.Lock()
	approvalID = view.ID
	idMu.Unlock()
	auths.set(view.ID, &pruneAuth{olderThan: olderThan, cutoff: cutoff, counts: c})
	return DataPruneResult{Cutoff: wireTimeStr(cutoff), Counts: c, More: true, Approval: &view}, nil
}

// runPrune answers a call that names an approval: pending while the human
// has not decided, one batch once approved.
func runPrune(ctx context.Context, db *sql.DB, apprStore *approval.Store, auths *pruneAuths, id string, olderThan time.Duration, t time.Time) (any, error) {
	auth, ok := auths.get(id)
	if !ok {
		view, err := apprStore.Show(ctx, id)
		switch {
		case err != nil:
			return nil, approvalError(err)
		case view.Kind != approval.KindDataPrune:
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "not a data_prune approval"}
		case view.State == approval.StateRejected:
			return nil, &ipc.Error{Code: CodeApprovalRejected, Message: "the prune was rejected; nothing was removed"}
		case view.State == approval.StateExpired:
			return nil, approvalError(approval.ErrExpired)
		}
		return nil, &ipc.Error{Code: "unknown_approval", Message: "this prune is no longer running here (the daemon restarted or it finished); run agentnet prune again"}
	}
	if auth.olderThan != olderThan {
		return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "older_than_s differs from the approved prune"}
	}
	if !auth.approved {
		view, err := apprStore.Show(ctx, id)
		if err != nil {
			return nil, approvalError(err)
		}
		switch view.State {
		case approval.StateRejected:
			auths.drop(id)
			return nil, &ipc.Error{Code: CodeApprovalRejected, Message: "the prune was rejected; nothing was removed"}
		case approval.StateExpired:
			auths.drop(id)
			return nil, approvalError(approval.ErrExpired)
		}
		return DataPruneResult{Cutoff: wireTimeStr(auth.cutoff), More: true, Approval: &view}, nil
	}
	if time.Now().After(auth.until) {
		auths.drop(id)
		return nil, &ipc.Error{Code: "unknown_approval", Message: "the approved prune has lapsed; run agentnet prune again"}
	}
	auths.run.Lock()
	defer auths.run.Unlock()
	if auth, ok = auths.get(id); !ok {
		return nil, &ipc.Error{Code: "unknown_approval", Message: "this prune is no longer running here (the daemon restarted or it finished); run agentnet prune again"}
	}
	c, more, err := pruneBatch(ctx, db, auth, id, t)
	if err != nil {
		return nil, err
	}
	auths.addRemoved(id, c)
	if !more {
		auths.drop(id)
	}
	return DataPruneResult{Cutoff: wireTimeStr(auth.cutoff), Counts: c, More: more}, nil
}

// pruneBatch removes one batch and audits it in the same transaction, so the
// counts and the removals commit together (retention.md §Audit). It removes
// no more of a table than the approved count less what the earlier batches
// removed (retention.md §Approval).
func pruneBatch(ctx context.Context, db *sql.DB, auth pruneAuth, id string, t time.Time) (retention.Counts, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return retention.Counts{}, false, pruneIOError(err)
	}
	defer func() { _ = tx.Rollback() }()
	c, more, err := retention.PruneTxWithin(ctx, tx, auth.cutoff, t, auth.counts.Sub(auth.removed))
	if err != nil {
		return retention.Counts{}, false, pruneIOError(err)
	}
	if !c.Zero() {
		// Counts only, under their JSON names, never ids or content.
		detail := map[string]any{}
		raw, err := json.Marshal(c)
		if err == nil {
			err = json.Unmarshal(raw, &detail)
		}
		if err != nil {
			return retention.Counts{}, false, err
		}
		detail["older_than_s"] = int64(auth.olderThan / time.Second)
		detail["cutoff"] = wireTimeStr(auth.cutoff)
		detail["approval"] = id
		if err := auditTx(ctx, tx, audit.ActorCLI, "data.prune", detail); err != nil {
			return retention.Counts{}, false, pruneIOError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return retention.Counts{}, false, pruneIOError(err)
	}
	return c, more, nil
}

// pruneIOError is io_error: the database write failed (for example a full
// disk) and nothing of this call was removed.
func pruneIOError(err error) error {
	var ie *ipc.Error
	if errors.As(err, &ie) {
		return ie
	}
	return &ipc.Error{Code: "io_error", Message: "the database write failed; nothing of this call was removed"}
}
