package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

func wireKey(seed byte) string {
	s := make([]byte, ed25519.SeedSize)
	s[0] = seed
	return base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(s).Public().(ed25519.PublicKey))
}

// TestDebateNotifyTitleDirection: R55-F20 acceptance test 12. The debate's
// webhook title comes from the row of the debate's own direction, never from
// an unrelated request with the same id in the other direction.
func TestDebateNotifyTitleDirection(t *testing.T) {
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const team = "t-fedcba9876543210fedcba9876543210"
	self, peer := wireKey(1), wireKey(2)
	id := request.NewID()
	for _, r := range []struct{ direction, title string }{{"out", "mine"}, {"in", "theirs"}} {
		from, to := self, peer
		if r.direction == "in" {
			from, to = peer, self
		}
		canon, err := request.Canonical(&request.Request{
			V: 1, ID: id, From: from, To: to, Team: team, Type: request.TypeTask, Title: r.title,
			Brief: "What: x", Urgency: request.UrgencyNormal, Created: time.Now().UTC().Truncate(time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash, state, state_seq, created, mail_id, updated)
VALUES (?, ?, ?, ?, 'task', 'normal', 'normal', ?, ?, 'pending', 0, '2026-09-25T10:00:00Z', 'm-1', '2026-09-25T10:00:00Z')`,
			r.direction, peer, id, team, string(canon), request.BodyHash(canon)); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	wh := &notify.Webhook{
		Settings: notify.NewSettings(st.DB()),
		Queue:    notify.NewQueue(st.DB()),
		Secret:   keystore.New(keystore.NewFile(filepath.Join(dir, "webhook.key"))),
	}
	if err := wh.Settings.SetWebhook(ctx, notify.WebhookConfig{URL: srv.URL, Format: notify.FormatGeneric, Title: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := wh.RotateSecret(); err != nil {
		t.Fatal(err)
	}
	trigger := &notify.Trigger{Webhook: wh, Show: func(context.Context, string, string) error { return nil }}
	fire := debateNotifyAdapter(trigger, nil, &request.Store{DB: st.DB(), Self: self})

	for i, c := range []struct{ role, sid, want, not string }{
		{"respondent", worksession.DeriveID(peer, self, id), "theirs", "mine"},
		{"initiator", worksession.DeriveID(self, peer, id), "mine", "theirs"},
	} {
		fire(ctx, notify.EventDebateAgreed, c.sid, peer, id)
		var body string
		deadline := time.Now().Add(10 * time.Second)
		for {
			rows, err := st.DB().Query(`SELECT body FROM webhook_queue ORDER BY created, rowid`)
			if err != nil {
				t.Fatal(err)
			}
			var bodies []string
			for rows.Next() {
				var b string
				if err := rows.Scan(&b); err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, b)
			}
			_ = rows.Close()
			if len(bodies) > i {
				body = bodies[i]
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: no webhook queued", c.role)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !strings.Contains(body, `"`+c.want+`"`) || strings.Contains(body, `"`+c.not+`"`) {
			t.Errorf("%s: webhook body %s, want title %q", c.role, body, c.want)
		}
	}
}
