package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

// guiRunHelper makes TestRunDockHelperProcess act as a long running
// program (the classic helper process pattern; the run tests start the
// test binary with -test.run selecting it).
const guiRunHelper = "TIEGEL_GUI_RUN_HELPER"

func TestRunDockHelperProcess(t *testing.T) {
	switch os.Getenv(guiRunHelper) {
	case "":
		return
	case "sleep":
		println("ready")
		time.Sleep(60 * time.Second)
	}
	os.Exit(0)
}

// helperCommand returns a command line running the test binary's helper
// (mode "" prints PASS and exits).
func helperCommand(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return run.QuoteWords([]string{self, "-test.run=^TestRunDockHelperProcess$"})
}

func lastRun(e *env) *run.Info {
	if t := e.ctl.Run.Current(); t != nil {
		return &t.Info
	}
	return nil
}

func TestRunDock(t *testing.T) {
	e := newEnv(t, true)
	r := e.v.Registry
	for _, id := range []string{string(ActRun), string(ActRunFile), string(ActRerun), string(ActRunView), string(ActRunStop), string(ActRunConfigs)} {
		if !r.HasHandler(id) {
			t.Errorf("no handler for %s", id)
		}
	}
	if _, ok := r.ToolWindow(ToolRun); !ok {
		t.Fatal("no Run tool window")
	}
	e.v.ShowTool(ToolRun)
	e.settle() // empty window with the hint

	// A configuration made in the editor popup runs with Save and Run.
	e.waitFor("config types", func() bool { return len(e.ctl.Run.Types) == 3 })
	cmdType, _ := e.v.rund.typeOf(run.TypeCommand)
	e.v.rund.openEditor(run.Config{Type: run.TypeCommand}, cmdType)
	if !e.v.PopupOpen() {
		t.Fatal("editor not open")
	}
	e.frame()
	ed := e.v.popup
	editor := findEditor(t, e)
	editor.name.SetText("Helper")
	for _, it := range editor.items {
		switch it.f.Key {
		case "command":
			it.text.SetText(helperCommand(t))
		case "env":
			it.text.SetText("A=1\n\nB=2")
		}
	}
	vals, err := editor.values()
	if err != nil || vals["interpreter"] != run.InterpreterHost || len(vals["env"].([]string)) != 2 {
		t.Fatalf("values %+v %v", vals, err)
	}
	ed.Buttons[0].OnClick() // Save and Run
	e.waitFor("run finished", func() bool { i := lastRun(e); return i != nil && !i.Active() })
	if !e.v.ToolShown(ToolRun) {
		t.Fatal("Run window not shown")
	}
	tab := e.ctl.Run.Current()
	if tab.Info.ExitCode != 0 || tab.Info.Name != "Helper" {
		t.Fatalf("run %+v", tab.Info)
	}
	e.waitFor("output", func() bool {
		for _, l := range e.ctl.Run.Current().Lines {
			if strings.Contains(l.Text, "PASS") {
				return true
			}
		}
		return false
	})
	if cfg := e.ctl.Run.SelectedConfig(); cfg == nil || cfg.Name != "Helper" {
		t.Fatalf("selected %+v", e.ctl.Run.Configs)
	}
	e.settle()

	// Ctrl+R runs the selected configuration again; Rerun reuses the tab.
	first := tab.Info.ID
	e.v.RunAction(ActRun)
	e.waitFor("second run", func() bool { i := lastRun(e); return i != nil && i.ID != first && !i.Active() })
	second := lastRun(e).ID
	e.v.RunAction(ActRerun)
	e.waitFor("rerun", func() bool { i := lastRun(e); return i != nil && i.ID != second && !i.Active() })
	if e.ctl.Run.Tab(second) != nil {
		t.Fatal("rerun kept the old tab")
	}
	e.settle()

	// Output locations open the file at the line.
	e.v.rund.openLink(run.Link{File: filepath.Join(e.root, "index.php"), Line: 2})
	e.waitFor("file opened", func() bool { return e.activeFile() == filepath.Join(e.root, "index.php") })
	// Rendering of output lines with links, stderr and system lines.
	cur := e.ctl.Run.Current()
	cur.Lines = append(cur.Lines,
		run.Line{Seq: 100, Stream: run.StreamStderr, Text: "in /app/index.php:2 boom",
			Links: []run.Link{{Start: 3, End: 19, File: filepath.Join(e.root, "index.php"), Line: 2}}},
		run.Line{Seq: 101, Stream: run.StreamSystem, Text: "done"})
	cur.Version++
	e.settle()

	// Run Current File runs the active PHP file (no php here: a notice).
	e.v.RunAction(ActRunFile)
	e.waitFor("file run error", func() bool { return strings.Contains(e.ctl.Status.Notice, "interpreter") })
	e.ctl.CloseTab(e.ctl.Active)
	e.settle()
	e.v.RunAction(ActRunFile)
	if !strings.Contains(e.ctl.Status.Notice, "not a PHP file") {
		t.Fatalf("notice %q", e.ctl.Status.Notice)
	}

	// The configurations menu offers new, edit and delete.
	e.v.RunAction(ActRunConfigs)
	e.frame()
	e.v.closeOverlay()

	// Closing with an active run asks (policy ask).
	e.ctl.Run.SaveConfig(run.Config{Name: "Sleep", Type: run.TypeCommand, Values: map[string]any{
		"command": helperCommand(t), "env": []string{guiRunHelper + "=sleep"}}}, func(c run.Config) { e.ctl.Run.RunConfig(c.ID) })
	e.waitFor("sleeper", func() bool { i := lastRun(e); return i != nil && i.Name == "Sleep" && i.Active() })
	proceeded := false
	e.v.rund.confirmClose(func() { proceeded = true })
	e.waitFor("close popup", e.v.PopupOpen)
	if proceeded || !strings.Contains(e.v.popup.Title, "1 process is still running") {
		t.Fatalf("popup %+v", e.v.popup)
	}
	e.v.popup.Buttons[0].OnClick() // Terminate
	e.waitFor("stopped", func() bool { i := lastRun(e); return i != nil && i.State == run.StateStopped })
	if !proceeded {
		t.Fatal("close did not proceed")
	}
	e.v.closeOverlay()
	// Policy keep: no question.
	if err := settings.Set(e.app.Settings(), run.KeyConfirmClose, run.CloseKeep); err != nil {
		t.Fatal(err)
	}
	proceeded = false
	e.v.rund.confirmClose(func() { proceeded = true })
	e.waitFor("proceed", func() bool { return proceeded })

	// Closing a tab of an active run stops it first.
	if err := settings.Set(e.app.Settings(), run.KeyConfirmClose, run.CloseTerminate); err != nil {
		t.Fatal(err)
	}
	e.v.RunAction(ActRun)
	e.waitFor("sleeper again", func() bool { i := lastRun(e); return i != nil && i.Active() })
	id := lastRun(e).ID
	e.v.RunAction(ActRunStop)
	e.waitFor("stop action", func() bool { t := e.ctl.Run.Tab(id); return t != nil && !t.Active() })
	e.ctl.Run.CloseTab(id)
	e.settle()
	if e.ctl.Run.Tab(id) != nil {
		t.Fatal("tab not closed")
	}
	// Policy terminate: the project's runs stop without a question.
	e.v.RunAction(ActRun)
	e.waitFor("sleeper 3", func() bool { i := lastRun(e); return i != nil && i.Active() })
	proceeded = false
	e.v.rund.confirmClose(func() { proceeded = true })
	e.waitFor("terminated", func() bool { i := lastRun(e); return proceeded && i != nil && !i.Active() })
	res, err := e.app.CallJSON(context.Background(), "run.list", api.RunListParams{Active: true})
	if err != nil || len(res.([]run.Info)) != 0 {
		t.Fatalf("active runs left: %v %v", res, err)
	}
}

// findEditor returns the configuration editor behind the open popup.
func findEditor(t *testing.T, e *env) *runConfigEditor {
	t.Helper()
	ed, ok := e.v.rund.editorOpen()
	if !ok {
		t.Fatal("no editor")
	}
	return ed
}

func TestRunHelpers(t *testing.T) {
	if stateGlyph(run.Info{State: run.StateRunning}) != atoms.GlyphRun || stateGlyph(run.Info{State: run.StateStopped}) != atoms.GlyphStop ||
		stateGlyph(run.Info{State: run.StateExited}) != atoms.GlyphDone || stateGlyph(run.Info{State: run.StateExited, ExitCode: 1}) != atoms.GlyphError {
		t.Fatal("glyphs")
	}
	s := runSummary(run.Info{State: run.StateExited, ExitCode: 0, DurationMs: 1500, Where: "host", Display: "php a.php"})
	if s != "exit 0 after 1.5s · host · php a.php" {
		t.Fatal(s)
	}
	if !isPHPFile("/a/B.PHP") || isPHPFile("/a/b.js") {
		t.Fatal("php file")
	}
}
