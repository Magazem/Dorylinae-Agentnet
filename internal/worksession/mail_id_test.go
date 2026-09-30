package worksession

import (
	"context"
	"testing"
)

// outboxHas reports whether n's outbox holds a ws.state row with id.
func outboxHas(t *testing.T, n *node, id string) bool {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE id = ? AND kind = ?`, id, KindState).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c == 1
}

// R55-115: every A-side transition returns the id of the ws.state it
// submitted.
func TestATransitionsReturnMailID(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, what string, a *node, v View, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if v.MailID == "" || !outboxHas(t, a, v.MailID) {
			t.Fatalf("%s: mail id %q is not the ws.state outbox row", what, v.MailID)
		}
	}

	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	v, err := a.ws.RequestChanges(ctx, sid, "again")
	check(t, "request changes", a, v, err)
	deliverState(t, a, b)
	deliverState(t, a, b)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	v, err = a.ws.AcceptResult(ctx, sid)
	check(t, "accept result", a, v, err)

	a, _, _, sid = setupAcceptedSession(t)
	v, err = a.ws.Cancel(ctx, sid, "")
	check(t, "cancel", a, v, err)

	a, b, reqID, sid = setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	submitAndDeliverResult(t, a, b, reqID, validResult())
	v, err = a.ws.Discard(ctx, sid)
	check(t, "discard", a, v, err)
}
