package ipc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// TestCallDeliversHTMLHeavyResult: debate.md §IPC "Size" (review 43 M7)
// promises that a '<'-heavy result keeps its canonical size on the wire. A
// 200 000-byte '<' string is far under the 1 MiB line, and must arrive end to
// end through ipc.Call (review 55 T6c-01).
func TestCallDeliversHTMLHeavyResult(t *testing.T) {
	p := shortHome(t)
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer()
	srv.Handle("big", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct {
			Ch string `json:"ch"`
		}
		_ = json.Unmarshal(params, &in)
		return map[string]string{"text": strings.Repeat(in.Ch, 200000)}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, ln) }()

	for _, ch := range []string{"a", "<", ">", "&"} {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		var out map[string]string
		err := ipc.Call(cctx, p.Endpoint, "big", map[string]string{"ch": ch}, &out)
		ccancel()
		if err != nil {
			t.Errorf("200000 x %q: %v", ch, err)
			continue
		}
		if out["text"] != strings.Repeat(ch, 200000) {
			t.Errorf("200000 x %q: result altered (%d bytes)", ch, len(out["text"]))
		}
	}
}
