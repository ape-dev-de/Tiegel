// Package tasks is the registry of background tasks (D505): every
// long-running operation that is not tied to a modal dialog (indexing,
// git network operations, Composer and QA tool runs, language server
// start) registers here, reports its progress and ends. Clients see the
// running tasks through one change event and can cancel them.
//
// The registry is UI-free and does not know the bus: it reports changes
// to a publish function (the App publishes them as "tasks.changed").
// Changes are throttled: at most one event per Interval (default 100 ms),
// the last change always follows. Every event carries the complete list,
// so a client that missed events is consistent again with the next one.
//
// Finished and cancelled tasks disappear at once; failed tasks stay for
// FailedLinger (default 5 s) with their error so the status bar can show
// what went wrong.
//
// A nil *Registry and a nil *Task are valid and do nothing, so producers
// need no checks when they run without an App (tests, one-shot tools).
package tasks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Topic is the bus topic of the change events (ChangedEvent).
const Topic = "tasks.changed"

// Defaults.
const (
	// DefaultInterval is the minimum time between two change events.
	DefaultInterval = 100 * time.Millisecond
	// DefaultFailedLinger is how long a failed task stays listed.
	DefaultFailedLinger = 5 * time.Second
)

// Errors of Cancel.
var (
	ErrNotFound       = errors.New("task not found")
	ErrNotCancellable = errors.New("task cannot be cancelled")
)

// Options describe a task when it begins.
type Options struct {
	// Title is the short description shown to the user ("Indexing PHP
	// files", "Pushing main").
	Title string
	// Module is the ID of the producing module ("php-index", "git").
	Module string
	// Cancellable allows Cancel (tasks.cancel). The task's context is
	// cancelled; the producer must honour it.
	Cancellable bool
	// Text is an optional initial detail line.
	Text string
}

// Info is the public state of a task.
type Info struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Module string `json:"module,omitempty"`
	// Done and Total are the progress; Total 0 means indeterminate.
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Text  string `json:"text,omitempty"`
	// StartedAt is when the task began.
	StartedAt   time.Time `json:"startedAt"`
	Cancellable bool      `json:"cancellable"`
	// Cancelling is set once Cancel was requested while the task still
	// runs.
	Cancelling bool `json:"cancelling,omitempty"`
	// Error is set for a failed task (it is listed for FailedLinger
	// after it ended).
	Error string `json:"error,omitempty"`
}

// Fraction returns the progress in [0,1], or -1 if it is indeterminate.
func (i Info) Fraction() float64 {
	if i.Total <= 0 {
		return -1
	}
	return min(1, float64(i.Done)/float64(i.Total))
}

// ChangedEvent is the payload of Topic: all listed tasks, oldest first.
type ChangedEvent struct {
	Tasks []Info `json:"tasks"`
}

// Option configures a Registry.
type Option func(*Registry)

// WithInterval sets the minimum time between change events (<= 0: no
// throttling).
func WithInterval(d time.Duration) Option { return func(r *Registry) { r.interval = d } }

// WithFailedLinger sets how long failed tasks stay listed (<= 0: they
// disappear at once).
func WithFailedLinger(d time.Duration) Option { return func(r *Registry) { r.linger = d } }

// Registry holds the running tasks. It is safe for concurrent use.
type Registry struct {
	publish  func(ChangedEvent)
	interval time.Duration
	linger   time.Duration

	// pubMu serialises publishing so events leave in snapshot order.
	pubMu sync.Mutex

	mu      sync.Mutex
	seq     uint64
	tasks   []*Task // in start order
	lastPub time.Time
	timer   *time.Timer
	closed  bool
}

// New creates a registry that reports changes to publish (nil: none).
func New(publish func(ChangedEvent), opts ...Option) *Registry {
	r := &Registry{publish: publish, interval: DefaultInterval, linger: DefaultFailedLinger}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Task is one running background operation. Its methods are safe for
// concurrent use and do nothing on a nil Task.
type Task struct {
	r      *Registry
	ctx    context.Context
	cancel context.CancelFunc

	// own guards the fields of an unregistered task (r == nil).
	own sync.Mutex
	// Guarded by r.mu (or own when r is nil).
	info      Info
	ended     bool
	cancelled bool
}

// Begin registers a task. Its context (Task.Context) derives from ctx and
// is cancelled by Cancel, by End and when ctx ends. On a nil Registry
// Begin returns an unregistered task that still carries the context.
func (r *Registry) Begin(ctx context.Context, o Options) *Task {
	if ctx == nil {
		ctx = context.Background()
	}
	tctx, cancel := context.WithCancel(ctx)
	t := &Task{r: r, ctx: tctx, cancel: cancel, info: Info{
		Title: o.Title, Module: o.Module, Cancellable: o.Cancellable, Text: o.Text, StartedAt: time.Now(),
	}}
	if r == nil {
		return t
	}
	r.mu.Lock()
	r.seq++
	t.info.ID = "t" + strconv.FormatUint(r.seq, 10)
	if r.closed {
		t.ended = true
		r.mu.Unlock()
		cancel()
		return t
	}
	r.tasks = append(r.tasks, t)
	r.mu.Unlock()
	r.changed()
	return t
}

// Run begins a task, runs fn with it and ends it with fn's error.
func (r *Registry) Run(ctx context.Context, o Options, fn func(t *Task) error) error {
	t := r.Begin(ctx, o)
	err := fn(t)
	t.End(err)
	return err
}

// List returns the listed tasks, oldest first.
func (r *Registry) List() []Info {
	if r == nil {
		return []Info{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *Registry) snapshotLocked() []Info {
	out := make([]Info, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t.info)
	}
	return out
}

// Get returns the task with the given ID.
func (r *Registry) Get(id string) (Info, bool) {
	if r == nil {
		return Info{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.findLocked(id); t != nil {
		return t.info, true
	}
	return Info{}, false
}

func (r *Registry) findLocked(id string) *Task {
	for _, t := range r.tasks {
		if t.info.ID == id {
			return t
		}
	}
	return nil
}

// Cancel requests cancellation of a running task. A failed task that is
// still listed is dismissed instead.
func (r *Registry) Cancel(id string) error {
	if r == nil {
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	r.mu.Lock()
	t := r.findLocked(id)
	switch {
	case t == nil:
		r.mu.Unlock()
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	case t.ended: // failed, lingering
		r.removeLocked(t)
		r.mu.Unlock()
		r.changed()
		return nil
	case !t.info.Cancellable:
		r.mu.Unlock()
		return fmt.Errorf("%s (%s): %w", id, t.info.Title, ErrNotCancellable)
	}
	t.cancelled = true
	t.info.Cancelling = true
	r.mu.Unlock()
	t.cancel()
	r.changed()
	return nil
}

// Close cancels all tasks, stops the timers and publishes nothing more.
// Tasks begun afterwards are not registered.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	list := r.tasks
	r.tasks = nil
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.mu.Unlock()
	for _, t := range list {
		t.cancel()
	}
}

func (r *Registry) removeLocked(t *Task) {
	if i := slices.Index(r.tasks, t); i >= 0 {
		r.tasks = slices.Delete(r.tasks, i, i+1)
	}
}

// changed publishes the list now or schedules it for the end of the
// current throttle interval.
func (r *Registry) changed() {
	r.mu.Lock()
	if r.closed || r.publish == nil {
		r.mu.Unlock()
		return
	}
	if r.timer != nil {
		r.mu.Unlock()
		return // a trailing event is due anyway
	}
	if wait := r.interval - time.Since(r.lastPub); r.interval > 0 && !r.lastPub.IsZero() && wait > 0 {
		r.timer = time.AfterFunc(wait, r.flush)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.emit()
}

// flush is the trailing publish after a throttled change.
func (r *Registry) flush() {
	r.mu.Lock()
	r.timer = nil
	r.mu.Unlock()
	r.emit()
}

func (r *Registry) emit() {
	r.pubMu.Lock()
	defer r.pubMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	ev := ChangedEvent{Tasks: r.snapshotLocked()}
	r.lastPub = time.Now()
	r.mu.Unlock()
	r.publish(ev)
}

// ID returns the task ID ("" for a nil or unregistered task).
func (t *Task) ID() string {
	if t == nil {
		return ""
	}
	t.lock()
	defer t.unlock()
	return t.info.ID
}

// Context returns the task's context; it ends on Cancel and End.
func (t *Task) Context() context.Context {
	if t == nil {
		return context.Background()
	}
	return t.ctx
}

// Info returns the current state of the task.
func (t *Task) Info() Info {
	if t == nil {
		return Info{}
	}
	t.lock()
	defer t.unlock()
	return t.info
}

// Cancelled reports whether Cancel was requested for the task.
func (t *Task) Cancelled() bool {
	if t == nil {
		return false
	}
	t.lock()
	defer t.unlock()
	return t.cancelled
}

func (t *Task) lock() {
	if t.r != nil {
		t.r.mu.Lock()
		return
	}
	t.own.Lock()
}

func (t *Task) unlock() {
	if t.r != nil {
		t.r.mu.Unlock()
		return
	}
	t.own.Unlock()
}

// Progress reports done of total units (total 0: indeterminate) and an
// optional detail text ("" keeps the previous text). Values are clamped
// to 0 <= done <= total.
func (t *Task) Progress(done, total int, text string) {
	if t == nil {
		return
	}
	total = max(total, 0)
	done = max(done, 0)
	if total > 0 {
		done = min(done, total)
	}
	t.lock()
	if t.ended || (t.info.Done == done && t.info.Total == total && (text == "" || text == t.info.Text)) {
		t.unlock()
		return
	}
	t.info.Done, t.info.Total = done, total
	if text != "" {
		t.info.Text = text
	}
	t.unlock()
	if t.r != nil {
		t.r.changed()
	}
}

// SetTitle changes the title (e.g. when a task moves to another phase).
func (t *Task) SetTitle(title string) {
	if t == nil {
		return
	}
	t.lock()
	if t.ended || t.info.Title == title {
		t.unlock()
		return
	}
	t.info.Title = title
	t.unlock()
	if t.r != nil {
		t.r.changed()
	}
}

// End finishes the task. A nil error, a cancellation (requested through
// Cancel, or err wrapping context.Canceled) removes it at once; any other
// error keeps it listed with the error for FailedLinger. End is
// idempotent and releases the task's context.
func (t *Task) End(err error) {
	if t == nil {
		return
	}
	defer t.cancel()
	r := t.r
	if r == nil {
		t.own.Lock()
		t.ended = true
		t.own.Unlock()
		return
	}
	r.mu.Lock()
	if t.ended {
		r.mu.Unlock()
		return
	}
	t.ended = true
	failed := err != nil && !t.cancelled && !errors.Is(err, context.Canceled)
	if !failed || r.linger <= 0 || r.closed {
		r.removeLocked(t)
		r.mu.Unlock()
		r.changed()
		return
	}
	t.info.Error = err.Error()
	t.info.Cancelling = false
	r.mu.Unlock()
	time.AfterFunc(r.linger, func() {
		r.mu.Lock()
		present := slices.Contains(r.tasks, t)
		r.removeLocked(t)
		r.mu.Unlock()
		if present {
			r.changed()
		}
	})
	r.changed()
}
