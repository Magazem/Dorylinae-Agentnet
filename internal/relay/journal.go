package relay

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// JournalEntry is one content-free line of the security journal
// (Docs/protocol/relay-hosted.md §3): every security-relevant change
// (unbind, account_delete, suspend/unsuspend, invite_redeem/invite_revoke,
// team_remove) so a restore can replay what happened after the backup it
// restores. Fields never carry an email or a payload, only ids and key
// prefixes.
type JournalEntry struct {
	Time   time.Time         `json:"ts"`
	Event  string            `json:"event"`
	Fields map[string]string `json:"fields,omitempty"`
}

// JournalWriter appends JournalEntry lines to an append-only sink (a file
// kept off the host, per the doc). It is safe for concurrent use.
type JournalWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJournalWriter wraps w, which the caller opens (typically
// os.O_APPEND|os.O_CREATE) and closes.
func NewJournalWriter(w io.Writer) *JournalWriter { return &JournalWriter{w: w} }

// Append writes one journal entry with the current time. fields must not
// contain an email address or a payload.
func (j *JournalWriter) Append(event string, fields map[string]string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	line, err := json.Marshal(JournalEntry{Time: time.Now().UTC(), Event: event, Fields: fields})
	if err != nil {
		return fmt.Errorf("marshal journal entry: %w", err)
	}
	line = append(line, '\n')
	_, err = j.w.Write(line)
	return err
}

// ReadJournal decodes every entry from r, in order.
func ReadJournal(r io.Reader) ([]JournalEntry, error) {
	dec := json.NewDecoder(r)
	var out []JournalEntry
	for dec.More() {
		var e JournalEntry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("decode journal entry %d: %w", len(out), err)
		}
		out = append(out, e)
	}
	return out, nil
}

// journalHandlers dispatches a journal event to the code that replays it
// into the relay database. Nothing is registered by this ticket (4.1a only
// adds the writer and the replay mechanism); 4.2a and 4.3a register handlers
// for "unbind", "account_delete", "suspend", "unsuspend", "invite_redeem",
// "invite_revoke" and "team_remove" once those tables exist.
var (
	journalMu       sync.Mutex
	journalHandlers = map[string]func(*sql.DB, JournalEntry) error{}
)

// RegisterJournalHandler wires event to fn, replacing any earlier handler
// for the same event. It is the extension point 4.2a/4.3a use to make
// --replay-journal restore their own state.
func RegisterJournalHandler(event string, fn func(*sql.DB, JournalEntry) error) {
	journalMu.Lock()
	defer journalMu.Unlock()
	journalHandlers[event] = fn
}

// ReplayJournal reads the journal file at journalPath and, for every entry
// at or after since, calls the handler registered for its event against the
// database at dbPath. An entry whose event has no registered handler is
// counted as skipped, not an error, so a restore predates the wave that
// wires a given event.
func ReplayJournal(dbPath, journalPath string, since time.Time) (applied, skipped int, err error) {
	f, err := os.Open(journalPath) //nolint:gosec // journalPath is an operator-supplied CLI path (relay restore --replay-journal), as intended
	if err != nil {
		return 0, 0, fmt.Errorf("open journal: %w", err)
	}
	defer func() { _ = f.Close() }()
	entries, err := ReadJournal(f)
	if err != nil {
		return 0, 0, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, 0, fmt.Errorf("open relay database: %w", err)
	}
	defer func() { _ = db.Close() }()

	journalMu.Lock()
	handlers := make(map[string]func(*sql.DB, JournalEntry) error, len(journalHandlers))
	for k, v := range journalHandlers {
		handlers[k] = v
	}
	journalMu.Unlock()

	for _, e := range entries {
		if e.Time.Before(since) {
			continue
		}
		h, ok := handlers[e.Event]
		if !ok {
			skipped++
			continue
		}
		if err := h(db, e); err != nil {
			return applied, skipped, fmt.Errorf("replay %s at %s: %w", e.Event, e.Time.Format(time.RFC3339), err)
		}
		applied++
	}
	return applied, skipped, nil
}
