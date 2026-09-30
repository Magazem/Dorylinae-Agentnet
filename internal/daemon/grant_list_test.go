package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// A16 (R55-F13, review 55 R55-064): grant_list pages. 450 rows, three to each
// created value, read 200 at a time: 3 pages, newest first, every id once;
// the limit is checked and clamped, and a cursor the daemon did not issue is
// bad_request.
func TestGrantListPaging(t *testing.T) {
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	ctx := context.Background()
	const sid = "s-44444444444444444444444444444444"
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	type key struct {
		i  int
		id string
	}
	var rows []key
	for i := 0; i < 450; i++ {
		rec := capability.Record{ID: capability.NewID(), Direction: capability.DirectionIssued, Peer: peer, Session: sid,
			Action: capability.ActionFSRead, Label: "repo-ab12", Path: "/tmp/repo", Sensitive: true,
			Nbf: base, Exp: base.Add(2 * time.Hour), Token: `{}`, Created: base.Add(time.Duration(i/3) * time.Second)}
		if err := h.caps.InsertPending(ctx, rec); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, key{i / 3, rec.ID})
	}
	// Expected order: created descending, then id descending.
	want := make([]string, 0, len(rows))
	for g := 149; g >= 0; g-- {
		var ids []string
		for _, r := range rows {
			if r.i == g {
				ids = append(ids, r.id)
			}
		}
		for len(ids) > 0 {
			m := 0
			for j := range ids {
				if ids[j] > ids[m] {
					m = j
				}
			}
			want = append(want, ids[m])
			ids = append(ids[:m], ids[m+1:]...)
		}
	}
	list := func(p GrantListParams) (GrantListResult, error) {
		var res GrantListResult
		err := h.call("grant_list", p, &res)
		return res, err
	}
	n := func(v int) *int { return &v }

	var got []string
	var sizes []int
	cursor := ""
	for len(sizes) < 5 {
		res, err := list(GrantListParams{Session: sid, Limit: n(200), Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(res.Grants))
		for _, g := range res.Grants {
			got = append(got, g.ID)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(sizes) != 3 || sizes[0] != 200 || sizes[1] != 200 || sizes[2] != 50 {
		t.Fatalf("page sizes = %v, want [200 200 50]", sizes)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paged ids differ from the newest-first order (%d listed)", len(got))
	}

	if res, err := list(GrantListParams{Session: sid}); err != nil || len(res.Grants) != 200 || res.NextCursor == "" {
		t.Fatalf("default page: %d grants, next %q, %v; want 200 and a cursor", len(res.Grants), res.NextCursor, err)
	}
	if res, err := list(GrantListParams{Session: sid, Limit: n(1000)}); err != nil || len(res.Grants) != 450 || res.NextCursor != "" {
		t.Fatalf("limit 1000: %d grants, next %q, %v; want all 450 (clamped to 500), no cursor", len(res.Grants), res.NextCursor, err)
	}
	for _, p := range []GrantListParams{{Limit: n(0)}, {Limit: n(-1)}, {Cursor: "!!"}, {Cursor: "Zm9yZ2Vk"}, {Cursor: cursor + "x"}} {
		if _, err := list(p); ipcCode(err) != ipc.CodeBadRequest {
			t.Errorf("params %+v: err = %v, want bad_request", p, err)
		}
	}
}

// A16: 500 held views of the largest size a grant token allows (2 KiB: a
// 64-byte label, a 255-byte branch and the longest scope that still fits)
// come back as one page, within the 1 MiB IPC line.
func TestGrantListLargestPageFitsOneLine(t *testing.T) {
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	ctx := context.Background()
	const sid = "s-55555555555555555555555555555555"
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	g := capability.Grant{V: 1, ID: capability.NewID(), Iss: base64.RawURLEncoding.EncodeToString(pub), Aud: h.self, Session: sid,
		Action: capability.ActionGitRead, Nbf: now, Exp: now.Add(2 * time.Hour), Sensitive: true,
		Resource: capability.Resource{Kind: capability.KindGit, Label: strings.Repeat("l", 64), Branch: strings.Repeat("b", 255)}}
	var wire []byte
	for n := 1024; n > 0; n-- {
		g.Scope = strings.Repeat("s", n)
		tok, err := capability.Sign(priv, g)
		if err != nil {
			t.Fatal(err)
		}
		if wire, err = capability.Canonical(tok); err != nil {
			t.Fatal(err)
		}
		if len(wire) <= capability.MaxTokenBytes {
			break
		}
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		rec := capability.Record{ID: capability.NewID(), Peer: peer, Session: sid, Action: g.Action, Label: g.Resource.Label,
			Branch: g.Resource.Branch, Scope: g.Scope, Sensitive: true, Nbf: g.Nbf, Exp: g.Exp, Token: string(wire)}
		if err := h.caps.InsertHeldTx(ctx, tx, rec); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	lim := 500
	var res GrantListResult
	if err := h.call("grant_list", GrantListParams{Session: sid, Limit: &lim}, &res); err != nil {
		t.Fatalf("grant_list of 500 largest views: %v", err)
	}
	b, _ := json.Marshal(res)
	if len(res.Grants) != 500 || res.NextCursor != "" || len(b) >= 1<<20 {
		t.Fatalf("%d views, next %q, %d bytes; want 500 in one line", len(res.Grants), res.NextCursor, len(b))
	}
	t.Logf("500 views with %d-byte scope: %d bytes", len(g.Scope), len(b))
}
