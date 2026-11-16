package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
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
	tf.atEOF = func() {
		// Deliver events caused by the last requests before the
		// connection goes away.
		dctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_ = a.bus.Drain(dctx)
	}
	conn := jsonrpc2.NewConnFraming(rwc, tf)
	sess := a.NewSession(func(ev Event) {
		_ = conn.Notify(context.Background(), NotifyMethod, ev)
	})
	defer sess.Close()
	conn.HandleDefault(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
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

func toRPCError(err error) *jsonrpc2.Error {
	var e *Error
	if errors.As(err, &e) {
		return jsonrpc2.NewError(int64(e.Code), e.Message)
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

	mu      sync.Mutex
	pending int
	changed chan struct{} // closed and replaced whenever pending drops
}

type peek struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func hasID(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

func (t *trackingFraming) ReadMessage(r *bufio.Reader) ([]byte, error) {
	if t.sequential {
		t.waitIdle()
	}
	body, err := t.inner.ReadMessage(r)
	if err != nil {
		t.waitIdle()
		if t.atEOF != nil {
			t.atEOF()
		}
		return nil, err
	}
	var p peek
	if json.Unmarshal(body, &p) == nil && hasID(p.ID) && p.Method != "" {
		t.mu.Lock()
		t.pending++
		t.mu.Unlock()
	}
	return body, nil
}

func (t *trackingFraming) WriteMessage(w io.Writer, body []byte) error {
	err := t.inner.WriteMessage(w, body)
	var p peek
	if json.Unmarshal(body, &p) == nil && hasID(p.ID) && p.Method == "" {
		t.mu.Lock()
		if t.pending > 0 {
			t.pending--
		}
		close(t.changed)
		t.changed = make(chan struct{})
		t.mu.Unlock()
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
