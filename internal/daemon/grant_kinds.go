package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

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

var pendingGrant sync.Map // map[*mail.Opened]*grantOutcome

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
			raw, err := json.Marshal(tokenGen)
			if err != nil {
				return badMailBody("token: %s", err.Error())
			}
			now := time.Now()
			sessionOpen := func(id, requester, worker string) (bool, bool) {
				v, err := wsStore.GetTx(ctx, tx, id)
				if err != nil {
					return false, false
				}
				r, w := v.SelfOf(self)
				if r != requester || w != worker {
					return false, false
				}
				return true, v.State == worksession.StateOpen
			}
			g, verr := capability.Verify(raw, capability.VerifyParams{
				Role: capability.RoleHolder, Self: self, Counterparty: op.Msg.From, Now: now, SessionOpen: sessionOpen,
			})
			if verr != nil {
				if capability.ReasonOf(verr) == capability.ReasonUnknownSession {
					pendingGrant.Store(op, &grantOutcome{kind: "orphan", grant: capability.GrantIDOf(verr), peer: op.Msg.From})
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
				pendingGrant.Store(op, &grantOutcome{kind: "conflict", grant: g.ID, peer: op.Msg.From})
				return nil
			}
			if !errors.Is(err, capability.ErrUnknownGrant) {
				return err
			}
			rec := capability.Record{
				ID: g.ID, Peer: g.Iss, Session: g.Session, Action: g.Action, Label: g.Resource.Label,
				Branch: g.Resource.Branch, Scope: g.Scope, Sensitive: g.Sensitive, Nbf: g.Nbf, Exp: g.Exp,
				Token: string(wire),
			}
			if err := capStore.InsertHeldTx(ctx, tx, rec); err != nil {
				return err
			}
			pendingGrant.Store(op, &grantOutcome{kind: "in", grant: g.ID, peer: op.Msg.From, action: g.Action, sensitive: g.Sensitive, session: g.Session})
			return nil
		},
		After: func(ctx context.Context, op *mail.Opened) {
			v, ok := pendingGrant.LoadAndDelete(op)
			if !ok || log == nil {
				return
			}
			out := v.(*grantOutcome)
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

var pendingGrantRevoke sync.Map // map[*mail.Opened]*grantRevokeOutcome

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
			if changed {
				pendingGrantRevoke.Store(op, &grantRevokeOutcome{applied: true, grant: gid, peer: op.Msg.From})
			}
			return nil
		},
		After: func(ctx context.Context, op *mail.Opened) {
			v, ok := pendingGrantRevoke.LoadAndDelete(op)
			if !ok || log == nil {
				return
			}
			out := v.(*grantRevokeOutcome)
			if out.applied {
				_ = log.Append(ctx, audit.ActorDaemon, "grant.revoked_in", map[string]any{"grant": out.grant, "peer": out.peer})
			}
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
