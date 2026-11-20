package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// Several projects per window (AP18.5, D468). A Workspace is hosted by one
// App (the window's or the serve process's) and keeps one api.App per open
// project: the host itself for the first project, further Apps from
// WorkspaceOptions.NewApp. Exactly one project is active; background
// projects keep everything running, hibernated ones have their App closed
// (language server, watcher, index, runs, documents released).
//
// For clients of the host (serve connections, InProcess on the host) the
// workspace is transparent: methods outside projects.*, events.* and app.*
// are forwarded to the active project's App, and events reach them from
// the host (projects.* and, while the host is active or no project is,
// everything) and from the active project's App. project.open and
// project.close keep their meaning "replace / close the project of this
// client": they close the client's project and open the new one in its
// place.

// KeyAutoHibernate is the core setting with the minutes after which a
// background project is hibernated (0: never).
const KeyAutoHibernate = settings.KeyAutoHibernate

// ProjectSession describes one open project of the workspace.
type ProjectSession = workspace.Info

// ProjectsListResult is the result of "projects.list" and "projects.close".
type ProjectsListResult struct {
	Projects []ProjectSession `json:"projects"`
	// Active is the root of the active project ("" if none).
	Active string `json:"active,omitempty"`
	// AutoHibernateMinutes is the auto-hibernation delay (0: off).
	AutoHibernateMinutes int `json:"autoHibernateMinutes"`
}

// ProjectsNameParams name a project: its root or, if unique, its
// directory name.
type ProjectsNameParams struct {
	Name string `json:"name"`
}

// ProjectsHibernateParams are the params of "projects.hibernate".
type ProjectsHibernateParams struct {
	Name string `json:"name"`
	// Force hibernates despite running processes or debug sessions (they
	// are stopped) and documents that cannot be saved (kept as recovery
	// points).
	Force bool `json:"force,omitempty"`
	// ClientState is opaque client state (open tabs, cursors, layout)
	// handed back by the projects.switch/resume call that resumes the
	// project.
	ClientState json.RawMessage `json:"clientState,omitempty"`
}

// ProjectsCloseParams are the params of "projects.close".
type ProjectsCloseParams struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

// ProjectsClientStateParams are the params of "projects.attachClientState".
type ProjectsClientStateParams struct {
	Name string `json:"name"`
	// ClientState is opaque client state as in ProjectsHibernateParams.
	ClientState json.RawMessage `json:"clientState"`
}

// ProjectsClientStateResult is the result of "projects.attachClientState".
type ProjectsClientStateResult struct {
	// Attached is false when the project is not hibernated (any more):
	// the state was dropped.
	Attached bool `json:"attached"`
}

// ProjectsBusyResult is the result of "projects.busy".
type ProjectsBusyResult struct {
	Busy []string `json:"busy"`
}

// WorkspaceOptions configure EnableWorkspace.
type WorkspaceOptions struct {
	// NewApp creates the App of a further project (nil: New with the
	// host's options and its global settings store).
	NewApp func() (*App, error)
	// Clock drives auto-hibernation (nil: real time; tests).
	Clock workspace.Clock
}

// Workspace is the set of open projects of one host App.
type Workspace struct {
	host   *App
	mgr    *workspace.Manager
	newApp func() (*App, error)
	relay  *bus.Bus // events delivered to the host's clients
	unsubs []func()

	active atomic.Pointer[appProject]

	mu       sync.Mutex
	hostUser *appProject // session that uses the host App
	relayed  *App        // App whose events are relayed besides the host
	unRelay  func()
	down     bool
	// exiting is set by shutdown: like App.Close, closing on exit keeps
	// unsaved changes as recovery points instead of saving them.
	exiting atomic.Bool
}

// Workspace returns the workspace the App hosts or belongs to, or nil.
func (a *App) Workspace() *Workspace { return a.ws.Load() }

// EnableWorkspace makes a the host of a workspace (once; later calls
// return the existing one). A project already open in a becomes the
// active project. Enable it before clients subscribe to events: earlier
// subscriptions only see the host's own events.
func (a *App) EnableWorkspace(opts WorkspaceOptions) (*Workspace, error) {
	if w := a.ws.Load(); w != nil {
		if w.host != a {
			return nil, Errorf(CodeFailed, "the App belongs to the workspace of another App")
		}
		return w, nil
	}
	w := &Workspace{host: a, newApp: opts.NewApp, relay: bus.New()}
	if w.newApp == nil {
		w.newApp = func() (*App, error) {
			o := a.opts
			o.GlobalSettings = a.global
			return New(o)
		}
	}
	mgr, err := workspace.New(workspace.Config{
		Open:          w.openProject,
		Bus:           a.bus,
		Clock:         opts.Clock,
		AutoHibernate: autoHibernate(a),
		OnChange:      w.refresh,
		OnError: func(root string, err error) {
			a.log.Info("auto-hibernate skipped", "root", root, "err", err)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("api: workspace: %w", err)
	}
	w.mgr = mgr
	if !a.ws.CompareAndSwap(nil, w) {
		return a.EnableWorkspace(opts)
	}
	w.unsubs = append(w.unsubs,
		a.bus.Subscribe("*", w.fromHost),
		settings.Changed.Subscribe(a.bus, func(c settings.Change) {
			if c.Key == KeyAutoHibernate {
				w.mgr.SetAutoHibernate(autoHibernate(a))
			}
		}))
	if root := a.ProjectRoot(); root != "" {
		p := &appProject{w: w, root: root, app: a}
		w.mu.Lock()
		w.hostUser = p
		w.mu.Unlock()
		if _, err := mgr.Add(context.Background(), root, p, true); err != nil {
			return nil, fmt.Errorf("api: workspace: %w", err)
		}
	}
	return w, nil
}

// autoHibernate reads the auto-hibernation delay from the settings.
func autoHibernate(a *App) time.Duration {
	return time.Duration(max(Setting(a, KeyAutoHibernate, 0), 0)) * time.Minute
}

// workspace returns the App's workspace, enabling one with default
// options on first use.
func (a *App) workspace() (*Workspace, error) {
	if w := a.ws.Load(); w != nil {
		return w, nil
	}
	return a.EnableWorkspace(WorkspaceOptions{})
}

// Manager returns the session manager (lifecycle in Go).
func (w *Workspace) Manager() *workspace.Manager { return w.mgr }

// Host returns the host App.
func (w *Workspace) Host() *App { return w.host }

// App returns the App of the project matching name (root or unique
// directory name); nil while it is hibernated. The GUI builds one
// controller per App.
func (w *Workspace) App(name string) (*App, error) {
	p, err := w.project(name)
	if err != nil {
		return nil, err
	}
	return p.current(), nil
}

// ActiveApp returns the App of the active project, or nil.
func (w *Workspace) ActiveApp() *App {
	if p := w.active.Load(); p != nil {
		return p.current()
	}
	return nil
}

func (w *Workspace) project(name string) (*appProject, error) {
	p, err := w.mgr.Project(name)
	if err != nil {
		return nil, apiError(err)
	}
	return p.(*appProject), nil
}

// List returns the open projects.
func (w *Workspace) List() ProjectsListResult {
	res := ProjectsListResult{Projects: w.mgr.List(), AutoHibernateMinutes: int(w.mgr.AutoHibernate() / time.Minute)}
	if act, ok := w.mgr.Active(); ok {
		res.Active = act.Root
	}
	return res
}

// Open opens the project directory path (or switches to it, if open) and
// makes it active.
func (w *Workspace) Open(ctx context.Context, path string) (ProjectSession, error) {
	root, err := resolveProjectDir(path)
	if err != nil {
		return ProjectSession{}, err
	}
	info, err := w.mgr.Open(ctx, root)
	return info, apiError(err)
}

// Switch activates a project (resuming it if hibernated).
func (w *Workspace) Switch(ctx context.Context, name string) (ProjectSession, error) {
	info, err := w.mgr.Switch(ctx, name)
	return info, apiError(err)
}

// Hibernate releases the resources of a project.
func (w *Workspace) Hibernate(ctx context.Context, p ProjectsHibernateParams) (ProjectSession, error) {
	info, err := w.mgr.Hibernate(ctx, p.Name, workspace.HibernateOptions{Force: p.Force, ClientState: p.ClientState})
	return info, apiError(err)
}

// AttachClientState stores client state for a hibernated project (handed
// back by the resume); a project that is not hibernated is left alone
// (attached false).
func (w *Workspace) AttachClientState(name string, state json.RawMessage) (bool, error) {
	ok, err := w.mgr.AttachClientState(name, state)
	return ok, apiError(err)
}

// Resume restores a hibernated project in the background (active if no
// project is).
func (w *Workspace) Resume(ctx context.Context, name string) (ProjectSession, error) {
	info, err := w.mgr.Resume(ctx, name)
	return info, apiError(err)
}

// Close closes a project for good.
func (w *Workspace) Close(ctx context.Context, name string, force bool) error {
	return apiError(w.mgr.Close(ctx, name, force))
}

// Busy reports what blocks hibernating or closing a project.
func (w *Workspace) Busy(ctx context.Context, name string) ([]string, error) {
	b, err := w.mgr.Busy(ctx, name)
	return b, apiError(err)
}

// apiError maps workspace errors to API codes.
func apiError(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return Errorf(CodeNotFound, "%v", err)
	case errors.Is(err, workspace.ErrAmbiguous):
		return Errorf(CodeInvalidParams, "%v", err)
	case errors.Is(err, workspace.ErrBusy), errors.Is(err, workspace.ErrUnsaved):
		var be *workspace.BusyError
		if errors.As(err, &be) {
			return Errorf(CodeConflict, "%v", err).WithData(map[string]any{"busy": be.Reasons})
		}
		return Errorf(CodeConflict, "%v", err)
	case errors.Is(err, workspace.ErrClosed):
		return Errorf(CodeUnavailable, "%v", err)
	case errors.As(err, &e):
		return &Error{Code: e.Code, Message: err.Error(), Data: e.Data, Err: err}
	}
	return Errorf(CodeFailed, "%v", err)
}

// ---- routing ---------------------------------------------------------------

// forwardTarget returns the App a call to method on a is forwarded to: the
// active project's App when a hosts a workspace and is not active itself.
// Calls from inside a handler of a (ctx marked by callLocal/Call) stay on
// a: they belong to a's project, not to the active one.
func (a *App) forwardTarget(ctx context.Context, method string) *App {
	w := a.ws.Load()
	if w == nil || w.host != a || hostMethod(method) || a.handling(ctx) {
		return nil
	}
	t := w.ActiveApp()
	if t == nil || t == a {
		return nil
	}
	return t
}

// hostMethod reports methods the host always answers itself.
func hostMethod(method string) bool {
	for _, p := range []string{"projects.", "events.", "app."} {
		if strings.HasPrefix(method, p) {
			return true
		}
	}
	return false
}

// eventBus is the bus the App's clients (sessions, InProcess) subscribe
// to: the workspace relay on a host, else the App's bus.
func (a *App) eventBus() *bus.Bus {
	if w := a.ws.Load(); w != nil && w.host == a {
		return w.relay
	}
	return a.bus
}

// activeRoot is the root of the project a client of a sees.
func (a *App) activeRoot() string {
	if t := a.forwardTarget(context.Background(), "project.info"); t != nil {
		return t.ProjectRoot()
	}
	return a.ProjectRoot()
}

// fromHost relays the host's events to its clients: workspace events
// always, the rest while the host is the active project or none is.
func (w *Workspace) fromHost(e bus.Event) {
	if strings.HasPrefix(e.Topic, "projects.") {
		w.relay.Publish(e.Topic, e.Payload)
		return
	}
	if t := w.ActiveApp(); t == nil || t == w.host {
		w.relay.Publish(e.Topic, e.Payload)
	}
}

// refresh follows the active project (manager OnChange): forwarding and
// the relay of its App's events.
func (w *Workspace) refresh() {
	var act *appProject
	if info, ok := w.mgr.Active(); ok {
		if p, err := w.mgr.Project(info.Root); err == nil {
			act = p.(*appProject)
		}
	}
	w.active.Store(act)
	var app *App
	if act != nil {
		app = act.current()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if app == w.relayed || w.down {
		return
	}
	if w.unRelay != nil {
		w.unRelay()
		w.unRelay = nil
	}
	w.relayed = nil
	if app != nil && app != w.host {
		w.relayed = app
		w.unRelay = app.bus.Subscribe("*", func(e bus.Event) {
			if w.ActiveApp() == app {
				w.relay.Publish(e.Topic, e.Payload)
			}
		})
	}
}

// shutdown closes all projects (host Close).
func (w *Workspace) shutdown() error {
	w.exiting.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := w.mgr.CloseAll(ctx)
	w.mu.Lock()
	w.down = true
	if w.unRelay != nil {
		w.unRelay()
		w.unRelay = nil
	}
	w.mu.Unlock()
	for _, un := range w.unsubs {
		un()
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	if cerr := w.relay.Close(cctx); cerr != nil {
		err = errors.Join(err, cerr)
	}
	return err
}

// ---- legacy project.open / project.close -----------------------------------

// sessionOf returns the session whose App is a.
func (w *Workspace) sessionOf(a *App) *appProject {
	for _, i := range w.mgr.List() {
		if p, err := w.mgr.Project(i.Root); err == nil && p.(*appProject).current() == a {
			return p.(*appProject)
		}
	}
	return nil
}

// legacyOpen implements project.open on a: the project of a is replaced
// (closed with its unsaved changes kept as recovery points) by path,
// which becomes active.
func (w *Workspace) legacyOpen(ctx context.Context, a *App, path string) (ProjectInfo, error) {
	root, err := resolveProjectDir(path)
	if err != nil {
		return ProjectInfo{}, err
	}
	if cur := w.sessionOf(a); cur != nil && cur.root != root {
		if err := w.closePreserving(ctx, cur); err != nil {
			return ProjectInfo{}, apiError(err)
		}
	}
	if _, err := w.mgr.Open(ctx, root); err != nil {
		return ProjectInfo{}, apiError(err)
	}
	t := w.ActiveApp()
	if t == nil {
		return ProjectInfo{}, Errorf(CodeFailed, "project %s is not active", root)
	}
	return t.projectInfo(ctx, struct{}{})
}

// legacyClose implements project.close on a: the project of a is closed
// (unsaved changes kept as recovery points).
func (w *Workspace) legacyClose(ctx context.Context, a *App) (ProjectInfo, error) {
	cur := w.sessionOf(a)
	if cur == nil {
		a.closeProjectDirect()
		return ProjectInfo{}, nil
	}
	return ProjectInfo{}, apiError(w.closePreserving(ctx, cur))
}

// closePreserving closes the session p with the legacy project.close
// semantics: documents are not saved (unsaved changes become recovery
// points) and runs and debug sessions are not stopped explicitly.
func (w *Workspace) closePreserving(ctx context.Context, p *appProject) error {
	p.preserve.Store(true)
	err := w.mgr.Close(ctx, p.root, true)
	p.preserve.Store(false)
	return err
}

// ---- project sessions --------------------------------------------------------

// appProject is a workspace.Project backed by an App.
type appProject struct {
	w    *Workspace
	root string

	// preserve marks a legacy project.close/open: stop neither saves
	// nor stops runs (closeProjectDirect keeps recovery points).
	preserve atomic.Bool

	mu  sync.Mutex
	app *App // nil while hibernated
}

// current returns the project's App (nil while hibernated).
func (p *appProject) current() *App {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.app
}

// openProject is the workspace.Opener.
func (w *Workspace) openProject(ctx context.Context, root string) (workspace.Project, error) {
	p := &appProject{w: w, root: root}
	if err := p.Resume(ctx, nil); err != nil {
		return nil, err
	}
	return p, nil
}

// acquire returns the App for p: the host if it has no project, else a
// new one.
func (w *Workspace) acquire(p *appProject) (*App, error) {
	w.mu.Lock()
	if w.hostUser == nil && w.host.ProjectRoot() == "" {
		w.hostUser = p
		w.mu.Unlock()
		return w.host, nil
	}
	w.mu.Unlock()
	a, err := w.newApp()
	if err != nil {
		return nil, fmt.Errorf("new app: %w", err)
	}
	a.ws.Store(w)
	return a, nil
}

// release closes the project of app (the host keeps running).
func (w *Workspace) release(p *appProject, app *App) {
	if app == w.host {
		app.closeProjectDirect()
		w.mu.Lock()
		if w.hostUser == p {
			w.hostUser = nil
		}
		w.mu.Unlock()
		return
	}
	if err := app.Close(); err != nil {
		w.host.log.Warn("close project app", "root", p.root, "err", err)
	}
}

// terminalBlockers names the terminals of root that do work closing them
// would end: a shell with a job in the foreground ("terminal: vim") and a
// running plugin launch such as "docker compose up" or ssh, whose program
// is the foreground job itself.
func terminalBlockers(infos []terminal.Info, root string) []string {
	var out []string
	for _, i := range infos {
		if !i.Running || (i.Project != "" && i.Project != root) {
			continue
		}
		switch {
		case i.Busy && i.Process != "":
			out = append(out, "terminal: "+i.Process)
		case i.Kind != "" && i.Kind != terminal.KindShell:
			name := i.Name
			if name == "" {
				name = i.Kind + " " + i.Target
			}
			out = append(out, "terminal: "+name)
		}
	}
	return out
}

// Busy implements workspace.Project: running processes, terminals with
// work in the foreground and debug sessions.
func (p *appProject) Busy(ctx context.Context) []string {
	app := p.current()
	if app == nil {
		return nil
	}
	var out []string
	for _, i := range app.runs.List() {
		if i.State == run.StateRunning && (i.Project == "" || i.Project == p.root) {
			out = append(out, "run: "+i.Name)
		}
	}
	out = append(out, terminalBlockers(app.terms.List(), p.root)...)
	var sessions []struct {
		ID string `json:"id"`
	}
	if err := app.callLocal(ctx, "debug.sessions", nil, &sessions); err == nil {
		for _, s := range sessions {
			out = append(out, "debug session "+s.ID)
		}
	}
	return out
}

// Hibernate implements workspace.Project.
func (p *appProject) Hibernate(ctx context.Context, force bool) ([]string, error) {
	app := p.current()
	if app == nil {
		return nil, nil
	}
	if err := p.stop(ctx, app, force); err != nil {
		return nil, err
	}
	var docs []string
	for _, d := range app.openDocs() {
		docs = append(docs, d.Path())
	}
	sort.Strings(docs)
	p.drop(app)
	return docs, nil
}

// stop saves the documents and, with force, stops runs and debugging.
func (p *appProject) stop(ctx context.Context, app *App, force bool) error {
	if p.preserve.Load() {
		return nil
	}
	if !p.w.exiting.Load() {
		if err := app.saver.SaveAll(ctx, autosave.TriggerClose); err != nil && !force {
			return fmt.Errorf("%w: %v", workspace.ErrUnsaved, err)
		}
	}
	if force {
		app.runs.StopAll(func(i run.Info) bool { return i.Project == "" || i.Project == p.root })
		if len(p.Busy(ctx)) > 0 {
			_ = app.callLocal(ctx, "debug.stop", nil, nil)
		}
	}
	return nil
}

// drop releases app.
func (p *appProject) drop(app *App) {
	p.mu.Lock()
	p.app = nil
	p.mu.Unlock()
	p.w.release(p, app)
}

// Resume implements workspace.Project: an App opens the project again and
// the documents are reopened (language servers start as configured, e.g.
// on the first PHP document).
func (p *appProject) Resume(ctx context.Context, docs []string) error {
	if p.current() != nil {
		return nil
	}
	app, err := p.w.acquire(p)
	if err != nil {
		return err
	}
	if _, err := app.openProjectDirect(ctx, p.root); err != nil {
		p.w.release(p, app)
		return err
	}
	for _, d := range docs {
		if _, err := app.docOpen(ctx, DocOpenParams{Path: d}); err != nil {
			app.log.Info("resume: document not reopened", "path", d, "err", err)
		}
	}
	p.mu.Lock()
	p.app = app
	p.mu.Unlock()
	return nil
}

// Close implements workspace.Project.
func (p *appProject) Close(ctx context.Context, force bool) error {
	app := p.current()
	if app == nil {
		return nil
	}
	if err := p.stop(ctx, app, force); err != nil {
		return err
	}
	p.drop(app)
	return nil
}

// ---- API methods ---------------------------------------------------------------

func (a *App) registerProjects() {
	mustHandle(a, "projects.list", "Open projects of the workspace (active, background, hibernated)", func(context.Context, struct{}) (ProjectsListResult, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectsListResult{}, err
		}
		return w.List(), nil
	})
	mustHandle(a, "projects.open", "Open a project directory next to the others and make it active (switches if open)", func(ctx context.Context, p PathParams) (ProjectSession, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectSession{}, err
		}
		return w.Open(ctx, p.Path)
	})
	mustHandle(a, "projects.switch", "Make a project active (root or name; resumes a hibernated one, returns its clientState)", func(ctx context.Context, p ProjectsNameParams) (ProjectSession, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectSession{}, err
		}
		return w.Switch(ctx, p.Name)
	})
	mustHandle(a, "projects.hibernate", "Save and release a project (language server, watcher, index, documents); running work needs force", func(ctx context.Context, p ProjectsHibernateParams) (ProjectSession, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectSession{}, err
		}
		return w.Hibernate(ctx, p)
	})
	mustHandle(a, "projects.attachClientState", "Hand client state (tabs, cursors) to a hibernated project for its resume; a running project is left alone", func(_ context.Context, p ProjectsClientStateParams) (ProjectsClientStateResult, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectsClientStateResult{}, err
		}
		ok, err := w.AttachClientState(p.Name, p.ClientState)
		return ProjectsClientStateResult{Attached: ok}, err
	})
	mustHandle(a, "projects.resume", "Restore a hibernated project in the background (active if none is)", func(ctx context.Context, p ProjectsNameParams) (ProjectSession, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectSession{}, err
		}
		return w.Resume(ctx, p.Name)
	})
	mustHandle(a, "projects.close", "Close a project of the workspace; running work needs force", func(ctx context.Context, p ProjectsCloseParams) (ProjectsListResult, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectsListResult{}, err
		}
		if err := w.Close(ctx, p.Name, p.Force); err != nil {
			return ProjectsListResult{}, err
		}
		return w.List(), nil
	})
	mustHandle(a, "projects.busy", "What blocks hibernating or closing a project (runs, debug sessions)", func(ctx context.Context, p ProjectsNameParams) (ProjectsBusyResult, error) {
		w, err := a.workspace()
		if err != nil {
			return ProjectsBusyResult{}, err
		}
		b, err := w.Busy(ctx, p.Name)
		if b == nil {
			b = []string{}
		}
		return ProjectsBusyResult{Busy: b}, err
	})
}
