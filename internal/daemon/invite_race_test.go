package daemon_test

// Review 79 H1 (R55-F24 security review): one approved team_invite approval must
// release exactly one invite code, even when the caller repeats the call
// concurrently. The gate's has/Show/drop sequence is not atomic.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func TestTeamInviteApprovalConcurrentReuse(t *testing.T) {
	a, _ := newGatePair(t)
	var tr daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &tr)
	var res daemon.TeamInviteResult
	a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID}, &res)
	id := res.Approval.ID
	a.humanApprove(id)
	harnessWait(t, "approval approved", func() bool {
		return a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+id+`' AND state = 'approved'`) == 1
	})

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var got daemon.TeamInviteResult
			if err := ipc.Call(ctx, a.p.Endpoint, "team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: id}, &got); err == nil && got.Code != "" {
				codes <- got.Code
			}
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	var all []string
	for c := range codes {
		all = append(all, c)
	}
	if len(all) != 1 {
		t.Fatalf("one approval released %d invite codes: %v", len(all), all)
	}
}
