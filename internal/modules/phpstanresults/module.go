// Package phpstanresults is the PHPStan results plugin (module ID
// "phpstan", D556): it keeps the PHPStan findings of the project without
// running PHPStan. They come from a report file (error formats json,
// checkstyle, junit; setting phpstan.reportFile or phpstan.import) or from
// PHPStan's result cache of the last command line run (located through
// tmpDir/resultCachePath of the PHPStan configuration, parsed statically,
// ignoreErrors and baselines applied). Paths from a container are mapped
// with the interpreter's path mappings; findings in files changed after
// the source was written are marked stale, findings on lines that no
// longer exist are dropped. Tiegel's own PHPStan runs (QA tool
// qa-phpstan) are folded in.
//
// The store is refreshed on demand (phpstan.reload, phpstan.import), when
// the source file changes (files.changed), when the window gets the focus
// (watch.refreshed: one stat of a cache outside the project) and after
// Tiegel's own PHPStan runs. "Fix with AI" (phpstan.fix) sends a finding
// with context to the AI agent plugin (agent.prompt) – only offered when
// that plugin runs and its program is installed.
//
// API (namespace phpstan.*): phpstan.status, phpstan.findings,
// phpstan.import, phpstan.reload, phpstan.fix; event phpstan.changed.
// Settings: phpstan.reportFile, phpstan.config, phpstan.useResultCache,
// phpstan.inline.
package phpstanresults

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/phpstan"
)

// ID is the module ID.
const ID = "phpstan"

// qaTool is the QA tool plugin whose runs the module folds into its
// findings (and whose own diagnostics clients drop while it runs).
const qaTool = "qa-phpstan"

// Settings keys (D190: namespace = module ID).
const (
	SettingReportFile = ID + ".reportFile"
	SettingConfig     = ID + ".config"
	SettingUseCache   = ID + ".useResultCache"
	SettingInline     = ID + ".inline"
)

// debounce delays reloads after file events (PHPStan writes its cache
// once at the end, a report may arrive in pieces).
var debounce = 300 * time.Millisecond

// Module is the PHPStan results plugin.
type Module struct {
	app atomic.Pointer[api.App]
	bus atomic.Pointer[bus.Bus]

	// loadMu serializes loads.
	loadMu sync.Mutex

	mu sync.Mutex
	st state
	// timer debounces file events; pendingLoad/pendingFiles are what it
	// will do.
	timer        *time.Timer
	pendingLoad  bool
	pendingFiles map[string]bool
	stopped      bool
	// agent is the last known agent availability (from agent.status);
	// agentMu orders the checks (each asks and stores as one step).
	agent   agentState
	agentMu sync.Mutex
}

type agentState struct {
	known, available bool
	reason           string
}

// New creates the module.
func New() *Module { return &Module{} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module: the QA tool and the agent plugin
// are optional and reached through the bus and the API only.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"phpstan.*"} }

// SettingsPage implements module.PageProvider.
func (m *Module) SettingsPage() (section, title string) { return "Quality Tools", "PHPStan Results" }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingInline, Kind: settings.KindBool, Default: true, Scope: settings.ScopeGlobal, Label: "Show PHPStan results inline", Group: "Display",
			Help: "Mark the lines PHPStan reported with a thin dotted underline and a dot in the gutter; hover shows the message. The Problems window lists them either way."},
		{Key: SettingUseCache, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Read PHPStan's result cache", Group: "Source",
			Help: "Show the errors of your last PHPStan run from its result cache (tmpDir of the configuration) – PHPStan is never started for this. A report file below takes precedence."},
		{Key: SettingReportFile, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Report file", Group: "Source",
			Help: "A report PHPStan wrote with --error-format=json, checkstyle or junit (e.g. build/phpstan.xml from CI). Empty: the result cache."},
		{Key: SettingConfig, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "PHPStan configuration", Group: "Source",
			Help: "Where tmpDir, resultCachePath and ignoreErrors (baseline) are read; empty: phpstan.neon(.dist) in the PHP root."},
	}
}

// Start registers the API and the event subscriptions.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("phpstan: needs an api.App context")
	}
	m.app.Store(app)
	m.bus.Store(ctx.Bus())
	m.mu.Lock()
	m.stopped = false
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "analysis.status", "PHPStan results: sources (report files, result cache) with the one in use, counts, stale findings, inline setting, Fix with AI availability", m.status)
	api.Add(p, "analysis.findings", "PHPStan findings per file (optionally of one file or folder), with stale marks and editor ranges", m.findings)
	api.Add(p, "analysis.import", "Read a PHPStan report (json, checkstyle, junit) or result cache file now and keep it as the source", m.importFile)
	api.Add(p, "analysis.reload", "Read the PHPStan source again (ifChanged: only when its file changed; reset: forget an imported file)", m.reload)
	api.Add(p, "analysis.fix", "Ask the AI agent to fix the PHPStan finding(s) of a line (dryRun: only build the prompt)", m.fix)
	if err := p.Err(); err != nil {
		return err
	}
	if err := m.provideCandidates(ctx); err != nil {
		return err
	}
	b := ctx.Bus()
	ctx.OnStop(b.Subscribe(api.TopicProjectOpened, func(bus.Event) { m.schedule(true, nil, 0) }))
	ctx.OnStop(b.Subscribe(api.TopicProjectClosed, func(bus.Event) { m.clear() }))
	ctx.OnStop(b.Subscribe(api.TopicPHPRootChanged, func(bus.Event) { m.schedule(true, nil, 0) }))
	ctx.OnStop(b.Subscribe(api.TopicWatchRefreshed, func(bus.Event) { m.reloadIfChanged() }))
	ctx.OnStop(b.Subscribe(api.TopicFilesChanged, m.onFilesChanged))
	ctx.OnStop(b.Subscribe(api.TopicFileSaved, m.onSaved))
	ctx.OnStop(b.Subscribe(api.TopicQAFinished, m.onQAFinished))
	ctx.OnStop(b.Subscribe(string(settings.Changed), m.onSetting))
	ctx.OnStop(b.Subscribe(api.TopicModuleChanged, func(bus.Event) { m.agentChanged(nil) }))
	// Events may arrive after the plugin was disabled: always ask.
	ctx.OnStop(b.Subscribe(api.TopicAgentStatus, func(bus.Event) { m.agentChanged(nil) }))
	ctx.OnStop(b.Subscribe("install.done", func(bus.Event) { m.agentChanged(nil) }))
	if app.ProjectRoot() != "" {
		m.schedule(true, nil, 0)
	}
	return nil
}

// Stop implements module.Module: the findings go with the module.
func (m *Module) Stop() error {
	m.mu.Lock()
	m.stopped = true
	if m.timer != nil {
		m.timer.Stop()
	}
	m.mu.Unlock()
	m.clear()
	return nil
}

func (m *Module) application() *api.App { return m.app.Load() }

func (m *Module) publish(topic string, v any) {
	if b := m.bus.Load(); b != nil {
		b.Publish(topic, v)
	}
}

// setting reads a setting (project before global).
func setting[T any](m *Module, key string, def T) T {
	app := m.application()
	if app == nil {
		return def
	}
	return api.Setting(app, key, def)
}

// schedule plans a reload (load) and/or a refresh of files after delay.
func (m *Module) schedule(load bool, files []string, delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	m.pendingLoad = m.pendingLoad || load
	for _, f := range files {
		if m.pendingFiles == nil {
			m.pendingFiles = map[string]bool{}
		}
		m.pendingFiles[f] = true
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(delay, m.runPending)
}

func (m *Module) runPending() {
	m.mu.Lock()
	load, files := m.pendingLoad, m.pendingFiles
	m.pendingLoad, m.pendingFiles = false, nil
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return
	}
	ctx := context.Background()
	if load {
		_, _ = m.load(ctx, loadOpts{})
		return
	}
	if len(files) > 0 {
		m.refreshFiles(slices.Sorted(mapKeys(files)))
	}
}

func mapKeys(m map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// reloadIfChanged checks the source's mtime (watch.refreshed).
func (m *Module) reloadIfChanged() {
	go func() { _, _ = m.load(context.Background(), loadOpts{ifChanged: true}) }()
}

func (m *Module) onFilesChanged(e bus.Event) {
	ev, ok := e.Payload.(api.FilesChangedEvent)
	if !ok {
		return
	}
	m.mu.Lock()
	src := ""
	if m.st.used != nil {
		src = m.st.used.File
	}
	var files []string
	load := false
	for _, c := range ev.Changes {
		for _, f := range []string{c.File, c.OldFile} {
			switch {
			case f == "":
			case f == src:
				load = true
			case m.st.files[f] != nil:
				files = append(files, f)
			}
		}
	}
	m.mu.Unlock()
	if load || len(files) > 0 {
		m.schedule(load, files, debounce)
	}
}

func (m *Module) onSaved(e bus.Event) {
	ev, ok := e.Payload.(api.FileSavedEvent)
	if !ok {
		return
	}
	m.mu.Lock()
	has := m.st.files[ev.File] != nil
	m.mu.Unlock()
	if has {
		m.schedule(false, []string{ev.File}, debounce)
	}
}

func (m *Module) onSetting(e bus.Event) {
	c, ok := e.Payload.(settings.Change)
	if !ok {
		return
	}
	switch c.Key {
	case SettingReportFile, SettingConfig, SettingUseCache, "qa-phpstan.config":
		m.schedule(true, nil, debounce)
	case SettingInline:
		m.publishStatus()
	}
}

// onQAFinished folds Tiegel's own PHPStan run into the findings and
// re-checks the cache PHPStan just wrote.
func (m *Module) onQAFinished(e bus.Event) {
	ev, ok := e.Payload.(api.QARunEvent)
	if !ok || ev.Tool != qaTool || ev.Kind != api.QAKindInspect || ev.Error != "" {
		return
	}
	go m.foldRun(context.Background(), ev.Paths)
}

// clear forgets the findings (project closed, module stopped).
func (m *Module) clear() {
	m.mu.Lock()
	had := len(m.st.files) > 0 || m.st.used != nil
	v := m.st.version + 1
	m.st = state{version: v}
	m.mu.Unlock()
	if had {
		m.publishStatus()
	}
}

// agentChanged updates the Fix with AI availability (st: the agent's
// status from its event; nil: ask).
func (m *Module) agentChanged(st *api.AgentStatus) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	var a agentState
	if st != nil {
		a = agentFrom(st, nil)
	} else {
		a = m.askAgent(context.Background())
	}
	m.mu.Lock()
	changed := m.agent != a
	m.agent = a
	m.mu.Unlock()
	if changed {
		m.publishStatus()
	}
}

// askAgent asks the agent plugin through the API (never imported).
func (m *Module) askAgent(ctx context.Context) agentState {
	app := m.application()
	if app == nil {
		return agentState{known: true, reason: "not started"}
	}
	var st api.AgentStatus
	err := app.CallLocal(ctx, "agent.status", nil, &st)
	return agentFrom(&st, err)
}

func agentFrom(st *api.AgentStatus, err error) agentState {
	switch {
	case err != nil:
		return agentState{known: true, reason: "The AI agent plugin is not enabled (Settings › Plugins)."}
	case !st.Available:
		return agentState{known: true, reason: "The AI agent (" + st.Command + ") is not installed (Settings › AI › Agent)."}
	}
	return agentState{known: true, available: true}
}

// agentNow returns the agent availability, asking once when unknown.
func (m *Module) agentNow(ctx context.Context) agentState {
	m.mu.Lock()
	a := m.agent
	m.mu.Unlock()
	if a.known {
		return a
	}
	a = m.askAgent(ctx)
	m.mu.Lock()
	m.agent = a
	m.mu.Unlock()
	return a
}

// provideCandidates lists report files and PHPStan configurations for
// the settings (D521): never used silently, the user picks.
func (m *Module) provideCandidates(ctx module.Context) error {
	if err := api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingReportFile}, Source: "PHPStan reports found in the project (build/, var/, reports/)",
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			var out []api.SettingCandidate
			for _, f := range findReports(q.Root, q.PHPRoot) {
				out = append(out, api.SettingCandidate{Value: f.rel, Label: f.rel, Detail: "PHPStan " + f.format + " report, " + f.mod.Format("2006-01-02 15:04")})
			}
			return out, nil
		}}); err != nil {
		return err
	}
	return api.ProvideCandidates(ctx, api.CandidateProvider{Keys: []string{SettingConfig}, Source: "PHPStan configuration files in the project",
		Find: func(_ context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			return api.ConfigCandidates(q, "PHPStan configuration", phpstan.ConfigFiles...), nil
		}})
}
