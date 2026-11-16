// Package docsync keeps a local copy of a backend document for clients that
// cannot share the backend's buffer – a GUI talking to a remote backend
// (WSL, socket) through api.Caller.
//
// A Mirror holds its own buffer.Buffer. Local edits are sent as doc.insert,
// doc.delete or doc.replace together with the version the mirror is at; the
// server applies them only if nobody else changed the document in between
// (optimistic locking). Edits of other clients arrive as doc.edited events
// and are applied in version order.
//
// The server is the authority. Whenever the mirror cannot follow – the
// server rejects a local edit with a version conflict, an event is missing
// (version gap) or does not fit the local text – the mirror reloads the
// whole text with doc.text. A rejected local edit is then lost and the call
// returns an error matching ErrConflict, so the editor can tell the user or
// retry on the new text.
//
// Local edits are applied after the server confirmed them (one round trip
// per edit, no operational transformation). That keeps the mirror byte for
// byte equal to the server, which is all a local socket or WSL connection
// needs.
//
// The package does not import Gio.
package docsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// ErrConflict reports a local edit the server rejected because the document
// changed in between; the mirror has been reloaded.
var ErrConflict = errors.New("docsync: version conflict, document reloaded")

// ErrClosed is returned after Close or when the server closed the document.
var ErrClosed = errors.New("docsync: mirror closed")

// Change describes a change of the mirror's buffer.
type Change struct {
	// Edit is the change in buffer coordinates.
	Edit buffer.Edit
	// Version is the server version after the change.
	Version uint64
	// Remote is true for changes made by other clients (or server-side
	// operations such as undo); false for edits made through this mirror.
	Remote bool
	// Reload is true if the whole text was replaced to resynchronise.
	Reload bool
}

// Options configure Open.
type Options struct {
	// Create opens an empty document if the file does not exist.
	Create bool
	// OnChange is called for every change of the buffer. It runs while
	// the mirror is locked: read the buffer through the Change, do not call
	// Mirror methods and do not block.
	OnChange func(Change)
	// OnClosed is called (once, without the lock) when the server closed
	// the document.
	OnClosed func()
	// Timeout bounds reloads triggered by events (default 10 s).
	Timeout time.Duration
}

// Mirror is a local copy of an open backend document. It is safe for
// concurrent use.
type Mirror struct {
	c    api.Caller
	opts Options

	mu      sync.Mutex
	path    string // display path as used in events
	buf     *buffer.Buffer
	version uint64
	state   api.DocState
	remote  bool // origin of the edit being applied
	reload  bool
	closed  bool
	changed chan struct{} // closed and replaced when version changes

	unsubscribe func()
}

// Open opens path on the server (doc.open) and mirrors it.
func Open(ctx context.Context, c api.Caller, path string, opts Options) (*Mirror, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	m := &Mirror{c: c, opts: opts, changed: make(chan struct{})}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Subscribe first so no edit after doc.open can be missed; events wait
	// for the lock and are filtered by version.
	cancel, err := c.Subscribe(ctx, []string{"doc.*"}, m.onEvent)
	if err != nil {
		return nil, fmt.Errorf("docsync: subscribe: %w", err)
	}
	res, err := api.Call[api.DocOpenResult](ctx, c, "doc.open", api.DocOpenParams{Path: path, Create: opts.Create, Text: true})
	if err != nil {
		cancel()
		return nil, err
	}
	m.unsubscribe = cancel
	m.path = res.Path
	m.state = res.DocState
	m.version = res.Version
	m.buf = buffer.NewString(res.Text)
	m.buf.OnEdit(m.onBufferEdit)
	return m, nil
}

func (m *Mirror) onBufferEdit(e buffer.Edit) {
	if m.opts.OnChange != nil {
		m.opts.OnChange(Change{Edit: e, Version: m.version, Remote: m.remote, Reload: m.reload})
	}
}

// Path returns the document path as the server reports it. It follows
// renames and moves (doc.renamed).
func (m *Mirror) Path() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path
}

// Version returns the server version the local buffer corresponds to.
func (m *Mirror) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// Text returns the current local text.
func (m *Mirror) Text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

// State returns the last document state reported by the server (modified
// flag, undo availability, ...).
func (m *Mirror) State() api.DocState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Read runs fn with the local buffer while the mirror is locked. fn must
// not modify the buffer (use Insert/Delete/Replace) and must not block.
func (m *Mirror) Read(fn func(b *buffer.Buffer, version uint64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m.buf, m.version)
}

// Insert inserts text at byte offset off.
func (m *Mirror) Insert(ctx context.Context, off int, text string) error {
	return m.Replace(ctx, off, off, text)
}

// Delete removes the bytes [start, end).
func (m *Mirror) Delete(ctx context.Context, start, end int) error {
	return m.Replace(ctx, start, end, "")
}

// Replace replaces the bytes [start, end) with text: on the server first
// (with the mirror's version), then locally. If the server's document has
// moved on, the mirror is reloaded and an error matching ErrConflict is
// returned; the edit is not applied.
func (m *Mirror) Replace(ctx context.Context, start, end int, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if start < 0 || end < start || end > m.buf.Len() {
		return api.Errorf(api.CodeInvalidParams, "range [%d,%d) out of bounds (document has %d bytes)", start, end, m.buf.Len())
	}
	if string(m.buf.Slice(start, end)) == text {
		return nil
	}
	var (
		st  api.DocState
		err error
	)
	v := api.VersionOf(m.version)
	switch {
	case start == end:
		st, err = api.Call[api.DocState](ctx, m.c, "doc.insert", api.DocInsertParams{Path: m.path, At: api.Pos{Offset: start}, Text: text, Version: v})
	case text == "":
		st, err = api.Call[api.DocState](ctx, m.c, "doc.delete", api.DocDeleteParams{Path: m.path, Start: api.Pos{Offset: start}, End: api.Pos{Offset: end}, Version: v})
	default:
		st, err = api.Call[api.DocState](ctx, m.c, "doc.replace", api.DocReplaceParams{Path: m.path, Start: &api.Pos{Offset: start}, End: &api.Pos{Offset: end}, Text: text, Version: v})
	}
	if err != nil {
		if api.ErrorCode(err) == api.CodeConflict {
			if rerr := m.reloadLocked(ctx); rerr != nil {
				return errors.Join(ErrConflict, rerr)
			}
			return ErrConflict
		}
		return err
	}
	m.remote, m.reload = false, false
	m.setVersionLocked(st.Version)
	m.state = st
	if _, err := m.buf.Replace(start, end-start, []byte(text)); err != nil {
		// Cannot happen after the range check; resynchronise anyway.
		return errors.Join(err, m.reloadLocked(ctx))
	}
	return nil
}

// Undo undoes the last change on the server (doc.undo) and returns once the
// mirror has caught up. done is false if there was nothing to undo.
func (m *Mirror) Undo(ctx context.Context) (done bool, err error) { return m.history(ctx, "doc.undo") }

// Redo is the counterpart of Undo.
func (m *Mirror) Redo(ctx context.Context) (done bool, err error) { return m.history(ctx, "doc.redo") }

func (m *Mirror) history(ctx context.Context, method string) (bool, error) {
	if err := m.checkOpen(); err != nil {
		return false, err
	}
	res, err := api.Call[api.UndoResult](ctx, m.c, method, api.PathParams{Path: m.Path()})
	if err != nil {
		return false, err
	}
	return res.Done, m.WaitVersion(ctx, res.Version)
}

// Save saves the document on the server (doc.save).
func (m *Mirror) Save(ctx context.Context) (api.DocSaveResult, error) {
	if err := m.checkOpen(); err != nil {
		return api.DocSaveResult{}, err
	}
	res, err := api.Call[api.DocSaveResult](ctx, m.c, "doc.save", api.PathParams{Path: m.Path()})
	if err != nil {
		return res, err
	}
	m.mu.Lock()
	if res.Version == m.version {
		m.state = res.DocState
	}
	m.mu.Unlock()
	return res, nil
}

// WaitVersion blocks until the mirror has applied version v (or a later
// one).
func (m *Mirror) WaitVersion(ctx context.Context, v uint64) error {
	for {
		m.mu.Lock()
		cur, ch, closed := m.version, m.changed, m.closed
		m.mu.Unlock()
		if cur >= v {
			return nil
		}
		if closed {
			return ErrClosed
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Reload replaces the local text with the server's.
func (m *Mirror) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return m.reloadLocked(ctx)
}

func (m *Mirror) reloadLocked(ctx context.Context) error {
	txt, err := api.Call[api.DocText](ctx, m.c, "doc.text", api.DocTextParams{Path: m.path})
	if err != nil {
		return fmt.Errorf("docsync: reload: %w", err)
	}
	m.setVersionLocked(txt.Version)
	if m.buf.String() != txt.Text {
		m.remote, m.reload = true, true
		_, _ = m.buf.Replace(0, m.buf.Len(), []byte(txt.Text))
		m.reload = false
	}
	return nil
}

func (m *Mirror) setVersionLocked(v uint64) {
	m.version = v
	m.state.Version = v
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Mirror) checkOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

// onEvent follows the server's doc.* events. It runs on the caller's event
// goroutine, one event at a time in publish order.
func (m *Mirror) onEvent(e api.Event) {
	switch e.Topic {
	case api.TopicDocEdited:
		var ev api.DocEditEvent
		if json.Unmarshal(e.Payload, &ev) != nil {
			return
		}
		m.applyRemote(ev)
	case api.TopicDocChanged, api.TopicDocSaved:
		var st api.DocState
		if json.Unmarshal(e.Payload, &st) != nil {
			return
		}
		m.mu.Lock()
		if st.Path == m.path && st.Version == m.version {
			m.state = st
		}
		m.mu.Unlock()
	case api.TopicDocRenamed:
		var ev api.DocRenamedEvent
		if json.Unmarshal(e.Payload, &ev) != nil {
			return
		}
		m.mu.Lock()
		if ev.From == m.path && ev.Generation == m.state.Generation && !m.closed {
			m.path = ev.To
			m.state.Path = ev.To
		}
		m.mu.Unlock()
	case api.TopicDocClosed:
		var res api.DocCloseResult
		if json.Unmarshal(e.Payload, &res) != nil {
			return
		}
		m.mu.Lock()
		mine := res.Path == m.path && !m.closed
		m.mu.Unlock()
		if mine {
			m.detach()
			if m.opts.OnClosed != nil {
				m.opts.OnClosed()
			}
		}
	}
}

func (m *Mirror) applyRemote(ev api.DocEditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ev.Path != m.path || ev.Version <= m.version {
		return // other document, or a change we already have (our own)
	}
	fits := ev.Version == m.version+1 &&
		ev.Start >= 0 && ev.Start <= ev.OldEnd && ev.OldEnd <= m.buf.Len() &&
		string(m.buf.Slice(ev.Start, ev.OldEnd)) == ev.OldText
	if !fits {
		ctx, cancel := context.WithTimeout(context.Background(), m.opts.Timeout)
		defer cancel()
		_ = m.reloadLocked(ctx)
		return
	}
	m.remote, m.reload = true, false
	m.setVersionLocked(ev.Version)
	_, _ = m.buf.Replace(ev.Start, ev.OldEnd-ev.Start, []byte(ev.Text))
}

// detach stops following the server.
func (m *Mirror) detach() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.changed)
	m.changed = make(chan struct{})
	un := m.unsubscribe
	m.mu.Unlock()
	if un != nil {
		un()
	}
}

// Close stops mirroring. The document stays open on the server for other
// clients; use CloseDocument to close it there as well.
func (m *Mirror) Close() error {
	m.detach()
	return nil
}

// CloseDocument closes the document on the server (doc.close with the
// mirrored generation) and stops mirroring. Unsaved changes are never
// discarded: the server then answers CodeConflict and the mirror stays
// attached (save first).
func (m *Mirror) CloseDocument(ctx context.Context) (api.DocCloseResult, error) {
	if err := m.checkOpen(); err != nil {
		return api.DocCloseResult{}, err
	}
	m.mu.Lock()
	params := api.DocCloseParams{Path: m.path, Generation: m.state.Generation}
	m.mu.Unlock()
	res, err := api.Call[api.DocCloseResult](ctx, m.c, "doc.close", params)
	if err != nil {
		return res, err
	}
	m.detach()
	return res, nil
}
