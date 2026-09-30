package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// TestServeConnKeepsHTMLUnescaped: ipc.md §Framing says results are encoded
// with HTML escaping off. marshalResult does that, and serveConn must not
// re-escape the Response's json.RawMessage Result with a default encoder,
// which would turn each '<' into a six-byte escape (review 55 T6a-01).
func TestServeConnKeepsHTMLUnescaped(t *testing.T) {
	s := NewServer()
	s.Handle("x", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"text": strings.Repeat("<>&", 1000)}, nil
	})
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	go s.serveConn(context.Background(), b)
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := a.Write([]byte(`{"id":"1","method":"x"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReaderSize(a, 1<<20).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"<", ">", "&"} {
		if n := strings.Count(line, c); n != 1000 {
			t.Errorf("raw %q on the wire = %d, want 1000 (line is %d bytes)", c, n, len(line))
		}
	}
	if strings.Contains(line, `\u00`) {
		t.Errorf("response line holds an escape: %.80s", line)
	}
}
