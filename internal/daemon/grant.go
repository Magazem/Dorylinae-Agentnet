package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// IPC error codes for grants (Docs/protocol/grant.md §IPC).
const (
	CodeUnknownGrant      = "unknown_grant"
	CodeNotGrantor        = "not_grantor"
	CodeForbiddenResource = "forbidden_resource"
	CodeNotRequester      = "not_requester"
	CodeUnknownSession    = "unknown_session"
	CodeUnknownPolicy     = "unknown_policy"
)

// DefaultGrantExpiry is the default of "--expires" (Docs/protocol/grant.md §Issuance step 4).
const DefaultGrantExpiry = 2 * time.Hour

// DefaultPolicyUntil and MaxPolicyUntil are the policy's own end
// (Docs/protocol/grant.md §Policies).
const (
	DefaultPolicyUntil = 30 * 24 * time.Hour
	MaxPolicyUntil     = 90 * 24 * time.Hour
)

// grantIdentity is the subset of daemon identity grant.go needs to sign
// tokens: the own key in wire form and a private-key loader that clears the
// seed after use, matching identityPriv's pattern in mail.go.
type grantIdentity struct {
	Self string
	Priv func() (ed25519.PrivateKey, error)
}

// GrantResourceView is the "resource" member of a grant view.
type GrantResourceView struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Branch string `json:"branch,omitempty"`
	Path   string `json:"path,omitempty"` // grantor (issued) side only
}

// GrantPeerRef is a light peer reference in a grant or policy view.
type GrantPeerRef struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// GrantView is the "grant view" of Docs/protocol/grant.md §IPC.
type GrantView struct {
	ID        string            `json:"id"`
	Direction string            `json:"direction"`
	Peer      GrantPeerRef      `json:"peer"`
	Session   string            `json:"session"`
	Action    string            `json:"action"`
	Resource  GrantResourceView `json:"resource"`
	Scope     string            `json:"scope,omitempty"`
	Nbf       string            `json:"nbf"`
	Exp       string            `json:"exp"`
	Sensitive bool              `json:"sensitive"`
	State     string            `json:"state"`
	RevokedAt string            `json:"revoked_at,omitempty"`
	Reason    string            `json:"reason,omitempty"`
}

func wireTimeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

func peerRef(ctx context.Context, ps *peers.Store, key string) GrantPeerRef {
	if p, err := resolvePeer(ctx, ps, key); err == nil {
		return GrantPeerRef{Name: p.Name, PublicKey: p.PublicKey}
	}
	return GrantPeerRef{PublicKey: key}
}

// grantViewTx builds a GrantView without any DB call outside the caller's
// transaction (the peer ref carries only the key, not its name): safe to
// call from inside an approval Action's Perform, which runs under the
// approval Store's own transaction on the daemon's single connection
// (Docs/review/27-2.1a-review.md C1, Docs/review/26-2.2a-review.md N3).
func grantViewTx(rec capability.Record) GrantView {
	v := grantViewCommon(rec)
	v.Peer = GrantPeerRef{PublicKey: rec.Peer}
	return v
}

func grantView(ctx context.Context, ps *peers.Store, rec capability.Record) GrantView {
	v := grantViewCommon(rec)
	v.Peer = peerRef(ctx, ps, rec.Peer)
	return v
}

func grantViewCommon(rec capability.Record) GrantView {
	v := GrantView{
		ID: rec.ID, Direction: rec.Direction, Session: rec.Session,
		Action: rec.Action, Scope: rec.Scope, Nbf: wireTimeStr(rec.Nbf), Exp: wireTimeStr(rec.Exp),
		Sensitive: rec.Sensitive, State: rec.State, Reason: rec.Reason,
	}
	v.Resource = GrantResourceView{Kind: capability.KindFS, Label: rec.Label}
	if rec.Action == capability.ActionGitRead {
		v.Resource.Kind = capability.KindGit
		v.Resource.Branch = rec.Branch
	}
	if rec.Direction == capability.DirectionIssued {
		v.Resource.Path = rec.Path
	}
	if !rec.RevokedAt.IsZero() {
		v.RevokedAt = wireTimeStr(rec.RevokedAt)
	}
	return v
}

// ---- Resource validation (Docs/protocol/grant.md §Issuance step 3) ----

// validateResource resolves rawPath once with filepath.EvalSymlinks and
// refuses a relative path, a non-existent or non-directory path, the config
// dir (or anything inside or containing it), the user's home directory
// itself, and a filesystem root.
func validateResource(configDir, rawPath string) (resolved string, ierr *ipc.Error) {
	if rawPath == "" {
		return "", &ipc.Error{Code: ipc.CodeBadRequest, Message: "resource path is required"}
	}
	if !filepath.IsAbs(rawPath) {
		return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource must be an absolute path"}
	}
	resolved, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource does not exist"}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource must be an existing directory"}
	}
	if cfg, err := filepath.EvalSymlinks(configDir); err == nil {
		if pathEqualOrContains(cfg, resolved) {
			return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource may not be the config dir or contain/be inside it"}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if h, err := filepath.EvalSymlinks(home); err == nil && pathsEqual(h, resolved) {
			return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource may not be the home directory"}
		}
	}
	if isFilesystemRoot(resolved) {
		return "", &ipc.Error{Code: CodeForbiddenResource, Message: "resource may not be a filesystem root"}
	}
	return resolved, nil
}

func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// pathEqualOrContains reports whether a and b are the same path, or one
// contains the other (a resource inside the config dir, or a resource that
// itself contains the config dir).
func pathEqualOrContains(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if pathsEqual(a, b) {
		return true
	}
	return isWithin(a, b) || isWithin(b, a)
}

// isWithin reports whether child is inside parent (parent/child...).
func isWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

func isFilesystemRoot(p string) bool {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(p)
		return vol != "" && (p == vol || p == vol+string(filepath.Separator))
	}
	return p == string(filepath.Separator)
}

var lookupGit = func() (string, error) { return exec.LookPath("git") }

// validateGitResource checks that resolved is the top of a git work tree or
// a bare repository, and that branch names an existing refs/heads/ branch
// (Docs/protocol/grant.md §Issuance step 3). Commands run without a shell.
func validateGitResource(resolved, branch string) *ipc.Error {
	gitPath, err := lookupGit()
	if err != nil {
		return &ipc.Error{Code: CodeForbiddenResource, Message: "git is not available"}
	}
	if !isGitTopLevel(gitPath, resolved) {
		return &ipc.Error{Code: CodeForbiddenResource, Message: "resource is not a git work tree or bare repository"}
	}
	//nolint:gosec // no shell; gitPath comes from exec.LookPath and resolved/branch
	// are already validated (an absolute existing directory; branch matches
	// the strict refs/heads/ grammar of capability.checkBranch).
	if err := exec.Command(gitPath, "-C", resolved, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run(); err != nil {
		return &ipc.Error{Code: CodeForbiddenResource, Message: "branch does not exist"}
	}
	return nil
}

// isGitTopLevel reports whether resolved is the top of a git work tree or a
// bare repository (Docs/protocol/grant.md §Issuance step 3).
func isGitTopLevel(gitPath, resolved string) bool {
	//nolint:gosec // no shell; gitPath comes from exec.LookPath, resolved is
	// already validated (an absolute, existing, resolved directory).
	out, err := exec.Command(gitPath, "-C", resolved, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		top, terr := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		return terr == nil && pathsEqual(top, resolved)
	}
	//nolint:gosec // see above
	out, err = exec.Command(gitPath, "-C", resolved, "rev-parse", "--is-bare-repository").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// deriveLabel builds the label of Docs/protocol/grant.md §Grant object: the
// basename of resolved, lowercased and cleaned to [a-z0-9._-], plus "-" and
// 4 random hex characters.
func deriveLabel(resolved string) string {
	base := strings.ToLower(filepath.Base(resolved))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "resource"
	}
	if len(name) > 59 {
		name = name[:59]
	}
	var suf [2]byte
	_, _ = rand.Read(suf[:])
	return name + "-" + hex.EncodeToString(suf[:])
}

// ---- IPC params ----

// GrantCreateParams are the params of "grant_create".
type GrantCreateParams struct {
	Peer     string `json:"peer"`
	Session  string `json:"session"`
	Action   string `json:"action"`
	Resource string `json:"resource"` // "<path>" or "<path>#<branch>"
	Scope    string `json:"scope,omitempty"`
	Expires  string `json:"expires,omitempty"`
	Public   bool   `json:"public,omitempty"`
}

// GrantListParams are the params of "grant_list".
type GrantListParams struct {
	Session   string `json:"session,omitempty"`
	Direction string `json:"direction,omitempty"`
	State     string `json:"state,omitempty"`
}

// GrantShowParams are the params of "grant_show".
type GrantShowParams struct {
	ID string `json:"id"`
}

// GrantRevokeParams are the params of "grant_revoke".
type GrantRevokeParams struct {
	ID string `json:"id"`
}

// GrantRevokeResult is the result of "grant_revoke".
type GrantRevokeResult struct {
	Grant     GrantView `json:"grant"`
	MailID    string    `json:"mail_id"`
	Duplicate bool      `json:"duplicate,omitempty"`
}

// splitResource splits "<path>[#<branch>]" on the first '#'.
func splitResource(s string) (path, branch string) {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func parseExpires(s string) (time.Duration, *ipc.Error) {
	if s == "" {
		return DefaultGrantExpiry, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, &ipc.Error{Code: ipc.CodeBadRequest, Message: "expires must be a duration like \"2h\""}
	}
	if d < capability.MinExpiry || d > capability.MaxExpiry {
		return 0, &ipc.Error{Code: ipc.CodeBadRequest, Message: "expires must be 1 minute to 7 days"}
	}
	return d, nil
}

// registerGrant wires grant_create/list/show/revoke and
// grant_policy_add/list/remove (Docs/protocol/grant.md §IPC).
func registerGrant(srv *ipc.Server, capStore *capability.Store, wsStore *worksession.Store, apprStore *approval.Store,
	ps *peers.Store, ob *mail.Outbox, log *audit.Log, id grantIdentity, configDir string, nonLoopbackRelay bool) {

	srv.Handle("grant_create", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p GrantCreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		if strings.TrimPrefix(p.Peer, "@") == "" || p.Session == "" || p.Action == "" || p.Resource == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer, session, action and resource are required"}
		}
		if p.Action != capability.ActionFSRead && p.Action != capability.ActionGitRead {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "action must be fs.read or git.read"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		// Step 1 (Docs/protocol/grant.md §Issuance): the session exists with
		// role = requester, peer = the resolved peer, state open.
		sess, serr := wsStore.Get(ctx, p.Session)
		if serr != nil {
			if errors.Is(serr, worksession.ErrUnknownSession) {
				return nil, &ipc.Error{Code: CodeUnknownSession, Message: "no such work session"}
			}
			return nil, serr
		}
		if sess.Role != worksession.RoleRequester || sess.Peer != peer.PublicKey {
			return nil, &ipc.Error{Code: CodeNotRequester, Message: "only the requester of the session may create a grant"}
		}
		if sess.State != worksession.StateOpen {
			return nil, &ipc.Error{Code: CodeBadState, Message: "the session is not open"}
		}
		// Step 2, D5.
		if nonLoopbackRelay && peer.Trust == peers.TrustRelay {
			return nil, &ipc.Error{Code: CodeUnverifiedPeer, Message: "the peer's trust is \"relay\" on a non-loopback relay; re-pair or run 'agentnet peers verify'"}
		}

		rawPath, branch := splitResource(p.Resource)
		resolved, ierr := validateResource(configDir, rawPath)
		if ierr != nil {
			return nil, ierr
		}
		if p.Action == capability.ActionGitRead {
			if branch == "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "a git.read resource needs #<branch>"}
			}
			if ierr := validateGitResource(resolved, branch); ierr != nil {
				return nil, ierr
			}
		} else if branch != "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "fs.read does not take a branch"}
		}
		if p.Scope != "" && !capability.ValidScopePath(p.Scope) {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "scope is not a valid path"}
		}
		expires, ierr := parseExpires(p.Expires)
		if ierr != nil {
			return nil, ierr
		}
		// fs.read is always sensitive; git.read is sensitive unless --public
		// (Docs/protocol/grant.md §Issuance step 5, OD-P2-13).
		sensitive := p.Action != capability.ActionGitRead || !p.Public

		now := time.Now()
		label := deriveLabel(resolved)
		priv, err := id.Priv()
		if err != nil {
			return nil, err
		}
		defer clear(priv)
		g := capability.Grant{
			V: 1, ID: capability.NewID(), Iss: id.Self, Aud: peer.PublicKey, Session: p.Session, Action: p.Action,
			Resource: capability.Resource{Kind: capability.KindFS, Label: label},
			Scope:    p.Scope,
			Nbf:      now, Exp: now.Add(expires), Sensitive: sensitive,
		}
		if p.Action == capability.ActionGitRead {
			g.Resource.Kind = capability.KindGit
			g.Resource.Branch = branch
		}
		tok, err := capability.Sign(priv, g)
		if err != nil {
			return nil, fmt.Errorf("grant: sign: %w", err)
		}
		wire, err := capability.Canonical(tok)
		if err != nil {
			return nil, fmt.Errorf("grant: canonicalize: %w", err)
		}
		rec := capability.Record{
			ID: g.ID, Direction: capability.DirectionIssued, Peer: peer.PublicKey, Session: p.Session, Action: p.Action,
			Label: label, Path: resolved, Branch: branch, Scope: p.Scope, Sensitive: sensitive,
			Nbf: g.Nbf, Exp: g.Exp, Token: string(wire),
		}

		// A matching policy issues at once (Docs/protocol/grant.md §Issuance
		// step 7); otherwise a human approval is created.
		match, merr := capStore.Match(ctx, capability.MatchParams{
			Peer: peer.PublicKey, Action: p.Action, Path: resolved, Branch: branch, Scope: p.Scope,
			Sensitive: sensitive, ExpiresS: int(expires.Seconds()), Now: now,
		})
		if merr != nil {
			return nil, merr
		}
		if match != nil {
			rec.Policy = match.ID
			tx, terr := capStore.DB.BeginTx(ctx, nil)
			if terr != nil {
				return nil, fmt.Errorf("grant: begin: %w", terr)
			}
			committed := false
			defer func() {
				if !committed {
					_ = tx.Rollback()
				}
			}()
			if err := capStore.InsertActiveTx(ctx, tx, rec); err != nil {
				return nil, err
			}
			tokenAny, perr := agentcard.ParseStrict(wire)
			if perr != nil {
				return nil, perr
			}
			if _, err := ob.SubmitTx(ctx, tx, peer.PublicKey, "grant", map[string]any{"token": tokenAny}); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("grant: commit: %w", err)
			}
			committed = true
			ob.Wake()
			rec.State = capability.StateActive
			if log != nil {
				_ = log.Append(ctx, audit.ActorCLI, "grant.create", map[string]any{
					"grant": g.ID, "session": p.Session, "peer": peer.PublicKey, "action": p.Action,
					"sensitive": sensitive, "expires_s": int(expires.Seconds()),
				})
				_ = log.Append(ctx, audit.ActorDaemon, "grant.auto", map[string]any{"grant": g.ID, "policy": match.ID})
			}
			return map[string]GrantView{"grant": grantView(ctx, ps, rec)}, nil
		}

		if err := capStore.InsertPending(ctx, rec); err != nil {
			return nil, err
		}
		rec.State = capability.StatePendingApproval
		sessionID := p.Session
		grantID := g.ID
		peerKey := peer.PublicKey
		action := approval.Action{
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				sv, err := wsStore.GetTx(ctx, tx, sessionID)
				if err != nil || sv.State != worksession.StateOpen || sv.Role != worksession.RoleRequester || sv.Peer != peerKey {
					return &ipc.Error{Code: CodeBadState, Message: "the session is no longer open"}
				}
				gr, err := capStore.GetTx(ctx, tx, grantID)
				if err != nil || gr.State != capability.StatePendingApproval {
					return &ipc.Error{Code: CodeBadState, Message: "the grant is no longer pending approval"}
				}
				return nil
			},
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				now := time.Now()
				ar, err := capStore.ActivateTx(ctx, tx, grantID, now)
				if err != nil {
					return nil, err
				}
				tokenAny, perr := agentcard.ParseStrict(wire)
				if perr != nil {
					return nil, perr
				}
				sub, err := ob.SubmitTx(ctx, tx, peerKey, "grant", map[string]any{"token": tokenAny})
				if err != nil {
					return nil, err
				}
				if log != nil {
					if err := auditTx(ctx, tx, audit.ActorDaemon, "grant.issue", map[string]any{
						"grant": grantID, "peer": peerKey, "mail": sub.ID,
					}); err != nil {
						return nil, err
					}
				}
				return map[string]GrantView{"grant": grantViewTx(ar)}, nil
			},
		}
		summary := fmt.Sprintf("approve grant %s on %s to %s for %s?", p.Action, label, peer.Name, expires.String())
		view, aerr := apprStore.Create(ctx, approval.KindGrant, g.ID, summary, action)
		if aerr != nil {
			_ = capStore.Delete(ctx, g.ID) // review 26 N5: drop the pending row if Create fails
			return nil, approvalError(aerr)
		}
		if log != nil {
			_ = log.Append(ctx, audit.ActorCLI, "grant.create", map[string]any{
				"grant": g.ID, "session": p.Session, "peer": peer.PublicKey, "action": p.Action,
				"sensitive": sensitive, "expires_s": int(expires.Seconds()), "approval": view.ID,
			})
		}
		res, merr := mergeApproval(map[string]GrantView{"grant": grantView(ctx, ps, rec)}, view)
		if merr != nil {
			return nil, merr
		}
		return res, nil
	})

	srv.Handle("grant_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p GrantListParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		recs, err := capStore.List(ctx, capability.ListFilter{Session: p.Session, Direction: p.Direction, State: p.State})
		if err != nil {
			return nil, err
		}
		views := make([]GrantView, 0, len(recs))
		for _, r := range recs {
			views = append(views, grantView(ctx, ps, r))
		}
		return map[string][]GrantView{"grants": views}, nil
	})

	srv.Handle("grant_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p GrantShowParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		rec, err := capStore.Get(ctx, p.ID)
		if err != nil {
			return nil, grantError(err)
		}
		out := map[string]any{"grant": grantView(ctx, ps, rec)}
		if rec.Direction == capability.DirectionHeld {
			tokenAny, terr := agentcard.ParseStrict([]byte(rec.Token))
			if terr == nil {
				out["token"] = tokenAny
			}
		}
		return out, nil
	})

	srv.Handle("grant_revoke", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p GrantRevokeParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		rec, err := capStore.Get(ctx, p.ID)
		if err != nil {
			return nil, grantError(err)
		}
		if rec.Direction != capability.DirectionIssued {
			return nil, &ipc.Error{Code: CodeNotGrantor, Message: "only the grantor may revoke a grant"}
		}
		if rec.State == capability.StateRevoked {
			return GrantRevokeResult{Grant: grantView(ctx, ps, rec), Duplicate: true}, nil
		}
		now := time.Now()
		tx, err := capStore.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("grant: begin revoke: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if _, err := capStore.RevokeTx(ctx, tx, p.ID, capability.ReasonUser, now); err != nil {
			return nil, err
		}
		var mailID string
		if ob != nil {
			sub, err := ob.SubmitTx(ctx, tx, rec.Peer, "grant.revoke", map[string]any{
				"at": wireTimeStr(now), "grant": p.ID, "reason": capability.ReasonUser,
			})
			if err != nil {
				return nil, err
			}
			mailID = sub.ID
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("grant: commit revoke: %w", err)
		}
		committed = true
		ob.Wake()
		if log != nil {
			_ = log.Append(ctx, audit.ActorCLI, "grant.revoke", map[string]any{"grant": p.ID, "peer": rec.Peer, "reason": capability.ReasonUser})
		}
		rec.State = capability.StateRevoked
		rec.Reason = capability.ReasonUser
		rec.RevokedAt = now
		return GrantRevokeResult{Grant: grantView(ctx, ps, rec), MailID: mailID}, nil
	})

	registerGrantPolicy(srv, capStore, apprStore, ps, log)
}

// auditTx appends an audit row through tx, matching audit.Log.Append's exact
// INSERT so rows are identical in shape. Used only from inside an approval
// Action's Perform, which may touch nothing but tx: audit.Log.Append uses
// its own *sql.DB, and calling it while tx is open on the daemon's single
// SQLite connection deadlocks (Docs/review/27-2.1a-review.md C1). Detail
// must carry only ids and enums, never content (Docs/protocol/grant.md
// §Audit).
func auditTx(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("grant: marshal audit detail: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (ts, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), actor, action, string(raw)); err != nil {
		return fmt.Errorf("grant: append audit %s: %w", action, err)
	}
	return nil
}

func grantError(err error) error {
	if errors.Is(err, capability.ErrUnknownGrant) {
		return &ipc.Error{Code: CodeUnknownGrant, Message: "no such grant"}
	}
	return err
}

// ---- Policies ----

// GrantPolicyAddParams are the params of "grant_policy_add".
type GrantPolicyAddParams struct {
	Peer       string `json:"peer"`
	Action     string `json:"action"`
	Resource   string `json:"resource"`
	Scope      string `json:"scope,omitempty"`
	Public     bool   `json:"public,omitempty"`
	MaxExpires string `json:"max_expires"`
	Until      string `json:"until,omitempty"`
}

// GrantPolicyView is the IPC view of a grant_policies row.
type GrantPolicyView struct {
	ID          string       `json:"id"`
	Peer        GrantPeerRef `json:"peer"`
	Action      string       `json:"action"`
	Branch      string       `json:"branch,omitempty"`
	Scope       string       `json:"scope,omitempty"`
	Public      bool         `json:"public"`
	MaxExpiresS int          `json:"max_expires_s"`
	Until       string       `json:"until"`
	Created     string       `json:"created"`
}

func policyView(ctx context.Context, ps *peers.Store, p capability.Policy) GrantPolicyView {
	v := policyViewTx(p)
	v.Peer = peerRef(ctx, ps, p.Peer)
	return v
}

// policyViewTx is policyView without any DB call outside the caller's
// transaction; see grantViewTx.
func policyViewTx(p capability.Policy) GrantPolicyView {
	return GrantPolicyView{
		ID: p.ID, Peer: GrantPeerRef{PublicKey: p.Peer}, Action: p.Action, Branch: p.Branch, Scope: p.Scope,
		Public: p.Public, MaxExpiresS: p.MaxExpiresS, Until: wireTimeStr(p.Until), Created: wireTimeStr(p.Created),
	}
}

func registerGrantPolicy(srv *ipc.Server, capStore *capability.Store, apprStore *approval.Store, ps *peers.Store, log *audit.Log) {
	srv.Handle("grant_policy_add", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p GrantPolicyAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		if strings.TrimPrefix(p.Peer, "@") == "" || p.Action == "" || p.Resource == "" || p.MaxExpires == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer, action, resource and max_expires are required"}
		}
		if p.Action != capability.ActionFSRead && p.Action != capability.ActionGitRead {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "action must be fs.read or git.read"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		rawPath, branch := splitResource(p.Resource)
		resolved, err := filepath.EvalSymlinks(rawPath)
		if err != nil || !filepath.IsAbs(rawPath) {
			return nil, &ipc.Error{Code: CodeForbiddenResource, Message: "resource must be an absolute, existing path"}
		}
		if p.Action == capability.ActionGitRead && branch == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "a git.read policy needs #<branch>"}
		}
		if p.Scope != "" && !capability.ValidScopePath(p.Scope) {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "scope is not a valid path"}
		}
		maxExpires, ierr := parseExpires(p.MaxExpires)
		if ierr != nil {
			return nil, ierr
		}
		until := DefaultPolicyUntil
		if p.Until != "" {
			d, err := time.ParseDuration(p.Until)
			if err != nil || d <= 0 || d > MaxPolicyUntil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "until must be a duration up to 90 days"}
			}
			until = d
		}
		count, err := capStore.PolicyCount(ctx)
		if err != nil {
			return nil, err
		}
		if count >= capability.MaxPolicies {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "at most 50 policies are allowed"}
		}
		now := time.Now()
		pol := capability.Policy{
			ID: capability.NewPolicyID(), Peer: peer.PublicKey, Action: p.Action, Path: resolved, Branch: branch,
			Scope: p.Scope, Public: p.Public, MaxExpiresS: int(maxExpires.Seconds()), Until: now.Add(until), Created: now,
		}
		action := approval.Action{
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				if err := capStore.PolicyInsertTx(ctx, tx, pol); err != nil {
					return nil, err
				}
				if log != nil {
					if err := auditTx(ctx, tx, audit.ActorDaemon, "grant.policy_add", map[string]any{
						"policy": pol.ID, "peer": pol.Peer, "action": pol.Action,
					}); err != nil {
						return nil, err
					}
				}
				return map[string]GrantPolicyView{"policy": policyViewTx(pol)}, nil
			},
		}
		summary := fmt.Sprintf("approve a grant policy: %s on %s for %s, up to %s?", p.Action, pol.Path, peer.Name, maxExpires.String())
		view, aerr := apprStore.Create(ctx, approval.KindGrantPolicy, pol.ID, summary, action)
		if aerr != nil {
			return nil, approvalError(aerr)
		}
		res, merr := mergeApproval(nil, view)
		if merr != nil {
			return nil, merr
		}
		return res, nil
	})

	srv.Handle("grant_policy_list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		pols, err := capStore.PolicyList(ctx)
		if err != nil {
			return nil, err
		}
		views := make([]GrantPolicyView, 0, len(pols))
		for _, p := range pols {
			views = append(views, policyView(ctx, ps, p))
		}
		return map[string][]GrantPolicyView{"policies": views}, nil
	})

	srv.Handle("grant_policy_remove", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		if err := capStore.PolicyDelete(ctx, p.ID); err != nil {
			if errors.Is(err, capability.ErrUnknownPolicy) {
				return nil, &ipc.Error{Code: CodeUnknownPolicy, Message: "no such policy"}
			}
			return nil, err
		}
		if log != nil {
			_ = log.Append(ctx, audit.ActorCLI, "grant.policy_remove", map[string]string{"policy": p.ID})
		}
		return map[string]bool{"ok": true}, nil
	})
}
