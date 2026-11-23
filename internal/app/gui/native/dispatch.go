package native

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
)

// Callbacks from Cocoa (menu items, gestures) run on the main thread inside
// the run loop. Gio owns that thread: anything that waits for the Gio
// window there (Window.Invalidate, locks held by the window goroutine)
// deadlocks the whole application. The callbacks therefore only enqueue
// the work; a dispatcher goroutine runs it in order, isolated from panics.

// dispatchQueue is the capacity of the callback queue; when the receiver
// is stuck, further callbacks are dropped instead of blocking the main
// thread.
const dispatchQueue = 64

// dispatcher runs posted functions one after another on its own goroutine.
type dispatcher struct {
	once sync.Once
	ch   chan func()
	// report receives recovered panics (default: stderr).
	report func(r any, stack []byte)
}

// post enqueues fn without blocking; it reports false when the queue is
// full (fn is dropped).
func (d *dispatcher) post(fn func()) bool {
	d.once.Do(func() {
		d.ch = make(chan func(), dispatchQueue)
		go d.run()
	})
	select {
	case d.ch <- fn:
		return true
	default:
		return false
	}
}

func (d *dispatcher) run() {
	for fn := range d.ch {
		d.call(fn)
	}
}

// call runs fn; a panic is reported and does not stop the dispatcher.
func (d *dispatcher) call(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			if d.report != nil {
				d.report(r, debug.Stack())
				return
			}
			fmt.Fprintf(os.Stderr, "tiegel: native callback panicked: %v\n%s", r, debug.Stack())
		}
	}()
	fn()
}

// callbacks dispatches the menu and gesture callbacks of the platform
// implementations.
var callbacks dispatcher
