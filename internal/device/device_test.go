package device

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	return &Store{DB: st.DB(), Self: "self-key", Now: clk.Now}, clk
}

func inTx(t *testing.T, s *Store, f func(tx *sql.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	f(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func keyFromSeedByte(first byte) string {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = first + byte(i)
	}
	return envelope.KeyString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
}

// TestLinkIDVector is the link-id vector of Docs/protocol/device.md §Kinds.
func TestLinkIDVector(t *testing.T) {
	controller := keyFromSeedByte(0x00)
	helper := keyFromSeedByte(0x20)
	got := LinkID(controller, helper, "00112233445566778899aabbccddeeff", "ffeeddccbbaa99887766554433221100")
	const want = "l-ee417e25d0625afad79d54cd3539dd64"
	if got != want {
		t.Fatalf("link id = %s, want %s", got, want)
	}
	if !ValidLinkID(got) || ValidLinkID("i-"+got[2:]) || ValidLinkID(got[:10]) {
		t.Error("ValidLinkID wrong")
	}
}

func TestNonceShape(t *testing.T) {
	n := NewNonce()
	if !ValidNonce(n) || n == NewNonce() {
		t.Fatalf("nonce %q not 32 random hex characters", n)
	}
	for _, bad := range []string{"", "AB", n + "0", "zz" + n[2:], "00112233445566778899AABBCCDDEEFF"} {
		if ValidNonce(bad) {
			t.Errorf("ValidNonce(%q) = true", bad)
		}
	}
}

// waitingIntent stores an approved intent for peer in role.
func waitingIntent(t *testing.T, s *Store, peer, role string) Link {
	t.Helper()
	ctx := context.Background()
	l, err := s.CreateIntent(ctx, peer, role)
	if err != nil {
		t.Fatal(err)
	}
	inTx(t, s, func(tx *sql.Tx) {
		if l, err = s.ApproveTx(ctx, tx, l.ID, s.Time()); err != nil {
			t.Fatal(err)
		}
	})
	return l
}

func offerFrom(s *Store, peer, role string) Offer {
	o := Offer{At: s.Time(), Nonce: NewNonce(), Role: role}
	if role == RoleController {
		o.Controller, o.Helper = peer, s.Self
	} else {
		o.Controller, o.Helper = s.Self, peer
	}
	return o
}

func tryActivate(t *testing.T, s *Store, peer string, o Offer) (Link, bool) {
	t.Helper()
	var l Link
	var ok bool
	inTx(t, s, func(tx *sql.Tx) {
		var err error
		if l, ok, err = s.TryActivateTx(context.Background(), tx, peer, o, s.Time()); err != nil {
			t.Fatal(err)
		}
	})
	return l, ok
}

func TestActivateNeedsComplementaryUnexpiredIntent(t *testing.T) {
	s, clk := newTestStore(t)
	// No intent: nothing.
	if _, ok := tryActivate(t, s, "p1", offerFrom(s, "p1", RoleController)); ok {
		t.Fatal("an offer without an intent activated")
	}
	// The same role as the offer: not complementary.
	waitingIntent(t, s, "p2", RoleController)
	if _, ok := tryActivate(t, s, "p2", offerFrom(s, "p2", RoleController)); ok {
		t.Fatal("an offer with the same role activated")
	}
	// Complementary and unexpired: active, with the same id the peer computes.
	l := waitingIntent(t, s, "p3", RoleController)
	o := offerFrom(s, "p3", RoleHelper)
	act, ok := tryActivate(t, s, "p3", o)
	if !ok || act.State != StateActive {
		t.Fatalf("activation = %+v %v", act, ok)
	}
	if want := LinkID(s.Self, "p3", l.Nonce, o.Nonce); act.ID != want {
		t.Errorf("link id = %s, want %s", act.ID, want)
	}
	if act.ActivatedAt != clk.Now() || act.PeerNonce != o.Nonce || !act.Expires.IsZero() {
		t.Errorf("active row = %+v", act)
	}
	// An expired intent does not activate.
	waitingIntent(t, s, "p4", RoleController)
	clk.Advance(IntentTTL + time.Second)
	if _, ok := tryActivate(t, s, "p4", offerFrom(s, "p4", RoleHelper)); ok {
		t.Fatal("an expired intent activated")
	}
}

func TestActivateRefusesOldOffer(t *testing.T) {
	s, _ := newTestStore(t)
	waitingIntent(t, s, "p", RoleHelper)
	o := offerFrom(s, "p", RoleController)
	o.At = s.Time().Add(-IntentTTL - time.Second) // older than intent.created - 10 min
	if _, ok := tryActivate(t, s, "p", o); ok {
		t.Fatal("an offer older than created-10min activated")
	}
	o.At = s.Time().Add(-IntentTTL + time.Second)
	if _, ok := tryActivate(t, s, "p", o); !ok {
		t.Fatal("an offer just inside created-10min did not activate")
	}
}

func TestHierarchy(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	check := func(peer, role string) error {
		var err error
		inTx(t, s, func(tx *sql.Tx) { err = s.CheckHierarchyTx(ctx, tx, peer, role, "") })
		return err
	}
	l := waitingIntent(t, s, "b", RoleController) // this device controls b
	if err := check("b", RoleHelper); !errors.Is(err, ErrCycle) {
		t.Errorf("reverse link: %v, want ErrCycle", err)
	}
	if err := check("b", RoleController); !errors.Is(err, ErrAlreadyLinked) {
		t.Errorf("same link twice: %v, want ErrAlreadyLinked", err)
	}
	if err := check("c", RoleHelper); !errors.Is(err, ErrCycle) {
		t.Errorf("a controller becoming a helper: %v, want ErrCycle", err)
	}
	if err := check("c", RoleController); err != nil {
		t.Errorf("a second helper: %v", err)
	}
	// The row itself is excluded when re-checking it.
	var err error
	inTx(t, s, func(tx *sql.Tx) { err = s.CheckHierarchyTx(ctx, tx, "b", RoleController, l.ID) })
	if err != nil {
		t.Errorf("re-check excluding own row: %v", err)
	}
	// At most 8 helpers.
	for i := 1; i < MaxHelpers; i++ {
		waitingIntent(t, s, "h"+string(rune('0'+i)), RoleController)
	}
	if err := check("extra", RoleController); !errors.Is(err, ErrLimit) {
		t.Errorf("ninth helper: %v, want ErrLimit", err)
	}

	// A helper cannot control, and has one controller.
	s2, _ := newTestStore(t)
	waitingIntent(t, s2, "boss", RoleHelper)
	inTx(t, s2, func(tx *sql.Tx) { err = s2.CheckHierarchyTx(ctx, tx, "x", RoleController, "") })
	if !errors.Is(err, ErrCycle) {
		t.Errorf("a helper becoming a controller: %v, want ErrCycle", err)
	}
	inTx(t, s2, func(tx *sql.Tx) { err = s2.CheckHierarchyTx(ctx, tx, "boss2", RoleHelper, "") })
	if !errors.Is(err, ErrLimit) {
		t.Errorf("a second controller: %v, want ErrLimit", err)
	}
}

func TestLapsedIntentsFreeThePeer(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	// A pending_approval row that was never approved lapses after 10 minutes.
	first, err := s.CreateIntent(ctx, "p", RoleHelper)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIntent(ctx, "p", RoleHelper); err != nil {
		t.Fatalf("a retry replaces the unapproved attempt: %v", err)
	}
	links, err := s.List(ctx)
	if err != nil || len(links) != 2 {
		t.Fatalf("list = %d links (%v), want 2 (the replaced one revoked)", len(links), err)
	}
	for _, l := range links {
		if l.ID == first.ID && l.State != StateRevoked {
			t.Errorf("replaced attempt is %s, want revoked", l.State)
		}
	}
	// A waiting intent lapses; the kept offer is dropped; a new link is allowed.
	clk.Advance(IntentTTL + time.Second)
	links, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.State != StateRevoked {
			t.Errorf("after 10 min %s is %s, want revoked", l.ID, l.State)
		}
	}
	if _, err := s.CreateIntent(ctx, "p", RoleController); err != nil {
		t.Fatalf("a lapsed attempt must not block a new one (even in the other role): %v", err)
	}
}

func TestOfferKeptOncePerPeerAndExpires(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	inTx(t, s, func(tx *sql.Tx) {
		if err := s.OfferPutTx(ctx, tx, "p", `{"a":1}`, clk.Now()); err != nil {
			t.Fatal(err)
		}
		if err := s.OfferPutTx(ctx, tx, "p", `{"a":2}`, clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM device_offers`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("offers = %d (%v), want 1 per peer", n, err)
	}
	inTx(t, s, func(tx *sql.Tx) {
		body, ok, err := s.OfferGetTx(ctx, tx, "p", clk.Now())
		if err != nil || !ok || body != `{"a":2}` {
			t.Fatalf("offer = %q %v %v", body, ok, err)
		}
	})
	clk.Advance(IntentTTL + time.Second)
	inTx(t, s, func(tx *sql.Tx) {
		if _, ok, _ := s.OfferGetTx(ctx, tx, "p", clk.Now()); ok {
			t.Error("an offer older than 10 min is still returned")
		}
		if err := s.ExpireTx(ctx, tx, clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM device_offers`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("offers after expiry = %d (%v), want 0", n, err)
	}
}

func TestRevokeForPeerIsPeerScopedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	l := waitingIntent(t, s, "p", RoleController)
	act, ok := tryActivate(t, s, "p", offerFrom(s, "p", RoleHelper))
	if !ok {
		t.Fatal("no activation")
	}
	other := waitingIntent(t, s, "q", RoleController)
	if _, err := s.DB.Exec(`INSERT INTO device_scopes (link, scope, expires, approval, created) VALUES (?, '{}', 'x', 'a', 'x'), (?, '{}', 'x', 'a', 'x')`, act.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	_ = l
	var revoked []Link
	inTx(t, s, func(tx *sql.Tx) {
		var err error
		if revoked, err = s.RevokeForPeerTx(ctx, tx, "p", clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if len(revoked) != 1 || revoked[0].ID != act.ID || revoked[0].State != StateActive {
		t.Fatalf("revoked = %+v, want the one active link (as it was)", revoked)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM device_scopes WHERE link = ?`, act.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("scope of the revoked link remains (%d, %v)", n, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM device_scopes WHERE link = ?`, other.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("scope of another peer's link was touched (%d, %v)", n, err)
	}
	var state string
	if err := s.DB.QueryRow(`SELECT state FROM device_links WHERE peer = 'q'`).Scan(&state); err != nil || state != StateWaiting {
		t.Errorf("another peer's link is %s (%v), want waiting", state, err)
	}
	inTx(t, s, func(tx *sql.Tx) {
		var err error
		if revoked, err = s.RevokeForPeerTx(ctx, tx, "p", clk.Now()); err != nil || len(revoked) != 0 {
			t.Fatalf("second revoke = %+v %v, want nothing", revoked, err)
		}
	})
}
