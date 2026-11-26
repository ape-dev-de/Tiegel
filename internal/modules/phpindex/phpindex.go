// Package phpindex is the "php-index" module: a project-wide PHP symbol
// index on Tree-sitter (internal/index) for structural navigation without
// or in addition to the language server (decision D111).
//
// Methods:
//
//	index.status          state, progress, statistics (optionally waits)
//	index.config          vendor/.gitignore/exclude settings (per project)
//	index.rebuild         re-scan (or drop the cache and re-index)
//	nav.definition        declarations of the symbol at a position (+ LSP)
//	nav.implementations   implementations/overrides (+ LSP)
//	nav.super             supertypes / overridden and implemented methods
//	nav.typeHierarchy     supertypes and subtypes of a class
//	nav.symbols           fuzzy symbol search (Go to Class / Symbol)
//	nav.gutter            implements/overrides marks of a file
//
// Events: index.progress (Progress) while indexing, index.updated
// (UpdatedEvent) after every change of the index content.
//
// The index is built in the background when a project opens: the cache
// from the project's settings directory is loaded first (instant results),
// then a Sync parses only new and changed files. Afterwards it follows
// document edits (doc.edited, debounced), saves (file.saved), file system
// changes (files.changed from the shared watcher), git operations
// (git.changed) and index.rebuild. Results of the language server come in
// through the nav.lsp hook (lang-php); the modules never call each other.
package phpindex

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/index"
)

// ID is the module ID.
const ID = "php-index"

// Topics published by the module.
const (
	TopicProgress = "index.progress"
	TopicUpdated  = "index.updated"
	// TopicFilesChanged is published by the project watcher and the
	// file operations (api.FilesChangedEvent).
	TopicFilesChanged = api.TopicFilesChanged
)

// Settings (declared in SettingSpecs; project value over global value).
const (
	// SettingLibraries indexes vendor/ (default true; marked as library).
	SettingLibraries = ID + ".libraries"
	// SettingIncludeIgnored indexes files matched by .gitignore.
	SettingIncludeIgnored = ID + ".includeIgnored"
	// SettingExclude lists additional patterns to skip.
	SettingExclude = ID + ".exclude"
)

// legacyKeys maps the keys used before the module namespace (D215) to
// the current ones; they are migrated when a project's index starts.
var legacyKeys = map[string]string{
	"index.libraries":      SettingLibraries,
	"index.includeIgnored": SettingIncludeIgnored,
	"index.exclude":        SettingExclude,
}

// DefaultExclude are the folders the index skips by default: caches and
// temp folders of common frameworks contain generated PHP (compiled
// containers, proxies, views) that would duplicate real classes.
var DefaultExclude = []string{"tmp", "cache", "var/cache"}

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingLibraries, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject,
			Label: "Index libraries (vendor/)", Help: "Include Composer dependencies so navigation and completion know their classes."},
		{Key: SettingIncludeIgnored, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject,
			Label: "Index ignored files", Help: "Also index files matched by .gitignore (local configuration, generated code); cache folders are left out by the excluded patterns."},
		{Key: SettingExclude, Kind: settings.KindList, Default: DefaultExclude, Scope: settings.ScopeProject,
			Label: "Excluded patterns", Help: "Relative paths or base-name patterns (path.Match) the index skips. The defaults skip cache and temp folders (var/cache, app/tmp, storage/framework/cache …) whose generated PHP would duplicate classes."},
	}
}

// Options configure the module.
type Options struct {
	// Lazy defers indexing until the first index.*/nav.* call (one-shot
	// CLI commands that do not navigate should not index).
	Lazy bool
	// DocDelay is the debounce delay for document edits (default 300 ms).
	DocDelay time.Duration
	// SaveDelay is how long the cache write waits after incremental
	// changes (default 10 s).
	SaveDelay time.Duration
	// Workers overrides the number of parallel parsers.
	Workers int
}

// Module is the php-index module.
type Module struct {
	opts Options

	mu  sync.Mutex
	app *api.App
	st  *state
	cfg Config // settings the current state was built with
	// stopped is set by Stop: late bus events or calls must not create a
	// new index state (it would write its cache after the App closed).
	stopped bool
	// retiring counts states of a previous project still shutting down.
	retiring sync.WaitGroup
}

// New creates the module.
func New(opts Options) *Module {
	if opts.DocDelay <= 0 {
		opts.DocDelay = 300 * time.Millisecond
	}
	if opts.SaveDelay <= 0 {
		opts.SaveDelay = 10 * time.Second
	}
	return &Module{opts: opts}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"index.*", "nav.*"} }

// Start implements module.Module.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("php-index: needs an api.App context")
	}
	m.mu.Lock()
	m.app, m.stopped = app, false
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "index.status", "PHP symbol index: state, progress, statistics (wait: until ready)", m.status)
	api.Add(p, "index.config", "Get or set what the index covers (vendor, .gitignore'd files, excludes; per project)", m.config)
	api.Add(p, "index.rebuild", "Re-scan the project (full: drop the cache and re-index everything)", m.rebuild)
	api.Add(p, "nav.definition", "Declarations of the symbol at a position (index, merged with the language server)", m.definition)
	api.Add(p, "nav.implementations", "Implementations, subclasses and overriding methods of the symbol at a position", m.implementations)
	api.Add(p, "nav.super", "Supertypes of a class or the methods a method overrides/implements", m.super)
	api.Add(p, "nav.typeHierarchy", "Supertypes and subtypes of a class (by FQN or position)", m.typeHierarchy)
	api.Add(p, "nav.symbols", "Fuzzy search for classes and symbols (Go to Class / Go to Symbol)", m.symbols)
	api.Add(p, "nav.gutter", "Implements/overrides marks of a file for the gutter", m.gutter)
	if err := p.Err(); err != nil {
		return err
	}
	b := ctx.Bus()
	ctx.OnStop(b.Subscribe(api.TopicProjectOpened, func(bus.Event) { m.projectChanged() }))
	ctx.OnStop(b.Subscribe(api.TopicProjectClosed, func(bus.Event) { m.projectChanged() }))
	ctx.OnStop(b.Subscribe("doc.*", m.onDoc))
	ctx.OnStop(b.Subscribe(api.TopicFileSaved, m.onSaved))
	ctx.OnStop(settings.Changed.Subscribe(b, m.onSetting))
	ctx.OnStop(b.Subscribe(TopicFilesChanged, m.onFilesChanged))
	ctx.OnStop(b.Subscribe(api.TopicGitChanged, func(bus.Event) { m.kick() }))
	if !m.opts.Lazy {
		m.projectChanged()
	}
	return nil
}

// Stop implements module.Module: indexing stops and the cache is written.
func (m *Module) Stop() error {
	m.mu.Lock()
	st := m.st
	m.st, m.stopped = nil, true
	m.mu.Unlock()
	if st != nil {
		st.stop()
	}
	m.retiring.Wait()
	return nil
}

// projectChanged (re)creates the index state for the open project.
// project.opened arrives asynchronously, possibly after a call already
// created the state for the same project: that state is kept.
func (m *Module) projectChanged() {
	m.mu.Lock()
	old := m.st
	if old != nil && m.app != nil {
		if root, err := m.app.RequireProject(); err == nil && root == old.root {
			m.mu.Unlock()
			if !m.opts.Lazy {
				old.start()
			}
			return
		}
	}
	m.st = nil
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}
	if m.opts.Lazy {
		return
	}
	if st, err := m.state(); err == nil {
		st.start()
	}
}

// restart drops the index state (new options) and, unless lazy, starts a
// new one.
func (m *Module) restart() {
	m.mu.Lock()
	old := m.st
	m.st = nil
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}
	if m.opts.Lazy {
		return
	}
	if st, err := m.state(); err == nil {
		st.start()
	}
}

// state returns the index state of the open project, starting it if
// needed.
func (m *Module) state() (*state, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.app == nil || m.stopped {
		return nil, api.Errorf(api.CodeModuleDisabled, "php-index stopped")
	}
	root, err := m.app.RequireProject()
	if err != nil {
		return nil, err
	}
	if m.st != nil && m.st.root == root {
		return m.st, nil
	}
	if old := m.st; old != nil {
		m.retiring.Add(1)
		go func() {
			defer m.retiring.Done()
			old.stop()
		}()
	}
	for _, st := range []*settings.Store{m.app.ProjectSettings(), m.app.Settings()} {
		for from, to := range legacyKeys {
			if _, err := settings.MigrateKey(st, from, to); err != nil {
				m.app.Logger().Warn("php-index: migrate setting", "err", err)
			}
		}
	}
	m.cfg = m.effectiveConfig(m.app)
	opts := index.Options{
		Root:           root,
		SkipLibraries:  !m.cfg.Libraries,
		IncludeIgnored: m.cfg.IncludeIgnored,
		Exclude:        m.cfg.Exclude,
		Workers:        m.opts.Workers,
	}
	cacheDir := ""
	if ps := m.app.ProjectSettings(); ps != nil {
		cacheDir = filepath.Join(ps.Dir(), "index")
	}
	m.st = newState(m.app, m.opts, opts, cacheDir)
	return m.st, nil
}

func (m *Module) current() *state {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *Module) kick() {
	if st := m.current(); st != nil {
		st.requestSync(false)
	}
}

func (m *Module) onSaved(ev bus.Event) {
	p, ok := ev.Payload.(api.FileSavedEvent)
	if st := m.current(); ok && st != nil {
		st.queueFiles(p.File)
	}
}

func (m *Module) onFilesChanged(ev bus.Event) {
	st := m.current()
	if st == nil {
		return
	}
	fc, ok := ev.Payload.(api.FilesChangedEvent)
	if !ok || len(fc.Changes) == 0 {
		st.requestSync(false) // unknown payload: a cheap mtime scan
		return
	}
	for _, c := range fc.Changes {
		for _, p := range []string{c.File, c.OldFile} {
			switch {
			case p == "":
			case !c.Dir && st.ix.Accepts(p):
				st.queueFiles(p) // a missing file is removed from the index
			case c.Dir:
				// a directory appeared, vanished or moved: a cheap mtime
				// scan finds what happened below it
				st.requestSync(false)
			}
		}
	}
}

func (m *Module) onDoc(ev bus.Event) {
	st := m.current()
	if st == nil {
		return
	}
	switch p := ev.Payload.(type) {
	case api.DocEditEvent:
		if st.ix.Accepts(p.File) {
			st.queueDoc(p.File)
		}
	case api.DocCloseResult:
		if abs, err := m.app.ResolvePath(p.Path); err == nil && st.ix.Accepts(abs) {
			st.queueFiles(abs) // unsaved changes are gone: back to the disk state
		}
	}
}

// ---- API types ----

// Location is a navigation target in API coordinates.
type Location struct {
	// Path is the display path (project relative), File the absolute one.
	Path string `json:"path"`
	File string `json:"file"`
	// Range is the range of the declared name.
	Range api.TextRange `json:"range"`
	// DeclRange is the whole declaration (index results only).
	DeclRange *api.TextRange `json:"declRange,omitempty"`
	// Kind: class, interface, trait, enum, method, property,
	// classConstant, enumCase, function, constant ("" for LSP-only
	// results).
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	FQN       string `json:"fqn,omitempty"`
	Container string `json:"container,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Library   bool   `json:"library,omitempty"`
	// Source is "index", "lsp" or "index+lsp".
	Source string `json:"source,omitempty"`
}

func textPos(p index.Pos) api.TextPos { return api.TextPos{Offset: p.Offset, Line: p.Line, Col: p.Col} }

func textRange(r index.Range) api.TextRange {
	return api.TextRange{Start: textPos(r.Start), End: textPos(r.End)}
}

func (m *Module) location(l index.Location) Location {
	out := Location{
		File: l.Path, Range: textRange(l.NameRange), Kind: l.Kind.String(), Name: l.Name, FQN: l.FQN,
		Container: l.Container, Detail: l.Detail, Library: l.Library, Source: "index",
	}
	if l.Path != "" {
		out.Path = m.app.DisplayPath(l.Path)
		dr := textRange(l.Range)
		out.DeclRange = &dr
	} else {
		out.Kind, out.Source = "", ""
	}
	return out
}

func (m *Module) locations(in []index.Location) []Location {
	out := make([]Location, 0, len(in))
	for _, l := range in {
		out = append(out, m.location(l))
	}
	return out
}

// Status is the result of index.status.
type Status struct {
	// State: "idle" (lazy, not started), "loading", "indexing", "ready",
	// "error" or "unavailable" (built without Tree-sitter).
	State    string         `json:"state"`
	Root     string         `json:"root,omitempty"`
	Progress index.Progress `json:"progress"`
	index.Stats
	// Generation increases with every change of the index content.
	Generation uint64 `json:"generation"`
	// FromCache is true when the cache was loaded at start.
	FromCache bool `json:"fromCache,omitempty"`
	// LastSync is the end of the last full scan (RFC 3339), Duration its
	// length in milliseconds.
	LastSync string `json:"lastSync,omitempty"`
	Duration int64  `json:"durationMs,omitempty"`
	Error    string `json:"error,omitempty"`
	// Libraries reports whether vendor/ is indexed.
	Libraries bool `json:"libraries"`
}

// StatusParams are the params of index.status and index.rebuild.
type StatusParams struct {
	// Wait blocks until the index is ready (or the call is cancelled).
	Wait bool `json:"wait,omitempty"`
	// Full (index.rebuild) drops the index and the cache first.
	Full bool `json:"full,omitempty"`
}

// UpdatedEvent is the payload of index.updated.
type UpdatedEvent struct {
	// Files lists changed or removed files (display paths), capped at
	// 1000 (Truncated).
	Files      []string `json:"files"`
	Truncated  bool     `json:"truncated,omitempty"`
	Generation uint64   `json:"generation"`
	// Reason: "sync", "file", "doc", "cache".
	Reason string `json:"reason"`
}

func unavailable() error {
	return api.Errorf(api.CodeUnavailable, "php-index: built without Tree-sitter (cgo disabled)")
}

func (m *Module) status(ctx context.Context, p StatusParams) (Status, error) {
	if !index.TreeSitter {
		return Status{State: "unavailable"}, unavailable()
	}
	st, err := m.state()
	if err != nil {
		return Status{}, err
	}
	st.start()
	if p.Wait {
		if err := st.waitReady(ctx); err != nil {
			return Status{}, err
		}
	}
	return st.status(), nil
}

func (m *Module) rebuild(ctx context.Context, p StatusParams) (Status, error) {
	if !index.TreeSitter {
		return Status{State: "unavailable"}, unavailable()
	}
	st, err := m.state()
	if err != nil {
		return Status{}, err
	}
	st.start()
	done := st.requestSync(p.Full)
	if p.Wait {
		select {
		case <-done:
		case <-ctx.Done():
			return Status{}, ctx.Err()
		}
	}
	return st.status(), nil
}

// Config is what the index covers (project settings).
type Config struct {
	// Libraries indexes vendor/ (default true).
	Libraries bool `json:"libraries"`
	// IncludeIgnored indexes files matched by .gitignore (default false).
	IncludeIgnored bool `json:"includeIgnored"`
	// Exclude lists path.Match patterns (relative path or base name).
	Exclude []string `json:"exclude"`
}

// ConfigParams are the params of index.config; unset fields stay as they
// are. Changes are stored in the project settings and restart the index.
type ConfigParams struct {
	Libraries      *bool     `json:"libraries,omitempty"`
	IncludeIgnored *bool     `json:"includeIgnored,omitempty"`
	Exclude        *[]string `json:"exclude,omitempty"`
}

func (m *Module) config(_ context.Context, p ConfigParams) (Config, error) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app == nil {
		return Config{}, api.Errorf(api.CodeModuleDisabled, "php-index stopped")
	}
	if _, err := app.RequireProject(); err != nil {
		return Config{}, err
	}
	store := app.SettingsStore()
	changed := false
	set := func(key string, v any) error {
		changed = true
		if err := settings.Set(store, key, v); err != nil {
			return api.Errorf(api.CodeFailed, "index.config: %w", err)
		}
		return nil
	}
	if p.Libraries != nil {
		if err := set(SettingLibraries, *p.Libraries); err != nil {
			return Config{}, err
		}
	}
	if p.IncludeIgnored != nil {
		if err := set(SettingIncludeIgnored, *p.IncludeIgnored); err != nil {
			return Config{}, err
		}
	}
	if p.Exclude != nil {
		if err := set(SettingExclude, *p.Exclude); err != nil {
			return Config{}, err
		}
	}
	if changed {
		m.restart()
	}
	return m.effectiveConfig(app), nil
}

// effectiveConfig reads the declared settings (project over global).
func (m *Module) effectiveConfig(app *api.App) Config {
	return Config{
		Libraries:      api.Setting(app, SettingLibraries, true),
		IncludeIgnored: api.Setting(app, SettingIncludeIgnored, true),
		Exclude:        append([]string{}, api.Setting(app, SettingExclude, DefaultExclude)...),
	}
}

// onSetting restarts the index when one of its settings changed from
// outside index.config (e.g. the settings dialog via settings.set).
func (m *Module) onSetting(c settings.Change) {
	if !strings.HasPrefix(c.Key, ID+".") {
		return
	}
	m.mu.Lock()
	app, st, cfg := m.app, m.st, m.cfg
	m.mu.Unlock()
	if app == nil || st == nil {
		return
	}
	// Only changes for the project the index belongs to (or global ones)
	// while that project is open; events arrive asynchronously.
	root := app.ProjectRoot()
	if root == "" || st.root != root || (c.Scope == settings.ScopeProject && c.Project != root) {
		return
	}
	if reflect.DeepEqual(cfg, m.effectiveConfig(app)) {
		return
	}
	m.mu.Lock()
	same := m.st == st
	m.mu.Unlock()
	if same {
		m.restart()
	}
}

// PosParams address a position in a document or file.
type PosParams struct {
	Path string  `json:"path"`
	At   api.Pos `json:"at"`
	// Source selects the providers: "auto" (index, plus the language
	// server if it runs; default), "index" or "lsp".
	Source string `json:"source,omitempty"`
	// Wait waits for the initial indexing (otherwise results may be
	// incomplete while indexing; Complete tells).
	Wait bool `json:"wait,omitempty"`
}

// NavResult is the result of the position based nav.* methods.
type NavResult struct {
	Locations []Location `json:"locations"`
	// Complete is false while the index is still being built.
	Complete bool `json:"complete"`
	// LSP reports whether the language server answered; LSPError holds
	// its error.
	LSP      bool   `json:"lsp,omitempty"`
	LSPError string `json:"lspError,omitempty"`
}

// source returns the text and absolute path of p.Path: the open document
// or the file.
func (m *Module) source(p PosParams) (string, []byte, int, error) {
	if p.Path == "" {
		return "", nil, 0, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	abs, err := m.app.ResolvePath(p.Path)
	if err != nil {
		return "", nil, 0, err
	}
	text, _, err := m.app.ReadText(abs)
	if err != nil {
		return "", nil, 0, err
	}
	off, err := p.At.ByteOffset(text)
	if err != nil {
		return "", nil, 0, err
	}
	return abs, text, off, nil
}

func (m *Module) ready(ctx context.Context, wait bool) (*state, error) {
	if !index.TreeSitter {
		return nil, unavailable()
	}
	st, err := m.state()
	if err != nil {
		return nil, err
	}
	st.start()
	if wait {
		if err := st.waitReady(ctx); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func checkSource(s string) (string, error) {
	switch s {
	case "", "auto":
		return "auto", nil
	case "index", "lsp":
		return s, nil
	}
	return "", api.Errorf(api.CodeInvalidParams, "source must be auto, index or lsp, got %q", s)
}

type posQuery func(ix *index.Index, path string, src []byte, off int) ([]index.Location, error)

// navigate runs an index query and merges it with the language server's
// answer (lspKind "" = index only).
func (m *Module) navigate(ctx context.Context, p PosParams, lspKind string, q posQuery) (NavResult, error) {
	source, err := checkSource(p.Source)
	if err != nil {
		return NavResult{}, err
	}
	if lspKind == "" && source == "lsp" {
		return NavResult{}, api.Errorf(api.CodeInvalidParams, "source lsp is not supported here")
	}
	res := NavResult{Locations: []Location{}}
	abs, src, off, err := m.source(p)
	if err != nil {
		return res, err
	}
	if source != "lsp" {
		st, err := m.ready(ctx, p.Wait)
		if err != nil {
			return res, err
		}
		st.flushDoc(ctx, abs)
		locs, err := q(st.ix, abs, src, off)
		if err != nil {
			return res, api.Errorf(api.CodeFailed, "php-index: %w", err)
		}
		res.Locations = m.locations(locs)
		res.Complete = st.isReady()
	} else {
		res.Complete = true
	}
	if source == "index" || lspKind == "" {
		return res, nil
	}
	lq, err := api.HookNavLSP.Apply(ctx, m.app.Hooks(), api.NavLSPQuery{Kind: lspKind, Path: abs, At: p.At})
	if err != nil {
		res.LSPError = err.Error()
	}
	res.LSP, res.LSPError = lq.Answered, firstNonEmpty(res.LSPError, lq.Err)
	if source == "lsp" && !lq.Answered && res.LSPError == "" {
		res.LSPError = "language server not running"
	}
	res.Locations = merge(res.Locations, lq.Locations)
	return res, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// merge adds the LSP locations to the index locations; a location on the
// same line of the same file as an index result (its name or its
// declaration start) marks that result "index+lsp" instead.
func merge(idx []Location, lsp []api.NavLSPLocation) []Location {
	for _, l := range lsp {
		dup := false
		for i := range idx {
			x := &idx[i]
			if x.File != l.File {
				continue
			}
			line := l.Range.Start.Line
			if x.Range.Start.Line == line || x.DeclRange != nil && x.DeclRange.Start.Line <= line && line <= x.Range.Start.Line {
				if x.Source == "index" {
					x.Source = "index+lsp"
				}
				dup = true
				break
			}
		}
		if !dup {
			idx = append(idx, Location{Path: l.Path, File: l.File, Range: l.Range, Source: "lsp"})
		}
	}
	return idx
}

func (m *Module) definition(ctx context.Context, p PosParams) (NavResult, error) {
	return m.navigate(ctx, p, "definition", (*index.Index).Definition)
}

func (m *Module) implementations(ctx context.Context, p PosParams) (NavResult, error) {
	return m.navigate(ctx, p, "implementation", (*index.Index).Implementations)
}

func (m *Module) super(ctx context.Context, p PosParams) (NavResult, error) {
	return m.navigate(ctx, p, "", (*index.Index).Super)
}

// HierarchyParams are the params of nav.typeHierarchy: a class FQN, or a
// position (the class referenced there or around it).
type HierarchyParams struct {
	FQN  string   `json:"fqn,omitempty"`
	Path string   `json:"path,omitempty"`
	At   *api.Pos `json:"at,omitempty"`
	Wait bool     `json:"wait,omitempty"`
}

// HierarchyNode is one type of the hierarchy; Children are its supertypes
// (in Supertypes) or subtypes (in Subtypes). Types that are not indexed
// (e.g. PHP's built-in interfaces) have only FQN and Name.
type HierarchyNode struct {
	Location
	Children []HierarchyNode `json:"children,omitempty"`
}

// HierarchyResult is the result of nav.typeHierarchy.
type HierarchyResult struct {
	Class      Location        `json:"class"`
	Supertypes []HierarchyNode `json:"supertypes"`
	Subtypes   []HierarchyNode `json:"subtypes"`
	Truncated  bool            `json:"truncated,omitempty"`
	Complete   bool            `json:"complete"`
}

func (m *Module) nodes(in []index.HierarchyNode) []HierarchyNode {
	out := make([]HierarchyNode, 0, len(in))
	for _, n := range in {
		out = append(out, HierarchyNode{Location: m.location(n.Location), Children: m.nodes(n.Children)})
	}
	return out
}

func (m *Module) typeHierarchy(ctx context.Context, p HierarchyParams) (HierarchyResult, error) {
	st, err := m.ready(ctx, p.Wait)
	if err != nil {
		return HierarchyResult{}, err
	}
	fqn := p.FQN
	if fqn == "" {
		if p.Path == "" || p.At == nil {
			return HierarchyResult{}, api.Errorf(api.CodeInvalidParams, "fqn or path and at are required")
		}
		abs, src, off, err := m.source(PosParams{Path: p.Path, At: *p.At})
		if err != nil {
			return HierarchyResult{}, err
		}
		if fqn, err = st.ix.ClassAt(abs, src, off); err != nil {
			return HierarchyResult{}, api.Errorf(api.CodeFailed, "php-index: %w", err)
		}
		if fqn == "" {
			return HierarchyResult{}, api.Errorf(api.CodeNotFound, "no class at %s", p.Path)
		}
	}
	h, ok := st.ix.TypeHierarchy(fqn)
	if !ok {
		return HierarchyResult{}, api.Errorf(api.CodeNotFound, "class %s is not indexed", fqn)
	}
	return HierarchyResult{
		Class: m.location(h.Class), Supertypes: m.nodes(h.Supertypes), Subtypes: m.nodes(h.Subtypes),
		Truncated: h.Truncated, Complete: st.isReady(),
	}, nil
}

// SymbolsParams are the params of nav.symbols.
type SymbolsParams struct {
	// Query is matched fuzzily against short names; with a backslash
	// against FQNs; "Class::member" searches members.
	Query string `json:"query"`
	// Kinds restricts the result: class, interface, trait, enum, method,
	// property, classConstant, enumCase, function, constant; "type" is
	// short for the four class-like kinds (Go to Class).
	Kinds []string `json:"kinds,omitempty"`
	// Limit caps the result (default 100, at most 10000).
	Limit int `json:"limit,omitempty"`
	// Libraries includes vendor symbols (default true; ranked after
	// project symbols).
	Libraries *bool `json:"libraries,omitempty"`
	Wait      bool  `json:"wait,omitempty"`
}

// SymbolMatch is one result of nav.symbols.
type SymbolMatch struct {
	Location
	Score int `json:"score"`
}

// SymbolsResult is the result of nav.symbols.
type SymbolsResult struct {
	Symbols  []SymbolMatch `json:"symbols"`
	Complete bool          `json:"complete"`
}

func (m *Module) symbols(ctx context.Context, p SymbolsParams) (SymbolsResult, error) {
	q := index.SymbolQuery{Query: p.Query, Limit: min(p.Limit, 10000), NoLibraries: p.Libraries != nil && !*p.Libraries}
	for _, k := range p.Kinds {
		if strings.EqualFold(k, "type") {
			q.Kinds = append(q.Kinds, index.KindClass, index.KindInterface, index.KindTrait, index.KindEnum)
			continue
		}
		kind, ok := index.ParseKind(k)
		if !ok {
			return SymbolsResult{}, api.Errorf(api.CodeInvalidParams, "unknown kind %q", k)
		}
		q.Kinds = append(q.Kinds, kind)
	}
	st, err := m.ready(ctx, p.Wait)
	if err != nil {
		return SymbolsResult{}, err
	}
	res := SymbolsResult{Symbols: []SymbolMatch{}, Complete: st.isReady()}
	for _, s := range st.ix.Symbols(q) {
		res.Symbols = append(res.Symbols, SymbolMatch{Location: m.location(s.Location), Score: s.Score})
	}
	return res, nil
}

// GutterParams are the params of nav.gutter.
type GutterParams struct {
	Path string `json:"path"`
	Wait bool   `json:"wait,omitempty"`
}

// GutterMark is one gutter entry: the declaration at Line overrides or
// implements Up and is overridden or implemented by Down.
type GutterMark struct {
	Line  int           `json:"line"`
	Kind  string        `json:"kind"`
	FQN   string        `json:"fqn"`
	Range api.TextRange `json:"range"`
	Up    []Location    `json:"up,omitempty"`
	Down  []Location    `json:"down,omitempty"`
	// UpTotal/DownTotal count all targets (the lists are capped at 100).
	UpTotal   int `json:"upTotal,omitempty"`
	DownTotal int `json:"downTotal,omitempty"`
}

// GutterResult is the result of nav.gutter.
type GutterResult struct {
	Path     string       `json:"path"`
	Marks    []GutterMark `json:"marks"`
	Complete bool         `json:"complete"`
}

func (m *Module) gutter(ctx context.Context, p GutterParams) (GutterResult, error) {
	if p.Path == "" {
		return GutterResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	st, err := m.ready(ctx, p.Wait)
	if err != nil {
		return GutterResult{}, err
	}
	abs, err := m.app.ResolvePath(p.Path)
	if err != nil {
		return GutterResult{}, err
	}
	// an open document with pending edits is indexed first, so the lines
	// match what the client shows
	st.flushDoc(ctx, abs)
	res := GutterResult{Path: m.app.DisplayPath(abs), Marks: []GutterMark{}, Complete: st.isReady()}
	for _, g := range st.ix.Gutter(abs) {
		res.Marks = append(res.Marks, GutterMark{
			Line: g.Line, Kind: g.Kind.String(), FQN: g.FQN, Range: textRange(g.NameRange),
			Up: m.locations(g.Up), Down: m.locations(g.Down), UpTotal: g.UpTotal, DownTotal: g.DownTotal,
		})
	}
	return res, nil
}
