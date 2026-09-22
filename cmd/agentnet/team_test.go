package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

type teamMemberOut struct {
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Added       string `json:"added"`
	Owner       bool   `json:"owner"`
	Self        bool   `json:"self"`
}

type teamSummaryOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Epoch   int64  `json:"epoch"`
	State   string `json:"state"`
	Role    string `json:"role"`
	Members int    `json:"members"`
}

type teamOut struct {
	OK    bool           `json:"ok"`
	Team  teamSummaryOut `json:"team"`
	Error *ipcErrOut     `json:"error"`
}

type teamShowOut struct {
	OK   bool `json:"ok"`
	Team struct {
		ID      string          `json:"id"`
		Name    string          `json:"name"`
		Owner   string          `json:"owner"`
		Epoch   int64           `json:"epoch"`
		State   string          `json:"state"`
		Role    string          `json:"role"`
		Members []teamMemberOut `json:"members"`
	} `json:"team"`
	Error *ipcErrOut `json:"error"`
}

type teamListOut struct {
	OK    bool             `json:"ok"`
	Teams []teamSummaryOut `json:"teams"`
	Error *ipcErrOut       `json:"error"`
}

type ipcErrOut struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeTeam(t *testing.T, out string) teamOut {
	t.Helper()
	var o teamOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out, err)
	}
	return o
}

func decodeTeamShow(t *testing.T, out string) teamShowOut {
	t.Helper()
	var o teamShowOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out, err)
	}
	return o
}

func decodeTeamList(t *testing.T, out string) teamListOut {
	t.Helper()
	var o teamListOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out, err)
	}
	return o
}

func decodeErr(t *testing.T, out string) ipcErrOut {
	t.Helper()
	var o struct {
		OK    bool      `json:"ok"`
		Error ipcErrOut `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out, err)
	}
	if o.OK {
		t.Fatalf("expected ok:false, got %q", out)
	}
	return o.Error
}

// teamDB opens a fresh connection to n's store; callers must close it.
func teamDB(t *testing.T, n *testNode) *sql.DB {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	return st.DB()
}

// insertTeam writes a teams row directly, as a roster from another owner
// would (Docs/protocol/team.md §Local names: "a roster can still introduce a
// duplicate" name).
func insertTeam(t *testing.T, n *testNode, id, name, owner string, epoch int64, state string) {
	t.Helper()
	db := teamDB(t, n)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := db.Exec(`INSERT INTO teams (id, name, owner, epoch, state, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, owner, epoch, state, now, now); err != nil {
		t.Fatal(err)
	}
}

// insertPeerAndMember stores a freshly generated, validly signed peer as an
// introduced team member (as a roster from n's own team would), and adds it
// to teamID's roster. It returns the member's public key.
func insertPeerAndMember(t *testing.T, n *testNode, teamID, name string, added time.Time) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	card, err := agentcard.New(pub, name, "test-harness", nil, added)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := agentcard.Sign(priv, card)
	if err != nil {
		t.Fatal(err)
	}
	cardJSON, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	key := envelope.KeyString(pub)
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ann, err := mail.SignAnnouncement(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, x.PublicKey().Bytes(), added)
	if err != nil {
		t.Fatal(err)
	}
	mailboxKeys, err := json.Marshal([]json.RawMessage{ann})
	if err != nil {
		t.Fatal(err)
	}
	db := teamDB(t, n)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys, introduced_by) VALUES (?, ?, 'test-harness', '[]', ?, ?, 'team', ?, ?)`,
		key, name, string(cardJSON), added.UTC().Format(time.RFC3339), string(mailboxKeys), n.key); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO team_members (team_id, key, added) VALUES (?, ?, ?)`,
		teamID, key, added.UTC().Format("2006-01-02T15:04:05Z")); err != nil {
		t.Fatal(err)
	}
	return key
}

func teamAuditActions(t *testing.T, n *testNode) []string {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if strings.HasPrefix(e.Action, "team.") {
			out = append(out, e.Action)
		}
	}
	return out
}

func TestTeamCreateListShowRenameDelete(t *testing.T) {
	n := startNode(t, "alice", "")

	// No teams yet.
	code, out, _ := cli(t, n, "team", "list")
	if code != exitOK || !strings.Contains(out, "No teams yet") {
		t.Fatalf("empty list: code %d out %q", code, out)
	}

	// Bad name.
	code, out, _ = cli(t, n, "team", "create", "Bad_Name", "--json")
	if code != exitError || decodeErr(t, out).Code != "bad_team_name" {
		t.Fatalf("bad name: code %d out %q", code, out)
	}

	// Create.
	code, out, _ = cli(t, n, "team", "create", "backend", "--json")
	if code != exitOK {
		t.Fatalf("create: code %d out %q", code, out)
	}
	created := decodeTeam(t, out)
	if !created.OK || created.Team.Name != "backend" || created.Team.Role != "owner" ||
		created.Team.State != "active" || created.Team.Epoch != 1 || created.Team.Members != 1 ||
		!strings.HasPrefix(created.Team.ID, "t-") {
		t.Fatalf("create result = %+v", created)
	}
	teamID := created.Team.ID

	// Human output.
	code, out, _ = cli(t, n, "team", "create", "web", "--json")
	if code != exitOK {
		t.Fatalf("create web: code %d out %q", code, out)
	}

	// Duplicate name.
	code, out, _ = cli(t, n, "team", "create", "backend", "--json")
	if code != exitError || decodeErr(t, out).Code != "team_exists" {
		t.Fatalf("dup name: code %d out %q", code, out)
	}

	// List --json.
	code, out, _ = cli(t, n, "team", "list", "--json")
	if code != exitOK {
		t.Fatalf("list --json: code %d out %q", code, out)
	}
	if l := decodeTeamList(t, out); !l.OK || len(l.Teams) != 2 {
		t.Fatalf("list --json = %+v", l)
	}

	// List human.
	code, out, _ = cli(t, n, "team", "list")
	if code != exitOK || !strings.Contains(out, "backend") || !strings.Contains(out, "owner") || !strings.Contains(out, teamID) {
		t.Fatalf("list human: code %d out %q", code, out)
	}

	// Show --json, by name.
	code, out, _ = cli(t, n, "team", "show", "backend", "--json")
	if code != exitOK {
		t.Fatalf("show: code %d out %q", code, out)
	}
	shown := decodeTeamShow(t, out)
	if !shown.OK || shown.Team.ID != teamID || len(shown.Team.Members) != 1 ||
		!shown.Team.Members[0].Self || !shown.Team.Members[0].Owner || shown.Team.Members[0].Name != "alice" {
		t.Fatalf("show result = %+v", shown)
	}

	// Show human, by id.
	code, out, _ = cli(t, n, "team", "show", teamID)
	if code != exitOK || !strings.Contains(out, "owner alice") || !strings.Contains(out, "epoch 1") || !strings.Contains(out, "alice") {
		t.Fatalf("show human: code %d out %q", code, out)
	}

	// Unknown team.
	code, out, _ = cli(t, n, "team", "show", "nope", "--json")
	if code != exitError || decodeErr(t, out).Code != "unknown_team" {
		t.Fatalf("unknown team: code %d out %q", code, out)
	}

	// owner_cannot_leave.
	code, out, _ = cli(t, n, "team", "leave", "backend", "--json")
	if code != exitError || decodeErr(t, out).Code != "owner_cannot_leave" {
		t.Fatalf("owner leave: code %d out %q", code, out)
	}

	// Rename.
	code, out, _ = cli(t, n, "team", "rename", "backend", "backend2", "--json")
	if code != exitOK {
		t.Fatalf("rename: code %d out %q", code, out)
	}
	renamed := decodeTeam(t, out)
	if renamed.Team.Name != "backend2" || renamed.Team.Epoch != 2 {
		t.Fatalf("rename result = %+v", renamed)
	}

	// Rename, bad new name.
	code, out, _ = cli(t, n, "team", "rename", "backend2", "Bad!", "--json")
	if code != exitError || decodeErr(t, out).Code != "bad_team_name" {
		t.Fatalf("rename bad name: code %d out %q", code, out)
	}

	// Delete.
	code, out, _ = cli(t, n, "team", "delete", "backend2", "--json")
	if code != exitOK {
		t.Fatalf("delete: code %d out %q", code, out)
	}
	deleted := decodeTeam(t, out)
	if deleted.Team.State != "dissolved" || deleted.Team.Epoch != 3 {
		t.Fatalf("delete result = %+v", deleted)
	}

	// Dissolved team is hidden from list unless --all.
	code, out, _ = cli(t, n, "team", "list", "--json")
	if code != exitOK {
		t.Fatalf("list after delete: code %d out %q", code, out)
	}
	if l := decodeTeamList(t, out); len(l.Teams) != 1 { // only "web" remains active
		t.Fatalf("list after delete = %+v", l)
	}
	code, out, _ = cli(t, n, "team", "list", "--all", "--json")
	if code != exitOK {
		t.Fatalf("list --all: code %d out %q", code, out)
	}
	if l := decodeTeamList(t, out); len(l.Teams) != 2 {
		t.Fatalf("list --all = %+v", l)
	}

	// Mutating a dissolved team is rejected (by id, since a dissolved team no
	// longer resolves by name).
	code, out, _ = cli(t, n, "team", "rename", teamID, "backend3", "--json")
	if code != exitError || decodeErr(t, out).Code != "team_inactive" {
		t.Fatalf("rename dissolved: code %d out %q", code, out)
	}

	acts := teamAuditActions(t, n)
	want := "team.create,team.create,team.rename,team.delete"
	if strings.Join(acts, ",") != want {
		t.Errorf("audit = %v, want %s", acts, want)
	}
}

func TestTeamUsageErrors(t *testing.T) {
	n := startNode(t, "alice", "")
	if code, out, _ := cli(t, n, "team"); code != exitOK || !strings.Contains(out, "Usage:") {
		t.Errorf("bare team: code %d out %q, want usage text on stdout with exit 0", code, out)
	}
	cases := [][]string{
		{"team", "bogus"},
		{"team", "create"},
		{"team", "create", "a", "b"},
		{"team", "list", "extra"},
		{"team", "show"},
		{"team", "show", "a", "b"},
		{"team", "remove"},
		{"team", "remove", "a"},
		{"team", "rename"},
		{"team", "rename", "a"},
		{"team", "leave"},
		{"team", "delete"},
	}
	for _, args := range cases {
		if code, _, _ := cli(t, n, args...); code != exitUsage {
			t.Errorf("%v: code %d, want usage", args, code)
		}
	}
	for _, sub := range []string{"create", "list", "show", "remove", "rename", "leave", "delete"} {
		var o, eb strings.Builder
		if c := run([]string{"team", sub, "--help"}, &o, &eb); c != exitOK || !strings.Contains(o.String(), "Exit codes") {
			t.Errorf("team %s --help: code %d out %q", sub, c, o.String())
		}
	}
}

func TestTeamAmbiguousAndNotOwner(t *testing.T) {
	n := startNode(t, "alice", "")

	code, out, _ := cli(t, n, "team", "create", "shared", "--json")
	if code != exitOK {
		t.Fatalf("create: code %d out %q", code, out)
	}
	id1 := decodeTeam(t, out).Team.ID

	// A second, same-named team owned by someone else, as if introduced by a
	// roster (Docs/protocol/team.md §Local names).
	otherOwner := envelope.KeyString(mustKey(t))
	id2 := "t-11111111111111111111111111111111"[:34]
	insertTeam(t, n, id2, "shared", otherOwner, 1, "active")

	code, out, _ = cli(t, n, "team", "show", "shared", "--json")
	if code != exitError {
		t.Fatalf("ambiguous show: code %d out %q", code, out)
	}
	if e := decodeErr(t, out); e.Code != "ambiguous_team" {
		t.Fatalf("ambiguous show error = %+v", e)
	}

	// Resolving by id works even while the name is ambiguous.
	code, out, _ = cli(t, n, "team", "show", id1, "--json")
	if code != exitOK || decodeTeamShow(t, out).Team.ID != id1 {
		t.Fatalf("show id1: code %d out %q", code, out)
	}
	code, out, _ = cli(t, n, "team", "show", id2, "--json")
	if code != exitOK {
		t.Fatalf("show id2: code %d out %q", code, out)
	}
	if s := decodeTeamShow(t, out); s.Team.Role != "member" {
		t.Fatalf("show id2 role = %+v, want member", s)
	}

	// not_owner: alice does not own id2.
	code, out, _ = cli(t, n, "team", "rename", id2, "newname", "--json")
	if code != exitError || decodeErr(t, out).Code != "not_owner" {
		t.Fatalf("rename not owner: code %d out %q", code, out)
	}
	code, out, _ = cli(t, n, "team", "delete", id2, "--json")
	if code != exitError || decodeErr(t, out).Code != "not_owner" {
		t.Fatalf("delete not owner: code %d out %q", code, out)
	}
	code, out, _ = cli(t, n, "team", "remove", id2, n.key, "--json")
	if code != exitError || decodeErr(t, out).Code != "not_owner" {
		t.Fatalf("remove not owner: code %d out %q", code, out)
	}
}

func TestTeamRemoveMemberAndAudit(t *testing.T) {
	n := startNode(t, "alice", "")
	code, out, _ := cli(t, n, "team", "create", "backend", "--json")
	if code != exitOK {
		t.Fatalf("create: code %d out %q", code, out)
	}
	teamID := decodeTeam(t, out).Team.ID
	bobKey := insertPeerAndMember(t, n, teamID, "bob", time.Now())

	code, out, _ = cli(t, n, "team", "show", "backend", "--json")
	if code != exitOK || len(decodeTeamShow(t, out).Team.Members) != 2 {
		t.Fatalf("show with bob: code %d out %q", code, out)
	}

	// The owner cannot remove itself this way.
	code, out, _ = cli(t, n, "team", "remove", "backend", n.key, "--json")
	if code != exitError || decodeErr(t, out).Code != "bad_request" {
		t.Fatalf("remove self: code %d out %q", code, out)
	}

	// Unknown peer.
	code, out, _ = cli(t, n, "team", "remove", "backend", "nonexistent", "--json")
	if code != exitError || decodeErr(t, out).Code != "unknown_peer" {
		t.Fatalf("remove unknown peer: code %d out %q", code, out)
	}

	// Remove bob.
	code, out, _ = cli(t, n, "team", "remove", "backend", bobKey, "--json")
	if code != exitOK {
		t.Fatalf("remove bob: code %d out %q", code, out)
	}
	removed := decodeTeam(t, out)
	if removed.Team.Members != 1 || removed.Team.Epoch != 2 {
		t.Fatalf("remove result = %+v", removed)
	}

	// Human output on error goes to stderr, prefixed "agentnet:".
	code, _, errs := cli(t, n, "team", "remove", "backend", "nonexistent")
	if code != exitError || !strings.Contains(errs, "agentnet:") || !strings.Contains(errs, "nonexistent") {
		t.Fatalf("remove human error: code %d err %q", code, errs)
	}

	acts := teamAuditActions(t, n)
	if !contains(acts, "team.member_remove") {
		t.Fatalf("audit = %v, want team.member_remove", acts)
	}
}

// TestPeersRemoveCascadesTeamMembership covers Docs/cli/peers.md §peers
// remove: removing a peer that is a member (not owner) of a team you own
// removes it from that team first, bumping the epoch and broadcasting the
// roster, before the peer itself is deleted.
func TestPeersRemoveCascadesTeamMembership(t *testing.T) {
	n := startNode(t, "alice", "")
	code, out, _ := cli(t, n, "team", "create", "backend", "--json")
	if code != exitOK {
		t.Fatalf("create: code %d out %q", code, out)
	}
	teamID := decodeTeam(t, out).Team.ID
	bobKey := insertPeerAndMember(t, n, teamID, "bob", time.Now())

	code, out, _ = cli(t, n, "team", "show", "backend", "--json")
	if code != exitOK || len(decodeTeamShow(t, out).Team.Members) != 2 {
		t.Fatalf("show with bob: code %d out %q", code, out)
	}

	code, out, _ = cli(t, n, "peers", "remove", bobKey, "--json")
	if code != exitOK {
		t.Fatalf("peers remove: code %d out %q", code, out)
	}

	code, out, _ = cli(t, n, "team", "show", "backend", "--json")
	if code != exitOK {
		t.Fatalf("show after remove: code %d out %q", code, out)
	}
	shown := decodeTeamShow(t, out)
	if len(shown.Team.Members) != 1 || shown.Team.Epoch != 2 {
		t.Fatalf("show after peers remove = %+v, want 1 member, epoch 2", shown)
	}

	if got := peerKeys(t, n); len(got) != 0 {
		t.Fatalf("peers after remove = %v, want none", got)
	}

	acts := teamAuditActions(t, n)
	if !contains(acts, "team.member_remove") {
		t.Fatalf("audit = %v, want team.member_remove", acts)
	}
}

func mustKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
