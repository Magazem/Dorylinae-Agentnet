package daemon_test

// accept, decline and defer resolve --from ("from" on the IPC) with the shared
// peer resolver: a name, a public key, an unknown name and an ambiguous name
// (Docs/cli/inbox.md).

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// unpairedDashKey is a well-formed public key (32 bytes, base64url) that
// starts with '-' and is paired with nobody.
const unpairedDashKey = "-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestLifecycleFromResolvesPeer(t *testing.T) {
	r := newHarnessRelay(t)
	a, b, c := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	c.start()
	waitRelayConnected(t, r, a.key, b.key, c.key)
	harnessPair(t, a, b)
	harnessPair(t, a, c)
	teamID := harnessSharedTeam(t, b, a, "x")

	// One pending request per accept/decline/defer x name/key case.
	verbs := []string{"request_accept", "request_decline", "request_defer"}
	forms := []string{"name", "key"}
	ids := map[string]string{}
	for _, v := range verbs {
		for _, f := range forms {
			var sub daemon.RequestSubmitResult
			b.call("request_submit", daemon.RequestSubmitParams{
				To: a.key, Type: "task", Team: teamID, Title: v + f, Brief: "What: x\n",
			}, &sub)
			ids[v+f] = sub.ID
		}
	}
	harnessWait(t, "A to see all pending requests", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == len(ids)
	})

	params := func(verb, id, from string) map[string]any {
		p := map[string]any{"id": id, "from": from}
		switch verb {
		case "request_decline":
			p["reason"] = "no"
		case "request_defer":
			p["until"] = "2h"
		}
		return p
	}

	// bob is now ambiguous (b and c share the name), so the name cases must
	// fail with the matches listed, and only the key cases succeed.
	for _, v := range verbs {
		err := ipcCallErr(a, v, params(v, ids[v+"name"], "bob"), &daemon.RequestLifecycleResult{})
		if errCode(err) != daemon.CodeAmbiguousPeer || !strings.Contains(err.Error(), b.key) || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s from an ambiguous name: err = %v, want ambiguous_peer listing both keys", v, err)
		}
		if err := ipcCallErr(a, v, params(v, ids[v+"name"], "nobody"), &daemon.RequestLifecycleResult{}); errCode(err) != daemon.CodeUnknownPeer {
			t.Errorf("%s from an unknown name: err = %v, want unknown_peer", v, err)
		}
		// A well-formed unpaired key starting with '-' is not an unknown
		// peer: it reaches the store, which finds no such request.
		err = ipcCallErr(a, v, params(v, ids[v+"name"], unpairedDashKey), &daemon.RequestLifecycleResult{})
		if errCode(err) != daemon.CodeUnknownRequest {
			t.Errorf("%s from an unpaired key: err = %v, want unknown_request", v, err)
		}
		var res daemon.RequestLifecycleResult
		a.call(v, params(v, ids[v+"key"], b.key), &res)
		if res.Request.ID != ids[v+"key"] {
			t.Errorf("%s from a key: result %+v", v, res)
		}
	}

	// complete shares the resolver (its "from" is documented the same way).
	err := ipcCallErr(a, "request_complete", map[string]any{"id": ids["request_acceptname"], "from": "bob"}, &daemon.RequestLifecycleResult{})
	if errCode(err) != daemon.CodeAmbiguousPeer {
		t.Errorf("request_complete from an ambiguous name: err = %v, want ambiguous_peer", err)
	}

	// Make the name unique again by unpairing c, then a name resolves.
	a.call("peers_remove", daemon.PeerRemoveParams{Peer: c.key}, &daemon.PeerResult{})
	for _, v := range verbs {
		var res daemon.RequestLifecycleResult
		a.call(v, params(v, ids[v+"name"], "@Bob"), &res)
		if res.Request.ID != ids[v+"name"] || res.Request.Peer.Name != "bob" {
			t.Errorf("%s from a name: result %+v", v, res)
		}
	}
}
