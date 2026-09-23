package daemon

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// noSessions is a stand-in request.SessionHooks, only to flip the wiring.
type noSessions struct{}

func (noSessions) OpenSession(context.Context, *sql.Tx, string, string, string, string, time.Time) error {
	return nil
}
func (noSessions) EarlyComplete(context.Context, *sql.Tx, string, string, bool) (bool, error) {
	return true, nil
}
func (noSessions) CompleteShorthand(context.Context, string, string, string, *request.Result) (bool, error) {
	return false, nil
}

// Review 27 M1: until 2.1b wires request.Store.Sessions, ws.* stays
// unregistered (acked unsupported), so a peer cannot create session rows or
// store results on a daemon that never opens sessions.
func TestWSKindsOnlyWithSessions(t *testing.T) {
	ws := &worksession.Store{}
	for _, tc := range []struct {
		name string
		rs   *request.Store
		want bool
	}{
		{"sessions off", &request.Store{}, false},
		{"sessions on", &request.Store{Sessions: noSessions{}}, true},
	} {
		rcv, _ := newMailReceiver(nil, nil, nil, make([]byte, ed25519.PublicKeySize), nil, nil, nil, tc.rs, ws)
		for _, k := range []string{worksession.KindResult, worksession.KindState, worksession.KindCancel} {
			if _, ok := rcv.Kinds[k]; ok != tc.want {
				t.Errorf("%s: %s registered = %v, want %v", tc.name, k, ok, tc.want)
			}
		}
	}
}
