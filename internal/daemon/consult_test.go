package daemon_test

// Ticket 2.5 acceptance through two real daemons (Docs/protocol/consult.md):
// the submit result's derived session id, the context caps on the IPC submit
// path, the one-step answer of a pending question (by request id or session
// id), the normal flow for everything else, and that context text is never
// audited.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// callCode runs an IPC method that is expected to fail and returns its error
// code and message ("" and "" when it succeeded).
func callCode(t *testing.T, n *harnessNode, method string, params any) (code, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	err := ipc.Call(ctx, n.p.Endpoint, method, params, &out)
	if err == nil {
		return "", ""
	}
	var ie *ipc.Error
	if !errors.As(err, &ie) {
		t.Fatalf("%s %s: %v", n.name, method, err)
	}
	return ie.Code, ie.Message
}

func consultPair(t *testing.T) (a, b *harnessNode, teamID string) {
	t.Helper()
	r := newHarnessRelay(t)
	a, b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	return a, b, harnessSharedTeam(t, a, b, "x")
}

func submitQuestion(t *testing.T, a, b *harnessNode, teamID string, files ...daemon.ContextParam) daemon.RequestSubmitResult {
	t.Helper()
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "question", Team: teamID, Title: "q", Brief: "Is this safe?", Context: files,
	}, &res)
	harnessWait(t, "B to store the question", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+res.ID+`' AND state = 'pending'`) == 1
	})
	return res
}

func TestConsultSubmitResultCarriesSession(t *testing.T) {
	a, b, teamID := consultPair(t)
	for _, typ := range []string{"review", "task", "question"} {
		var res daemon.RequestSubmitResult
		a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: typ, Team: teamID, Title: typ, Brief: "What: x"}, &res)
		want := worksession.DeriveID(a.key, b.key, res.ID)
		if res.Session != want {
			t.Fatalf("%s: submit session = %q, want %q", typ, res.Session, want)
		}
		// The session does not exist yet, but its id resolves to the request.
		var show daemon.RequestShowResult
		a.call("request_show", map[string]any{"id": res.Session}, &show)
		if show.Request.ID != res.ID || show.Request.Session != nil {
			t.Fatalf("%s: request_show by session id = %+v", typ, show.Request)
		}
		if code, _ := callCode(t, a, "ws_show", map[string]any{"id": res.Session}); code != daemon.CodeUnknownSession {
			t.Fatalf("%s: ws_show before accept = %q, want unknown_session", typ, code)
		}
	}
	if code, _ := callCode(t, a, "request_show", map[string]any{"id": "s-00000000000000000000000000000001"}); code != daemon.CodeUnknownRequest {
		t.Fatalf("request_show of an unrelated session id = %q, want unknown_request", code)
	}
}

// consultSize returns the canonical size of the question the daemon will
// build for these parameters (the id and times have fixed lengths).
func consultSize(t *testing.T, a, b *harnessNode, teamID string, files []daemon.ContextParam) int {
	t.Helper()
	r := &request.Request{
		V: 1, ID: request.NewID(), From: a.key, To: b.key, Team: teamID, Type: request.TypeQuestion,
		Title: "q", Brief: "Is this safe?", Urgency: request.UrgencyNormal, Created: time.Now().UTC().Truncate(time.Second),
	}
	for _, f := range files {
		r.Context = append(r.Context, request.ContextFile{Name: f.Name, Text: f.Text})
	}
	canon, err := request.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	return len(canon)
}

func TestConsultContextCapsIPC(t *testing.T) {
	a, b, teamID := consultPair(t)
	one := func(text string) []daemon.ContextParam { return []daemon.ContextParam{{Name: "a.go", Text: text}} }
	submit := func(typ string, files []daemon.ContextParam) (string, string) {
		return callCode(t, a, "request_submit", daemon.RequestSubmitParams{
			To: b.key, Type: typ, Team: teamID, Title: "q", Brief: "Is this safe?", Context: files,
		})
	}

	// An empty array is refused too (sent raw: the typed param omits it).
	if code, msg := callCode(t, a, "request_submit", map[string]any{
		"to": b.key, "type": "question", "team": teamID, "title": "q", "brief": "b", "context": []any{},
	}); code != "bad_request" || !strings.Contains(msg, "context") {
		t.Errorf("empty context array: code %q message %q, want bad_request", code, msg)
	}

	many := func(n int) []daemon.ContextParam {
		out := make([]daemon.ContextParam, n)
		for i := range out {
			out[i] = daemon.ContextParam{Name: "f" + string(rune('a'+i)) + ".go", Text: "x"}
		}
		return out
	}
	for name, tc := range map[string]struct {
		typ   string
		files []daemon.ContextParam
		want  string
		field string
	}{
		"8 files":        {"question", many(8), "", ""},
		"9 files":        {"question", many(9), "bad_request", "context"},
		"65536 bytes":    {"question", one(strings.Repeat("a", 65536)), "", ""},
		"65537 bytes":    {"question", one(strings.Repeat("a", 65537)), "bad_request", "context[0].text"},
		"control char":   {"question", one("a\x1bb"), "bad_request", "context[0].text"},
		"context review": {"review", one("x"), "bad_request", "context"},
		"context task":   {"task", one("x"), "bad_request", "context"},
		"path in name":   {"question", []daemon.ContextParam{{Name: "dir/a.go", Text: "x"}}, "bad_request", "context[0].name"},
	} {
		code, msg := submit(tc.typ, tc.files)
		if code != tc.want || !strings.Contains(msg, tc.field) {
			t.Errorf("%s: code %q message %q, want %q mentioning %q", name, code, msg, tc.want, tc.field)
		}
	}

	// CRLF in a context text is normalised to LF by the daemon.
	var crlf daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "question", Team: teamID, Title: "q", Brief: "b", Context: one("a\r\nb\r\n")}, &crlf)
	var shown daemon.RequestShowResult
	a.call("request_show", map[string]any{"id": crlf.ID}, &shown)
	if len(shown.Request.Context) != 1 || shown.Request.Context[0].Text != "a\nb\n" {
		t.Fatalf("stored context = %+v, want CRLF turned into LF", shown.Request.Context)
	}

	// The total: exactly 327680 canonical bytes is accepted, 327681 is not.
	build := func(total int) []daemon.ContextParam {
		files := make([]daemon.ContextParam, 0, 5)
		for i := 0; i < 4; i++ {
			files = append(files, daemon.ContextParam{Name: "f" + string(rune('a'+i)) + ".go", Text: strings.Repeat("a", 65536)})
		}
		files = append(files, daemon.ContextParam{Name: "tuned.go", Text: "a"})
		base := consultSize(t, a, b, teamID, files)
		files[4].Text = strings.Repeat("a", 1+total-base)
		if got := consultSize(t, a, b, teamID, files); got != total {
			t.Fatalf("built %d bytes, want %d", got, total)
		}
		return files
	}
	if code, msg := submit("question", build(request.MaxQuestionBody+1)); code != daemon.CodeRequestTooLarge {
		t.Fatalf("327681 bytes: code %q (%s), want request_too_large", code, msg)
	}
	var big daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "question", Team: teamID, Title: "q", Brief: "Is this safe?", Context: build(request.MaxQuestionBody)}, &big)
	harnessWait(t, "B to store the largest question", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+big.ID+`'`) == 1
	})
	// The same size without context is over the ordinary cap: a brief cannot get there,
	// but a review with context is refused before any size question.
	if code, _ := submit("review", build(request.MaxQuestionBody)); code != "bad_request" {
		t.Fatalf("review with 327680 bytes of context: code %q, want bad_request", code)
	}
}

func TestConsultAnswerInOneStep(t *testing.T) {
	a, b, teamID := consultPair(t)
	const ctxMarker = "CTX-MARKER-4d2e"
	q := submitQuestion(t, a, b, teamID, daemon.ContextParam{Name: "outbox.go", Text: "// " + ctxMarker + "\n"})

	// B's views: the list shows sizes only, show has the text.
	var inbox daemon.RequestListResult
	b.call("inbox_list", map[string]any{}, &inbox)
	if len(inbox.Requests) != 1 || inbox.Requests[0].ContextFiles == nil || *inbox.Requests[0].ContextFiles != 1 ||
		*inbox.Requests[0].ContextBytes != len("// "+ctxMarker+"\n") || len(inbox.Requests[0].Context) != 0 {
		t.Fatalf("inbox_list = %+v", inbox.Requests)
	}
	var shown daemon.RequestShowResult
	b.call("request_show", map[string]any{"id": q.ID}, &shown)
	if len(shown.Request.Context) != 1 || shown.Request.Context[0].Name != "outbox.go" || !strings.Contains(shown.Request.Context[0].Text, ctxMarker) {
		t.Fatalf("request_show on B = %+v", shown.Request)
	}

	// Answer by the derived SESSION id, before any session exists on B; the
	// result carries no status, so it defaults to n/a.
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": q.Session, "result": map[string]any{"output": "yes\n"}}, &res)
	if res.Session.ID != q.Session || res.Session.State != "open" || res.Session.Result == nil ||
		res.Session.Result.Status != "n/a" || res.MailID == "" {
		t.Fatalf("ws_result = %+v", res)
	}
	b.call("request_show", map[string]any{"id": q.ID}, &shown)
	if shown.Request.State != "accepted" || shown.Request.Session == nil || shown.Request.Session.ID != q.Session {
		t.Fatalf("B request after the answer = %+v", shown.Request)
	}

	harnessWait(t, "A to hold the answer", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+q.Session+`' AND state = 'awaiting_result'`) == 1
	})
	var wait daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": q.Session}, &wait)
	if wait.Session.Result == nil || wait.Session.Result.Output != "yes\n" {
		t.Fatalf("A session = %+v", wait.Session)
	}
	var acc daemon.SessionResult
	a.call("ws_accept_result", map[string]any{"id": q.Session}, &acc)
	if acc.Session.State != "closed" {
		t.Fatalf("accept-result = %+v", acc)
	}
	harnessWait(t, "both requests to complete", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND state = 'completed'`) == 1 &&
			b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'completed'`) == 1
	})

	// A second answer is bad_state; nothing about the audit holds the text.
	if code, _ := callCode(t, b, "ws_result", map[string]any{"id": q.ID, "result": map[string]any{"status": "n/a", "verification": "none"}}); code != daemon.CodeBadState {
		t.Fatalf("second answer: code %q, want bad_state", code)
	}
	for _, n := range []*harnessNode{a, b} {
		st, err := store.Open(context.Background(), n.p.DB)
		if err != nil {
			t.Fatal(err)
		}
		var all string
		err = st.DB().QueryRow(`SELECT COALESCE(group_concat(action || ' ' || detail, char(10)), '') FROM audit_events`).Scan(&all)
		_ = st.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(all, ctxMarker) || strings.Contains(all, "outbox.go") {
			t.Fatalf("%s audit holds context text or a file name:\n%s", n.name, all)
		}
		if !strings.Contains(all, `"context_files":1`) {
			t.Fatalf("%s audit has no context_files size:\n%s", n.name, all)
		}
	}
}

// Everything but a pending question keeps the normal flow: accept, then result.
func TestConsultNormalFlowElsewhere(t *testing.T) {
	a, b, teamID := consultPair(t)

	var task daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x"}, &task)
	harnessWait(t, "B to store the task", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+task.ID+`'`) == 1
	})
	// A pending task is not answered in one step, by request or session id.
	for _, id := range []string{task.ID, task.Session} {
		if code, _ := callCode(t, b, "ws_result", map[string]any{"id": id, "result": map[string]any{"status": "pass", "verification": "none"}}); code != daemon.CodeUnknownSession {
			t.Fatalf("result on a pending task by %s: code %q, want unknown_session", id, code)
		}
	}
	if b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'accepted'`) != 0 {
		t.Fatal("a refused result accepted the task")
	}
	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": task.ID}, &acc)
	// Outside the one-step answer, status stays required.
	if code, msg := callCode(t, b, "ws_result", map[string]any{"id": task.ID, "result": map[string]any{"verification": "none"}}); code != "bad_request" || !strings.Contains(msg, "status") {
		t.Fatalf("result without status on an accepted task: code %q (%s), want bad_request naming status", code, msg)
	}

	// A question accepted first follows the normal flow too.
	q := submitQuestion(t, a, b, teamID)
	b.call("request_accept", map[string]any{"id": q.ID}, &acc)
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": q.ID, "result": map[string]any{"status": "n/a", "output": "ok", "verification": "none"}}, &res)
	if res.Session.ID != q.Session || res.Session.Result == nil {
		t.Fatalf("normal-flow answer = %+v", res)
	}
}

// A refused result leaves the question pending: the answer is all-or-nothing.
func TestConsultRefusedAnswerLeavesQuestionPending(t *testing.T) {
	a, b, teamID := consultPair(t)
	q := submitQuestion(t, a, b, teamID)
	for name, params := range map[string]map[string]any{
		"bad status":  {"id": q.ID, "result": map[string]any{"status": "maybe"}},
		"too large":   {"id": q.ID, "result": map[string]any{"output": strings.Repeat(`"`, 32768)}},
		"unknown key": {"id": q.ID, "result": map[string]any{"mood": "good"}},
	} {
		if code, _ := callCode(t, b, "ws_result", params); code == "" {
			t.Fatalf("%s: the answer was accepted", name)
		}
	}
	if b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) != 1 ||
		b.count(`SELECT COUNT(*) FROM work_sessions`) != 0 || b.count(`SELECT COUNT(*) FROM outbox WHERE kind IN ('request.accept', 'ws.result')`) != 0 {
		t.Fatal("a refused answer changed B's state")
	}
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": q.ID, "result": map[string]any{"output": "fine"}}, &res)
	if res.Session.State != "open" {
		t.Fatalf("retry = %+v", res)
	}
}

// A declined consult ends a wait on the derived session id: request_show by
// that id is what `agentnet wait` polls before the session exists.
func TestConsultDeclinedIsVisibleBySessionID(t *testing.T) {
	a, b, teamID := consultPair(t)
	q := submitQuestion(t, a, b, teamID)
	var dec daemon.RequestLifecycleResult
	b.call("request_decline", map[string]any{"id": q.ID, "reason": "not now"}, &dec)
	harnessWait(t, "A to see the decline", func() bool {
		var show daemon.RequestShowResult
		a.call("request_show", map[string]any{"id": q.Session}, &show)
		return show.Request.State == "declined"
	})
}
