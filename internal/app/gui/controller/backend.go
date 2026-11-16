// Package controller holds the state and logic of Tiegel's GUI without any
// Gio dependency: open tabs, file tree, diagnostics, completion, navigation
// history, preferences and the status shown in the status bar.
//
// The controller is a client of the API (D41): it calls API methods by name
// through an api.Caller and reacts to bus events. All state is owned by the
// UI goroutine. Blocking calls run on background goroutines (Go); their
// results are queued and applied on the UI goroutine by Drain, which the
// window calls at the start of every frame. Invalidate is called whenever
// new work is queued so the window schedules a frame.
//
// Documents are shared in-process (api.App.Document): the editor widget
// edits the buffer directly while holding the document lock, see DocHandle.
package controller

import (
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Backend is what the GUI needs from the backend: the API (method calls by
// name, exactly as in docs/headless.md, and event subscriptions) plus
// access to the shared document buffers for in-process editing.
type Backend interface {
	api.Caller
	// Document returns the shared document for an open path (after
	// "doc.open").
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
// api.App: api.InProcess for calls and events, App.Document for buffers.
type InProcess struct {
	*api.InProcess
}

// NewInProcess returns a Backend for a.
func NewInProcess(a *api.App) *InProcess {
	return &InProcess{InProcess: api.NewInProcess(a)}
}

// Document implements Backend.
func (b *InProcess) Document(path string) (DocHandle, error) {
	d, err := b.App().Document(path)
	if err != nil {
		return nil, err
	}
	return d, nil
}
