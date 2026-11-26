package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/modules"
	agentmod "github.com/ape-dev-de/tiegel/internal/modules/agent"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// newAnalysisEnv opens a project with PHPStan 2.2's result cache of the
// fixture in internal/phpstan/testdata (4 findings in 2 files) and the
// agent plugin connected to fake (nil: not installed).
func newAnalysisEnv(t *testing.T, fake *acptest.Agent) *env {
	t.Helper()
	read := func(n string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "phpstan", "testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	opts := agentmod.Options{LookPath: func(string) (string, error) { return "", errors.New("missing") }, HomeDir: t.TempDir()}
	if fake != nil {
		opts = agentmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }}
	}
	envModules = modules.Options{Agent: opts}
	t.Cleanup(func() { envModules = modules.Options{} })
	extra := map[string]string{
		"src/Greeter.php":                   read("Greeter.php.txt"),
		"src/Shouting.php":                  read("Shouting.php.txt"),
		"phpstan-baseline.neon":             read("phpstan-baseline.neon"),
		"phpstan.neon":                      "includes:\n\t- phpstan-baseline.neon\nparameters:\n\ttmpDir: build/phpstan-tmp\n",
		"build/phpstan-tmp/resultCache.php": read("resultCache-2.2.php"),
	}
	e := newEnvWith(t, true, extra, func(root string) {
		old := time.Now().Add(-time.Hour)
		for _, f := range []string{"src/Greeter.php", "src/Shouting.php"} {
			_ = os.Chtimes(filepath.Join(root, f), old, old)
		}
	})
	e.waitFor("phpstan findings", func() bool { return e.ctl.Analysis.Count() == 4 })
	return e
}

func (e *env) openPHPStanFile(rel string) *editorSession {
	e.t.Helper()
	e.ctl.OpenFile(rel, nil)
	e.waitFor("tab", func() bool { return e.v.activeSession() != nil && e.v.activeSession().file == e.ctl.Abs(rel) })
	e.settle()
	return e.v.activeSession()
}

func TestPHPStanInline(t *testing.T) {
	e := newAnalysisEnv(t, nil)
	p := e.ctl.Analysis
	if !p.Active() || !p.Inline() || p.FixAvailable() || p.Status.FixReason == "" {
		t.Fatalf("%+v", p.Status)
	}
	s := e.openPHPStanFile("src/Greeter.php")
	file := s.file
	// Subtle diagnostics on the named expressions.
	s.doc.Lock()
	ds := s.ed.Diagnostics()
	text := s.doc.Buffer().String()
	s.doc.Unlock()
	var marked []string
	for _, d := range ds {
		if d.Subtle {
			marked = append(marked, text[d.Start:d.End])
			if !strings.Contains(d.Source, "PHPStan") {
				t.Fatalf("%+v", d)
			}
		}
	}
	if strings.Join(marked, ",") != "missing,strlen,items" {
		t.Fatalf("marked %q", marked)
	}
	// The hover carries the tip.
	for _, d := range ds {
		if d.Subtle && strings.HasPrefix(d.Message, "Method Acme\\Greeter::items()") && !strings.Contains(d.Detail, "Tip: See: https://phpstan.org/") {
			t.Fatalf("detail %q", d.Detail)
		}
	}
	// A dot in the gutter of each line with findings.
	mp := e.v.Registry.markers(file)
	if mp == nil {
		t.Fatal("no markers")
	}
	dots := 0
	for line := 0; line < 30; line++ {
		for _, m := range mp.Markers(line) {
			if m.Kind == editorview.MarkerDot && m.Tone == editorview.ToneAnalysis {
				dots++
			}
		}
	}
	if dots != 3 {
		t.Fatalf("%d dots", dots)
	}
	// Problems: a PHPStan section after the other problems, with header.
	probs := e.ctl.Problems()
	n := 0
	for i, d := range probs {
		if d.Section == controller.SectionAnalysis {
			n++
			if i+1 < len(probs) && probs[i+1].Section == "" {
				t.Fatal("section not last")
			}
		}
	}
	if n != 4 || !strings.HasPrefix(p.Summary(), "PHPStan · 4 · result cache 2.2.17") {
		t.Fatalf("%d %q", n, p.Summary())
	}
	if errs, warns := e.ctl.ProblemCounts(); warns != 0 || errs != 0 {
		t.Fatalf("PHPStan findings counted: %d %d", errs, warns)
	}
	e.v.toggleTool(toolProblems)
	e.settle()
	if h := e.v.probs.Sections[controller.SectionAnalysis]; h != p.Summary() {
		t.Fatalf("header %q", h)
	}
	// Without the agent: no Fix with AI, the other entries stay.
	got := menuTitles(e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: file, Line: 13}))
	if strings.Contains(strings.Join(got, "|"), "with AI") || !strings.Contains(strings.Join(got, "|"), "Copy PHPStan Message") {
		t.Fatalf("%q", got)
	}
	if got := e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: file, Line: 14}); len(got) != 0 {
		t.Fatalf("%q", menuTitles(got))
	}
	if h := e.v.analysisHint(); h == nil || *h != "" {
		t.Fatal("hint without agent")
	}
	// Turning inline display off removes the marks (Problems keeps them).
	raw, _ := json.Marshal(false)
	if err := e.app.CallLocal(context.Background(), "settings.set", api.SettingsSetParams{Key: "phpstan.inline", Scope: "global", Value: raw}, nil); err != nil {
		t.Fatal(err)
	}
	e.waitFor("inline off", func() bool { return !p.Inline() })
	e.settle()
	s.doc.Lock()
	for _, d := range s.ed.Diagnostics() {
		if d.Subtle {
			t.Fatal("still marked")
		}
	}
	s.doc.Unlock()
	if e.v.Registry.markers(file) != nil {
		t.Fatal("still dotted")
	}
	if len(e.ctl.Problems()) < 4 {
		t.Fatal("problems lost")
	}
}

func TestPHPStanFixWithAI(t *testing.T) {
	fake := &acptest.Agent{Name: "OpenCode", Script: func(_ context.Context, turn *acptest.Turn) string {
		turn.Text("Fixed the call.")
		return ""
	}}
	e := newAnalysisEnv(t, fake)
	p := e.ctl.Analysis
	e.waitFor("fix offered", p.FixAvailable)
	s := e.openPHPStanFile("src/Greeter.php")
	file := s.file
	if h := e.v.analysisHint(); h == nil || !strings.Contains(*h, "Fix with AI") {
		t.Fatal("hint")
	}
	// Alt+Enter offers the finding's fix first.
	acts := e.v.analysisd.intentions(file, 13)
	if len(acts) == 0 || !strings.HasPrefix(acts[0].Title, "Fix with AI: Call to an undefined method") {
		t.Fatalf("%q", menuTitles(acts))
	}
	// The Problems context menu of a PHPStan entry.
	var d controller.Diagnostic
	for _, x := range e.ctl.Problems() {
		if x.Section == controller.SectionAnalysis && x.File == file && x.Line == 13 {
			d = x
		}
	}
	items := e.v.Registry.ContextMenu(e.v, ContextProblems, ContextTarget{File: file, Line: 13, Problem: &d})
	if len(items) == 0 || items[0].Title != "Fix PHPStan Finding with AI" {
		t.Fatalf("%q", menuTitles(items))
	}
	items[0].Run()
	e.waitFor("prompt sent", func() bool { return len(fake.Prompts()) == 1 })
	e.waitFor("agent window", func() bool { e.frame(); return e.ctl.Tools.Visible(ToolAgent) })
	pr := fake.Prompts()[0]
	for _, want := range []string{"src/Greeter.php at line 13", "Identifier: method.notFound", "> 13 |", "do not commit"} {
		if !strings.Contains(pr, want) {
			t.Errorf("prompt lacks %q:\n%s", want, pr)
		}
	}
	// The gutter menu offers it too.
	if got := menuTitles(e.v.Registry.ContextMenu(e.v, ContextEditorGutter, ContextTarget{File: file, Line: 18})); len(got) == 0 || got[0] != "Fix PHPStan Finding with AI" {
		t.Fatalf("%q", got)
	}
	// Disabling the agent plugin withdraws the offer.
	if err := e.app.CallLocal(context.Background(), "modules.disable", api.ModuleParams{ID: agentmod.ID}, nil); err != nil {
		t.Fatal(err)
	}
	e.waitFor("fix withdrawn", func() bool { return !p.FixAvailable() })
	if got := e.v.analysisd.intentions(file, 13); len(got) != 0 {
		t.Fatalf("%q", menuTitles(got))
	}
}

// TestPHPStanScreenshots renders the editor with the inline marks, the
// hover of a finding and the Problems window (TIEGEL_SHOTS:
// phpstan-dark.png, phpstan-light.png).
func TestPHPStanScreenshots(t *testing.T) {
	fake := &acptest.Agent{Name: "OpenCode"}
	e := newAnalysisEnv(t, fake)
	e.waitFor("fix offered", e.ctl.Analysis.FixAvailable)
	s := e.openPHPStanFile("src/Greeter.php")
	e.v.toggleTool(toolProblems)
	e.settle()
	s.doc.Lock()
	off := strings.Index(s.doc.Buffer().String(), "missing()")
	var msgs []string
	for _, d := range s.ed.DiagnosticsAt(off + 1) {
		msgs = append(msgs, d.Message+"  ["+d.Source+"]\n"+d.Detail)
	}
	s.doc.Unlock()
	if len(msgs) != 1 {
		t.Fatalf("hover %q", msgs)
	}
	e.ctl.RequestHover(s.file, off+1, msgs)
	e.frame()
	e.shot("phpstan")
}
