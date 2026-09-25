package ipc_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// shortHome returns a short temp dir: Unix socket paths are length limited.
func shortHome(t *testing.T) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func startEcho(t *testing.T, p paths.Paths) {
	t.Helper()
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer()
	srv.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var v map[string]any
		if err := json.Unmarshal(params, &v); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "bad params"}
		}
		return v, nil
	})
	srv.Handle("boom", func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("secret detail")
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
}

func call(t *testing.T, p paths.Paths, method string, params, out any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return ipc.Call(ctx, p.Endpoint, method, params, out)
}

func TestRoundTripAndErrors(t *testing.T) {
	p := shortHome(t)
	startEcho(t, p)

	var out map[string]any
	if err := call(t, p, "echo", map[string]any{"a": "b"}, &out); err != nil || out["a"] != "b" {
		t.Fatalf("echo: out=%v err=%v", out, err)
	}

	var ie *ipc.Error
	if err := call(t, p, "nope", nil, nil); !errors.As(err, &ie) || ie.Code != ipc.CodeUnknownMethod {
		t.Fatalf("unknown method: %v", err)
	}
	err := call(t, p, "boom", nil, nil)
	if !errors.As(err, &ie) || ie.Code != ipc.CodeInternal || ie.Message == "secret detail" {
		t.Fatalf("internal error must not leak detail: %v", err)
	}
}

func TestDialWhenNotRunning(t *testing.T) {
	p := shortHome(t)
	start := time.Now()
	err := call(t, p, "echo", nil, nil)
	if !errors.Is(err, ipc.ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("dial took too long")
	}
}

func TestSecondListenerRefused(t *testing.T) {
	p := shortHome(t)
	startEcho(t, p)
	ln, err := ipc.Listen(p.Endpoint)
	if err == nil {
		_ = ln.Close()
		t.Fatal("second Listen on a live endpoint should fail")
	}
	if !errors.Is(err, ipc.ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}
