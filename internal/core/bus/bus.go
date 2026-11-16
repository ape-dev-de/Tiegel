// Package bus implements Tiegel's in-process event bus.
//
// Modules never call each other directly; they publish events on topics and
// subscribe to the topics they care about. A topic is a dot-separated string
// such as "file.saved" or "settings.changed".
//
// Delivery comes in two flavours:
//
//   - Publish is asynchronous. Every subscription owns a FIFO mailbox that is
//     drained by a short-lived goroutine, so a slow subscriber never delays the
//     publisher or other subscribers, and each subscriber sees events in
//     publish order.
//   - PublishSync calls all matching handlers in the caller's goroutine, in
//     subscription order, and returns once all of them have run.
//
// Every handler invocation is protected against panics; a panicking handler is
// reported (see WithPanicHandler) and never takes down the bus or the caller.
//
// Subscriptions may use the patterns "*" (all topics) and "prefix.*" (all
// topics starting with "prefix.").
//
// For compile-time type safety declare topics with Topic:
//
//	var FileSaved = bus.Topic[SavedEvent]("file.saved")
//	unsub := FileSaved.Subscribe(b, func(e SavedEvent) { ... })
//	FileSaved.Publish(b, SavedEvent{...})
package bus

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
)

// Event is a single message delivered to handlers.
type Event struct {
	// Topic is the concrete topic the event was published on (never a pattern).
	Topic string
	// Payload is the event data; its type is defined by the topic.
	Payload any
}

// Handler receives events. Handlers registered for asynchronous delivery
// should return quickly; long work belongs in its own goroutine.
type Handler func(Event)

// PanicError describes a handler that panicked while processing an event.
type PanicError struct {
	Topic string
	Value any
	Stack []byte
}

// Error implements error.
func (e *PanicError) Error() string {
	return fmt.Sprintf("bus: handler for %q panicked: %v", e.Topic, e.Value)
}

// Option configures a Bus.
type Option func(*Bus)

// WithPanicHandler installs a callback that is invoked whenever a handler
// panics. It is called from the goroutine that ran the handler. Without it,
// panics are silently recovered (PublishSync still reports them).
func WithPanicHandler(fn func(*PanicError)) Option {
	return func(b *Bus) { b.onPanic = fn }
}

// Bus is a concurrency-safe publish/subscribe hub. The zero value is not
// usable; create buses with New.
type Bus struct {
	onPanic func(*PanicError)

	mu     sync.RWMutex
	subs   []*subscription
	closed bool

	pendMu  sync.Mutex
	pending int
	idle    chan struct{} // closed while pending == 0
}

// New creates an empty bus.
func New(opts ...Option) *Bus {
	b := &Bus{idle: make(chan struct{})}
	close(b.idle)
	for _, o := range opts {
		o(b)
	}
	return b
}

type subscription struct {
	bus     *Bus
	pattern string
	handler Handler

	mu      sync.Mutex
	queue   []Event
	running bool
	removed bool
}

// Subscribe registers h for topic, which may also be "*" or "prefix.*". The
// returned function removes the subscription; it is idempotent. Events that
// were queued for asynchronous delivery but not yet handled are discarded on
// unsubscribe; a handler that is already running finishes normally.
//
// Subscribing on a closed bus returns a no-op unsubscribe function.
func (b *Bus) Subscribe(topic string, h Handler) (unsubscribe func()) {
	if h == nil {
		return func() {}
	}
	s := &subscription{bus: b, pattern: topic, handler: h}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return func() {}
	}
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	var once sync.Once
	return func() { once.Do(func() { b.remove(s) }) }
}

func (b *Bus) remove(s *subscription) {
	b.mu.Lock()
	for i, x := range b.subs {
		if x == s {
			b.subs = append(b.subs[:i:i], b.subs[i+1:]...)
			break
		}
	}
	b.mu.Unlock()

	s.mu.Lock()
	s.removed = true
	dropped := len(s.queue)
	s.queue = nil
	s.mu.Unlock()
	b.done(dropped)
}

// Match reports whether a subscription pattern ("doc.saved", "doc.*", "*")
// matches a concrete topic, using the same rules as Subscribe.
func Match(pattern, topic string) bool { return match(pattern, topic) }

// match reports whether a subscription pattern matches a concrete topic.
func match(pattern, topic string) bool {
	switch {
	case pattern == topic, pattern == "*":
		return true
	case strings.HasSuffix(pattern, ".*"):
		return strings.HasPrefix(topic, pattern[:len(pattern)-1])
	}
	return false
}

func (b *Bus) matching(topic string) []*subscription {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil
	}
	var out []*subscription
	for _, s := range b.subs {
		if match(s.pattern, topic) {
			out = append(out, s)
		}
	}
	return out
}

// Publish delivers payload to all subscribers of topic asynchronously and
// returns immediately. Publishing on a closed bus is a no-op.
func (b *Bus) Publish(topic string, payload any) {
	ev := Event{Topic: topic, Payload: payload}
	for _, s := range b.matching(topic) {
		s.enqueue(ev)
	}
}

func (s *subscription) enqueue(ev Event) {
	s.mu.Lock()
	if s.removed {
		s.mu.Unlock()
		return
	}
	s.bus.add(1)
	s.queue = append(s.queue, ev)
	start := !s.running
	s.running = true
	s.mu.Unlock()
	if start {
		go s.drain()
	}
}

// drain delivers queued events in order and exits once the queue is empty.
func (s *subscription) drain() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 || s.removed {
			s.running = false
			s.mu.Unlock()
			return
		}
		ev := s.queue[0]
		s.queue[0] = Event{}
		s.queue = s.queue[1:]
		s.mu.Unlock()

		s.bus.call(s.handler, ev)
		s.bus.done(1)
	}
}

// PublishSync delivers payload to all subscribers of topic in the calling
// goroutine and returns after every handler has run. Panics are recovered
// and returned as joined *PanicError values.
func (b *Bus) PublishSync(topic string, payload any) error {
	ev := Event{Topic: topic, Payload: payload}
	var errs []error
	for _, s := range b.matching(topic) {
		s.mu.Lock()
		removed := s.removed
		s.mu.Unlock()
		if removed {
			continue
		}
		if err := b.call(s.handler, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *Bus) call(h Handler, ev Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			pe := &PanicError{Topic: ev.Topic, Value: r, Stack: debug.Stack()}
			if b.onPanic != nil {
				b.onPanic(pe)
			}
			err = pe
		}
	}()
	h(ev)
	return nil
}

func (b *Bus) add(n int) {
	b.pendMu.Lock()
	if b.pending == 0 && n > 0 {
		b.idle = make(chan struct{})
	}
	b.pending += n
	b.pendMu.Unlock()
}

func (b *Bus) done(n int) {
	if n == 0 {
		return
	}
	b.pendMu.Lock()
	b.pending -= n
	if b.pending == 0 {
		close(b.idle)
	}
	b.pendMu.Unlock()
}

// Drain blocks until every asynchronously published event has been handled
// (or discarded), or until ctx is done.
func (b *Bus) Drain(ctx context.Context) error {
	for {
		b.pendMu.Lock()
		if b.pending == 0 {
			b.pendMu.Unlock()
			return nil
		}
		idle := b.idle
		b.pendMu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return fmt.Errorf("bus: drain: %w", ctx.Err())
		}
	}
}

// Close stops accepting new events and subscriptions and waits (bounded by
// ctx) until queued events have been delivered.
func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return b.Drain(ctx)
}

// Topic is a typed topic name. It binds a payload type to a topic string so
// that publishers and subscribers agree on the payload at compile time.
type Topic[T any] string

// Name returns the topic string.
func (t Topic[T]) Name() string { return string(t) }

// Subscribe registers a typed handler. Events on this topic whose payload is
// not a T (published untyped by mistake) are ignored.
func (t Topic[T]) Subscribe(b *Bus, h func(T)) (unsubscribe func()) {
	return b.Subscribe(string(t), func(ev Event) {
		if v, ok := ev.Payload.(T); ok {
			h(v)
		}
	})
}

// Publish publishes v asynchronously.
func (t Topic[T]) Publish(b *Bus, v T) { b.Publish(string(t), v) }

// PublishSync publishes v synchronously; see Bus.PublishSync.
func (t Topic[T]) PublishSync(b *Bus, v T) error { return b.PublishSync(string(t), v) }
