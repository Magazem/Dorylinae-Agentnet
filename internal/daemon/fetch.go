package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

const fetchAuditBudget = 5 * time.Second

// startFetchServer serves fetch.req messages of the Noise sessions
// (Docs/protocol/grant.md §Enforcement and fetch) on a worker pool of its own.
// The returned func stops it. The second return value is the D23 git-version
// reason (empty when git is usable), for "status" to report.
func startFetchServer(sessions *session.Manager, caps *capability.Store, ws *worksession.Store, log *audit.Log, self string, logger *slog.Logger) (func(), string) {
	// git is resolved and version-checked once here, never per request (D23).
	gitBackend, gitReason := capability.NewGitBackend()
	if gitReason != "" && logger != nil {
		logger.Warn("git.read grants refused: unsupported git", "reason", gitReason)
	}
	srv := capability.NewFetchServer(capability.FetchConfig{
		Store: caps,
		Self:  self,
		SessionOpen: func(ctx context.Context, id, requester, worker string) (bool, bool) {
			v, err := ws.Get(ctx, id)
			if err != nil {
				return false, false
			}
			r, w := v.SelfOf(self)
			if r != requester || w != worker {
				return false, false
			}
			return true, v.State == worksession.StateOpen
		},
		Send: sessions.SendData,
		Backends: map[string]capability.Backend{
			capability.KindFS:  capability.FSBackend{},
			capability.KindGit: gitBackend,
		},
		Audit: func(ctx context.Context, action string, detail map[string]any) {
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchAuditBudget)
			defer cancel()
			_ = log.Append(actx, audit.ActorDaemon, action, detail)
		},
	})
	sessions.Handle(capability.TypeFetchReq, srv.Handle)
	return srv.Close, gitReason
}
