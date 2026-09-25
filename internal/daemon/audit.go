package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// AuditListResult is the result of "audit_list".
type AuditListResult = audit.ListResult

// AuditVerifyParams are the params of "audit_verify": anchors are "ID:HASH".
type AuditVerifyParams struct {
	Anchors []string `json:"anchors,omitempty"`
}

// AuditVerifyResult is the result of "audit_verify".
type AuditVerifyResult struct {
	Verify *audit.VerifyResult `json:"verify"`
}

// AuditHeadResult is the result of "audit_head".
type AuditHeadResult struct {
	Head *audit.Head `json:"head"`
}

// AuditVerify parses anchors and runs the chain check; shared by the IPC
// handler and the CLI's read-only fallback (Docs/protocol/audit.md §Verification).
func AuditVerify(ctx context.Context, l *audit.Log, anchors []string) (*AuditVerifyResult, error) {
	as := make([]audit.Anchor, 0, len(anchors))
	for _, s := range anchors {
		a, err := audit.ParseAnchor(s)
		if err != nil {
			return nil, err
		}
		as = append(as, a)
	}
	res, err := l.Verify(ctx, as...)
	if err != nil {
		return nil, err
	}
	return &AuditVerifyResult{Verify: res}, nil
}

// auditError maps caller mistakes to bad_request.
func auditError(err error) error {
	if errors.Is(err, audit.ErrBadParams) || errors.Is(err, audit.ErrBadAnchor) {
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: err.Error()}
	}
	return err
}

// registerAudit wires "audit_list", "audit_verify" and "audit_head" (Docs/protocol/audit.md
// §Verification, §agentnet log). All three only read. audit_verify is exempt from the
// IPC 2-second rule: the walk takes about a second per 10^5 rows and the CLI calls it
// with its own timeout.
func registerAudit(srv *ipc.Server, l *audit.Log) {
	srv.Handle("audit_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p audit.ListParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		res, err := l.Query(ctx, p)
		if err != nil {
			return nil, auditError(err)
		}
		return res, nil
	})
	srv.Handle("audit_verify", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p AuditVerifyParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		res, err := AuditVerify(ctx, l, p.Anchors)
		if err != nil {
			return nil, auditError(err)
		}
		return res, nil
	})
	srv.Handle("audit_head", func(ctx context.Context, _ json.RawMessage) (any, error) {
		h, err := l.Head(ctx)
		if err != nil {
			return nil, err
		}
		return AuditHeadResult{Head: h}, nil
	})
}
