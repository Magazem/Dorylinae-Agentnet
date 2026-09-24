package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// The holder side of a fetch (Docs/protocol/grant.md §Enforcement and fetch,
// §IPC): fetch_start verifies the grant locally, sends a fetch.req over the
// Noise session and returns an id; fetch_status polls it. The reassembly of the
// fragments of a read, and its retry, live here.

// CodeFetchTimeout is the error of a fetch the grantor did not answer in time.
const CodeFetchTimeout = "timeout"

const (
	// fetchWait is how long fetch_start and fetch_status wait for an answer
	// before returning "pending" (every IPC call returns within 2 s).
	fetchWait = time.Second
	// fetchKeep is how long a finished fetch stays readable.
	fetchKeep = 60 * time.Second
	// fetchDefaultTimeout and fetchMaxTimeout bound how long an operation may
	// stay pending; a longer wait is the caller's polling.
	fetchDefaultTimeout = 30 * time.Second
	fetchMaxTimeout     = 300 * time.Second
	// fetchRetryAfter: a read with a missing fragment is sent again, whole, once
	// no fragment has arrived for this long (Docs/protocol/grant.md §Transport).
	fetchRetryAfter = 10 * time.Second
	fetchMaxRetries = 2
	// fetchMaxPerPeer matches the grantor's limit of two in flight per holder
	// (Docs/protocol/grant.md §Limits): more would only be answered rate_limited.
	fetchMaxPerPeer = 2
	fetchMaxLive    = 64
	fetchTick       = 200 * time.Millisecond
	maxFragments    = capability.MaxReadBytes / capability.FragmentBytes
)

// Fetch states, as in ping.
const (
	fetchPending  = "pending"
	fetchComplete = "complete"
	fetchFailed   = "failed"
)

var fetchCodePattern = regexp.MustCompile(`^[a-z_]{1,32}$`)

// FetchStartParams are the params of "fetch_start". Path is required for stat
// and read; Length defaults to (and is at most) 256 KiB. TimeoutS is how long
// the operation may stay pending (default 30, at most 300).
type FetchStartParams struct {
	Grant    string `json:"grant"`
	Op       string `json:"op"`
	Path     string `json:"path,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	Length   int64  `json:"length,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

// FetchStatusParams are the params of "fetch_status".
type FetchStatusParams struct {
	FetchID string `json:"fetch_id"`
}

// FetchError is the error member of a failed fetch: the grantor's code, or a
// local one (timeout, io_error).
type FetchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FetchStatus is the result of fetch_start and fetch_status.
type FetchStatus struct {
	FetchID string          `json:"fetch_id"`
	State   string          `json:"state"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *FetchError     `json:"error,omitempty"`
}

type fetchOp struct {
	id, req, peer string
	p             FetchStartParams
	token         json.RawMessage
	deadline      time.Time

	state    string
	result   json.RawMessage
	err      *FetchError
	finished time.Time
	done     chan struct{}

	// read reassembly
	frags    [][]byte
	have     int
	size     int64
	commit   string
	lastFrag time.Time
	retries  int
}

type fetchWireReq struct {
	Type   string          `json:"type"`
	Req    string          `json:"req"`
	TS     string          `json:"ts"`
	Token  json.RawMessage `json:"token"`
	Op     string          `json:"op"`
	Path   string          `json:"path"`
	Offset *int64          `json:"offset,omitempty"`
	Length *int64          `json:"length,omitempty"`
	Cursor string          `json:"cursor,omitempty"`
}

type fetchClient struct {
	caps *capability.Store
	ws   *worksession.Store
	self string
	send func(ctx context.Context, peer string, pt []byte) error
	// retryAfter is fetchRetryAfter; tests shorten it.
	retryAfter time.Duration

	mu   sync.Mutex
	ops  map[string]*fetchOp
	reqs map[string]*fetchOp // request id of the current attempt -> op

	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// startFetchClient registers the fetch.resp handler on sessions and starts the
// janitor (deadlines, retries, expiry of finished fetches). Close stops it.
func startFetchClient(sessions *session.Manager, caps *capability.Store, ws *worksession.Store, self string) *fetchClient {
	c := newFetchClient(caps, ws, self, sessions.Send)
	sessions.Handle(capability.TypeFetchResp, c.onResp)
	return c
}

func newFetchClient(caps *capability.Store, ws *worksession.Store, self string, send func(context.Context, string, []byte) error) *fetchClient {
	c := &fetchClient{
		caps: caps, ws: ws, self: self, send: send, retryAfter: fetchRetryAfter,
		ops: map[string]*fetchOp{}, reqs: map[string]*fetchOp{}, stop: make(chan struct{}),
	}
	c.wg.Add(1)
	go c.janitor()
	return c
}

// Close stops the janitor.
func (c *fetchClient) Close() {
	c.once.Do(func() { close(c.stop) })
	c.wg.Wait()
}

func randHexID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (c *fetchClient) janitor() {
	defer c.wg.Done()
	t := time.NewTicker(fetchTick)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			for _, r := range c.tick(time.Now()) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = c.send(ctx, r.peer, r.pt) // a failed resend ends at the deadline
				cancel()
			}
		}
	}
}

type resend struct {
	peer string
	pt   []byte
}

// tick applies the deadlines, prepares the retries and drops old finished ops.
func (c *fetchClient) tick(now time.Time) []resend {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []resend
	for id, op := range c.ops {
		switch {
		case op.state != fetchPending:
			if now.Sub(op.finished) >= fetchKeep {
				delete(c.ops, id)
			}
		case !now.Before(op.deadline):
			c.finishLocked(op, nil, &FetchError{Code: CodeFetchTimeout, Message: "the grantor did not answer in time"}, now)
		case op.have > 0 && op.have < len(op.frags) && now.Sub(op.lastFrag) >= c.retryAfter && op.retries < fetchMaxRetries:
			delete(c.reqs, op.req)
			op.retries++
			op.req = randHexID("f-", 16)
			c.reqs[op.req] = op
			op.frags, op.have, op.lastFrag = nil, 0, now
			out = append(out, resend{peer: op.peer, pt: c.wireLocked(op, now)})
		}
	}
	return out
}

func (c *fetchClient) wireLocked(op *fetchOp, now time.Time) []byte {
	w := fetchWireReq{
		Type: capability.TypeFetchReq, Req: op.req, TS: now.UTC().Format(time.RFC3339),
		Token: op.token, Op: op.p.Op, Path: op.p.Path, Cursor: op.p.Cursor,
	}
	if op.p.Op == capability.OpRead {
		off, length := op.p.Offset, op.p.Length
		w.Offset, w.Length = &off, &length
	}
	pt, _ := json.Marshal(w)
	return pt
}

// finishLocked ends op with a result or an error.
func (c *fetchClient) finishLocked(op *fetchOp, result json.RawMessage, ferr *FetchError, now time.Time) {
	if op.state != fetchPending {
		return
	}
	op.state = fetchComplete
	if ferr != nil {
		op.state = fetchFailed
	}
	op.result, op.err, op.finished = result, ferr, now
	op.frags = nil
	delete(c.reqs, op.req)
	close(op.done)
}

// onResp handles a fetch.resp plaintext from peer. It runs on the session
// manager's receive goroutine, so it only takes the lock and records.
func (c *fetchClient) onResp(peer string, pt []byte) {
	var m map[string]json.RawMessage
	if json.Unmarshal(pt, &m) != nil {
		return
	}
	var req string
	_ = json.Unmarshal(m["req"], &req)
	c.mu.Lock()
	defer c.mu.Unlock()
	op := c.reqs[req]
	if op == nil || op.peer != peer || op.state != fetchPending {
		return // late (an earlier attempt or a finished fetch) or not ours
	}
	now := time.Now()
	var ok bool
	_ = json.Unmarshal(m["ok"], &ok)
	if !ok {
		var code string
		_ = json.Unmarshal(m["error"], &code)
		if !fetchCodePattern.MatchString(code) {
			code = capability.CodeIO
		}
		c.finishLocked(op, nil, &FetchError{Code: code, Message: "the grantor refused the fetch: " + code}, now)
		return
	}
	if op.p.Op != capability.OpRead {
		delete(m, "type")
		delete(m, "req")
		delete(m, "ok")
		res, _ := json.Marshal(m)
		c.finishLocked(op, res, nil, now)
		return
	}
	c.onFragLocked(op, m, now)
}

func (c *fetchClient) onFragLocked(op *fetchOp, m map[string]json.RawMessage, now time.Time) {
	var frag, frags int
	var size int64
	var data, commit string
	if json.Unmarshal(m["frag"], &frag) != nil || json.Unmarshal(m["frags"], &frags) != nil ||
		json.Unmarshal(m["size"], &size) != nil || json.Unmarshal(m["data"], &data) != nil {
		return
	}
	_ = json.Unmarshal(m["commit"], &commit)
	bad := func(msg string) {
		c.finishLocked(op, nil, &FetchError{Code: capability.CodeIO, Message: msg}, now)
	}
	if frags < 1 || frags > maxFragments || frag < 0 || frag >= frags || size < 0 {
		bad("the grantor sent a malformed fragment")
		return
	}
	if op.frags == nil {
		op.frags, op.size, op.commit = make([][]byte, frags), size, commit
	}
	if frags != len(op.frags) || size != op.size {
		bad("the grantor sent inconsistent fragments")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) > capability.FragmentBytes {
		bad("the grantor sent a malformed fragment")
		return
	}
	op.lastFrag = now
	if op.frags[frag] != nil {
		return // a duplicate
	}
	op.frags[frag] = raw
	op.have++
	if op.have < len(op.frags) {
		return
	}
	var all []byte
	for _, f := range op.frags {
		all = append(all, f...)
	}
	want := int64(0)
	if op.p.Offset < op.size {
		want = min(op.p.Length, op.size-op.p.Offset)
	}
	if int64(len(all)) != want {
		bad("the grantor sent a short read")
		return
	}
	res := map[string]any{"data": base64.StdEncoding.EncodeToString(all), "offset": op.p.Offset, "size": op.size}
	if op.commit != "" {
		res["commit"] = op.commit
	}
	b, _ := json.Marshal(res)
	c.finishLocked(op, b, nil, now)
}

func (c *fetchClient) snapshotLocked(op *fetchOp) FetchStatus {
	return FetchStatus{FetchID: op.id, State: op.state, Result: op.result, Error: op.err}
}

// wait returns the status of op after at most fetchWait.
func (c *fetchClient) wait(ctx context.Context, op *fetchOp) FetchStatus {
	t := time.NewTimer(fetchWait)
	defer t.Stop()
	select {
	case <-op.done:
	case <-t.C:
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked(op)
}

func badFetch(msg string) error { return &ipc.Error{Code: ipc.CodeBadRequest, Message: msg} }

// start runs the local checks (Verify steps 1-8: an expired or ended grant
// fails here, with no message sent), sends the request and waits briefly.
func (c *fetchClient) start(ctx context.Context, p FetchStartParams) (FetchStatus, error) {
	if p.Grant == "" {
		return FetchStatus{}, badFetch("grant is required")
	}
	switch p.Op {
	case capability.OpStat, capability.OpRead:
		if p.Path == "" {
			return FetchStatus{}, badFetch("path is required")
		}
	case capability.OpList:
		if !utf8.ValidString(p.Cursor) || len(p.Cursor) > 1024 {
			return FetchStatus{}, badFetch("cursor is not valid")
		}
	default:
		return FetchStatus{}, badFetch("op must be stat, list or read")
	}
	if !capability.ValidScopePath(p.Path) {
		return FetchStatus{}, &ipc.Error{Code: capability.CodeBadPath, Message: "the path is not a valid relative path"}
	}
	if p.Op == capability.OpRead {
		if p.Length == 0 {
			p.Length = capability.MaxReadBytes
		}
		if p.Offset < 0 || p.Length < 1 || p.Length > capability.MaxReadBytes {
			return FetchStatus{}, badFetch("offset must be >= 0 and length 1 to 262144")
		}
	}
	timeout := fetchDefaultTimeout
	if p.TimeoutS != 0 {
		if p.TimeoutS < 1 || time.Duration(p.TimeoutS)*time.Second > fetchMaxTimeout {
			return FetchStatus{}, badFetch("timeout_s must be 1 to 300")
		}
		timeout = time.Duration(p.TimeoutS) * time.Second
	}

	rec, err := c.caps.Get(ctx, p.Grant)
	if err != nil {
		return FetchStatus{}, grantError(err)
	}
	if rec.Direction != capability.DirectionHeld {
		return FetchStatus{}, &ipc.Error{Code: CodeUnknownGrant, Message: "not a grant you hold"}
	}
	if rec.State == capability.StateRevoked {
		return FetchStatus{}, &ipc.Error{Code: capability.CodeRevoked, Message: "the grant was revoked"}
	}
	now := time.Now()
	if _, err := capability.Verify([]byte(rec.Token), capability.VerifyParams{
		Role: capability.RoleHolder, Self: c.self, Counterparty: rec.Peer, Now: now,
		SessionOpen: func(id, requester, worker string) (bool, bool) {
			v, err := c.ws.Get(ctx, id)
			if err != nil {
				return false, false
			}
			r, w := v.SelfOf(c.self)
			if r != requester || w != worker {
				return false, false
			}
			return true, v.State == worksession.StateOpen
		},
	}); err != nil {
		reason := capability.ReasonOf(err)
		if reason == "" {
			return FetchStatus{}, err
		}
		return FetchStatus{}, &ipc.Error{Code: reason, Message: "the grant cannot be used: " + reason}
	}

	op := &fetchOp{
		id: randHexID("ft-", 8), req: randHexID("f-", 16), peer: rec.Peer, p: p, token: json.RawMessage(rec.Token),
		deadline: now.Add(timeout), state: fetchPending, done: make(chan struct{}),
	}
	c.mu.Lock()
	inflight := 0
	for _, o := range c.ops {
		if o.state == fetchPending && o.peer == op.peer {
			inflight++
		}
	}
	if inflight >= fetchMaxPerPeer || len(c.ops) >= fetchMaxLive {
		c.mu.Unlock()
		return FetchStatus{}, &ipc.Error{Code: capability.CodeRateLimited, Message: "too many fetches in progress; wait for one to finish"}
	}
	c.ops[op.id] = op
	c.reqs[op.req] = op
	pt := c.wireLocked(op, now)
	c.mu.Unlock()

	if err := c.send(ctx, op.peer, pt); err != nil {
		c.mu.Lock()
		delete(c.ops, op.id)
		delete(c.reqs, op.req)
		c.mu.Unlock()
		var ie *ipc.Error
		if e := pingError(err); errors.As(e, &ie) {
			return FetchStatus{}, ie
		}
		return FetchStatus{}, &ipc.Error{Code: capability.CodeIO, Message: "could not send the fetch request"}
	}
	return c.wait(ctx, op), nil
}

func (c *fetchClient) status(ctx context.Context, id string) (FetchStatus, error) {
	c.mu.Lock()
	op := c.ops[id]
	c.mu.Unlock()
	if op == nil {
		return FetchStatus{}, &ipc.Error{Code: capability.CodeNotFound, Message: "no fetch with that id (finished fetches are kept for 60 s)"}
	}
	return c.wait(ctx, op), nil
}

func registerFetch(srv *ipc.Server, c *fetchClient) {
	srv.Handle("fetch_start", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p FetchStartParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, badFetch("malformed params")
		}
		return c.start(ctx, p)
	})
	srv.Handle("fetch_status", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p FetchStatusParams
		if err := json.Unmarshal(params, &p); err != nil || p.FetchID == "" {
			return nil, badFetch("fetch_id is required")
		}
		return c.status(ctx, p.FetchID)
	})
}
