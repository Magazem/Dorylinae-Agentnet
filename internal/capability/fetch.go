package capability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"
)

// Message types of Docs/protocol/grant.md §Transport (plaintexts of
// session.data).
const (
	TypeFetchReq  = "fetch.req"
	TypeFetchResp = "fetch.resp"
)

// Fetch operations.
const (
	OpStat = "stat"
	OpList = "list"
	OpRead = "read"
)

// Limits of Docs/protocol/grant.md §Limits and §Transport.
const (
	maxInflightPerGrant  = 2
	maxInflightPerPeer   = 2
	maxInflightTotal     = 32
	maxOpsPerSecond      = 20
	defaultBytesPer24h   = 256 << 20
	tsPast               = 30 * time.Second
	tsFuture             = 10 * time.Minute
	reqRemember          = 11 * time.Minute
	maxSeenPerPeer       = 16384
	maxRejectsPerPeer    = 2
	auditRowsPerMinute   = 60
	defaultWorkers       = 8
	queueSize            = 256
	defaultFlushInterval = 10 * time.Second
	opBudget             = time.Minute
)

var reqPattern = regexp.MustCompile(`^f-[0-9a-f]{32}$`)

// FetchConfig configures a FetchServer.
type FetchConfig struct {
	Store *Store
	// Self is this daemon's identity key (the grantor).
	Self string
	// SessionOpen reports whether id is a known work session between
	// requester and worker and whether it is open (the grantor's own store).
	SessionOpen func(ctx context.Context, id, requester, worker string) (known, open bool)
	// Send encrypts pt to peer on the Noise session (session.Manager.SendData).
	Send func(ctx context.Context, peer string, pt []byte) error
	// Backends serves each resource kind ("fs"); a kind without a backend
	// fails with `unsupported`.
	Backends map[string]Backend
	// Audit appends a daemon audit row. Details carry ids, enums and sizes
	// only, never paths (Docs/protocol/grant.md §Audit).
	Audit func(ctx context.Context, action string, detail map[string]any)
	// Now is the clock; nil = time.Now. It must be safe for concurrent use.
	Now func() time.Time
	// Workers is the size of the worker pool (default 8).
	Workers int
	// BytesPer24h caps the bytes served per grant per 24 h (default 256 MiB).
	BytesPer24h int64
	// FlushInterval is how often audit summaries are flushed (default 10 s).
	FlushInterval time.Duration
}

// FetchServer answers fetch.req messages for the grantor (Docs/protocol/
// grant.md §Enforcement and fetch). Handle never blocks: the work runs on a
// bounded worker pool, off the session manager's receive goroutine.
type FetchServer struct {
	cfg   FetchConfig
	queue chan fetchJob
	stop  chan struct{}
	wg    sync.WaitGroup

	mu            sync.Mutex
	closed        bool
	inflightPeer  map[string]int
	inflightGrant map[string]int
	inflightTotal int
	rejects       map[string]int // queued rate_limited answers per peer
	rate          map[string]*opWindow
	served        map[string]*byteWindow
	seen          map[string]map[string]time.Time // peer -> req -> first seen

	aud *fetchAudit
}

type opWindow struct {
	start time.Time
	n     int
}

type byteWindow struct {
	start time.Time
	n     int64
}

type fetchJob struct {
	peer    string
	req     fetchReq
	reject  string // non-empty: answer with this error, do nothing else
	counted bool   // holds an in-flight slot for peer and the daemon
}

type fetchReq struct {
	Type   string          `json:"type"`
	Req    string          `json:"req"`
	TS     string          `json:"ts"`
	Token  json.RawMessage `json:"token"`
	Op     string          `json:"op"`
	Path   *string         `json:"path"`
	Offset *int64          `json:"offset"`
	Length *int64          `json:"length"`
	Cursor string          `json:"cursor"`
}

type respErr struct {
	Type  string `json:"type"`
	Req   string `json:"req"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type respRead struct {
	Type   string `json:"type"`
	Req    string `json:"req"`
	OK     bool   `json:"ok"`
	Frag   int    `json:"frag"`
	Frags  int    `json:"frags"`
	Size   int64  `json:"size"`
	Commit string `json:"commit,omitempty"`
	Data   string `json:"data"`
}

type respStat struct {
	Type   string `json:"type"`
	Req    string `json:"req"`
	OK     bool   `json:"ok"`
	Entry  Entry  `json:"entry"`
	Commit string `json:"commit,omitempty"`
}

type respList struct {
	Type    string  `json:"type"`
	Req     string  `json:"req"`
	OK      bool    `json:"ok"`
	Entries []Entry `json:"entries"`
	Cursor  string  `json:"cursor,omitempty"`
	Commit  string  `json:"commit,omitempty"`
}

// NewFetchServer starts the worker pool. Call Close to stop it.
func NewFetchServer(cfg FetchConfig) *FetchServer {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.BytesPer24h <= 0 {
		cfg.BytesPer24h = defaultBytesPer24h
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	s := &FetchServer{
		cfg: cfg, queue: make(chan fetchJob, queueSize), stop: make(chan struct{}),
		inflightPeer: map[string]int{}, inflightGrant: map[string]int{}, rejects: map[string]int{},
		rate: map[string]*opWindow{}, served: map[string]*byteWindow{}, seen: map[string]map[string]time.Time{},
	}
	s.aud = newFetchAudit(cfg.Audit)
	s.wg.Add(cfg.Workers + 1)
	for i := 0; i < cfg.Workers; i++ {
		go s.worker()
	}
	go s.flusher()
	return s
}

// Close stops the workers and waits for the running operations.
func (s *FetchServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	s.wg.Wait()
}

func (s *FetchServer) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// Handle takes the plaintext of a fetch.req from peer (the identity the Noise
// session authenticated). It only queues: it is safe to call from the session
// manager's receive goroutine.
func (s *FetchServer) Handle(peer string, pt []byte) {
	var r fetchReq
	if json.Unmarshal(pt, &r) != nil || r.Type != TypeFetchReq || !reqPattern.MatchString(r.Req) {
		return // nothing to answer: there is no request id to correlate
	}
	j := fetchJob{peer: peer, req: r}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.inflightPeer[peer] >= maxInflightPerPeer || s.inflightTotal >= maxInflightTotal {
		// A flooding peer gets a bounded number of queued answers, so it cannot
		// fill the shared queue and crowd out other peers' requests.
		if s.rejects[peer] >= maxRejectsPerPeer {
			s.mu.Unlock()
			return
		}
		s.rejects[peer]++
		j.reject = CodeRateLimited
	} else {
		s.inflightPeer[peer]++
		s.inflightTotal++
		j.counted = true
	}
	s.mu.Unlock()
	select {
	case s.queue <- j:
	default:
		s.release(j)
	}
}

// release frees what Handle reserved for j.
func (s *FetchServer) release(j fetchJob) {
	switch {
	case j.counted:
		s.releasePeer(j.peer)
	case j.reject != "":
		s.mu.Lock()
		if s.rejects[j.peer]--; s.rejects[j.peer] <= 0 {
			delete(s.rejects, j.peer)
		}
		s.mu.Unlock()
	}
}

func (s *FetchServer) releasePeer(peer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflightPeer[peer]--; s.inflightPeer[peer] <= 0 {
		delete(s.inflightPeer, peer)
	}
	s.inflightTotal--
}

func (s *FetchServer) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case j := <-s.queue:
			s.run(j)
		}
	}
}

func (s *FetchServer) flusher() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.Flush(context.Background())
		}
	}
}

// Flush emits the audit summaries of finished windows and prunes idle state.
func (s *FetchServer) Flush(ctx context.Context) {
	now := s.now()
	s.aud.flush(ctx, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer, reqs := range s.seen {
		for id, t := range reqs {
			if now.Sub(t) > reqRemember {
				delete(reqs, id)
			}
		}
		if len(reqs) == 0 {
			delete(s.seen, peer)
		}
	}
	for id, w := range s.rate {
		if now.Sub(w.start) > time.Minute && s.inflightGrant[id] == 0 {
			delete(s.rate, id)
		}
	}
	for id, w := range s.served {
		if now.Sub(w.start) > 24*time.Hour {
			delete(s.served, id)
		}
	}
}

// respond sends one fetch.resp plaintext.
func (s *FetchServer) respond(ctx context.Context, peer string, v any) error {
	pt, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.cfg.Send(ctx, peer, pt)
}

func (s *FetchServer) respondErr(ctx context.Context, peer, req, code string) {
	_ = s.respond(ctx, peer, respErr{Type: TypeFetchResp, Req: req, Error: code})
}

// run serves one queued request.
func (s *FetchServer) run(j fetchJob) {
	ctx, cancel := context.WithTimeout(context.Background(), opBudget)
	defer cancel()
	defer s.release(j)
	if j.reject != "" {
		s.respondErr(ctx, j.peer, j.req.Req, j.reject)
		return
	}
	rec, bytes, err := s.serve(ctx, j)
	if err != nil {
		code := FetchCode(err)
		s.respondErr(ctx, j.peer, j.req.Req, code)
		s.auditOp(ctx, rec, j, bytes, code)
		return
	}
	s.auditOp(ctx, rec, j, bytes, "ok")
}

func (s *FetchServer) auditOp(ctx context.Context, rec *Record, j fetchJob, bytes int64, result string) {
	grant := ""
	if rec != nil {
		grant = rec.ID
	}
	// op is peer-supplied: only the enum reaches the audit log (review 34 M2).
	op := j.req.Op
	switch op {
	case OpStat, OpList, OpRead:
	default:
		op = "unknown"
	}
	s.aud.record(ctx, s.now(), grant, j.peer, op, bytes, result)
}

// serve runs the checks and the operation. rec is non-nil once the grant row
// was found and matched (so the audit row names a real grant).
func (s *FetchServer) serve(ctx context.Context, j fetchJob) (*Record, int64, error) {
	r := j.req
	// Shape: cheap, stateless.
	ts, err := time.Parse(time.RFC3339, r.TS)
	if err != nil {
		return nil, 0, fetchErr(ReasonMalformed)
	}
	switch r.Op {
	case OpStat:
		if r.Path == nil {
			return nil, 0, fetchErr(ReasonMalformed)
		}
	case OpList:
		if !utf8.ValidString(r.Cursor) || len(r.Cursor) > 1024 {
			return nil, 0, fetchErr(ReasonMalformed)
		}
	case OpRead:
		if r.Path == nil || r.Offset == nil || r.Length == nil ||
			*r.Offset < 0 || *r.Length < 1 || *r.Length > MaxReadBytes {
			return nil, 0, fetchErr(ReasonMalformed)
		}
	default:
		return nil, 0, fetchErr(ReasonMalformed)
	}
	if len(r.Token) == 0 {
		return nil, 0, fetchErr(ReasonMalformed)
	}

	// ts window.
	now := s.now()
	if ts.Before(now.Add(-tsPast)) || ts.After(now.Add(tsFuture)) {
		return nil, 0, fetchErr(CodeStale)
	}

	// Steps 1-8, then 9: the grants row.
	rec, g, err := s.authorize(ctx, j.peer, r.Token, now)
	if err != nil {
		return rec, 0, err
	}
	// req dedupe, only once the peer is known to hold a valid grant, so a peer
	// without one cannot fill the store (review 34 M1).
	if !s.markSeen(j.peer, r.Req, now) {
		return rec, 0, fetchErr(CodeStale)
	}
	release, err := s.admit(rec.ID, now)
	if err != nil {
		return rec, 0, err
	}
	defer release()

	kind := KindFS
	if g.Action == ActionGitRead {
		kind = KindGit
	}
	be := s.cfg.Backends[kind]
	if be == nil {
		return rec, 0, fetchErr(CodeUnsupported)
	}

	// Step 10: the effective path is always scope + path, so it cannot leave
	// the scope; the grammar decides the rest.
	path := ""
	if r.Path != nil {
		path = *r.Path
	}
	if !ValidScopePath(path) || (path == "" && r.Op != OpList) {
		return rec, 0, fetchErr(CodeBadPath)
	}
	rel := joinScope(rec.Scope, path)

	switch r.Op {
	case OpStat:
		e, commit, err := be.Stat(ctx, *rec, rel)
		if err != nil {
			return rec, 0, err
		}
		return rec, 0, wrapSend(s.respond(ctx, j.peer, respStat{Type: TypeFetchResp, Req: r.Req, OK: true, Entry: e, Commit: commit}))
	case OpList:
		entries, next, commit, err := be.List(ctx, *rec, rel, r.Cursor)
		if err != nil {
			return rec, 0, err
		}
		if entries == nil {
			entries = []Entry{}
		}
		return rec, 0, wrapSend(s.respond(ctx, j.peer, respList{Type: TypeFetchResp, Req: r.Req, OK: true, Entries: entries, Cursor: next, Commit: commit}))
	}

	// read: the grantor checks its own row before every fragment.
	var sent int64
	err = be.Read(ctx, *rec, rel, *r.Offset, int(*r.Length), func(f Fragment) error {
		if err := s.recheck(ctx, *rec, j.peer); err != nil {
			return err
		}
		if err := s.respond(ctx, j.peer, respRead{
			Type: TypeFetchResp, Req: r.Req, OK: true, Frag: f.Index, Frags: f.Count, Size: f.Size,
			Commit: f.Commit, Data: base64.StdEncoding.EncodeToString(f.Data),
		}); err != nil {
			return fetchErr(CodeIO)
		}
		sent += int64(len(f.Data))
		s.addServed(rec.ID, int64(len(f.Data)), now)
		return nil
	})
	return rec, sent, err
}

func wrapSend(err error) error {
	if err != nil {
		return fetchErr(CodeIO)
	}
	return nil
}

func joinScope(scope, path string) string {
	switch {
	case scope == "":
		return path
	case path == "":
		return scope
	}
	return scope + "/" + path
}

// markSeen records peer's req and reports false if it was seen inside the
// window. Each peer has its own bounded store, so a flood refuses only the
// flooding peer's requests.
func (s *FetchServer) markSeen(peer, req string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	reqs := s.seen[peer]
	if reqs == nil {
		reqs = map[string]time.Time{}
		s.seen[peer] = reqs
	}
	if t, ok := reqs[req]; ok && now.Sub(t) <= reqRemember {
		return false
	}
	if len(reqs) >= maxSeenPerPeer {
		for k, t := range reqs {
			if now.Sub(t) > reqRemember {
				delete(reqs, k)
			}
		}
		if len(reqs) >= maxSeenPerPeer {
			return false // flooded: refuse rather than forget a request
		}
	}
	reqs[req] = now
	return true
}

// authorize runs Verify steps 1-8 and the grantor's step 9.
func (s *FetchServer) authorize(ctx context.Context, peer string, token json.RawMessage, now time.Time) (*Record, *Grant, error) {
	open := func(id, requester, worker string) (bool, bool) {
		if s.cfg.SessionOpen == nil {
			return false, false
		}
		return s.cfg.SessionOpen(ctx, id, requester, worker)
	}
	g, err := Verify(token, VerifyParams{
		Role: RoleGrantor, Self: s.cfg.Self, Counterparty: peer, Now: now, SessionOpen: open,
	})
	if err != nil {
		return nil, nil, err
	}
	var w struct {
		Sig string `json:"sig"`
	}
	if json.Unmarshal(token, &w) != nil {
		return nil, nil, fetchErr(ReasonMalformed)
	}
	wire, err := Canonical(Token{Grant: *g, Sig: w.Sig})
	if err != nil {
		return nil, nil, fetchErr(ReasonMalformed)
	}
	rec, err := s.cfg.Store.Get(ctx, g.ID)
	if errors.Is(err, ErrUnknownGrant) {
		return nil, nil, fetchErr(CodeUnknownGrnt)
	}
	if err != nil {
		return nil, nil, fetchErr(CodeIO)
	}
	if rec.Direction != DirectionIssued || rec.Peer != peer || rec.Token != string(wire) {
		return nil, nil, fetchErr(CodeUnknownGrnt)
	}
	switch rec.State {
	case StateRevoked:
		return &rec, g, fetchErr(CodeRevoked)
	case StateActive:
	default:
		return nil, nil, fetchErr(CodeUnknownGrnt)
	}
	return &rec, g, nil
}

// recheck is step 9 (and 6, 7) again, before each fragment: a revoke, an
// expiry or the end of the session stops a read in progress.
func (s *FetchServer) recheck(ctx context.Context, rec Record, peer string) error {
	cur, err := s.cfg.Store.Get(ctx, rec.ID)
	if err != nil {
		return fetchErr(CodeIO)
	}
	if cur.State == StateRevoked {
		return fetchErr(CodeRevoked)
	}
	if cur.State != StateActive || cur.Token != rec.Token {
		return fetchErr(CodeUnknownGrnt)
	}
	if !s.now().Before(rec.Exp) {
		return fetchErr(ReasonExpired)
	}
	if s.cfg.SessionOpen != nil {
		known, open := s.cfg.SessionOpen(ctx, rec.Session, s.cfg.Self, peer)
		if !known {
			return fetchErr(ReasonUnknownSession)
		}
		if !open {
			return fetchErr(ReasonSessionNotOpen)
		}
	}
	return nil
}

// admit applies the per-grant limits: 2 in flight, 20 operations per second,
// and the bytes served per 24 h. It returns the release func.
func (s *FetchServer) admit(grant string, now time.Time) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflightGrant[grant] >= maxInflightPerGrant {
		return nil, fetchErr(CodeRateLimited)
	}
	w := s.rate[grant]
	if w == nil || now.Sub(w.start) >= time.Second {
		w = &opWindow{start: now}
		s.rate[grant] = w
	}
	if w.n >= maxOpsPerSecond {
		return nil, fetchErr(CodeRateLimited)
	}
	if b := s.served[grant]; b != nil && now.Sub(b.start) < 24*time.Hour && b.n >= s.cfg.BytesPer24h {
		return nil, fetchErr(CodeRateLimited)
	}
	w.n++
	s.inflightGrant[grant]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.inflightGrant[grant]--; s.inflightGrant[grant] <= 0 {
			delete(s.inflightGrant, grant)
		}
	}, nil
}

func (s *FetchServer) addServed(grant string, n int64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.served[grant]
	if b == nil || now.Sub(b.start) >= 24*time.Hour {
		b = &byteWindow{start: now}
		s.served[grant] = b
	}
	b.n += n
}

// ---- audit -----------------------------------------------------------

// fetchAudit writes grant.fetch rows, at most auditRowsPerMinute per grant per
// minute, and summarises the rest once a minute (grant.fetch_summary).
type fetchAudit struct {
	emit func(ctx context.Context, action string, detail map[string]any)
	mu   sync.Mutex
	wins map[string]*auditWindow
}

type auditWindow struct {
	start          time.Time
	rows           int
	grant, peer    string
	ops, bytes, er int64 // suppressed since start
}

func newFetchAudit(emit func(ctx context.Context, action string, detail map[string]any)) *fetchAudit {
	return &fetchAudit{emit: emit, wins: map[string]*auditWindow{}}
}

func (a *fetchAudit) record(ctx context.Context, now time.Time, grant, peer, op string, bytes int64, result string) {
	if a.emit == nil {
		return
	}
	key := grant
	if key == "" {
		key = "peer:" + peer
	}
	var summary map[string]any
	logRow := false
	a.mu.Lock()
	w := a.wins[key]
	if w == nil {
		w = &auditWindow{start: now, grant: grant, peer: peer}
		a.wins[key] = w
	}
	if now.Sub(w.start) >= time.Minute {
		summary = w.takeSummary()
		w.start, w.rows = now, 0
	}
	if w.rows < auditRowsPerMinute {
		w.rows++
		logRow = true
	} else {
		w.ops++
		w.bytes += bytes
		if result != "ok" {
			w.er++
		}
	}
	a.mu.Unlock()
	if summary != nil {
		a.emit(ctx, "grant.fetch_summary", summary)
	}
	if logRow {
		detail := map[string]any{"peer": peer, "op": op, "bytes": bytes, "result": result}
		if grant != "" {
			detail["grant"] = grant
		}
		a.emit(ctx, "grant.fetch", detail)
	}
}

func (w *auditWindow) takeSummary() map[string]any {
	if w.ops == 0 {
		return nil
	}
	d := map[string]any{"ops": w.ops, "bytes": w.bytes, "errors": w.er, "peer": w.peer}
	if w.grant != "" {
		d["grant"] = w.grant
	}
	w.ops, w.bytes, w.er = 0, 0, 0
	return d
}

// flush emits the summaries of windows older than a minute and drops idle ones.
func (a *fetchAudit) flush(ctx context.Context, now time.Time) {
	if a.emit == nil {
		return
	}
	var out []map[string]any
	a.mu.Lock()
	for k, w := range a.wins {
		if now.Sub(w.start) < time.Minute {
			continue
		}
		if s := w.takeSummary(); s != nil {
			out = append(out, s)
		}
		delete(a.wins, k)
	}
	a.mu.Unlock()
	for _, s := range out {
		a.emit(ctx, "grant.fetch_summary", s)
	}
}
