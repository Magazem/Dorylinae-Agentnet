package daemon_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// Review 68 A9 (R55-086): mail_submit reads body with the strict parse and
// rule 4. A number that is not a canonical integer below 2^53, a duplicate
// member or a lone surrogate escape is bad_request, and nothing is rounded:
// the largest safe integer arrives exactly.
func TestMailSubmitBodyIsStrict(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)

	submit := func(body string) (daemon.MailSubmitResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res daemon.MailSubmitResult
		err := ipc.Call(ctx, a.p.Endpoint, "mail_submit", daemon.MailSubmitParams{To: b.key, Kind: "note", Body: []byte(body)}, &res)
		return res, err
	}
	for _, body := range []string{
		`{"n":9007199254740993}`,
		`{"n":9007199254740992}`,
		`{"n":1.5}`,
		`{"n":1e3}`,
		`{"a":1,"a":2}`,
		`{"s":"` + string(rune(0x5C)) + `ud800"}`,
	} {
		_, err := submit(body)
		var ie *ipc.Error
		if !errors.As(err, &ie) || ie.Code != ipc.CodeBadRequest {
			t.Errorf("mail_submit body %s: err = %v, want bad_request", body, err)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Fatalf("%d refused mails were queued", n)
	}

	res, err := submit(`{"n":9007199254740991}`)
	if err != nil {
		t.Fatal(err)
	}
	harnessWaitFor(t, "B to receive the mail", 90*time.Second, func() bool {
		return b.count(`SELECT COUNT(*) FROM mail_inbox WHERE id = '`+res.ID+`'`) == 1
	}, func() { t.Logf("relay log:\n%s", r.logs.String()) })
	// B opened and stored it (its strict parser accepts the exact integer).
	// Since R55-F13 the inbox row keeps no copy of the plaintext
	// (Docs/protocol/mail.md §Inbox rows), so the integer itself is checked
	// on A's signed copy while it is still queued, if it still is.
	var signed string
	if err := b.query(`SELECT signed FROM mail_inbox WHERE id = '`+res.ID+`'`, &signed); err != nil || signed != "" {
		t.Fatalf("inbox signed = %q, %v; want blank", signed, err)
	}
	var sent sql.NullString
	if err := a.query(`SELECT signed FROM outbox WHERE id = '`+res.ID+`'`, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Valid && !strings.Contains(sent.String, `"body":{"n":9007199254740991}`) {
		t.Fatalf("the signed mail does not carry the exact integer: %s", sent.String)
	}
}
