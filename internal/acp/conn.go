// Package acp is a client of the Agent Client Protocol (ACP,
// agentclientprotocol.com): a coding agent (e.g. `opencode acp`) runs as
// a child process and speaks JSON-RPC 2.0 over stdio, one JSON object
// per line. The client sends initialize, session/new, session/prompt,
// session/cancel and session/set_config_option; the agent streams
// session/update notifications and calls back into the client for
// permissions (session/request_permission) and file access
// (fs/read_text_file, fs/write_text_file), which a Handler answers.
//
// The package imports no Gio and no Tiegel API code; the agent module
// (internal/modules/agent) builds on it.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// JSON-RPC error codes used by ACP.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeAuthRequired is returned by agents that need a login first.
	CodeAuthRequired = -32000
	// CodeResourceNotFound is returned for missing files.
	CodeResourceNotFound = -32002
)

// maxLine bounds one JSON-RPC message (agents send whole files in diffs).
const maxLine = 64 << 20

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s (code %d): %s", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// ErrorCode returns the JSON-RPC code of err (0 if it is no *Error).
func ErrorCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// ErrClosed is returned by calls on a closed connection.
var ErrClosed = errors.New("acp: connection closed")

// message is any JSON-RPC message (request, notification or response).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// RequestHandler answers a request of the peer. It runs on its own
// goroutine, so it may block (e.g. waiting for the user).
type RequestHandler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// NotificationHandler receives the notifications of the peer in order on
// the read goroutine; it must not block for long.
type NotificationHandler func(method string, params json.RawMessage)

// Conn is a JSON-RPC 2.0 connection with newline-delimited framing. It
// correlates responses with requests by ID, delivers notifications in
// order and serves the peer's requests concurrently.
type Conn struct {
	w   io.Writer
	wmu sync.Mutex

	onRequest RequestHandler
	onNotify  NotificationHandler

	mu      sync.Mutex
	next    int64
	pending map[string]chan *message
	closed  bool
	err     error

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewConn starts reading r; requests and notifications of the peer go to
// the handlers (nil: requests fail with method not found, notifications
// are dropped). Close or the end of r stops the connection.
func NewConn(r io.Reader, w io.Writer, onRequest RequestHandler, onNotify NotificationHandler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{w: w, onRequest: onRequest, onNotify: onNotify, pending: map[string]chan *message{},
		ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Done is closed when the connection ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection ended (nil while it runs, io.EOF after
// a clean end of input).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close stops the connection: pending calls fail with ErrClosed and the
// contexts of running request handlers are cancelled. It does not close
// the reader or writer.
func (c *Conn) Close() error {
	c.shutdown(ErrClosed)
	return nil
}

func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = err
	pending := c.pending
	c.pending = map[string]chan *message{}
	c.mu.Unlock()
	c.cancel()
	for _, ch := range pending {
		close(ch)
	}
}

func (c *Conn) read(r io.Reader) {
	defer close(c.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // not JSON-RPC (e.g. a log line on stdout)
		}
		c.dispatch(&m)
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.shutdown(err)
}

func (c *Conn) dispatch(m *message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go c.serve(m)
	case m.Method != "":
		if c.onNotify != nil {
			c.onNotify(m.Method, m.Params)
		}
	case len(m.ID) > 0:
		c.mu.Lock()
		ch := c.pending[idKey(m.ID)]
		delete(c.pending, idKey(m.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// idKey normalises an ID for the pending map ("1" and 1 differ in JSON,
// but we only ever send numbers).
func idKey(id json.RawMessage) string { return string(id) }

func (c *Conn) serve(m *message) {
	var (
		res any
		err error
	)
	if c.onRequest == nil {
		err = &Error{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
	} else {
		res, err = c.onRequest(c.ctx, m.Method, m.Params)
	}
	out := message{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeInternalError, Message: err.Error()}
		}
		out.Error = e
	} else {
		b, merr := json.Marshal(res)
		if merr != nil {
			out.Error = &Error{Code: CodeInternalError, Message: merr.Error()}
		} else {
			out.Result = b
		}
	}
	_ = c.write(&out)
}

func (c *Conn) write(m *message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("acp: encode %s: %w", m.Method, err)
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(b); err != nil {
		return fmt.Errorf("acp: write: %w", err)
	}
	return nil
}

// Call sends a request and waits for its response; result (a pointer or
// nil) receives the result. A cancelled ctx returns ctx.Err() (the
// response is then discarded).
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.next++
	id := json.RawMessage(strconv.FormatInt(c.next, 10))
	ch := make(chan *message, 1)
	c.pending[idKey(id)] = ch
	c.mu.Unlock()

	p, err := json.Marshal(params)
	if err != nil {
		c.forget(id)
		return fmt.Errorf("acp: encode %s: %w", method, err)
	}
	if err := c.write(&message{JSONRPC: "2.0", ID: id, Method: method, Params: p}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			if err := c.Err(); err != nil && !errors.Is(err, ErrClosed) {
				return fmt.Errorf("%w: %v", ErrClosed, err)
			}
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 && string(m.Result) != "null" {
			if err := json.Unmarshal(m.Result, result); err != nil {
				return fmt.Errorf("acp: decode %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *Conn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, idKey(id))
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acp: encode %s: %w", method, err)
	}
	return c.write(&message{JSONRPC: "2.0", Method: method, Params: p})
}
