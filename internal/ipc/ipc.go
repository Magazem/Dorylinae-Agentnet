// Package ipc implements the local socket API between the CLI and the daemon.
//
// Transport is a Unix domain socket (Linux, macOS) or a named pipe (Windows);
// framing and message shapes are specified in Docs/protocol/ipc.md.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Error codes returned in Response.Error.Code.
const (
	CodeBadRequest    = "bad_request"
	CodeUnknownMethod = "unknown_method"
	CodeInternal      = "internal"
)

const (
	maxLine     = 1 << 20
	idleTimeout = 30 * time.Second
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
}

// NewServer returns a Server with no handlers.
func NewServer() *Server { return &Server{handlers: map[string]HandlerFunc{}} }

// Handle registers a method handler.
func (s *Server) Handle(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Serve accepts connections until ctx is cancelled or the listener fails.
// It closes ln and waits for in-flight connections before returning.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	conns := map[net.Conn]struct{}{}
	var cmu sync.Mutex

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		_ = ln.Close()
		cmu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		cmu.Unlock()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("ipc accept: %w", err)
		}
		cmu.Lock()
		conns[c] = struct{}{}
		cmu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, c)
			cmu.Lock()
			delete(conns, c)
			cmu.Unlock()
		}()
	}
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReaderSize(c, 4096)
	enc := json.NewEncoder(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(idleTimeout))
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
	res, err := h(ctx, req.Params)
	if err != nil {
		var ie *Error
		if errors.As(err, &ie) {
			return Response{ID: req.ID, Error: ie}
		}
		return Response{ID: req.ID, Error: &Error{Code: CodeInternal, Message: "internal error"}}
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return Response{ID: req.ID, Error: &Error{Code: CodeInternal, Message: "internal error"}}
	}
	return Response{ID: req.ID, OK: true, Result: raw}
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
