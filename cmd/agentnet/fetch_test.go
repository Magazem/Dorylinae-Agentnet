package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func complete(result any) daemon.FetchStatus {
	b, _ := json.Marshal(result)
	return daemon.FetchStatus{FetchID: "ft-1", State: "complete", Result: b}
}

// fakeFetchDaemon serves fetch_start from serve and records every start.
type fakeFetchDaemon struct {
	mu     sync.Mutex
	starts []daemon.FetchStartParams
}

func (f *fakeFetchDaemon) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeFetchDaemon) start(t *testing.T, serve func(daemon.FetchStartParams) (any, error)) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p daemon.FetchStartParams
		if err := json.Unmarshal(params, &p); err != nil {
			t.Errorf("fetch_start params %q: %v", params, err)
		}
		f.mu.Lock()
		f.starts = append(f.starts, p)
		f.mu.Unlock()
		return serve(p)
	}
}

// contentServer answers reads of content the way the daemon does.
func contentServer(content []byte, commit string) func(daemon.FetchStartParams) (any, error) {
	return func(p daemon.FetchStartParams) (any, error) {
		end := min(int(p.Offset)+int(p.Length), len(content))
		chunk := []byte{}
		if int(p.Offset) < len(content) {
			chunk = content[p.Offset:end]
		}
		r := map[string]any{"data": base64.StdEncoding.EncodeToString(chunk), "offset": p.Offset, "size": len(content)}
		if commit != "" {
			r["commit"] = commit
		}
		return complete(r), nil
	}
}

func TestFetchFileInReads(t *testing.T) {
	p := shortHome(t)
	content := make([]byte, 600000) // 262144 + 262144 + 75712
	_, _ = rand.Read(content)
	fd := &fakeFetchDaemon{}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"fetch_start": fd.start(t, contentServer(content, ""))})

	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "dir/big.bin", "--timeout", "20"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Fatalf("stdout has %d bytes, want the %d bytes of the file", out.Len(), len(content))
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.starts) != 3 {
		t.Fatalf("%d reads, want 3", len(fd.starts))
	}
	for i, s := range fd.starts {
		if s.Grant != "g-1" || s.Op != "read" || s.Path != "dir/big.bin" || s.Offset != int64(i)*capability.MaxReadBytes || s.Length != capability.MaxReadBytes {
			t.Errorf("read %d = %+v", i, s)
		}
		if s.TimeoutS < 19 || s.TimeoutS > 20 {
			t.Errorf("read %d passes timeout_s %d, want the remaining ~20 s", i, s.TimeoutS)
		}
	}
}

func TestFetchOutFileAndJSON(t *testing.T) {
	p := shortHome(t)
	content := []byte("hello fetch\n")
	commit := strings.Repeat("ab", 20)
	fd := &fakeFetchDaemon{}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"fetch_start": fd.start(t, contentServer(content, commit))})
	dest := filepath.Join(t.TempDir(), "copy.txt")

	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "a.txt", "--out", dest}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "Wrote 12 bytes") {
		t.Fatalf("--out: code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	//nolint:gosec // dest is a path the test just made under t.TempDir
	if got, err := os.ReadFile(dest); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("file = %q, %v", got, err)
	}

	out.Reset()
	if code := run([]string{"fetch", "g-1", "a.txt", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("--json: code %d", code)
	}
	var body struct {
		OK     bool
		Path   string
		Size   int64
		Commit string
		Data   string
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil || !body.OK || body.Path != "a.txt" || body.Size != 12 || body.Commit != commit {
		t.Fatalf("json = %q (%v)", out.String(), err)
	}
	if data, _ := base64.StdEncoding.DecodeString(body.Data); !bytes.Equal(data, content) {
		t.Fatalf("data = %q", data)
	}

	out.Reset()
	other := filepath.Join(t.TempDir(), "j.txt")
	if code := run([]string{"fetch", "g-1", "a.txt", "--json", "--out", other}, &out, &errb); code != exitOK || strings.Contains(out.String(), `"data"`) {
		t.Fatalf("--json --out: code %d, stdout %q (the file is written, so no data)", code, out.String())
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal(err)
	}

	// an empty file: one read, nothing written
	fd2 := &fakeFetchDaemon{}
	p2 := shortHome(t)
	startFakeDaemon(t, p2, map[string]ipc.HandlerFunc{"fetch_start": fd2.start(t, contentServer(nil, ""))})
	out.Reset()
	if code := run([]string{"fetch", "g-1", "empty"}, &out, &errb); code != exitOK || out.Len() != 0 || fd2.startCount() != 1 {
		t.Fatalf("empty file: code %d, %d bytes, %d reads", code, out.Len(), fd2.startCount())
	}
}

func TestFetchPollsUntilComplete(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex
	polls := 0
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"fetch_start": func(context.Context, json.RawMessage) (any, error) {
			return daemon.FetchStatus{FetchID: "ft-9", State: "pending"}, nil
		},
		"fetch_status": func(_ context.Context, params json.RawMessage) (any, error) {
			var q daemon.FetchStatusParams
			_ = json.Unmarshal(params, &q)
			mu.Lock()
			defer mu.Unlock()
			polls++
			if q.FetchID != "ft-9" {
				t.Errorf("fetch_status id = %q", q.FetchID)
			}
			if polls < 3 {
				return daemon.FetchStatus{FetchID: "ft-9", State: "pending"}, nil
			}
			return complete(map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("ok")), "offset": 0, "size": 2}), nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "f"}, &out, &errb); code != exitOK || out.String() != "ok" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if polls != 3 {
		t.Fatalf("%d status polls, want 3", polls)
	}
}

func TestFetchListPagesAndStat(t *testing.T) {
	p := shortHome(t)
	size := int64(7)
	fd := &fakeFetchDaemon{}
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{"fetch_start": fd.start(t, func(q daemon.FetchStartParams) (any, error) {
		switch q.Op {
		case "stat":
			return complete(map[string]any{"entry": capability.Entry{Name: "f.txt", Type: "file", Size: &size}, "commit": strings.Repeat("c", 40)}), nil
		case "list":
			if q.Cursor == "" {
				return complete(map[string]any{"entries": []capability.Entry{{Name: "a", Type: "dir"}}, "cursor": "a"}), nil
			}
			return complete(map[string]any{"entries": []capability.Entry{{Name: "b.txt", Type: "file", Size: &size}}}), nil
		}
		return nil, &ipc.Error{Code: "bad_request", Message: "op"}
	})})

	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "--list", "sub"}, &out, &errb); code != exitOK {
		t.Fatalf("list: code %d, stderr %q", code, errb.String())
	}
	for _, want := range []string{"NAME", "a", "dir", "b.txt", "file", "7"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list %q lacks %q", out.String(), want)
		}
	}
	fd.mu.Lock()
	if len(fd.starts) != 2 || fd.starts[0].Path != "sub" || fd.starts[1].Cursor != "a" {
		t.Errorf("list calls = %+v, want two pages, the second with the cursor", fd.starts)
	}
	fd.mu.Unlock()

	out.Reset()
	if code := run([]string{"fetch", "g-1", "--list", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("list --json: code %d", code)
	}
	var lb struct {
		OK      bool
		Path    string
		Entries []capability.Entry
	}
	if err := json.Unmarshal(out.Bytes(), &lb); err != nil || !lb.OK || lb.Path != "" || len(lb.Entries) != 2 {
		t.Fatalf("list json = %q (%v)", out.String(), err)
	}

	out.Reset()
	if code := run([]string{"fetch", "g-1", "--stat", "f.txt"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "f.txt\tfile\t7") {
		t.Fatalf("stat: code %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"fetch", "g-1", "--stat", "f.txt", "--json"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), `"entry":{"name":"f.txt","type":"file","size":7}`) || !strings.Contains(out.String(), `"commit"`) {
		t.Fatalf("stat json: code %d, stdout %q", code, out.String())
	}
}

func TestFetchFailuresAndExitCodes(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"fetch_start": func(_ context.Context, params json.RawMessage) (any, error) {
			var q daemon.FetchStartParams
			_ = json.Unmarshal(params, &q)
			switch q.Grant {
			case "g-expired": // a local failure: no fetch was started
				return nil, &ipc.Error{Code: "expired", Message: "the grant cannot be used: expired"}
			case "g-timeout":
				return daemon.FetchStatus{FetchID: "ft-t", State: "failed", Error: &daemon.FetchError{Code: "timeout", Message: "the grantor did not answer in time"}}, nil
			case "g-revoked":
				return daemon.FetchStatus{FetchID: "ft-r", State: "failed", Error: &daemon.FetchError{Code: "revoked", Message: "the grantor refused the fetch: revoked"}}, nil
			}
			return nil, &ipc.Error{Code: "unknown_grant", Message: "no such grant"}
		},
	})
	for _, tc := range []struct {
		grant string
		exit  int
		code  string
	}{
		{"g-expired", exitError, "expired"},
		{"g-timeout", exitWaitTimeout, "timeout"},
		{"g-revoked", exitError, "revoked"},
		{"g-unknown", exitError, "unknown_grant"},
	} {
		var out, errb bytes.Buffer
		if code := run([]string{"fetch", tc.grant, "f"}, &out, &errb); code != tc.exit || out.Len() != 0 || !strings.Contains(errb.String(), "agentnet: ") {
			t.Errorf("%s: code %d, stdout %q, stderr %q; want exit %d and a message on stderr", tc.grant, code, out.String(), errb.String(), tc.exit)
		}
		out.Reset()
		if code := run([]string{"fetch", tc.grant, "f", "--json"}, &out, &errb); code != tc.exit || !strings.Contains(out.String(), `"code":"`+tc.code+`"`) || !strings.Contains(out.String(), `"ok":false`) {
			t.Errorf("%s --json: code %d, stdout %q", tc.grant, code, out.String())
		}
	}
}

func TestFetchNoNetworkForLocalFailure(t *testing.T) {
	// the daemon reports a locally failing grant as an IPC error on fetch_start
	// and never gets a fetch_status call
	p := shortHome(t)
	var mu sync.Mutex
	statusCalls := 0
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"fetch_start": func(context.Context, json.RawMessage) (any, error) {
			return nil, &ipc.Error{Code: "expired", Message: "the grant cannot be used: expired"}
		},
		"fetch_status": func(context.Context, json.RawMessage) (any, error) {
			mu.Lock()
			statusCalls++
			mu.Unlock()
			return nil, &ipc.Error{Code: "not_found", Message: "x"}
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "f"}, &out, &errb); code != exitError || !strings.Contains(errb.String(), "expired") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if statusCalls != 0 {
		t.Fatalf("%d fetch_status calls after a local failure", statusCalls)
	}
}

func TestFetchDetectsAChangeBetweenReads(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex
	calls := 0
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"fetch_start": func(context.Context, json.RawMessage) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			commit := strings.Repeat("a", 40)
			if calls > 1 {
				commit = strings.Repeat("b", 40) // the branch moved
			}
			chunk := make([]byte, capability.MaxReadBytes)
			return complete(map[string]any{"data": base64.StdEncoding.EncodeToString(chunk), "offset": 0, "size": 2 * capability.MaxReadBytes, "commit": commit}), nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "f", "--json"}, &out, &errb); code != exitError || !strings.Contains(out.String(), `"code":"changed"`) {
		t.Fatalf("code %d, stdout %q", code, out.String())
	}
}

func TestFetchGivesUpWhenTheDaemonStopsAnswering(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"fetch_start": func(context.Context, json.RawMessage) (any, error) {
			return daemon.FetchStatus{FetchID: "ft-1", State: "pending"}, nil
		},
		"fetch_status": func(context.Context, json.RawMessage) (any, error) {
			time.Sleep(200 * time.Millisecond)
			return daemon.FetchStatus{FetchID: "ft-1", State: "pending"}, nil
		},
	})
	begin := time.Now()
	var out, errb bytes.Buffer
	if code := run([]string{"fetch", "g-1", "f", "--timeout", "1"}, &out, &errb); code != exitWaitTimeout || !strings.Contains(errb.String(), "did not answer") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	if el := time.Since(begin); el < time.Second || el > 8*time.Second {
		t.Fatalf("gave up after %s, want just over --timeout 1", el)
	}
}

func TestFetchUsage(t *testing.T) {
	shortHome(t)
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"fetch"}, {"fetch", "--help"}} {
		out.Reset()
		if code := run(args, &out, &errb); code != exitOK || !strings.Contains(out.String(), "agentnet fetch <g-id>") {
			t.Errorf("%v: code %d, stdout %q", args, code, out.String())
		}
	}
	for _, bad := range [][]string{
		{"fetch", "g-1"},
		{"fetch", "g-1", "a", "b"},
		{"fetch", "g-1", "--stat"},
		{"fetch", "g-1", "--list", "a", "b"},
		{"fetch", "g-1", "--list", "--stat", "a"},
		{"fetch", "g-1", "--list", "--out", "x"},
		{"fetch", "g-1", "a", "--timeout", "0"},
		{"fetch", "g-1", "a", "--timeout", "301"},
		{"fetch", "g-1", "a", "--nope"},
	} {
		out.Reset()
		errb.Reset()
		if code := run(bad, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d", bad, code, exitUsage)
		}
	}
}
