package daemon

import (
	"context"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Consult support (Docs/protocol/consult.md): resolving a derived session id
// back to its request before the session exists, and finding the question a
// `result` answers in one step.

// requestKeyForSession finds the request whose derived session id is sid, on
// either side: an `out` request (this daemon is the requester, A) or an `in`
// request (this daemon is the worker, B). The session row may not exist yet
// (Docs/protocol/consult.md: the id is derivable at submit time). It returns
// the exact row's key, so a request of another peer that reuses the id is
// never shown in its place, and request.ErrUnknownRequest when no request
// derives to sid. The scan reads only the key columns: wait polls it.
func requestKeyForSession(ctx context.Context, rs *request.Store, self, sid string) (request.Key, error) {
	return rs.FindKey(ctx, func(k request.Key) bool {
		if k.Direction == "out" {
			return worksession.DeriveID(self, k.Peer, k.ID) == sid
		}
		return worksession.DeriveID(k.Peer, self, k.ID) == sid
	})
}

// answerTarget is a pending or deferred question a result can answer in one
// step (Docs/protocol/consult.md §Answering).
type answerTarget struct {
	requestID string
	from      string // the requester's key, to disambiguate the id
}

// findAnswerable resolves id (a session s-... or request r-... id) to a
// question that has no session yet and is still pending or deferred. It
// returns nil, nil when the normal flow applies: a session already exists, or
// the id is not such a question.
func findAnswerable(ctx context.Context, ws *worksession.Store, rs *request.Store, self, id string) (*answerTarget, error) {
	rid := id
	var key *request.Key // set for an s- id: the exact row
	switch {
	case worksession.ValidID(id):
		if _, err := ws.Get(ctx, id); err == nil {
			return nil, nil
		} else if !errors.Is(err, worksession.ErrUnknownSession) {
			return nil, err
		}
		k, err := requestKeyForSession(ctx, rs, self, id)
		if errors.Is(err, request.ErrUnknownRequest) || (err == nil && k.Direction != "in") {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		key = &k
		rid = k.ID
	case request.ValidID(id):
		if _, err := ws.GetByRequestID(ctx, id); err == nil {
			return nil, nil
		} else if !errors.Is(err, worksession.ErrUnknownSession) {
			return nil, err
		}
	default:
		return nil, nil
	}
	var v request.View
	var err error
	if key != nil {
		v, err = rs.ShowKey(ctx, *key)
	} else {
		v, err = rs.Show(ctx, rid, "")
	}
	if errors.Is(err, request.ErrUnknownRequest) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if v.Direction != "in" || v.Type != request.TypeQuestion || (v.State != request.StatePending && v.State != request.StateDeferred) {
		return nil, nil
	}
	return &answerTarget{requestID: rid, from: v.Peer}, nil
}

// answerError maps an error of the one-step answer to an *ipc.Error: the
// accept half raises request errors, the result half worksession errors.
func answerError(err error) error {
	var bse *request.BadStateError
	if errors.As(err, &bse) || errors.Is(err, request.ErrUnknownRequest) || errors.Is(err, request.ErrAmbiguousRequest) {
		return lifecycleError(err)
	}
	return sessionError(err)
}
