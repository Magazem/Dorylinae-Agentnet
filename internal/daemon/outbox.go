package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

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
		// The own-device link kinds carry no signature of their own: the mail
		// signature is their only proof that this device's human confirmed, so
		// only the device handlers send them, after the local approval
		// (Docs/protocol/device.md §Kinds, review 36 M1).
		if strings.HasPrefix(p.Kind, "device.") {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "device.* mail is sent only by 'agentnet device'"}
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
