package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Review 97 L3: when the grant.revoke row's detail cannot be read, the failure
// is logged and the row is written without the peer; the removal's
// transaction goes on and commits (D68).
func TestAuditGrantRevokesReadFailureDoesNotAbort(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var buf lockedBuf
	audit.SetErrorLog(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { audit.SetErrorLog(nil) })

	log := audit.New(st.DB())
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// g-missing has no grants row, so reading its peer fails.
	if err := auditGrantRevokes(ctx, tx, log, []string{"g-missing"}, capability.ReasonSessionClosed, ""); err != nil {
		t.Fatalf("a failing detail read aborted the removal: %v", err)
	}
	// With the peer known (a peer removal) nothing is read.
	if err := auditGrantRevokes(ctx, tx, log, []string{"g-other"}, capability.ReasonPeerRemoved, "peer-key"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	evs, err := log.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		if e.Action == "grant.revoke" {
			got = append(got, string(e.Detail))
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], `"grant":"g-missing"`) || !strings.Contains(got[0], `"peer":""`) ||
		!strings.Contains(got[1], `"peer":"peer-key"`) {
		t.Fatalf("grant.revoke rows = %v", got)
	}
	if l := buf.String(); !strings.Contains(l, "event=audit_error action=grant.revoke ") || strings.Count(l, "audit_error") != 1 {
		t.Fatalf("central log = %q, want one audit_error line for grant.revoke", l)
	}
}
