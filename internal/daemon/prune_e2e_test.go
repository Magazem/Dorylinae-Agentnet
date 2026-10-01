package daemon_test

// R55-F13 (Docs/protocol/retention.md §IPC, §Approval, §Audit; owner
// decision D57): data_prune through a live daemon. A dry run removes
// nothing; a removal waits for a human approval, then runs in batches against
// the cutoff the human saw, each batch audited with counts only; a rejected
// approval removes nothing (tests A11 daemon part and A14's IPC side of
// Docs/review/71-r55-f13-spec.md).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func pruneDaysAgo(d int) string {
	return time.Now().Add(-time.Duration(d) * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
}

func pruneCall(n *harnessNode, params map[string]any) (daemon.DataPruneResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var r daemon.DataPruneResult
	err := ipc.Call(ctx, n.p.Endpoint, "data_prune", params, &r)
	return r, err
}

func TestDataPruneNeedsApproval(t *testing.T) {
	e := newQEnv(t)
	a := e.a
	const secret = "PRUNE-CONTENT-MARKER"
	for i, days := range []int{40, 41, 34} {
		id := "r-00000000000000000000000000000" + string(rune('a'+i)) + "0"
		if err := a.exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash,
			state, created, received_at, mail_id, updated) VALUES ('in', ?, ?, 't', 'task', 'normal', 'normal', ?, 'h', 'completed', ?, ?, 'm', ?)`,
			e.b.key, id, `{"title":"`+secret+`"}`, pruneDaysAgo(days), pruneDaysAgo(days), pruneDaysAgo(days)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.exec(`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES ('k', 'm-old', 'note', 'c', ?, '{"x":"`+secret+`"}')`,
		pruneDaysAgo(1)); err != nil {
		t.Fatal(err)
	}
	const d35 = 3024000

	// Bad params.
	for _, p := range []map[string]any{
		{"older_than_s": d35 - 1}, {}, {"older_than_s": 3024000.5}, {"older_than_s": "3024000"},
		{"older_than_s": d35, "dry_run": true, "approval": "a-00000000000000000000000000000000"},
	} {
		_, err := pruneCall(a, p)
		var ie *ipc.Error
		if !errors.As(err, &ie) || ie.Code != ipc.CodeBadRequest {
			t.Errorf("data_prune %v: %v, want bad_request", p, err)
		}
	}

	// A dry run counts and removes nothing.
	dry, err := pruneCall(a, map[string]any{"older_than_s": d35, "dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Counts.Requests != 2 || dry.Counts.InboxBlanked != 1 || dry.More || dry.Approval != nil {
		t.Fatalf("dry run = %+v", dry)
	}

	// Without the human, nothing is removed: a rejected approval ends it.
	first, err := pruneCall(a, map[string]any{"older_than_s": d35})
	if err != nil || first.Approval == nil || first.Approval.Kind != "data_prune" || first.Counts != dry.Counts {
		t.Fatalf("data_prune = %+v, %v; want a pending data_prune approval with the dry run's counts", first, err)
	}
	if strings.Contains(first.Approval.Summary, secret) || !strings.Contains(first.Approval.Summary, "2 request(s)") {
		t.Fatalf("summary = %q", first.Approval.Summary)
	}
	again, err := pruneCall(a, map[string]any{"older_than_s": d35, "approval": first.Approval.ID})
	if err != nil || again.Approval == nil || again.Approval.State != "pending" {
		t.Fatalf("while pending = %+v, %v", again, err)
	}
	var rej json.RawMessage
	a.call("approval_reject", map[string]any{"id": first.Approval.ID}, &rej)
	_, err = pruneCall(a, map[string]any{"older_than_s": d35, "approval": first.Approval.ID})
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != daemon.CodeApprovalRejected {
		t.Fatalf("after reject: %v, want %s", err, daemon.CodeApprovalRejected)
	}
	if n := a.count(`SELECT COUNT(*) FROM requests`); n != 3 {
		t.Fatalf("requests = %d after a rejected prune, want 3", n)
	}

	// Approved: the loop removes what the approval showed.
	second, err := pruneCall(a, map[string]any{"older_than_s": d35})
	if err != nil || second.Approval == nil {
		t.Fatalf("second data_prune = %+v, %v", second, err)
	}
	if _, err := pruneCall(a, map[string]any{"older_than_s": d35 + 1, "approval": second.Approval.ID}); !errors.As(err, &ie) || ie.Code != ipc.CodeBadRequest {
		t.Fatalf("another older_than_s with the approval: %v, want bad_request", err)
	}
	e.approve(t, second.Approval.ID)
	var got daemon.DataPruneResult
	harnessWait(t, "the approved prune to run", func() bool {
		r, err := pruneCall(a, map[string]any{"older_than_s": d35, "approval": second.Approval.ID})
		if err != nil {
			t.Fatalf("approved data_prune: %v", err)
		}
		got = r
		return r.Approval == nil
	})
	if got.Counts != dry.Counts || got.More || got.DryRun {
		t.Fatalf("removed = %+v, want the dry run's %+v", got, dry.Counts)
	}
	if n := a.count(`SELECT COUNT(*) FROM requests`); n != 1 {
		t.Fatalf("requests left = %d, want the 34-day one", n)
	}
	if n := a.count(`SELECT COUNT(*) FROM mail_inbox WHERE signed <> ''`); n != 0 {
		t.Fatalf("%d inbox rows still hold plaintext", n)
	}
	// Finished: the approval id no longer runs anything.
	if _, err := pruneCall(a, map[string]any{"older_than_s": d35, "approval": second.Approval.ID}); !errors.As(err, &ie) || ie.Code != "unknown_approval" {
		t.Fatalf("after the loop: %v, want unknown_approval", err)
	}

	// One data.prune row with the counts and no content; the chain verifies.
	var rows []audit.Entry
	harnessWait(t, "the data.prune audit row", func() bool {
		rows = nil
		for _, ev := range auditList(t, a, audit.ListParams{Limit: audit.MaxLimit}) {
			if ev.Action == "data.prune" {
				rows = append(rows, ev)
			}
		}
		return len(rows) == 1
	})
	var detail map[string]any
	if err := json.Unmarshal(rows[0].Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["requests"] != float64(2) || detail["inbox_blanked"] != float64(1) || detail["older_than_s"] != float64(d35) ||
		detail["approval"] != second.Approval.ID || strings.Contains(string(rows[0].Detail), secret) {
		t.Fatalf("data.prune detail = %s", rows[0].Detail)
	}
	var v daemon.AuditVerifyResult
	a.call("audit_verify", daemon.AuditVerifyParams{}, &v)
	if v.Verify == nil || v.Verify.Status != "ok" {
		t.Fatalf("audit verify = %+v", v.Verify)
	}

	// Nothing left: no approval is asked.
	none, err := pruneCall(a, map[string]any{"older_than_s": d35})
	if err != nil || none.Approval != nil || !none.Counts.Zero() {
		t.Fatalf("nothing to remove = %+v, %v", none, err)
	}
}

// Retention.md §Approval: the batches never remove more than the approval
// showed. A finished request that joins the set after the approval was
// created is left for a new approval.
func TestDataPruneRemovesNoMoreThanApproved(t *testing.T) {
	e := newQEnv(t)
	a := e.a
	insert := func(id string, days int) {
		t.Helper()
		if err := a.exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash,
			state, created, received_at, mail_id, updated) VALUES ('in', ?, ?, 't', 'task', 'normal', 'normal', '{}', 'h', 'completed', ?, ?, 'm', ?)`,
			e.b.key, id, pruneDaysAgo(days), pruneDaysAgo(days), pruneDaysAgo(days)); err != nil {
			t.Fatal(err)
		}
	}
	insert("r-000000000000000000000000000000a0", 40)
	const d35 = 3024000
	first, err := pruneCall(a, map[string]any{"older_than_s": d35})
	if err != nil || first.Approval == nil || first.Counts.Requests != 1 {
		t.Fatalf("data_prune = %+v, %v; want an approval for 1 request", first, err)
	}
	// Joins after counting, older than the approved one.
	insert("r-000000000000000000000000000000b0", 41)
	e.approve(t, first.Approval.ID)
	var total daemon.DataPruneResult
	harnessWait(t, "the approved prune to finish", func() bool {
		r, err := pruneCall(a, map[string]any{"older_than_s": d35, "approval": first.Approval.ID})
		if err != nil {
			t.Fatalf("approved data_prune: %v", err)
		}
		if r.Approval != nil {
			return false
		}
		total.Counts = total.Counts.Add(r.Counts)
		return !r.More
	})
	if total.Counts.Requests != 1 {
		t.Fatalf("removed %d requests, the approval showed 1", total.Counts.Requests)
	}
	if n := a.count(`SELECT COUNT(*) FROM requests`); n != 1 {
		t.Fatalf("requests left = %d, want 1 for a new approval", n)
	}
}
