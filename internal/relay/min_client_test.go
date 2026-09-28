package relay_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

func readyFrame(t *testing.T, opts relay.Options) (envelope.Control, string) {
	t.Helper()
	_, url := start(t, opts)
	p := newPeer(t)
	c, nonce := rawDial(t, url)
	writeFrame(t, c, authFrame(t, p, nonce))
	_, raw, err := c.Read(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	var ready envelope.Control
	if err := json.Unmarshal(raw, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Op != envelope.OpReady {
		t.Fatalf("first frame = %s, want ready", raw)
	}
	return ready, string(raw)
}

// 4.4a: MinClient is advertised as ready.min_client, and omitted when unset.
func TestReadyMinClient(t *testing.T) {
	ready, _ := readyFrame(t, relay.Options{MinClient: "1.2.3"})
	if ready.MinClient != "1.2.3" {
		t.Fatalf("min_client = %q, want 1.2.3", ready.MinClient)
	}
	_, raw := readyFrame(t, relay.Options{})
	if strings.Contains(raw, "min_client") {
		t.Fatalf("ready without MinClient carries min_client: %s", raw)
	}
}

func TestOpenRefusesBadMinClient(t *testing.T) {
	for _, v := range []string{"v1.2.3", "1.2", "1.2.3-beta", "latest"} {
		if _, err := relay.Open(relay.Options{MinClient: v}); err == nil {
			t.Errorf("Open accepted MinClient %q", v)
		}
	}
}
