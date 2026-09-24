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
// CodeNotRequester and CodeUnknownSession are declared in session.go.
const (
	CodeUnknownGrant      = "unknown_grant"
	CodeNotGrantor        = "not_grantor"
	CodeForbiddenResource = "forbidden_resource"
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

// caseInsensitiveFS reports whether the default filesystem of this OS
// ignores case (NTFS; APFS and HFS+ as macOS formats them by default).
// filepath.EvalSymlinks does not canonicalise case on macOS, so
// "/Users/me/.DORYLINAE" would otherwise pass as a path different from the
// config dir (review 28 M5). On a case-sensitive macOS volume this refuses a
// little more than needed, which is the safe side.
var caseInsensitiveFS = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// foldPath cleans p and, on a case-insensitive filesystem, lowercases it.
func foldPath(p string) string {
	p = filepath.Clean(p)
	if caseInsensitiveFS {
		return strings.ToLower(p)
	}
	return p
}

func pathsEqual(a, b string) bool {
	return foldPath(a) == foldPath(b)
}

// pathEqualOrContains reports whether a and b are the same path, or one
// contains the other (a resource inside the config dir, or a resource that
// itself contains the config dir).
func pathEqualOrContains(a, b string) bool {
	a, b = foldPath(a), foldPath(b)
	if a == b {
		return true
	}
	return isWithin(a, b) || isWithin(b, a)
}

// isWithin reports whether child is inside parent (parent/child...), on
// path components: a sibling named "..x" is not outside (review 28 L1).
func isWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isFilesystemRoot(p string) bool {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(p)
		return vol != "" && (p == vol || p == vol+string(filepath.Separator))
	}
	return p == string(filepath.Separator)
}

var lookupGit = capability.LookGit

// gitCommand builds an issuance-time git command with the shared rules of
// capability.GitCommand (Docs/protocol/grant.md §Serving git): every inherited
// GIT_* variable removed, system and global config disabled, no prompts, and
// a 10 s timeout. The approval Precondition runs these under the approval
// Store's lock and inside its transaction (review 28 L5), so a hung git must
// not hold them for long. Callers pass a validated path (an absolute,
// existing, resolved directory) and fixed arguments.
func gitCommand(ctx context.Context, gitPath string, args ...string) (*exec.Cmd, context.CancelFunc) {
	return capability.GitCommand(ctx, gitPath, args...)
}

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
	// The same exact resolution as serving: no DWIM, no symbolic ref, a
	// commit (review 37 M1).
	if _, err := (capability.GitBackend{Git: gitPath}).BranchTip(context.Background(), resolved, branch); err != nil {
		return &ipc.Error{Code: CodeForbiddenResource, Message: "branch does not exist or is not a plain branch"}
	}
	return nil
}

// isGitTopLevel reports whether resolved is the top of a git work tree or a
// bare repository (Docs/protocol/grant.md §Issuance step 3).
func isGitTopLevel(gitPath, resolved string) bool {
	cmd, cancel := gitCommand(context.Background(), gitPath, "-C", resolved, "rev-parse", "--show-toplevel")
	defer cancel()
	out, err := cmd.Output()
	if err == nil {
		top, terr := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(string(out))))
		return terr == nil && sameDir(top, resolved)
	}
	// Bare: resolved must be the git directory itself, not a directory inside
	// one (discovery from repo.git/objects finds repo.git; review 37 L1).
	cmd2, cancel2 := gitCommand(context.Background(), gitPath, "-C", resolved, "rev-parse", "--is-bare-repository", "--absolute-git-dir")
	defer cancel2()
	out, err = cmd2.Output()
	if err != nil {
		return false
	}
	bare, dir, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if strings.TrimSpace(bare) != "true" {
		return false
	}
	gd, gerr := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(dir)))
	return gerr == nil && sameDir(gd, resolved)
}

// sameDir reports whether a and b name the same directory. It compares file
// identity, not spelling: git may report a path in another form than ours
// (8.3 short names on Windows, /private/var vs /var on macOS).
func sameDir(a, b string) bool {
	if pathsEqual(a, b) {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && fa.IsDir() && os.SameFile(fa, fb)
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
			// Steps 1-2 were checked before the transaction; the session may
			// have closed (revoking its grants) or the peer been removed
			// since. Re-check them here so no active grant is ever written
			// for a closed session or a removed peer (review 28 M1).
			if err := recheckIssuanceTx(ctx, tx, wsStore, p.Session, peer.PublicKey, nonLoopbackRelay); err != nil {
				return nil, err
			}
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
			// Re-check steps 1-3 against the current state (Docs/protocol/
			// grant.md §Issuance step 7; review 28 M2): session, peer and
			// D5 through tx, then the resource on disk.
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				if err := recheckIssuanceTx(ctx, tx, wsStore, sessionID, peerKey, nonLoopbackRelay); err != nil {
					return err
				}
				gr, err := capStore.GetTx(ctx, tx, grantID)
				if err != nil || gr.State != capability.StatePendingApproval {
					return &ipc.Error{Code: CodeBadState, Message: "the grant is no longer pending approval"}
				}
				if !time.Now().Before(gr.Exp) {
					return &ipc.Error{Code: CodeBadState, Message: "the grant expired before it was approved"}
				}
				return recheckResource(configDir, gr)
			},
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				now := time.Now()
				// The approval id marks the row as once-active for the
				// quarantine rule (capability.QuarantineHolds). It is read in
				// tx, where Confirm has just marked this grant's approval
				// approved, not from a variable the IPC goroutine sets after
				// Create returns: a confirm can in principle win that race
				// (review 35 L2).
				var approvalID string
				if err := tx.QueryRowContext(ctx, `SELECT id FROM approvals WHERE kind = ? AND subject = ? AND state = 'approved'`,
					approval.KindGrant, grantID).Scan(&approvalID); err != nil {
					return nil, fmt.Errorf("grant: read approval id: %w", err)
				}
				_, err := capStore.ActivateTx(ctx, tx, grantID, approvalID, now)
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
				return afterCommitResult{after: func(context.Context) { ob.Wake() }}, nil
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
		return map[string]any{"grant": grantView(ctx, ps, rec), "approval": view}, nil
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
		if ob != nil {
			ob.Wake()
		}
		if log != nil {
			_ = log.Append(ctx, audit.ActorCLI, "grant.revoke", map[string]any{"grant": p.ID, "peer": rec.Peer, "reason": capability.ReasonUser})
		}
		rec.State = capability.StateRevoked
		rec.Reason = capability.ReasonUser
		rec.RevokedAt = now
		return GrantRevokeResult{Grant: grantView(ctx, ps, rec), MailID: mailID}, nil
	})

	registerGrantPolicy(srv, capStore, apprStore, ps, log, configDir)
}

// auditTx appends an audit row through tx, matching audit.Log.Append's exact
// INSERT so rows are identical in shape. Used only from inside an approval
// Action's Perform, which may touch nothing but tx: audit.Log.Append uses
// its own *sql.DB, and calling it while tx is open on the daemon's single
// SQLite connection deadlocks (Docs/review/27-2.1a-review.md C1). Detail
// must carry only ids and enums, never content (Docs/protocol/grant.md
// §Audit).
func auditTx(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	return audit.AppendTx(ctx, tx, actor, action, detail)
}

// peerTrustTx reads the trust of a paired peer through tx; ok is false if
// the peer is no longer paired. Used to re-check issuance steps 1-2 inside
// the issuing transaction (Docs/review/28-2.2c-review.md M1, M2, M3).
func peerTrustTx(ctx context.Context, tx *sql.Tx, key string) (trust string, ok bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT trust FROM peers WHERE public_key = ?`, key).Scan(&trust)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("grant: read peer: %w", err)
	}
	return trust, true, nil
}

// recheckIssuanceTx re-checks issuance steps 1-2 of Docs/protocol/grant.md
// against the current state, inside tx: the session is still ours as
// requester, with this peer, open; the peer is still paired; and D5 still
// holds. It returns an *ipc.Error on failure.
func recheckIssuanceTx(ctx context.Context, tx *sql.Tx, wsStore *worksession.Store, sessionID, peerKey string, nonLoopbackRelay bool) error {
	sv, err := wsStore.GetTx(ctx, tx, sessionID)
	if err != nil || sv.State != worksession.StateOpen || sv.Role != worksession.RoleRequester || sv.Peer != peerKey {
		return &ipc.Error{Code: CodeBadState, Message: "the session is no longer open"}
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
	return nil
}

// recheckResource re-runs issuance step 3 on the stored resolved path: it
// must still resolve to itself (no symlink or junction swapped in since),
// still be an allowed directory and, for git.read, still hold the branch
// (Docs/protocol/grant.md §Issuance step 7, "re-check steps 1-3").
func recheckResource(configDir string, rec capability.Record) error {
	resolved, ierr := validateResource(configDir, rec.Path)
	if ierr != nil {
		return ierr
	}
	if !pathsEqual(resolved, rec.Path) {
		return &ipc.Error{Code: CodeForbiddenResource, Message: "the resource path changed since the grant was created"}
	}
	if rec.Action == capability.ActionGitRead {
		if ierr := validateGitResource(resolved, rec.Branch); ierr != nil {
			return ierr
		}
	}
	return nil
}

// revokeForRemovedPeer is the peers.Store.OnRemovedTx hook: it revokes every
// grant with the removed peer and deletes its policies, inside the removal's
// own transaction (Docs/protocol/grant.md §Session end, §Policies).
func revokeForRemovedPeer(capStore *capability.Store) func(ctx context.Context, tx *sql.Tx, key string) error {
	return func(ctx context.Context, tx *sql.Tx, key string) error {
		if _, err := capStore.RevokeForPeerTx(ctx, tx, key, capability.ReasonPeerRemoved, time.Now()); err != nil {
			return err
		}
		return capStore.PolicyDeleteForPeerTx(ctx, tx, key)
	}
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

func registerGrantPolicy(srv *ipc.Server, capStore *capability.Store, apprStore *approval.Store, ps *peers.Store, log *audit.Log, configDir string) {
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
		// The same resource rules as issuance (step 3): a policy for a
		// forbidden path would never match a grant anyway, but it should not
		// be approvable either (review 28 L2).
		resolved, ierr := validateResource(configDir, rawPath)
		if ierr != nil {
			return nil, ierr
		}
		if p.Action == capability.ActionGitRead {
			if branch == "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "a git.read policy needs #<branch>"}
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
			// The peer may have been removed while the approval waited
			// (peers remove deletes policies, so a policy inserted after it
			// would outlive the peer and revive on re-pairing), and other
			// policies may have been approved meanwhile (review 28 M3).
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				if _, ok, err := peerTrustTx(ctx, tx, pol.Peer); err != nil {
					return err
				} else if !ok {
					return &ipc.Error{Code: CodeBadState, Message: "the peer is no longer paired"}
				}
				n, err := capStore.PolicyCountTx(ctx, tx)
				if err != nil {
					return err
				}
				if n >= capability.MaxPolicies {
					return &ipc.Error{Code: ipc.CodeBadRequest, Message: "at most 50 policies are allowed"}
				}
				return nil
			},
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
		return map[string]approval.View{"approval": view}, nil
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
