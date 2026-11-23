package gui

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// clickGutterIcon clicks the icon column of 0-based line of s with the
// mouse, as a user does on a run arrow. The editor's window position is
// found by probing down the icon column of the editor area until the
// editor reports a gutter click (row 0 starts there).
func (e *env) clickGutterIcon(s *editorSession, line int) {
	e.t.Helper()
	icon, ok := s.ed.GutterIconRect(line)
	if !ok {
		e.t.Fatalf("line %d not visible", line)
	}
	area := e.v.bench.EditorRect()
	x := area.Min.X + icon.Min.X + icon.Dx()/2
	var hits []editorview.GutterClickEvent
	saved := e.v.Registry.gutterClicks
	e.v.Registry.gutterClicks = []GutterClickHandler{func(_ string, ev editorview.GutterClickEvent) bool {
		hits = append(hits, ev)
		return true
	}}
	top := -1
	for y := area.Min.Y; y < area.Max.Y && top < 0; y++ {
		e.h.Click(image.Pt(x, y))
		e.frame()
		if len(hits) > 0 {
			top = y
		}
	}
	e.v.Registry.gutterClicks = saved
	if top < 0 || hits[0].Area != editorview.GutterIcons {
		e.t.Fatalf("editor gutter not found in %v: %+v", area, hits)
	}
	// top is the window position of the editor's local y = 0.
	icon, _ = s.ed.GutterIconRect(line)
	e.h.Click(image.Pt(x, top+icon.Min.Y+icon.Dy()/2))
	e.frame()
}

// hostPHP makes the host's php the project interpreter (the env finds
// none); it skips the test without php.
func hostPHP(t *testing.T, e *env) {
	t.Helper()
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	t.Cleanup(api.HookRunInterpreter.On(e.app.Hooks(), -10, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
		q.Interpreter, q.Err = interp.NewLocal(php), ""
		return q, nil
	}))
}

// gutterRun clicks the run arrow at 0-based line of file, chooses Run and
// waits for the finished test run.
func (e *env) gutterRun(s *editorSession, line int, entry string) *api.TestRun {
	e.t.Helper()
	e.waitFor("run arrow", func() bool {
		ms := e.v.Registry.markers(s.file)
		return ms != nil && len(ms.Markers(line)) > 0
	})
	e.clickGutterIcon(s, line)
	e.waitFor("test menu", func() bool { return e.v.navd.menus.Opened() || len(e.v.chooser.Items) > 0 })
	e.menuChoose(entry)
	e.waitFor("test run", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.State != run.StateRunning && r.Counts.Total > 0
	})
	e.settle()
	if !e.v.ToolShown(ToolTests) {
		e.t.Fatal("Tests window not shown")
	}
	return e.ctl.Tests.Run
}

// shownTests returns the test IDs in the Tests window's tree.
func (e *env) shownTests() map[string]teamcity.Status {
	out := map[string]teamcity.Status{}
	for _, n := range e.v.tstd.data {
		if n.Kind.IsTest() {
			out[n.ID] = n.Status
		}
	}
	return out
}

// TestGutterRunArrowClick: the play arrow in the editor gutter, clicked
// with the mouse, runs exactly that test; the result shows in the Tests
// window and in the gutter.
func TestGutterRunArrowClick(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	e := newEnvWith(t, true, testEnvFiles(), nil)
	hostPHP(t, e)
	s := e.openEditor("tests/CalcTest.php")
	// Line 8 (0-based 7): testAdd.
	r := e.gutterRun(s, 7, "Run 'testAdd'")
	if r.Name != "CalcTest::testAdd" || r.Counts.Total != 1 || r.Counts.Passed != 1 {
		t.Fatalf("run %+v", r)
	}
	if got := e.shownTests(); len(got) != 1 || got[`Tests\CalcTest::testAdd`] != teamcity.StatusPassed {
		t.Fatalf("Tests window %+v", got)
	}
	e.waitFor("passed marker", func() bool {
		m := e.v.Registry.markers(s.file).Markers(7)
		return len(m) > 0 && m[0].Glyph == atoms.GlyphDone && e.ctl.Tests.StatusOf(`Tests\CalcTest::testAdd`) == teamcity.StatusPassed
	})
}

// TestGutterRunNestedPHPRoot: the PHP project lives in project/ (no
// composer.json at the top, D515); the arrow runs project/vendor/bin/
// phpunit in project/ and the failure points to the host file.
func TestGutterRunNestedPHPRoot(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	files := map[string]string{}
	for n, c := range testEnvFiles() {
		files["project/"+n] = c
	}
	e := newEnvWith(t, true, files, func(root string) {
		if err := os.Remove(filepath.Join(root, "composer.json")); err != nil {
			t.Fatal(err)
		}
	})
	hostPHP(t, e)
	if e.ctl.Project.PHPRoot != "project" || e.ctl.PHPRoot() != filepath.Join(e.root, "project") {
		t.Fatalf("PHP root %q", e.ctl.Project.PHPRoot)
	}
	e.ctl.Tests.LoadStatus()
	e.waitFor("test status", func() bool { return e.ctl.Tests.Loaded })
	if st := e.ctl.Tests.Status; !st.Available || st.Base != "project" || st.Executable != "project/vendor/bin/phpunit" {
		t.Fatalf("status %+v", st)
	}
	s := e.openEditor("project/tests/CalcTest.php")
	// Line 6 (0-based 5): the class runs both tests.
	r := e.gutterRun(s, 5, "Run 'CalcTest'")
	if r.Counts.Total != 2 || r.Counts.Failed != 1 || r.Counts.Passed != 1 {
		t.Fatalf("run %+v", r.Counts)
	}
	got := e.shownTests()
	if got[`Tests\CalcTest::testAdd`] != teamcity.StatusPassed || got[`Tests\CalcTest::testFail`] != teamcity.StatusFailed {
		t.Fatalf("Tests window %+v", got)
	}
	e.waitFor("detail", func() bool { n := e.ctl.Tests.Detail(`Tests\CalcTest::testFail`); return n != nil && n.FailFile != "" })
	if n := e.ctl.Tests.Detail(`Tests\CalcTest::testFail`); n.FailFile != s.file || n.FailLine != 15 {
		t.Fatalf("failure %s:%d", n.FailFile, n.FailLine)
	}
	// Choosing the repository root as PHP root (setting) reaches the
	// window: the test status follows.
	if _, err := e.app.CallJSON(context.Background(), "settings.set", api.SettingsSetParams{Key: "project.phpRoot", Scope: "project", Value: []byte(`"."`)}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("PHP root change", func() bool {
		return e.ctl.Project.PHPRoot == "." && e.ctl.Tests.Loaded && e.ctl.Tests.Status.Base == "." && !e.ctl.Tests.Status.Available
	})
}
