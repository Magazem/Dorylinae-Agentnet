package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// reservedKindPrefixes are the namespaces of daemon protocols. A kind in one
// of them is refused by mail_submit even if this build registers no handler
// for it, so a future daemon kind is never sendable by accident.
var reservedKindPrefixes = []string{"device.", "ws.", "request", "team.", "grant", "presence", "fetch", "consult", "approval", "pair", "keys", "ack"}

// ownedKinds is the set of kinds the daemon registers a handler for, derived
// from the same functions that build the mail receiver (daemonKinds and
// withDeviceKinds), plus the transport kinds keys and ack. The stores are
// zero values: only the kind names are used.
var ownedKinds = sync.OnceValue(func() map[string]bool {
	ws := &worksession.Store{}
	rs := &request.Store{Sessions: ws}
	kinds := daemonKinds(&team.Store{}, rs, ws, &capability.Store{}, "", nil)
	kinds = withDeviceKinds(kinds, &device.Store{}, "", deviceHooks{})
	out := map[string]bool{"keys": true, "ack": true}
	for k := range kinds {
		out[k] = true
	}
	return out
})

// daemonOwnedKind reports whether mail_submit must refuse kind: a kind the
// daemon owns, or one in a reserved namespace.
func daemonOwnedKind(kind string) bool {
	if ownedKinds()[kind] {
		return true
	}
	for _, p := range reservedKindPrefixes {
		if strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

// IPC error codes for mail_submit, documented in Docs/protocol/ipc.md.
const (
	CodeUnpaired     = "unpaired"
	CodeNoMailboxKey = "no_mailbox_key"
)

// MailSubmitParams are the params of "mail_submit". To is a paired peer's name
// or public key, with or without a leading "@". Body is a JSON object, or absent.
type MailSubmitParams struct {
	To   string          `json:"to"`
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body,omitempty"`
}

// MailSubmitResult is the result of "mail_submit": the new mail's id and its
// state, always queued.
type MailSubmitResult = mail.Submitted

func registerMail(srv *ipc.Server, ob *mail.Outbox, ps *peers.Store) {
	srv.Handle("mail_submit", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p MailSubmitParams
		if err := json.Unmarshal(params, &p); err != nil || p.To == "" || p.Kind == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "to and kind are required"}
		}
		// mail_submit sends only kinds the daemon does not own (review 36
		// L7). Every daemon kind is sent only by its own method, after that
		// method's checks: device.link, for example, has no signature of its
		// own, and the mail signature is its only proof that this device's
		// human confirmed (Docs/protocol/device.md §Kinds, review 36 M1).
		if daemonOwnedKind(p.Kind) {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: fmt.Sprintf("kind %q is sent only by the daemon's own commands, never by mail_submit", p.Kind)}
		}
		var body any
		if len(p.Body) > 0 {
			if err := json.Unmarshal(p.Body, &body); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "body must be a JSON object"}
			}
			if _, ok := body.(map[string]any); !ok {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "body must be a JSON object"}
			}
		}
		peer, err := resolvePeer(ctx, ps, p.To)
		if err != nil {
			return nil, err
		}
		res, err := ob.Submit(ctx, peer.PublicKey, p.Kind, body)
		switch {
		case errors.Is(err, mail.ErrUnpaired):
			return nil, &ipc.Error{Code: CodeUnpaired, Message: "that peer is not paired"}
		case errors.Is(err, mail.ErrNoMailboxKey):
			return nil, &ipc.Error{Code: CodeNoMailboxKey, Message: "that peer has no mailbox key; re-pair with it"}
		case err != nil:
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: err.Error()}
		}
		return res, nil
	})
}
