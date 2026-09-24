package daemon

import (
	"context"
	"database/sql"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// wireQuarantine connects the work session store to the grants table and the
// approval store (Docs/protocol/work-session.md §Quarantine, 2.4):
//
//   - Quarantine is the real rule of the spec (a sensitive grant that was ever
//     active in the session, or issued to the same peer within 7 d of its exp)
//     unless a test injected its own;
//   - OnQuarantined fires the content-free session.quarantined notification;
//   - OnClosed rejects the closed session's pending approvals (review 28 L8:
//     grant approvals of its grants, and any release or accept_result approval
//     of the session itself), after the closing transaction committed.
func wireQuarantine(ws *worksession.Store, caps *capability.Store, appr *approval.Store, req *request.Store, override func(ctx context.Context, tx *sql.Tx, sid, peer string, round int) (bool, error)) {
	if override != nil {
		ws.Quarantine = override
	} else {
		ws.Quarantine = func(ctx context.Context, tx *sql.Tx, sid, peer string, _ int) (bool, error) {
			return caps.QuarantineHolds(ctx, tx, sid, peer)
		}
	}
	ws.OnQuarantined = func(ctx context.Context, _, peer, requestID string) {
		if req.Notify != nil {
			req.Notify(ctx, notify.EventQuarantined, request.NotifyInfo{Peer: peer, RequestID: requestID, State: worksession.StateQuarantined})
		}
	}
	ws.OnClosed = func(ctx context.Context, sid string) {
		subjects := []string{sid}
		if recs, err := caps.List(ctx, capability.ListFilter{Session: sid, Direction: capability.DirectionIssued}); err == nil {
			for _, r := range recs {
				subjects = append(subjects, r.ID)
			}
		}
		appr.RejectSubjects(ctx, subjects)
	}
}
