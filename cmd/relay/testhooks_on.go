//go:build testhooks

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// testHookBindPath completes a bind without the browser: it stands in for
// the confirm page in tests (Docs/protocol/accounts.md, review 50 M7). This
// file is compiled only with the build tag testhooks; release and container
// builds never set it, and TestReleaseBuildHasNoTestHooks checks that.
const testHookBindPath = "/testhook/bind"

type testHooks struct{ bind *bool }

func registerTestHooks(fs *flag.FlagSet) testHooks {
	return testHooks{bind: fs.Bool("testhook-bind", false, "TEST BUILD ONLY: serve "+testHookBindPath+", which binds a key to an account without the browser")}
}

// wrap serves the enabled hooks in front of h.
func (t testHooks) wrap(h http.Handler, rs *relay.Server, stderr io.Writer) http.Handler {
	if t.bind == nil || !*t.bind {
		return h
	}
	_, _ = fmt.Fprintf(stderr, "%s: WARNING: test hooks enabled (%s); never run this build in production\n", name, testHookBindPath)
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc(testHookBindPath, func(w http.ResponseWriter, r *http.Request) {
		testHookBind(w, r, rs)
	})
	return mux
}

// testHookBind handles POST user_code=…&subject=…&display=…[&provider=github][&action=deny]:
// the account (provider, subject) is created if needed, then the pending bind
// with that user code is confirmed for it (or denied). It answers 200 with
// the account id, 404 for an unknown code, 409 for a full account.
func testHookBind(w http.ResponseWriter, r *http.Request, rs *relay.Server) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	req, err := rs.PendingBind(r.PostForm.Get("user_code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if r.PostForm.Get("action") == "deny" {
		if err := rs.DenyBind(req.Ref); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintln(w, "denied")
		return
	}
	provider := r.PostForm.Get("provider")
	if provider == "" {
		provider = "github"
	}
	acc, err := rs.EnsureAccount(provider, r.PostForm.Get("subject"), r.PostForm.Get("display"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch err := rs.ConfirmBind(req.Ref, acc); {
	case errors.Is(err, relay.ErrAccountKeysFull), errors.Is(err, relay.ErrAlreadyBound):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, acc) //nolint:gosec // test build only; plain text; acc is a relay-generated acc_ id
	}
}
