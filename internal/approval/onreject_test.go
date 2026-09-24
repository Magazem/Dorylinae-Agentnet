package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// OnReject runs exactly once on every way an approval ends without being
// approved, and never on approval (review 36 L8).
func TestOnRejectRunsOnceOnEveryNonApproval(t *testing.T) {
	ctx := context.Background()
	type tcase struct {
		name   string
		action func(*int32) Action
		end    func(t *testing.T, s *Store, n *fakeNotifier, now *time.Time, id string)
		want   int32
	}
	counting := func(calls *int32) Action {
		return Action{OnReject: func(context.Context) { atomic.AddInt32(calls, 1) }}
	}
	cases := []tcase{
		{"approved", counting, func(t *testing.T, s *Store, n *fakeNotifier, _ *time.Time, id string) {
			if _, err := s.Confirm(ctx, id, n.lastCode(t)); err != nil {
				t.Fatal(err)
			}
		}, 0},
		{"rejected by the human", counting, func(t *testing.T, s *Store, _ *fakeNotifier, _ *time.Time, id string) {
			if _, err := s.Reject(ctx, id, "ipc"); err != nil {
				t.Fatal(err)
			}
			_, _ = s.Reject(ctx, id, "ipc") // a second reject finds nothing
		}, 1},
		{"precondition failed", func(calls *int32) Action {
			a := counting(calls)
			a.Precondition = func(context.Context, *sql.Tx) error { return errors.New("gone") }
			return a
		}, func(t *testing.T, s *Store, n *fakeNotifier, _ *time.Time, id string) {
			if _, err := s.Confirm(ctx, id, n.lastCode(t)); err == nil {
				t.Fatal("confirm succeeded")
			}
		}, 1},
		{"perform failed then approved", func(calls *int32) Action {
			a := counting(calls)
			failed := false
			a.Perform = func(context.Context, *sql.Tx) (any, error) {
				if !failed {
					failed = true
					return nil, errors.New("retry")
				}
				return nil, nil
			}
			return a
		}, func(t *testing.T, s *Store, n *fakeNotifier, _ *time.Time, id string) {
			code := n.lastCode(t)
			if _, err := s.Confirm(ctx, id, code); err == nil {
				t.Fatal("first confirm succeeded")
			}
			if _, err := s.Confirm(ctx, id, code); err != nil {
				t.Fatal(err)
			}
		}, 0},
		{"three wrong codes", counting, func(_ *testing.T, s *Store, _ *fakeNotifier, _ *time.Time, id string) {
			for i := 0; i < MaxAttempts; i++ {
				_, _ = s.Confirm(ctx, id, "000000")
			}
		}, 1},
		{"expired at confirm", counting, func(t *testing.T, s *Store, n *fakeNotifier, now *time.Time, id string) {
			*now = now.Add(TTL + time.Second)
			if _, err := s.Confirm(ctx, id, n.lastCode(t)); !errors.Is(err, ErrExpired) {
				t.Fatalf("err = %v", err)
			}
		}, 1},
		{"expired by the sweep", counting, func(t *testing.T, s *Store, _ *fakeNotifier, now *time.Time, _ string) {
			*now = now.Add(TTL + time.Second)
			if _, err := s.List(ctx); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"expired by the sweep in Create", counting, func(t *testing.T, s *Store, _ *fakeNotifier, now *time.Time, _ string) {
			*now = now.Add(TTL + time.Second)
			if _, err := s.Create(ctx, KindGrant, "g-next", "s", Action{}); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"subject rejected", counting, func(_ *testing.T, s *Store, _ *fakeNotifier, _ *time.Time, _ string) {
			s.RejectSubjects(ctx, []string{"subj"})
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
			s, n, _ := newTestStore(t, clock(&now))
			var calls int32
			v, err := s.Create(ctx, KindDeviceLink, "subj", "s", tc.action(&calls))
			if err != nil {
				t.Fatal(err)
			}
			tc.end(t, s, n, &now, v.ID)
			if got := atomic.LoadInt32(&calls); got != tc.want {
				t.Fatalf("OnReject ran %d times, want %d", got, tc.want)
			}
		})
	}
}

// The lockout (the 10th wrong code in 24 h) rejects every pending approval
// and runs each one's OnReject.
func TestOnRejectRunsOnLockout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s, _, _ := newTestStore(t, clock(&now))
	var calls int32
	var last string
	for i := 0; i < MaxWrongPerDay; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{OnReject: func(context.Context) { atomic.AddInt32(&calls, 1) }})
		if err != nil {
			t.Fatal(err)
		}
		last = v.ID
		_, _ = s.Confirm(ctx, v.ID, "000000")
		if i < MaxWrongPerDay-1 {
			if _, err := s.Reject(ctx, v.ID, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if v, _ := s.Show(ctx, last); v.State != StateRejected {
		t.Fatalf("last approval %s", v.State)
	}
	if got := atomic.LoadInt32(&calls); got != MaxWrongPerDay {
		t.Fatalf("OnReject ran %d times, want %d", got, MaxWrongPerDay)
	}
}
