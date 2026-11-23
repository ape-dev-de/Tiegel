package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/fspath"
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
	// EditorFont is the editor font family ("" = default chain),
	// EditorFontSize its size in sp (0 = default), LineHeight the editor
	// line height factor (0 = default) and UIFontSize the UI text size in
	// sp (0 = default).
	EditorFont     string
	EditorFontSize float32
	LineHeight     float32
	UIFontSize     float32
	// InsertSpaces makes Tab insert spaces (.editorconfig overrides it
	// per file).
	InsertSpaces bool
	// OpenProjectIn is ask, new or this (another project while one is
	// open).
	OpenProjectIn string
	// SoftWrap is off, files or all; SoftWrapFiles are the name patterns
	// of "files" (D285).
	SoftWrap      string
	SoftWrapFiles []string
	// ShowWhitespace is none, selection, trailing or all (D286).
	ShowWhitespace string
	// RightMargin is the ruler column (effective value, project over
	// global; 0 = none), ShowRightMargin whether it is drawn (D287).
	RightMargin     int
	ShowRightMargin bool
	// InlayHints shows the language server's inlay hints (AP17.3).
	InlayHints bool
}

// Setting keys of the GUI: declared core settings (settings.schema) and
// client state (ui.recentProjects, ui.recentFiles).
const (
	KeyTheme          = "ui.theme"
	KeyRecentProjects = "ui.recentProjects"
	KeyRecentFiles    = "ui.recentFiles"
	KeyTabWidth       = "editor.tabWidth"
	KeyUpdateCheck    = "update.checkOnStart"
	KeyEditorFont     = "editor.fontFamily"
	KeyEditorFontSize = "editor.fontSize"
	KeyLineHeight     = "editor.lineHeight"
	KeyUIFontSize     = "ui.fontSize"
	KeyInsertSpaces   = "editor.insertSpaces"
	KeyOpenProjectIn  = "projects.openIn"
	KeyAutosaveIdle   = "autosave.idleSeconds"
	KeySoftWrap       = "editor.softWrap"
	KeySoftWrapFiles  = "editor.softWrapFiles"
	KeyShowWhitespace = "editor.showWhitespace"
	KeyRightMargin    = "editor.rightMargin"
	KeyShowMargin     = "editor.showRightMargin"
	KeyInlayHints     = "editor.inlayHints"
)

// DefaultOpenTool is the tool window shown on the first start of a
// project (the project tree).
const DefaultOpenTool = "project"

// maxRecent bounds the recent project and file lists.
const maxRecent = 20

// Controller is the GUI state machine. Create it with New; all exported
// fields and methods belong to the UI goroutine.
type Controller struct {
	be Backend
	// raw is the backend without the blocking-call guard (Backend).
	raw   Backend
	guard syncGuard
	// Invalidate requests a new frame; called from any goroutine.
	Invalidate func()

	ctx    context.Context
	cancel context.CancelFunc
	unsub  func()

	mu    sync.Mutex
	queue []func()
	// jobs counts running background work (Settle polls it; a WaitGroup
	// must not be reused while a timed-out Wait is still pending).
	jobs atomic.Int64

	// Project is the open project (Open false: welcome screen).
	Project ProjectInfo
	// RecentProjects are absolute project paths, most recent first.
	RecentProjects []string
	// Prefs are the user preferences.
	Prefs Prefs
	// Status feeds the status bar.
	Status Status
	// Templates is the live templates integration (templates.go).
	Templates LiveTemplates
	// Tree is the lazily loaded project tree.
	Tree *Tree
	// Git maps project-relative paths to their git state.
	Git map[string]GitFile
	// GitVersion changes whenever Git changes.
	GitVersion uint64
	// RolesVersion changes whenever the folder roles (RoleOf) change.
	RolesVersion uint64
	roles        map[string]string

	// Tabs are the open editors of the active editor group in tab order;
	// Active indexes Tabs (-1: none). See groups.go for splits.
	Tabs   []*Tab
	Active int
	// Root is the editor split tree; SplitVersion changes with it and
	// with the active group.
	Root         *SplitNode
	SplitVersion uint64
	groups       []*EditorGroup
	activeGroup  int

	// Tools is the tool window layout of the project (persisted per
	// project, see PersistToolLayout).
	Tools       *ToolLayout
	toolsSaved  uint64
	toolsReady  bool
	toolsSaving bool

	// MonoFonts lists the installed monospace font families (settings).
	MonoFonts []string
	// RecentFiles are absolute paths of recently opened files.
	RecentFiles []string
	// Nav is the back/forward navigation history.
	Nav Navigation
	// Keymap are the keymap settings (keynav.go).
	Keymap KeymapPrefs
	// KeymapEdit is the state of the keymap editor (keymapedit.go).
	KeymapEdit KeymapEdit

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
	// Probe is the info of the selected interpreter (interp.probe).
	Probe ProbeState
	// Schema is the result of settings.schema (core and plugin settings
	// with values; nil until loaded).
	Schema *SettingsSchema
	// Cands caches settings.candidates per key and Discovered the
	// interpreters found around the project (D521, D524).
	Cands      CandidateCache
	Discovered DiscoveredState
	// Setup is the project setup sheet (D526).
	Setup SetupState

	// Reveal is set when a tab should move its caret (go to definition,
	// problem click); the view applies and clears it.
	Reveal *Location

	// Now is the clock (tests replace it).
	Now func() time.Time

	gitPending bool

	// Recovery lists unsaved changes of the last session that could not
	// be saved (history.recovery, loaded when a project opens).
	Recovery        []RecoveryPoint
	RecoveryVersion uint64
	// RecoveryShown is a recovery point loaded for review (ShowRecovery).
	RecoveryShown        *RecoveryDiff
	RecoveryShownVersion uint64

	// Vcs is the git state (commit window, log, branches, gutter, merge).
	Vcs *Git
	// Debug is the debugger state (listener, sessions, stack, variables,
	// breakpoints).
	Debug *Debug
	// Work is the file side: file operations, external changes, diff
	// and history tabs, annotations, remote git (workspace.go).
	Work *Workspace
	// LSP is the language server supervisor state (lspsup.go).
	LSP LSPInfo
	// Services is the Services tool window (services.go).
	Services *Services
	// Run is the run tool window state (run.go).
	Run *Runs
	// QA is the state of the quality tool plugins (AP17.1).
	QA QAState
	// IDEImport is the settings import wizard (M16).
	IDEImport IDEImportState
	// Tests is the test state: gutter tests, results (tests.go).
	Tests *Tests
	// Composer holds the scripts of composer.json (plugin composer).
	Composer *Composer
	// Terminals is the terminal tool window state (terminal.go).
	Terminals *Terminals
}

// New returns a controller for be. Call Close when done.
func New(be Backend) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		raw: be, ctx: ctx, cancel: cancel, Active: -1,
		diags: map[string]*fileDiags{}, Git: map[string]GitFile{},
		Prefs: Prefs{Theme: "dark", TabWidth: 4}, Now: time.Now,
		Tools: NewToolLayout(),
	}
	c.be = guardedBackend{Backend: be, g: &c.guard}
	c.resetGroups()
	c.Vcs = newGit(c)
	c.Debug = newDebug(c)
	c.Work = newWorkspace(c)
	c.Services = newServices(c)
	c.Run = newRuns(c)
	c.Tests = newTests(c)
	c.Composer = newComposer(c)
	c.Terminals = newTerminals(c)
	unsub, err := be.Subscribe(ctx, []string{"lsp.*", "php.diagnostics", "php.versionChanged", "interpreter.changed",
		"git.changed", "git.committed", "file.saved", "doc.changed", "doc.edited", "modules.changed", "debug.*", "settings.changed", "install.*", "docker.*", "run.*", "qa.*", "test.*", "terminal.*", api.TopicPHPRootChanged}, func(e api.Event) {
		c.post(func() { c.handleEvent(e.Topic, e.Payload) })
	})
	if err != nil {
		unsub = func() {}
		c.Status.Notice = "events: " + err.Error()
	}
	c.unsub = unsub
	return c
}

// closeWait bounds how long Close waits for running background work.
const closeWait = 5 * time.Second

// Close stops background work and event delivery. It cancels the running
// jobs and waits (at most closeWait) until they returned: a job may still
// be inside the backend (e.g. an autosave writing files after the window
// lost its focus), and the backend is closed right after the controller.
// The jobs' UI results are discarded.
func (c *Controller) Close() {
	c.Services.shutdown()
	c.unsub()
	c.cancel()
	for deadline := time.Now().Add(closeWait); c.jobs.Load() > 0 && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
	}
}

// Context is cancelled by Close.
func (c *Controller) Context() context.Context { return c.ctx }

// Backend returns the backend without the blocking-call guard (D512):
// for tests and callers that run off the UI goroutine anyway.
func (c *Controller) Backend() Backend { return c.raw }

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
// nil) is applied on the UI goroutine by the next Drain. The context of
// work is marked Background: only there backend calls are allowed.
func (c *Controller) Go(work func(ctx context.Context) func()) {
	c.jobs.Add(1)
	go func() {
		defer c.jobs.Add(-1)
		if apply := work(Background(c.ctx)); apply != nil {
			c.post(apply)
		}
	}()
}

// Settle waits for background work and drains the results until nothing
// is pending or the timeout expires (tests and shutdown).
func (c *Controller) Settle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for c.jobs.Load() > 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
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

// call is Backend.Call; ctx must be marked Background (D512).
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
		p := c.loadPrefs(ctx)
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

// loadPrefs reads the preferences (global values; a missing value keeps the
// GUI default).
func (c *Controller) loadPrefs(ctx context.Context) Prefs {
	var p Prefs
	p.Theme = getSetting(ctx, c.be, KeyTheme, "global", "dark")
	p.TabWidth = getSetting(ctx, c.be, KeyTabWidth, "global", 4)
	p.UpdateCheck = getSetting(ctx, c.be, KeyUpdateCheck, "global", false)
	p.EditorFont = getSetting(ctx, c.be, KeyEditorFont, "global", "")
	p.EditorFontSize = getSetting[float32](ctx, c.be, KeyEditorFontSize, "global", 0)
	p.LineHeight = getSetting[float32](ctx, c.be, KeyLineHeight, "global", 0)
	p.UIFontSize = getSetting[float32](ctx, c.be, KeyUIFontSize, "global", 0)
	p.InsertSpaces = getSetting(ctx, c.be, KeyInsertSpaces, "global", true)
	p.OpenProjectIn = getSetting(ctx, c.be, KeyOpenProjectIn, "global", "ask")
	p.SoftWrap = getEffective(ctx, c.be, KeySoftWrap, "files")
	p.SoftWrapFiles = getEffective(ctx, c.be, KeySoftWrapFiles, []string{"*.md", "*.txt", "*.rst", "*.adoc"})
	p.ShowWhitespace = getEffective(ctx, c.be, KeyShowWhitespace, "none")
	p.RightMargin = getEffective(ctx, c.be, KeyRightMargin, 120)
	p.ShowRightMargin = getEffective(ctx, c.be, KeyShowMargin, true)
	p.InlayHints = getEffective(ctx, c.be, KeyInlayHints, true)
	var cfg struct {
		IdleSeconds *int `json:"idleSeconds"`
	}
	if err := c.call(ctx, "autosave.config", nil, &cfg); err == nil && cfg.IdleSeconds != nil {
		p.IdleSeconds = *cfg.IdleSeconds
	}
	return p
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

// getEffective reads the value of a setting that applies (project over
// global over the declared default), returning def if it is missing or
// invalid.
func getEffective[T any](ctx context.Context, be Backend, key string, def T) T {
	var v settingValue
	if err := be.Call(ctx, "settings.get", map[string]any{"key": key, "effective": true}, &v); err != nil || !v.Found {
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

// SetEditorFont changes and stores the editor font family ("" =
// default).
func (c *Controller) SetEditorFont(family string) {
	c.Prefs.EditorFont = family
	c.SetSetting(KeyEditorFont, "global", family)
}

// SetEditorFontSize changes and stores the editor font size (sp).
func (c *Controller) SetEditorFontSize(sp float32) {
	if sp < 6 || sp > 48 {
		return
	}
	c.Prefs.EditorFontSize = sp
	c.SetSetting(KeyEditorFontSize, "global", sp)
}

// SetLineHeight changes and stores the editor line height factor.
func (c *Controller) SetLineHeight(f float32) {
	if f < 1 || f > 2.5 {
		return
	}
	c.Prefs.LineHeight = f
	c.SetSetting(KeyLineHeight, "global", f)
}

// SetUIFontSize changes and stores the UI text size (sp).
func (c *Controller) SetUIFontSize(sp float32) {
	if sp < 9 || sp > 24 {
		return
	}
	c.Prefs.UIFontSize = sp
	c.SetSetting(KeyUIFontSize, "global", sp)
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

// OpenProject opens dir as the project of this window. An open project
// is closed first (documents saved, see CloseProject).
func (c *Controller) OpenProject(dir string) {
	wasOpen := c.Project.Open
	c.Go(func(ctx context.Context) func() {
		if wasOpen {
			if err := c.closeBackendProject(ctx); err != nil {
				return func() { c.notice(err) }
			}
		}
		var info ProjectInfo
		err := c.call(ctx, "project.open", map[string]string{"path": dir}, &info)
		return func() {
			if err != nil {
				if wasOpen {
					c.projectClosed()
				}
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
	c.resetGroups()
	c.diags = map[string]*fileDiags{}
	c.DiagVersion++
	c.Git = map[string]GitFile{}
	c.GitVersion++
	c.Nav = Navigation{}
	c.Files = nil
	c.Tree = NewTree(info.Name)
	c.addRecentProject(info.Root)
	c.loadToolLayout()
	c.LoadDir("")
	c.roles = nil
	c.LoadRoles()
	c.refreshGit()
	c.refreshRuntime()
	c.RefreshQA()
	c.checkIDEImportOffer()
	c.checkProjectSetup()
	c.Vcs.reset()
	c.Debug.reset()
	c.Debug.refresh()
	c.Work.reset()
	c.startLSP()
	c.RecoveryShown = nil
	c.setRecovery(nil)
	c.loadRecovery()
	prevMargin := c.Prefs.RightMargin
	c.Go(func(ctx context.Context) func() {
		rel := getSetting[[]string](ctx, c.be, KeyRecentFiles, "project", nil)
		margin := getEffective(ctx, c.be, KeyRightMargin, prevMargin) // project value
		return func() {
			c.Prefs.RightMargin = margin
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
	if c.Project.Root == "" {
		return rel
	}
	return fspath.Resolve(c.Project.Root, rel)
}

// PHPRoot returns the PHP project root (absolute; the project root
// unless composer.json lives in a subfolder, D515).
func (c *Controller) PHPRoot() string {
	if c.Project.PHPRoot == "" || c.Project.PHPRoot == "." {
		return c.Project.Root
	}
	return c.Abs(c.Project.PHPRoot)
}

// phpRootChanged follows project.phpRootChanged: roles, Composer scripts
// and the test status resolve from the PHP root.
func (c *Controller) phpRootChanged(payload json.RawMessage) {
	var ev struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(payload, &ev) != nil || !c.Project.Open {
		return
	}
	c.Project.PHPRoot = ev.Path
	c.LoadRoles()
	c.Composer.Load(nil)
	c.Tests.PHPRootChanged()
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
	if strings.HasPrefix(topic, "debug.") {
		c.Debug.handleEvent(topic, payload)
		return
	}
	if strings.HasPrefix(topic, "docker.") {
		c.Services.handleEvent(topic, payload)
		return
	}
	if strings.HasPrefix(topic, "run.") {
		c.Run.handleEvent(topic, payload)
		return
	}
	if strings.HasPrefix(topic, "test.") || topic == "install.done" {
		c.Tests.handleEvent(topic, payload)
	}
	if topic == "file.saved" {
		var ev struct {
			File string `json:"file"`
		}
		if json.Unmarshal(payload, &ev) == nil && ev.File != "" {
			c.Tests.FileChanged(ev.File)
			c.Composer.FileChanged(ev.File)
			c.rolesFileChanged(ev.File)
		}
	}
	if strings.HasPrefix(topic, "qa.") {
		c.handleQAEvent(topic, payload)
		return
	}
	if strings.HasPrefix(topic, "terminal.") || topic == "settings.changed" {
		c.Terminals.handleEvent(topic, payload)
	}
	if strings.HasPrefix(topic, "terminal.") {
		return
	}
	switch topic {
	case "git.changed", "git.committed", api.TopicDocEdited:
		c.Vcs.handleEvent(topic, payload)
	}
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
	case "lsp.state", "lsp.log", "install.output", "install.done":
		c.handleLSPEvent(topic, payload)
		if topic == "install.done" {
			c.RefreshQA() // a freshly installed tool becomes available
		}
	case "php.versionChanged", "interpreter.changed":
		c.refreshRuntime()
	case "git.changed", "git.committed", "file.saved":
		c.refreshGit()
	case "doc.changed":
		var st DocState
		if json.Unmarshal(payload, &st) == nil {
			abs := c.Abs(st.Path)
			for _, t := range c.allTabs() {
				if t.File == abs {
					t.Modified = st.Modified
					t.EOL = st.EOL
				}
			}
		}
	case api.TopicPHPRootChanged:
		c.phpRootChanged(payload)
	case "settings.changed":
		if keymapSettingChanged(payload) {
			c.LoadKeymap()
		}
		c.RefreshQA()
	case "modules.changed":
		c.RefreshQA()
		if c.Project.Open {
			c.refreshGit()
			c.refreshRuntime()
			c.Debug.refresh()
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

// DetectPHPStarting detects the PHP version and allows starting a stopped
// compose container for it (php.version with start: true; docker may pull
// an image). Only call it on an explicit user action.
func (c *Controller) DetectPHPStarting() {
	c.Status.Notice = "Starting container to detect the PHP version…"
	c.Go(func(ctx context.Context) func() {
		var v PHPVersion
		err := c.call(ctx, "php.version", map[string]bool{"start": true}, &v)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.PHP = v
			c.Status.Notice = "PHP " + v.Short + " detected"
			if v.Hint != nil {
				c.Status.Notice = v.Hint.Message
			}
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
		var st fullStatus
		err := c.call(ctx, "git.status", nil, &st)
		return func() {
			c.gitPending = false
			c.Git = map[string]GitFile{}
			c.Status.Branch = ""
			c.Vcs.setStatus(st, err)
			if err == nil {
				for _, f := range st.Files {
					c.Git[f.Path] = GitFile{Path: f.Path, Kind: f.Kind, Index: f.Index, Worktree: f.Worktree, Conflict: f.Conflict}
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
	c.Status.LSP = lspStarting
	c.Go(func(ctx context.Context) func() {
		var st LSPStatus
		err := c.call(ctx, "lsp.start", map[string]any{}, &st)
		return func() { c.applyLSPStatus(st, err) }
	})
}

// RestartLSP restarts the language server (also after a crash loop).
func (c *Controller) RestartLSP() {
	c.Status.LSP = lspStarting
	c.lspCall("lsp.restart", nil)
}

func (c *Controller) applyLSPStatus(st LSPStatus, err error) {
	switch {
	case err != nil:
		c.Status.LSPRunning = false
		switch api.ErrorCode(err) {
		case api.CodeModuleDisabled, api.CodeMethodNotFound:
			c.Status.LSP, c.Status.LSPHint = "", ""
		case api.CodeUnavailable, api.CodeNotFound:
			if st := c.LSP.Status.State; st != "" && st != LSPRunning && st != LSPStarting {
				c.applyLSPState(c.LSP.Status) // the lsp.state event explains it
				return
			}
			c.Status.LSP = "No language server"
			c.Status.LSPHint = "No PHP language server found. Install Phpactor or PHPantom (e.g. via Composer or your package manager); Tiegel never downloads one. " + err.Error()
		default:
			c.Status.LSP = "LSP failed"
			c.Status.LSPHint = err.Error()
		}
	case st.State != "":
		c.applyLSPState(st)
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
