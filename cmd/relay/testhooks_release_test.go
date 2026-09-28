//go:build !testhooks

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestReleaseBuildHasNoTestHooks is review 50 M7: the build without the tag
// testhooks (the release and container configuration) has neither the bind
// hook's flag nor its route, and the built binary does not even contain them.
func TestReleaseBuildHasNoTestHooks(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &out, &errb); code != 0 || strings.Contains(out.String(), "testhook") {
		t.Fatalf("--help: code %d, mentions a test hook:\n%s", code, out.String())
	}
	if code := run(context.Background(), []string{"--listen", "127.0.0.1:0", "--testhook-bind"}, &out, &errb); code != 2 {
		t.Fatalf("--testhook-bind accepted by a release build: code %d", code)
	}

	url, _ := startRelay(t, "--accounts", "github", "--db", filepath.Join(testutil.TempDir(t), "relay.db"))
	base := "http://" + strings.TrimSuffix(strings.TrimPrefix(url, "ws://"), envelope.ConnectPath)
	resp, err := http.Post(base+"/testhook/bind", "application/x-www-form-urlencoded", strings.NewReader("user_code=AAAA-AAAA")) //nolint:gosec,noctx // fixed loopback test URL
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /testhook/bind on a release build: %d, want 404", resp.StatusCode)
	}

	bin, err := os.ReadFile(relayHelper(t)) // built by `go build` without tags
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"/testhook/bind", "testhook-bind"} {
		if bytes.Contains(bin, []byte(s)) {
			t.Fatalf("release binary contains %q", s)
		}
	}
}
