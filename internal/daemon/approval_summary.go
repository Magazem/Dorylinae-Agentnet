package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Approval summaries (Docs/protocol/approval.md §Approval summaries, R55-F5).
// Every handler that creates an approval takes its facts from the object it
// will perform, builds the text with internal/approvaltext, and registers a
// Rebuild that re-derives the facts inside the confirm transaction; the
// approval store then requires the rebuilt text to equal approvals.summary.

// factQuerier is the part of *sql.DB and *sql.Tx the fact readers need.
type factQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// peerFacts reads the peer that key names from the peers table (through the
// confirm transaction at confirm time): its current name, or not paired.
func peerFacts(ctx context.Context, q factQuerier, key string) (approvaltext.Peer, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT name FROM peers WHERE public_key = ?`, key).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return approvaltext.Peer{Key: key}, nil
	}
	if err != nil {
		return approvaltext.Peer{}, fmt.Errorf("approval summary: read peer: %w", err)
	}
	return approvaltext.Peer{Key: key, Name: name, Paired: true}, nil
}

// summaryTooLong is the IPC refusal of a summary longer than the window
// shows (Docs/protocol/approval.md §Length): bad_request naming the field
// that made it long. Nothing has been stored yet.
func summaryTooLong(field string) error {
	return &ipc.Error{Code: ipc.CodeBadRequest, Message: field + ": too long to show in full in the approval window"}
}

// summaryField maps a builder error: too long is the caller's bad_request
// naming field; anything else is returned as is (internal).
func summaryField(err error, field string) error {
	if errors.Is(err, approvaltext.ErrTooLong) {
		return summaryTooLong(field)
	}
	return err
}

// sameFacts is the field-by-field comparison of Precondition compares: the
// facts re-derived at confirm must equal those captured at Create (times are
// compared as instants because every fact carries them in UTC).
func sameFacts(created, now any) error {
	if !reflect.DeepEqual(created, now) {
		return approval.ErrChanged
	}
	return nil
}

// rebuildWith returns an approval.Action Rebuild that re-derives the facts
// with derive (inside the confirm transaction), requires them equal to the
// facts captured at Create, and rebuilds the text.
func rebuildWith[F any](created F, derive func(ctx context.Context, tx *sql.Tx) (F, error), build func(F) (string, error)) func(context.Context, *sql.Tx) (string, error) {
	return func(ctx context.Context, tx *sql.Tx) (string, error) {
		now, err := derive(ctx, tx)
		if err != nil {
			return "", err
		}
		if err := sameFacts(created, now); err != nil {
			return "", err
		}
		return build(now)
	}
}

// grantFacts derives a grant's facts from the token Perform will send (wire)
// plus the row's resolved path (Docs/protocol/approval.md §One builder,
// review 58a M3).
func grantFacts(wire []byte, path string, peer approvaltext.Peer, reqType, reqTitle string) (approvaltext.Grant, error) {
	g, err := capability.DecodeToken(wire)
	if err != nil {
		return approvaltext.Grant{}, err
	}
	return approvaltext.Grant{
		ID: g.ID, Action: g.Action, Peer: peer, Path: path, Label: g.Resource.Label, Branch: g.Resource.Branch,
		Scope: g.Scope, Sensitive: g.Sensitive, Nbf: g.Nbf.UTC(), Exp: g.Exp.UTC(), Session: g.Session,
		RequestType: reqType, RequestTitle: reqTitle,
	}, nil
}

// rowMatchesToken is the grant Precondition's first check: the stored row
// agrees with the signed token on every shared field (review 58a M3, A19).
// The row keeps milliseconds and the token whole seconds.
func rowMatchesToken(rec capability.Record, f approvaltext.Grant) error {
	if rec.ID != f.ID || rec.Action != f.Action || rec.Peer != f.Peer.Key || rec.Session != f.Session ||
		rec.Label != f.Label || rec.Branch != f.Branch || rec.Scope != f.Scope || rec.Sensitive != f.Sensitive ||
		!rec.Nbf.Truncate(time.Second).Equal(f.Nbf) || !rec.Exp.Truncate(time.Second).Equal(f.Exp) {
		return approval.ErrChanged
	}
	return nil
}

// resultFacts derives a release or accept_result approval's facts from the
// session row: status and sizes only, never the result's content
// (OD-R55F5-8).
func resultFacts(v worksession.View, peer approvaltext.Peer, reqType, reqTitle string, k int) approvaltext.Result {
	f := approvaltext.Result{
		Session: v.ID, Peer: peer, RequestID: v.RequestID, RequestType: reqType, RequestTitle: reqTitle,
		Round: v.Round, Seq: v.Seq, Status: v.ResultStatus, ResultBytes: v.ResultBytes, OutputBytes: v.OutputBytes,
		Artifacts: v.Artifacts, SensitiveGrants: k,
	}
	if v.Result != nil {
		f.Status = v.Result.Status
		f.Artifacts = len(v.Result.Artifacts)
	}
	return f
}

// policyFacts derives a grant_policy approval's facts from the policy that
// Perform inserts.
func policyFacts(pol capability.Policy, peer approvaltext.Peer) approvaltext.Policy {
	return approvaltext.Policy{
		ID: pol.ID, Action: pol.Action, Peer: peer, Path: pol.Path, Branch: pol.Branch, Scope: pol.Scope,
		Public: pol.Public, MaxExpires: time.Duration(pol.MaxExpiresS) * time.Second,
		Created: pol.Created.UTC(), Until: pol.Until.UTC(),
	}
}

// sessionResultFacts derives a release (withK) or accept_result approval's
// facts from the session row v through q: the peer, the request, the round,
// the result's status and sizes and, for release, the number K of sensitive
// grants ever active in the session (OD-R55F5-7). K never decreases, and the
// K = 0 reason (rule 2) reads no clock, so the text is stable while the
// approval waits (review 58a I2).
func sessionResultFacts(ctx context.Context, q request.Querier, v worksession.View, withK bool) (approvaltext.Result, error) {
	pf, err := peerFacts(ctx, q, v.Peer)
	if err != nil {
		return approvaltext.Result{}, err
	}
	typ, title, err := requestFacts(ctx, q, v.Peer, v.RequestID)
	if err != nil {
		return approvaltext.Result{}, err
	}
	k := 0
	if withK {
		if k, err = capability.SensitiveGrantsInSession(ctx, q, v.ID); err != nil {
			return approvaltext.Result{}, err
		}
	}
	return resultFacts(v, pf, typ, title, k), nil
}

// requestFacts reads the type and title of the request a requester-side
// session belongs to. A request row that does not exist gives empty values
// (shown as "" in the summary), as in the session view.
func requestFacts(ctx context.Context, q request.Querier, peer, requestID string) (typ, title string, err error) {
	typ, title, err = request.PeekTypeTitleIn(ctx, q, "out", peer, requestID)
	if errors.Is(err, request.ErrUnknownRequest) {
		return "", "", nil
	}
	return typ, title, err
}
