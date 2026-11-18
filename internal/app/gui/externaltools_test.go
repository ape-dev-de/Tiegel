package gui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/modules/composer"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

const composerScripts = `{
    "require": {"php": "^8.0"},
    "scripts": {
        "test": "phpunit",
        "lint": "phpcs"
    },
    "scripts-descriptions": {"lint": "Check the style"}
}
`

func runOutputText(e *env) string {
	var sb strings.Builder
	if t := e.ctl.Run.Current(); t != nil {
		for _, l := range t.Lines {
			sb.WriteString(l.Text + "\n")
		}
	}
	return sb.String()
}

func TestComposerScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake composer is a shell script")
	}
	e := newEnvWith(t, true, map[string]string{"composer.json": composerScripts}, nil)
	fake := filepath.Join(t.TempDir(), "composer")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"fake $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(e.app.ProjectSettings(), composer.SettingPath, fake); err != nil {
		t.Fatal(err)
	}
	// No PHP interpreter in the test: Composer runs on the host.
	if err := settings.Set(e.app.ProjectSettings(), composer.SettingInterp, run.InterpreterHost); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.root, "composer.json")
	e.waitFor("scripts", func() bool { return len(e.ctl.Composer.ScriptsAt(file)) == 2 })

	// Play arrows at the script keys (lines 4 and 5).
	p := e.v.cmpd.markers(file)
	if p == nil || len(p.Markers(3)) != 1 || len(p.Markers(4)) != 1 || len(p.Markers(0)) != 0 || e.v.cmpd.markers(filepath.Join(e.root, "index.php")) != nil {
		t.Fatal("gutter markers")
	}
	if e.v.cmpd.gutterClick(file, editorview.GutterClickEvent{Area: editorview.GutterIcons, Line: 0}) {
		t.Fatal("click outside a script")
	}
	if !e.v.cmpd.gutterClick(file, editorview.GutterClickEvent{Area: editorview.GutterIcons, Line: 3}) {
		t.Fatal("click on the test script")
	}
	e.menuChoose("Run 'test'")
	e.waitFor("composer run", func() bool { i := lastRun(e); return i != nil && i.Name == "composer test" && !i.Active() })
	e.waitFor("output", func() bool { return strings.Contains(runOutputText(e), "fake run-script test") })
	if !e.v.ToolShown(ToolRun) {
		t.Fatal("Run window not shown")
	}

	// The chooser lists the scripts with descriptions.
	e.v.RunAction(ActComposerRun)
	e.settle()
	e.menuChoose("lint – Check the style")
	e.waitFor("lint run", func() bool { i := lastRun(e); return i != nil && i.Name == "composer lint" && !i.Active() })

	// Context menus of composer.json offer the chooser.
	if items := e.v.Registry.ContextMenu(e.v, ContextProjectTree, ContextTarget{Paths: []string{file}}); !slices.ContainsFunc(items, func(m MenuItem) bool {
		return m.Title == "Run Composer Script…"
	}) {
		t.Fatalf("tree menu %+v", items)
	}
	if items := e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: filepath.Join(e.root, "index.php")}); slices.ContainsFunc(items, func(m MenuItem) bool {
		return m.Title == "Run Composer Script…"
	}) {
		t.Fatal("chooser offered for another file")
	}

	// Modify Run Configuration opens the editor with the script.
	e.waitFor("types", func() bool { _, ok := e.v.rund.typeOf(composer.TypeScript); return ok })
	e.v.cmpd.modifyConfig("lint")
	ed := findEditor(t, e)
	if ed.typ.ID != composer.TypeScript || ed.cfg.Values[composer.FieldScript] != "lint" {
		t.Fatalf("editor %+v", ed.cfg)
	}
	e.v.closeOverlay()

	// Saving composer.json reloads the scripts.
	if err := os.WriteFile(file, []byte(`{"scripts":{"only":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.ctl.Composer.FileChanged(file)
	e.waitFor("reloaded", func() bool { return len(e.ctl.Composer.ScriptsAt(file)) == 1 })
}

func TestExternalToolsMenu(t *testing.T) {
	e := newEnv(t, true)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.v.Registry.Action(ActConfigureTools); !ok {
		t.Fatal("no configure action")
	}
	e.waitFor("types", func() bool { _, ok := e.v.rund.typeOf(run.TypeExternalTool); return ok })
	e.v.RunAction(ActConfigureTools)
	if ed := findEditor(t, e); ed.typ.ID != run.TypeExternalTool {
		t.Fatalf("editor type %s", ed.typ.ID)
	}
	e.v.closeOverlay()

	// A saved tool becomes a menu action that runs quietly with the
	// editor context.
	var saved run.Config
	e.ctl.Run.SaveConfig(run.Config{Name: "Where", Type: run.TypeExternalTool, Values: map[string]any{
		run.ToolFieldProgram: self, run.ToolFieldArgs: "-test.run=^TestRunDockHelperProcess$ -- $FileRelativePath$ $LineNumber$ $SelectedText$",
		run.ToolFieldEnv: []string{guiRunHelper + "=args"}, run.ToolFieldConsole: false}}, func(c run.Config) { saved = c })
	e.waitFor("saved", func() bool { return saved.ID != "" })
	id := api.ExternalToolAction(saved.ID)
	e.waitFor("tool action", func() bool { a, ok := e.v.Registry.Action(id); return ok && a.Menu == externalToolsMenu })
	if !e.v.Registry.Scheme().Has(id) {
		t.Fatal("tool not bindable")
	}
	e.v.HideTool(ToolRun)
	s := e.openEditor("index.php")
	e.caretAt(s, "echo", 0)
	e.v.RunAction(id)
	e.waitFor("tool run", func() bool { i := lastRun(e); return i != nil && i.Name == "Where" && !i.Active() })
	e.waitFor("tool output", func() bool { return strings.Contains(runOutputText(e), "index.php\n2\n") })
	if e.v.ToolShown(ToolRun) {
		t.Fatal("quiet tool opened the console")
	}

	// A user binding of the tool reaches the window's scheme.
	if _, err := e.app.CallJSON(context.Background(), "keymap.set", api.KeymapSetParams{Action: string(id), Chords: []string{"Ctrl+Alt+Shift+W"}}); err != nil {
		t.Fatal(err)
	}
	e.ctl.LoadKeymap()
	e.waitFor("binding", func() bool { return e.v.Registry.Scheme().ChordText(id) != "" })

	// Deleting the tool removes the action.
	e.ctl.Run.DeleteConfig(saved.ID)
	e.waitFor("action removed", func() bool { _, ok := e.v.Registry.Action(id); return !ok })
}
