// Package phpfeatures is the php-features module: it checks PHP documents
// against the project's PHP version (features that are too new,
// deprecations, removals) using the feature table of internal/phpver.
//
// Open PHP documents are checked automatically in the background: every
// buffer change (doc.edited, from the API or the GUI) updates a
// syntax.Document incrementally, and after a short pause the document is
// reparsed and checked; the findings are published as php.diagnostics.
//
// API: php.check, php.diagnostics, php.setVersionOverride.
// The PHP version comes from the php.resolveVersion hook (php-runtime).
package phpfeatures

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/phpver"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// ID is the module ID.
const ID = "php-features"

// DefaultDebounce is the pause after the last change before a document is
// checked.
const DefaultDebounce = 300 * time.Millisecond

// Options configure the module.
type Options struct {
	// Debounce overrides DefaultDebounce (tests use a few milliseconds).
	Debounce time.Duration
}

// Module is the php-features module.
type Module struct {
	opts Options

	mu      sync.Mutex
	app     *api.App
	ctx     context.Context
	cancel  context.CancelFunc
	checker *phpver.Checker
	ready   chan struct{}       // closed once the checker is compiled (or failed)
	docs    map[string]*tracked // by absolute path
	diags   map[string]Diagnostics
	version *resolved
	wg      sync.WaitGroup
}

type resolved struct {
	v      phpver.Version
	source string
}

// tracked is the incremental parse state of one open PHP document.
type tracked struct {
	file    string
	src     []byte
	version uint64
	doc     *syntax.Document
	timer   *time.Timer
	dirty   bool // edits since the last reparse
}

// New creates the module.
func New(opts Options) *Module {
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	return &Module{opts: opts}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return []string{"php-runtime"} }

// Claims implements api.Claimer.
func (m *Module) Claims() []string {
	return []string{"php.check", "php.diagnostics", "php.setVersionOverride"}
}

// Start registers the API and starts tracking open documents.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("php-features: needs an api.App context")
	}
	// Without Tree-sitter (built without cgo, D80) the module still runs
	// for php.setVersionOverride; checks report CodeUnavailable.
	var table *phpver.Table
	if syntax.TreeSitter {
		var err error
		if table, err = phpver.DefaultTable(); err != nil {
			return err
		}
	}
	c, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	m.mu.Lock()
	m.app, m.ctx, m.cancel, m.checker, m.ready = app, c, cancel, nil, ready
	m.docs = map[string]*tracked{}
	m.diags = map[string]Diagnostics{}
	m.version = nil
	m.mu.Unlock()
	// Compiling the combined feature query takes ~0.5 s; it must not delay
	// the start of the application (GUI first frame), so it runs in the
	// background and checks wait for it (waitChecker).
	if table == nil {
		close(ready)
	} else {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer close(ready)
			checker, err := phpver.NewChecker(table)
			if err != nil {
				app.Logger().Warn("php-features: checker", "err", err)
				return
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if c.Err() != nil {
				checker.Close() // stopped meanwhile
				return
			}
			m.checker = checker
		}()
	}

	p := api.NewProvider(ctx)
	api.Add(p, "php.check", "Check a PHP file against the project's PHP version (features, deprecations)", m.check)
	api.Add(p, "php.diagnostics", "Latest background feature diagnostics of an open document", m.diagnostics)
	api.Add(p, "php.setVersionOverride", "Set or clear (empty) the persistent PHP version override", m.setOverride)
	if err := p.Err(); err != nil {
		return err
	}
	b := ctx.Bus()
	// One subscription for all doc.* events keeps them in publish order.
	ctx.OnStop(b.Subscribe("doc.*", m.onDoc))
	invalidate := func(bus.Event) { m.invalidate() }
	ctx.OnStop(b.Subscribe(api.TopicInterpreterChanged, invalidate))
	ctx.OnStop(b.Subscribe(api.TopicPHPVersionChanged, invalidate))
	ctx.OnStop(b.Subscribe(api.TopicProjectOpened, invalidate))
	for _, d := range app.Documents() {
		m.track(d)
	}
	return nil
}

// Stop cancels pending checks and frees the syntax trees.
func (m *Module) Stop() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, t := range m.docs {
		m.dropLocked(t)
	}
	m.docs = map[string]*tracked{}
	checker := m.checker
	m.checker = nil
	m.mu.Unlock()
	m.wg.Wait()
	if checker != nil {
		checker.Close()
	}
	return nil
}

func (m *Module) onDoc(e bus.Event) {
	switch ev := e.Payload.(type) {
	case api.DocState:
		if e.Topic == api.TopicDocOpened {
			if d, err := m.app.Document(ev.Path); err == nil {
				m.track(d.Snapshot())
			}
		}
	case api.DocEditEvent:
		m.edited(ev)
	case api.DocCloseResult:
		abs, err := m.app.ResolvePath(ev.Path)
		if err != nil {
			return
		}
		m.mu.Lock()
		if t, ok := m.docs[abs]; ok {
			m.dropLocked(t)
			delete(m.docs, abs)
			delete(m.diags, abs)
		}
		m.mu.Unlock()
	}
}

// track starts (or restarts) tracking a document from a snapshot.
func (m *Module) track(s api.DocSnapshot) {
	if s.Language != string(syntax.LangPHP) || !syntax.TreeSitter {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	if old, ok := m.docs[s.File]; ok {
		if old.version >= s.Version {
			return
		}
		m.dropLocked(old)
	}
	doc, err := syntax.NewDocument(m.ctx, syntax.LangPHP, s.Text)
	if err != nil {
		m.app.Logger().Warn("php-features: parse", "file", s.File, "err", err)
		return
	}
	t := &tracked{file: s.File, src: s.Text, version: s.Version, doc: doc}
	m.docs[s.File] = t
	m.scheduleLocked(t)
}

// edited applies one change incrementally.
func (m *Module) edited(ev api.DocEditEvent) {
	m.mu.Lock()
	t, ok := m.docs[ev.File]
	switch {
	case !ok || ev.Version <= t.version:
		m.mu.Unlock()
		return // not PHP, or already contained in the snapshot
	case ev.Version != t.version+1 || ev.OldEnd > len(t.src):
		m.mu.Unlock() // missed a change: start over from the buffer
		if d, err := m.app.Document(ev.File); err == nil {
			m.track(d.Snapshot())
		}
		return
	}
	src := make([]byte, 0, len(t.src)-(ev.OldEnd-ev.Start)+len(ev.Text))
	src = append(append(append(src, t.src[:ev.Start]...), ev.Text...), t.src[ev.OldEnd:]...)
	t.doc.Edit(syntax.InputEdit{
		StartByte: ev.Start, OldEndByte: ev.OldEnd, NewEndByte: ev.NewEnd,
		StartPoint:  syntax.Point{Line: ev.StartPoint.Line, Column: ev.StartPoint.Column},
		OldEndPoint: syntax.Point{Line: ev.OldEndPoint.Line, Column: ev.OldEndPoint.Column},
		NewEndPoint: syntax.Point{Line: ev.NewEndPoint.Line, Column: ev.NewEndPoint.Column},
	})
	t.src, t.version, t.dirty = src, ev.Version, true
	m.scheduleLocked(t)
	m.mu.Unlock()
}

func (m *Module) dropLocked(t *tracked) {
	if t.timer != nil {
		t.timer.Stop()
	}
	if t.doc != nil {
		t.doc.Close()
		t.doc = nil
	}
}

// scheduleLocked (re)starts the debounce timer of t.
func (m *Module) scheduleLocked(t *tracked) {
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(m.opts.Debounce, func() {
		m.mu.Lock()
		if m.ctx == nil || m.ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		m.wg.Add(1) // under mu: Stop cancels under mu before Wait
		m.mu.Unlock()
		defer m.wg.Done()
		m.runCheck(t)
	})
}

// invalidate forgets the cached version and re-checks all documents.
func (m *Module) invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.version = nil
	for _, t := range m.docs {
		m.scheduleLocked(t)
	}
}

// phpVersion returns the cached version or resolves it through the
// php.resolveVersion hook.
func (m *Module) phpVersion(ctx context.Context) (*resolved, error) {
	m.mu.Lock()
	v := m.version
	app := m.app
	m.mu.Unlock()
	if v != nil {
		return v, nil
	}
	q, _ := api.HookPHPVersion.Apply(ctx, app.Hooks(), api.PHPVersionQuery{})
	if q.Version == "" {
		return nil, api.Errorf(api.CodeNotFound, "PHP version unknown (select an interpreter, set config.platform.php or require.php, or php.setVersionOverride)")
	}
	pv, err := phpver.ParseVersion(q.Version)
	if err != nil {
		return nil, api.Errorf(api.CodeFailed, "php version %q: %w", q.Version, err)
	}
	r := &resolved{v: pv, source: q.Source}
	m.mu.Lock()
	m.version = r
	m.mu.Unlock()
	return r, nil
}

// runCheck reparses t if needed, checks it and publishes the findings.
// waitChecker waits until the background compilation of the checker has
// finished.
func (m *Module) waitChecker(ctx context.Context) error {
	m.mu.Lock()
	ready := m.ready
	m.mu.Unlock()
	if ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) runCheck(t *tracked) {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || m.waitChecker(ctx) != nil {
		return
	}
	v, err := m.phpVersion(ctx)
	if err != nil {
		m.app.Logger().Debug("php-features: no version", "err", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs[t.file] != t || t.doc == nil || m.checker == nil {
		return // closed or replaced meanwhile
	}
	if t.dirty {
		if err := t.doc.Reparse(ctx, t.src); err != nil {
			return
		}
		t.dirty = false
	}
	found, err := m.checker.Check(t.doc, v.v)
	if err != nil {
		m.app.Logger().Warn("php-features: check", "file", t.file, "err", err)
		return
	}
	ev := Diagnostics{
		Path: m.app.DisplayPath(t.file), File: t.file, Version: t.version,
		PHPVersion: v.v.String(), Source: v.source, Diagnostics: convert(t.src, found),
	}
	m.diags[t.file] = ev
	m.app.Bus().Publish(api.TopicPHPDiagnostics, ev)
}

// Diagnostic is a feature or deprecation finding. Line and Col are 1-based
// (Col in characters); Start and End are byte offsets.
type Diagnostic struct {
	Feature   string `json:"feature"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	Highlight string `json:"text"`
}

// Diagnostics is the payload of php.diagnostics and the result of the
// "php.diagnostics" method.
type Diagnostics struct {
	Path string `json:"path"`
	File string `json:"file"`
	// Version is the document version the findings belong to.
	Version     uint64       `json:"version"`
	PHPVersion  string       `json:"phpVersion"`
	Source      string       `json:"source"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func convert(src []byte, found []phpver.Diagnostic) []Diagnostic {
	out := []Diagnostic{}
	if len(found) == 0 {
		return out
	}
	buf := buffer.New(src)
	for _, d := range found {
		r := d.Range
		out = append(out, Diagnostic{
			Feature: d.FeatureID, Severity: d.Severity.String(), Message: d.Message,
			Start: r.StartByte, End: r.EndByte,
			Line: r.Start.Line + 1, Col: buffer.RuneCol(buf.Line(r.Start.Line), r.Start.Column) + 1,
			EndLine: r.End.Line + 1, EndCol: buffer.RuneCol(buf.Line(r.End.Line), r.End.Column) + 1,
			Highlight: string(src[r.StartByte:r.EndByte]),
		})
	}
	return out
}

// CheckParams are the params of "php.check". The open document is checked
// if there is one, otherwise the file on disk.
type CheckParams struct {
	Path string `json:"path"`
	// Version to check against; empty means the project version.
	Version string `json:"version,omitempty"`
}

// CheckResult is the result of "php.check".
type CheckResult struct {
	Path        string       `json:"path"`
	Version     string       `json:"version"`
	Source      string       `json:"source"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (m *Module) check(ctx context.Context, p CheckParams) (CheckResult, error) {
	if !syntax.TreeSitter {
		return CheckResult{}, api.Errorf(api.CodeUnavailable, "php.check: %w", syntax.ErrNoTreeSitter)
	}
	abs, err := m.app.ResolvePath(p.Path)
	if err != nil {
		return CheckResult{}, err
	}
	src, _, err := m.app.ReadText(abs)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{Path: m.app.DisplayPath(abs), Diagnostics: []Diagnostic{}}
	var v phpver.Version
	if p.Version != "" {
		if v, err = phpver.ParseVersion(p.Version); err != nil {
			return res, api.Errorf(api.CodeInvalidParams, "version: %w", err)
		}
		res.Source = "param"
	} else {
		r, err := m.phpVersion(ctx)
		if err != nil {
			return res, err
		}
		v, res.Source = r.v, r.source
	}
	res.Version = v.String()
	doc, err := syntax.NewDocument(ctx, syntax.LangPHP, src)
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "parse: %w", err)
	}
	defer doc.Close()
	if err := m.waitChecker(ctx); err != nil {
		return res, api.Errorf(api.CodeFailed, "check: %w", err)
	}
	m.mu.Lock()
	checker := m.checker
	var found []phpver.Diagnostic
	if checker != nil {
		found, err = checker.Check(doc, v)
	}
	m.mu.Unlock()
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "check: %w", err)
	}
	res.Diagnostics = convert(src, found)
	return res, nil
}

func (m *Module) diagnostics(ctx context.Context, p api.PathParams) (Diagnostics, error) {
	if !syntax.TreeSitter {
		return Diagnostics{}, api.Errorf(api.CodeUnavailable, "php.diagnostics: %w", syntax.ErrNoTreeSitter)
	}
	abs, err := m.app.ResolvePath(p.Path)
	if err != nil {
		return Diagnostics{}, err
	}
	m.mu.Lock()
	t, tracked := m.docs[abs]
	d, have := m.diags[abs]
	m.mu.Unlock()
	if !tracked {
		return Diagnostics{}, api.Errorf(api.CodeNotFound, "%s is not an open PHP document (use doc.open or php.check)", p.Path)
	}
	if have && d.Version == t.version {
		return d, nil
	}
	m.runCheck(t) // not checked yet (or stale): check now
	m.mu.Lock()
	d, have = m.diags[abs]
	m.mu.Unlock()
	if !have {
		_, err := m.phpVersion(ctx)
		if err == nil {
			err = api.Errorf(api.CodeFailed, "no diagnostics for %s", p.Path)
		}
		return Diagnostics{}, err
	}
	return d, nil
}

// OverrideParams are the params of "php.setVersionOverride".
type OverrideParams struct {
	// Version like "8.2"; empty removes the override.
	Version string `json:"version"`
}

// OverrideResult is the result of "php.setVersionOverride".
type OverrideResult struct {
	Override string `json:"override"`
}

func (m *Module) setOverride(_ context.Context, p OverrideParams) (OverrideResult, error) {
	store := m.app.SettingsStore()
	if p.Version == "" {
		if err := store.Delete(api.SettingPHPVersionOverride); err != nil {
			return OverrideResult{}, api.Errorf(api.CodeFailed, "override: %w", err)
		}
	} else {
		v, err := phpver.ParseVersion(p.Version)
		if err != nil {
			return OverrideResult{}, api.Errorf(api.CodeInvalidParams, "version: %w", err)
		}
		if err := settings.Set(store, api.SettingPHPVersionOverride, v.Short()); err != nil {
			return OverrideResult{}, api.Errorf(api.CodeFailed, "override: %w", err)
		}
	}
	res := OverrideResult{Override: settings.Get(store, api.SettingPHPVersionOverride, "")}
	m.app.Bus().Publish(api.TopicPHPVersionChanged, api.PHPVersionChangedEvent{Override: res.Override})
	return res, nil
}
