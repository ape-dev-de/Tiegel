package gui

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
)

func testMenuItem(t *testing.T, items []MenuItem, title string) MenuItem {
	t.Helper()
	i := slices.IndexFunc(items, func(m MenuItem) bool { return m.Title == title })
	if i < 0 {
		t.Fatalf("no %q in %q", title, menuTitles(items))
	}
	return items[i]
}

// TestTestContextMenus: the tree offers the test entries on the test
// directory, a test file and its ancestors, not elsewhere; the editor on
// a test method; Run with Filter… runs the path with --filter.
func TestTestContextMenus(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	e := newEnvWith(t, true, testEnvFiles(), nil)
	e.ctl.Tests.LoadStatus()
	e.waitFor("test status", func() bool { return e.ctl.Tests.Loaded && e.ctl.Tests.Status.Available })
	tree := func(rel string) []MenuItem {
		abs := e.ctl.Abs(rel)
		return e.v.Registry.ContextMenu(e.v, ContextProjectTree, ContextTarget{File: abs, Paths: []string{abs}})
	}
	testMenuItem(t, tree("tests"), "Run Tests in 'tests'")
	testMenuItem(t, tree("tests"), "Run Tests in 'tests' with Filter…")
	testMenuItem(t, tree("tests/CalcTest.php"), "Debug 'CalcTest'")
	testMenuItem(t, tree(e.root), "Run All Tests")
	for _, rel := range []string{"src", "src/Calc.php", "composer.json"} {
		if slices.ContainsFunc(tree(rel), func(m MenuItem) bool { return m.Icon == "debug" }) {
			t.Fatalf("test entries on %s: %q", rel, menuTitles(tree(rel)))
		}
	}

	file := filepath.Join(e.root, "tests", "CalcTest.php")
	e.waitFor("tests of the file", func() bool { ft := e.ctl.Tests.ForFile(file); return ft != nil && len(ft.ByLine) == 3 })
	ed := e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: file, Line: 10})
	testMenuItem(t, ed, "Run 'testAdd'")
	testMenuItem(t, ed, "Run 'testAdd' with Filter…").Run()
	if e.v.custom == nil || e.v.chooser.Query() != "testAdd" {
		t.Fatalf("filter dialog not opened with the method")
	}
	e.v.closeOverlay()
	if ed := e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: filepath.Join(e.root, "src", "Calc.php"), Line: 4}); slices.ContainsFunc(ed, func(m MenuItem) bool { return m.Icon == "debug" }) {
		t.Fatalf("test entries in a class: %q", menuTitles(ed))
	}

	if got := filterScope(api.TestScope{Path: "/p/tests/A.php", Class: "A", Method: "m"}, " x "); got.Path != "/p/tests/A.php" || got.Class != "" || got.Method != "" || got.Filter != "x" {
		t.Fatalf("filter scope %+v", got)
	}

	php, err := exec.LookPath("php")
	if err != nil {
		return
	}
	t.Cleanup(api.HookRunInterpreter.On(e.app.Hooks(), -10, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
		q.Interpreter, q.Err = interp.NewLocal(php), ""
		return q, nil
	}))
	testMenuItem(t, tree("tests"), "Run Tests in 'tests' with Filter…").Run()
	e.v.custom.OnChoose(-1, "/testFail/")
	e.waitFor("filtered run", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.State != run.StateRunning && r.Counts.Total > 0
	})
	if r := e.ctl.Tests.Run; r.Counts.Total != 1 || r.Counts.Failed != 1 || r.Scope.Filter != "/testFail/" {
		t.Fatalf("run %+v", r)
	}
}
