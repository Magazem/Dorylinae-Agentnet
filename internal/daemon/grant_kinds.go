package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Holder-side apply of the "grant" and "grant.revoke" mail kinds
// (Docs/protocol/grant.md §Kinds).

type grantOutcome struct {
	kind      string // "orphan", "conflict", "in"
	grant     string
	peer      string
	action    string
	sensitive bool
	session   string
}

// grantKind returns the receiver Kind for "grant" (Docs/protocol/grant.md
// §Kinds, holder apply).
func grantKind(capStore *capability.Store, wsStore *worksession.Store, self string, log *audit.Log) mail.Kind {
	return mail.Kind{
		Inbox: true,
		Apply: func(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
			body := op.Msg.Body
			if len(body) != 1 {
				return badMailBody("grant body must have exactly \"token\"")
			}
			tokenGen, ok := body["token"]
			if !ok {
				return badMailBody("missing token")
			}
			tokMap, ok := tokenGen.(map[string]any)
			if !ok {
				return badMailBody("token must be an object")
			}
			sigStr, _ := tokMap["sig"].(string)
			// Canonical form, not json.Marshal: its HTML escaping of & < >
			// could push a valid token over MaxTokenBytes (review 55 R55-065).
			raw, err := agentcard.CanonicalValue(tokenGen)
			if err != nil {
				return badMailBody("token: %s", err.Error())
			}
			now := time.Now()
			// readErr keeps a session read error other than not-found: only
			// a session we do not have makes the grant an orphan. A DB error
			// fails the apply so the mail is retried, never acked and lost
			// (review 55 R55-081).
			var readErr error
			sessionOpen := func(id, requester, worker string) (bool, bool) {
				v, err := wsStore.GetTx(ctx, tx, id)
				if err != nil {
					if !errors.Is(err, worksession.ErrUnknownSession) {
						readErr = err
					}
					return false, false
				}
				r, w := v.SelfOf(self)
				if r != requester || w != worker {
					return false, false
				}
				// Debates carry no grants (Docs/protocol/debate.md §What a
				// debate session does not do): never open for a grant.
				return true, v.State == worksession.StateOpen && v.Kind != worksession.SessionKindDebate
			}
			g, verr := capability.Verify(raw, capability.VerifyParams{
				Role: capability.RoleHolder, Self: self, Counterparty: op.Msg.From, Now: now, SessionOpen: sessionOpen,
			})
			if readErr != nil {
				return fmt.Errorf("grant: read session: %w", readErr)
			}
			if verr != nil {
				if capability.ReasonOf(verr) == capability.ReasonUnknownSession {
					op.Outcome = &grantOutcome{kind: "orphan", grant: capability.GrantIDOf(verr), peer: op.Msg.From}
					return nil
				}
				return fmt.Errorf("grant: %s: %w", capability.ReasonOf(verr), mail.ErrBadBody)
			}
			wire, err := capability.Canonical(capability.Token{Grant: *g, Sig: sigStr})
			if err != nil {
				return badMailBody("token: %s", err.Error())
			}
			existing, err := capStore.GetTx(ctx, tx, g.ID)
			if err == nil {
				if existing.Token == string(wire) {
					return nil // duplicate id, identical token: nothing
				}
				op.Outcome = &grantOutcome{kind: "conflict", grant: g.ID, peer: op.Msg.From}
				return nil
			}
			if !errors.Is(err, capability.ErrUnknownGrant) {
				return err
			}
			// Held caps (R55-F13, review 71b F6): after the duplicate and
			// conflict checks, so a redelivered grant is never refused.
			live, err := capStore.CountHeldLiveInSessionTx(ctx, tx, g.Session, now)
			if err != nil {
				return err
			}
			total, err := capStore.CountHeldInSessionTx(ctx, tx, g.Session)
			if err != nil {
				return err
			}
			if live >= capability.MaxHeldLivePerSession || total >= capability.MaxHeldTotalPerSession {
				return fmt.Errorf("grant: session %s holds %d live and %d total grants: %w", g.Session, live, total, mail.ErrLimit)
			}
			rec := capability.Record{
				ID: g.ID, Peer: g.Iss, Session: g.Session, Action: g.Action, Label: g.Resource.Label,
				Branch: g.Resource.Branch, Scope: g.Scope, Sensitive: g.Sensitive, Nbf: g.Nbf, Exp: g.Exp,
				Token: string(wire),
			}
			if err := capStore.InsertHeldTx(ctx, tx, rec); err != nil {
				return err
			}
			op.Outcome = &grantOutcome{kind: "in", grant: g.ID, peer: op.Msg.From, action: g.Action, sensitive: g.Sensitive, session: g.Session}
			return nil
		},
		After: func(ctx context.Context, op *mail.Opened) {
			out, ok := op.Outcome.(*grantOutcome)
			if !ok || log == nil {
				return
			}
			switch out.kind {
			case "orphan":
				_ = log.Append(ctx, audit.ActorDaemon, "grant.orphan", map[string]any{"grant": out.grant, "peer": out.peer})
			case "conflict":
				_ = log.Append(ctx, audit.ActorDaemon, "grant.conflict", map[string]any{"grant": out.grant, "peer": out.peer})
			case "in":
				_ = log.Append(ctx, audit.ActorDaemon, "grant.in", map[string]any{
					"grant": out.grant, "session": out.session, "peer": out.peer, "action": out.action, "sensitive": out.sensitive,
				})
			}
		},
	}
}

type grantRevokeOutcome struct {
	applied bool
	grant   string
	peer    string
}

// grantRevokeKind returns the receiver Kind for "grant.revoke"
// (Docs/protocol/grant.md §Kinds, holder apply). Review 24 M8: applied only
// when msg.from equals the peer that granted it (capability.Store.FindHeldTx).
func grantRevokeKind(capStore *capability.Store, log *audit.Log) mail.Kind {
	return mail.Kind{
		Inbox: true,
		Apply: func(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
			body := op.Msg.Body
			if err := strictGrantRevokeMembers(body); err != nil {
				return err
			}
			atRaw, ok := body["at"].(string)
			if !ok || atRaw == "" {
				return badMailBody("at is required")
			}
			gid, ok := body["grant"].(string)
			if !ok || gid == "" {
				return badMailBody("grant is required")
			}
			reason, ok := body["reason"].(string)
			if !ok {
				return badMailBody("reason must be a string")
			}
			switch reason {
			case capability.ReasonUser, capability.ReasonSessionClosed, capability.ReasonPeerRemoved:
			default:
				return badMailBody("reason must be user, session_closed or peer_removed")
			}
			rec, ok, err := capStore.FindHeldTx(ctx, tx, gid, op.Msg.From)
			if err != nil {
				return err
			}
			if !ok {
				// Unknown id, or held from another grantor: ignore, nothing
				// changes (Docs/protocol/grant.md §Kinds).
				return nil
			}
			_ = rec
			changed, err := capStore.RevokeTx(ctx, tx, gid, reason, time.Now())
			if err != nil {
				return err
			}
			// grant.revoked_in is an S- row: written in this transaction
			// through a savepoint, so the revoke applies even if it fails.
			if changed && log != nil {
				return audit.AppendTxSoft(ctx, tx, audit.ActorDaemon, "grant.revoked_in", map[string]any{"grant": gid, "peer": op.Msg.From})
			}
			return nil
		},
	}
}

func strictGrantRevokeMembers(body map[string]any) error {
	allowed := map[string]bool{"at": true, "grant": true, "reason": true}
	for k := range body {
		if !allowed[k] {
			return badMailBody("unknown member %q", k)
		}
	}
	return nil
}

func badMailBody(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(append([]any{}, a...), mail.ErrBadBody)...)
}
