package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// IPC error codes for requests, documented in Docs/protocol/ipc.md and
// Docs/protocol/request.md. unknown_team, ambiguous_team, not_team_member are
// shared with team.go (as CodeUnknownTeam, CodeAmbiguousTeam, CodeNotMember)
// but request_submit's not_team_member is a distinct code from team.go's
// CodeNotMember, so it gets its own constant here.
const (
	CodeNoSharedTeam         = "no_shared_team"
	CodeRequestNotTeamMember = "not_team_member"
	CodeUnverifiedPeer       = "unverified_peer"
	CodeRequestTooLarge      = "request_too_large"
	CodeIdempotencyConflict  = "idempotency_conflict"
)

// ArtifactParam is one entry of request_submit's "artifacts".
type ArtifactParam struct {
	URL    string `json:"url,omitempty"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	Path   string `json:"path,omitempty"`
}

// GrantParam is request_submit's "requested_grant".
type GrantParam struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Note     string `json:"note,omitempty"`
}

// RequestSubmitParams are the params of "request_submit".
type RequestSubmitParams struct {
	To             string          `json:"to"`
	Type           string          `json:"type"`
	Team           string          `json:"team,omitempty"`
	Title          string          `json:"title"`
	Brief          string          `json:"brief"`
	Urgency        string          `json:"urgency,omitempty"`
	UrgencyReason  string          `json:"urgency_reason,omitempty"`
	Artifacts      []ArtifactParam `json:"artifacts,omitempty"`
	RequestedGrant *GrantParam     `json:"requested_grant,omitempty"`
	Deadline       string          `json:"deadline,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

// teamRefResult is the "team": {"id","name"} member of the submit result.
type teamRefResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// submitPeerResult is request_submit's "peer" member.
type submitPeerResult struct {
	Name         string  `json:"name"`
	PublicKey    string  `json:"public_key"`
	DaemonOnline bool    `json:"daemon_online"`
	LastSeen     *string `json:"last_seen"`
}

// RequestSubmitResult is the result of "request_submit"
// (Docs/protocol/request.md §Submit result).
type RequestSubmitResult struct {
	ID              string           `json:"id"`
	MailID          string           `json:"mail_id"`
	Status          string           `json:"status"`
	Duplicate       bool             `json:"duplicate"`
	Team            teamRefResult    `json:"team"`
	Urgency         string           `json:"urgency"`
	UrgencyDeclared string           `json:"urgency_declared,omitempty"`
	UrgencyNote     string           `json:"urgency_note,omitempty"`
	Peer            submitPeerResult `json:"peer"`
}

// newRequestStore builds the Store that owns both sides of the requests
// table: the receiving path (Kind, wired into the mail receiver's Kinds) and
// the sending path (Submit, called by request_submit below).
func newRequestStore(db *sql.DB, self string, ob *mail.Outbox, log *audit.Log, ts *team.Store, nonLoopbackRelay bool) *request.Store {
	return &request.Store{
		DB: db, Self: self, Outbox: ob, Audit: log,
		TeamActive: func(ctx context.Context, tx *sql.Tx, teamID string) (bool, error) {
			t, err := ts.GetTx(ctx, tx, teamID)
			if err != nil {
				if errors.Is(err, team.ErrNotFound) {
					return false, nil
				}
				return false, err
			}
			return t.State == team.StateActive, nil
		},
		TeamHasMembers: func(ctx context.Context, tx *sql.Tx, teamID, peer string) (bool, error) {
			members, err := ts.MembersTx(ctx, tx, teamID)
			if err != nil {
				return false, err
			}
			var self, other bool
			for _, m := range members {
				if m.Key == ts.Self {
					self = true
				}
				if m.Key == peer {
					other = true
				}
			}
			return self && other, nil
		},
		UnverifiedPeer: func(tx *sql.Tx, peer string) bool {
			return nonLoopbackRelay && trustOfTx(tx, peer) == peers.TrustRelay
		},
	}
}

func registerRequest(srv *ipc.Server, db *sql.DB, rs *request.Store, ps *peers.Store, ts *team.Store, log *audit.Log, nonLoopbackRelay bool) {
	srv.Handle("request_submit", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p RequestSubmitParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		if strings.TrimPrefix(p.To, "@") == "" || p.Type == "" || p.Title == "" || p.Brief == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "to, type, title and brief are required"}
		}
		if p.IdempotencyKey != "" && !request.ValidIdempotencyKey(p.IdempotencyKey) {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "idempotency_key must be 1-64 characters from [A-Za-z0-9._:-]"}
		}
		peer, err := resolvePeer(ctx, ps, p.To)
		if err != nil {
			return nil, err
		}
		if nonLoopbackRelay && peer.Trust == peers.TrustRelay {
			return nil, &ipc.Error{Code: CodeUnverifiedPeer, Message: "the peer's trust is \"relay\" on a non-loopback relay; re-pair or run 'agentnet peers verify'"}
		}
		t, err := resolveSharedTeam(ctx, ts, peer.PublicKey, p.Team)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		deadline, err := parseDeadline(p.Deadline, now)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "deadline: " + err.Error()}
		}
		artifacts := make([]request.Artifact, len(p.Artifacts))
		for i, a := range p.Artifacts {
			artifacts[i] = request.Artifact{URL: a.URL, Branch: a.Branch, Commit: a.Commit, Path: a.Path}
		}
		var grant *request.RequestedGrant
		if p.RequestedGrant != nil {
			grant = &request.RequestedGrant{Action: p.RequestedGrant.Action, Resource: p.RequestedGrant.Resource, Note: p.RequestedGrant.Note}
		}
		urgency := p.Urgency
		if urgency == "" {
			urgency = request.UrgencyNormal
		}
		sp := request.SubmitParams{
			From: ts.Self, To: peer.PublicKey, Team: t.ID, Type: p.Type, Title: p.Title, Brief: p.Brief,
			Urgency: urgency, UrgencyReason: p.UrgencyReason, Artifacts: artifacts, RequestedGrant: grant,
			Deadline: deadline, IdempotencyKey: p.IdempotencyKey,
		}
		if p.IdempotencyKey != "" {
			sp.ParamsHash = submitParamsHash(peer.PublicKey, t.ID, p)
		}
		outcome, err := rs.Submit(ctx, sp)
		if err != nil {
			return nil, submitError(err)
		}
		if log != nil && !outcome.Duplicate {
			detail := map[string]any{"request": outcome.Request.ID, "peer": peer.PublicKey, "team": t.ID, "type": outcome.Request.Type, "urgency": outcome.Request.Urgency, "mail": outcome.MailID}
			if aerr := log.Append(ctx, audit.ActorCLI, "request.submit", detail); aerr != nil {
				return nil, aerr
			}
		}
		online, lastSeen := presenceBrief(db, peer.PublicKey, now)
		return RequestSubmitResult{
			ID: outcome.Request.ID, MailID: outcome.MailID, Status: outcome.Status, Duplicate: outcome.Duplicate,
			Team: teamRefResult{ID: t.ID, Name: t.Name}, Urgency: outcome.Request.Urgency,
			Peer: submitPeerResult{Name: peer.Name, PublicKey: peer.PublicKey, DaemonOnline: online, LastSeen: lastSeen},
		}, nil
	})
}

// resolveSharedTeam is Docs/protocol/request.md §Submitting step 3.
func resolveSharedTeam(ctx context.Context, ts *team.Store, peer, teamRef string) (team.Team, error) {
	if teamRef != "" {
		t, err := resolveTeam(ctx, ts, teamRef)
		if err != nil {
			return team.Team{}, err
		}
		if err := requireActive(t); err != nil {
			return team.Team{}, err
		}
		members, err := ts.Members(ctx, t.ID)
		if err != nil {
			return team.Team{}, err
		}
		if !hasKey(members, ts.Self) || !hasKey(members, peer) {
			return team.Team{}, &ipc.Error{Code: CodeRequestNotTeamMember, Message: fmt.Sprintf("you and the peer are not both members of %s", t.ID)}
		}
		return t, nil
	}
	list, err := ts.List(ctx)
	if err != nil {
		return team.Team{}, err
	}
	var matches []team.Team
	for _, t := range list {
		if t.State != team.StateActive {
			continue
		}
		members, err := ts.Members(ctx, t.ID)
		if err != nil {
			return team.Team{}, err
		}
		if hasKey(members, ts.Self) && hasKey(members, peer) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return team.Team{}, &ipc.Error{Code: CodeNoSharedTeam, Message: "you share no active team with this peer"}
	default:
		ids := make([]string, len(matches))
		for i, t := range matches {
			ids[i] = t.ID
		}
		return team.Team{}, &ipc.Error{Code: CodeAmbiguousTeam, Message: fmt.Sprintf("you share several teams with this peer; pass --team: %v", ids)}
	}
}

func hasKey(members []team.Member, key string) bool {
	for _, m := range members {
		if m.Key == key {
			return true
		}
	}
	return false
}

func submitError(err error) error {
	var fe *request.FieldError
	var tl *request.TooLargeError
	switch {
	case errors.As(err, &fe):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: fe.Error()}
	case errors.As(err, &tl):
		return &ipc.Error{Code: CodeRequestTooLarge, Message: tl.Error()}
	case errors.Is(err, request.ErrIdempotencyConflict):
		return &ipc.Error{Code: CodeIdempotencyConflict, Message: "this idempotency_key was already used with different params"}
	case errors.Is(err, mail.ErrUnpaired):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is no longer paired"}
	case errors.Is(err, mail.ErrNoMailboxKey):
		return &ipc.Error{Code: CodeNoMailboxKey, Message: "the peer was paired with v1 and must re-pair"}
	default:
		return err
	}
}

// parseDeadline resolves a --deadline value: an RFC 3339 time, or a duration
// from now (Go time.ParseDuration, plus a "d" suffix meaning 24h), truncated
// to whole seconds (Docs/protocol/request.md §Submitting step 5).
func parseDeadline(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Truncate(time.Second), nil
	}
	var dur time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("must be an RFC 3339 time or a duration like 90m, 2h, 3d")
		}
		dur = time.Duration(n * float64(24*time.Hour))
	} else {
		d, err := time.ParseDuration(s)
		if err != nil {
			return time.Time{}, fmt.Errorf("must be an RFC 3339 time or a duration like 90m, 2h, 3d")
		}
		dur = d
	}
	return now.Add(dur).UTC().Truncate(time.Second), nil
}

// submitParamsHash is Docs/protocol/request.md §Submitting's params_hash:
// canonical JSON of the IPC params as given, with "to" and "team" resolved
// and without idempotency_key.
func submitParamsHash(to, teamID string, p RequestSubmitParams) string {
	m := map[string]any{"to": to, "type": p.Type, "team": teamID, "title": p.Title, "brief": p.Brief}
	if p.Urgency != "" {
		m["urgency"] = p.Urgency
	}
	if p.UrgencyReason != "" {
		m["urgency_reason"] = p.UrgencyReason
	}
	if len(p.Artifacts) > 0 {
		arr := make([]any, len(p.Artifacts))
		for i, a := range p.Artifacts {
			arr[i] = map[string]any{"url": a.URL, "branch": a.Branch, "commit": a.Commit, "path": a.Path}
		}
		m["artifacts"] = arr
	}
	if p.RequestedGrant != nil {
		m["requested_grant"] = map[string]any{"action": p.RequestedGrant.Action, "resource": p.RequestedGrant.Resource, "note": p.RequestedGrant.Note}
	}
	if p.Deadline != "" {
		m["deadline"] = p.Deadline
	}
	canon, err := agentcard.CanonicalValue(m)
	if err != nil {
		// m holds only strings, []any and map[string]any built above: this cannot fail.
		panic(err)
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// trustOfTx reads a peer's trust through tx instead of the connection pool:
// see mail.TxOutboxPeers for why this matters inside apply's transaction.
func trustOfTx(tx *sql.Tx, peer string) string {
	var t string
	if err := tx.QueryRow(`SELECT trust FROM peers WHERE public_key = ?`, peer).Scan(&t); err != nil {
		return ""
	}
	return t
}

// presenceBrief reads the presence brief of Docs/protocol/ipc.md for peer:
// daemon_online and last_seen. A peer never heard from gets false and nil.
func presenceBrief(db *sql.DB, peer string, now time.Time) (online bool, lastSeen *string) {
	var state, lastRx string
	var interval int
	if err := db.QueryRow(`SELECT state, interval, last_rx FROM presence_peers WHERE key = ?`, peer).
		Scan(&state, &interval, &lastRx); err != nil {
		return false, nil
	}
	t, err := time.Parse(mail.StoreTimeFmt, lastRx)
	if err != nil {
		return false, nil
	}
	online = state == "online" && now.Sub(t) <= time.Duration(interval)*5*time.Second/2
	s := t.UTC().Format("2006-01-02T15:04:05Z")
	return online, &s
}

// relayIsNonLoopback reports whether rawURL's host is not loopback
// (Docs/protocol/pairing.md §Storage and trust states, D5). An empty URL (no
// relay configured) is treated as loopback-safe.
func relayIsNonLoopback(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	host := u.Hostname()
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}
