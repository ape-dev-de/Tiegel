package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// candFake declares tool settings and provides candidates for them.
type candFake struct{ fakeModule }

func (candFake) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: "tool.path", Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Tool executable"},
		{Key: "tool.config", Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Tool config"},
		{Key: "tool.mode", Kind: settings.KindEnum, Default: "a", Options: []string{"a", "b"}, Scope: settings.ScopeProject, Label: "Mode"},
		{Key: "tool.on", Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "On"},
		{Key: "tool.interp", Kind: settings.KindString, Default: "project", Scope: settings.ScopeProject, Label: "Interpreter"},
	}
}

func (m candFake) Start(ctx module.Context) error {
	a, _ := AppFrom(ctx)
	if err := ProvideCandidates(ctx, CandidateProvider{Keys: []string{"tool.path"}, Source: "tool scripts", Setup: true,
		Find: func(_ context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
			return ToolCandidates(q, "Tool", "tool"), nil
		}}); err != nil {
		return err
	}
	if err := ProvideCandidates(ctx, CandidateProvider{Keys: []string{"tool.config"}, Source: "tool configs", Setup: true,
		Find: func(_ context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
			return ConfigCandidates(q, "Tool config", "tool.xml", "tool.xml.dist"), nil
		}}); err != nil {
		return err
	}
	if err := ProvideCandidates(ctx, CandidateProvider{Keys: []string{SettingInterpreter}, Source: "interpreters", Setup: true,
		Find: func(_ context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
			return []SettingCandidate{{Value: "local:/usr/bin/php", Label: "php"}, {Value: "compose:x:php", Label: "compose php", Recommended: true}}, nil
		}}); err != nil {
		return err
	}
	return ProvideCandidates(ctx, CandidateProvider{Keys: []string{"tool.interp"}, Source: "interpreters",
		Find: func(c context.Context, q SettingCandidatesQuery) ([]SettingCandidate, error) {
			return a.InterpreterRefCandidates(c, q), nil
		}})
}

func candidatesProject(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	if err := a.AddModule(candFake{fakeModule{id: "tool"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app", "composer.json"), `{"config":{"vendor-dir":"Vendor"}}`)
	writeFile(t, filepath.Join(dir, "app", "Vendor", "bin", "tool"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "app", "tool.xml.dist"), "<x/>")
	writeFile(t, filepath.Join(dir, "other", "tool.xml"), "<x/>")
	var info ProjectInfo
	call(t, a, "project.open", PathParams{Path: dir}, &info)
	return a, a.ProjectRoot()
}

func TestSettingsCandidates(t *testing.T) {
	a, dir := candidatesProject(t)
	app := filepath.Join(dir, "app")
	if got := a.ToolRoot(context.Background()); got != app {
		t.Fatalf("tool root %s", got)
	}
	var res CandidatesResult
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.path"}, &res)
	if res.Source != "tool scripts" || len(res.Candidates) != 1 || !res.Candidates[0].Recommended || res.Candidates[0].Label != "app/Vendor/bin/tool" ||
		res.Candidates[0].Value != filepath.Join(app, "Vendor", "bin", "tool") || !res.Custom {
		t.Fatalf("path %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.config"}, &res)
	if len(res.Candidates) != 2 || res.Candidates[0].Label != "app/tool.xml.dist" || !res.Candidates[0].Recommended {
		t.Fatalf("config %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.mode"}, &res)
	if res.Custom || len(res.Candidates) != 2 || !res.Candidates[0].Current {
		t.Fatalf("enum %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.on"}, &res)
	if res.Custom || len(res.Candidates) != 2 || !res.Candidates[1].Current {
		t.Fatalf("bool %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.interp"}, &res)
	if len(res.Candidates) != 4 || res.Candidates[0].Value != "project" || !res.Candidates[0].Current || res.Candidates[3].Recommended {
		t.Fatalf("interp ref %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "terminal.shell"}, &res)
	if res.Source == "" {
		t.Fatalf("shell %+v", res)
	}
	call(t, a, "settings.candidates", CandidatesParams{Key: "project.phpRoot"}, &res)
	if len(res.Candidates) == 0 || res.Candidates[0].Value != "app" || !res.Candidates[0].Recommended {
		t.Fatalf("php root %+v", res)
	}
	wantCode(t, a, "settings.candidates", CandidatesParams{}, CodeInvalidParams)

	var s SettingsSchema
	call(t, a, "settings.schema", SchemaParams{Module: "tool"}, &s)
	if len(s.Modules) != 1 || s.Modules[0].Settings[0].Candidates != "tool scripts" || s.Modules[0].Settings[2].Candidates != "" {
		t.Fatalf("schema %+v", s.Modules)
	}

	// Stopping the module removes its providers.
	call(t, a, "modules.disable", map[string]any{"id": "tool"}, nil)
	call(t, a, "settings.candidates", CandidatesParams{Key: "tool.path"}, &res)
	if len(res.Candidates) != 0 || res.Source != "" {
		t.Fatalf("after stop %+v", res)
	}
}

func TestSetupProposalAndApply(t *testing.T) {
	a, dir := candidatesProject(t)
	app := filepath.Join(dir, "app")
	var prop SetupProposal
	call(t, a, "project.setupProposal", nil, &prop)
	keys := []string{}
	for _, it := range prop.Items {
		keys = append(keys, it.Key)
	}
	if prop.Dismissed || !prop.Notable || strings.Join(keys, ",") != "project.phpRoot,php-runtime.interpreter,tool.config,tool.path" {
		t.Fatalf("proposal %v %+v", keys, prop)
	}
	if prop.Items[1].Proposed.Value != "compose:x:php" || len(prop.Items[1].Choices) != 2 {
		t.Fatalf("interpreter item %+v", prop.Items[1])
	}
	raw, _ := json.Marshal(filepath.Join(app, "Vendor", "bin", "tool"))
	var res SetupApplyResult
	call(t, a, "project.setupApply", SetupApplyParams{Items: []SetupValue{{Key: "tool.path", Value: raw}, {Key: "nope.key", Value: raw}}, Dismiss: true}, &res)
	if len(res.Applied) != 1 || len(res.Errors) != 1 {
		t.Fatalf("apply %+v", res)
	}
	call(t, a, "project.setupProposal", nil, &prop)
	for _, it := range prop.Items {
		if it.Key == "tool.path" {
			t.Fatal("configured key still proposed")
		}
	}
	if !prop.Dismissed {
		t.Fatal("not dismissed")
	}
	// The interpreter is applied through interp.select (not registered
	// here: an error is reported, nothing written).
	call(t, a, "project.setupApply", SetupApplyParams{Items: []SetupValue{{Key: SettingInterpreter, Value: json.RawMessage(`"compose:x:php"`)}}}, &res)
	if len(res.Errors) != 1 {
		t.Fatalf("interp %+v", res)
	}
	call(t, a, "project.close", nil, nil)
	wantCode(t, a, "project.setupProposal", nil, CodeNoProject)
	wantCode(t, a, "project.setupApply", SetupApplyParams{}, CodeNoProject)
}

func TestSameValueAndHelpers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", "x"), "")
	if !sameValue(dir, filepath.Join(dir, "a"), "x", filepath.Join(dir, "a", "x")) {
		t.Fatal("relative to php root")
	}
	if sameValue(dir, "", "y", filepath.Join(dir, "y")) {
		t.Fatal("missing file equal")
	}
	if !sameValue("", "", []string{"a"}, []any{"a"}) || sameValue("", "", 1.0, "1") {
		t.Fatal("normalize")
	}
	if ToolCandidates(SettingCandidatesQuery{}, "x", "y") != nil || ConfigCandidates(SettingCandidatesQuery{}, "x", "y") != nil {
		t.Fatal("no project")
	}
	if orDot("") != "." || orDot("a") != "a" {
		t.Fatal("orDot")
	}
	for k, want := range map[string]int{keyPHPRoot: 0, SettingInterpreter: 1, "phpunit.x": 2, "composer.path": 3, "qa.x": 4} {
		if setupOrder(k) != want {
			t.Fatalf("order %s", k)
		}
	}
}
