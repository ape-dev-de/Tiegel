package api

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/run"
)

func TestExternalTools(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "A.php"), "<?php\n")
	call(t, a, "project.open", PathParams{Path: root}, nil)
	root = a.ProjectRoot()

	var tool run.Config
	call(t, a, "run.configs.save", run.Config{Name: "Show", Type: run.TypeExternalTool, Values: map[string]any{
		run.ToolFieldProgram: self, run.ToolFieldArgs: "$FileRelativePath$ $LineNumber$ $SelectedText$",
		run.ToolFieldDir: "$FileDir$", run.ToolFieldEnv: []string{runHelperEnv + "=echo"}, run.ToolFieldConsole: false}}, &tool)

	// Listing works without a file: the template is shown.
	var cfgs []RunConfigInfo
	call(t, a, "run.configs.list", nil, &cfgs)
	if len(cfgs) != 1 || cfgs[0].Error != "" || !strings.Contains(cfgs[0].Display, "$FileRelativePath$") {
		t.Fatalf("list %+v", cfgs)
	}
	// Without context the file macros fail.
	wantCode(t, a, "run.start", RunStartParams{Config: "Show"}, CodeInvalidParams)

	var info run.Info
	call(t, a, "run.start", RunStartParams{Config: "show", Context: &run.MacroContext{
		File: "src/A.php", Line: 3, Column: 1, SelectedText: "two words"}}, &info)
	if !info.Spec.Quiet || !info.Spec.SyncFiles {
		t.Fatalf("spec %+v", info.Spec)
	}
	waitRun(t, a, info.ID)
	out := outputText(t, a, info.ID)
	if !strings.Contains(out, "wd="+filepath.Join(root, "src")) || !strings.Contains(out, "src/A.php\n3\ntwo words") {
		t.Fatalf("output %q", out)
	}

	// The sync after the run reconciles open documents: a document
	// changed on disk by the tool is reloaded.
	file := filepath.Join(root, "src", "A.php")
	var st DocState
	call(t, a, "doc.open", PathParams{Path: file}, &st)
	writeFile(t, file, "<?php // changed\n")
	reloaded := make(chan struct{}, 1)
	unsub := a.Bus().Subscribe(TopicDocReloaded, func(bus.Event) {
		select {
		case reloaded <- struct{}{}:
		default:
		}
	})
	defer unsub()
	call(t, a, "run.start", RunStartParams{Config: tool.ID, Context: &run.MacroContext{File: file}}, &info)
	waitRun(t, a, info.ID)
	select {
	case <-reloaded:
	case <-time.After(10 * time.Second):
		t.Fatal("no reload after the tool run")
	}

	// Macros with preview values.
	var macros []RunMacroInfo
	call(t, a, "run.macros", RunMacrosParams{Context: &run.MacroContext{File: file, Line: 9}}, &macros)
	byName := map[string]RunMacroInfo{}
	for _, m := range macros {
		byName[m.Name] = m
	}
	if byName["$FileName$"].Value != "A.php" || byName["$LineNumber$"].Value != "9" || byName["$ProjectFileDir$"].Value != root {
		t.Fatalf("macros %+v", macros)
	}
	call(t, a, "run.macros", nil, &macros)
	for _, m := range macros {
		if m.NeedsFile && m.Error == "" {
			t.Fatalf("file macro without error %+v", m)
		}
	}

	// Every tool is a keymap action that can be bound.
	action := string(ExternalToolAction(tool.ID))
	var ks KeymapState
	call(t, a, "keymap.list", KeymapListParams{Query: "external tools"}, &ks)
	if len(ks.Bindings) != 1 || ks.Bindings[0].Action != action || ks.Bindings[0].Title != "External Tools: Show" {
		t.Fatalf("keymap %+v", ks)
	}
	var res KeymapSetResult
	call(t, a, "keymap.set", KeymapSetParams{Action: action, Chords: []string{"Ctrl+Alt+Shift+T"}}, &res)
	if res.Binding == nil || !slices.Equal(res.Binding.Chords, []string{"Ctrl+Alt+Shift+T"}) {
		t.Fatalf("set %+v", res)
	}
	defs := ExternalToolDefs([]run.Config{{ID: "x", Name: "X", Type: run.TypeExternalTool}, {ID: "y", Type: run.TypeCommand}})
	if len(defs) != 1 || defs[0].ID != ExternalToolAction("x") {
		t.Fatalf("defs %+v", defs)
	}
	// Without project there are no tool actions; the binding stays stored.
	call(t, a, "project.close", nil, nil)
	call(t, a, "keymap.list", KeymapListParams{Query: "external tools"}, &ks)
	if len(ks.Bindings) != 0 {
		t.Fatalf("keymap without project %+v", ks)
	}
	call(t, a, "project.open", PathParams{Path: root}, nil)
	call(t, a, "keymap.list", KeymapListParams{User: true}, &ks)
	if len(ks.Bindings) != 1 || ks.Bindings[0].Action != action {
		t.Fatalf("binding after reopen %+v", ks)
	}
}
