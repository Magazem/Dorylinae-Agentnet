package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// IPC error codes of the helper scope (Docs/protocol/device.md §IPC and CLI).
const (
	CodeNotHelper   = "not_helper"
	CodeUnknownLink = "unknown_link"
	CodeBadScope    = "bad_scope"
)

// maxScopeSummary caps the approval summary of a scope: every command, its
// repo path and its full resolved argv must fit in the approval window (the
// Windows window receives the summary through its environment block).
const maxScopeSummary = 16384

// DeviceScopeSetParams are the params of "device_scope_set".
type DeviceScopeSetParams struct {
	Peer  string          `json:"peer"`
	Scope json.RawMessage `json:"scope"`
}

// DeviceScopeSetResult is the result of "device_scope_set". Scope is the
// scope as it will be stored once approved: repo paths resolved and argv[0]
// an absolute path.
type DeviceScopeSetResult struct {
	Approval approval.View `json:"approval"`
	Scope    device.Scope  `json:"scope"`
}

// DevicePeerParams are the params of "device_scope_clear" and
// "device_scope_show".
type DevicePeerParams struct {
	Peer string `json:"peer"`
}

// DeviceScopeClearResult is the result of "device_scope_clear".
type DeviceScopeClearResult struct {
	Link DeviceLinkView `json:"link"`
}

// DeviceScopeShowResult is the result of "device_scope_show".
type DeviceScopeShowResult struct {
	Scope device.Scope `json:"scope"`
}

// DeviceScopeView is the link view's "scope" member (helper only).
type DeviceScopeView struct {
	Expires  string   `json:"expires"`
	Types    []string `json:"types"`
	Commands []string `json:"commands"`
}

// scopeApprovals remembers the pending device_scope approval for each
// controller, keyed on the controller's key (never the link id, review 36
// L6), so a newer scope_set, a scope_clear or an unlink rejects the older
// one. An approval's OnReject hook removes its entry (review 36 L8).
type scopeApprovals struct {
	mu sync.Mutex
	m  map[string]string // controller key -> approval id
}

func newScopeApprovals() *scopeApprovals { return &scopeApprovals{m: map[string]string{}} }

func (s *scopeApprovals) put(peer, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[peer] = id
}

func (s *scopeApprovals) take(peer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.m[peer]
	delete(s.m, peer)
	return id
}

// drop removes peer's entry only if it is still id.
func (s *scopeApprovals) drop(peer, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[peer] == id {
		delete(s.m, peer)
	}
}

// decodeScope decodes a scope strictly: no unknown member at any level.
func decodeScope(raw json.RawMessage) (device.Scope, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var sc device.Scope
	if err := dec.Decode(&sc); err != nil {
		return device.Scope{}, &ipc.Error{Code: CodeBadScope, Message: "scope: " + err.Error()}
	}
	if dec.More() {
		return device.Scope{}, &ipc.Error{Code: CodeBadScope, Message: "scope: trailing data"}
	}
	return sc, nil
}

// scopeError maps a validation error to its IPC error.
func scopeError(err error) error {
	var se *device.ScopeError
	if errors.As(err, &se) {
		code := CodeBadScope
		if se.Forbidden {
			code = CodeForbiddenResource
		}
		return &ipc.Error{Code: code, Message: se.Error()}
	}
	return err
}

// scopeResolver resolves repo paths with the grant resource rule: an
// existing absolute directory, resolved once, never the config dir (or inside
// or containing it), the home directory or a filesystem root.
func scopeResolver(configDir string) device.Resolver {
	return device.Resolver{RepoPath: func(raw string) (string, error) {
		resolved, ierr := validateResource(configDir, raw)
		if ierr != nil {
			return "", &device.ScopeError{Reason: ierr.Message, Forbidden: ierr.Code == CodeForbiddenResource}
		}
		return resolved, nil
	}}
}

// scopeSummary is what the human approves: every command's name, repo path
// and full argv with argv[0] resolved (Docs/protocol/device.md §Scope). The
// argv strings are JSON-quoted so that no control character or quote can
// change how the line reads.
func scopeSummary(peerName string, sc device.Scope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "let %s run commands on this device until %s, for %s requests:", peerName, sc.Expires, strings.Join(sc.Types, ", "))
	for _, c := range sc.Commands {
		dir, _ := sc.RepoPath(c.Repo)
		argv, _ := json.Marshal(c.Argv)
		fmt.Fprintf(&b, " [%s] in %s runs %s (timeout %d s", c.Name, quoteForSummary(dir), argv, c.TimeoutS)
		if len(c.Env) > 0 {
			fmt.Fprintf(&b, ", env %s", strings.Join(c.Env, " "))
		}
		b.WriteString(");")
	}
	b.WriteString(" Confirm only if you set this scope yourself.")
	return b.String()
}

func quoteForSummary(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

// helperLinkFor resolves peer and returns this device's active helper link
// with it: not_helper when this device is the peer's controller, unknown_link
// when there is no active link.
func helperLinkFor(ctx context.Context, ds *device.Store, ps *peers.Store, ref string) (peers.Peer, device.Link, error) {
	if strings.TrimPrefix(ref, "@") == "" {
		return peers.Peer{}, device.Link{}, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
	}
	peer, err := resolvePeer(ctx, ps, ref)
	if err != nil {
		return peers.Peer{}, device.Link{}, err
	}
	l, ok, err := ds.ActiveWith(ctx, peer.PublicKey)
	if err != nil {
		return peers.Peer{}, device.Link{}, err
	}
	if !ok {
		return peers.Peer{}, device.Link{}, &ipc.Error{Code: CodeUnknownLink, Message: "no active device link with this peer (see 'agentnet device list')"}
	}
	if l.Role != device.RoleHelper {
		return peers.Peer{}, device.Link{}, &ipc.Error{Code: CodeNotHelper, Message: "this device is the controller of that peer: a scope is set on the helper"}
	}
	return peer, l, nil
}

// registerDeviceScope wires "device_scope_set", "device_scope_clear" and
// "device_scope_show" (Docs/protocol/device.md §Scope, §IPC and CLI).
func registerDeviceScope(srv *ipc.Server, ds *device.Store, apprStore *approval.Store, ps *peers.Store, runner *helperRunner, configDir string, pending *scopeApprovals) {
	srv.Handle("device_scope_set", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DeviceScopeSetParams
		if err := json.Unmarshal(params, &p); err != nil || len(p.Scope) == 0 {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer and scope are required"}
		}
		sc, err := decodeScope(p.Scope)
		if err != nil {
			return nil, err
		}
		peer, link, err := helperLinkFor(ctx, ds, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		resolved, err := device.ValidateScope(sc, ds.Time(), scopeResolver(configDir))
		if err != nil {
			return nil, scopeError(err)
		}
		who := stripLongDigits(notify.Clean(peer.Name, 40))
		summary := scopeSummary(who, resolved)
		if len(summary) > maxScopeSummary {
			return nil, &ipc.Error{Code: CodeBadScope, Message: fmt.Sprintf("scope: too long to show in one approval (%d bytes of summary, at most %d); set fewer or shorter commands", len(summary), maxScopeSummary)}
		}
		// A newer scope supersedes one still waiting for its code.
		if old := pending.take(peer.PublicKey); old != "" {
			_, _ = apprStore.Reject(ctx, old, "superseded")
		}
		peerKey, linkID := peer.PublicKey, link.ID
		var approvalID string
		var idMu sync.Mutex
		action := approval.Action{
			// Every state check is here (review 26 N1): the link with this
			// peer is still active with this device as the helper (keyed on
			// the peer, never the link id, review 36 L6), and the scope has
			// not expired while waiting for the code.
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				cur, ok, err := ds.ActiveHelperLinkTx(ctx, tx, peerKey)
				if err != nil {
					return err
				}
				if !ok || cur.ID != linkID {
					return &ipc.Error{Code: CodeUnknownLink, Message: "the device link ended while the scope waited for approval"}
				}
				exp, err := resolved.ExpiresAt()
				if err != nil || !ds.Time().Before(exp) {
					return &ipc.Error{Code: CodeBadScope, Message: "expires: the scope expired while it waited for approval"}
				}
				return nil
			},
			// Perform touches only tx: the scope and its audit row commit
			// together (review 26 N1, review 27 C1).
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				now := ds.Time()
				idMu.Lock()
				aid := approvalID
				idMu.Unlock()
				if err := ds.SetScopeTx(ctx, tx, linkID, resolved, aid, now); err != nil {
					return nil, err
				}
				exp, _ := resolved.ExpiresAt()
				if err := auditTx(ctx, tx, audit.ActorCLI, "device.scope_set", map[string]any{
					"link": linkID, "types": resolved.Types, "commands": len(resolved.Commands), "repos": len(resolved.Repos),
					"expires_s": int64(exp.Sub(now).Seconds()), "approval": aid,
				}); err != nil {
					return nil, err
				}
				return afterCommitResult{after: func(context.Context) {
					pending.drop(peerKey, aid)
					runner.kick() // queued runs are checked against the new scope
				}}, nil
			},
			OnReject: func(context.Context) {
				idMu.Lock()
				aid := approvalID
				idMu.Unlock()
				pending.drop(peerKey, aid)
			},
		}
		view, aerr := apprStore.Create(ctx, approval.KindDeviceScope, linkID, summary, action)
		if aerr != nil {
			return nil, approvalError(aerr)
		}
		idMu.Lock()
		approvalID = view.ID
		idMu.Unlock()
		pending.put(peerKey, view.ID)
		return DeviceScopeSetResult{Approval: view, Scope: resolved}, nil
	})

	srv.Handle("device_scope_clear", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DevicePeerParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
		}
		peer, link, err := helperLinkFor(ctx, ds, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		tx, err := ds.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("device: begin scope clear: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := ds.ClearScopeTx(ctx, tx, link.ID); err != nil {
			return nil, err
		}
		if err := auditTx(ctx, tx, audit.ActorCLI, "device.scope_clear", map[string]any{"link": link.ID}); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("device: commit scope clear: %w", err)
		}
		// Narrowing takes effect at once: a scope still waiting for its code
		// is rejected, and queued runs are dropped.
		if old := pending.take(peer.PublicKey); old != "" {
			_, _ = apprStore.Reject(ctx, old, "scope_cleared")
		}
		runner.kick()
		return DeviceScopeClearResult{Link: deviceView(ctx, ps, ds, link)}, nil
	})

	srv.Handle("device_scope_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DevicePeerParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
		}
		_, link, err := helperLinkFor(ctx, ds, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		sc, err := ds.Scope(ctx, link.ID)
		if errors.Is(err, device.ErrNoScope) {
			return nil, &ipc.Error{Code: CodeBadState, Message: "no scope is set for this link"}
		}
		if err != nil {
			return nil, err
		}
		return DeviceScopeShowResult{Scope: sc}, nil
	})
}
