// Package workspace manages several open projects of one window (AP18.5):
// exactly one project is active, the others run in the background (all
// their resources stay up) or are hibernated (language servers, watchers,
// indexer and documents released; only a small snapshot remains).
//
// The package is generic: a Project is anything with a Hibernate/Resume/
// Close lifecycle. internal/api implements it on top of one api.App per
// project; tests use fakes. Lifecycle changes are published on a bus as
// typed topics (TopicOpened … TopicClosed). Background projects can be
// hibernated automatically after a configurable time; the timers use an
// injectable Clock.
//
// The package has no Gio dependency (backend, D41).
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// State is the lifecycle state of a project session.
type State string

// States of a session.
const (
	// StateActive is the project shown in the window (at most one).
	StateActive State = "active"
	// StateBackground keeps all resources running (LSP, watcher, runs).
	StateBackground State = "background"
	// StateHibernated has released its resources; Resume restores them.
	StateHibernated State = "hibernated"
)

// Project is the backend of one open project. The Manager calls its
// methods one at a time (never concurrently for the whole Manager).
type Project interface {
	// Busy describes work that blocks hibernating or closing without
	// force, e.g. running processes or debug sessions ("run: phpunit").
	Busy(ctx context.Context) []string
	// Hibernate saves the documents and releases the resources. It
	// returns the open documents to restore on Resume. Without force,
	// documents that cannot be saved make it fail (wrap ErrUnsaved);
	// with force their content is preserved otherwise (recovery points).
	Hibernate(ctx context.Context, force bool) (docs []string, err error)
	// Resume brings the resources back and reopens docs.
	Resume(ctx context.Context, docs []string) error
	// Close saves and releases everything for good (force as in
	// Hibernate).
	Close(ctx context.Context, force bool) error
}

// HibernateBlocker is implemented by projects for which less work blocks
// hibernating than closing (e.g. terminals survive a hibernation, D547,
// but not a close). The Manager then asks HibernateBusy instead of Busy
// before hibernating.
type HibernateBlocker interface {
	HibernateBusy(ctx context.Context) []string
}

// hibernateBusy reports what blocks hibernating p.
func hibernateBusy(ctx context.Context, p Project) []string {
	if hb, ok := p.(HibernateBlocker); ok {
		return hb.HibernateBusy(ctx)
	}
	return p.Busy(ctx)
}

// Opener creates the Project for an absolute project root.
type Opener func(ctx context.Context, root string) (Project, error)

// Errors reported by the Manager (test with errors.Is).
var (
	// ErrNotFound: no open project matches the name.
	ErrNotFound = errors.New("workspace: project not open")
	// ErrAmbiguous: several open projects share the name; use the root.
	ErrAmbiguous = errors.New("workspace: ambiguous project name")
	// ErrBusy: running work blocks the operation (see BusyError).
	ErrBusy = errors.New("workspace: project busy")
	// ErrUnsaved: documents could not be saved.
	ErrUnsaved = errors.New("workspace: unsaved documents")
	// ErrClosed: the Manager was closed.
	ErrClosed = errors.New("workspace: closed")
)

// BusyError lists what blocks hibernating or closing a project. It
// matches ErrBusy.
type BusyError struct {
	Name    string
	Reasons []string
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("workspace: %s is busy (%s); use force", e.Name, strings.Join(e.Reasons, ", "))
}

// Is reports ErrBusy.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// Info describes one project session.
type Info struct {
	Name  string `json:"name"`
	Root  string `json:"root"`
	State State  `json:"state"`
	// Since is when the session entered State.
	Since time.Time `json:"since"`
	// Docs are the documents restored on resume (hibernated sessions).
	Docs []string `json:"docs,omitempty"`
	// AutoHibernateAt is when a background session will be hibernated
	// automatically (nil: never).
	AutoHibernateAt *time.Time `json:"autoHibernateAt,omitempty"`
	// ClientState is the opaque state a client stored on hibernation
	// (open tabs, cursors, layout); it is returned by the call that
	// resumes the session and then dropped.
	ClientState json.RawMessage `json:"clientState,omitempty"`
	// Previous is the project that was active before a switch or open
	// with SwitchOptions.HibernatePrevious (results of those calls only),
	// PreviousState its state afterwards and PreviousBusy why it stayed
	// in the background instead of hibernating.
	Previous      string   `json:"previous,omitempty"`
	PreviousState State    `json:"previousState,omitempty"`
	PreviousBusy  []string `json:"previousBusy,omitempty"`
}

// SwitchOptions are the options of SwitchWith and OpenWith.
type SwitchOptions struct {
	// HibernatePrevious hibernates the project that was active before,
	// unless work that hibernation would interrupt runs there (it then
	// stays in the background, see Info.PreviousBusy). The hibernation
	// event has the reason "switch".
	HibernatePrevious bool
}

// Event is the payload of the lifecycle topics.
type Event struct {
	Name  string `json:"name"`
	Root  string `json:"root"`
	State State  `json:"state"`
	// Previous is the state before the change (empty for opened).
	Previous State `json:"previous,omitempty"`
	// Reason is "auto" for an automatic hibernation, "switch" for one on
	// switching away (SwitchOptions), "forced" when running work was
	// stopped.
	Reason string `json:"reason,omitempty"`
}

// Lifecycle topics. They live in the "projects" namespace: the kernel's
// "project.opened"/"project.closed" describe the project of one api.App,
// and modules react to them.
const (
	TopicOpened       = bus.Topic[Event]("projects.opened")
	TopicActivated    = bus.Topic[Event]("projects.activated")
	TopicBackgrounded = bus.Topic[Event]("projects.backgrounded")
	TopicHibernated   = bus.Topic[Event]("projects.hibernated")
	TopicResumed      = bus.Topic[Event]("projects.resumed")
	TopicClosed       = bus.Topic[Event]("projects.closed")
)

// Clock abstracts time for the auto-hibernation timers.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine after d.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a stoppable timer of a Clock.
type Timer interface {
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// Config configures a Manager.
type Config struct {
	// Open creates projects (required).
	Open Opener
	// Bus receives the lifecycle events (nil: none).
	Bus *bus.Bus
	// Clock drives auto-hibernation (nil: real time).
	Clock Clock
	// AutoHibernate hibernates background projects after this time
	// (0: off). See SetAutoHibernate.
	AutoHibernate time.Duration
	// OnChange is called after every state change (inside the operation,
	// before its events are delivered).
	OnChange func()
	// OnError receives failures of automatic hibernation.
	OnError func(root string, err error)
}

// HibernateOptions are the options of Hibernate.
type HibernateOptions struct {
	// Force hibernates despite running work and unsaved documents.
	Force bool
	// ClientState is stored and handed back on resume.
	ClientState json.RawMessage
}

type session struct {
	root    string
	proj    Project
	state   State
	since   time.Time
	docs    []string
	client  json.RawMessage
	timer   Timer
	timerAt time.Time
	gen     uint64 // bumped on every state change (stale timers)
}

// Manager holds the open projects of one window. It is safe for
// concurrent use; lifecycle operations run one at a time.
type Manager struct {
	cfg   Config
	clock Clock

	op sync.Mutex // serialises lifecycle operations

	mu       sync.Mutex // guards the fields below
	sessions map[string]*session
	order    []string // roots in opening order
	active   string
	auto     time.Duration
	closed   bool
}

// New returns a Manager.
func New(cfg Config) (*Manager, error) {
	if cfg.Open == nil {
		return nil, errors.New("workspace: Config.Open is required")
	}
	c := cfg.Clock
	if c == nil {
		c = realClock{}
	}
	return &Manager{cfg: cfg, clock: c, sessions: map[string]*session{}, auto: max(cfg.AutoHibernate, 0)}, nil
}

// NameOf is the display name of a project root.
func NameOf(root string) string { return filepath.Base(root) }

// List returns the sessions in opening order.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, r := range m.order {
		out = append(out, m.infoLocked(m.sessions[r]))
	}
	return out
}

// Active returns the active session, if any.
func (m *Manager) Active() (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[m.active]
	if s == nil {
		return Info{}, false
	}
	return m.infoLocked(s), true
}

// Get returns the session matching name (root or base name).
func (m *Manager) Get(name string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.findLocked(name)
	if err != nil {
		return Info{}, err
	}
	return m.infoLocked(s), nil
}

// Project returns the Project of the session matching name.
func (m *Manager) Project(name string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.findLocked(name)
	if err != nil {
		return nil, err
	}
	return s.proj, nil
}

// Busy reports what blocks hibernating or closing the session.
func (m *Manager) Busy(ctx context.Context, name string) ([]string, error) {
	p, err := m.Project(name)
	if err != nil {
		return nil, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	return p.Busy(ctx), nil
}

// HibernateBusy reports what blocks hibernating the session (less than
// Busy for projects implementing HibernateBlocker).
func (m *Manager) HibernateBusy(ctx context.Context, name string) ([]string, error) {
	p, err := m.Project(name)
	if err != nil {
		return nil, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	return hibernateBusy(ctx, p), nil
}

func (m *Manager) infoLocked(s *session) Info {
	i := Info{Name: NameOf(s.root), Root: s.root, State: s.state, Since: s.since}
	if s.state == StateHibernated {
		i.Docs = append([]string(nil), s.docs...)
	}
	if s.timer != nil {
		at := s.timerAt
		i.AutoHibernateAt = &at
	}
	return i
}

// findLocked resolves name: an exact root, else a unique base name.
func (m *Manager) findLocked(name string) (*session, error) {
	if s := m.sessions[name]; s != nil {
		return s, nil
	}
	if s := m.sessions[filepath.Clean(name)]; s != nil && name != "" {
		return s, nil
	}
	var hits []*session
	for _, r := range m.order {
		if NameOf(r) == name {
			hits = append(hits, m.sessions[r])
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	case 1:
		return hits[0], nil
	}
	roots := make([]string, len(hits))
	for i, h := range hits {
		roots[i] = h.root
	}
	sort.Strings(roots)
	return nil, fmt.Errorf("%w: %s matches %s", ErrAmbiguous, name, strings.Join(roots, ", "))
}

// change records a state change of s and returns the event (m.mu held).
func (m *Manager) changeLocked(s *session, to State, reason string) Event {
	ev := Event{Name: NameOf(s.root), Root: s.root, State: to, Previous: s.state, Reason: reason}
	s.state, s.since = to, m.clock.Now()
	s.gen++
	m.stopTimerLocked(s)
	if to == StateBackground {
		m.armLocked(s)
	}
	return ev
}

func (m *Manager) stopTimerLocked(s *session) {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// armLocked starts the auto-hibernation timer of a background session.
func (m *Manager) armLocked(s *session) {
	if m.auto <= 0 || m.closed {
		return
	}
	gen, d := s.gen, m.auto
	s.timerAt = m.clock.Now().Add(d)
	s.timer = m.clock.AfterFunc(d, func() { m.autoHibernate(s, gen) })
}

// publish delivers the events and calls OnChange (op held, mu not).
func (m *Manager) publish(evs ...Event) {
	if m.cfg.OnChange != nil {
		m.cfg.OnChange()
	}
	if m.cfg.Bus == nil {
		return
	}
	for _, ev := range evs {
		topic := TopicActivated
		switch {
		case ev.Previous == "" && ev.State != StateActive:
			topic = TopicOpened
		case ev.State == StateBackground && ev.Previous == StateHibernated:
			topic = TopicResumed
		case ev.State == StateActive:
			topic = TopicActivated
		case ev.State == StateBackground:
			topic = TopicBackgrounded
		case ev.State == StateHibernated:
			topic = TopicHibernated
		case ev.State == "":
			topic = TopicClosed
		}
		topic.Publish(m.cfg.Bus, ev)
	}
}

func (m *Manager) checkOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

// Open opens the project at root (absolute) and activates it. An already
// open project is activated instead (and resumed if hibernated).
func (m *Manager) Open(ctx context.Context, root string) (Info, error) {
	return m.open(ctx, root, nil, true, SwitchOptions{})
}

// OpenWith is Open with options for the previously active project.
func (m *Manager) OpenWith(ctx context.Context, root string, opts SwitchOptions) (Info, error) {
	return m.open(ctx, root, nil, true, opts)
}

// Add registers an already opened project (e.g. one the host opened
// before the Manager existed) and activates it if activate is set,
// otherwise it starts in the background.
func (m *Manager) Add(ctx context.Context, root string, p Project, activate bool) (Info, error) {
	if p == nil {
		return Info{}, errors.New("workspace: nil project")
	}
	return m.open(ctx, root, p, activate, SwitchOptions{})
}

func (m *Manager) open(ctx context.Context, root string, p Project, activate bool, opts SwitchOptions) (Info, error) {
	if root == "" || !filepath.IsAbs(root) {
		return Info{}, fmt.Errorf("workspace: project root must be absolute: %q", root)
	}
	root = filepath.Clean(root)
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	_, exists := m.sessions[root]
	m.mu.Unlock()
	if exists {
		if !activate {
			return m.Get(root)
		}
		return m.switchLocked(ctx, root, opts)
	}
	if p == nil {
		var err error
		if p, err = m.cfg.Open(ctx, root); err != nil {
			return Info{}, fmt.Errorf("workspace: open %s: %w", root, err)
		}
	}
	m.mu.Lock()
	s := &session{root: root, proj: p}
	m.sessions[root] = s
	m.order = append(m.order, root)
	evs := []Event{m.changeLocked(s, StateBackground, "")}
	evs[0].Previous = ""
	var prev *session
	if activate {
		prev = m.previousLocked(s)
		evs = append(evs, m.activateLocked(s)...)
	}
	info := m.infoLocked(s)
	m.mu.Unlock()
	m.publish(evs...)
	m.leavePrevious(ctx, prev, opts, &info)
	return info, nil
}

// previousLocked returns the active session other than s (nil: none).
func (m *Manager) previousLocked(s *session) *session {
	if prev := m.sessions[m.active]; prev != nil && prev != s && prev.state == StateActive {
		return prev
	}
	return nil
}

// leavePrevious hibernates prev, the project active before a switch,
// when opts ask for it and nothing blocks it, and records the outcome in
// info (op held).
func (m *Manager) leavePrevious(ctx context.Context, prev *session, opts SwitchOptions, info *Info) {
	if prev == nil || !opts.HibernatePrevious {
		return
	}
	info.Previous, info.PreviousState = prev.root, StateBackground
	_, err := m.hibernateOp(ctx, prev, HibernateOptions{}, "switch")
	var be *BusyError
	switch {
	case err == nil:
		info.PreviousState = StateHibernated
	case errors.As(err, &be):
		info.PreviousBusy = be.Reasons
	case errors.Is(err, ErrUnsaved):
		info.PreviousBusy = []string{"unsaved changes"}
	default:
		info.PreviousBusy = []string{err.Error()}
	}
}

// activateLocked makes s the active session; the previous one goes to
// the background (m.mu held).
func (m *Manager) activateLocked(s *session) []Event {
	var evs []Event
	if prev := m.sessions[m.active]; prev != nil && prev != s && prev.state == StateActive {
		evs = append(evs, m.changeLocked(prev, StateBackground, ""))
	}
	m.active = s.root
	if s.state != StateActive {
		evs = append(evs, m.changeLocked(s, StateActive, ""))
	}
	return evs
}

// Switch activates the session matching name, resuming it if hibernated.
// The resume result carries the stored ClientState.
func (m *Manager) Switch(ctx context.Context, name string) (Info, error) {
	return m.SwitchWith(ctx, name, SwitchOptions{})
}

// SwitchWith is Switch with options for the previously active project.
func (m *Manager) SwitchWith(ctx context.Context, name string, opts SwitchOptions) (Info, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return Info{}, err
	}
	return m.switchLocked(ctx, name, opts)
}

func (m *Manager) switchLocked(ctx context.Context, name string, opts SwitchOptions) (Info, error) {
	m.mu.Lock()
	s, err := m.findLocked(name)
	m.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	var evs []Event
	var client json.RawMessage
	if s.state == StateHibernated {
		ev, c, err := m.resumeOp(ctx, s)
		if err != nil {
			return Info{}, err
		}
		evs, client = append(evs, ev), c
	}
	m.mu.Lock()
	prev := m.previousLocked(s)
	evs = append(evs, m.activateLocked(s)...)
	info := m.infoLocked(s)
	m.mu.Unlock()
	info.ClientState = client
	m.publish(evs...)
	m.leavePrevious(ctx, prev, opts, &info)
	return info, nil
}

// resumeOp resumes a hibernated session into the background (op held).
func (m *Manager) resumeOp(ctx context.Context, s *session) (Event, json.RawMessage, error) {
	m.mu.Lock()
	docs := s.docs
	m.mu.Unlock()
	if err := s.proj.Resume(ctx, docs); err != nil {
		return Event{}, nil, fmt.Errorf("workspace: resume %s: %w", s.root, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	client := s.client
	s.docs, s.client = nil, nil
	return m.changeLocked(s, StateBackground, ""), client, nil
}

// Resume restores a hibernated session into the background (or makes it
// active when no project is active). Resuming a running session does
// nothing.
func (m *Manager) Resume(ctx context.Context, name string) (Info, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	s, err := m.findLocked(name)
	m.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	if s.state != StateHibernated {
		return m.Get(s.root)
	}
	ev, client, err := m.resumeOp(ctx, s)
	if err != nil {
		return Info{}, err
	}
	evs := []Event{ev}
	m.mu.Lock()
	if m.sessions[m.active] == nil {
		evs = append(evs, m.activateLocked(s)...)
	}
	info := m.infoLocked(s)
	m.mu.Unlock()
	info.ClientState = client
	m.publish(evs...)
	return info, nil
}

// Hibernate releases the resources of the session matching name. The
// active session may be hibernated too; afterwards no project is active.
func (m *Manager) Hibernate(ctx context.Context, name string, opts HibernateOptions) (Info, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	s, err := m.findLocked(name)
	m.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	return m.hibernateOp(ctx, s, opts, "")
}

// AttachClientState stores client state (handed back by the resume) for
// the session matching name only while it is hibernated, e.g. after an
// automatic hibernation the client did not ask for. It reports whether
// the state was stored; a session resumed meanwhile is left alone (unlike
// Hibernate, which would hibernate it again).
func (m *Manager) AttachClientState(name string, state json.RawMessage) (bool, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.findLocked(name)
	if err != nil {
		return false, err
	}
	if s.state != StateHibernated {
		return false, nil
	}
	s.client = state
	return true, nil
}

func (m *Manager) hibernateOp(ctx context.Context, s *session, opts HibernateOptions, reason string) (Info, error) {
	m.mu.Lock()
	state := s.state
	m.mu.Unlock()
	if state == StateHibernated {
		if opts.ClientState != nil {
			m.mu.Lock()
			s.client = opts.ClientState
			m.mu.Unlock()
		}
		return m.Get(s.root)
	}
	if busy := hibernateBusy(ctx, s.proj); len(busy) > 0 {
		if !opts.Force {
			return Info{}, &BusyError{Name: NameOf(s.root), Reasons: busy}
		}
		if reason == "" {
			reason = "forced"
		}
	}
	docs, err := s.proj.Hibernate(ctx, opts.Force)
	if err != nil {
		return Info{}, fmt.Errorf("workspace: hibernate %s: %w", s.root, err)
	}
	m.mu.Lock()
	s.docs, s.client = docs, opts.ClientState
	if m.active == s.root {
		m.active = ""
	}
	ev := m.changeLocked(s, StateHibernated, reason)
	info := m.infoLocked(s)
	m.mu.Unlock()
	m.publish(ev)
	return info, nil
}

// autoHibernate is the timer callback of a background session.
func (m *Manager) autoHibernate(s *session, gen uint64) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	stale := m.closed || s.gen != gen || s.state != StateBackground || m.sessions[s.root] != s
	if !stale {
		s.timer = nil
	}
	m.mu.Unlock()
	if stale {
		return
	}
	_, err := m.hibernateOp(context.Background(), s, HibernateOptions{}, "auto")
	if err == nil {
		return
	}
	if m.cfg.OnError != nil {
		m.cfg.OnError(s.root, err)
	}
	// Busy or unsaved: try again after another interval.
	m.mu.Lock()
	if s.gen == gen && s.state == StateBackground && s.timer == nil {
		m.armLocked(s)
	}
	m.mu.Unlock()
}

// Close closes the session matching name for good.
func (m *Manager) Close(ctx context.Context, name string, force bool) error {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.checkOpen(); err != nil {
		return err
	}
	m.mu.Lock()
	s, err := m.findLocked(name)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return m.closeOp(ctx, s, force)
}

func (m *Manager) closeOp(ctx context.Context, s *session, force bool) error {
	m.mu.Lock()
	hibernated := s.state == StateHibernated
	m.mu.Unlock()
	reason := ""
	if !hibernated {
		if busy := s.proj.Busy(ctx); len(busy) > 0 {
			if !force {
				return &BusyError{Name: NameOf(s.root), Reasons: busy}
			}
			reason = "forced"
		}
	}
	if err := s.proj.Close(ctx, force); err != nil {
		return fmt.Errorf("workspace: close %s: %w", s.root, err)
	}
	m.mu.Lock()
	ev := m.changeLocked(s, "", reason)
	delete(m.sessions, s.root)
	for i, r := range m.order {
		if r == s.root {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	if m.active == s.root {
		m.active = ""
	}
	m.mu.Unlock()
	m.publish(ev)
	return nil
}

// SetAutoHibernate changes the auto-hibernation delay (0: off); the
// timers of background sessions restart with it.
func (m *Manager) SetAutoHibernate(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d = max(d, 0)
	if d == m.auto {
		return
	}
	m.auto = d
	for _, s := range m.sessions {
		if s.state != StateBackground {
			continue
		}
		m.stopTimerLocked(s)
		s.gen++
		m.armLocked(s)
	}
}

// AutoHibernate returns the auto-hibernation delay (0: off).
func (m *Manager) AutoHibernate() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auto
}

// CloseAll closes every session with force and rejects further
// operations. The errors of single projects are joined.
func (m *Manager) CloseAll(ctx context.Context) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	roots := append([]string(nil), m.order...)
	for _, s := range m.sessions {
		m.stopTimerLocked(s)
	}
	m.mu.Unlock()
	var errs []error
	for i := len(roots) - 1; i >= 0; i-- {
		m.mu.Lock()
		s := m.sessions[roots[i]]
		m.mu.Unlock()
		if err := m.closeOp(ctx, s, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
