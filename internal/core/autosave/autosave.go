// Package autosave implements PhpStorm-style automatic saving without any
// UI dependency.
//
// Documents are saved
//
//   - when the editor loses focus (window, tab switch, terminal) – FocusLost,
//   - before Run, Debug and Commit – BeforeRun, BeforeDebug, BeforeCommit,
//   - optionally after a period of inactivity – Activity + idle delay,
//   - on explicit request – SaveAll / SaveDoc with TriggerManual.
//
// The editor side is abstracted by DirtyDocs. Every save of a document runs
// the same pipeline:
//
//  1. filter hook "file.beforeSave" (BeforeSave), e.g. formatters; failing
//     filters are skipped and reported,
//  2. DirtyDocs.Save writes the (filtered) content,
//  3. the content is recorded in Local History,
//  4. action hook "file.saved" (Saved) is fired.
//
// Steps 3 and 4 never fail a save; their errors go to Config.OnError.
// SaveBeforeGuard and SaveOnAction attach the manager to existing hook
// points (e.g. "commit.before", "editor.focusLost").
package autosave

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/history"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// Trigger tells why a save happened.
type Trigger string

// Triggers.
const (
	TriggerManual       Trigger = "manual"
	TriggerFocusLost    Trigger = "focusLost"
	TriggerBeforeRun    Trigger = "beforeRun"
	TriggerBeforeDebug  Trigger = "beforeDebug"
	TriggerBeforeCommit Trigger = "beforeCommit"
	TriggerIdle         Trigger = "idle"
	// TriggerClose saves before the application (window) closes.
	TriggerClose Trigger = "close"
)

// Doc is a snapshot of an unsaved document.
type Doc struct {
	Path    string
	Content []byte
	// Version identifies the buffer state the snapshot was taken from, so
	// DirtyDocs.Save can avoid marking a document clean that changed in
	// the meantime.
	Version uint64
	// Instance optionally identifies the open document instance the
	// snapshot was taken from (0 = address by path only). DirtyDocs.Save
	// returns ErrDocGone instead of writing when that instance was closed
	// meanwhile (discarded content must not be written, and versions of a
	// reopened document must not be confused with the old ones).
	Instance uint64
	// Force asks DirtyDocs.Save to write even if the file was changed by
	// another program since it was last read or written (see
	// ErrExternalChange). Only explicit user decisions set it.
	Force bool
}

// ErrDocGone is returned by DirtyDocs.Save when the document instance of
// the snapshot is no longer open. SaveAll skips such documents silently.
var ErrDocGone = errors.New("autosave: document is no longer open")

// ErrExternalChange is returned (wrapped) by DirtyDocs.Save when the file
// on disk was changed by another program since the document last read or
// wrote it: the save is refused instead of silently overwriting the other
// change. SaveAll skips such documents for the background triggers
// (idle, focusLost) – the conflict is reported to the user separately –
// and reports them as errors for all others (e.g. before a commit, which
// must not run on content the user has not seen).
var ErrExternalChange = errors.New("file changed on disk")

// DirtyDocs is the editor-side view autosave works with.
type DirtyDocs interface {
	// Dirty returns snapshots of all documents with unsaved changes.
	Dirty() []Doc
	// Save writes doc.Content to doc.Path (atomically) and marks the
	// document clean if its version is still doc.Version. doc.Content may
	// differ from the snapshot when a file.beforeSave filter changed it;
	// the implementation should then update the buffer as well. It
	// returns ErrDocGone (without writing) if doc.Instance is set and that
	// instance is no longer open, and an error wrapping ErrExternalChange
	// (without writing) if the file was changed externally and doc.Force
	// is not set.
	Save(ctx context.Context, doc Doc) error
}

// History records saved content. *history.Store implements it.
type History interface {
	Save(path string, content []byte, label string) (history.Revision, bool, error)
}

// SaveEvent is the payload of the BeforeSave filter. Filters may change
// Content; changes to Path or Trigger are ignored.
type SaveEvent struct {
	Path    string
	Content []byte
	Trigger Trigger
}

// SavedEvent is the payload of the Saved action.
type SavedEvent struct {
	Path    string
	Trigger Trigger
	Size    int
	// Revision is the Local History revision of the saved content (zero if
	// no history is configured or recording failed).
	Revision history.Revision
}

// Hook points used by the save pipeline.
var (
	BeforeSave = hooks.NewFilter[SaveEvent]("file.beforeSave")
	Saved      = hooks.NewAction[SavedEvent]("file.saved")
)

// Timer is a stoppable pending call, as returned by Clock.AfterFunc.
type Timer interface {
	Stop() bool
}

// Clock abstracts time for the idle timer.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// RealClock returns a Clock backed by package time.
func RealClock() Clock { return realClock{} }

// DefaultBackgroundTimeout bounds idle saves, which have no caller context.
const DefaultBackgroundTimeout = 30 * time.Second

// Config configures a Manager.
type Config struct {
	// Docs is required.
	Docs DirtyDocs
	// Hooks is optional; without it no hooks run.
	Hooks *hooks.Registry
	// History is optional; without it no revisions are recorded.
	History History
	// Clock defaults to RealClock().
	Clock Clock
	// IdleDelay enables saving after this much inactivity; 0 disables it.
	IdleDelay time.Duration
	// BackgroundTimeout bounds idle saves (default DefaultBackgroundTimeout).
	BackgroundTimeout time.Duration
	// OnError receives non-fatal problems (filter, history and saved-hook
	// failures) and errors of idle saves. Must be safe for concurrent use.
	OnError func(error)
}

// Manager runs autosave. It is safe for concurrent use.
type Manager struct {
	cfg Config

	saveMu sync.Mutex // serialises save runs

	mu      sync.Mutex
	delay   time.Duration
	timer   Timer
	gen     uint64
	closed  bool
	lastAct time.Time
	idle    sync.WaitGroup // running idle saves (Close waits for them)
}

// ErrEmptyFilterResult is reported (Config.OnError) when the
// file.beforeSave filters turn non-empty content into empty content; the
// unfiltered content is saved instead.
var ErrEmptyFilterResult = errors.New("filter returned empty content for non-empty input")

// ErrNoDocs is returned by New when Config.Docs is nil.
var ErrNoDocs = errors.New("autosave: Config.Docs is required")

// New creates a manager.
func New(cfg Config) (*Manager, error) {
	if cfg.Docs == nil {
		return nil, ErrNoDocs
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock()
	}
	if cfg.BackgroundTimeout <= 0 {
		cfg.BackgroundTimeout = DefaultBackgroundTimeout
	}
	if cfg.OnError == nil {
		cfg.OnError = func(error) {}
	}
	return &Manager{cfg: cfg, delay: cfg.IdleDelay}, nil
}

// SaveAll saves every dirty document. Documents are saved independently;
// the errors of documents that could not be written are returned joined.
func (m *Manager) SaveAll(ctx context.Context, trigger Trigger) error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	var errs []error
	for _, d := range m.cfg.Docs.Dirty() {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		_, err := m.saveLocked(ctx, d, trigger)
		switch {
		case err == nil, errors.Is(err, ErrDocGone):
		case errors.Is(err, ErrExternalChange) && background(trigger):
		default:
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// background reports whether trigger saves without the user asking for it
// and without an operation depending on the result.
func background(trigger Trigger) bool {
	return trigger == TriggerIdle || trigger == TriggerFocusLost
}

// SaveWith runs the save pipeline for the document snapshot returns. The
// snapshot is taken while holding the save lock, so it can never be older
// than content another save (e.g. autosave) has written in the meantime;
// an explicit save with a stale copy would otherwise overwrite newer
// content on disk while the buffer stays marked as saved.
func (m *Manager) SaveWith(ctx context.Context, trigger Trigger, snapshot func() (Doc, error)) (SavedEvent, error) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	d, err := snapshot()
	if err != nil {
		return SavedEvent{}, err
	}
	return m.saveLocked(ctx, d, trigger)
}

// Exclusive runs fn while no save runs: it holds the lock that serialises
// all saves of m. Use it for other writers of document files (restoring a
// revision, reloading after a git operation, closing a document), so that
// a save in flight cannot overwrite their result with an older snapshot.
// fn must not save through m (that would deadlock).
func (m *Manager) Exclusive(fn func() error) error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	return fn()
}

// SaveDoc runs the save pipeline for a single document (e.g. explicit
// Cmd+S) and returns the resulting event.
func (m *Manager) SaveDoc(ctx context.Context, d Doc, trigger Trigger) (SavedEvent, error) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	return m.saveLocked(ctx, d, trigger)
}

func (m *Manager) saveLocked(ctx context.Context, d Doc, trigger Trigger) (SavedEvent, error) {
	content := d.Content
	if m.cfg.Hooks != nil {
		ev, err := BeforeSave.Apply(ctx, m.cfg.Hooks, SaveEvent{Path: d.Path, Content: content, Trigger: trigger})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SavedEvent{}, fmt.Errorf("autosave: %s: %w", d.Path, ctxErr)
		}
		if err != nil {
			m.cfg.OnError(fmt.Errorf("autosave: %s: before-save filters: %w", d.Path, err))
		}
		if len(ev.Content) == 0 && len(content) > 0 {
			// A filter that returns an empty event (e.g. a zero SaveEvent
			// on an internal error) would empty file and buffer. Treat it
			// as a filter failure and save the unfiltered content.
			m.cfg.OnError(fmt.Errorf("autosave: %s: before-save filters: %w", d.Path, ErrEmptyFilterResult))
		} else {
			content = ev.Content
		}
	}

	// A cancelled or timed-out save must not write anymore: the caller
	// (e.g. the commit guard) has already given up on it.
	if err := ctx.Err(); err != nil {
		return SavedEvent{}, fmt.Errorf("autosave: %s: %w", d.Path, err)
	}
	if err := m.cfg.Docs.Save(ctx, Doc{Path: d.Path, Content: content, Version: d.Version, Instance: d.Instance, Force: d.Force}); err != nil {
		return SavedEvent{}, fmt.Errorf("autosave: save %s: %w", d.Path, err)
	}

	saved := SavedEvent{Path: d.Path, Trigger: trigger, Size: len(content)}
	if m.cfg.History != nil {
		rev, _, err := m.cfg.History.Save(d.Path, content, "autosave: "+string(trigger))
		if err != nil {
			m.cfg.OnError(fmt.Errorf("autosave: %s: local history: %w", d.Path, err))
		} else {
			saved.Revision = rev
		}
	}
	if m.cfg.Hooks != nil {
		if err := Saved.Do(ctx, m.cfg.Hooks, saved); err != nil {
			m.cfg.OnError(fmt.Errorf("autosave: %s: file.saved hooks: %w", d.Path, err))
		}
	}
	return saved, nil
}

// FocusLost saves all dirty documents because the editor lost focus.
func (m *Manager) FocusLost(ctx context.Context) error { return m.SaveAll(ctx, TriggerFocusLost) }

// BeforeRun saves all dirty documents before a run configuration starts.
func (m *Manager) BeforeRun(ctx context.Context) error { return m.SaveAll(ctx, TriggerBeforeRun) }

// BeforeDebug saves all dirty documents before a debug session starts.
func (m *Manager) BeforeDebug(ctx context.Context) error { return m.SaveAll(ctx, TriggerBeforeDebug) }

// BeforeCommit saves all dirty documents before the commit dialog commits.
func (m *Manager) BeforeCommit(ctx context.Context) error {
	return m.SaveAll(ctx, TriggerBeforeCommit)
}

// Activity signals user activity (typing, cursor moves) and restarts the
// idle timer if an idle delay is configured.
func (m *Manager) Activity() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAct = m.cfg.Clock.Now()
	m.armLocked()
}

// LastActivity returns the time of the last Activity call (zero if none).
func (m *Manager) LastActivity() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastAct
}

// SetIdleDelay changes the idle delay; 0 disables idle saving. A running
// timer is restarted with the new delay.
func (m *Manager) SetIdleDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
	if m.timer != nil {
		m.armLocked()
	}
}

// IdleDelay returns the current idle delay.
func (m *Manager) IdleDelay() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.delay
}

func (m *Manager) armLocked() {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.gen++
	if m.closed || m.delay <= 0 {
		return
	}
	gen := m.gen
	m.timer = m.cfg.Clock.AfterFunc(m.delay, func() { m.idleFired(gen) })
}

func (m *Manager) idleFired(gen uint64) {
	m.mu.Lock()
	if gen != m.gen || m.closed {
		m.mu.Unlock()
		return // superseded by newer activity
	}
	m.timer = nil
	m.idle.Add(1) // under mu with closed checked: never after Close's Wait
	m.mu.Unlock()
	defer m.idle.Done()

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.BackgroundTimeout)
	defer cancel()
	if err := m.SaveAll(ctx, TriggerIdle); err != nil {
		m.cfg.OnError(err)
	}
}

// Close stops the idle timer and waits for a running idle save, so that no
// file is written after Close returns. Saves started by callers (SaveAll,
// FocusLost, …) are theirs to wait for.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.armLocked()
	m.mu.Unlock()
	m.idle.Wait()
}

// AttachPriority is the priority SaveBeforeGuard and SaveOnAction use, so
// that saving happens before any other hook (e.g. a linter) looks at files.
const AttachPriority = math.MinInt32

// AttachTimeout is the hook time limit used by SaveBeforeGuard and
// SaveOnAction; saving many files may take longer than the default limit.
const AttachTimeout = 30 * time.Second

// SaveBeforeGuard registers a guard hook on g that saves all dirty
// documents first (e.g. on "commit.before" with TriggerBeforeCommit). If a
// document cannot be saved, the guard vetoes the operation. The guard
// fails closed: a timeout or panic vetoes as well, so the operation never
// runs on files whose save did not complete.
func SaveBeforeGuard[T any](m *Manager, r *hooks.Registry, g hooks.Guard[T], trigger Trigger) (remove func()) {
	return g.On(r, AttachPriority, func(ctx context.Context, _ T) error {
		return m.SaveAll(ctx, trigger)
	}, hooks.WithOwner("autosave"), hooks.WithTimeout(AttachTimeout), hooks.WithFailClosed())
}

// SaveOnAction registers an action hook on a that saves all dirty
// documents (e.g. on "editor.focusLost" with TriggerFocusLost).
func SaveOnAction[T any](m *Manager, r *hooks.Registry, a hooks.Action[T], trigger Trigger) (remove func()) {
	return a.On(r, AttachPriority, func(ctx context.Context, _ T) error {
		return m.SaveAll(ctx, trigger)
	}, hooks.WithOwner("autosave"), hooks.WithTimeout(AttachTimeout))
}
