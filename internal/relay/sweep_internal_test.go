package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Expiry sweep in bounded batches and WAL bound (R55-011), internal
// acceptance tests 2, 14, 15, 16 and 19 of Docs/review/66-r55-f2-spec.md.

// envInt reads a positive integer from the environment, or def.
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// expiredAt is an enqueue time already past the TTL at now.
func expiredAt(now time.Time) time.Time { return now.Add(-defaultQueueTTL - time.Hour) }

// fillRows inserts rows frames of frameSize bytes enqueued at at, spread over
// many senders and recipients, and rebuilds the totals.
func fillRows(t *testing.T, q *queue, at time.Time, rows, frameSize int, tag string) {
	t.Helper()
	frame := make([]byte, frameSize)
	var b byte
	for i := range frame {
		frame[i], b = b, b*131+7
	}
	old := at.UnixMilli()
	const perTx = 256
	for i := 0; i < rows; i += perTx {
		tx, err := q.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for j := i; j < i+perTx && j < rows; j++ {
			if _, err := tx.Exec(`INSERT INTO queue (to_key, from_key, id, enqueued, frame) VALUES (?, ?, ?, ?, ?)`,
				fmt.Sprintf("to-%05d", j%997), fmt.Sprintf("from-%05d", j/64), fmt.Sprintf("%s-%07d", tag, j), old, frame); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.rebuildTotals(); err != nil {
		t.Fatal(err)
	}
}

func rowCount(t *testing.T, q *queue) int {
	t.Helper()
	var n int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM queue`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Test 2 (C02-02 converted): a large expiry is deleted in batches of at most
// 32 rows with the production DSN; an add started during the sweep waits at
// most about one batch; the WAL ends within its 64 MiB limit. ZZ_MB (default
// 256) and ZZ_FRAME (default 1 MiB) scale it for a manual 1 GiB or 2 GiB run.
func TestQueueSweepBatched(t *testing.T) {
	mb := envInt("ZZ_MB", 256)
	frameSize := envInt("ZZ_FRAME", 1<<20)
	path := filepath.Join(testutil.TempDir(t), "q.db")
	now := time.Now()
	q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.close() }()
	var jsl int64
	if err := q.db.QueryRow(`PRAGMA journal_size_limit`).Scan(&jsl); err != nil || jsl != walSizeLimit {
		t.Fatalf("journal_size_limit = %d, %v; want %d", jsl, err, walSizeLimit)
	}
	rows := (mb << 20) / frameSize
	start := time.Now()
	fillRows(t, q, expiredAt(now), rows, frameSize, "id")
	if _, err := q.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	t.Logf("filled %d rows x %d B = %d MiB in %s", rows, frameSize, mb, time.Since(start).Round(time.Millisecond))

	var mu sync.Mutex
	var maxRows int64
	var maxBatch time.Duration
	batches := 0
	second := make(chan struct{})
	q.mu.Lock()
	q.sweepHook = func(i int) error {
		if i == 1 {
			close(second)
		}
		return nil
	}
	q.sweptHook = func(_ int, n int64, took time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		batches++
		maxRows, maxBatch = max(maxRows, n), max(maxBatch, took)
	}
	q.mu.Unlock()

	type result struct {
		n, bytes int64
		err      error
		took     time.Duration
	}
	swept := make(chan result, 1)
	start = time.Now()
	go func() {
		n, bytes, err := q.sweepExpired(sweepTickBudget, nil)
		if err == nil && bytes > walSizeLimit {
			_, err = q.truncateWAL()
		}
		swept <- result{n, bytes, err, time.Since(start)}
	}()
	<-second // the sweep is under way: an add must not wait for all of it
	addStart := time.Now()
	if err := q.add(envelope.Header{From: "late-from", To: "late-to", ID: "late"}, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	addTook := time.Since(addStart)
	r := <-swept
	if r.err != nil {
		t.Fatal(r.err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("sweep: %d rows, %d MiB in %d batches, %s; longest batch %s; add waited %s; wal %d MiB",
		r.n, r.bytes>>20, batches, r.took.Round(time.Millisecond), maxBatch.Round(time.Millisecond), addTook.Round(time.Millisecond), fileSize(path+"-wal")>>20)
	if r.n != int64(rows) || rowCount(t, q) != 1 {
		t.Fatalf("deleted %d of %d rows, %d left (want only the late add)", r.n, rows, rowCount(t, q)-1)
	}
	if q.total.n != 1 || q.total.bytes != int64(len("frame")) || len(q.senders) != 1 {
		t.Fatalf("totals after the sweep: %+v, %d senders; want only the late add", q.total, len(q.senders))
	}
	if maxRows > sweepBatchRows {
		t.Fatalf("a transaction deleted %d rows, want at most %d", maxRows, sweepBatchRows)
	}
	if bound := maxBatch + 500*time.Millisecond; addTook > bound {
		t.Fatalf("an add during the sweep took %s, want at most %s (the longest batch + 500 ms)", addTook, bound)
	}
	if wal := fileSize(path + "-wal"); wal > walSizeLimit+1<<20 {
		t.Fatalf("WAL is %d MiB after the sweep, want at most 65 MiB", wal>>20)
	}
}

// Test 14: a batch that fails rolls back only itself; the batches before it
// stay deleted, the totals equal a fresh scan, and the next sweep finishes.
func TestQueueSweepFailureKeepsProgress(t *testing.T) {
	now := time.Now()
	q, err := openQueue(filepath.Join(testutil.TempDir(t), "q.db"), defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.close() }()
	fillRows(t, q, expiredAt(now), 100, 1000, "id")
	failed := errors.New("injected")
	q.mu.Lock()
	q.sweepHook = func(i int) error {
		if i == 2 {
			return failed
		}
		return nil
	}
	q.mu.Unlock()
	n, bytes, err := q.sweepExpired(0, nil)
	if !errors.Is(err, failed) || n != 2*sweepBatchRows || bytes != 2*sweepBatchRows*1000 {
		t.Fatalf("sweep with a failing 3rd batch: %d rows, %d bytes, %v; want the first 2 batches and the error", n, bytes, err)
	}
	if left := rowCount(t, q); left != 100-2*sweepBatchRows {
		t.Fatalf("%d rows left, want %d", left, 100-2*sweepBatchRows)
	}
	q.mu.Lock()
	total, senders := q.total, map[string]usage{}
	for k, u := range q.senders {
		senders[k] = *u
	}
	q.sweepHook = nil
	q.mu.Unlock()
	if err := q.rebuildTotals(); err != nil {
		t.Fatal(err)
	}
	rebuilt := map[string]usage{}
	for k, u := range q.senders {
		rebuilt[k] = *u
	}
	if total != q.total || !reflect.DeepEqual(senders, rebuilt) {
		t.Fatalf("totals after the failed batch %+v differ from a scan %+v", total, q.total)
	}
	if n, err := q.sweep(); err != nil || n != 100-2*sweepBatchRows || rowCount(t, q) != 0 {
		t.Fatalf("next sweep: %d, %v; %d left", n, err, rowCount(t, q))
	}
}

// Test 15: with a tiny time budget each tick still deletes one batch and the
// expiry finishes over later ticks; Close during a sweep returns promptly,
// since the stop is checked between batches.
func TestQueueSweepTickBudget(t *testing.T) {
	now := time.Now()
	s := testServer(t, Options{Now: func() time.Time { return now }})
	fillRows(t, s.q, expiredAt(now), 100, 100, "id")
	s.sweepBudget = time.Nanosecond
	s.q.mu.Lock()
	s.q.sweepHook = func(int) error { time.Sleep(2 * time.Millisecond); return nil } // time moves on every batch
	s.q.mu.Unlock()
	for tick, want := range []int64{32, 32, 32, 4, 0} {
		if n, err := s.Sweep(); err != nil || n != want {
			t.Fatalf("tick %d: %d, %v; want %d", tick, n, err, want)
		}
	}

	// The sweep loop itself, every 10 ms: the rows expire only once the
	// clock moves, after the hook that slows each batch is in place.
	// The rows are written before the relay (and its loop) starts.
	path := filepath.Join(testutil.TempDir(t), "q.db")
	q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	fillRows(t, q, now, 50*sweepBatchRows, 10, "id")
	_ = q.close()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	s2, err := Open(Options{QueuePath: path, Now: func() time.Time { return time.Unix(0, clock.Load()) }, SweepInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var expired atomic.Bool
	running := make(chan struct{})
	var once sync.Once
	s2.q.mu.Lock()
	s2.q.sweepHook = func(int) error {
		if expired.Load() {
			once.Do(func() { close(running) })
			time.Sleep(50 * time.Millisecond) // 50 batches: 2.5 s for the whole expiry
		}
		return nil
	}
	s2.q.mu.Unlock()
	expired.Store(true)
	clock.Store(now.Add(defaultQueueTTL + time.Hour).UnixNano())
	<-running
	start := time.Now()
	s2.Close()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Close during a sweep took %s, want it to stop between batches", took)
	}
}

// Test 16: the sweep prunes the delivered mark of a key whose rows are all
// gone and keeps the mark of a key that still has rows.
func TestQueuePruneDelivered(t *testing.T) {
	now := time.Now()
	s := testServer(t, Options{Now: func() time.Time { return now }})
	gone, kept, from := testKey(t), testKey(t), testKey(t)
	for _, to := range []string{gone, kept} {
		for i := range 2 {
			if err := s.q.add(envelope.Header{From: from, To: to, ID: fmt.Sprintf("m-%d", i)}, []byte("frame")); err != nil {
				t.Fatal(err)
			}
		}
		if rows, _, err := s.q.claim(to, 0, 64, 1<<20); err != nil || len(rows) != 2 {
			t.Fatalf("claim: %d rows, %v", len(rows), err)
		}
	}
	for i := range 2 {
		if err := s.q.ack(gone, from, fmt.Sprintf("m-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Sweep(); err != nil {
		t.Fatal(err)
	}
	if h := s.q.mustDelivered(t, gone); h != 0 {
		t.Fatalf("the mark of a key with no rows survived: %d", h)
	}
	if h := s.q.mustDelivered(t, kept); h == 0 {
		t.Fatal("the mark of a key with rows was pruned")
	}
}

// Test 19 (review 66b M3): while another connection holds a read
// transaction, the TRUNCATE checkpoint after a large sweep returns at once
// instead of waiting for the busy timeout; after the reader ends, the next
// large sweep truncates the WAL.
func TestQueueCheckpointDoesNotWait(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "q.db")
	now := time.Now()
	s, err := Open(Options{QueuePath: path, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	const rows = 70 // just over the 64 MiB WAL limit
	fillRows(t, s.q, expiredAt(now), rows, 1<<20, "a")

	reader, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM queue`).Scan(&n); err != nil { // holds a read snapshot
		t.Fatal(err)
	}
	if got, err := s.Sweep(); err != nil || got != rows {
		t.Fatalf("sweep: %d, %v", got, err)
	}
	start := time.Now()
	truncated, err := s.q.truncateWAL()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the checkpoint waited %s with a reader holding a snapshot (busy timeout 5 s)", took)
	}
	if err != nil || truncated {
		t.Fatalf("checkpoint with a reader: truncated %v, %v; want skipped without error", truncated, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	fillRows(t, s.q, expiredAt(now), rows, 1<<20, "b")
	if got, err := s.Sweep(); err != nil || got != rows {
		t.Fatalf("second sweep: %d, %v", got, err)
	}
	if wal := fileSize(path + "-wal"); wal != 0 {
		t.Fatalf("WAL is %d bytes after a large sweep with no reader, want truncated", wal)
	}
	var busy int
	if err := s.q.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil || busy != busyTimeoutMS {
		t.Fatalf("busy_timeout after the checkpoint = %d, %v; want %d restored", busy, err, busyTimeoutMS)
	}
}
