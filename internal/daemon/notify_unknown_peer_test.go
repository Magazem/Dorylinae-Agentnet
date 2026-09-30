package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// TestNotifyAdapterUnknownPeerHidesKey (R55-075): when the peer is not in the
// peer list the notification says "unknown peer", never the public key.
func TestNotifyAdapterUnknownPeerHidesKey(t *testing.T) {
	const key = "PUBLIC-KEY-OF-A-STRANGER"
	shown := make(chan string, 1)
	tr := &notify.Trigger{Show: func(_ context.Context, title, body string) error {
		shown <- title + "|" + body
		return nil
	}}
	fn := notifyAdapter(tr, nil, nil)
	fn(context.Background(), notify.EventReceived, request.NotifyInfo{Peer: key, Type: "review", Urgency: "high", Title: "t"})

	select {
	case got := <-shown:
		if strings.Contains(got, key) || !strings.Contains(got, "unknown peer") {
			t.Fatalf("notification = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification shown")
	}
}
