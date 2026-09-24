package capability

import (
	"context"
	"testing"
	"time"
)

// TestQuarantineHolds covers the rule of Docs/protocol/work-session.md
// §Quarantine (2.4) at its edges, with an injected clock: sensitive grants
// that were ever active count (active, or revoked after approval or by
// policy), a never-approved one does not, non-sensitive and held rows do not,
// and the peer-wide clause holds while exp is later than now - 7 d.
func TestQuarantineHolds(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	const sid, other = "s-11111111111111111111111111111111", "s-22222222222222222222222222222222"

	type row struct {
		mutate func(r *Record)
		state  string // "pending", "active", "revoked-approved", "revoked-pending"
	}
	cases := []struct {
		name string
		rows []row
		sid  string
		want bool
	}{
		{"no grants", nil, sid, false},
		{"active sensitive in the session", []row{{state: "active"}}, sid, true},
		{"revoked after approval", []row{{state: "revoked-approved"}}, sid, true},
		{"revoked by session close, never approved", []row{{state: "revoked-pending"}}, sid, false},
		{"pending only", []row{{state: "pending"}}, sid, false},
		{"non-sensitive (public git)", []row{{state: "active", mutate: func(r *Record) { r.Sensitive = false }}}, sid, false},
		{"held row is not an issued grant", []row{{state: "active", mutate: func(r *Record) { r.Direction = DirectionHeld }}}, sid, false},
		{"policy-issued then revoked", []row{{state: "revoked-policy"}}, sid, true},
		{"other session, same peer, exp in the future", []row{{state: "active", mutate: func(r *Record) { r.Session = other }}}, sid, true},
		{"other session, same peer, expired 6 d 23 h ago", []row{{state: "active", mutate: func(r *Record) {
			r.Session, r.Exp = other, now.Add(-(7*24*time.Hour - time.Hour))
		}}}, sid, true},
		{"other session, same peer, expired 7 d 1 h ago", []row{{state: "active", mutate: func(r *Record) {
			r.Session, r.Exp = other, now.Add(-(7*24*time.Hour + time.Hour))
		}}}, sid, false},
		{"other session, other peer", []row{{state: "active", mutate: func(r *Record) { r.Session, r.Peer = other, "someone-else" }}}, sid, false},
		{"same session, expired long ago still counts", []row{{state: "active", mutate: func(r *Record) { r.Exp = now.Add(-30 * 24 * time.Hour) }}}, sid, true},
		{"other session, pending only", []row{{state: "pending", mutate: func(r *Record) { r.Session = other }}}, sid, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			s := &Store{DB: db, Now: func() time.Time { return now }}
			for _, rw := range tc.rows {
				rec := testRecord(NewGrantID(), now)
				rec.Session = sid
				if rw.mutate != nil {
					rw.mutate(&rec)
				}
				// The store shares one SQLite connection: never call a non-tx
				// method while a tx is open.
				if rw.state == "pending" || rw.state == "revoked-pending" || rw.state == "revoked-approved" {
					if err := s.InsertPending(ctx, rec); err != nil {
						t.Fatal(err)
					}
				}
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				switch rw.state {
				case "revoked-pending":
					_, err = s.RevokeTx(ctx, tx, rec.ID, ReasonSessionClosed, now)
				case "active":
					if rec.Direction == DirectionHeld {
						err = s.InsertHeldTx(ctx, tx, rec)
					} else {
						err = s.InsertActiveTx(ctx, tx, rec)
					}
				case "revoked-approved":
					if _, err = s.ActivateTx(ctx, tx, rec.ID, "a-approved", now); err == nil {
						_, err = s.RevokeTx(ctx, tx, rec.ID, ReasonSessionClosed, now)
					}
				case "revoked-policy":
					rec.Policy = "p-1"
					if err = s.InsertActiveTx(ctx, tx, rec); err == nil {
						_, err = s.RevokeTx(ctx, tx, rec.ID, ReasonUser, now)
					}
				}
				if err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			got, err := s.QuarantineHolds(ctx, tx, tc.sid, "peer-key")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("QuarantineHolds = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestActivateNeedsApprovalID: a grant is never activated without the id of
// the approval that activated it (it is the mark the quarantine rule reads).
func TestActivateNeedsApprovalID(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := testRecord(NewGrantID(), now)
	if err := s.InsertPending(ctx, rec); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.ActivateTx(ctx, tx, rec.ID, "", now); err == nil {
		t.Fatal("ActivateTx with an empty approval id succeeded")
	}
}
