package gui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

const guiCalcTest = `<?php
namespace Tests;

use PHPUnit\Framework\TestCase;

final class CalcTest extends TestCase
{
    public function testAdd(): void
    {
        $this->assertSame(2, 1 + 1);
    }

    public function testFail(): void
    {
        $this->assertSame('foo', 'bar');
    }
}
`

// guiFakePHPUnit stands in for vendor/bin/phpunit (run with the host's
// php): it reports the tests the --filter selects, testFail failing with
// a comparison at line 15.
const guiFakePHPUnit = `<?php
echo "Fake PHPUnit 11 by tests\n\n";
$filter = null;
foreach ($argv as $i => $a) { if ($a === '--filter') { $filter = $argv[$i + 1]; } }
$file = getcwd() . '/tests/CalcTest.php';
$hint = 'php_qn://' . $file . '::\Tests\CalcTest';
echo "##teamcity[testSuiteStarted name='Tests\\CalcTest' locationHint='$hint']\n";
foreach (['testAdd', 'testFail'] as $t) {
    if ($filter !== null && !preg_match($filter, 'Tests\CalcTest::' . $t)) { continue; }
    echo "##teamcity[testStarted name='$t' locationHint='$hint::$t']\n";
    if ($t === 'testFail') {
        echo "##teamcity[testFailed name='$t' message='Failed asserting that two strings are identical.' details='$file:15|n' type='comparisonFailure' expected='|'foo|'' actual='|'bar|'']\n";
    }
    echo "##teamcity[testFinished name='$t' duration='2']\n";
}
echo "##teamcity[testSuiteFinished name='Tests\\CalcTest']\n";
echo "\nFAILURES!\n";
`

func testEnvFiles() map[string]string {
	return map[string]string{
		"composer.json":          `{"autoload":{"psr-4":{"App\\":"src/"}},"autoload-dev":{"psr-4":{"Tests\\":"tests/"}}}`,
		"phpunit.xml":            `<phpunit><testsuites><testsuite name="unit"><directory>tests</directory></testsuite></testsuites></phpunit>`,
		"tests/CalcTest.php":     guiCalcTest,
		"src/Calc.php":           "<?php\nnamespace App;\n\nclass Calc {}\n",
		"src/Service/Mailer.php": "<?php\nnamespace App\\Service;\n\nclass Mailer {}\n",
		"vendor/bin/phpunit":     guiFakePHPUnit,
	}
}

func TestTestDockGutterAndGoto(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	e := newEnvWith(t, true, testEnvFiles(), nil)
	for _, id := range []string{string(ActDebug), string(ActDebugFile), string(ActGotoTest), string(ActRerunFailed), string(ActTestsView)} {
		if !e.v.Registry.HasHandler(id) {
			t.Errorf("no handler for %s", id)
		}
	}
	s := e.openEditor("tests/CalcTest.php")
	file := s.file
	e.waitFor("tests of the file", func() bool { ft := e.ctl.Tests.ForFile(file); return ft != nil && len(ft.ByLine) == 3 })
	ms := e.v.tstd.markers(file)
	// Line 6: the class, line 8: testAdd (0-based 5 and 7).
	if m := ms.Markers(5); len(m) != 1 || m[0].Glyph != atoms.GlyphRun || m[0].Tone != editorview.ToneSuccess {
		t.Fatalf("class marker %+v", m)
	}
	if m := ms.Markers(6); m != nil {
		t.Fatalf("marker on a line without test: %+v", m)
	}
	if e.v.tstd.markers("/x/readme.md") != nil {
		t.Fatal("markers for a non-PHP file")
	}
	// The play arrow opens Run / Debug / Modify Run Configuration.
	if !e.v.tstd.gutterClick(file, editorview.GutterClickEvent{Line: 7, Area: editorview.GutterIcons}) {
		t.Fatal("click not handled")
	}
	e.frame()
	if e.v.tstd.gutterClick(file, editorview.GutterClickEvent{Line: 7, Area: editorview.GutterNumbers}) ||
		e.v.tstd.gutterClick(file, editorview.GutterClickEvent{Line: 6, Area: editorview.GutterIcons}) {
		t.Fatal("click outside the play arrow handled")
	}
	e.v.closeOverlay()
	items := e.v.tstd.testMenu(e.ctl.Tests.ForFile(file).ByLine[8])
	if len(items) != 3 || items[0].Title != "Run 'testAdd'" || items[1].Title != "Debug 'testAdd'" {
		t.Fatalf("menu %+v", items)
	}
	// Modify Run Configuration… opens the editor with the method scope.
	e.waitFor("config types", func() bool { return len(e.ctl.Run.Types) > 3 })
	items[2].Run()
	e.waitFor("config editor", e.v.PopupOpen)
	if ed := findEditor(t, e); ed.cfg.Name != "CalcTest::testAdd" || ed.cfg.Values["method"] != "testAdd" {
		t.Fatalf("config %+v", ed.cfg)
	}
	e.v.closeOverlay()

	// Go to Test: class ↔ test.
	e.v.tstd.gotoTest(file)
	e.waitFor("class under test", func() bool { return e.activeFile() == filepath.Join(e.root, "src", "Calc.php") })
	e.v.tstd.gotoTest(filepath.Join(e.root, "src", "Calc.php"))
	e.waitFor("test", func() bool { return e.activeFile() == file })
	// Without a test: Create New Test… shows the skeleton and creates it.
	mailer := filepath.Join(e.root, "src", "Service", "Mailer.php")
	e.v.tstd.gotoTest(mailer)
	e.waitFor("create menu", func() bool {
		s := e.v.navd.menus.source
		return e.v.navd.menus.Opened() && len(s) == 1 && s[0].Title == "Create New Test…"
	})
	e.menuChoose("Create New Test…")
	e.waitFor("create popup", e.v.PopupOpen)
	e.v.popup.Buttons[0].OnClick()
	created := filepath.Join(e.root, "tests", "Service", "MailerTest.php")
	e.waitFor("created test", func() bool { return e.activeFile() == created })
	if b, err := os.ReadFile(created); err != nil || !strings.Contains(string(b), "final class MailerTest extends TestCase") {
		t.Fatalf("created %q %v", b, err)
	}
}

func TestTestDockRun(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	e := newEnvWith(t, true, testEnvFiles(), nil)
	// The host's php as project interpreter (the env finds none).
	t.Cleanup(api.HookRunInterpreter.On(e.app.Hooks(), -10, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
		q.Interpreter, q.Err = interp.NewLocal(php), ""
		return q, nil
	}))
	s := e.openEditor("tests/CalcTest.php")
	file := s.file
	e.waitFor("tests of the file", func() bool { ft := e.ctl.Tests.ForFile(file); return ft != nil && len(ft.ByLine) == 3 })

	// Run Context Configuration with the caret in testFail runs exactly it.
	e.caretAt(s, "assertSame('foo'", 0)
	e.v.RunAction(ActRunFile)
	e.waitFor("test run", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.State != run.StateRunning && r.Counts.Total > 0
	})
	r := e.ctl.Tests.Run
	if r.Counts.Total != 1 || r.Counts.Failed != 1 || r.Name != "CalcTest::testFail" {
		t.Fatalf("run %+v", r)
	}
	if !e.v.ToolShown(ToolTests) {
		t.Fatal("Tests window not shown")
	}
	// The gutter shows the result.
	e.waitFor("failed marker", func() bool {
		m := e.v.tstd.markers(file).Markers(12)
		return len(m) == 1 && m[0].Glyph == atoms.GlyphError
	})
	e.settle()
	// The failure is selected; Show Diff opens expected/actual, the
	// failure opens at the local line.
	sel := e.v.tstd.data[e.v.tstd.tree.sel]
	if sel == nil || sel.ID != `Tests\CalcTest::testFail` || !sel.Comparison || sel.FailLine != 15 || sel.FailFile != file {
		t.Fatalf("selected %+v", sel)
	}
	// The tree is light; the detail comes from test.node.
	if sel.Expected != "" || sel.Details != "" {
		t.Fatalf("light tree has details: %+v", sel)
	}
	e.waitFor("detail", func() bool { n := e.ctl.Tests.Detail(sel.ID); return n != nil && n.Expected != "" })
	sel = e.ctl.Tests.Detail(sel.ID)
	e.v.tstd.showDiff(sel)
	e.waitFor("diff", func() bool {
		for _, d := range e.ctl.Work.Diffs() {
			if d.Spec.Texts != nil && !d.Loading && d.Result != nil {
				return true
			}
		}
		return false
	})
	e.v.tstd.openNode(sel)
	e.waitFor("failure line", func() bool { return e.activeFile() == file })
	if lines := detailLines(sel, e.ctl.Rel); !strings.Contains(strings.Join(lines, "\n"), "at tests/CalcTest.php:15") {
		t.Fatalf("details %q", lines)
	}

	// Run All via the window, then Rerun Failed.
	e.v.ShowTool(ToolTests)
	e.ctl.Tests.RunTests(api.TestScope{}, false)
	e.waitFor("all", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.State != run.StateRunning && r.Counts.Total == 2
	})
	e.v.RunAction(ActRerunFailed)
	e.waitFor("rerun", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.Name != "PHPUnit" && r.State != run.StateRunning && r.Counts.Total == 1
	})
	if st := e.ctl.Tests.StatusOf(`Tests\CalcTest::testAdd`); st != teamcity.StatusPassed {
		t.Fatalf("last %s", st)
	}
	e.settle()
	if e.v.tstd.summary() == "" {
		t.Fatal("no summary")
	}
}
