package phpunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// session is the test view of one run of type "phpunit": the run's
// output is fed into a TeamCity result tree.
type session struct {
	run   api.TestRun
	tree  *teamcity.Tree
	timer *time.Timer
	dirty bool
	done  bool
}

func (s *session) stopTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// onRun follows the run infrastructure's events (one subscription, so
// started → output → exited arrive in order).
func (m *Module) onRun(e bus.Event) {
	switch e.Topic {
	case api.TopicRunStarted:
		if i, ok := e.Payload.(run.Info); ok && i.Type == ID {
			m.ensure(i) // nil for runs of another project
		}
	case api.TopicRunOutput:
		o, ok := e.Payload.(run.Output)
		if !ok {
			return
		}
		m.mu.Lock()
		s := m.sessions[o.RunID]
		if s == nil || s.done {
			m.mu.Unlock()
			return
		}
		changed := false
		for _, l := range o.Lines {
			if l.Stream == run.StreamSystem {
				continue
			}
			if s.tree.Feed(l.Text) {
				changed = true
			}
		}
		if changed {
			m.scheduleLocked(s)
		}
		m.mu.Unlock()
	case api.TopicRunExited:
		if i, ok := e.Payload.(run.Info); ok && i.Type == ID {
			m.finish(i)
		}
	}
}

// ensure returns the session of run i, creating it on first sight; nil
// for a run of another project (one started before a project switch
// that ends after it, D381).
func (m *Module) ensure(i run.Info) *session {
	m.mu.Lock()
	if s := m.sessions[i.ID]; s != nil {
		m.mu.Unlock()
		return s
	}
	app := m.app
	m.mu.Unlock()
	if app != nil && !sameProject(i.Project, app.ProjectRoot()) {
		return nil
	}
	// The mapper may ask php-runtime (hook): outside the lock.
	root := i.Project
	// Relative arguments and reported paths are relative to the working
	// directory: the PHP project root (D515).
	dir := i.Spec.Dir
	if dir == "" || !filepath.IsAbs(dir) {
		dir = root
	}
	tree := teamcity.NewTree()
	tree.MapPath = m.mapper(app, root, dir, i.Spec.EffectiveInterpreter())
	m.mu.Lock()
	if s := m.sessions[i.ID]; s != nil { // created meanwhile
		m.mu.Unlock()
		return s
	}
	s := &session{tree: tree, run: api.TestRun{
		RunID: i.ID, Name: i.Name, Framework: Framework, State: i.State, Started: i.Started,
		Scope: scopeOfArgs(i.Spec.Args, dir), Debug: i.Spec.Debug, Project: root,
	}}
	m.sessions[i.ID] = s
	m.order = append(m.order, i.ID)
	for len(m.order) > m.opts.MaxRuns {
		old := m.order[0]
		m.order = m.order[1:]
		if o := m.sessions[old]; o != nil {
			o.stopTimer()
		}
		delete(m.sessions, old)
	}
	started := s.run
	m.mu.Unlock()
	if app != nil {
		app.Bus().Publish(api.TopicTestStarted, started)
	}
	return s
}

// sameProject reports whether a run of project runRoot belongs to the
// open project root (runs without project always do).
func sameProject(runRoot, root string) bool {
	return runRoot == "" || (root != "" && filepath.Clean(runRoot) == filepath.Clean(root))
}

// mapper maps reported (container) paths to host files: files that exist
// on the host stay, else the interpreter's path mappings apply, else the
// longest suffix that exists below the project root.
func (m *Module) mapper(app *api.App, root, dir, interp string) func(string) string {
	var maps compose.Mappings
	if app != nil && interp != run.InterpreterHost {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		q, _ := api.HookPathMappings.Apply(ctx, app.Hooks(), api.PathMappingsQuery{})
		cancel()
		for _, mp := range q.Mappings {
			maps = maps.Set(mp.Host, mp.Remote)
		}
	}
	cache := map[string]string{}
	return func(p string) string {
		if v, ok := cache[p]; ok {
			return v
		}
		v := mapPath(p, dir, maps)
		if v == p && dir != root {
			v = mapPath(p, root, maps)
		}
		cache[p] = v
		return v
	}
}

func mapPath(p, root string, maps compose.Mappings) string {
	if filepath.IsAbs(p) && isFile(p) {
		return p
	}
	if h, ok := maps.ToHost(p); ok {
		return h
	}
	if root == "" {
		return p
	}
	// Suffix fallback: /var/www/html/tests/A.php → <root>/tests/A.php.
	parts := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	for i := 1; i < len(parts); i++ {
		cand := filepath.Join(root, filepath.FromSlash(strings.Join(parts[i:], "/")))
		if isFile(cand) {
			return cand
		}
	}
	if !filepath.IsAbs(p) {
		if cand := filepath.Join(root, filepath.FromSlash(p)); fileExists(cand) {
			return cand
		}
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// scheduleLocked publishes test.updated soon (throttled).
func (m *Module) scheduleLocked(s *session) {
	s.dirty = true
	if s.timer != nil {
		return
	}
	s.timer = time.AfterFunc(m.opts.PublishInterval, func() {
		m.mu.Lock()
		s.timer = nil
		if !s.dirty || s.done {
			m.mu.Unlock()
			return
		}
		s.dirty = false
		snap := m.snapshotLocked(s, true)
		app := m.app
		m.mu.Unlock()
		if app != nil {
			app.Bus().Publish(api.TopicTestUpdated, snap)
		}
	})
}

// snapshotLocked copies the run with its tree and records the statuses.
// A light snapshot (events) leaves out the bulky per-test fields (D380).
func (m *Module) snapshotLocked(s *session, light bool) api.TestRun {
	r := s.run
	if light {
		r.Tree, r.Light = s.tree.SnapshotLight(), true
	} else {
		r.Tree = s.tree.Snapshot()
	}
	r.Counts = s.tree.Counts()
	if !s.done {
		r.DurationMs = time.Since(r.Started).Milliseconds()
	}
	teamcity.Walk(r.Tree, func(n *teamcity.Node) {
		switch n.Kind {
		case teamcity.KindClass, teamcity.KindMethod, teamcity.KindTest, teamcity.KindDataSet:
			m.last[n.ID] = n.Status
		}
	})
	return r
}

// finish ends the session of run i.
func (m *Module) finish(i run.Info) {
	s := m.ensure(i)
	if s == nil {
		return
	}
	m.mu.Lock()
	if s.done {
		m.mu.Unlock()
		return
	}
	s.stopTimer()
	s.tree.Finish()
	s.done = true
	s.run.State, s.run.ExitCode, s.run.DurationMs, s.run.Error = i.State, i.ExitCode, i.DurationMs, i.Error
	snap := m.snapshotLocked(s, true)
	if snap.Counts.Total == 0 && snap.Error == "" && i.State == run.StateExited && i.ExitCode != 0 {
		snap.Error = "The test run ended without results (exit code " + strconv.Itoa(i.ExitCode) + "); see its output in the Run tool window."
		s.run.Error = snap.Error
	}
	app := m.app
	m.mu.Unlock()
	if app != nil {
		app.Bus().Publish(api.TopicTestFinished, snap)
	}
}

// session returns the session of runID ("" = the latest).
func (m *Module) session(runID string) (*session, error) {
	if runID == "" {
		if len(m.order) == 0 {
			return nil, api.Errorf(api.CodeNotFound, "no test run yet")
		}
		runID = m.order[len(m.order)-1]
	}
	s := m.sessions[runID]
	if s == nil {
		return nil, api.Errorf(api.CodeNotFound, "no test run %s", runID)
	}
	return s, nil
}

func (m *Module) results(_ context.Context, p api.TestRunIDParams) (api.TestRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.session(p.RunID)
	if err != nil {
		return api.TestRun{}, err
	}
	return m.snapshotLocked(s, false), nil
}

// node returns one node of a test run in full (details, output).
func (m *Module) node(_ context.Context, p api.TestNodeParams) (teamcity.Node, error) {
	if p.ID == "" {
		return teamcity.Node{}, api.Errorf(api.CodeInvalidParams, "id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.session(p.RunID)
	if err != nil {
		return teamcity.Node{}, err
	}
	n, ok := s.tree.SnapshotNode(p.ID)
	if !ok {
		return teamcity.Node{}, api.Errorf(api.CodeNotFound, "no test %q in test run %s", p.ID, s.run.RunID)
	}
	return n, nil
}

func (m *Module) runs(_ context.Context, _ struct{}) ([]api.TestRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.TestRun, 0, len(m.order))
	for _, id := range m.order {
		s := m.sessions[id]
		r := s.run
		r.Counts = s.tree.Counts()
		out = append(out, r)
	}
	return out, nil
}

func (m *Module) lastStatus(_ context.Context, p api.TestLastStatusParams) (map[string]teamcity.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]teamcity.Status{}
	if len(p.IDs) == 0 {
		for k, v := range m.last {
			out[k] = v
		}
		return out, nil
	}
	for _, id := range p.IDs {
		if v, ok := m.last[strings.TrimPrefix(id, `\`)]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// ---- running ------------------------------------------------------------------

func (m *Module) runTests(ctx context.Context, p api.TestRunParams) (api.TestRun, error) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	start := api.RunStartParams{Debug: p.Debug}
	sc := p.TestScope
	if p.Config != "" {
		start.Config = p.Config
	} else {
		root, err := m.root()
		if err != nil {
			return api.TestRun{}, err
		}
		if sc.Path != "" {
			sc.Path = abs(root, sc.Path)
		}
		spec, err := m.buildSpec(root, sc, p.Interpreter, nil)
		if err != nil {
			return api.TestRun{}, specError(err)
		}
		start.Spec = &spec
	}
	var info run.Info
	if err := app.CallLocal(ctx, "run.start", start, &info); err != nil {
		return api.TestRun{}, err
	}
	if info.Type != ID {
		return api.TestRun{}, api.Errorf(api.CodeInvalidParams, "run configuration %q is not a %s configuration (type %s)", p.Config, Framework, info.Type)
	}
	s := m.ensure(info)
	if s == nil {
		return api.TestRun{}, api.Errorf(api.CodeFailed, "the test run %s belongs to another project", info.ID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Config == "" {
		s.run.Scope = sc
	}
	r := s.run
	r.Counts = s.tree.Counts()
	return r, nil
}

// specError maps build errors to API errors.
func specError(err error) error {
	var ni errNotInstalled
	if errors.As(err, &ni) {
		return api.Errorf(api.CodeUnavailable, "%s", ni.hint)
	}
	if errors.Is(err, run.ErrInvalidConfig) {
		return api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	return api.Errorf(api.CodeFailed, "%w", err)
}

func (m *Module) rerunFailed(ctx context.Context, p api.TestRunIDParams) (api.TestRun, error) {
	m.mu.Lock()
	s, err := m.session(p.RunID)
	if err != nil {
		m.mu.Unlock()
		return api.TestRun{}, err
	}
	var ids []string
	for _, n := range s.tree.Tests("") {
		if n.Status == teamcity.StatusFailed || n.Status == teamcity.StatusTerminated {
			ids = append(ids, testID(n))
		}
	}
	sc := s.run.Scope
	debug := s.run.Debug || p.Debug
	m.mu.Unlock()
	if len(ids) == 0 {
		return api.TestRun{}, api.Errorf(api.CodeNotFound, "the test run has no failed tests")
	}
	scope := api.TestScope{Suite: sc.Suite, Path: sc.Path, Tests: ids}
	return m.runTests(ctx, api.TestRunParams{TestScope: scope, Debug: debug})
}

// testID returns the ID of a test node ("Ns\Cls::method[ with data set
// …]"); tests without location use their name.
func testID(n *teamcity.Node) string {
	if n.Class != "" && n.Method != "" {
		id := n.Class + "::" + n.Method
		if n.DataSet != "" {
			id += " with data set " + n.DataSet
		}
		return id
	}
	return n.ID
}
