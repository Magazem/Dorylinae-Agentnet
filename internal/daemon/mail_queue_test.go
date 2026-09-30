package daemon

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) lines(ev string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, ln := range strings.Split(s.b.String(), "\n") {
		if strings.Contains(ln, "event="+ev+" ") {
			out = append(out, ln)
		}
	}
	return out
}

// R55-F13 (review 55 R55-052): with the receiver held, 200 mail frames of
// 700 KiB keep the receive queue within 64 MiB, and the drops of a window
// make one mail_queue_drop line.
func TestMailQueueByteBound(t *testing.T) {
	old := mailDropWindow
	mailDropWindow = 100 * time.Millisecond
	t.Cleanup(func() { mailDropWindow = old })
	out := &syncBuf{}
	q := newMailInbox(slog.New(slog.NewTextHandler(out, nil)))
	t.Cleanup(q.flush)
	const size = 700 << 10
	dropped := 0
	for i := 0; i < 200; i++ {
		before := q.bytes.Load()
		q.push(envelope.Envelope{Type: mail.MailType, Payload: make([]byte, size)})
		if q.bytes.Load() == before {
			dropped++
		}
		if n := q.bytes.Load(); n > mailQueueBytes || len(q.ch) > mailQueue {
			t.Fatalf("queue holds %d bytes in %d envelopes", n, len(q.ch))
		}
	}
	want := 200 - mailQueueBytes/size
	if dropped != want || len(q.ch) != mailQueueBytes/size {
		t.Fatalf("dropped %d, queued %d; want %d dropped", dropped, len(q.ch), want)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(out.lines("mail_queue_drop")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no mail_queue_drop line")
		}
		time.Sleep(5 * time.Millisecond)
	}
	lines := out.lines("mail_queue_drop")
	if len(lines) != 1 || !strings.Contains(lines[0], " count=107 ") {
		t.Fatalf("drop lines %q, want one line with count=107", lines)
	}

	// The count bound still holds for small envelopes.
	for len(q.ch) > 0 {
		e := <-q.ch
		q.bytes.Add(-int64(len(e.Payload)))
	}
	for i := 0; i < mailQueue+10; i++ {
		q.push(envelope.Envelope{Type: mail.MailType, Payload: []byte{1}})
	}
	if len(q.ch) != mailQueue || q.bytes.Load() != mailQueue {
		t.Fatalf("queued %d envelopes, %d bytes; want %d", len(q.ch), q.bytes.Load(), mailQueue)
	}
}
