package dbgp

import "sync"

// Events are callbacks invoked by the server. All fields are optional.
//
// Callbacks of one session run sequentially on a dedicated goroutine in the
// order the events occurred; SessionStarted is always first and SessionEnded
// always last. Callbacks may call session methods, but a long-running
// callback delays further events of its session, so blocking calls such as
// [Session.Run] should be made from another goroutine.
type Events struct {
	// SessionStarted is called after the init packet was received and the
	// configured features were negotiated.
	SessionStarted func(s *Session)
	// SessionEnded is called after the connection was closed. err is nil
	// for a regular end.
	SessionEnded func(s *Session, err error)
	// StatusChanged is called when the execution status changes and after
	// every continuation command (run, step_*, stop, detach).
	StatusChanged func(s *Session, ev StatusEvent)
	// BreakpointResolved is called for breakpoint_resolved notifications.
	BreakpointResolved func(s *Session, bp Breakpoint)
	// Notification is called for other engine notifications.
	Notification func(s *Session, n Notification)
	// Output is called for stream packets (redirected stdout/stderr).
	Output func(s *Session, st Stream)
}

// eventQueue runs queued callbacks sequentially on its own goroutine. It is
// created stopped so that early events can be held back until the session is
// announced.
type eventQueue struct {
	mu      sync.Mutex
	items   []func()
	closed  bool
	started bool
	signal  chan struct{}
}

func newEventQueue() *eventQueue {
	return &eventQueue{signal: make(chan struct{}, 1)}
}

// push appends a callback. Calls after close are ignored.
func (q *eventQueue) push(f func()) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, f)
	q.mu.Unlock()
	q.wake()
}

// pushFront inserts a callback before all queued ones.
func (q *eventQueue) pushFront(f func()) {
	q.mu.Lock()
	q.items = append([]func(){f}, q.items...)
	q.mu.Unlock()
	q.wake()
}

// close lets the queue goroutine exit after all queued callbacks ran.
func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wake()
}

// start launches the queue goroutine (once).
func (q *eventQueue) start() {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.mu.Unlock()
	go q.loop()
}

func (q *eventQueue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *eventQueue) loop() {
	for {
		q.mu.Lock()
		items := q.items
		q.items = nil
		closed := q.closed
		q.mu.Unlock()
		for _, f := range items {
			f()
		}
		if len(items) > 0 {
			continue
		}
		if closed {
			return
		}
		<-q.signal
	}
}
