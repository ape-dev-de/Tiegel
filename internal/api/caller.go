package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// Caller is what a client of the backend programs against: method calls and
// event subscriptions. Two implementations exist and behave the same:
//
//   - InProcess dispatches directly through the method table of an App in
//     the same process (no JSON when the parameter and result types match
//     the handler's),
//   - client.Client (internal/api/client) talks JSON-RPC 2.0 to a
//     `tiegel --headless serve` process over stdio or a socket, e.g. the
//     Linux backend inside WSL.
//
// Errors are *Error values (use ErrorCode); a remote caller additionally
// reports a lost connection with an error matching client.ErrDisconnected.
type Caller interface {
	// Call invokes method with params (any JSON-serialisable value,
	// json.RawMessage, or nil) and stores the result in result (a pointer,
	// or nil to discard it).
	Call(ctx context.Context, method string, params, result any) error
	// Subscribe delivers events whose topic matches one of the patterns
	// ("doc.saved", "doc.*", "*") to fn until cancel is called. Each event
	// reaches fn at most once and in publish order; fn runs on an event
	// goroutine and must not block for long.
	Subscribe(ctx context.Context, topics []string, fn func(Event)) (cancel func(), err error)
}

// Call is the typed form of Caller.Call:
//
//	st, err := api.Call[api.DocState](ctx, c, "doc.insert", api.DocInsertParams{...})
//
// The result type comes first so that the params type can be inferred.
func Call[R, P any](ctx context.Context, c Caller, method string, params P) (R, error) {
	var r R
	err := c.Call(ctx, method, params, &r)
	return r, err
}

// directFunc runs a typed handler without JSON when params and result have
// the handler's types. handled is false if the types do not fit; the caller
// then falls back to the JSON path.
type directFunc func(ctx context.Context, params, result any) (handled bool, err error)

func direct[P, R any](fn func(ctx context.Context, p P) (R, error)) directFunc {
	return func(ctx context.Context, params, result any) (bool, error) {
		var p P
		switch v := params.(type) {
		case nil:
		case P:
			p = v
		case *P:
			if v != nil {
				p = *v
			}
		default:
			return false, nil
		}
		r, err := fn(ctx, p)
		if err != nil {
			return true, err
		}
		return true, storeResult(r, result)
	}
}

// storeResult copies r into result: directly if result is a *R, otherwise
// through JSON (e.g. into a map or a json.RawMessage).
func storeResult[R any](r R, result any) error {
	switch dst := result.(type) {
	case nil:
		return nil
	case *R:
		*dst = r
		return nil
	}
	return convert(r, result)
}

// convert re-encodes v into the value result points to.
func convert(v, result any) error {
	if result == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return Errorf(CodeInternal, "encoding result: %v", err)
	}
	if err := json.Unmarshal(b, result); err != nil {
		return Errorf(CodeInternal, "decoding result: %v", err)
	}
	return nil
}

// callValue implements Caller.Call for a context that may carry a session.
func (a *App) callValue(ctx context.Context, method string, params, result any) error {
	m, err := a.lookup(method)
	if err != nil {
		return err
	}
	if _, raw := params.(json.RawMessage); !raw && m.direct != nil {
		if handled, err := m.direct(ctx, params, result); handled {
			return err
		}
	}
	var rawParams json.RawMessage
	switch v := params.(type) {
	case nil:
	case json.RawMessage:
		rawParams = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return Errorf(CodeInvalidParams, "encoding params: %v", err)
		}
		rawParams = b
	}
	res, err := m.Handler(ctx, rawParams)
	if err != nil {
		return err
	}
	return convert(res, result)
}

// InProcess is the Caller for a client in the same process as the App (the
// GUI in the single-binary build, tests). Calls go straight to the method
// table; when params are the handler's own type (or a pointer to it) and
// result points to its result type, no JSON is involved and values are
// shared, not copied deeply – do not mutate them afterwards. Events are
// JSON-encoded like on the wire, so subscribers see the same payloads in
// both modes.
type InProcess struct {
	app  *App
	sess *Session

	mu     sync.Mutex
	cancel map[*inprocSub]func()
	closed bool
}

type inprocSub struct {
	patterns []string
}

var _ Caller = (*InProcess)(nil)

// NewInProcess returns a Caller for a. Handlers see a session of their own
// (SessionFrom), so "events.subscribe" works as well.
func NewInProcess(a *App) *InProcess {
	return &InProcess{app: a, sess: a.NewSession(nil), cancel: make(map[*inprocSub]func())}
}

// App returns the App the caller dispatches to.
func (c *InProcess) App() *App { return c.app }

// Call implements Caller.
func (c *InProcess) Call(ctx context.Context, method string, params, result any) error {
	return c.app.callValue(c.sess.With(ctx), method, params, result)
}

// Subscribe implements Caller. All patterns of one subscription share a bus
// subscription, so events arrive once each and in publish order.
func (c *InProcess) Subscribe(_ context.Context, topics []string, fn func(Event)) (func(), error) {
	s := &inprocSub{}
	for _, t := range topics {
		if t = strings.TrimSpace(t); t != "" {
			s.patterns = append(s.patterns, t)
		}
	}
	if len(s.patterns) == 0 {
		return nil, Errorf(CodeInvalidParams, "topics is required")
	}
	if fn == nil {
		fn = func(Event) {}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, Errorf(CodeFailed, "caller closed")
	}
	un := c.app.bus.Subscribe("*", func(e bus.Event) {
		for _, p := range s.patterns {
			if bus.Match(p, e.Topic) {
				fn(Event{Topic: e.Topic, Payload: encodePayload(e.Payload)})
				return
			}
		}
	})
	c.cancel[s] = un
	return func() {
		c.mu.Lock()
		delete(c.cancel, s)
		c.mu.Unlock()
		un()
	}, nil
}

// Close ends all subscriptions of the caller. The App stays open.
func (c *InProcess) Close() error {
	c.mu.Lock()
	subs := c.cancel
	c.cancel = map[*inprocSub]func(){}
	c.closed = true
	c.mu.Unlock()
	for _, un := range subs {
		un()
	}
	c.sess.Close()
	return nil
}
