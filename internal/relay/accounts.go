package relay

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Accounts modes of Options.Accounts (Docs/protocol/accounts.md).
const (
	AccountsOff    = "off"
	AccountsGitHub = "github"
	AccountsEmail  = "email"
	AccountsBoth   = "both"
)

const (
	defaultBindPollInterval = 5 * time.Second
	// maxUnboundPerPrefix is how many unbound connections one network
	// prefix may keep open; past it the oldest is closed (review 50 M5).
	maxUnboundPerPrefix = 16
	// accountsWatchEvery is how often the relay looks for account changes
	// another process (relay admin) committed to the database. Unbind and
	// suspension close live connections within 1 s, so it is well below that.
	accountsWatchEvery = 250 * time.Millisecond
	// revokeFlush bounds how long a revoked or suspended connection's
	// buffer may take to deliver the error frame before it is closed.
	revokeFlush = 300 * time.Millisecond
	// Per-account pairing limits (Docs/protocol/relay-hosted.md §2 L5).
	maxPairNewPerAccountDay  = 30
	maxOutstandingPerAccount = 20
	pairAccountWindow        = 24 * time.Hour
	maxBindRefLen            = 64
)

// accounts is the live account state of a relay with accounts: a cache of
// every binding, reloaded after each change this relay makes and whenever
// another connection (relay admin) commits to the database.
type accounts struct {
	store     *accountStore
	loginURL  string
	interval  time.Duration
	providers map[string]bool

	mu   sync.RWMutex
	keys map[string]binding

	// reconcileMu serialises reload-and-reconcile passes.
	reconcileMu sync.Mutex

	// pollMu guards lastPoll: when each bind ref last got a bind_pending,
	// for the bind_poll interval.
	pollMu   sync.Mutex
	lastPoll map[string]time.Time

	// version is the last data_version the watcher acted on (watcher only).
	version    int64
	stop, done chan struct{}
}

func newAccounts(opts Options, q *queue, journal *JournalWriter, now func() time.Time) (*accounts, error) {
	providers := map[string]bool{}
	switch opts.Accounts {
	case AccountsGitHub:
		providers["github"] = true
	case AccountsEmail:
		providers["email"] = true
	case AccountsBoth:
		providers["github"], providers["email"] = true, true
	default:
		return nil, fmt.Errorf("accounts mode %q: want off, github, email or both", opts.Accounts)
	}
	a := &accounts{
		store:     &accountStore{db: q.db, now: now, journal: journal},
		loginURL:  opts.LoginURL,
		interval:  opts.BindPollInterval,
		providers: providers,
		lastPoll:  map[string]time.Time{},
	}
	if a.interval <= 0 {
		a.interval = defaultBindPollInterval
	}
	// The watcher's baseline is taken before the first load, so a commit by
	// relay admin between the two is seen as a change, never missed.
	v, err := a.dataVersion()
	if err != nil {
		return nil, fmt.Errorf("read database version: %w", err)
	}
	a.version = v
	if err := a.reload(); err != nil {
		return nil, err
	}
	return a, nil
}

// dataVersion is SQLite's PRAGMA data_version on the relay's connection: it
// changes whenever another connection has committed.
func (a *accounts) dataVersion() (int64, error) {
	ctx, cancel := opCtx()
	defer cancel()
	var v int64
	err := a.store.db.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v)
	return v, err
}

func (a *accounts) reload() error {
	keys, err := a.store.loadBindings()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.keys = keys
	a.mu.Unlock()
	return nil
}

// state returns key's binding, and false if the key is unbound.
func (a *accounts) state(key string) (binding, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	b, ok := a.keys[key]
	return b, ok
}

// info is key's account state as ready and bind_done carry it.
func (a *accounts) info(key string) *envelope.Account {
	b, ok := a.state(key)
	switch {
	case !ok:
		return &envelope.Account{State: envelope.AccountUnbound}
	case b.suspended:
		return &envelope.Account{State: envelope.AccountSuspended, ID: b.account}
	default:
		return &envelope.Account{State: envelope.AccountBound, ID: b.account, Display: b.display, Group: b.group}
	}
}

// eligible reports whether key may send or receive an envelope of type typ
// (Docs/protocol/accounts.md "Who may send to whom"): bound to an active
// account with a quota group, or only bound for pair.confirm.
func (a *accounts) eligible(key, typ string) bool {
	b, ok := a.state(key)
	return ok && !b.suspended && (b.group != "" || typ == envelope.TypePairConfirm)
}

// watch reloads the bindings whenever another connection has committed to
// the database (PRAGMA data_version changes), until stop is closed.
func (s *Server) watchAccounts() {
	a := s.acct
	defer close(a.done)
	t := time.NewTicker(accountsWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
		}
		v, err := a.dataVersion()
		if err != nil || v == a.version {
			continue
		}
		a.version = v
		s.accountsChanged()
	}
}

// accountsChanged reloads the bindings and brings every live connection in
// line: a key that lost its binding (or now belongs to another account) gets
// account_revoked, a suspended one account_suspended, each followed by close
// 1008; an unbound connection that is now bound starts receiving its queue.
func (s *Server) accountsChanged() {
	a := s.acct
	a.reconcileMu.Lock()
	defer a.reconcileMu.Unlock()
	if err := a.reload(); err != nil {
		s.log.Warn("accounts reload failed", "event", "accounts_error", "error", err)
		return
	}
	type closing struct {
		c         *conn
		code, msg string
	}
	var closes []closing
	var bound []*conn
	s.mu.Lock()
	for _, c := range s.conns {
		if !c.acctInit || c.acctClosing {
			continue
		}
		b, ok := a.state(c.key)
		switch {
		case ok && b.suspended:
			c.acctClosing = true
			closes = append(closes, closing{c, envelope.CodeAccountSuspended, "the account is suspended"})
		case c.acctSeen != "" && (!ok || b.account != c.acctSeen):
			c.acctClosing = true
			closes = append(closes, closing{c, envelope.CodeAccountRevoked, "this key was unbound from its account"})
		case c.acctSeen == "" && ok:
			c.acctSeen = b.account
			s.unlistUnbound(c)
			bound = append(bound, c)
		}
	}
	s.mu.Unlock()
	for _, x := range closes {
		s.log.Info("account closed connection", "event", "account_close", "reason", x.code, "peer", short(x.c.key))
		s.reject(x.c, x.code, x.msg, "")
		go x.c.flushThenClose(websocket.StatusPolicyViolation, x.code, revokeFlush)
	}
	for _, c := range bound {
		s.startDrain(c)
	}
}

// listUnbound records c as an unbound connection of its prefix and returns
// the oldest one to close if the prefix is over maxUnboundPerPrefix. Callers
// hold s.mu.
func (s *Server) listUnbound(c *conn) (evict *conn) {
	list := append(s.unbound[c.prefix], c)
	c.unboundListed = true
	if len(list) > maxUnboundPerPrefix {
		evict, list = list[0], list[1:]
		evict.unboundListed = false
	}
	s.unbound[c.prefix] = list
	return evict
}

// unlistUnbound removes c from its prefix's unbound connections. Callers hold s.mu.
func (s *Server) unlistUnbound(c *conn) {
	if !c.unboundListed {
		return
	}
	c.unboundListed = false
	list := s.unbound[c.prefix]
	for i, x := range list {
		if x == c {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(s.unbound, c.prefix)
	} else {
		s.unbound[c.prefix] = list
	}
}

// accountAllows applies the account state table of Docs/protocol/accounts.md
// to a control frame from c. It replies account_required (or
// account_suspended) and returns false if the frame is not allowed.
func (s *Server) accountAllows(c *conn, op, ref string) bool {
	switch op {
	case envelope.OpBindStart, envelope.OpBindPoll, envelope.OpBindCancel:
		return true
	}
	b, ok := s.acct.state(c.key)
	switch {
	case ok && b.suspended:
		s.reject(c, envelope.CodeAccountSuspended, "the account is suspended", ref)
		return false
	case ok && (b.group != "" || op == envelope.OpAck || op == envelope.OpPairRedeem || op == envelope.OpUnbind):
		return true
	case ok:
		s.reject(c, envelope.CodeAccountRequired, "this needs an account in a quota group; redeem an invite or join a team", ref)
	default:
		s.reject(c, envelope.CodeAccountRequired, "this relay needs a bound account; run agentnet login", ref)
	}
	return false
}

// accountRoutes applies "Who may send to whom" to an envelope from sender.
// A sender that may not send gets account_required; for a recipient that
// may not receive, a non-ephemeral envelope gets account_required (and is
// not queued) while an ephemeral one is dropped silently like any other
// undeliverable ephemeral envelope.
func (s *Server) accountRoutes(sender *conn, h envelope.Header) bool {
	if s.acct == nil {
		return true
	}
	msg := ""
	switch {
	case !s.acct.eligible(h.From, h.Type):
		msg = "this key needs a bound account in a quota group to send this"
	case !s.acct.eligible(h.To, h.Type):
		if envelope.IsEphemeral(h.Type) {
			return false
		}
		msg = "the recipient has no bound account that may receive this"
	default:
		return true
	}
	s.log.Info("dropped", "event", "drop", "reason", envelope.CodeAccountRequired, "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID)
	s.reject(sender, envelope.CodeAccountRequired, msg, h.ID)
	return false
}

// handleAccountOp serves bind_start, bind_poll, bind_cancel and unbind.
func (s *Server) handleAccountOp(c *conn, ctl *envelope.Control) {
	switch ctl.Op {
	case envelope.OpBindStart:
		s.bindStart(c, ctl)
	case envelope.OpBindPoll:
		s.bindPoll(c, ctl)
	case envelope.OpBindCancel:
		if ctl.Ref != "" && len(ctl.Ref) <= maxBindRefLen {
			if err := s.acct.store.cancelBind(ctl.Ref, c.key); err != nil {
				s.log.Warn("accounts failed", "event", "accounts_error", "op", "bind_cancel", "error", err)
			}
			s.acct.forgetPoll(ctl.Ref)
		}
	case envelope.OpUnbind:
		s.unbindSelf(c)
	}
}

func (s *Server) bindStart(c *conn, ctl *envelope.Control) {
	a := s.acct
	if _, ok := a.state(c.key); ok {
		s.reject(c, envelope.CodeAlreadyBound, "this key is already bound; run agentnet logout first", ctl.Ref)
		return
	}
	if !envelope.ValidDevice(ctl.Device) || !envelope.ValidOS(ctl.OS) {
		s.reject(c, envelope.CodeBadEnvelope, "bind_start needs a device label (1-32 of A-Z a-z 0-9 space . _ -) and an os", ctl.Ref)
		return
	}
	ref, code, expires, err := a.store.createBind(c.key, ctl.Device, ctl.OS)
	if err != nil {
		s.log.Warn("accounts failed", "event", "accounts_error", "op", "bind_start", "error", err)
		s.reject(c, envelope.CodeInternal, "could not start the bind", ctl.Ref)
		return
	}
	a.notePoll(ref, s.now())
	s.log.Info("bind started", "event", "bind_start", "peer", short(c.key))
	c.send(control(envelope.Control{Op: envelope.OpBindPending, Ref: ref, UserCode: envelope.FormatBindCode(code),
		URL: a.loginURL, Expires: expires.UTC().Format(time.RFC3339), Interval: a.intervalSeconds()}))
}

func (s *Server) bindPoll(c *conn, ctl *envelope.Control) {
	a := s.acct
	ref := ctl.Ref
	if ref == "" || len(ref) > maxBindRefLen {
		s.reject(c, envelope.CodeBindExpired, "unknown or expired bind", "")
		return
	}
	now := s.now()
	if !a.pollAllowed(ref, now) {
		s.reject(c, envelope.CodeRateLimited, "polling faster than the interval", ref)
		return
	}
	state, expires, err := a.store.bindStatus(ref, c.key)
	if err != nil {
		s.log.Warn("accounts failed", "event", "accounts_error", "op", "bind_poll", "error", err)
		s.reject(c, envelope.CodeInternal, "could not read the bind", ref)
		return
	}
	switch state {
	case "pending":
		c.send(control(envelope.Control{Op: envelope.OpBindPending, Ref: ref, URL: a.loginURL,
			Expires: expires.UTC().Format(time.RFC3339), Interval: a.intervalSeconds()}))
		return
	case "confirmed":
		if info := a.info(c.key); info.State != envelope.AccountUnbound {
			a.forgetPoll(ref)
			c.send(control(envelope.Control{Op: envelope.OpBindDone, Ref: ref, Account: info}))
			return
		}
	case "denied":
		a.forgetPoll(ref)
		s.reject(c, envelope.CodeBindDenied, "the bind was denied in the browser", ref)
		return
	}
	a.forgetPoll(ref)
	s.reject(c, envelope.CodeBindExpired, "unknown or expired bind", ref)
}

// unbindSelf serves unbind (agentnet logout): the sender's own key only.
func (s *Server) unbindSelf(c *conn) {
	if err := s.acct.store.unbindKey(c.key); err != nil && !errors.Is(err, ErrNoAccount) {
		s.log.Warn("accounts failed", "event", "accounts_error", "op", "unbind", "error", err)
		s.reject(c, envelope.CodeInternal, "could not unbind", "")
		return
	}
	s.log.Info("key unbound", "event", "unbind", "peer", short(c.key))
	s.accountsChanged()
}

func (a *accounts) intervalSeconds() int {
	return max(1, int((a.interval+time.Second-1)/time.Second))
}

func (a *accounts) notePoll(ref string, now time.Time) {
	a.pollMu.Lock()
	defer a.pollMu.Unlock()
	if len(a.lastPoll) >= limiterSweepThreshold {
		for r, t := range a.lastPoll {
			if now.Sub(t) > bindTTL {
				delete(a.lastPoll, r)
			}
		}
	}
	a.lastPoll[ref] = now
}

// pollAllowed reports whether ref may be polled now (at least the interval
// since its last bind_pending), and records the poll if so.
func (a *accounts) pollAllowed(ref string, now time.Time) bool {
	a.pollMu.Lock()
	last, ok := a.lastPoll[ref]
	a.pollMu.Unlock()
	if ok && now.Sub(last) < time.Duration(a.intervalSeconds())*time.Second {
		return false
	}
	a.notePoll(ref, now)
	return true
}

func (a *accounts) forgetPoll(ref string) {
	a.pollMu.Lock()
	defer a.pollMu.Unlock()
	delete(a.lastPoll, ref)
}

// errNoAccounts is an account operation on a relay without accounts.
var errNoAccounts = errors.New("this relay does not have accounts")

// EnsureAccount returns the id of the account for (provider, subject) —
// provider "github" with the numeric user id, or "email" with the address —
// creating it on first sign-in and recording display (@login or the email).
// Used by the web login (ticket 4.2b).
func (s *Server) EnsureAccount(provider, subject, display string) (string, error) {
	if s.acct == nil {
		return "", errNoAccounts
	}
	if !s.acct.providers[provider] {
		return "", ErrProviderDisabled
	}
	return s.acct.store.ensureAccount(provider, subject, display)
}

// PendingBind returns the pending bind whose user code is code, for the
// confirm page. ErrBindInvalid for an unknown, expired or used code.
func (s *Server) PendingBind(code string) (BindRequest, error) {
	if s.acct == nil {
		return BindRequest{}, errNoAccounts
	}
	return s.acct.store.pendingByCode(code)
}

// ConfirmBind binds the key of pending bind ref to account acc (the confirm
// page's "Bind this device"). The key's live connection becomes bound at once
// without reconnecting (its next bind_poll gets bind_done), and every other
// live key of the account gets account_changed.
func (s *Server) ConfirmBind(ref, acc string) error {
	if s.acct == nil {
		return errNoAccounts
	}
	key, err := s.acct.store.confirmBind(ref, acc)
	if err != nil {
		return err
	}
	s.log.Info("key bound", "event", "bind_done", "peer", short(key), "account", acc)
	s.accountsChanged()
	var others []*conn
	s.mu.Lock()
	for k, c := range s.conns {
		if k == key {
			continue
		}
		if b, ok := s.acct.state(k); ok && b.account == acc {
			others = append(others, c)
		}
	}
	s.mu.Unlock()
	for _, c := range others {
		c.send(control(envelope.Control{Op: envelope.OpAccountChanged}))
	}
	return nil
}

// DenyBind ends pending bind ref as denied (the confirm page's "Deny").
func (s *Server) DenyBind(ref string) error {
	if s.acct == nil {
		return errNoAccounts
	}
	return s.acct.store.denyBind(ref)
}

// UnbindKey removes key's binding (account page or operator); its live
// connection gets account_revoked and close 1008.
func (s *Server) UnbindKey(key string) error {
	return s.accountOp(func(st *accountStore) error { return st.unbindKey(key) })
}

// SetAccountSuspended suspends or reactivates account acc; a suspended
// account's live connections are closed.
func (s *Server) SetAccountSuspended(acc string, suspend bool) error {
	return s.accountOp(func(st *accountStore) error { return st.setAccountState(acc, suspend) })
}

// SetGroupSuspended suspends or reactivates quota group g for every member.
func (s *Server) SetGroupSuspended(g string, suspend bool) error {
	return s.accountOp(func(st *accountStore) error { return st.setGroupState(g, suspend) })
}

// DeleteAccount deletes account acc with its bindings (Docs/protocol/accounts.md
// "Revocation"); its live connections get account_revoked.
func (s *Server) DeleteAccount(acc string) error {
	return s.accountOp(func(st *accountStore) error { return st.deleteAccount(acc) })
}

func (s *Server) accountOp(op func(*accountStore) error) error {
	if s.acct == nil {
		return errNoAccounts
	}
	err := op(s.acct.store)
	s.accountsChanged() // also after a journal error: the database did change
	return err
}

// readyFor builds c's ready frame.
func (s *Server) readyFor(c *conn) envelope.Control {
	r := envelope.Control{Op: envelope.OpReady, PublicKey: c.key, Features: []string{envelope.FeatureEphemeral}}
	if s.acct != nil {
		r.Features = append(r.Features, envelope.FeatureAccounts)
		r.Account = s.acct.info(c.key)
	}
	return r
}

// touchAccountKey records the UTC day a bound key connected (account page
// "last connected day").
func (s *Server) touchAccountKey(key string) {
	if err := s.acct.store.touchKey(key, s.now().UTC().Format(time.DateOnly)); err != nil {
		s.log.Warn("accounts failed", "event", "accounts_error", "op", "touch", "error", err)
	}
}

// LoginURLFromOrigin turns a relay origin (ws:// or wss://) into the fixed
// login page URL bind_pending carries: https:// (http:// for ws://) plus /login.
func LoginURLFromOrigin(origin string) string {
	switch {
	case strings.HasPrefix(origin, "wss://"):
		return "https://" + strings.TrimPrefix(origin, "wss://") + "/login"
	case strings.HasPrefix(origin, "ws://"):
		return "http://" + strings.TrimPrefix(origin, "ws://") + "/login"
	}
	return ""
}
