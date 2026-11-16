// Package controller holds the state and logic of Tiegel's GUI without any
// Gio dependency: open tabs, file tree, diagnostics, completion, navigation
// history, preferences and the status shown in the status bar.
//
// The controller is a client of the API (D41): it calls API methods by name
// through a Backend and reacts to bus events. All state is owned by the UI
// goroutine. Blocking calls run on background goroutines (Go); their results
// are queued and applied on the UI goroutine by Drain, which the window calls
// at the start of every frame. Invalidate is called whenever new work is
// queued so the window schedules a frame.
//
// Documents are shared in-process (api.App.Document): the editor widget
// edits the buffer directly while holding the document lock, see DocHandle.
package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Backend is the narrow interface the GUI uses to reach the backend. Method
// names and parameters are exactly those of the API (docs/headless.md), so
// an in-process and a remote implementation are interchangeable.
type Backend interface {
	// Call invokes method with params (any JSON-serialisable value, may be
	// nil) and decodes the result into result (a pointer, may be nil).
	Call(ctx context.Context, method string, params, result any) error
	// Subscribe forwards bus events of the topic patterns to fn (called
	// from background goroutines, in publish order per pattern) until
	// cancel is called.
	Subscribe(fn func(topic string, payload json.RawMessage), patterns ...string) (cancel func())
	// Document returns the shared document for an open path (after
	// "doc.open"). Only available in-process.
	Document(path string) (DocHandle, error)
}

// DocHandle is a shared open document. Every access to Buffer must happen
// between Lock and Unlock (see api.Doc).
type DocHandle interface {
	Lock()
	Unlock()
	Buffer() *buffer.Buffer
	// Path is the absolute file path.
	Path() string
}

// InProcess is the Backend of a GUI running in the same process as the
// api.App. Results are converted through JSON so the controller sees the
// same shapes as a remote client would.
type InProcess struct {
	app  *api.App
	sess *api.Session
}

// NewInProcess returns a Backend for a.
func NewInProcess(a *api.App) *InProcess {
	return &InProcess{app: a, sess: a.NewSession(nil)}
}

// Call implements Backend.
func (b *InProcess) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("%s: encoding params: %w", method, err)
	}
	if params == nil {
		raw = nil
	}
	res, err := b.sess.Call(ctx, method, raw)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("%s: encoding result: %w", method, err)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}

// Subscribe implements Backend.
func (b *InProcess) Subscribe(fn func(topic string, payload json.RawMessage), patterns ...string) func() {
	s := b.app.NewSession(func(e api.Event) { fn(e.Topic, e.Payload) })
	s.Subscribe(patterns...)
	return s.Close
}

// Document implements Backend.
func (b *InProcess) Document(path string) (DocHandle, error) {
	d, err := b.app.Document(path)
	if err != nil {
		return nil, err
	}
	return d, nil
}
