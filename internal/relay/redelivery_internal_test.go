package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Redelivery budget and delivered mark H (R55-F2), internal acceptance tests
// 6, 7, 8, 9, 13 and 20 of Docs/review/66-r55-f2-spec.md.

// keyConn returns a socketless connection of key from prefix, registered on
// s as a new connection: draining, cursor 0.
func keyConn(t *testing.T, s *Server, key, prefix string) *conn {
	t.Helper()
	c := newConn(nil, key, 64, prefix)
	c.led, c.maxBytes, c.ctx = s.led, s.lim.connBuffer, context.Background()
	s.mu.Lock()
	s.conns[key] = c
	s.mu.Unlock()
	return c
}

// drainAll runs c's drain to the end, inline first as serve does, and
// returns the frames handed to c's buffer, which it empties.
func drainAll(s *Server, c *conn) int {
	if s.drainStep(c, false) {
		for s.drainStep(c, true) {
		}
	}
	n := 0
	for {
		select {
		case f := <-c.out:
			c.written(f)
			n++
		default:
			return n
		}
	}
}

// queueRows adds n rows of size bytes for to, from one sender.
func queueRows(t *testing.T, q *queue, to, prefix string, n, size int) {
	t.Helper()
	from := testKey(t)
	for i := range n {
		if err := q.add(envelope.Header{From: from, To: to, ID: fmt.Sprintf("%s-%d", prefix, i)}, make([]byte, size)); err != nil {
			t.Fatal(err)
		}
	}
}

// noBufferLimits turns off the memory bounds, so a socketless connection's
// drain never waits for room.
func noBufferLimits(o Options) Options {
	o.ConnBufferBytes, o.MaxInflight = -1, -1
	return o
}

// Test 6 (review 66b M1): with the key's budget spent, a reconnect reads no
// frame of an old row from the database, on its first batch either; only
// the probe touches old rows. A new row is still read and delivered.
func TestQueueNoFrameReadAfterSkip(t *testing.T) {
	s := testServer(t, noBufferLimits(Options{QueueRedeliverPerKey: 1 << 20, QueueRedeliverPerPrefix: -1}))
	key := testKey(t)
	queueRows(t, s.q, key, "old", 8, 300<<10)
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 8 {
		t.Fatalf("first connection got %d frames, want 8", n)
	}
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n == 0 || n == 8 {
		t.Fatalf("second connection got %d redeliveries, want some within the 1 MiB budget", n)
	}

	var mu sync.Mutex
	var read []int64
	s.q.mu.Lock()
	s.q.readHook = func(_ string, rows []queued) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			read = append(read, r.seq)
		}
	}
	s.q.mu.Unlock()
	h := s.q.mustDelivered(t, key)
	for range 5 {
		if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 0 {
			t.Fatalf("a reconnect got %d frames with the budget spent", n)
		}
	}
	mu.Lock()
	if len(read) != 0 {
		t.Fatalf("reconnects with the budget spent read frames of rows %v", read)
	}
	mu.Unlock()

	queueRows(t, s.q, key, "new", 1, 100)
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 1 {
		t.Fatalf("the new row was not delivered: %d frames", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(read) != 1 || read[0] <= h {
		t.Fatalf("read rows %v, want only the new row above H=%d", read, h)
	}
}

func (q *queue) mustDelivered(t *testing.T, key string) int64 {
	t.Helper()
	h, err := q.delivered(key)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Test 7: H survives a restart, so unacked rows are redeliveries afterwards;
// and two overlapping drains of one key (a replacement) never lower H and
// never both get a row as a first delivery.
func TestQueueHighWaterPersistentAndMonotonic(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "relay.db")
	key := testKey(t)
	s1, err := Open(noBufferLimits(Options{QueuePath: path}))
	if err != nil {
		t.Fatal(err)
	}
	queueRows(t, s1.q, key, "m", 4, 1000)
	if n := drainAll(s1, keyConn(t, s1, key, "10.2.0.0")); n != 4 {
		t.Fatalf("first delivery: %d frames", n)
	}
	h := s1.q.mustDelivered(t, key)
	s1.mu.Lock()
	clear(s1.conns)
	s1.mu.Unlock()
	s1.Close()

	s2, err := Open(noBufferLimits(Options{QueuePath: path}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s2.mu.Lock()
		clear(s2.conns)
		s2.mu.Unlock()
		s2.Close()
	})
	if got := s2.q.mustDelivered(t, key); got != h || h == 0 {
		t.Fatalf("H after restart = %d, want %d", got, h)
	}
	if n := drainAll(s2, keyConn(t, s2, key, "10.2.0.0")); n != 4 {
		t.Fatalf("after restart: %d frames", n)
	}
	if got := s2.redeliveredBytes.Load(); got != 4000 {
		t.Fatalf("after restart %d bytes counted as redelivered, want all 4000", got)
	}

	// Two drains of one key claiming at once, while rows keep arriving.
	other := testKey(t)
	var mu sync.Mutex
	firsts := map[int64]int{}
	var wg sync.WaitGroup
	stop, added := make(chan struct{}), make(chan struct{})
	lowered := make(chan int64, 1)
	watched := make(chan struct{})
	go func() { // H never goes down
		defer close(watched)
		var last int64
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
			h, err := s2.q.delivered(other)
			if err == nil && h < last {
				select {
				case lowered <- h:
				default:
				}
			}
			last = max(last, h)
		}
	}()
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cursor int64
			idle := 0
			for {
				rows, high, err := s2.q.claim(other, cursor, 4, 1<<20)
				if err != nil {
					t.Error(err)
					return
				}
				switch {
				case rows != nil:
					mu.Lock()
					for _, r := range rows {
						firsts[r.seq]++
					}
					mu.Unlock()
					cursor, idle = rows[len(rows)-1].seq, 0
				case cursor < high:
					cursor = high // skipped: a redelivery, not a first delivery
				default:
					select {
					case <-added:
						if idle++; idle > 3 {
							return
						}
					default:
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	queueRows(t, s2.q, other, "c", 60, 100)
	close(added)
	wg.Wait()
	close(stop)
	<-watched
	select {
	case h := <-lowered:
		t.Fatalf("H went down to %d", h)
	default:
	}
	if len(firsts) != 60 {
		t.Fatalf("%d rows were first-delivered, want 60", len(firsts))
	}
	for seq, n := range firsts {
		if n != 1 {
			t.Fatalf("row %d was a first delivery %d times", seq, n)
		}
	}
}

// Test 8: the H rule relies on every new row getting a seq above H at the
// time it is added, also while a drain of its recipient is claiming.
func TestQueueNewRowsAboveHighWater(t *testing.T) {
	q, err := openQueue(filepath.Join(testutil.TempDir(t), "q.db"), time.Hour, 1000, 32<<20, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.close() }()
	key, from := testKey(t), testKey(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { // a drain claiming everything as it arrives
		defer close(done)
		var cursor int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			if rows, _, err := q.claim(key, cursor, 64, 1<<20); err == nil && rows != nil {
				cursor = rows[len(rows)-1].seq
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := range 200 {
		h := q.mustDelivered(t, key)
		id := fmt.Sprintf("m-%d", i)
		if err := q.add(envelope.Header{From: from, To: key, ID: id}, []byte("x")); err != nil {
			t.Fatal(err)
		}
		var seq int64
		if err := q.db.QueryRow(`SELECT seq FROM queue WHERE id = ?`, id).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if seq <= h {
			t.Fatalf("row %s got seq %d, not above H=%d", id, seq, h)
		}
	}
}

// withMigrations runs f with relayMigrations cut to the first n, as a binary
// that predates the later ones.
func withMigrations(n int, f func()) {
	all := relayMigrations
	relayMigrations = all[:n]
	defer func() { relayMigrations = all }()
	f()
}

// Test 9: an R2 database with queued rows gets queue_delivered, its rows are
// first deliveries, and backup, restore and relay admin still work on it.
func TestRelayMigrationR3(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "relay.db")
	key := testKey(t)
	withMigrations(2, func() {
		q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		queueRows(t, q, key, "r2", 3, 100)
		var n int
		if err := q.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'queue_delivered'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("an R2 database has queue_delivered: %d, %v", n, err)
		}
		_ = q.close()
	})

	s, err := Open(noBufferLimits(Options{QueuePath: path}))
	if err != nil {
		t.Fatalf("open the R2 database: %v", err)
	}
	var version int
	if err := s.q.db.QueryRow(`SELECT MAX(version) FROM relay_migrations`).Scan(&version); err != nil || version != 3 {
		t.Fatalf("schema version %d, %v; want 3", version, err)
	}
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 3 || s.redeliveredBytes.Load() != 0 {
		t.Fatalf("got %d frames, %d redelivered bytes; want 3 first deliveries", n, s.redeliveredBytes.Load())
	}
	h := s.q.mustDelivered(t, key)
	s.mu.Lock()
	clear(s.conns)
	s.mu.Unlock()
	s.Close()

	backup := filepath.Join(dir, "backup.db")
	if err := Backup(path, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.db")
	if err := Restore(backup, restored, false); err != nil {
		t.Fatal(err)
	}
	q, err := openQueue(restored, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.mustDelivered(t, key); got != h {
		t.Fatalf("restored H = %d, want %d (rows and H restore together)", got, h)
	}
	_ = q.close()
	a, err := OpenAdmin(restored, nil)
	if err != nil {
		t.Fatalf("relay admin on an R3 database: %v", err)
	}
	if _, err := a.Accounts(); err != nil {
		t.Fatalf("relay admin accounts: %v", err)
	}
	_ = a.Close()
}

// Test 20 (review 66b M2): after the documented rollback statements, a binary
// with R1-R2 opens the file and keeps its rows; the R3 binary re-creates the
// table and the rows are first deliveries again.
func TestRelayR3Rollback(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "relay.db")
	key := testKey(t)
	s, err := Open(noBufferLimits(Options{QueuePath: path}))
	if err != nil {
		t.Fatal(err)
	}
	queueRows(t, s.q, key, "m", 3, 100)
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 3 {
		t.Fatalf("first delivery: %d", n)
	}
	s.mu.Lock()
	clear(s.conns)
	s.mu.Unlock()
	s.Close()

	withMigrations(2, func() {
		if _, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now); err == nil ||
			!strings.Contains(err.Error(), "newer than this binary") {
			t.Fatalf("an R2 binary opened an R3 database: %v", err)
		}
	})
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM relay_migrations WHERE version = 3; DROP TABLE queue_delivered;`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	withMigrations(2, func() {
		q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
		if err != nil {
			t.Fatalf("the R2 binary after the rollback: %v", err)
		}
		if n, err := q.count(key); err != nil || n != 3 {
			t.Fatalf("rows after the rollback: %d, %v", n, err)
		}
		_ = q.close()
	})

	s, err = Open(noBufferLimits(Options{QueuePath: path}))
	if err != nil {
		t.Fatalf("re-upgrade: %v", err)
	}
	t.Cleanup(func() {
		s.mu.Lock()
		clear(s.conns)
		s.mu.Unlock()
		s.Close()
	})
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 3 || s.redeliveredBytes.Load() != 0 {
		t.Fatalf("after the re-upgrade: %d frames, %d redelivered bytes; want 3 first deliveries", n, s.redeliveredBytes.Load())
	}
}

// Test 13 (ack-forgery guard), end to end: stranger A's frame queued for
// victim V by a relay older than the payload check carries two "from" keys
// and the id of peer P's queued mail. V cannot parse it and acks it by the
// relay's own decoding, (A, id): P's mail to V stays queued. P's row is held
// back meanwhile (a redelivery with no budget), so V has not acked it itself.
func TestAckForgeryCannotDeletePeersMail(t *testing.T) {
	s := testServer(t, Options{QueueRedeliverPerKey: 1})
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	pPub, _, _ := ed25519.GenerateKey(rand.Reader)
	aPub, _, _ := ed25519.GenerateKey(rand.Reader)
	vPub, vPriv, _ := ed25519.GenerateKey(rand.Reader)
	p, a, v := envelope.KeyString(pPub), envelope.KeyString(aPub), envelope.KeyString(vPub)
	const id = "mail-1"
	ts0 := "2026-01-02T03:04:05Z"
	forged := `{"from":"` + p + `","to":"` + v + `","team":"t","type":"ping","ts":"` + ts0 + `","from":"` + a + `","id":"` + id + `","payload":1}`
	h, err := envelope.ParseHeader([]byte(forged))
	if err == nil || h.From != a || h.ID != id {
		t.Fatalf("the relay's decoding of the forged frame: %+v, %v; want from A", h, err)
	}
	mail, err := envelope.Envelope{From: p, To: v, Team: "t", Type: "ping", ID: id, TS: ts0, Payload: []byte("from P")}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.q.add(envelope.Header{From: p, To: v, ID: id}, mail); err != nil {
		t.Fatal(err)
	}
	if _, err := s.q.db.Exec(`INSERT INTO queue_delivered (to_key, seq) SELECT ?, MAX(seq) FROM queue`, v); err != nil {
		t.Fatal(err) // P's mail was delivered once before and not acked
	}
	// Stored as a relay before R55-F2 stored it: routing fields only.
	if err := s.q.add(h, []byte(forged)); err != nil {
		t.Fatal(err)
	}
	fromCount := func(from string) int {
		var n int
		if err := s.q.db.QueryRow(`SELECT COUNT(*) FROM queue WHERE to_key = ? AND from_key = ?`, v, from).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	var mu sync.Mutex
	var got []envelope.Envelope
	c, err := relayclient.New(relayclient.Config{
		URL:    "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath,
		Signer: relayclient.NewKeySigner(vPriv),
		OnEnvelope: func(e envelope.Envelope) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for fromCount(a) != 0 { // V acked the forged frame as (A, id)
		if time.Now().After(deadline) {
			t.Fatal("V never acked the frame it cannot parse")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := fromCount(p); n != 1 {
		t.Fatalf("P's queued mail to V: %d rows, want 1 (the ack deleted it)", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("V was handed %+v, want nothing (the forged frame is not handed up)", got)
	}
}
