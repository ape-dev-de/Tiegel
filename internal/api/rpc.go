package api

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
)

// ServeOptions configure Serve.
type ServeOptions struct {
	// Framing splits the stream into messages; nil means
	// jsonrpc2.LineFraming (one JSON message per line).
	Framing jsonrpc2.Framing
	// Concurrent handles requests in parallel. By default requests are
	// handled one after another in arrival order, which is what pipelines
	// expect ("doc.open" before "doc.insert").
	Concurrent bool
	// Token, if non-empty, makes the connection unusable until the client
	// has sent the request "auth" with {"token": Token}. Every other request
	// before that fails with CodeUnauthorized and closes the connection; so
	// does a wrong token. Socket transports always set it.
	Token string
	// AuthTimeout closes a connection that has not authenticated in time
	// (only with Token; 0 means 10 seconds).
	AuthTimeout time.Duration
	// PreAuthMaxMessage limits the size of messages before successful
	// authentication (only with Token; 0 means DefaultPreAuthMaxMessage).
	// A larger message closes the connection.
	PreAuthMaxMessage int
	// MaxQueuedEvents bounds the events waiting to be written to the
	// client (0 means DefaultMaxQueuedEvents, < 0 unbounded). A client
	// that stops reading is disconnected when the limit is reached.
	MaxQueuedEvents int
}

// DefaultPreAuthMaxMessage is the message size limit before "auth"
// succeeded; the auth request itself is tiny.
const DefaultPreAuthMaxMessage = 4 << 10

// DefaultMaxQueuedEvents is the default ServeOptions.MaxQueuedEvents.
const DefaultMaxQueuedEvents = 10000

// AuthMethod is the request a socket client must send first (see
// ServeOptions.Token). It is answered by every transport, also without a
// token, so clients can always send it.
const AuthMethod = "auth"

// AuthParams are the params of "auth".
type AuthParams struct {
	Token string `json:"token"`
}

// AuthResult is the result of "auth".
type AuthResult struct {
	OK         bool   `json:"ok"`
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
}

// Serve speaks JSON-RPC 2.0 on rwc until the peer closes the stream or ctx is
// done. Every request is dispatched through the method table; events the
// client subscribed to (events.subscribe) are sent as "event" notifications.
// When the input ends, Serve waits for outstanding responses before
// returning, so `printf '...' | tiegel --headless serve` gets every answer.
func (a *App) Serve(ctx context.Context, rwc io.ReadWriteCloser, opts ServeOptions) error {
	f := opts.Framing
	if f == nil {
		f = jsonrpc2.LineFraming
	}
	tf := &trackingFraming{inner: f, sequential: !opts.Concurrent, changed: make(chan struct{}), stop: ctx.Done()}
	auth := &authState{token: opts.Token}
	auth.ok.Store(opts.Token == "")
	if opts.Token != "" {
		limit := opts.PreAuthMaxMessage
		if limit <= 0 {
			limit = DefaultPreAuthMaxMessage
		}
		tf.limit = func() int {
			if auth.ok.Load() {
				return 0
			}
			return limit
		}
	}
	var sess *Session
	tf.atEOF = func() {
		// Deliver events caused by the last requests before the
		// connection goes away: first let synchronous follow-ups on the
		// bus settle briefly, then wait for this session's own queue
		// (independent of other publishers that keep the bus busy).
		dctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		_ = a.bus.Drain(dctx)
		cancel()
		fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = sess.Flush(fctx)
	}
	conn := jsonrpc2.NewConnFraming(rwc, tf)
	sess = a.NewSession(func(ev Event) {
		_ = conn.Notify(context.Background(), NotifyMethod, ev)
	})
	defer sess.Close()
	maxQueue := opts.MaxQueuedEvents
	if maxQueue == 0 {
		maxQueue = DefaultMaxQueuedEvents
	}
	sess.LimitQueue(maxQueue, func() {
		a.log.Warn("api: client does not read its events; disconnecting", "limit", maxQueue)
		_ = conn.Close()
	})
	tf.afterWrite = func() {
		if auth.failed.Load() {
			go conn.Close()
		}
	}
	if !auth.ok.Load() {
		d := opts.AuthTimeout
		if d <= 0 {
			d = 10 * time.Second
		}
		t := time.AfterFunc(d, func() {
			if !auth.ok.Load() {
				_ = conn.Close()
			}
		})
		defer t.Stop()
	}
	conn.HandleDefault(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		if req.Method == AuthMethod {
			return a.authenticate(auth, req)
		}
		if !auth.ok.Load() {
			if req.IsNotification() {
				return nil, nil
			}
			auth.failed.Store(true)
			return nil, toRPCError(Errorf(CodeUnauthorized, "authentication required (send %q first)", AuthMethod))
		}
		res, err := sess.Call(ctx, req.Method, req.Params)
		if err != nil {
			return nil, toRPCError(err)
		}
		return res, nil
	})
	conn.Start()
	select {
	case <-conn.Done():
	case <-ctx.Done():
		_ = conn.Close()
		return ctx.Err()
	}
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, jsonrpc2.ErrClosed) {
		return err
	}
	return nil
}

type authState struct {
	token  string
	ok     atomic.Bool
	failed atomic.Bool
}

func (a *App) authenticate(st *authState, req *jsonrpc2.Request) (any, error) {
	var p AuthParams
	if err := DecodeParams(req.Params, &p); err != nil {
		st.failed.Store(!st.ok.Load())
		return nil, toRPCError(err)
	}
	if st.token != "" && subtle.ConstantTimeCompare([]byte(p.Token), []byte(st.token)) != 1 {
		st.failed.Store(!st.ok.Load())
		return nil, toRPCError(Errorf(CodeUnauthorized, "invalid token"))
	}
	st.ok.Store(true)
	return AuthResult{OK: true, Version: a.opts.AppVersion, APIVersion: Version}, nil
}

func toRPCError(err error) *jsonrpc2.Error {
	var e *Error
	if errors.As(err, &e) {
		re := jsonrpc2.NewError(int64(e.Code), e.Message)
		if e.Data != nil {
			if b, merr := json.Marshal(e.Data); merr == nil {
				re.Data = b
			}
		}
		return re
	}
	if errors.Is(err, context.Canceled) {
		return jsonrpc2.NewError(jsonrpc2.CodeRequestCancelled, err.Error())
	}
	return jsonrpc2.NewError(CodeFailed, err.Error())
}

// trackingFraming wraps a framing and counts requests whose response has not
// been written yet. It makes the reader wait for those responses before
// reading the next message (sequential mode) and before reporting the end of
// the input.
type trackingFraming struct {
	inner      jsonrpc2.Framing
	sequential bool
	stop       <-chan struct{}
	atEOF      func()
	afterWrite func()     // after each response
	limit      func() int // current message size limit (0: framing default)

	mu      sync.Mutex
	pending int
	changed chan struct{} // closed and replaced whenever pending drops
}

// peek decodes the ID the same way the connection does (jsonrpc2.ID: string
// or integer). A request whose ID does not decode (object, float, …) is
// answered as a parse error without ID, so it must not count as pending.
type peek struct {
	ID     *jsonrpc2.ID `json:"id"`
	Method string       `json:"method"`
}

// pendingRequest reports whether body is a request that will get a
// response carrying its ID.
func pendingRequest(body []byte) bool {
	var p peek
	return json.Unmarshal(body, &p) == nil && p.ID != nil && p.Method != ""
}

// responseWithID reports whether body is a response to a request with ID.
func responseWithID(body []byte) bool {
	var p peek
	return json.Unmarshal(body, &p) == nil && p.ID != nil && p.Method == ""
}

func (t *trackingFraming) ReadMessage(r *bufio.Reader) ([]byte, error) {
	if t.sequential {
		t.waitIdle()
	}
	var body []byte
	var err error
	if lr, ok := t.inner.(jsonrpc2.LimitedReader); ok && t.limit != nil && t.limit() > 0 {
		body, err = lr.ReadMessageLimit(r, t.limit())
	} else {
		body, err = t.inner.ReadMessage(r)
	}
	if err != nil {
		t.waitIdle()
		if t.atEOF != nil {
			t.atEOF()
		}
		return nil, err
	}
	if pendingRequest(body) {
		t.mu.Lock()
		t.pending++
		t.mu.Unlock()
	}
	return body, nil
}

func (t *trackingFraming) WriteMessage(w io.Writer, body []byte) error {
	err := t.inner.WriteMessage(w, body)
	if responseWithID(body) {
		t.mu.Lock()
		if t.pending > 0 {
			t.pending--
		}
		close(t.changed)
		t.changed = make(chan struct{})
		t.mu.Unlock()
		if t.afterWrite != nil {
			t.afterWrite()
		}
	}
	return err
}

func (t *trackingFraming) waitIdle() {
	for {
		t.mu.Lock()
		if t.pending == 0 {
			t.mu.Unlock()
			return
		}
		ch := t.changed
		t.mu.Unlock()
		select {
		case <-ch:
		case <-t.stop:
			return
		}
	}
}
