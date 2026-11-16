package jsonrpc2

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrClosed is returned (wrapped) by calls on a connection that has been
// closed locally or whose peer went away.
var ErrClosed = errors.New("jsonrpc2: connection closed")

// CancelMethod is the LSP notification used to cancel a pending request.
const CancelMethod = "$/cancelRequest"

// Handler handles an incoming request or notification. For requests the
// returned value is sent as result, a returned error as error response
// (an *Error is sent unchanged, other errors as internal errors). For
// notifications the return values are ignored.
//
// Requests are handled concurrently, each in its own goroutine; the context
// is cancelled when the peer sends $/cancelRequest or the connection closes.
// Notifications are handled one after another in arrival order.
type Handler func(ctx context.Context, req *Request) (any, error)

// Conn is a bidirectional JSON-RPC 2.0 connection.
type Conn struct {
	rwc     io.ReadWriteCloser
	r       *bufio.Reader
	framing Framing

	wmu sync.Mutex // serializes writes

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan *wireMessage
	handlers map[string]Handler
	fallback Handler
	incoming map[ID]context.CancelFunc
	started  bool
	closed   bool
	err      error

	notifyMu   sync.Mutex
	notifyCond *sync.Cond
	notifyQ    []*Request

	baseCtx    context.Context
	baseCancel context.CancelFunc
	done       chan struct{}
	closeOnce  sync.Once
}

// NewConn wraps rwc using HeaderFraming. Handlers should be registered before
// calling Start.
func NewConn(rwc io.ReadWriteCloser) *Conn {
	return NewConnFraming(rwc, HeaderFraming)
}

// NewConnFraming wraps rwc using the given framing (nil means
// HeaderFraming). Handlers should be registered before calling Start.
func NewConnFraming(rwc io.ReadWriteCloser, f Framing) *Conn {
	if f == nil {
		f = HeaderFraming
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		framing:    f,
		rwc:        rwc,
		r:          bufio.NewReaderSize(rwc, 64<<10),
		pending:    make(map[int64]chan *wireMessage),
		handlers:   make(map[string]Handler),
		incoming:   make(map[ID]context.CancelFunc),
		baseCtx:    ctx,
		baseCancel: cancel,
		done:       make(chan struct{}),
	}
	c.notifyCond = sync.NewCond(&c.notifyMu)
	return c
}

// Handle registers h for method, replacing any previous handler.
func (c *Conn) Handle(method string, h Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == nil {
		delete(c.handlers, method)
		return
	}
	c.handlers[method] = h
}

// HandleDefault registers a handler for methods without a specific handler.
// Without a default handler unknown requests are answered with
// CodeMethodNotFound and unknown notifications are dropped.
func (c *Conn) HandleDefault(h Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fallback = h
}

// Start begins reading messages from the peer. It must be called exactly
// once; further calls are no-ops.
func (c *Conn) Start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	go c.readLoop()
	go c.notifyLoop()
}

// Done is closed once the connection has shut down.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns the reason the connection shut down, or nil while it is open.
// A locally closed connection reports ErrClosed; a peer that closed the
// stream reports io.EOF.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close shuts the connection down and closes the underlying stream.
// Pending calls fail with an error wrapping ErrClosed.
func (c *Conn) Close() error {
	return c.shutdown(ErrClosed)
}

func (c *Conn) shutdown(cause error) error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.err = cause
		pending := c.pending
		c.pending = make(map[int64]chan *wireMessage)
		c.mu.Unlock()

		err = c.rwc.Close()
		c.baseCancel()
		for _, ch := range pending {
			close(ch)
		}
		c.notifyMu.Lock()
		c.notifyCond.Broadcast()
		c.notifyMu.Unlock()
		close(c.done)
	})
	return err
}

func (c *Conn) closedErr() error {
	c.mu.Lock()
	cause := c.err
	c.mu.Unlock()
	if cause == nil || errors.Is(cause, ErrClosed) {
		return ErrClosed
	}
	return fmt.Errorf("%w: %w", ErrClosed, cause)
}

// Call sends a request and waits for its response. If result is non-nil the
// response result is decoded into it. When ctx is cancelled before the
// response arrives, a $/cancelRequest notification is sent to the peer and
// ctx.Err() is returned.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	ch := make(chan *wireMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.closedErr()
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	wid := NumberID(id)
	if err := c.send(&wireMessage{ID: &wid, Method: method, Params: rawParams}); err != nil {
		c.removePending(id)
		return fmt.Errorf("jsonrpc2: sending %s: %w", method, err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return c.closedErr()
		}
		if msg.Error != nil {
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 && string(msg.Result) != "null" {
			if err := json.Unmarshal(msg.Result, result); err != nil {
				return fmt.Errorf("jsonrpc2: decoding %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		if c.removePending(id) {
			go func() { _ = c.Notify(context.Background(), CancelMethod, map[string]ID{"id": wid}) }()
		}
		return ctx.Err()
	}
}

func (c *Conn) removePending(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[id]
	delete(c.pending, id)
	return ok
}

// Notify sends a notification to the peer.
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return c.closedErr()
	}
	if err := c.send(&wireMessage{Method: method, Params: rawParams}); err != nil {
		return fmt.Errorf("jsonrpc2: sending %s: %w", method, err)
	}
	return nil
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc2: encoding params: %w", err)
	}
	return b, nil
}

func (c *Conn) send(msg *wireMessage) error {
	msg.JSONRPC = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("jsonrpc2: encoding message: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.framing.WriteMessage(c.rwc, body)
}

func (c *Conn) readLoop() {
	for {
		body, err := c.framing.ReadMessage(c.r)
		if err != nil {
			_ = c.shutdown(err)
			return
		}
		var msg wireMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			// A broken message cannot be attributed to a request; report
			// a parse error as the spec demands and keep going.
			_ = c.send(&wireMessage{ID: nil, Error: NewError(CodeParseError, err.Error())})
			continue
		}
		switch {
		case msg.isResponse():
			c.dispatchResponse(&msg)
		case msg.isRequest():
			c.dispatchRequest(&msg)
		case msg.isNotify():
			c.dispatchNotify(&msg)
		}
	}
}

func (c *Conn) dispatchResponse(msg *wireMessage) {
	if msg.ID.isStr {
		return // we only issue numeric ids
	}
	c.mu.Lock()
	ch, ok := c.pending[msg.ID.num]
	delete(c.pending, msg.ID.num)
	c.mu.Unlock()
	if ok {
		ch <- msg
	}
}

func (c *Conn) lookup(method string) Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.handlers[method]; ok {
		return h
	}
	return c.fallback
}

func (c *Conn) dispatchRequest(msg *wireMessage) {
	id := *msg.ID
	h := c.lookup(msg.Method)
	if h == nil {
		_ = c.send(&wireMessage{ID: &id, Error: NewError(CodeMethodNotFound, "method not found: "+msg.Method)})
		return
	}
	ctx, cancel := context.WithCancel(c.baseCtx)
	c.mu.Lock()
	c.incoming[id] = cancel
	c.mu.Unlock()
	req := &Request{ID: &id, Method: msg.Method, Params: msg.Params}
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.incoming, id)
			c.mu.Unlock()
			cancel()
		}()
		res, err := h(ctx, req)
		resp := &wireMessage{ID: &id}
		if err != nil {
			resp.Error = toWireError(err)
		} else {
			raw, merr := json.Marshal(res)
			if merr != nil {
				resp.Error = NewError(CodeInternalError, merr.Error())
			} else {
				resp.Result = raw
			}
		}
		_ = c.send(resp)
	}()
}

func toWireError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return NewError(CodeRequestCancelled, err.Error())
	}
	return NewError(CodeInternalError, err.Error())
}

func (c *Conn) dispatchNotify(msg *wireMessage) {
	if msg.Method == CancelMethod {
		var p struct {
			ID ID `json:"id"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			c.mu.Lock()
			cancel := c.incoming[p.ID]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		if c.lookup(CancelMethod) == nil {
			return
		}
	}
	c.notifyMu.Lock()
	c.notifyQ = append(c.notifyQ, &Request{Method: msg.Method, Params: msg.Params})
	c.notifyCond.Signal()
	c.notifyMu.Unlock()
}

func (c *Conn) notifyLoop() {
	for {
		c.notifyMu.Lock()
		for len(c.notifyQ) == 0 {
			select {
			case <-c.done:
				c.notifyMu.Unlock()
				return
			default:
			}
			c.notifyCond.Wait()
		}
		req := c.notifyQ[0]
		c.notifyQ[0] = nil
		c.notifyQ = c.notifyQ[1:]
		c.notifyMu.Unlock()

		if h := c.lookup(req.Method); h != nil {
			_, _ = h(c.baseCtx, req)
		}
	}
}
