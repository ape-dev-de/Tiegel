package controller

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// Test state of the window (AP15.3): the test plugin's status, the tests
// of open files for the gutter (test.discover), the latest test run with
// its result tree and the last result per test. The backend is the test
// plugin's test.* namespace; the controller knows no framework.

// FileTests are the discovered tests of one file.
type FileTests struct {
	// ByLine maps 1-based lines to the class or method declared there.
	ByLine map[int]api.TestItem
	// Classes in file order.
	Classes []api.TestItem
}

type fileTests struct {
	FileTests
	loading, loaded bool
	// stale marks a reload after the current one (the file changed).
	stale bool
}

// Tests is the test state.
type Tests struct {
	c *Controller
	// Status is the test plugin's view of the project (Loaded tells
	// whether it was fetched); Disabled is set while no plugin runs.
	Status   api.TestStatus
	Loaded   bool
	Disabled bool
	// Run is the shown test run (the latest of this window).
	Run *api.TestRun
	// Last are the last results per test ID (class, method, data set).
	Last map[string]teamcity.Status
	// Version changes with status, run and results; FilesVersion with
	// the discovered tests of files; DetailVersion with Detail.
	Version, FilesVersion, DetailVersion uint64
	// Started is set when a test run started from this window (the view
	// shows the tool window); the view clears it.
	Started bool

	files   map[string]*fileTests
	project string
	detail  testDetail
}

// testDetail is the full result of the selected node, fetched with
// test.node (events carry light trees without output and details, D380).
type testDetail struct {
	runID, id string
	// ver is Version when node was fetched; a newer Version (the run
	// changed) fetches again.
	ver     uint64
	fetched bool
	loading bool
	node    *teamcity.Node
}

func newTests(c *Controller) *Tests {
	return &Tests{c: c, Last: map[string]teamcity.Status{}, files: map[string]*fileTests{}}
}

// ProjectChanged forgets the state of the previous project.
func (t *Tests) ProjectChanged() {
	root := t.c.Project.Root
	if root == t.project {
		return
	}
	t.project = root
	t.Status, t.Loaded, t.Disabled, t.Run = api.TestStatus{}, false, false, nil
	t.Last = map[string]teamcity.Status{}
	t.files = map[string]*fileTests{}
	t.detail = testDetail{}
	t.Version++
	t.FilesVersion++
}

// PHPRootChanged reloads the status and the tests of open files (the
// runner and its configuration resolve from the PHP root).
func (t *Tests) PHPRootChanged() {
	t.files = map[string]*fileTests{}
	t.FilesVersion++
	t.LoadStatus()
}

// LoadStatus fetches test.status.
func (t *Tests) LoadStatus() {
	if t.c.Project.Root == "" {
		return
	}
	t.c.Go(func(ctx context.Context) func() {
		var st api.TestStatus
		err := t.c.call(ctx, "test.status", nil, &st)
		return func() {
			t.Loaded = true
			t.Disabled = disabledErr(err)
			if err == nil {
				t.Status = st
			}
			t.Version++
		}
	})
}

// disabledErr reports errors of a missing or switched off plugin.
func disabledErr(err error) bool {
	switch api.ErrorCode(err) {
	case api.CodeModuleDisabled, api.CodeMethodNotFound:
		return true
	}
	return false
}

// ForFile returns the discovered tests of file, loading them on first
// use; nil while loading, for non-PHP files or without a test plugin.
func (t *Tests) ForFile(file string) *FileTests {
	if t.Disabled || t.c.Project.Root == "" || !isPHP(file) {
		return nil
	}
	ft := t.files[file]
	if ft == nil {
		ft = &fileTests{}
		t.files[file] = ft
	}
	if !ft.loaded && !ft.loading {
		t.discover(file, ft)
	}
	if !ft.loaded {
		return nil
	}
	return &ft.FileTests
}

func (t *Tests) discover(file string, ft *fileTests) {
	ft.loading, ft.stale = true, false
	t.c.Go(func(ctx context.Context) func() {
		var d api.TestDiscovery
		err := t.c.call(ctx, "test.discover", api.TestDiscoverParams{Path: file}, &d)
		return func() {
			ft.loading = false
			if disabledErr(err) {
				t.Disabled = true
				t.FilesVersion++
				return
			}
			ft.loaded = true
			ft.ByLine = map[int]api.TestItem{}
			ft.Classes = nil
			if err == nil {
				for _, c := range d.Classes {
					if c.File != file {
						continue
					}
					ft.Classes = append(ft.Classes, c)
					ft.ByLine[c.Line] = c
					for _, m := range c.Children {
						ft.ByLine[m.Line] = m
					}
				}
			}
			t.FilesVersion++
			if ft.stale {
				t.discover(file, ft)
			}
		}
	})
}

// FileChanged rediscovers the tests of file (saved or edited).
func (t *Tests) FileChanged(file string) {
	ft := t.files[file]
	if ft == nil {
		return
	}
	if ft.loading {
		ft.stale = true
		return
	}
	if ft.loaded {
		t.discover(file, ft)
	}
}

// StatusOf returns the last result of test id ("" = never run here).
func (t *Tests) StatusOf(id string) teamcity.Status { return t.Last[id] }

// TestAt returns the test (method, else class) of file around 1-based
// line: the method whose declaration starts at or before line within
// the class (Run Context Configuration at the caret).
func (t *Tests) TestAt(file string, line int) (api.TestItem, bool) {
	ft := t.ForFile(file)
	if ft == nil {
		return api.TestItem{}, false
	}
	var best api.TestItem
	found := false
	for _, c := range ft.Classes {
		if c.Line > line {
			continue
		}
		if !found || c.Line >= best.Line {
			best, found = c, true
		}
		for _, m := range c.Children {
			if m.Line <= line && m.Line >= best.Line {
				best = m
			}
		}
	}
	return best, found
}

// ScopeOf returns the test scope that runs item.
func ScopeOf(item api.TestItem) api.TestScope {
	return api.TestScope{Path: item.File, Class: item.Class, Method: item.Method}
}

// RunTests starts a test run of scope (debug: under the debugger).
func (t *Tests) RunTests(scope api.TestScope, debug bool) {
	t.start("test.run", api.TestRunParams{TestScope: scope, Debug: debug})
}

// RunConfig runs a stored configuration of the test plugin's type.
func (t *Tests) RunConfig(id string, debug bool) {
	t.start("test.run", api.TestRunParams{Config: id, Debug: debug})
}

// RerunFailed runs the failed tests of the shown run again.
func (t *Tests) RerunFailed() {
	id := ""
	if t.Run != nil {
		id = t.Run.RunID
	}
	t.start("test.rerunFailed", api.TestRunIDParams{RunID: id})
}

func (t *Tests) start(method string, params any) {
	t.c.Go(func(ctx context.Context) func() {
		var r api.TestRun
		err := t.c.call(ctx, method, params, &r)
		return func() {
			if err != nil {
				t.c.Status.Notice = "Tests: " + err.Error()
				return
			}
			if !t.ownRun(r) {
				return
			}
			if t.Run == nil || t.Run.RunID != r.RunID {
				t.Run = &r
			}
			t.Started = true
			t.Version++
		}
	})
}

// Stop stops the shown run.
func (t *Tests) Stop() {
	if t.Run != nil && t.Run.State == run.StateRunning {
		t.c.Run.Stop(t.Run.RunID)
	}
}

// Failed returns the failed (or terminated) tests of the shown run.
func (t *Tests) Failed() []*teamcity.Node {
	if t.Run == nil {
		return nil
	}
	var out []*teamcity.Node
	teamcity.Walk(t.Run.Tree, func(n *teamcity.Node) {
		if n.Kind.IsTest() && (n.Status == teamcity.StatusFailed || n.Status == teamcity.StatusTerminated) {
			out = append(out, n)
		}
	})
	return out
}

// handleEvent follows test.* events (UI goroutine).
func (t *Tests) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicTestStarted, api.TopicTestUpdated, api.TopicTestFinished:
		var r api.TestRun
		if json.Unmarshal(payload, &r) != nil || !t.ownRun(r) {
			return
		}
		// Runs of configurations (Ctrl+R) show up here too.
		if topic == api.TopicTestStarted || t.Run == nil || t.Run.RunID == r.RunID {
			if topic == api.TopicTestStarted && t.Run != nil && t.Run.RunID == r.RunID && len(t.Run.Tree) > 0 {
				return
			}
			t.Run = &r
			if topic == api.TopicTestStarted {
				t.Started = true
			}
		}
		teamcity.Walk(r.Tree, func(n *teamcity.Node) {
			switch n.Kind {
			case teamcity.KindClass, teamcity.KindMethod, teamcity.KindTest, teamcity.KindDataSet:
				t.Last[n.ID] = n.Status
			}
		})
		t.Version++
		t.FilesVersion++
	case "install.done":
		t.LoadStatus()
	}
}

// ownRun reports whether r belongs to the open project: a run of the
// previous project may end after a switch (D381).
func (t *Tests) ownRun(r api.TestRun) bool {
	root := t.c.Project.Root
	return r.Project == "" || (root != "" && filepath.Clean(r.Project) == filepath.Clean(root))
}

// Detail returns node id of the shown run in full (message, details,
// expected/actual, output), fetching it with test.node when the run
// changed since; until the first answer it returns nil.
func (t *Tests) Detail(id string) *teamcity.Node {
	if t.Run == nil || id == "" {
		return nil
	}
	d := &t.detail
	if d.runID != t.Run.RunID || d.id != id {
		*d = testDetail{runID: t.Run.RunID, id: id}
	}
	if !d.loading && (!d.fetched || d.ver != t.Version) {
		t.fetchDetail()
	}
	return d.node
}

func (t *Tests) fetchDetail() {
	d := &t.detail
	d.loading = true
	runID, id, ver := d.runID, d.id, t.Version
	t.c.Go(func(ctx context.Context) func() {
		var n teamcity.Node
		err := t.c.call(ctx, "test.node", api.TestNodeParams{RunID: runID, ID: id}, &n)
		return func() {
			d := &t.detail
			if d.runID != runID || d.id != id {
				return // selection changed meanwhile
			}
			d.loading, d.fetched, d.ver = false, true, ver
			if err == nil {
				d.node = &n
			}
			t.DetailVersion++
		}
	})
}

// GotoTest opens the test of the class in file, or the class under test
// of a test; without a test it offers to create one (create is called on
// the UI goroutine with the proposal; it may be nil).
func (t *Tests) GotoTest(file string, choose func(api.TestGotoResult)) {
	t.c.Go(func(ctx context.Context) func() {
		var g api.TestGotoResult
		err := t.c.call(ctx, "test.goto", api.TestGotoParams{Path: file}, &g)
		return func() {
			if err != nil {
				t.c.Status.Notice = "Go to Test: " + err.Error()
				return
			}
			var existing []api.TestTarget
			for _, tg := range g.Targets {
				if tg.Exists {
					existing = append(existing, tg)
				}
			}
			if len(existing) == 1 && g.Direction == "subject" {
				t.c.OpenFile(existing[0].File, nil)
				return
			}
			if len(existing) == 1 && (g.Create == nil || g.Create.Exists) {
				t.c.OpenFile(existing[0].File, nil)
				return
			}
			if len(existing) == 0 && g.Direction == "subject" {
				t.c.Status.Notice = "Go to Test: no class under test found for " + g.Class
				return
			}
			choose(g)
		}
	})
}

// CreateTest writes the proposed test of the class in file and opens it.
func (t *Tests) CreateTest(file, target string) {
	t.c.Go(func(ctx context.Context) func() {
		var plan api.TestCreatePlan
		err := t.c.call(ctx, "test.create", api.TestCreateParams{Path: file, File: target}, &plan)
		return func() {
			if err != nil {
				t.c.Status.Notice = "Create Test: " + err.Error()
				return
			}
			if plan.Exists {
				t.c.Status.Notice = "Create Test: " + t.c.Rel(plan.File) + " exists"
			}
			t.c.OpenFile(plan.File, nil)
		}
	})
}

// ConfigFor asks the test plugin for an unsaved run configuration of
// scope; done runs on the UI goroutine.
func (t *Tests) ConfigFor(scope api.TestScope, done func(run.Config)) {
	t.c.Go(func(ctx context.Context) func() {
		var cfg run.Config
		err := t.c.call(ctx, "test.configFor", scope, &cfg)
		return func() {
			if err != nil {
				t.c.Status.Notice = "Tests: " + err.Error()
				return
			}
			done(cfg)
		}
	})
}

// SortedLast returns the test IDs with a result (tests).
func (t *Tests) SortedLast() []string {
	out := make([]string, 0, len(t.Last))
	for k := range t.Last {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
