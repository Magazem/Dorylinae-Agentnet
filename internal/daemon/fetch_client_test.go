package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// sendLog records what a fetch client sends. The janitor goroutine can send
// too (a retry), so every access is guarded.
type sendLog struct {
	mu  sync.Mutex
	pts [][]byte
}

func (s *sendLog) send(_ context.Context, _ string, pt []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pts = append(s.pts, append([]byte(nil), pt...))
	return nil
}

func (s *sendLog) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pts)
}

const testGrantor = "grantor-key"

// addReadOp registers a pending read the way start would, without the grant
// checks (those are covered separately).
func addReadOp(c *fetchClient, offset, length int64) *fetchOp {
	op := &fetchOp{
		id: "ft-test", req: randHexID("f-", 16), peer: testGrantor, state: fetchPending, done: make(chan struct{}),
		p:        FetchStartParams{Op: capability.OpRead, Path: "f", Offset: offset, Length: length},
		deadline: time.Now().Add(time.Hour),
	}
	c.mu.Lock()
	c.ops[op.id] = op
	c.reqs[op.req] = op
	c.mu.Unlock()
	return op
}

func opState(c *fetchClient, op *fetchOp) (state, code string, result json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if op.err != nil {
		code = op.err.Code
	}
	return op.state, code, op.result
}

func opReq(c *fetchClient, op *fetchOp) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return op.req
}

func fragResp(req string, frag, frags int, size int64, data []byte) []byte {
	pt, _ := json.Marshal(map[string]any{
		"type": "fetch.resp", "req": req, "ok": true, "frag": frag, "frags": frags, "size": size,
		"data": base64.StdEncoding.EncodeToString(data),
	})
	return pt
}

func newTestFetchClient(t *testing.T) (*fetchClient, *sendLog) {
	t.Helper()
	log := &sendLog{}
	c := newFetchClient(nil, nil, "holder", log.send)
	t.Cleanup(c.Close)
	return c, log
}

// insertHeldGrant stores a grant issued by h to holder in session sid, as the
// holder's row.
func insertHeldGrant(t *testing.T, h *grantHarness, holder, sid string, exp time.Time, state string) string {
	t.Helper()
	ctx := context.Background()
	nbf := exp.Add(-2 * time.Hour)
	tok, err := capability.Sign(h.priv, capability.Grant{
		V: 1, ID: capability.NewID(), Iss: h.self, Aud: holder, Session: sid, Action: capability.ActionFSRead,
		Resource: capability.Resource{Kind: capability.KindFS, Label: "res-ab12"}, Nbf: nbf, Exp: exp, Sensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := capability.Canonical(tok)
	rec := capability.Record{
		ID: tok.Grant.ID, Direction: capability.DirectionHeld, Peer: h.self, Session: sid, Action: capability.ActionFSRead,
		Label: "res-ab12", Sensitive: true, Nbf: nbf, Exp: exp, Token: string(wire),
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.caps.InsertHeldTx(ctx, tx, rec); err != nil {
		t.Fatal(err)
	}
	if state == capability.StateRevoked {
		if _, err := h.caps.RevokeTx(ctx, tx, rec.ID, capability.ReasonUser, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

func TestFetchExpiredGrantFailsLocally(t *testing.T) {
	h, _ := newGrantHarness(t, "code", false)
	ctx := context.Background()
	hpub, _, _ := ed25519.GenerateKey(rand.Reader)
	holder := envelope.KeyString(hpub)
	sid := h.openSession(holder, "r-"+strings.Repeat("c", 32))

	expired := insertHeldGrant(t, h, holder, sid, time.Now().Add(-time.Hour), capability.StateActive)
	revoked := insertHeldGrant(t, h, holder, sid, time.Now().Add(time.Hour), capability.StateRevoked)

	sl := &sendLog{}
	c := newFetchClient(h.caps, h.ws, holder, sl.send)
	t.Cleanup(c.Close)
	for _, tc := range []struct {
		name string
		p    FetchStartParams
		code string
	}{
		{"expired", FetchStartParams{Grant: expired, Op: capability.OpStat, Path: "f"}, capability.ReasonExpired},
		{"revoked row", FetchStartParams{Grant: revoked, Op: capability.OpStat, Path: "f"}, capability.CodeRevoked},
		{"unknown grant", FetchStartParams{Grant: "g-" + strings.Repeat("0", 32), Op: capability.OpStat, Path: "f"}, CodeUnknownGrant},
		{"no grant", FetchStartParams{Op: capability.OpStat, Path: "f"}, ipc.CodeBadRequest},
		{"bad op", FetchStartParams{Grant: expired, Op: "write", Path: "f"}, ipc.CodeBadRequest},
		{"stat without path", FetchStartParams{Grant: expired, Op: capability.OpStat}, ipc.CodeBadRequest},
		{"dot dot", FetchStartParams{Grant: expired, Op: capability.OpRead, Path: "a/../b"}, capability.CodeBadPath},
		{"backslash", FetchStartParams{Grant: expired, Op: capability.OpRead, Path: `a\b`}, capability.CodeBadPath},
		{"length too big", FetchStartParams{Grant: expired, Op: capability.OpRead, Path: "f", Length: capability.MaxReadBytes + 1}, ipc.CodeBadRequest},
		{"negative offset", FetchStartParams{Grant: expired, Op: capability.OpRead, Path: "f", Offset: -1}, ipc.CodeBadRequest},
		{"timeout too long", FetchStartParams{Grant: expired, Op: capability.OpStat, Path: "f", TimeoutS: 301}, ipc.CodeBadRequest},
	} {
		st, err := c.start(ctx, tc.p)
		if got := ipcCode(err); got != tc.code {
			t.Errorf("%s: start = %+v, %v; want error %q", tc.name, st, err, tc.code)
		}
	}
	if n := sl.count(); n != 0 {
		t.Fatalf("%d message(s) were sent for grants that fail locally, want none", n)
	}
}

func TestFetchReassemblesOutOfOrderAndDuplicates(t *testing.T) {
	c, _ := newTestFetchClient(t)
	const size = 3*capability.FragmentBytes + 1696
	want := make([]byte, size)
	_, _ = rand.Read(want)
	op := addReadOp(c, 0, capability.MaxReadBytes)
	req := opReq(c, op)
	piece := func(i int) []byte {
		lo := i * capability.FragmentBytes
		return want[lo:min(lo+capability.FragmentBytes, size)]
	}
	for _, i := range []int{2, 0, 0, 3} {
		c.onResp(testGrantor, fragResp(req, i, 4, size, piece(i)))
		if st, _, _ := opState(c, op); st != fetchPending {
			t.Fatalf("state after fragment %d = %s, want pending", i, st)
		}
	}
	c.onResp(testGrantor, fragResp(req, 1, 4, size, piece(1)))
	st, code, res := opState(c, op)
	if st != fetchComplete {
		t.Fatalf("state = %s (%s), want complete", st, code)
	}
	var r struct {
		Data   string `json:"data"`
		Offset int64  `json:"offset"`
		Size   int64  `json:"size"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	got, _ := base64.StdEncoding.DecodeString(r.Data)
	if string(got) != string(want) || r.Size != size || r.Offset != 0 {
		t.Fatalf("result: %d bytes, size %d, offset %d; want the %d bytes back", len(got), r.Size, r.Offset, size)
	}
}

func TestFetchRefusesBadResponses(t *testing.T) {
	half := make([]byte, capability.FragmentBytes)
	for _, tc := range []struct {
		name string
		send func(c *fetchClient, req string)
		want string // "" = still pending
	}{
		{"error response", func(c *fetchClient, req string) {
			c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":false,"error":"revoked"}`))
		}, "revoked"},
		{"error code with spaces", func(c *fetchClient, req string) {
			c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":false,"error":"a b <script>"}`))
		}, "io_error"},
		{"another peer", func(c *fetchClient, req string) {
			c.onResp("someone-else", fragResp(req, 0, 1, 3, []byte("abc")))
		}, ""},
		{"unknown request", func(c *fetchClient, _ string) {
			c.onResp(testGrantor, fragResp("f-"+strings.Repeat("0", 32), 0, 1, 3, []byte("abc")))
		}, ""},
		{"not JSON", func(c *fetchClient, _ string) { c.onResp(testGrantor, []byte("nope")) }, ""},
		{"too many fragments", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 0, 9, 10, half))
		}, "io_error"},
		{"fragment past the count", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 2, 2, 10, half))
		}, "io_error"},
		{"size changes between fragments", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 0, 2, 40000, half))
			c.onResp(testGrantor, fragResp(req, 1, 2, 50000, half))
		}, "io_error"},
		{"bad base64", func(c *fetchClient, req string) {
			c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":true,"frag":0,"frags":1,"size":3,"data":"!!"}`))
		}, "io_error"},
		{"fragment over 32768 bytes", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 0, 1, 40000, make([]byte, capability.FragmentBytes+1)))
		}, "io_error"},
		{"short read", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 0, 1, 100, []byte("abc")))
		}, "io_error"},
		{"data beyond the request", func(c *fetchClient, req string) {
			c.onResp(testGrantor, fragResp(req, 0, 1, 3, []byte("abcdef")))
		}, "io_error"},
		{"base64 larger than a fragment", func(c *fetchClient, req string) {
			c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":true,"frag":0,"frags":1,"size":3,"data":"`+strings.Repeat("A", 50000)+`"}`))
		}, "io_error"},
		{"commit not a hash", func(c *fetchClient, req string) {
			c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":true,"frag":0,"frags":2,"size":40000,"commit":"\u001b[31m","data":""}`))
		}, "io_error"},
		{"commit changes between fragments", func(c *fetchClient, req string) {
			for i, commit := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
				c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+req+`","ok":true,"frag":`+string(rune('0'+i))+`,"frags":2,"size":40000,"commit":"`+commit+`","data":""}`))
			}
		}, "io_error"},
	} {
		c, _ := newTestFetchClient(t)
		op := addReadOp(c, 0, capability.MaxReadBytes)
		tc.send(c, opReq(c, op))
		st, code, _ := opState(c, op)
		switch {
		case tc.want == "" && st != fetchPending:
			t.Errorf("%s: state %s (%s), want pending", tc.name, st, code)
		case tc.want != "" && (st != fetchFailed || code != tc.want):
			t.Errorf("%s: state %s (%s), want failed with %s", tc.name, st, code, tc.want)
		}
	}
}

// A read with a missing fragment is sent again, whole, under a new request id;
// fragments of the abandoned attempt no longer count (Docs/protocol/grant.md
// §Transport).
func TestFetchRetriesReadWithMissingFragment(t *testing.T) {
	c, sl := newTestFetchClient(t)
	const size = capability.FragmentBytes + 10
	data := make([]byte, size)
	_, _ = rand.Read(data)
	op := addReadOp(c, 0, capability.MaxReadBytes)
	first := opReq(c, op)
	c.onResp(testGrantor, fragResp(first, 0, 2, size, data[:capability.FragmentBytes]))

	if r := c.tick(time.Now()); len(r) != 0 {
		t.Fatalf("a retry before %s: %d resend(s)", c.retryAfter, len(r))
	}
	for attempt := 1; attempt <= fetchMaxRetries; attempt++ {
		resends := c.tick(time.Now().Add(time.Duration(attempt) * time.Hour / 100))
		if len(resends) != 1 {
			t.Fatalf("attempt %d: %d resends, want 1", attempt, len(resends))
		}
		var w fetchWireReq
		if err := json.Unmarshal(resends[0].pt, &w); err != nil || w.Req == first || w.Op != capability.OpRead || *w.Length != capability.MaxReadBytes || *w.Offset != 0 {
			t.Fatalf("resend = %s (%v), want the same read under a new req", resends[0].pt, err)
		}
		// the old attempt's fragment is ignored; the new attempt starts empty
		c.onResp(testGrantor, fragResp(first, 1, 2, size, data[capability.FragmentBytes:]))
		if st, _, _ := opState(c, op); st != fetchPending {
			t.Fatalf("a fragment of the abandoned attempt changed the state to %s", st)
		}
		c.onResp(testGrantor, fragResp(opReq(c, op), 0, 2, size, data[:capability.FragmentBytes]))
	}
	if r := c.tick(time.Now().Add(50 * time.Minute)); len(r) != 0 {
		t.Fatalf("a %dth retry was sent", fetchMaxRetries+1)
	}
	last := opReq(c, op)
	c.onResp(testGrantor, fragResp(last, 0, 2, size, data[:capability.FragmentBytes]))
	c.onResp(testGrantor, fragResp(last, 1, 2, size, data[capability.FragmentBytes:]))
	if st, code, _ := opState(c, op); st != fetchComplete {
		t.Fatalf("state = %s (%s), want complete after the last attempt", st, code)
	}
	if sl.count() != 0 {
		t.Fatalf("the test's own ticks must not have sent anything through the janitor, got %d", sl.count())
	}
}

func TestFetchDeadlineAndKeep(t *testing.T) {
	c, _ := newTestFetchClient(t)
	op := addReadOp(c, 0, 10)
	c.mu.Lock()
	op.deadline = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.tick(time.Now())
	if st, code, _ := opState(c, op); st != fetchFailed || code != CodeFetchTimeout {
		t.Fatalf("after the deadline: %s (%s), want failed with timeout", st, code)
	}
	if _, err := c.status(context.Background(), op.id); err != nil {
		t.Fatalf("a failed fetch must stay readable: %v", err)
	}
	c.tick(time.Now().Add(fetchKeep + time.Second))
	if _, err := c.status(context.Background(), op.id); ipcCode(err) != capability.CodeNotFound {
		t.Fatalf("after %s the fetch is gone, got %v", fetchKeep, err)
	}
}

func TestFetchListAndStatResultsPassThrough(t *testing.T) {
	c, _ := newTestFetchClient(t)
	op := &fetchOp{
		id: "ft-l", req: randHexID("f-", 16), peer: testGrantor, state: fetchPending, done: make(chan struct{}),
		p: FetchStartParams{Op: capability.OpList}, deadline: time.Now().Add(time.Hour),
	}
	c.mu.Lock()
	c.ops[op.id], c.reqs[op.req] = op, op
	c.mu.Unlock()
	c.onResp(testGrantor, []byte(`{"type":"fetch.resp","req":"`+op.req+`","ok":true,"entries":[{"name":"a","type":"file","size":3}],"cursor":"a","commit":"`+strings.Repeat("a", 40)+`"}`))
	st, _, res := opState(c, op)
	if st != fetchComplete {
		t.Fatalf("state = %s", st)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(res, &r); err != nil || len(r) != 3 || r["entries"] == nil || r["cursor"] == nil || r["commit"] == nil {
		t.Fatalf("result = %s (%v), want entries, cursor and commit only", res, err)
	}
}

// A stat or list response from the grantor is checked and only its known
// members are kept.
func TestFetchChecksListAndStatResponses(t *testing.T) {
	longName := strings.Repeat("n", 1025)
	for _, tc := range []struct {
		name, op, body string
		want           string // "" = complete
	}{
		{"extra members dropped", capability.OpStat, `"entry":{"name":"a","type":"file","size":1},"injected":"x"`, ""},
		{"stat without entry", capability.OpStat, `"entries":[]`, "io_error"},
		{"unknown type", capability.OpStat, `"entry":{"name":"a","type":"fifo"}`, "io_error"},
		{"negative size", capability.OpStat, `"entry":{"name":"a","type":"file","size":-1}`, "io_error"},
		{"empty name", capability.OpList, `"entries":[{"name":"","type":"file"}]`, "io_error"},
		{"name too long", capability.OpList, `"entries":[{"name":"` + longName + `","type":"file"}]`, "io_error"},
		{"bad commit", capability.OpList, `"entries":[],"commit":"HEAD"`, "io_error"},
		{"entries not a list", capability.OpList, `"entries":{"a":1}`, "io_error"},
		{"empty listing", capability.OpList, ``, ""},
	} {
		c, _ := newTestFetchClient(t)
		op := &fetchOp{
			id: "ft-v", req: randHexID("f-", 16), peer: testGrantor, state: fetchPending, done: make(chan struct{}),
			p: FetchStartParams{Op: tc.op}, deadline: time.Now().Add(time.Hour),
		}
		c.mu.Lock()
		c.ops[op.id], c.reqs[op.req] = op, op
		c.mu.Unlock()
		body := `{"type":"fetch.resp","req":"` + op.req + `","ok":true`
		if tc.body != "" {
			body += "," + tc.body
		}
		c.onResp(testGrantor, []byte(body+"}"))
		st, code, res := opState(c, op)
		switch {
		case tc.want == "" && (st != fetchComplete || strings.Contains(string(res), "injected") || strings.Contains(string(res), `"req"`)):
			t.Errorf("%s: %s (%s) %s, want complete with known members only", tc.name, st, code, res)
		case tc.want != "" && (st != fetchFailed || code != tc.want):
			t.Errorf("%s: %s (%s), want failed with %s", tc.name, st, code, tc.want)
		}
	}
}

// Finished fetches kept for fetch_status do not count against the live limit:
// the oldest is dropped instead. A slow send shortens the wait so that the call
// still returns within the IPC budget.
func TestFetchStartKeepsFinishedBoundedAndReturnsInTime(t *testing.T) {
	h, _ := newGrantHarness(t, "code", false)
	hpub, _, _ := ed25519.GenerateKey(rand.Reader)
	holder := envelope.KeyString(hpub)
	// the holder's view of the session: worker, with the grantor as peer
	hws := &worksession.Store{DB: h.db, Self: holder, Audit: h.log}
	reqID := "r-" + strings.Repeat("d", 32)
	ctx := context.Background()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := hws.OpenSession(ctx, tx, worksession.RoleWorker, h.self, reqID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sid := worksession.DeriveID(h.self, holder, reqID)
	grant := insertHeldGrant(t, h, holder, sid, time.Now().Add(time.Hour), capability.StateActive)

	slow := func(context.Context, string, []byte) error {
		time.Sleep(1200 * time.Millisecond)
		return nil
	}
	c := newFetchClient(h.caps, hws, holder, slow)
	t.Cleanup(c.Close)
	old := time.Now().Add(-time.Minute / 2)
	c.mu.Lock()
	for i := range fetchMaxKept {
		id := "ft-done" + string(rune('A'+i))
		c.ops[id] = &fetchOp{id: id, peer: "p", state: fetchComplete, finished: old.Add(time.Duration(i) * time.Millisecond), done: make(chan struct{})}
	}
	c.mu.Unlock()

	begin := time.Now()
	st, err := c.start(context.Background(), FetchStartParams{Grant: grant, Op: capability.OpStat, Path: "f"})
	if err != nil || st.State != fetchPending {
		t.Fatalf("start = %+v, %v; want pending", st, err)
	}
	if d := time.Since(begin); d > fetchCallBudget+300*time.Millisecond {
		t.Fatalf("start took %s with a slow send, want about %s", d, fetchCallBudget)
	}
	c.mu.Lock()
	n, oldestGone := len(c.ops), c.ops["ft-doneA"] == nil
	c.mu.Unlock()
	if n != fetchMaxKept || !oldestGone {
		t.Fatalf("%d ops kept (oldest dropped: %v), want %d finished plus the new one", n, oldestGone, fetchMaxKept-1)
	}
}
