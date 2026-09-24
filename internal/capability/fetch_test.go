package capability

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

const testSession = "s-36375782ceb6baea9cee4d4273dfb035"

var fetchT0 = time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)

type fetchHarness struct {
	t       *testing.T
	root    string
	store   *Store
	srv     *FetchServer
	issPriv ed25519.PrivateKey
	iss     string
	holder  string
	other   string
	clock   atomic.Int64
	open    atomic.Bool
	out     chan []byte

	mu     sync.Mutex
	audits []auditRow
}

type auditRow struct {
	Action string
	Detail map[string]any
}

func newFetchHarness(t *testing.T, be Backend, tweak func(*FetchConfig)) *fetchHarness {
	t.Helper()
	h := &fetchHarness{t: t, root: testutil.TempDir(t), out: make(chan []byte, 4096)}
	h.issPriv = seed(0)
	h.iss = base64ify(t, h.issPriv.Public().(ed25519.PublicKey))
	h.holder = base64ify(t, seed(0x20).Public().(ed25519.PublicKey))
	h.other = base64ify(t, seed(0x40).Public().(ed25519.PublicKey))
	h.clock.Store(fetchT0.UnixNano())
	h.open.Store(true)
	h.store = &Store{DB: openTestDB(t)}
	if be == nil {
		be = FSBackend{}
	}
	cfg := FetchConfig{
		Store: h.store, Self: h.iss,
		SessionOpen: func(_ context.Context, id, requester, worker string) (bool, bool) {
			if id != testSession || requester != h.iss || worker != h.holder {
				return false, false
			}
			return true, h.open.Load()
		},
		Send: func(_ context.Context, _ string, pt []byte) error {
			if len(pt) > 65535 {
				t.Errorf("fetch.resp of %d bytes is over the Noise plaintext limit", len(pt))
			}
			h.out <- bytes.Clone(pt)
			return nil
		},
		Backends: map[string]Backend{KindFS: be},
		Audit: func(_ context.Context, action string, detail map[string]any) {
			h.mu.Lock()
			h.audits = append(h.audits, auditRow{action, detail})
			h.mu.Unlock()
		},
		Now:           func() time.Time { return time.Unix(0, h.clock.Load()).UTC() },
		FlushInterval: time.Hour,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	h.srv = NewFetchServer(cfg)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fetchHarness) advance(d time.Duration) { h.clock.Add(int64(d)) }

func (h *fetchHarness) auditsOf(action string) []auditRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []auditRow
	for _, a := range h.audits {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

// issue signs and stores an active grant for the holder over h.root.
func (h *fetchHarness) issue(scope string) (Record, json.RawMessage) {
	h.t.Helper()
	now := time.Unix(0, h.clock.Load()).UTC()
	g := Grant{
		V: 1, ID: NewID(), Iss: h.iss, Aud: h.holder, Session: testSession, Action: ActionFSRead,
		Resource: Resource{Kind: KindFS, Label: "res-ab12"}, Scope: scope,
		Nbf: now.Add(-time.Hour), Exp: now.Add(72 * time.Hour), Sensitive: true,
	}
	tok, err := Sign(h.issPriv, g)
	if err != nil {
		h.t.Fatal(err)
	}
	wire, err := Canonical(tok)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := Record{
		ID: g.ID, Direction: DirectionIssued, Peer: h.holder, Session: testSession, Action: ActionFSRead,
		Label: "res-ab12", Path: h.root, Scope: scope, Sensitive: true, Nbf: g.Nbf, Exp: g.Exp, Token: string(wire),
	}
	tx, err := h.store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.InsertActiveTx(context.Background(), tx, rec); err != nil {
		h.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatal(err)
	}
	return rec, wire
}

func (h *fetchHarness) revoke(id string) {
	h.t.Helper()
	tx, err := h.store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.store.RevokeTx(context.Background(), tx, id, ReasonUser, time.Unix(0, h.clock.Load()).UTC()); err != nil {
		h.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatal(err)
	}
}

func newReqID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "f-" + hex.EncodeToString(b)
}

type reqOpts struct {
	peer   string
	req    string
	ts     time.Time
	op     string
	path   any // string or nil (absent)
	offset any
	length any
	cursor string
}

func (h *fetchHarness) send(tok json.RawMessage, o reqOpts) string {
	h.t.Helper()
	if o.peer == "" {
		o.peer = h.holder
	}
	if o.req == "" {
		o.req = newReqID()
	}
	if o.ts.IsZero() {
		o.ts = time.Unix(0, h.clock.Load()).UTC()
	}
	m := map[string]any{"type": TypeFetchReq, "req": o.req, "ts": o.ts.Format(time.RFC3339), "token": tok, "op": o.op}
	if o.path != nil {
		m["path"] = o.path
	}
	if o.offset != nil {
		m["offset"] = o.offset
	}
	if o.length != nil {
		m["length"] = o.length
	}
	if o.cursor != "" {
		m["cursor"] = o.cursor
	}
	pt, err := json.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	h.srv.Handle(o.peer, pt)
	return o.req
}

func (h *fetchHarness) next() map[string]any {
	h.t.Helper()
	select {
	case pt := <-h.out:
		var m map[string]any
		if err := json.Unmarshal(pt, &m); err != nil {
			h.t.Fatal(err)
		}
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for a fetch.resp")
		return nil
	}
}

// call sends a request and gathers its response messages (all fragments of a
// read) until the final one.
func (h *fetchHarness) call(tok json.RawMessage, o reqOpts) []map[string]any {
	h.t.Helper()
	req := h.send(tok, o)
	var got []map[string]any
	for {
		m := h.next()
		if m["req"] != req {
			h.t.Fatalf("response for %v, want %s", m["req"], req)
		}
		got = append(got, m)
		if ok, _ := m["ok"].(bool); !ok {
			return got
		}
		if m["data"] == nil || int(m["frag"].(float64)) == int(m["frags"].(float64))-1 {
			return got
		}
	}
}

func errOf(resp []map[string]any) string {
	last := resp[len(resp)-1]
	if ok, _ := last["ok"].(bool); ok {
		return ""
	}
	s, _ := last["error"].(string)
	return s
}

func (h *fetchHarness) expectErr(tok json.RawMessage, o reqOpts, want string) {
	h.t.Helper()
	if got := errOf(h.call(tok, o)); got != want {
		h.t.Fatalf("%s %v: error = %q, want %q", o.op, o.path, got, want)
	}
}

func readAll(resp []map[string]any) []byte {
	var out []byte
	for _, m := range resp {
		b, err := base64.StdEncoding.DecodeString(m["data"].(string))
		if err != nil {
			panic(err)
		}
		out = append(out, b...)
	}
	return out
}

func readOpts(path string) reqOpts {
	return reqOpts{op: OpRead, path: path, offset: 0, length: MaxReadBytes}
}

func (h *fetchHarness) write(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// ---- path grammar ------------------------------------------------------

func TestPathGrammar(t *testing.T) {
	ok := []string{"", "a", "a/b", "internal/mail/mail.go", "a.b", ".hidden", "a b", "CONX", "conx.y2", "COM10", "LPT0", "héllo", strings.Repeat("a", 1024)}
	bad := []string{
		"/a", "a/", "a//b", "..", "a/../b", ".", "a/./b", `a\b`, `\a`, "C:", "C:/x", "a:b", "a\x00b", "a\nb", "a\x7fb",
		"CON", "con", "CON.txt", "a/NUL", "PRN.x", "AUX", "COM1", "com9.txt", "LPT1", "lpt5.log", "CON .txt",
		"a.", "a/b.", "a ", "a/b ",
		"CONIN$", "CONOUT$", "conout$.txt", "COM\u00b9", "LPT\u00b2.txt", "com\u00b3",
		"a\u0085b", "a\u009fb", "a\u202eb", "a\u2066b", "a\u200fb", "a\u061cb",
		"\xff", strings.Repeat("a", 1025),
	}
	for _, s := range ok {
		if !ValidScopePath(s) {
			t.Errorf("ValidScopePath(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidScopePath(s) {
			t.Errorf("ValidScopePath(%q) = true, want false", s)
		}
	}
}

func TestGitNameAndShortName(t *testing.T) {
	for _, s := range []string{".git", ".GIT", ".Git", ".gIt", ".g\u200bit", ".\u200dgit", ".g\u0131t", ".G\u0131T"} {
		if !isGitName(s) {
			t.Errorf("isGitName(%q) = false", s)
		}
	}
	for _, s := range []string{".gitignore", "git", ".git2", "a.git", ".gi"} {
		if isGitName(s) {
			t.Errorf("isGitName(%q) = true", s)
		}
	}
	for _, s := range []string{"GIT~1", "git~1", "a~2b", "PROGRA~1"} {
		if !isShortNameShape(s) {
			t.Errorf("isShortNameShape(%q) = false", s)
		}
	}
	for _, s := range []string{"a~b", "~", "a~", "abc"} {
		if isShortNameShape(s) {
			t.Errorf("isShortNameShape(%q) = true", s)
		}
	}
}

// ---- serving -----------------------------------------------------------

func TestFetchStatListRead(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "hello")
	h.write("sub/b.txt", "bee")
	h.write("sub/c.txt", "sea")
	if err := os.MkdirAll(filepath.Join(h.root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	h.write(".git/config", "secret")
	_, tok := h.issue("")

	st := h.call(tok, reqOpts{op: OpStat, path: "a.txt"})
	e := st[0]["entry"].(map[string]any)
	if st[0]["ok"] != true || e["type"] != "file" || e["size"].(float64) != 5 || e["name"] != "a.txt" {
		t.Fatalf("stat = %v", st[0])
	}
	if got := h.call(tok, reqOpts{op: OpStat, path: "sub"})[0]["entry"].(map[string]any); got["type"] != "dir" || got["size"] != nil {
		t.Fatalf("stat dir = %v", got)
	}
	// list hides .git
	l := h.call(tok, reqOpts{op: OpList, path: ""})[0]
	var names []string
	for _, x := range l["entries"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "a.txt,sub" {
		t.Fatalf("list names = %v", names)
	}
	l = h.call(tok, reqOpts{op: OpList, path: "sub"})[0]
	if len(l["entries"].([]any)) != 2 {
		t.Fatalf("list sub = %v", l)
	}
	// read
	if got := readAll(h.call(tok, readOpts("a.txt"))); string(got) != "hello" {
		t.Fatalf("read = %q", got)
	}
	// offset past the end: one empty fragment
	r := h.call(tok, reqOpts{op: OpRead, path: "a.txt", offset: 99, length: 10})
	if len(r) != 1 || r[0]["frags"].(float64) != 1 || r[0]["data"] != "" || r[0]["size"].(float64) != 5 {
		t.Fatalf("read past end = %v", r)
	}
	// missing file
	h.expectErr(tok, readOpts("nope.txt"), CodeNotFound)
	// a directory is not readable
	h.expectErr(tok, readOpts("sub"), CodeNotRegular)
	// a file is not listable
	h.expectErr(tok, reqOpts{op: OpList, path: "a.txt"}, CodeBadPath)
	// one audit row per op, no path anywhere
	rows := h.auditsOf("grant.fetch")
	if len(rows) == 0 {
		t.Fatal("no grant.fetch audit rows")
	}
	for _, a := range rows {
		b, _ := json.Marshal(a.Detail)
		for _, bad := range []string{"a.txt", "sub", h.root} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("audit row %s contains %q", b, bad)
			}
		}
	}
}

func TestFetchFragmentsAndListPaging(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	data := make([]byte, 3*FragmentBytes+100)
	_, _ = rand.Read(data)
	h.write("big.bin", string(data))
	for i := 0; i < 2500; i++ {
		h.write(fmt.Sprintf("many/f%05d", i), "")
	}
	_, tok := h.issue("")
	resp := h.call(tok, readOpts("big.bin"))
	if len(resp) != 4 || !bytes.Equal(readAll(resp), data) {
		t.Fatalf("fragments = %d, equal = %v", len(resp), bytes.Equal(readAll(resp), data))
	}
	for i, m := range resp {
		if int(m["frag"].(float64)) != i || m["frags"].(float64) != 4 || m["size"].(float64) != float64(len(data)) {
			t.Fatalf("fragment %d = %v", i, m)
		}
	}
	// a window inside the file
	resp = h.call(tok, reqOpts{op: OpRead, path: "big.bin", offset: 10, length: 20})
	if !bytes.Equal(readAll(resp), data[10:30]) {
		t.Fatal("windowed read differs")
	}
	// list paging: sorted by name, all 2500 exactly once
	seen := 0
	prev := ""
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		l := h.call(tok, reqOpts{op: OpList, path: "many", cursor: cursor})[0]
		es := l["entries"].([]any)
		if len(es) > MaxListEntries {
			t.Fatalf("page has %d entries", len(es))
		}
		for _, x := range es {
			n := x.(map[string]any)["name"].(string)
			if n <= prev {
				t.Fatalf("not sorted: %q after %q", n, prev)
			}
			prev = n
			seen++
		}
		c, _ := l["cursor"].(string)
		if c == "" {
			break
		}
		cursor = c
	}
	if seen != 2500 {
		t.Fatalf("listed %d entries, want 2500", seen)
	}
}

func TestFetchTooLarge(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	p := filepath.Join(h.root, "big")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	h.write("ok", "x")
	if err := os.Truncate(filepath.Join(h.root, "ok"), MaxFileBytes); err != nil {
		t.Fatal(err)
	}
	_, tok := h.issue("")
	h.expectErr(tok, readOpts("big"), CodeTooLarge)
	if e := errOf(h.call(tok, reqOpts{op: OpRead, path: "ok", offset: 0, length: 10})); e != "" {
		t.Fatalf("exactly 8 MiB: %q", e)
	}
}

func TestFetchBadPaths(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	for _, p := range []string{"..", "a//b", `a\b`, "C:", "CON.txt", "a.", "a\x00b", "/a", "a/", "./a", "COM\u00b9", "CONIN$"} {
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeBadPath)
	}
	// "" is only for list
	h.expectErr(tok, reqOpts{op: OpStat, path: ""}, CodeBadPath)
	h.expectErr(tok, readOpts(""), CodeBadPath)
}

func TestFetchGitDirNeverServed(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write(".git/config", "secret")
	h.write(".git/HEAD", "ref")
	h.write("sub/.git/x", "nested")
	_, tok := h.issue("")
	for _, p := range []string{".git/config", ".GIT/config", ".Git/HEAD", ".gIt/HEAD", "sub/.GIT/x", ".git", ".GIT", "sub/.git/x"} {
		h.expectErr(tok, readOpts(p), CodeOutOfScope)
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeOutOfScope)
	}
	h.expectErr(tok, reqOpts{op: OpList, path: ".git"}, CodeOutOfScope)
	// a scope inside .git does not help either
	_, tok2 := h.issue(".git")
	h.expectErr(tok2, readOpts("config"), CodeOutOfScope)
}

func TestFetchScopeBySegments(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("internal/mail/x.go", "in mail")
	h.write("internal/mailbox/x.go", "in mailbox")
	_, tok := h.issue("internal/mail")
	if got := readAll(h.call(tok, readOpts("x.go"))); string(got) != "in mail" {
		t.Fatalf("read = %q", got)
	}
	// the only ways to name the sibling are refused by the grammar
	h.expectErr(tok, readOpts("../mailbox/x.go"), CodeBadPath)
	h.expectErr(tok, readOpts("box/x.go"), CodeNotFound)
	// listing "" = the scope root
	l := h.call(tok, reqOpts{op: OpList, path: ""})[0]
	es := l["entries"].([]any)
	if len(es) != 1 || es[0].(map[string]any)["name"] != "x.go" {
		t.Fatalf("scope root list = %v", l)
	}
}

func TestFetchSymlinkEscapes(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	outside := testutil.TempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.write("inside/real.txt", "inside")
	// Directory links are symlinks, or junctions where symlinks need a privilege.
	linkDir(t, outside, filepath.Join(h.root, "out-dir"))
	linkDir(t, filepath.Join(h.root, "inside"), filepath.Join(h.root, "in-dir"))
	files := linkFile(t, filepath.Join(outside, "secret"), filepath.Join(h.root, "out-file")) &&
		linkFile(t, filepath.Join(h.root, "inside", "real.txt"), filepath.Join(h.root, "in-file"))
	_, tok := h.issue("")
	paths := []string{"out-dir/secret", "in-dir/real.txt"}
	if files {
		paths = append(paths, "out-file", "in-file")
	}
	for _, p := range paths {
		h.expectErr(tok, readOpts(p), CodeSymlink)
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeSymlink)
	}
	h.expectErr(tok, reqOpts{op: OpList, path: "out-dir"}, CodeSymlink)
	// list shows links as symlink (a junction is reported as other)
	types := map[string]string{}
	for _, x := range h.call(tok, reqOpts{op: OpList, path: ""})[0]["entries"].([]any) {
		m := x.(map[string]any)
		types[m["name"].(string)] = m["type"].(string)
	}
	if (types["out-dir"] != "symlink" && types["out-dir"] != "other") || types["inside"] != "dir" {
		t.Fatalf("list types = %v", types)
	}
	if files {
		if types["out-file"] != "symlink" {
			t.Fatalf("list types = %v", types)
		}
		// stat reports the link itself and never follows it
		e := h.call(tok, reqOpts{op: OpStat, path: "out-file"})[0]["entry"].(map[string]any)
		if e["type"] != "symlink" {
			t.Fatalf("stat symlink = %v", e)
		}
	}
}

func TestFetchDirSwappedForSymlinkBetweenCalls(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	outside := testutil.TempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.write("d/f.txt", "inside")
	_, tok := h.issue("")
	if got := readAll(h.call(tok, readOpts("d/f.txt"))); string(got) != "inside" {
		t.Fatalf("first read = %q", got)
	}
	if err := os.RemoveAll(filepath.Join(h.root, "d")); err != nil {
		t.Fatal(err)
	}
	linkDir(t, outside, filepath.Join(h.root, "d"))
	h.expectErr(tok, readOpts("d/f.txt"), CodeSymlink)
}

func TestFetchNonRegularAndIrregular(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("dir/x", "x")
	_, tok := h.issue("")
	h.expectErr(tok, readOpts("dir"), CodeNotRegular)
	makeSpecialFiles(t, h.root) // FIFO where the OS has them, a junction on Windows
	specialFileExpectations(t, h, tok)
}

// ---- token, time, limits -----------------------------------------------

func TestFetchTokenChecks(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	rec, tok := h.issue("")
	// a fetch whose Noise identity differs from aud
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", peer: h.other}, ReasonWrongAudience)
	// unknown grant: a token that was never stored
	other := h.freshUnstoredToken()
	h.expectErr(other, reqOpts{op: OpStat, path: "a.txt"}, CodeUnknownGrnt)
	// session not open
	h.open.Store(false)
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, ReasonSessionNotOpen)
	h.open.Store(true)
	// expired
	h.advance(73 * time.Hour)
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, ReasonExpired)
	h.advance(-73 * time.Hour)
	// widened caveat: same sig, later exp
	var top map[string]any
	_ = json.Unmarshal(tok, &top)
	top["grant"].(map[string]any)["exp"] = "2026-01-06T04:00:00Z"
	widened, _ := json.Marshal(top)
	h.expectErr(widened, reqOpts{op: OpStat, path: "a.txt"}, ReasonBadSignature)
	// revoked
	h.revoke(rec.ID)
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, CodeRevoked)
	// malformed token
	h.expectErr(json.RawMessage(`{"grant":1}`), reqOpts{op: OpStat, path: "a.txt"}, ReasonMalformed)
}

func (h *fetchHarness) freshUnstoredToken() json.RawMessage {
	h.t.Helper()
	now := time.Unix(0, h.clock.Load()).UTC()
	tok, err := Sign(h.issPriv, Grant{
		V: 1, ID: NewID(), Iss: h.iss, Aud: h.holder, Session: testSession, Action: ActionFSRead,
		Resource: Resource{Kind: KindFS, Label: "res-ab12"}, Nbf: now.Add(-time.Hour), Exp: now.Add(time.Hour), Sensitive: true,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	wire, _ := Canonical(tok)
	return wire
}

func TestFetchHeldRowIsNotServed(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	rec, tok := h.issue("")
	// The same id as a held row (this daemon is the holder of it): not served.
	if _, err := h.store.DB.Exec(`UPDATE grants SET direction = 'held' WHERE id = ?`, rec.ID); err != nil {
		t.Fatal(err)
	}
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, CodeUnknownGrnt)
}

func TestFetchTimestampAndReplay(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	now := time.Unix(0, h.clock.Load()).UTC()
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", ts: now.Add(-31 * time.Second)}, CodeStale)
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", ts: now.Add(11 * time.Minute)}, CodeStale)
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt", ts: now.Add(-29 * time.Second)})); e != "" {
		t.Fatalf("29 s old: %q", e)
	}
	// a repeated req 10 minutes later, with a fresh ts, is stale
	req := newReqID()
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt", req: req})); e != "" {
		t.Fatal(e)
	}
	h.advance(10 * time.Minute)
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", req: req}, CodeStale)
	// after the whole window it would be accepted again (dedupe is bounded)
	h.advance(2 * time.Minute)
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt", req: req})); e != "" {
		t.Fatalf("after 12 min: %q", e)
	}
	// the same req from another peer is a different request
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", req: req, peer: h.other}, ReasonWrongAudience)
}

func TestFetchMalformedRequests(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	for name, o := range map[string]reqOpts{
		"length too big": {op: OpRead, path: "a.txt", offset: 0, length: 262145},
		"length zero":    {op: OpRead, path: "a.txt", offset: 0, length: 0},
		"negative off":   {op: OpRead, path: "a.txt", offset: -1, length: 1},
		"no length":      {op: OpRead, path: "a.txt", offset: 0},
		"no offset":      {op: OpRead, path: "a.txt", length: 1},
		"no path":        {op: OpStat},
		"unknown op":     {op: "delete", path: "a.txt"},
	} {
		if got := errOf(h.call(tok, o)); got != ReasonMalformed {
			t.Errorf("%s: error = %q, want malformed", name, got)
		}
	}
	// a request without a valid req id gets no answer at all
	h.srv.Handle(h.holder, []byte(`{"type":"fetch.req","req":"nope","ts":"x","token":{},"op":"stat","path":"a"}`))
	h.srv.Handle(h.holder, []byte(`not json`))
	// an unparsable ts is malformed
	pt := fmt.Sprintf(`{"type":"fetch.req","req":%q,"ts":"yesterday","token":%s,"op":"stat","path":"a.txt"}`, newReqID(), tok)
	h.srv.Handle(h.holder, []byte(pt))
	if m := h.next(); m["error"] != ReasonMalformed {
		t.Fatalf("bad ts: %v", m)
	}
	select {
	case m := <-h.out:
		t.Fatalf("unexpected response %v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

// blockBackend blocks reads until released.
type blockBackend struct {
	FSBackend
	started chan struct{}
	release chan struct{}
}

func (b *blockBackend) Read(ctx context.Context, rec Record, rel string, off int64, n int, emit func(Fragment) error) error {
	b.started <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return b.FSBackend.Read(ctx, rec, rel, off, n, emit)
}

func TestFetchInflightLimitsAndOffSessionGoroutine(t *testing.T) {
	be := &blockBackend{started: make(chan struct{}, 8), release: make(chan struct{})}
	h := newFetchHarness(t, be, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	// Handle returns at once although the reads block: it never runs them.
	begin := time.Now()
	r1 := h.send(tok, readOpts("a.txt"))
	r2 := h.send(tok, readOpts("a.txt"))
	r3 := h.send(tok, readOpts("a.txt")) // the 3rd in flight for this holder
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Handle blocked for %v", d)
	}
	m := h.next()
	if m["req"] != r3 || m["error"] != CodeRateLimited {
		t.Fatalf("3rd in flight = %v, want rate_limited for %s", m, r3)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-be.started:
		case <-time.After(5 * time.Second):
			t.Fatal("reads did not start")
		}
	}
	// while two reads are blocked, another peer's request is still answered
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", peer: h.other}, ReasonWrongAudience)
	close(be.release)
	got := map[string]bool{}
	for len(got) < 2 {
		m := h.next()
		if m["ok"] != true {
			t.Fatalf("read failed: %v", m)
		}
		got[m["req"].(string)] = true
	}
	if !got[r1] || !got[r2] {
		t.Fatalf("answers = %v", got)
	}
	// slots are released: a new read goes through
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt"})); e != "" {
		t.Fatalf("after release: %q", e)
	}
}

func TestFetchInflightAcrossGrantsPerHolder(t *testing.T) {
	be := &blockBackend{started: make(chan struct{}, 8), release: make(chan struct{})}
	h := newFetchHarness(t, be, nil)
	h.write("a.txt", "x")
	_, t1 := h.issue("")
	_, t2 := h.issue("")
	h.send(t1, readOpts("a.txt"))
	h.send(t2, readOpts("a.txt"))
	req := h.send(t1, readOpts("a.txt"))
	if m := h.next(); m["req"] != req || m["error"] != CodeRateLimited {
		t.Fatalf("3rd op of the holder = %v", m)
	}
	close(be.release)
}

func TestFetchRatePerSecond(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	for i := 0; i < maxOpsPerSecond; i++ {
		if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt"})); e != "" {
			t.Fatalf("op %d: %q", i+1, e)
		}
	}
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, CodeRateLimited) // the 21st in the same second
	h.advance(time.Second)
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt"})); e != "" {
		t.Fatalf("next second: %q", e)
	}
}

func TestFetchBytesPer24h(t *testing.T) {
	h := newFetchHarness(t, nil, func(c *FetchConfig) { c.BytesPer24h = 100 })
	h.write("a.txt", strings.Repeat("x", 60))
	_, tok := h.issue("")
	for i := 0; i < 2; i++ { // 60 then 120 bytes served
		if e := errOf(h.call(tok, readOpts("a.txt"))); e != "" {
			t.Fatalf("read %d: %q", i, e)
		}
		h.advance(time.Second)
	}
	h.expectErr(tok, readOpts("a.txt"), CodeRateLimited)
	h.advance(25 * time.Hour)
	if e := errOf(h.call(tok, readOpts("a.txt"))); e != "" {
		t.Fatalf("after 24 h: %q", e)
	}
}

func TestFetchRevokeBetweenFragments(t *testing.T) {
	var h *fetchHarness
	var rec Record
	var once sync.Once
	h = newFetchHarness(t, nil, func(c *FetchConfig) {
		inner := c.Send
		c.Send = func(ctx context.Context, peer string, pt []byte) error {
			err := inner(ctx, peer, pt)
			if bytes.Contains(pt, []byte(`"frag":0`)) {
				once.Do(func() { h.revoke(rec.ID) }) // the revoke lands right after fragment 0
			}
			return err
		}
	})
	data := strings.Repeat("y", 3*FragmentBytes)
	h.write("big", data)
	var tok json.RawMessage
	rec, tok = h.issue("")
	resp := h.call(tok, readOpts("big"))
	if len(resp) != 2 || resp[0]["ok"] != true || errOf(resp) != CodeRevoked {
		t.Fatalf("responses = %v", resp)
	}
	// and the next fetch fails at once
	h.expectErr(tok, reqOpts{op: OpStat, path: "big"}, CodeRevoked)
	if rows := h.auditsOf("grant.fetch"); len(rows) == 0 || rows[0].Detail["result"] != CodeRevoked {
		t.Fatalf("audit = %v", rows)
	}
}

func TestFetchSessionClosedBetweenFragments(t *testing.T) {
	var h *fetchHarness
	var once sync.Once
	h = newFetchHarness(t, nil, func(c *FetchConfig) {
		inner := c.Send
		c.Send = func(ctx context.Context, peer string, pt []byte) error {
			err := inner(ctx, peer, pt)
			if bytes.Contains(pt, []byte(`"frag":0`)) {
				once.Do(func() { h.open.Store(false) })
			}
			return err
		}
	})
	h.write("big", strings.Repeat("y", 2*FragmentBytes))
	_, tok := h.issue("")
	if e := errOf(h.call(tok, readOpts("big"))); e != ReasonSessionNotOpen {
		t.Fatalf("error = %q", e)
	}
}

func TestFetchAuditRateLimitAndSummary(t *testing.T) {
	h := newFetchHarness(t, nil, func(c *FetchConfig) { c.BytesPer24h = 1 << 40 })
	h.write("a.txt", "x")
	rec, tok := h.issue("")
	total := 0
	for total < 70 {
		for i := 0; i < maxOpsPerSecond && total < 70; i++ {
			if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt"})); e != "" {
				t.Fatalf("op %d: %q", total, e)
			}
			total++
		}
		h.advance(time.Second)
	}
	// 60 rows in the first minute, the rest suppressed
	if n := len(h.auditsOf("grant.fetch")); n != auditRowsPerMinute {
		t.Fatalf("grant.fetch rows = %d, want %d", n, auditRowsPerMinute)
	}
	if n := len(h.auditsOf("grant.fetch_summary")); n != 0 {
		t.Fatalf("summaries before the minute ends = %d", n)
	}
	h.advance(time.Minute)
	h.srv.Flush(context.Background())
	sum := h.auditsOf("grant.fetch_summary")
	if len(sum) != 1 {
		t.Fatalf("summaries = %v", sum)
	}
	d := sum[0].Detail
	if d["grant"] != rec.ID || d["ops"].(int64) != 10 || d["errors"].(int64) != 0 {
		t.Fatalf("summary = %v", d)
	}
	for _, a := range h.audits {
		b, _ := json.Marshal(a.Detail)
		if strings.Contains(string(b), "a.txt") {
			t.Fatalf("audit leaks a path: %s", b)
		}
	}
}

// Review 34 M1: a peer without a valid grant leaves nothing in the dedupe
// store, and one peer's full store does not refuse another peer's requests.
func TestFetchDedupeOnlyAfterAuthAndPerPeer(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	req := newReqID()
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt", req: req, peer: h.other}, ReasonWrongAudience)
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt", req: req})); e != "" {
		t.Fatalf("req first used by an unauthorised peer: %q", e)
	}
	now := time.Unix(0, h.clock.Load()).UTC()
	for i := 0; i < maxSeenPerPeer; i++ {
		if !h.srv.markSeen(h.other, fmt.Sprintf("f-%032x", i), now) {
			t.Fatalf("markSeen %d refused", i)
		}
	}
	if h.srv.markSeen(h.other, newReqID(), now) {
		t.Fatal("a full per-peer store accepted another req")
	}
	if e := errOf(h.call(tok, reqOpts{op: OpStat, path: "a.txt"})); e != "" {
		t.Fatalf("another peer's flood refused the holder: %q", e)
	}
}

// Review 34 M3: a peer over its in-flight limit gets at most
// maxRejectsPerPeer queued rate_limited answers; the rest are dropped.
func TestFetchRejectFloodBounded(t *testing.T) {
	be := &blockBackend{started: make(chan struct{}, 8), release: make(chan struct{})}
	h := newFetchHarness(t, be, func(c *FetchConfig) { c.Workers = 2 })
	h.write("a.txt", "x")
	_, tok := h.issue("")
	h.send(tok, readOpts("a.txt"))
	h.send(tok, readOpts("a.txt"))
	for i := 0; i < 2; i++ {
		select {
		case <-be.started:
		case <-time.After(5 * time.Second):
			t.Fatal("reads did not start")
		}
	}
	// both workers are busy: rejects queue up, but only maxRejectsPerPeer of them
	for i := 0; i < 50; i++ {
		h.send(tok, readOpts("a.txt"))
	}
	pending := func() int {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.rejects[h.holder]
	}
	if n := pending(); n != maxRejectsPerPeer {
		t.Fatalf("queued rejects = %d, want %d", n, maxRejectsPerPeer)
	}
	close(be.release)
	limited, ok := 0, 0
	for limited+ok < 2+maxRejectsPerPeer {
		m := h.next()
		switch {
		case m["error"] == CodeRateLimited:
			limited++
		case m["ok"] == true:
			ok++
		default:
			t.Fatalf("unexpected %v", m)
		}
	}
	select {
	case pt := <-h.out:
		t.Fatalf("more answers than queued: %s", pt)
	case <-time.After(200 * time.Millisecond):
	}
	if limited != maxRejectsPerPeer || pending() != 0 {
		t.Fatalf("rate_limited = %d, pending = %d", limited, pending())
	}
}

// Review 34 M2: the peer-supplied op never reaches the audit log as sent.
func TestFetchAuditOpIsEnum(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	h.write("a.txt", "x")
	_, tok := h.issue("")
	h.expectErr(tok, reqOpts{op: "customer-acme-secret-plan", path: "a.txt"}, ReasonMalformed)
	rows := h.auditsOf("grant.fetch")
	if len(rows) != 1 || rows[0].Detail["op"] != "unknown" {
		t.Fatalf("audit = %v", rows)
	}
}

// Review 34 L1: names that JSON escapes heavily still fit one response.
func TestFetchListEscapedNamesFit(t *testing.T) {
	h := newFetchHarness(t, nil, nil)
	for i := 0; i < 120; i++ {
		h.write(fmt.Sprintf("%s%03d", strings.Repeat("&", 200), i), "")
	}
	_, tok := h.issue("")
	seen, cursor := 0, ""
	for page := 0; page < 10; page++ {
		resp := h.call(tok, reqOpts{op: OpList, path: "", cursor: cursor})
		if e := errOf(resp); e != "" {
			t.Fatalf("list: %q", e)
		}
		seen += len(resp[0]["entries"].([]any))
		cursor, _ = resp[0]["cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if seen != 120 {
		t.Fatalf("listed %d, want 120", seen)
	}
}

func TestFetchUnsupportedKindAndStopped(t *testing.T) {
	h := newFetchHarness(t, nil, func(c *FetchConfig) { c.Backends = map[string]Backend{} })
	h.write("a.txt", "x")
	_, tok := h.issue("")
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, CodeUnsupported)
	h.srv.Close()
	h.srv.Handle(h.holder, []byte(`{"type":"fetch.req","req":"`+newReqID()+`"}`)) // no panic, no answer
}

// linkDir makes link a directory link to target: a symlink, or on Windows
// without the symlink privilege a junction.
func linkDir(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	makeJunction(t, target, link)
}

// linkFile makes link a file symlink to target and reports whether the OS
// allowed it.
func linkFile(t *testing.T, target, link string) bool {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Logf("file symlinks not available here: %v", err)
		return false
	}
	return true
}
