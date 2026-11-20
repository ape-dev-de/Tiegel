package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// Several projects per window (AP18.5, D468, D477). The window's App
// hosts an api.Workspace; every project of it lives in its own api.App
// and gets its own controller (on controller.NewLocalInProcess, so calls
// never wander to another project) and its own View. A View keeps the
// whole per-project GUI state – tabs and editors, tool layout, tree
// expansion, docks, terminals – so switching only changes which View the
// window draws. Background projects keep running; hibernated ones have
// no App, controller or View: their tabs and carets are handed to the
// workspace as clientState and restored on resume.
//
// The workspace is the source of truth: Sessions follows projects.*
// events by comparing projects.list with its slots (one slot per App) on
// the next frame. All lifecycle calls (projects.open/switch/hibernate/
// resume/close) run off the UI goroutine.

// Sessions is the set of project views of one window. All methods except
// Post run on the window's UI goroutine.
type Sessions struct {
	ws     *api.Workspace
	host   *api.App
	goos   string
	shaper *text.Shaper
	// setup wires a new view into the window (invalidate, window host,
	// quit, raise).
	setup      func(v *View)
	invalidate func()
	ctx        context.Context
	cancel     context.CancelFunc
	unsub      func()

	postMu sync.Mutex
	posted []func()
	dirty  atomic.Bool
	// ops counts running lifecycle calls (tests wait for them).
	ops atomic.Int32

	slots    map[*api.App]*sessionSlot
	hostSlot *sessionSlot
	shown    *sessionSlot
	list     api.ProjectsListResult
	seq      uint64
	// restore is client state returned by a resume, by project root,
	// until the project's slot exists.
	restore map[string]json.RawMessage
	// stored marks roots whose client state went with the hibernate call.
	stored map[string]bool
	// switching is set while a fallback switch (no active project) runs.
	switching bool
}

// sessionSlot is one project App of the window with its controller and
// view. The host App's slot is permanent and shows the welcome screen
// while the host has no project.
type sessionSlot struct {
	app  *api.App
	be   *controller.InProcess
	ctl  *controller.Controller
	view *View
	// root is the project the slot shows ("" none).
	root string
	// restore is client state to apply once the project is shown.
	restore json.RawMessage
	// used orders the slots by the last time they were shown.
	used uint64
}

// newSessions builds the window's sessions on the workspace ws of host.
// setup is called for every new view.
func newSessions(host *api.App, ws *api.Workspace, goos string, invalidate func(), setup func(v *View)) *Sessions {
	ctx, cancel := context.WithCancel(context.Background())
	if invalidate == nil {
		invalidate = func() {}
	}
	if setup == nil {
		setup = func(*View) {}
	}
	s := &Sessions{
		ws: ws, host: host, goos: goos, shaper: newShaper(), setup: setup, invalidate: invalidate,
		ctx: ctx, cancel: cancel, slots: map[*api.App]*sessionSlot{},
		restore: map[string]json.RawMessage{}, stored: map[string]bool{}, unsub: func() {},
	}
	s.hostSlot = s.newSlot(host)
	s.shown = s.hostSlot
	if un, err := s.hostSlot.be.Subscribe(ctx, []string{"projects.*"}, func(api.Event) {
		s.dirty.Store(true)
		s.invalidate()
	}); err == nil {
		s.unsub = un
	}
	s.dirty.Store(true)
	return s
}

// Start loads the host view and opens project (if not empty and not
// already the host's project) through the workspace.
func (s *Sessions) Start(project string) {
	s.hostSlot.ctl.Start("")
	if project != "" && s.host.ProjectRoot() == "" {
		s.Open(project)
	}
}

// newSlot creates the controller and view of app.
func (s *Sessions) newSlot(app *api.App) *sessionSlot {
	be := controller.NewLocalInProcess(app)
	ctl := controller.New(be)
	v := newView(ctl, s.goos, s.shaper)
	v.sessions = s
	v.hdr.Sessions = s
	s.setup(v)
	sl := &sessionSlot{app: app, be: be, ctl: ctl, view: v}
	s.slots[app] = sl
	return sl
}

// dropSlot closes the view and controller of a slot whose App is gone.
func (s *Sessions) dropSlot(sl *sessionSlot) {
	delete(s.slots, sl.app)
	sl.view.Close()
	sl.ctl.Close()
	_ = sl.be.Close()
	if s.shown == sl {
		s.shown = s.hostSlot
	}
}

// Shutdown releases all views and controllers (the window goes away; the
// host App closes the workspace).
func (s *Sessions) Shutdown() {
	s.unsub()
	s.cancel()
	for _, sl := range s.slots {
		sl.view.Close()
		sl.ctl.Close()
		_ = sl.be.Close()
	}
	s.slots = map[*api.App]*sessionSlot{}
}

// Controllers returns the controllers of all slots (saving on close).
func (s *Sessions) Controllers() []*controller.Controller {
	out := make([]*controller.Controller, 0, len(s.slots))
	for _, sl := range s.slots {
		out = append(out, sl.ctl)
	}
	return out
}

// Post runs fn on the UI goroutine before the next frame (any
// goroutine).
func (s *Sessions) Post(fn func()) {
	s.postMu.Lock()
	s.posted = append(s.posted, fn)
	s.postMu.Unlock()
	s.invalidate()
}

// Current applies pending results and workspace changes and returns the
// view to draw.
func (s *Sessions) Current() *View {
	s.postMu.Lock()
	q := s.posted
	s.posted = nil
	s.postMu.Unlock()
	for _, fn := range q {
		fn()
	}
	if s.dirty.Swap(false) {
		s.sync()
	}
	for _, sl := range s.slots {
		if sl != s.shown {
			// Background projects keep running: their results and events
			// are applied (bounded queues, current state on switching).
			sl.ctl.Drain()
		}
		if sl.restore != nil && sl.ctl.Project.Open && sl.ctl.Project.Root == sl.root {
			sl.view.restoreClientState(sl.restore)
			sl.restore = nil
		}
	}
	return s.shown.view
}

// sync follows the workspace: slots for new or resumed projects, slots
// of hibernated or closed projects released, the active project shown.
func (s *Sessions) sync() {
	list := s.ws.List()
	s.list = list
	seen := map[*api.App]bool{}
	for _, p := range list.Projects {
		if p.State == workspace.StateHibernated {
			continue
		}
		app, err := s.ws.App(p.Root)
		if err != nil || app == nil {
			continue
		}
		seen[app] = true
		sl := s.slots[app]
		switch {
		case sl == nil:
			sl = s.newSlot(app)
			sl.root = p.Root
			sl.ctl.Start("")
		case sl.root != p.Root:
			sl.root = p.Root
			sl.ctl.AdoptProject()
		}
		if cs, ok := s.restore[p.Root]; ok {
			sl.restore = cs
			delete(s.restore, p.Root)
		}
	}
	for app, sl := range s.slots {
		if seen[app] || sl.root == "" {
			continue
		}
		s.release(sl)
	}
	target := s.slotOf(list.Active)
	if target == nil {
		if next := s.fallback(""); next != nil {
			target = next
			s.switchFallback(next.root)
		}
	}
	if target == nil {
		target = s.hostSlot
	}
	if target != s.shown {
		if prev := s.shown; prev != nil && prev.ctl.Project.Open {
			prev.ctl.Autosave("focusLost")
		}
		s.shown = target
		if list.Active != "" && target.root == list.Active {
			target.ctl.Activated()
		}
	}
	s.seq++
	target.used = s.seq
}

// release ends the slot of a project that was hibernated or closed. A
// hibernation the GUI did not ask for (auto-hibernation) gets the tabs
// and carets as client state afterwards.
func (s *Sessions) release(sl *sessionSlot) {
	root := sl.root
	if s.stateOf(root) == workspace.StateHibernated && !s.stored[root] {
		cs := sl.view.clientState()
		s.background(func(ctx context.Context) func() {
			_, err := s.ws.Hibernate(ctx, api.ProjectsHibernateParams{Name: root, ClientState: cs})
			return func() { s.notice(err) }
		})
	}
	delete(s.stored, root)
	if sl == s.hostSlot {
		sl.ctl.ProjectReleased()
		sl.root = ""
		return
	}
	s.dropSlot(sl)
}

// switchFallback activates root when no project is active any more (the
// active one was hibernated or closed by someone else).
func (s *Sessions) switchFallback(root string) {
	if s.switching {
		return
	}
	s.switching = true
	s.background(func(ctx context.Context) func() {
		_, err := s.ws.Switch(ctx, root)
		return func() {
			s.switching = false
			s.notice(err)
		}
	})
}

// slotOf returns the slot showing root, or nil.
func (s *Sessions) slotOf(root string) *sessionSlot {
	if root == "" {
		return nil
	}
	for _, sl := range s.slots {
		if sl.root == root {
			return sl
		}
	}
	return nil
}

// fallback returns the most recently shown running project other than
// except (nil: none).
func (s *Sessions) fallback(except string) *sessionSlot {
	var best *sessionSlot
	for _, sl := range s.slots {
		if sl.root == "" || sl.root == except || s.stateOf(sl.root) == workspace.StateHibernated || s.stateOf(sl.root) == "" {
			continue
		}
		if best == nil || sl.used > best.used {
			best = sl
		}
	}
	return best
}

// stateOf returns the state of root in the last list ("" not open).
func (s *Sessions) stateOf(root string) workspace.State {
	for _, p := range s.list.Projects {
		if p.Root == root {
			return p.State
		}
	}
	return ""
}

// Has reports whether root is open in the window (any state).
func (s *Sessions) Has(root string) bool { return s.stateOf(root) != "" }

// Roots returns the roots of the window's open projects.
func (s *Sessions) Roots() []string {
	out := make([]string, 0, len(s.list.Projects))
	for _, p := range s.list.Projects {
		out = append(out, p.Root)
	}
	return out
}

// background runs fn off the UI goroutine and its result on it.
func (s *Sessions) background(fn func(ctx context.Context) func()) {
	s.ops.Add(1)
	go func() {
		defer s.ops.Add(-1)
		done := fn(s.ctx)
		if done != nil {
			s.Post(done)
		}
	}()
}

// notice shows err in the status of the shown project.
func (s *Sessions) notice(err error) {
	if err != nil && s.shown != nil {
		s.shown.ctl.Status.Notice = err.Error()
	}
}

// remember keeps the client state a resume returned for the project's
// next slot.
func (s *Sessions) remember(info api.ProjectSession) {
	if len(info.ClientState) > 0 {
		s.restore[info.Root] = info.ClientState
		s.dirty.Store(true)
	}
}

// ---- lifecycle intents (header, actions, open dialogs) ---------------------

// Open opens the project directory next to the others and shows it
// (projects.open; an open project is switched to, a hibernated one
// resumed).
func (s *Sessions) Open(dir string) {
	s.shown.ctl.Status.Notice = ""
	s.background(func(ctx context.Context) func() {
		info, err := s.ws.Open(ctx, dir)
		return func() {
			s.notice(err)
			s.remember(info)
		}
	})
}

// Switch shows project id (projects.switch; resumes a hibernated one).
func (s *Sessions) Switch(id string) {
	if id == s.list.Active {
		return
	}
	s.background(func(ctx context.Context) func() {
		info, err := s.ws.Switch(ctx, id)
		return func() {
			s.notice(err)
			s.remember(info)
		}
	})
}

// Resume restores a hibernated project in the background.
func (s *Sessions) Resume(id string) {
	s.background(func(ctx context.Context) func() {
		info, err := s.ws.Resume(ctx, id)
		return func() {
			s.notice(err)
			s.remember(info)
		}
	})
}

// Hibernate releases project id after asking when running work blocks it
// (the blockers are listed, Force stops them).
func (s *Sessions) Hibernate(id string) {
	s.confirmBusy(id, "Hibernate", func(force bool) { s.hibernate(id, force) })
}

// Close closes project id for good after asking when running work blocks
// it. Unsaved changes that cannot be saved are kept as recovery points,
// like File › Close Project.
func (s *Sessions) Close(id string) {
	s.confirmBusy(id, "Close", func(bool) { s.close(id) })
}

// confirmBusy asks projects.busy and runs do directly when nothing
// blocks, else after the user confirmed stopping the listed work.
func (s *Sessions) confirmBusy(id, verb string, do func(force bool)) {
	s.background(func(ctx context.Context) func() {
		busy, err := s.ws.Busy(ctx, id)
		return func() {
			if err != nil {
				s.notice(err)
				return
			}
			if len(busy) == 0 {
				do(false)
				return
			}
			s.shown.view.OpenPopup(&Popup{
				Title:    fmt.Sprintf("%s “%s”?", verb, projectName(id)),
				Detail:   "Still running:\n• " + strings.Join(busy, "\n• ") + "\n\n" + verb + " anyway stops it.",
				Centered: true,
				Buttons: []*PopupButton{
					{Text: "Stop and " + verb, Kind: atoms.Primary, OnClick: func() { do(true) }},
					{Text: "Cancel", Kind: atoms.Secondary},
				},
			})
		}
	})
}

// leave returns the project to show instead of root when root goes away
// while it is shown ("" none).
func (s *Sessions) leave(root string) string {
	if s.list.Active != root {
		return ""
	}
	if next := s.fallback(root); next != nil {
		return next.root
	}
	return ""
}

// hibernate stores the view state with the hibernation; a shown project
// is replaced by the last other running one first.
func (s *Sessions) hibernate(root string, force bool) {
	var cs json.RawMessage
	if sl := s.slotOf(root); sl != nil {
		cs = sl.view.clientState()
		sl.ctl.Autosave("focusLost")
	}
	s.stored[root] = true
	next := s.leave(root)
	s.background(func(ctx context.Context) func() {
		if next != "" {
			if _, err := s.ws.Switch(ctx, next); err != nil {
				return func() { s.notice(err) }
			}
		}
		_, err := s.ws.Hibernate(ctx, api.ProjectsHibernateParams{Name: root, Force: force, ClientState: cs})
		return func() {
			if err == nil {
				return
			}
			delete(s.stored, root)
			if api.ErrorCode(err) == api.CodeConflict && !force {
				s.shown.view.OpenPopup(&Popup{
					Title: fmt.Sprintf("Hibernate “%s”?", projectName(root)), Detail: err.Error() + "\n\nForce keeps unsaved changes as recovery points.",
					Centered: true,
					Buttons: []*PopupButton{
						{Text: "Force Hibernate", Kind: atoms.Primary, OnClick: func() { s.hibernate(root, true) }},
						{Text: "Cancel", Kind: atoms.Secondary},
					},
				})
				return
			}
			s.notice(err)
		}
	})
}

// close closes root (forced: running work was confirmed, unsaved changes
// become recovery points); a shown project is replaced first.
func (s *Sessions) close(root string) {
	if sl := s.slotOf(root); sl != nil {
		sl.ctl.Autosave("focusLost")
	}
	next := s.leave(root)
	s.background(func(ctx context.Context) func() {
		if next != "" {
			if _, err := s.ws.Switch(ctx, next); err != nil {
				return func() { s.notice(err) }
			}
		}
		err := s.ws.Close(ctx, root, true)
		return func() { s.notice(err) }
	})
}

// Cycle switches to the next (delta 1) or previous (-1) running project
// in the order they were opened.
func (s *Sessions) Cycle(delta int) {
	var roots []string
	cur := -1
	for _, p := range s.list.Projects {
		if p.State == workspace.StateHibernated {
			continue
		}
		if p.Root == s.list.Active {
			cur = len(roots)
		}
		roots = append(roots, p.Root)
	}
	if len(roots) < 2 {
		return
	}
	if cur < 0 {
		cur = 0
	}
	s.Switch(roots[((cur+delta)%len(roots)+len(roots))%len(roots)])
}

// Projects lists the open projects for the header and the switcher.
func (s *Sessions) Projects() ([]toolbar.Project, string) {
	out := make([]toolbar.Project, 0, len(s.list.Projects))
	for _, p := range s.list.Projects {
		st := toolbar.ProjectBackground
		switch p.State {
		case workspace.StateActive:
			st = toolbar.ProjectActive
		case workspace.StateHibernated:
			st = toolbar.ProjectHibernated
		}
		name := p.Name
		if name == "" {
			name = filepath.Base(p.Root)
		}
		out = append(out, toolbar.Project{ID: p.Root, Name: name, Path: homeShort(p.Root), State: st})
	}
	return out, s.list.Active
}

// projectName is the display name of a project root.
func projectName(root string) string { return filepath.Base(root) }

// ---- client state: tabs and carets ----------------------------------------------

// viewState is the client state stored with a hibernated project.
type viewState struct {
	Tabs   []tabState `json:"tabs,omitempty"`
	Active string     `json:"active,omitempty"`
}

type tabState struct {
	File  string `json:"file"`
	Caret int    `json:"caret,omitempty"`
}

// clientState captures the open file tabs (all groups, in order) and
// their carets.
func (v *View) clientState() json.RawMessage {
	var st viewState
	seen := map[string]bool{}
	for _, g := range v.ctl.Groups() {
		tabs, _ := v.ctl.GroupTabs(g)
		for _, t := range tabs {
			if t.Virtual != "" || seen[t.File] {
				continue
			}
			seen[t.File] = true
			ts := tabState{File: t.File}
			if es := v.editors[sessionKey{g, t.File}]; es != nil {
				es.doc.Lock()
				ts.Caret = es.ed.Cursor().Primary().Head
				es.doc.Unlock()
			}
			st.Tabs = append(st.Tabs, ts)
		}
	}
	if t := v.ctl.ActiveTab(); t != nil && t.Virtual == "" {
		st.Active = t.File
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	return raw
}

// restoreClientState reopens the tabs of a clientState (files that are
// gone are skipped) with their carets.
func (v *View) restoreClientState(raw json.RawMessage) {
	var st viewState
	if json.Unmarshal(raw, &st) != nil || len(st.Tabs) == 0 {
		return
	}
	if v.restoreCarets == nil {
		v.restoreCarets = map[string]int{}
	}
	var files []string
	caret := 0
	for _, t := range st.Tabs {
		if fi, err := os.Stat(t.File); err != nil || fi.IsDir() {
			continue
		}
		files = append(files, t.File)
		v.restoreCarets[t.File] = t.Caret
		if t.File == st.Active {
			caret = t.Caret
		}
	}
	v.ctl.RestoreTabs(files, st.Active, caret)
}
