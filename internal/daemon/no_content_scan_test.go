package daemon_test

// R55-200 (T9): the no-content e2e tests used to scan audit_events only. They
// now also scan every daemon's log, the relay's log, the outbox error column
// and the webhook queue (body and error): content must appear in none of them
// (rubric invariant 1, notify.md §Privacy).

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// markerHits returns "<where>: <marker>" for every marker found in a text.
func markerHits(texts map[string]string, markers []string) []string {
	var hits []string
	for where, text := range texts {
		for _, m := range markers {
			if strings.Contains(text, m) {
				hits = append(hits, where+": "+m)
			}
		}
	}
	return hits
}

// nodeColumn reads one text column of every row of q from n's database.
func nodeColumn(t *testing.T, n *harnessNode, q string) string {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var sb strings.Builder
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		sb.WriteString(s)
		sb.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// assertNoContentAnywhere fails if a marker is in a log, an outbox error or a
// webhook queue row of any of the nodes, or in the relay's log.
func assertNoContentAnywhere(t *testing.T, markers []string, nodes ...*harnessNode) {
	t.Helper()
	texts := map[string]string{}
	for _, n := range nodes {
		texts[n.name+" log"] = n.logs.String()
		texts[n.name+" outbox.error"] = nodeColumn(t, n, `SELECT COALESCE(error, '') FROM outbox`)
		texts[n.name+" webhook_queue.body"] = nodeColumn(t, n, `SELECT body FROM webhook_queue`)
		texts[n.name+" webhook_queue.error"] = nodeColumn(t, n, `SELECT COALESCE(error, '') FROM webhook_queue`)
		if n.relay != nil {
			texts["relay log"] = n.relay.logs.String()
		}
	}
	for _, h := range markerHits(texts, markers) {
		t.Errorf("content outside the audit log: %s", h)
	}
}

// TestNoContentScanNegativeControl: a deliberate marker in a log line, an
// outbox error and a webhook row is found, so the scan above cannot pass by
// looking at nothing.
func TestNoContentScanNegativeControl(t *testing.T) {
	markers := []string{"MARKER-CONTROL"}
	for _, where := range []string{"log", "outbox.error", "webhook_queue.body"} {
		texts := map[string]string{"clean": "nothing here", where: "x MARKER-CONTROL y"}
		if hits := markerHits(texts, markers); len(hits) != 1 || !strings.HasPrefix(hits[0], where) {
			t.Errorf("%s: hits %v, want the planted marker", where, hits)
		}
	}
	r := newHarnessRelay(t)
	n := newHarnessNode(t, "alice", r)
	n.start()
	n.logs.Write([]byte("level=INFO msg=planted MARKER-CONTROL\n"))
	if hits := markerHits(map[string]string{"alice log": n.logs.String()}, markers); len(hits) != 1 {
		t.Fatalf("a marker planted in a daemon log was not found: %v", hits)
	}
	// And the real scan, over a quiet node, is clean.
	assertNoContentAnywhere(t, []string{"MARKER-NOT-PRESENT"}, n)
}
