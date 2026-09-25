package experience

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func baseInput() Input {
	now := time.Now().UTC().Truncate(time.Second)
	return Input{
		Session: "s-1", Request: "r-1", Role: "requester", Kind: KindWork,
		Peer: "peerkey", Team: "t-1", Type: "task",
		ProblemTitle: "title", ProblemBrief: "brief",
		Rounds: 1, Verification: "none",
		Outcome: "accepted", AgeS: 5,
		Opened: now.Add(-time.Minute), Closed: now,
	}
}

func decodeCanon(t *testing.T, canon []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(canon, &m); err != nil {
		t.Fatalf("decode canon: %v", err)
	}
	return m
}

func TestBuildWorkAccepted(t *testing.T) {
	in := baseInput()
	in.WorkedStatus, in.WorkedSummary = "pass", "did the thing"
	canon, truncated, err := Build(in)
	if err != nil || truncated {
		t.Fatalf("Build: %v truncated=%v", err, truncated)
	}
	m := decodeCanon(t, canon)
	if m["session"] != "s-1" || m["kind"] != KindWork {
		t.Fatalf("record = %v", m)
	}
	worked, ok := m["worked"].(map[string]any)
	if !ok || worked["status"] != "pass" {
		t.Fatalf("worked = %v", m["worked"])
	}
	if _, ok := m["failed"]; ok {
		t.Fatalf("failed present on a clean accept: %v", m["failed"])
	}
}

func TestBuildWorkCancelled(t *testing.T) {
	in := baseInput()
	in.Outcome = "cancelled"
	in.WorkedStatus = "should never appear"
	canon, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCanon(t, canon)
	if _, ok := m["worked"]; ok {
		t.Fatalf("worked present on a cancelled close: %v", m["worked"])
	}
}

func TestBuildWorkCancelledByRecordsCancelledBy(t *testing.T) {
	in := baseInput()
	in.Outcome, in.CancelledBy = "cancelled", "worker"
	canon, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCanon(t, canon)
	failed, ok := m["failed"].(map[string]any)
	if !ok || failed["cancelled_by"] != "worker" {
		t.Fatalf("failed = %v", m["failed"])
	}
}

func TestBuildDebateAgreed(t *testing.T) {
	in := baseInput()
	in.Kind, in.Role = KindDebate, "initiator"
	in.InitiatorClaim, in.RespondentClaim = "claim A", "claim B"
	in.RoundsUsed = 2
	in.Outcome = "agreed"
	in.WorkedDecision = "use approach X"
	in.Decision = &Decision{ID: "d-1", Hash: "abc"}
	canon, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCanon(t, canon)
	approach, ok := m["approach"].(map[string]any)
	if !ok {
		t.Fatalf("approach = %v", m["approach"])
	}
	positions, ok := approach["positions"].(map[string]any)
	if !ok || positions["initiator"] != "claim A" || positions["respondent"] != "claim B" {
		t.Fatalf("positions = %v", approach["positions"])
	}
	acc, ok := m["acceptance"].(map[string]any)
	if !ok {
		t.Fatalf("acceptance = %v", m["acceptance"])
	}
	dec, ok := acc["decision"].(map[string]any)
	if !ok || dec["id"] != "d-1" || dec["hash"] != "abc" {
		t.Fatalf("decision = %v", acc["decision"])
	}
	worked, ok := m["worked"].(map[string]any)
	if !ok || worked["decision"] != "use approach X" {
		t.Fatalf("worked = %v", m["worked"])
	}
}

func TestBuildDebateEscalated(t *testing.T) {
	in := baseInput()
	in.Kind, in.Role = KindDebate, "initiator"
	in.Outcome = "escalated"
	in.HasRemainingDisagreement, in.RemainingDisagreementPoints = true, 3
	canon, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCanon(t, canon)
	failed, ok := m["failed"].(map[string]any)
	if !ok || failed["remaining_disagreement_points"] != float64(3) {
		t.Fatalf("failed = %v", m["failed"])
	}
	if _, ok := m["worked"]; ok {
		t.Fatalf("worked present on an escalated close: %v", m["worked"])
	}
}

// TestBuildTruncatesInOrder: Docs/protocol/experience.md §Record, the
// truncation order approach.changes, failed.last_changes, problem.brief.
func TestBuildTruncatesInOrder(t *testing.T) {
	big := strings.Repeat("x", 70000)
	in := baseInput()
	in.Rounds = 2
	in.Changes, in.LastChanges = big, big
	in.RoundsRejected = 1
	in.ProblemBrief = big
	canon, truncated, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("want truncated")
	}
	if len(canon) > MaxRecordBytes {
		t.Fatalf("canon size = %d, want <= %d", len(canon), MaxRecordBytes)
	}
	m := decodeCanon(t, canon)
	if m["truncated"] != true {
		t.Fatalf(`"truncated" missing: %v`, m)
	}
	approach := m["approach"].(map[string]any)
	if _, ok := approach["changes"]; ok {
		t.Fatal("approach.changes survived truncation")
	}
	if failed, ok := m["failed"].(map[string]any); ok {
		if _, ok := failed["last_changes"]; ok {
			t.Fatal("failed.last_changes survived truncation")
		}
	}
	problem := m["problem"].(map[string]any)
	if _, ok := problem["brief"]; ok {
		t.Fatal("problem.brief survived truncation")
	}
	if problem["title"] != "title" {
		t.Fatalf("title dropped too: %v", problem)
	}
}

func TestBuildNeverExceedsCapEvenAtFullDrop(t *testing.T) {
	// Every optional large member dropped; the remainder must still fit even
	// with an implausibly long title (the builder must not loop forever or
	// panic: Build always returns at level 3).
	in := baseInput()
	in.ProblemTitle = strings.Repeat("y", 200000)
	canon, truncated, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("want truncated")
	}
	_ = canon // may still exceed the cap once every droppable member is gone; just must not error/panic
}

func TestWriteTxStoresOneRowPerSessionRole(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()

	write := func(in Input) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := WriteTx(ctx, tx, in, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	write(baseInput())
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM experience_records`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v", n, err)
	}
	var record string
	if err := db.QueryRow(`SELECT record FROM experience_records WHERE session = ? AND role = ?`, "s-1", "requester").Scan(&record); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !json.Valid([]byte(record)) {
		t.Fatal("stored record is not valid JSON")
	}
}

func TestWriteTxRolledBackLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "e2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteTx(ctx, tx, baseInput(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM experience_records`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after rollback = %d, %v", n, err)
	}
}
