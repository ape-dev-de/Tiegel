package gui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
)

// Test context menus: a right click on a test directory, a test file or
// an ancestor of the test directories (project tree) and on a test in
// the editor offers Run / Debug, Run with Filter… (the framework's
// --filter, scoped to the path) and Modify Run Configuration…. The runs
// go through test.run like the gutter arrows.

// installTestMenus registers the providers of the editor and the tree.
func (d *testDock) installTestMenus() {
	d.v.Registry.AddContextMenu(ContextProjectTree, func(_ *View, t ContextTarget) []MenuItem {
		path := t.File
		if len(t.Paths) > 0 {
			path = t.Paths[0]
		}
		return d.pathMenu(path)
	})
	d.v.Registry.AddContextMenu(ContextEditor, func(_ *View, t ContextTarget) []MenuItem {
		return d.editorMenu(t.File, t.Line)
	})
}

// testsReady reports whether the test plugin runs and found its runner.
func (d *testDock) testsReady() bool {
	t := d.tests()
	return d.v.ctl.Project.Root != "" && !t.Disabled && t.Loaded && t.Status.Available
}

// testPathKind classifies abs: "dir" (a test directory or below),
// "file" (a PHP file in a test directory or with discovered tests),
// "all" (an ancestor of the test directories) or "" (no tests).
func (d *testDock) testPathKind(abs string, dir bool) string {
	abs = filepath.Clean(abs)
	in, above := false, false
	for _, td := range d.tests().Status.TestDirs {
		td = filepath.Clean(d.v.ctl.Abs(td))
		switch {
		case abs == td || strings.HasPrefix(abs, td+string(filepath.Separator)):
			in = true
		case dir && strings.HasPrefix(td, abs+string(filepath.Separator)):
			above = true
		}
	}
	switch {
	case dir && in:
		return "dir"
	case dir && above:
		return "all"
	case !dir && isPHPFile(abs) && (in || d.hasTests(abs)):
		return "file"
	}
	return ""
}

// hasTests reports whether file has discovered test classes (loaded).
func (d *testDock) hasTests(file string) bool {
	ft := d.tests().ForFile(file)
	return ft != nil && len(ft.Classes) > 0
}

// pathMenu are the test entries of a tree entry (absolute path).
func (d *testDock) pathMenu(abs string) []MenuItem {
	if abs == "" || !d.testsReady() {
		return nil
	}
	st, err := os.Stat(abs)
	dir := err == nil && st.IsDir()
	var scope api.TestScope
	var label string
	switch d.testPathKind(abs, dir) {
	case "dir":
		scope, label = api.TestScope{Path: abs}, "Tests in '"+filepath.Base(abs)+"'"
	case "file":
		scope, label = api.TestScope{Path: abs}, "'"+strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))+"'"
	case "all":
		scope, label = api.TestScope{}, "All Tests"
	default:
		return nil
	}
	return d.scopeMenu(label, scope, "")
}

// editorMenu are the test entries of the editor at 1-based line: the
// test at the caret, else the whole file when it holds tests.
func (d *testDock) editorMenu(file string, line int) []MenuItem {
	if file == "" || !d.testsReady() {
		return nil
	}
	if it, ok := d.tests().TestAt(file, line); ok {
		name, filter := it.Name, ""
		if it.Kind == "method" {
			name, filter = it.Method, it.Method
		}
		return d.scopeMenu("'"+name+"'", controller.ScopeOf(it), filter)
	}
	if d.testPathKind(file, false) == "file" {
		return d.pathMenu(file)
	}
	return nil
}

// scopeMenu are Run / Debug / Run with Filter… / Modify Run
// Configuration… of scope; prefill is the suggested filter.
func (d *testDock) scopeMenu(label string, scope api.TestScope, prefill string) []MenuItem {
	t := d.tests()
	return []MenuItem{
		{Title: "Run " + label, Icon: "run", Run: func() { t.RunTests(scope, false) }},
		{Title: "Debug " + label, Icon: "debug", Run: func() { t.RunTests(scope, true) }},
		{Title: "Run " + label + " with Filter…", Icon: "search", Run: func() { d.filterDialog(label, scope, prefill) }},
		{Title: "Modify Run Configuration…", Icon: "edit", Run: func() { d.modifyConfig(scope) }},
	}
}

// filterDialog asks for a filter expression and runs scope with it
// (method and class narrow to the path: the filter selects the tests).
func (d *testDock) filterDialog(label string, scope api.TestScope, prefill string) {
	fw := d.tests().Status.Framework
	if fw == "" {
		fw = "Test"
	}
	d.v.OpenChooser(ChooserSpec{
		Title: "Run " + label + " with Filter",
		Hint:  fw + " filter (regular expression on Class::method, e.g. testAdd or CalcTest::test.*); empty runs all",
		Query: prefill, NoList: true,
		OnChoose: func(_ int, q string) { d.tests().RunTests(filterScope(scope, q), false) },
	})
}

// filterScope narrows scope to its path with filter q (class and method
// are replaced by the filter; empty q keeps scope).
func filterScope(scope api.TestScope, q string) api.TestScope {
	q = strings.TrimSpace(q)
	if q == "" {
		return scope
	}
	return api.TestScope{Suite: scope.Suite, Path: scope.Path, Filter: q}
}
