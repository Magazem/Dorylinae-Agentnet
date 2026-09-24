package daemon_test

// Ticket 2.3c acceptance (Docs/review/23-phase2-tickets.md "2.3c"): the holder's
// fetch client against a live grantor, through two daemons and a relay. Grants
// are approved through the fake approval window; nothing here writes to a
// database except through IPC.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// bFetch runs one fetch on B (fetch_start, then fetch_status while pending).
func (e *qEnv) bFetch(p daemon.FetchStartParams) (daemon.FetchStatus, error) {
	var st daemon.FetchStatus
	if err := ipcCall(e.b, "fetch_start", p, &st); err != nil {
		return st, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for st.State == "pending" {
		if time.Now().After(deadline) {
			return st, errors.New("the fetch is still pending")
		}
		if err := ipcCall(e.b, "fetch_status", daemon.FetchStatusParams{FetchID: st.FetchID}, &st); err != nil {
			return st, err
		}
	}
	return st, nil
}

// failure returns the error code a fetch ends with, whether it fails at
// fetch_start (a local check) or in the final fetch_status.
func (e *qEnv) failure(t *testing.T, p daemon.FetchStartParams) string {
	t.Helper()
	st, err := e.bFetch(p)
	if err != nil {
		return errCode(err)
	}
	if st.State != "failed" || st.Error == nil {
		t.Fatalf("fetch = %+v, want a failure", st)
	}
	return st.Error.Code
}

// fsGrant opens a session, issues an fs.read grant on a fresh directory holding
// files, and waits until B holds it.
func fsGrant(t *testing.T, files map[string][]byte) (e *qEnv, sid, gid string) {
	t.Helper()
	e = newQEnv(t)
	_, sid = openSession(t, e.a, e.b, e.teamID, "fetch work")
	dir := qDir(t)
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gid = e.grant(t, sid, "fs.read", dir, false)
	harnessWait(t, "B to hold the grant", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM grants WHERE id = '`+gid+`' AND direction = 'held' AND state = 'active'`) == 1
	})
	return e, sid, gid
}

// TestFetchLargeFileByteIdentical: B reads a 3 MiB file in 256 KiB reads (each
// of 8 fragments) and gets the same bytes; stat and list agree.
func TestFetchLargeFileByteIdentical(t *testing.T) {
	big := make([]byte, 3<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	e, _, gid := fsGrant(t, map[string][]byte{"big.bin": big, "small.txt": []byte("hello")})

	st, err := e.bFetch(daemon.FetchStartParams{Grant: gid, Op: capability.OpStat, Path: "big.bin"})
	if err != nil || st.State != "complete" {
		t.Fatalf("stat = %+v, %v", st, err)
	}
	var stat struct {
		Entry capability.Entry `json:"entry"`
	}
	if err := json.Unmarshal(st.Result, &stat); err != nil || stat.Entry.Type != "file" || stat.Entry.Size == nil || *stat.Entry.Size != int64(len(big)) {
		t.Fatalf("stat result = %s (%v)", st.Result, err)
	}
	st, err = e.bFetch(daemon.FetchStartParams{Grant: gid, Op: capability.OpList})
	if err != nil || st.State != "complete" {
		t.Fatalf("list = %+v, %v", st, err)
	}
	var list struct {
		Entries []capability.Entry `json:"entries"`
	}
	if err := json.Unmarshal(st.Result, &list); err != nil || len(list.Entries) != 2 || list.Entries[0].Name != "big.bin" || list.Entries[1].Name != "small.txt" {
		t.Fatalf("list result = %s (%v)", st.Result, err)
	}

	var got []byte
	reads := 0
	for size := int64(-1); size < 0 || int64(len(got)) < size; reads++ {
		st, err := e.bFetch(daemon.FetchStartParams{Grant: gid, Op: capability.OpRead, Path: "big.bin", Offset: int64(len(got)), Length: capability.MaxReadBytes})
		if err != nil || st.State != "complete" {
			t.Fatalf("read at %d = %+v, %v", len(got), st, err)
		}
		var r struct {
			Data string `json:"data"`
			Size int64  `json:"size"`
		}
		if err := json.Unmarshal(st.Result, &r); err != nil {
			t.Fatal(err)
		}
		chunk, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil || len(chunk) == 0 {
			t.Fatalf("read at %d: %d bytes, %v", len(got), len(chunk), err)
		}
		size = r.Size
		got = append(got, chunk...)
	}
	if reads != 12 || !bytes.Equal(got, big) {
		t.Fatalf("%d reads, %d bytes, equal = %v; want 12 reads of the 3 MiB file", reads, len(got), bytes.Equal(got, big))
	}
}

// TestRevokeNextFetchFails is the 2.3 acceptance: after `revoke`, the next
// fetch fails within one second, measured from the revoke IPC return.
func TestRevokeNextFetchFails(t *testing.T) {
	e, _, gid := fsGrant(t, map[string][]byte{"f.txt": []byte("secret")})
	p := daemon.FetchStartParams{Grant: gid, Op: capability.OpStat, Path: "f.txt"}
	if st, err := e.bFetch(p); err != nil || st.State != "complete" {
		t.Fatalf("stat before the revoke = %+v, %v", st, err)
	}

	var rev daemon.GrantRevokeResult
	e.a.call("grant_revoke", daemon.GrantRevokeParams{ID: gid}, &rev)
	start := time.Now()
	code := e.failure(t, p)
	elapsed := time.Since(start)
	if code != capability.CodeRevoked {
		t.Fatalf("the fetch after the revoke failed with %q, want revoked", code)
	}
	if elapsed >= time.Second {
		t.Fatalf("the failing fetch took %s after the revoke returned, want under 1 s", elapsed)
	}

	// Once the grant.revoke mail reaches B, the row is revoked and the fetch
	// fails locally (no message to A).
	harnessWait(t, "B's row to be revoked", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM grants WHERE id = '`+gid+`' AND state = 'revoked'`) == 1
	})
	var st daemon.FetchStatus
	if err := ipcCall(e.b, "fetch_start", p, &st); errCode(err) != capability.CodeRevoked {
		t.Fatalf("fetch_start on a revoked row = %+v, %v; want a local revoked error", st, err)
	}
}

// TestFetchGrantorStoppedTimesOut: with the grantor stopped a fetch ends with
// `timeout` after its timeout, and a third fetch in flight is refused locally
// (the grantor allows two per holder).
func TestFetchGrantorStoppedTimesOut(t *testing.T) {
	e, _, gid := fsGrant(t, map[string][]byte{"f.txt": []byte("x")})
	p := daemon.FetchStartParams{Grant: gid, Op: capability.OpStat, Path: "f.txt", TimeoutS: 3}
	if st, err := e.bFetch(p); err != nil || st.State != "complete" {
		t.Fatalf("stat while A runs = %+v, %v", st, err)
	}
	e.a.stop()

	begin := time.Now()
	var first, second daemon.FetchStatus
	if err := ipcCall(e.b, "fetch_start", p, &first); err != nil || first.State != "pending" {
		t.Fatalf("first fetch to the stopped grantor = %+v, %v; want pending", first, err)
	}
	if err := ipcCall(e.b, "fetch_start", p, &second); err != nil || second.State != "pending" {
		t.Fatalf("second fetch = %+v, %v; want pending", second, err)
	}
	var third daemon.FetchStatus
	if err := ipcCall(e.b, "fetch_start", p, &third); errCode(err) != capability.CodeRateLimited {
		t.Fatalf("third fetch in flight = %+v, %v; want rate_limited", third, err)
	}
	for first.State == "pending" {
		if time.Since(begin) > 15*time.Second {
			t.Fatal("the fetch did not end")
		}
		if err := ipcCall(e.b, "fetch_status", daemon.FetchStatusParams{FetchID: first.FetchID}, &first); err != nil {
			t.Fatal(err)
		}
	}
	if first.State != "failed" || first.Error == nil || first.Error.Code != daemon.CodeFetchTimeout {
		t.Fatalf("fetch = %+v, want failed with timeout", first)
	}
	if el := time.Since(begin); el < 2500*time.Millisecond {
		t.Fatalf("timeout after %s, want about 3 s", el)
	}
}
