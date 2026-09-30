// Package ipc implements the local socket API between the CLI and the daemon.
//
// Transport is a Unix domain socket (Linux, macOS) or a named pipe (Windows);
// framing and message shapes are specified in Docs/protocol/ipc.md.
package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Error codes returned in Response.Error.Code.
const (
	CodeBadRequest    = "bad_request"
	CodeUnknownMethod = "unknown_method"
	CodeInternal      = "internal"
	// CodeBusy answers a client while maxConns connections are being served.
	CodeBusy = "busy"
)

const (
	maxLine     = 1 << 20
	idleTimeout = 30 * time.Second
	// maxConns bounds the connections served at once; a client past it is
	// answered CodeBusy and closed at once (review 55, R55-083; review 77, M1).
	maxConns = 64
	// firstRequestTimeout bounds how long a new connection may take to send
	// its first complete request line, so silent connections cannot hold
	// the slots for the whole idle timeout (review 77, M1).
	firstRequestTimeout = 5 * time.Second
	// maxBusyReplies bounds the CodeBusy answers in progress; past it, a
	// client over maxConns is closed without one. busyTimeout bounds each.
	maxBusyReplies = 16
	busyTimeout    = 2 * time.Second
	// acceptLogEvery is how often a run of failing Accept calls is logged
	// (review 77, L3).
	acceptLogEvery = time.Minute
	// Accept errors other than a closed listener (EMFILE, a transient pipe
	// error) are retried after a delay that doubles from acceptBackoffFirst up
	// to acceptBackoffCap, instead of stopping the daemon (review 55, R55-083).
	acceptBackoffFirst = 5 * time.Millisecond
	acceptBackoffCap   = time.Second
)

// Request is a client call.
type Request struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Error is a machine-readable failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Response answers a Request.
type Response struct {
	ID     string          `json:"id,omitempty"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// HandlerFunc serves one method. Returning an *Error sends that error to the
// client; any other error is reported as CodeInternal without its text.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Server dispatches requests to handlers over a listener.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	// Activity, if set, is called once for every dispatched request (any
	// known method, including status), before its handler runs. The presence
	// sender (1.2c) uses it to detect the agent-active edge
	// (Docs/protocol/presence.md §Levels, ipc.md §Phase 1 methods).
	Activity func()
	// Logger receives handler panics and failing Accept calls; nil means
	// slog.Default().
	Logger *slog.Logger
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// NewServer returns a Server with no handlers.
func NewServer() *Server { return &Server{handlers: map[string]HandlerFunc{}} }

// Handle registers a method handler.
func (s *Server) Handle(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Methods returns every registered method name, in no particular order
// (Docs/review/23-phase2-tickets.md 2.2d acceptance, "a test lists every
// registered IPC method").
func (s *Server) Methods() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.handlers))
	for m := range s.handlers {
		out = append(out, m)
	}
	return out
}

// Serve accepts connections until ctx is cancelled or ln is closed; other
// Accept errors are retried. It serves at most maxConns connections at once
// and answers any further client CodeBusy, so a listener instance is always
// waiting and a client never hangs. It closes ln and waits for in-flight
// connections before returning.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	conns := map[net.Conn]struct{}{}
	var cmu sync.Mutex
	closing := false

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		_ = ln.Close()
		cmu.Lock()
		closing = true
		for c := range conns {
			_ = c.Close()
		}
		cmu.Unlock()
	}()

	slots := make(chan struct{}, maxConns)
	busy := make(chan struct{}, maxBusyReplies)
	var retry time.Duration
	failures := 0
	var lastLog time.Time
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return nil
			}
			failures++
			if failures == 1 || time.Since(lastLog) >= acceptLogEvery {
				lastLog = time.Now()
				s.logger().Error("ipc: accept failed; retrying", "event", "ipc_accept_error", "error", err, "consecutive", failures)
			}
			retry = min(max(2*retry, acceptBackoffFirst), acceptBackoffCap)
			select {
			case <-time.After(retry):
			case <-ctx.Done():
			}
			continue
		}
		if failures > 0 {
			s.logger().Info("ipc: accept recovered", "event", "ipc_accept_recovered", "failures", failures)
		}
		retry, failures = 0, 0
		// A slot for serving, else a busy answer, else a plain close.
		var serve func(context.Context, net.Conn)
		var free chan struct{}
		select {
		case slots <- struct{}{}:
			serve, free = s.serveConn, slots
		default:
			select {
			case busy <- struct{}{}:
				serve, free = refuseBusy, busy
			default:
				_ = c.Close()
				continue
			}
		}
		cmu.Lock()
		if closing {
			// Accepted just as shutdown began: the closer above already
			// swept conns, so this one would sit in its read until the idle
			// timeout and hold wg.Wait.
			cmu.Unlock()
			_ = c.Close()
			<-free
			continue
		}
		conns[c] = struct{}{}
		cmu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-free }()
			serve(ctx, c)
			cmu.Lock()
			delete(conns, c)
			cmu.Unlock()
		}()
	}
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReaderSize(c, 4096)
	// HTML escaping stays off on the wire too: Response.Result is already
	// marshalled by marshalResult, and a default encoder would re-escape its
	// '<', '>' and '&' as six-byte sequences (ipc.md §Framing, review 43 M7).
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	wait := firstRequestTimeout
	for {
		_ = c.SetReadDeadline(time.Now().Add(wait))
		wait = idleTimeout
		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				_ = enc.Encode(Response{Error: &Error{Code: CodeBadRequest, Message: "request too large"}})
			}
			return
		}
		resp := s.dispatch(ctx, line)
		_ = c.SetWriteDeadline(time.Now().Add(idleTimeout))
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// refuseBusy answers a client over maxConns with CodeBusy and closes it. It
// reads the client's first request, when one arrives within busyTimeout, so
// the answer carries its id and the close does not reset an unread request.
func refuseBusy(_ context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(busyTimeout))
	resp := Response{Error: &Error{Code: CodeBusy, Message: "the daemon is serving too many connections; try again"}}
	if line, err := readLine(bufio.NewReaderSize(c, 4096)); err == nil {
		var req Request
		if json.Unmarshal(line, &req) == nil {
			resp.ID = req.ID
		}
	}
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
}

func (s *Server) dispatch(ctx context.Context, line []byte) Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Response{Error: &Error{Code: CodeBadRequest, Message: "malformed JSON request"}}
	}
	if req.Method == "" {
		return Response{ID: req.ID, Error: &Error{Code: CodeBadRequest, Message: "method is required"}}
	}
	s.mu.RLock()
	h, ok := s.handlers[req.Method]
	s.mu.RUnlock()
	if !ok {
		return Response{ID: req.ID, Error: &Error{Code: CodeUnknownMethod, Message: fmt.Sprintf("unknown method %q", req.Method)}}
	}
	if s.Activity != nil {
		s.Activity()
	}
	res, err := s.callHandler(ctx, req.Method, h, req.Params)
	if err != nil {
		var ie *Error
		if errors.As(err, &ie) {
			return Response{ID: req.ID, Error: ie}
		}
		return Response{ID: req.ID, Error: &Error{Code: CodeInternal, Message: "internal error"}}
	}
	raw, err := marshalResult(res)
	if err != nil {
		return Response{ID: req.ID, Error: &Error{Code: CodeInternal, Message: "internal error"}}
	}
	return Response{ID: req.ID, OK: true, Result: raw}
}

// errHandlerPanic replaces a handler's panic.
var errHandlerPanic = errors.New("ipc: handler panicked")

// callHandler runs h, turning a panic into an error so one faulty handler
// answers "internal error" instead of stopping the daemon (review 55,
// R55-143). The panic is logged with the method and the stack, never the
// params (review 77, M2).
func (s *Server) callHandler(ctx context.Context, method string, h HandlerFunc, params json.RawMessage) (res any, err error) {
	defer func() {
		if v := recover(); v != nil {
			s.logger().Error("ipc: handler panicked", "event", "ipc_handler_panic", "method", method,
				"panic", panicSummary(v), "stack", string(debug.Stack()))
			res, err = nil, errHandlerPanic
		}
	}()
	return h(ctx, params)
}

// panicSummary describes a panic value without its content: a runtime
// error's own message (an index, a nil map), otherwise only the type.
func panicSummary(v any) string {
	if re, ok := v.(runtime.Error); ok {
		return re.Error()
	}
	return fmt.Sprintf("%T", v)
}

// marshalResult encodes a handler's result with HTML escaping off (review 43
// M7, Docs/protocol/debate.md §IPC "Size"): json.Marshal writes '<', '>' and
// '&' as six-byte \u00XX escapes, which would let a peer's debate transcript
// (a large amount of untrusted text one side controls) inflate a result past
// the 1 MiB line limit. Debate text additionally refuses U+2028/U+2029, which
// Go always escapes regardless of this setting.
func marshalResult(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

var errLineTooLong = errors.New("ipc: line too long")

// readLine reads one newline-terminated line of at most maxLine bytes.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, errLineTooLong
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// Call dials endpoint, sends one request and decodes the response. The whole
// exchange is bounded by ctx. A daemon-side failure is returned as *Error.
func Call(ctx context.Context, endpoint, method string, params, result any) error {
	conn, err := Dial(ctx, endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Unblock reads if ctx is cancelled without a deadline.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	req := Request{ID: "1", Method: method}
	if params != nil {
		if req.Params, err = json.Marshal(params); err != nil {
			return fmt.Errorf("ipc: marshal params: %w", err)
		}
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("ipc: send: %w", err)
	}
	line, err := readLine(bufio.NewReader(conn))
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("ipc: read response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("ipc: decode response: %w", err)
	}
	if !resp.OK {
		if resp.Error == nil {
			return errors.New("ipc: response not ok and no error")
		}
		return resp.Error
	}
	if result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("ipc: decode result: %w", err)
		}
	}
	return nil
}

// ErrNotRunning means nothing is listening on the endpoint.
var ErrNotRunning = errors.New("ipc: no daemon listening")

// ErrAlreadyRunning means Listen found another process already listening on
// the endpoint, or LockInstance found the lock held (a second agentnetd for
// the same home).
var ErrAlreadyRunning = errors.New("ipc: endpoint already in use")

// ErrForeignOwner means the endpoint is held by another OS user: Dial refuses
// to send anything to it and Listen refuses to report it as our own daemon
// (review 55 R55-008).
var ErrForeignOwner = errors.New("ipc: endpoint held by another user")

// InstanceLock is the file, inside the config dir, that LockInstance locks.
const InstanceLock = "agentnetd.lock"
