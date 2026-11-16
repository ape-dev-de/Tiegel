package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Status is the information shown in the status bar.
type Status struct {
	// LSP describes the language server ("Phpactor", "No language server").
	LSP string
	// LSPRunning is true while a server runs.
	LSPRunning bool
	// LSPHint explains a missing or failed server (tooltip).
	LSPHint string
	// PHP is the project's PHP version.
	PHP PHPVersion
	// Interpreter is the label of the selected interpreter.
	Interpreter string
	// Branch is the git branch ("" without git).
	Branch string
	// Notice is a transient message (last error, update available).
	Notice string
}

// Prefs are the user preferences kept in the internal settings.
type Prefs struct {
	// Theme is "dark", "light" or "system".
	Theme string
	// TabWidth is the editor tab width.
	TabWidth int
	// IdleSeconds is the autosave idle delay (0 = off).
	IdleSeconds int
	// UpdateCheck enables the update check at start (opt-in).
	UpdateCheck bool
}

// Setting keys of the GUI (namespaces ui.*, editor.*, update.*).
const (
	KeyTheme          = "ui.theme"
	KeyRecentProjects = "ui.recentProjects"
	KeyRecentFiles    = "ui.recentFiles"
	KeyTabWidth       = "editor.tabWidth"
	KeyUpdateCheck    = "update.checkOnStart"
)

// maxRecent bounds the recent project and file lists.
const maxRecent = 20

// Controller is the GUI state machine. Create it with New; all exported
// fields and methods belong to the UI goroutine.
type Controller struct {
	be Backend
	// Invalidate requests a new frame; called from any goroutine.
	Invalidate func()

	ctx    context.Context
	cancel context.CancelFunc
	unsub  func()

	mu    sync.Mutex
	queue []func()
	jobs  sync.WaitGroup

	// Project is the open project (Open false: welcome screen).
	Project ProjectInfo
	// RecentProjects are absolute project paths, most recent first.
	RecentProjects []string
	// Prefs are the user preferences.
	Prefs Prefs
	// Status feeds the status bar.
	Status Status
	// Tree is the lazily loaded project tree.
	Tree *Tree
	// Git maps project-relative paths to their git state.
	Git map[string]GitFile
	// GitVersion changes whenever Git changes.
	GitVersion uint64

	// Tabs are the open editors in tab order; Active indexes Tabs (-1:
	// none).
	Tabs   []*Tab
	Active int
	// RecentFiles are absolute paths of recently opened files.
	RecentFiles []string
	// Nav is the back/forward navigation history.
	Nav Navigation

	// Diagnostics per absolute file.
	diags map[string]*fileDiags
	// DiagVersion changes whenever any diagnostics change.
	DiagVersion uint64

	// Completion, Signature and Hover are the editor popups.
	Completion Completion
	Signature  SignatureHelp
	Hover      HoverInfo

	// Locations is a pending choice of several go-to targets.
	Locations []Location
	// LocationsTitle describes Locations ("Implementations").
	LocationsTitle string

	// Files is the quick-open index (project-relative paths).
	Files []string

	// Modules, Interpreters, Mappings and Diagnosis feed the settings
	// dialog (loaded by LoadSettingsData).
	Modules      []ModuleInfo
	Interpreters []Interpreter
	Mappings     Mappings
	Diagnosis    Diagnosis

	// Reveal is set when a tab should move its caret (go to definition,
	// problem click); the view applies and clears it.
	Reveal *Location

	// Now is the clock (tests replace it).
	Now func() time.Time

	gitPending bool
}

// New returns a controller for be. Call Close when done.
func New(be Backend) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		be: be, ctx: ctx, cancel: cancel, Active: -1,
		diags: map[string]*fileDiags{}, Git: map[string]GitFile{},
		Prefs: Prefs{Theme: "dark", TabWidth: 4}, Now: time.Now,
	}
	unsub, err := be.Subscribe(ctx, []string{"lsp.*", "php.diagnostics", "php.versionChanged", "interpreter.changed",
		"git.changed", "git.committed", "file.saved", "doc.changed", "modules.changed"}, func(e api.Event) {
		c.post(func() { c.handleEvent(e.Topic, e.Payload) })
	})
	if err != nil {
		unsub = func() {}
		c.Status.Notice = "events: " + err.Error()
	}
	c.unsub = unsub
	return c
}

// Close stops background work and event delivery.
func (c *Controller) Close() {
	c.unsub()
	c.cancel()
}

// Context is cancelled by Close.
func (c *Controller) Context() context.Context { return c.ctx }

// Backend returns the backend.
func (c *Controller) Backend() Backend { return c.be }

// post queues fn for the UI goroutine.
func (c *Controller) post(fn func()) {
	c.mu.Lock()
	c.queue = append(c.queue, fn)
	c.mu.Unlock()
	if c.Invalidate != nil {
		c.Invalidate()
	}
}

// Drain runs the queued results on the calling (UI) goroutine. It reports
// whether anything ran.
func (c *Controller) Drain() bool {
	ran := false
	for {
		c.mu.Lock()
		q := c.queue
		c.queue = nil
		c.mu.Unlock()
		if len(q) == 0 {
			return ran
		}
		for _, fn := range q {
			fn()
		}
		ran = true
	}
}

// Go runs work on a background goroutine; the function it returns (if not
// nil) is applied on the UI goroutine by the next Drain.
func (c *Controller) Go(work func(ctx context.Context) func()) {
	c.jobs.Add(1)
	go func() {
		defer c.jobs.Done()
		if apply := work(c.ctx); apply != nil {
			c.post(apply)
		}
	}()
}

// Settle waits for background work and drains the results until nothing
// is pending or the timeout expires (tests and shutdown).
func (c *Controller) Settle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done := make(chan struct{})
		go func() { c.jobs.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Until(deadline)):
		}
		// Events from the bus arrive asynchronously; give them a moment.
		time.Sleep(10 * time.Millisecond)
		if !c.Drain() {
			c.mu.Lock()
			empty := len(c.queue) == 0
			c.mu.Unlock()
			if empty {
				return
			}
		}
	}
}

// call is Backend.Call with the controller context.
func (c *Controller) call(ctx context.Context, method string, params, result any) error {
	return c.be.Call(ctx, method, params, result)
}

// notice shows err in the status bar (UI goroutine).
func (c *Controller) notice(err error) {
	if err != nil {
		c.Status.Notice = err.Error()
	}
}

// ---- startup, projects, preferences ---------------------------------------

// Start loads preferences and the recent projects; with a non-empty
// projectPath the project is opened (or, if the backend already has it
// open, adopted).
func (c *Controller) Start(projectPath string) {
	c.Go(func(ctx context.Context) func() {
		var p Prefs
		p.Theme = getSetting(ctx, c.be, KeyTheme, "global", "dark")
		p.TabWidth = getSetting(ctx, c.be, KeyTabWidth, "global", 4)
		p.UpdateCheck = getSetting(ctx, c.be, KeyUpdateCheck, "global", false)
		var cfg struct {
			IdleSeconds *int `json:"idleSeconds"`
		}
		if err := c.call(ctx, "autosave.config", nil, &cfg); err == nil && cfg.IdleSeconds != nil {
			p.IdleSeconds = *cfg.IdleSeconds
		}
		recent := getSetting[[]string](ctx, c.be, KeyRecentProjects, "global", nil)
		var info ProjectInfo
		_ = c.call(ctx, "project.info", nil, &info)
		return func() {
			c.Prefs = p
			c.RecentProjects = recent
			if p.UpdateCheck {
				c.CheckUpdate()
			}
			switch {
			case info.Open:
				c.projectOpened(info)
			case projectPath != "":
				c.OpenProject(projectPath)
			}
		}
	})
}

// getSetting reads a setting and decodes it, returning def if it is
// missing or invalid.
func getSetting[T any](ctx context.Context, be Backend, key, scope string, def T) T {
	var v settingValue
	if err := be.Call(ctx, "settings.get", map[string]string{"key": key, "scope": scope}, &v); err != nil || !v.Found {
		return def
	}
	var out T
	if err := json.Unmarshal(v.Value, &out); err != nil {
		return def
	}
	return out
}

// SetSetting writes a client setting in the background.
func (c *Controller) SetSetting(key, scope string, value any) {
	c.Go(func(ctx context.Context) func() {
		raw, _ := json.Marshal(value)
		err := c.call(ctx, "settings.set", map[string]any{"key": key, "scope": scope, "value": json.RawMessage(raw)}, nil)
		if err != nil {
			return func() { c.notice(err) }
		}
		return nil
	})
}

// SetTheme changes and stores the theme preference.
func (c *Controller) SetTheme(theme string) {
	c.Prefs.Theme = theme
	c.SetSetting(KeyTheme, "global", theme)
}

// SetTabWidth changes and stores the tab width.
func (c *Controller) SetTabWidth(n int) {
	if n < 1 || n > 16 {
		return
	}
	c.Prefs.TabWidth = n
	c.SetSetting(KeyTabWidth, "global", n)
}

// SetUpdateCheck enables or disables the update check at start.
func (c *Controller) SetUpdateCheck(on bool) {
	c.Prefs.UpdateCheck = on
	c.SetSetting(KeyUpdateCheck, "global", on)
}

// SetIdleSeconds sets the autosave idle delay through the kernel.
func (c *Controller) SetIdleSeconds(secs int) {
	c.Prefs.IdleSeconds = secs
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "autosave.config", map[string]int{"idleSeconds": secs}, nil)
		return func() { c.notice(err) }
	})
}

// CheckUpdate runs the (user-enabled) update check.
func (c *Controller) CheckUpdate() {
	c.Go(func(ctx context.Context) func() {
		var r updateResult
		err := c.call(ctx, "update.check", nil, &r)
		return func() {
			switch {
			case err != nil:
				c.Status.Notice = "Update check failed: " + err.Error()
			case r.Available:
				c.Status.Notice = "Update available: " + r.Latest
			default:
				c.Status.Notice = "Tiegel is up to date"
			}
		}
	})
}

// OpenProject opens dir as the project (closing open tabs).
func (c *Controller) OpenProject(dir string) {
	c.Go(func(ctx context.Context) func() {
		var info ProjectInfo
		err := c.call(ctx, "project.open", map[string]string{"path": dir}, &info)
		return func() {
			if err != nil {
				c.Status.Notice = err.Error()
				return
			}
			c.projectOpened(info)
		}
	})
}

// projectOpened resets the project state and starts the background loads.
func (c *Controller) projectOpened(info ProjectInfo) {
	c.Project = info
	c.Tabs, c.Active = nil, -1
	c.diags = map[string]*fileDiags{}
	c.DiagVersion++
	c.Git = map[string]GitFile{}
	c.GitVersion++
	c.Nav = Navigation{}
	c.Files = nil
	c.Tree = NewTree(info.Name)
	c.RecentProjects = addRecent(c.RecentProjects, info.Root)
	c.SetSetting(KeyRecentProjects, "global", c.RecentProjects)
	c.LoadDir("")
	c.refreshGit()
	c.refreshRuntime()
	c.startLSP()
	c.Go(func(ctx context.Context) func() {
		rel := getSetting[[]string](ctx, c.be, KeyRecentFiles, "project", nil)
		return func() {
			c.RecentFiles = c.RecentFiles[:0]
			for _, r := range rel {
				c.RecentFiles = append(c.RecentFiles, c.Abs(r))
			}
		}
	})
}

// addRecent moves item to the front of list.
func addRecent(list []string, item string) []string {
	out := []string{item}
	for _, x := range list {
		if x != item && len(out) < maxRecent {
			out = append(out, x)
		}
	}
	return out
}

// Abs resolves a project-relative path.
func (c *Controller) Abs(rel string) string {
	if filepath.IsAbs(rel) || c.Project.Root == "" {
		return rel
	}
	return filepath.Join(c.Project.Root, filepath.FromSlash(rel))
}

// Rel returns the project-relative slash path of abs (abs if outside).
func (c *Controller) Rel(abs string) string {
	if c.Project.Root == "" {
		return abs
	}
	r, err := filepath.Rel(c.Project.Root, abs)
	if err != nil || r == ".." || len(r) > 2 && r[:3] == ".."+string(filepath.Separator) {
		return abs
	}
	return filepath.ToSlash(r)
}

// ---- events ---------------------------------------------------------------

func (c *Controller) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "lsp.diagnostics", "php.diagnostics":
		var ev diagEvent
		if json.Unmarshal(payload, &ev) == nil {
			src := sourceLSP
			if topic == "php.diagnostics" {
				src = sourcePHP
			}
			c.setDiagnostics(ev, src)
		}
	case "lsp.status":
		var st LSPStatus
		if json.Unmarshal(payload, &st) == nil {
			c.applyLSPStatus(st, nil)
		}
	case "php.versionChanged", "interpreter.changed":
		c.refreshRuntime()
	case "git.changed", "git.committed", "file.saved":
		c.refreshGit()
	case "doc.changed":
		var st DocState
		if json.Unmarshal(payload, &st) == nil {
			if t := c.Tab(c.Abs(st.Path)); t != nil {
				t.Modified = st.Modified
				t.EOL = st.EOL
			}
		}
	case "modules.changed":
		if c.Project.Open {
			c.refreshGit()
			c.refreshRuntime()
		}
	}
}

// ---- runtime, git, lsp ----------------------------------------------------

// refreshRuntime reloads the PHP version and the interpreter label.
func (c *Controller) refreshRuntime() {
	c.Go(func(ctx context.Context) func() {
		var v PHPVersion
		verr := c.call(ctx, "php.version", nil, &v)
		var list []Interpreter
		_ = c.call(ctx, "interp.list", map[string]bool{"compose": false}, &list)
		label := ""
		for _, i := range list {
			if i.Selected {
				label = i.Label
			}
		}
		return func() {
			if verr != nil {
				v = PHPVersion{}
			}
			c.Status.PHP = v
			c.Status.Interpreter = label
		}
	})
}

// SetPHPOverride sets ("" clears) the PHP version override.
func (c *Controller) SetPHPOverride(version string) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "php.setVersionOverride", map[string]string{"version": version}, nil)
		return func() {
			c.notice(err)
			c.refreshRuntime()
		}
	})
}

// PHPVersions lists the versions offered for the override.
var PHPVersions = []string{"8.5", "8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}

// refreshGit reloads git.status (coalesced; ignored without git module).
func (c *Controller) refreshGit() {
	if c.gitPending || !c.Project.Open {
		return
	}
	c.gitPending = true
	c.Go(func(ctx context.Context) func() {
		var st gitStatus
		err := c.call(ctx, "git.status", nil, &st)
		return func() {
			c.gitPending = false
			c.Git = map[string]GitFile{}
			c.Status.Branch = ""
			if err == nil {
				for _, f := range st.Files {
					c.Git[f.Path] = f
				}
				c.Status.Branch = st.Branch
				if st.Detached && len(st.Head) >= 7 {
					c.Status.Branch = st.Head[:7]
				}
			}
			c.GitVersion++
		}
	})
}

// startLSP starts the language server if one is installed; otherwise the
// status bar shows a hint. Nothing is downloaded.
func (c *Controller) startLSP() {
	c.Status.LSP = "LSP: starting…"
	c.Go(func(ctx context.Context) func() {
		var st LSPStatus
		err := c.call(ctx, "lsp.start", map[string]any{}, &st)
		return func() { c.applyLSPStatus(st, err) }
	})
}

// RestartLSP starts the language server again.
func (c *Controller) RestartLSP() { c.startLSP() }

func (c *Controller) applyLSPStatus(st LSPStatus, err error) {
	switch {
	case err != nil:
		c.Status.LSPRunning = false
		switch api.ErrorCode(err) {
		case api.CodeModuleDisabled, api.CodeMethodNotFound:
			c.Status.LSP, c.Status.LSPHint = "", ""
		case api.CodeUnavailable, api.CodeNotFound:
			c.Status.LSP = "No language server"
			c.Status.LSPHint = "No PHP language server found. Install Phpactor or PHPantom (e.g. via Composer or your package manager); Tiegel never downloads one. " + err.Error()
		default:
			c.Status.LSP = "LSP failed"
			c.Status.LSPHint = err.Error()
		}
	case st.Running:
		c.Status.LSPRunning = true
		name := st.Name
		if name == "" {
			name = st.Server
		}
		c.Status.LSP, c.Status.LSPHint = name, fmt.Sprintf("%s running (PHP %s)", name, st.PHPVersion)
	default:
		wasRunning := c.Status.LSPRunning
		c.Status.LSPRunning = false
		if st.Error != "" {
			c.Status.LSP, c.Status.LSPHint = "LSP crashed", st.Error
		} else if wasRunning {
			c.Status.LSP, c.Status.LSPHint = "LSP stopped", ""
		}
	}
}

// errIsDisabled reports whether err means "module disabled".
func errIsDisabled(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.CodeModuleDisabled
}
