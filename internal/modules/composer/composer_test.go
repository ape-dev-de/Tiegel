package composer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
)

const composerJSON = `{
    "name": "acme/app",
    "require": {"php": ">=8.2"},
    "scripts": {
        "test": "phpunit",
        "lint": ["@php -l src", "phpcs"],
        "nested": {"odd": true}
    },
    "scripts-descriptions": {
        "test": "Run the tests"
    }
}
`

func TestParseScripts(t *testing.T) {
	list, err := ParseScripts([]byte(composerJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("scripts %+v", list)
	}
	lint, nested, test := list[0], list[1], list[2]
	if lint.Name != "lint" || lint.Line != 6 || !slices.Equal(lint.Commands, []string{"@php -l src", "phpcs"}) {
		t.Fatalf("lint %+v", lint)
	}
	if nested.Commands != nil || nested.Line != 7 {
		t.Fatalf("nested %+v", nested)
	}
	if test.Line != 5 || test.Description != "Run the tests" || !slices.Equal(test.Commands, []string{"phpunit"}) {
		t.Fatalf("test %+v", test)
	}
	if l, err := ParseScripts([]byte(`{"name":"x"}`)); err != nil || len(l) != 0 {
		t.Fatalf("no scripts %v %v", l, err)
	}
	if _, err := ParseScripts([]byte(`{`)); err == nil {
		t.Fatal("broken json")
	}
	// Line lookup tolerates odd documents.
	if m := scriptLines([]byte(`[1]`)); len(m) != 0 {
		t.Fatal("array")
	}
	if m := scriptLines([]byte(`{"scripts": 3}`)); len(m) != 0 {
		t.Fatal("scripts not an object")
	}
	if m := scriptLines([]byte(`{"a": {"b": [1, {"c": 2}]}, "scripts": {"x": "y"}}`)); m["x"] != 1 {
		t.Fatalf("after nested value %v", m)
	}
	if err := skipValue(json.NewDecoder(strings.NewReader(`[1`))); err == nil {
		t.Fatal("truncated")
	}
}

// fakeComposer writes a shell script that prints its arguments and the
// interaction variable.
func fakeComposer(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	p := filepath.Join(dir, "fake-composer")
	modtest.WriteFile(t, p, "#!/bin/sh\necho \"args: $*\"\necho \"nointeraction: $COMPOSER_NO_INTERACTION\"\nexit 3\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func setSetting(t *testing.T, a *api.App, key string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: key, Scope: "project", Value: b}, nil)
}

func waitRun(t *testing.T, a *api.App, id string) (run.Info, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info run.Info
	if err := api.NewInProcess(a).Call(ctx, "run.wait", api.RunIDParams{RunID: id}, &info); err != nil {
		t.Fatal(err)
	}
	var pg run.Page
	modtest.Call(t, a, "run.output", api.RunOutputParams{RunID: id}, &pg)
	var sb strings.Builder
	for _, l := range pg.Lines {
		sb.WriteString(l.Text + "\n")
	}
	return info, sb.String()
}

func TestModule(t *testing.T) {
	m := New()
	a := modtest.NewApp(t, []module.Module{m}, nil)
	modtest.WantCode(t, a, "composer.scripts", nil, api.CodeNoProject)
	root := modtest.Project(t, a, map[string]string{"composer.json": composerJSON})
	fake := fakeComposer(t, t.TempDir())

	var res ScriptsResult
	modtest.Call(t, a, "composer.scripts", nil, &res)
	if !res.Exists || len(res.Scripts) != 3 || res.Command != "composer" || res.File != filepath.Join(root, "composer.json") {
		t.Fatalf("scripts %+v", res)
	}

	// composer.run runs the fake with run-script, -- and the arguments.
	setSetting(t, a, SettingPath, fake)
	modtest.WantCode(t, a, "composer.run", RunParams{Script: "--version"}, api.CodeInvalidParams)
	// Names from composer.json with cmd.exe metacharacters (Windows: composer.bat).
	for _, bad := range []string{"a&calc", "x|y", "%PATH%", "a^b", "q\"r"} {
		modtest.WantCode(t, a, "composer.run", RunParams{Script: bad}, api.CodeInvalidParams)
	}
	modtest.WantCode(t, a, "composer.run", RunParams{}, api.CodeInvalidParams)
	var info run.Info
	modtest.Call(t, a, "composer.run", RunParams{Script: "test", Args: []string{"--filter", "a b"}, Interpreter: run.InterpreterHost}, &info)
	if info.Type != TypeScript || info.Name != "composer test" {
		t.Fatalf("info %+v", info)
	}
	end, out := waitRun(t, a, info.ID)
	if end.ExitCode != 3 || !strings.Contains(out, "args: run-script test -- --filter a b") || !strings.Contains(out, "nointeraction: 1") {
		t.Fatalf("run %+v %q", end, out)
	}

	// Configuration type with suggestions, saved and started; without a
	// provider of run.interpreter "project" runs on the host.
	var types []api.RunConfigTypeInfo
	modtest.Call(t, a, "run.configTypes", nil, &types)
	i := slices.IndexFunc(types, func(t api.RunConfigTypeInfo) bool { return t.ID == TypeScript })
	if i < 0 || types[i].Plugin != ID || !slices.Equal(types[i].Suggestions[FieldScript], []string{"lint", "nested", "test"}) ||
		len(types[i].Suggestions[FieldInterp]) != 2 {
		t.Fatalf("types %+v", types)
	}
	var saved run.Config
	modtest.Call(t, a, "run.configs.save", run.Config{Name: "Lint", Type: TypeScript, Values: map[string]any{FieldScript: "lint", FieldArgs: "-v", FieldEnv: []string{"X=1"}}}, &saved)
	modtest.Call(t, a, "run.start", api.RunStartParams{Config: "Lint"}, &info)
	end, out = waitRun(t, a, info.ID)
	if info.Name != "Lint" || info.Spec.Interpreter != run.InterpreterProject || !strings.Contains(out, "args: run-script lint -- -v") {
		t.Fatalf("config run %+v %q", info, out)
	}
	_ = end
	cfg := run.Config{Name: "Bad", Type: TypeScript, Values: map[string]any{FieldScript: "x", FieldArgs: `"open`}}
	if _, err := m.configType().Build(context.Background(), cfg, run.BuildEnv{ProjectDir: root}); err == nil {
		t.Fatal("bad arguments")
	}
	if got := m.configType().Suggest(context.Background(), run.BuildEnv{ProjectDir: root}, "other"); got != nil {
		t.Fatalf("other suggestions %v", got)
	}

	// A project-local composer.phar runs with php; a relative program
	// on the host is relative to the project.
	setSetting(t, a, SettingPath, "")
	modtest.WriteFile(t, filepath.Join(root, pharName), "<?php")
	spec, err := m.buildSpec(root, "test", nil, run.InterpreterProject, nil)
	if err != nil || !spec.PHP || !slices.Equal(spec.Args, []string{"composer.phar", "run-script", "test"}) {
		t.Fatalf("phar spec %+v %v", spec, err)
	}
	modtest.Call(t, a, "composer.scripts", nil, &res)
	if res.Command != "php composer.phar" {
		t.Fatalf("phar command %q", res.Command)
	}
	setSetting(t, a, SettingPath, "tools/composer")
	spec, _ = m.buildSpec(root, "test", nil, run.InterpreterHost, nil)
	if spec.Program != filepath.Join(root, "tools", "composer") || spec.PHP {
		t.Fatalf("host relative %+v", spec)
	}
	spec, _ = m.buildSpec(root, "test", nil, "", nil)
	if spec.Program != "tools/composer" || spec.Interpreter != run.InterpreterProject {
		t.Fatalf("interpreter relative %+v", spec)
	}

	// Without composer.json: no scripts, no error.
	if err := os.Remove(filepath.Join(root, "composer.json")); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "composer.scripts", nil, &res)
	if res.Exists || len(res.Scripts) != 0 {
		t.Fatalf("no file %+v", res)
	}
	if l, err := Scripts(root); l != nil || err != nil {
		t.Fatal("Scripts without file")
	}
	modtest.WriteFile(t, filepath.Join(root, "composer.json"), "{")
	modtest.WantCode(t, a, "composer.scripts", nil, api.CodeFailed)

	// Switched off: the API reports the disabled module and the stored
	// configuration stays, unavailable.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "composer.scripts", nil, api.CodeModuleDisabled)
	var cfgs []api.RunConfigInfo
	modtest.Call(t, a, "run.configs.list", nil, &cfgs)
	if len(cfgs) != 1 || cfgs[0].Available {
		t.Fatalf("configs with plugin off %+v", cfgs)
	}
}

func TestSettingSpecs(t *testing.T) {
	m := New()
	for _, s := range m.SettingSpecs() {
		if err := s.Validate(); err != nil || !strings.HasPrefix(s.Key, ID+".") {
			t.Fatalf("%+v %v", s, err)
		}
	}
	if m.ID() != ID || m.DependsOn() != nil || m.Stop() != nil || len(m.Claims()) != 1 {
		t.Fatal("module basics")
	}
	if setting(m, SettingPath, "def") != "def" {
		t.Fatal("setting without app")
	}
}
