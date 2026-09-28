//go:build !testhooks

package main

import (
	"flag"
	"io"
	"net/http"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// testHooks is empty in release builds: no test hook flag or route exists
// without the build tag testhooks (review 50 M7; see testhooks_on.go).
type testHooks struct{}

func registerTestHooks(*flag.FlagSet) testHooks { return testHooks{} }

func (testHooks) wrap(h http.Handler, _ *relay.Server, _ io.Writer) http.Handler { return h }
