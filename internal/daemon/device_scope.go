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
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
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
//
// A device_scope_set holds its controller's set lock from rejecting the older
// approval until it has recorded its own, so that concurrent sets supersede
// each other in turn, never both stay pending (review 55 R55-085). take waits
// for that lock too, so a clear or an unlink cannot miss an approval being
// created.
type scopeApprovals struct {
	mu   sync.Mutex
	m    map[string]string   // controller key -> approval id
	sets map[string]*setLock // controller key -> its set lock, while used
}

// setLock is one controller's set lock; n counts its holders and waiters.
type setLock struct {
	mu sync.Mutex
	n  int
}

func newScopeApprovals() *scopeApprovals {
	return &scopeApprovals{m: map[string]string{}, sets: map[string]*setLock{}}
}

// lock takes peer's set lock and returns its release.
func (s *scopeApprovals) lock(peer string) (unlock func()) {
	s.mu.Lock()
	l := s.sets[peer]
	if l == nil {
		l = &setLock{}
		s.sets[peer] = l
	}
	l.n++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		if l.n--; l.n == 0 {
			delete(s.sets, peer)
		}
	}
}

func (s *scopeApprovals) put(peer, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[peer] = id
}

// take removes and returns peer's pending approval, after any
// device_scope_set of peer under way has recorded its own.
func (s *scopeApprovals) take(peer string) string {
	defer s.lock(peer)()
	return s.takeLocked(peer)
}

// takeLocked is take for a caller that holds peer's set lock.
func (s *scopeApprovals) takeLocked(peer string) string {
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

// scopeFacts is what the human approves (Docs/protocol/approval.md §Contents
// per kind): the controller, and every command's name, repo directory and
// full argv with argv[0] resolved (Docs/protocol/device.md §Scope). The
// builder quotes every path and argv string with the displayQuote rule, so
// nothing in them can change how the line reads (review 40 M1).
func scopeFacts(peer approvaltext.Peer, sc device.Scope) (approvaltext.Scope, error) {
	exp, err := sc.ExpiresAt()
	if err != nil {
		return approvaltext.Scope{}, &ipc.Error{Code: CodeBadScope, Message: "expires: " + err.Error()}
	}
	f := approvaltext.Scope{Peer: peer, Types: sc.Types, Expires: exp.UTC()}
	for _, c := range sc.Commands {
		dir, _ := sc.RepoPath(c.Repo)
		f.Commands = append(f.Commands, approvaltext.ScopeCommand{Name: c.Name, Dir: dir, Argv: c.Argv, TimeoutS: c.TimeoutS, Env: c.Env})
	}
	return f, nil
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
		// The whole scope must fit in the window, never cut: a longer one is
		// refused here, before any approval exists (R55-F5, review 55
		// R55-007 / C14-02).
		pf, err := peerFacts(ctx, ds.DB, peer.PublicKey)
		if err != nil {
			return nil, err
		}
		facts, err := scopeFacts(pf, resolved)
		if err != nil {
			return nil, err
		}
		summary, err := approvaltext.BuildScope(facts)
		if errors.Is(err, approvaltext.ErrTooLong) {
			return nil, &ipc.Error{Code: CodeBadScope, Message: fmt.Sprintf("scope: too long to show in full in the approval window (at most %d code points of summary); set fewer or shorter commands, or run a wrapper script", notify.MaxWindowSummary)}
		}
		if err != nil {
			return nil, err
		}
		// A newer scope supersedes one still waiting for its code. The set
		// lock is held until this approval is recorded (R55-085).
		defer pending.lock(peer.PublicKey)()
		if old := pending.takeLocked(peer.PublicKey); old != "" {
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
			// Precondition compares (R55-F5): the captured scope and the
			// controller's current name.
			Rebuild: rebuildWith(facts, func(ctx context.Context, tx *sql.Tx) (approvaltext.Scope, error) {
				pf, err := peerFacts(ctx, tx, peerKey)
				if err != nil {
					return approvaltext.Scope{}, err
				}
				return scopeFacts(pf, resolved)
			}, approvaltext.BuildScope),
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
