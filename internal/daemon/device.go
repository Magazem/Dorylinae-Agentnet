package daemon

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// CodeDeviceCycle is the IPC error for a link that would reverse an existing
// one or make a chain (Docs/protocol/device.md §One-way hierarchy).
const CodeDeviceCycle = "device_cycle"

// DeviceLinkParams are the params of "device_link".
type DeviceLinkParams struct {
	Peer        string `json:"peer"`
	As          string `json:"as"`
	Fingerprint string `json:"fingerprint"`
}

// DeviceUnlinkParams are the params of "device_unlink".
type DeviceUnlinkParams struct {
	Peer string `json:"peer"`
}

// DeviceLinkView is the "link view" of Docs/protocol/device.md §IPC and CLI.
// The scope member is added by 2.D2.
type DeviceLinkView struct {
	ID          string       `json:"id"`
	Peer        GrantPeerRef `json:"peer"`
	Role        string       `json:"role"`
	State       string       `json:"state"`
	ActivatedAt string       `json:"activated_at,omitempty"`
}

// DeviceLinkResult is the result of "device_link".
type DeviceLinkResult struct {
	Approval approval.View  `json:"approval"`
	Link     DeviceLinkView `json:"link"`
}

// DeviceUnlinkResult is the result of "device_unlink". Link is absent when
// this device held no link or intent with the peer: the mail is sent anyway.
type DeviceUnlinkResult struct {
	Link   *DeviceLinkView `json:"link,omitempty"`
	MailID string          `json:"mail_id"`
}

// DeviceListResult is the result of "device_list".
type DeviceListResult struct {
	Links []DeviceLinkView `json:"links"`
}

func deviceView(ctx context.Context, ps *peers.Store, l device.Link) DeviceLinkView {
	v := DeviceLinkView{ID: l.ID, Peer: peerRef(ctx, ps, l.Peer), Role: l.Role, State: l.State}
	if !l.ActivatedAt.IsZero() {
		v.ActivatedAt = wireTimeStr(l.ActivatedAt)
	}
	return v
}

// deviceHierarchyError maps a hierarchy error of the device package to an
// *ipc.Error; any other error is returned unchanged.
func deviceHierarchyError(err error) error {
	switch {
	case errors.Is(err, device.ErrCycle):
		return &ipc.Error{Code: CodeDeviceCycle, Message: "a link in the other direction, or a chain of links, is not allowed: a device is either a controller or a helper (Docs/protocol/device.md §One-way hierarchy)"}
	case errors.Is(err, device.ErrAlreadyLinked):
		return &ipc.Error{Code: CodeBadState, Message: "a link or a link attempt with this peer already exists (see 'agentnet device list')"}
	case errors.Is(err, device.ErrLimit):
		return &ipc.Error{Code: CodeBadState, Message: "a helper has one controller and a controller at most 8 helpers"}
	default:
		return err
	}
}

// stripLongDigits replaces runs of six or more digits with an ellipsis so that
// peer-supplied text cannot show a decoy approval code (review 26 N4). A run
// counts any Unicode digit and may be split by single spaces or punctuation
// ("482 913", "48-29-13", fullwidth digits), which a human reads as the same
// code (review 36 L2).
func stripLongDigits(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if !unicode.IsDigit(rs[i]) {
			b.WriteRune(rs[i])
			i++
			continue
		}
		digits, end := 0, i
		for j := i; j < len(rs); j++ {
			if unicode.IsDigit(rs[j]) {
				digits++
				end = j + 1
				continue
			}
			sep := unicode.IsSpace(rs[j]) || unicode.IsPunct(rs[j]) || unicode.IsSymbol(rs[j])
			if !sep || j+1 >= len(rs) || !unicode.IsDigit(rs[j+1]) {
				break
			}
		}
		if digits >= 6 {
			b.WriteString("…")
		} else {
			b.WriteString(string(rs[i:end]))
		}
		i = end
	}
	return b.String()
}

// offerBody is the JSON of a device.link body, also what a kept offer stores.
type offerBody struct {
	At         string `json:"at"`
	Controller string `json:"controller"`
	Helper     string `json:"helper"`
	Nonce      string `json:"nonce"`
	Role       string `json:"role"`
}

func (o offerBody) offer() (device.Offer, error) {
	at, err := time.Parse(time.RFC3339, o.At)
	if err != nil {
		return device.Offer{}, err
	}
	return device.Offer{At: at, Controller: o.Controller, Helper: o.Helper, Nonce: o.Nonce, Role: o.Role}, nil
}

// parseOffer validates a device.link body strictly (Docs/protocol/device.md
// §Kinds): exactly its five members; the sender's key is the one in its role
// and the recipient's key the other.
func parseOffer(body map[string]any, from, self string) (offerBody, device.Offer, error) {
	for k := range body {
		switch k {
		case "at", "controller", "helper", "nonce", "role":
		default:
			return offerBody{}, device.Offer{}, badMailBody("unknown member %q", k)
		}
	}
	str := func(k string) (string, bool) { s, ok := body[k].(string); return s, ok }
	var ob offerBody
	var ok1, ok2, ok3, ok4, ok5 bool
	ob.At, ok1 = str("at")
	ob.Controller, ok2 = str("controller")
	ob.Helper, ok3 = str("helper")
	ob.Nonce, ok4 = str("nonce")
	ob.Role, ok5 = str("role")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || len(body) != 5 {
		return offerBody{}, device.Offer{}, badMailBody("device.link needs at, controller, helper, nonce and role as strings")
	}
	if !device.ValidRole(ob.Role) || !device.ValidNonce(ob.Nonce) {
		return offerBody{}, device.Offer{}, badMailBody("bad role or nonce")
	}
	want := offerBody{Controller: ob.Controller, Helper: ob.Helper}
	if ob.Role == device.RoleController {
		want.Controller, want.Helper = from, self
	} else {
		want.Controller, want.Helper = self, from
	}
	if ob.Controller != want.Controller || ob.Helper != want.Helper {
		return offerBody{}, device.Offer{}, badMailBody("device.link keys do not match the sender and the recipient")
	}
	o, err := ob.offer()
	if err != nil {
		return offerBody{}, device.Offer{}, badMailBody("at is not a timestamp")
	}
	return ob, o, nil
}

// deviceLinkKind is the receiver Kind for "device.link" (Docs/protocol/device.md
// §Link flow step 3): activate when the local intent is complementary and
// unexpired, otherwise keep the offer (one per peer) for IntentTTL. An offer
// never creates a link on its own.
func deviceLinkKind(ds *device.Store, self string) mail.Kind {
	return mail.Kind{
		Inbox: true,
		Apply: func(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
			ob, offer, err := parseOffer(op.Msg.Body, op.Msg.From, self)
			if err != nil {
				return err
			}
			now := ds.Time()
			if err := ds.ExpireTx(ctx, tx, now); err != nil {
				return err
			}
			l, activated, err := ds.TryActivateTx(ctx, tx, op.Msg.From, offer, now)
			if err != nil {
				return err
			}
			if activated {
				return auditTx(ctx, tx, audit.ActorDaemon, "device.link_active", map[string]any{"link": l.ID, "peer": l.Peer, "role": l.Role})
			}
			raw, err := json.Marshal(ob)
			if err != nil {
				return err
			}
			return ds.OfferPutTx(ctx, tx, op.Msg.From, string(raw), now)
		},
	}
}

// deviceUnlinkKind is the receiver Kind for "device.unlink": revoke every
// link, intent and offer with msg.from, and delete their scopes. The "link"
// member is informational (review 24 M8): a peer can only end its own links.
func deviceUnlinkKind(ds *device.Store) mail.Kind {
	return mail.Kind{
		Inbox: true,
		Apply: func(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
			body := op.Msg.Body
			for k := range body {
				if k != "at" && k != "link" {
					return badMailBody("unknown member %q", k)
				}
			}
			at, ok := body["at"].(string)
			if !ok {
				return badMailBody("at is required")
			}
			if _, err := time.Parse(time.RFC3339, at); err != nil {
				return badMailBody("at is not a timestamp")
			}
			if raw, present := body["link"]; present {
				s, ok := raw.(string)
				if !ok || !device.ValidLinkID(s) {
					return badMailBody("link is not a link id")
				}
			}
			revoked, err := ds.RevokeForPeerTx(ctx, tx, op.Msg.From, ds.Time())
			if err != nil {
				return err
			}
			if len(revoked) == 0 {
				return nil
			}
			return auditTx(ctx, tx, audit.ActorDaemon, "device.unlink", unlinkAudit(revoked, op.Msg.From, "remote"))
		},
	}
}

func unlinkAudit(revoked []device.Link, peer, side string) map[string]any {
	d := map[string]any{"peer": peer, "side": side}
	for _, l := range revoked {
		if l.State == device.StateActive {
			d["link"] = l.ID
		}
	}
	return d
}

// withDeviceKinds returns kinds plus the two device kinds, without changing the
// caller's map.
func withDeviceKinds(kinds map[string]mail.Kind, ds *device.Store, self string) map[string]mail.Kind {
	out := make(map[string]mail.Kind, len(kinds)+2)
	for k, v := range kinds {
		out[k] = v
	}
	out[device.KindLink] = deviceLinkKind(ds, self)
	out[device.KindUnlink] = deviceUnlinkKind(ds)
	return out
}

// revokeDeviceForRemovedPeer is an OnRemovedTx hook: removing the other device
// ends its link locally (Docs/protocol/device.md §Unlink and expiry).
func revokeDeviceForRemovedPeer(ds *device.Store) func(ctx context.Context, tx *sql.Tx, key string) error {
	return func(ctx context.Context, tx *sql.Tx, key string) error {
		revoked, err := ds.RevokeForPeerTx(ctx, tx, key, ds.Time())
		if err != nil || len(revoked) == 0 {
			return err
		}
		return auditTx(ctx, tx, audit.ActorDaemon, "device.unlink", unlinkAudit(revoked, key, "local"))
	}
}

// chainRemovedTx runs several OnRemovedTx hooks in order; the first error
// rolls the removal back.
func chainRemovedTx(hooks ...func(context.Context, *sql.Tx, string) error) func(context.Context, *sql.Tx, string) error {
	return func(ctx context.Context, tx *sql.Tx, key string) error {
		for _, h := range hooks {
			if err := h(ctx, tx, key); err != nil {
				return err
			}
		}
		return nil
	}
}

// registerDevice wires "device_link", "device_list" and "device_unlink"
// (Docs/protocol/device.md §IPC and CLI). Scope and run are 2.D2.
func registerDevice(srv *ipc.Server, ds *device.Store, apprStore *approval.Store, ps *peers.Store, ob *mail.Outbox, log *audit.Log, nonLoopbackRelay bool) {
	srv.Handle("device_link", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DeviceLinkParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		if strings.TrimPrefix(p.Peer, "@") == "" || p.Fingerprint == "" || !device.ValidRole(p.As) {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer, fingerprint and as (controller or helper) are required"}
		}
		given, ok := envelope.NormalizeFingerprint(p.Fingerprint)
		if !ok {
			return nil, &ipc.Error{Code: CodeBadFingerprint, Message: "not a valid fingerprint (20 letters and digits, e.g. 2ED9 TGVE R471 63MC C451)"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		d := peerAuditDetail{Peer: peer.PublicKey, Name: peer.Name, Fingerprint: peer.Fingerprint}
		if subtle.ConstantTimeCompare([]byte(given), []byte(peer.Fingerprint)) != 1 {
			if log != nil {
				_ = log.Append(ctx, audit.ActorCLI, audit.ActionPeerVerifyFail, d)
			}
			return nil, &ipc.Error{Code: CodeFingerprintMismatch, Message: "the fingerprint does not match this peer's key; nothing was changed"}
		}
		if nonLoopbackRelay && peer.Trust == peers.TrustRelay {
			return nil, &ipc.Error{Code: CodeUnverifiedPeer, Message: "the peer's trust is \"relay\" on a non-loopback relay; re-pair or run 'agentnet peers verify'"}
		}
		// A link attempt that was never approved is replaced by this one:
		// reject its approval first so it cannot be confirmed later.
		if old, ok, err := ds.PendingFor(ctx, peer.PublicKey); err != nil {
			return nil, err
		} else if ok && old.Approval != "" {
			_, _ = apprStore.Reject(ctx, old.Approval, "superseded")
		}
		link, err := ds.CreateIntent(ctx, peer.PublicKey, p.As)
		if err != nil {
			return nil, deviceHierarchyError(err)
		}
		linkID, peerKey, role := link.ID, peer.PublicKey, p.As
		action := approval.Action{
			// Every state check is here, not in Perform (review 26 N1): the
			// row is still ours and pending, the peer is still paired and the
			// hierarchy still allows the link.
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				// Lapse stale intents first, so that neither a lapsed attempt
				// with another peer blocks this one nor a lapsed own row is
				// approved (review 36 L1).
				if err := ds.ExpireTx(ctx, tx, ds.Time()); err != nil {
					return err
				}
				cur, err := ds.GetTx(ctx, tx, linkID)
				if err != nil || cur.State != device.StatePendingApproval {
					return &ipc.Error{Code: CodeBadState, Message: "the link attempt is no longer pending approval"}
				}
				trust, ok, err := peerTrustTx(ctx, tx, peerKey)
				if err != nil {
					return err
				}
				if !ok {
					return &ipc.Error{Code: CodeBadState, Message: "the peer is no longer paired"}
				}
				if nonLoopbackRelay && trust == peers.TrustRelay {
					return &ipc.Error{Code: CodeUnverifiedPeer, Message: "the peer's trust is \"relay\" on a non-loopback relay"}
				}
				return deviceHierarchyError(ds.CheckHierarchyTx(ctx, tx, peerKey, role, linkID))
			},
			// Perform touches only tx: the intent, the trust, the mail row and
			// the audit rows commit together (review 26 N1, review 27 C1).
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				now := ds.Time()
				l, err := ds.ApproveTx(ctx, tx, linkID, now)
				if err != nil {
					return nil, err
				}
				// A matched fingerprint is exactly `peers verify` (step 2).
				if err := peers.SetTrustTx(ctx, tx, peerKey, peers.TrustFingerprint); err != nil {
					return nil, err
				}
				vd := d
				vd.Trust = peers.TrustFingerprint
				if err := auditTx(ctx, tx, audit.ActorCLI, audit.ActionPeerVerify, vd); err != nil {
					return nil, err
				}
				if err := auditTx(ctx, tx, audit.ActorDaemon, "device.link_intent", map[string]any{"peer": peerKey, "role": role, "approval": l.Approval}); err != nil {
					return nil, err
				}
				controller, helper := ds.Self, peerKey
				if role == device.RoleHelper {
					controller, helper = peerKey, ds.Self
				}
				if _, err := ob.SubmitTx(ctx, tx, peerKey, device.KindLink, map[string]any{
					"at": wireTimeStr(now), "controller": controller, "helper": helper, "nonce": l.Nonce, "role": role,
				}); err != nil {
					return nil, err
				}
				// The other device may have confirmed first: its offer was kept.
				if raw, ok, err := ds.OfferGetTx(ctx, tx, peerKey, now); err != nil {
					return nil, err
				} else if ok {
					var kept offerBody
					if json.Unmarshal([]byte(raw), &kept) == nil {
						if o, oerr := kept.offer(); oerr == nil {
							act, activated, err := ds.TryActivateTx(ctx, tx, peerKey, o, now)
							if err != nil {
								return nil, err
							}
							if activated {
								if err := auditTx(ctx, tx, audit.ActorDaemon, "device.link_active", map[string]any{"link": act.ID, "peer": peerKey, "role": role}); err != nil {
									return nil, err
								}
							}
						}
					}
				}
				return afterCommitResult{after: func(context.Context) { ob.Wake() }}, nil
			},
		}
		who := stripLongDigits(notify.Clean(peer.Name, 40))
		summary := fmt.Sprintf("link this device as the %s of %s? Confirm only if you started this on both devices.", role, who)
		view, aerr := apprStore.Create(ctx, approval.KindDeviceLink, linkID, summary, action)
		if aerr != nil {
			_ = ds.Delete(ctx, linkID) // review 26 N5: drop the pending row if Create fails
			return nil, approvalError(aerr)
		}
		_ = ds.SetApproval(ctx, linkID, view.ID)
		link.Approval = view.ID
		return DeviceLinkResult{Approval: view, Link: deviceView(ctx, ps, link)}, nil
	})

	srv.Handle("device_list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		links, err := ds.List(ctx)
		if err != nil {
			return nil, err
		}
		views := make([]DeviceLinkView, 0, len(links))
		for _, l := range links {
			views = append(views, deviceView(ctx, ps, l))
		}
		return DeviceListResult{Links: views}, nil
	})

	srv.Handle("device_unlink", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DeviceUnlinkParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimPrefix(p.Peer, "@") == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		now := ds.Time()
		tx, err := ds.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("device: begin unlink: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		revoked, err := ds.RevokeForPeerTx(ctx, tx, peer.PublicKey, now)
		if err != nil {
			return nil, err
		}
		// The mail is sent even when this device holds no active link: each
		// side activates on its own, so one side can be active while the
		// other's intent lapsed (Docs/protocol/device.md §Unlink and expiry).
		body := map[string]any{"at": wireTimeStr(now)}
		detail := unlinkAudit(revoked, peer.PublicKey, "local")
		if id, ok := detail["link"].(string); ok {
			body["link"] = id
		}
		sub, err := ob.SubmitTx(ctx, tx, peer.PublicKey, device.KindUnlink, body)
		if err != nil {
			return nil, err
		}
		if err := auditTx(ctx, tx, audit.ActorCLI, "device.unlink", detail); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("device: commit unlink: %w", err)
		}
		committed = true
		ob.Wake()
		// A link attempt still waiting for its code is dead now: close its
		// approval window too (its Precondition would refuse it anyway),
		// review 36 L3.
		for _, r := range revoked {
			if r.State == device.StatePendingApproval && r.Approval != "" {
				_, _ = apprStore.Reject(ctx, r.Approval, "unlinked")
			}
		}
		res := DeviceUnlinkResult{MailID: sub.ID}
		if len(revoked) > 0 {
			l := revoked[0]
			for _, r := range revoked {
				if r.State == device.StateActive {
					l = r
				}
			}
			l.State = device.StateRevoked
			v := deviceView(ctx, ps, l)
			res.Link = &v
		}
		return res, nil
	})
}
