package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// writeProject writes files (relative path → content) into a temporary
// project and opens it.
func writeProject(t *testing.T, a *App, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

const evilDockerIdea = `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="DockerToolSettings">
    <option name="DOCKER_PATH" value="$PROJECT_DIR$/tools/docker" />
  </component>
  <component name="ProjectTool">
    <option name="PATH" value="$PROJECT_DIR$/tools/lint" />
  </component>
  <component name="ProjectRunner">
    <option name="PATH" value="$PROJECT_DIR$/tools/lint" />
  </component>
</project>
`

// evilPlugin maps components like a careless plugin would: a global
// setting from whatever scope the component has (the docker handler of
// wave 8), a project tool path marked executable and an external tool
// run configuration.
func evilPlugin(a *App) error {
	HookIDEImportComponent.On(a.Hooks(), 0, func(_ context.Context, q IDEComponentQuery) (IDEComponentQuery, error) {
		c := q.Component
		if c.Kind != ideimport.KindComponent {
			return q, nil
		}
		path, _ := c.Element.Option("PATH")
		if path == "" {
			path, _ = c.Element.Option("DOCKER_PATH")
		}
		p := c.Macros.Path(path)
		var it ideimport.Item
		switch c.Name {
		case "DockerToolSettings":
			it = c.Item("Docker binary", p, ideimport.Setting("ui.dockerBinary", ideimport.ScopeGlobal, p))
		case "ProjectTool":
			ch := c.Setting("ui.projectTool", c.Path(path))
			ch.Executable = true
			it = c.Item("Project tool", p, ch)
		case "ProjectRunner":
			cfg := run.Config{Name: "Lint", Type: run.TypeExternalTool, Values: map[string]any{run.ToolFieldProgram: c.Path(path)}}
			it = ideimport.RunConfigItem(c, cfg, "")
		default:
			return q, nil
		}
		q.Items = append(slices.Clone(q.Items), it)
		q.Handled = true
		return q, nil
	}, hooks.WithOwner("evil"))
	return nil
}

// A cloned repository's .idea must not write global settings, and
// programs from the repository need the explicit opt-in (review wave 8,
// finding 1).
func TestIDEImportProjectCannotWriteGlobalOrRunRepoPrograms(t *testing.T) {
	a := newTestApp(t, evilPlugin)
	writeProject(t, a, map[string]string{
		".idea/misc.xml": evilDockerIdea,
		"tools/docker":   "#!/bin/sh\n",
		"tools/lint":     "#!/bin/sh\n",
	})
	params := IDEImportParams{NoApp: true, Areas: []string{"all"}}
	var rep IDEImportReport
	call(t, a, "import.ide.apply", params, &rep)
	if _, ok := a.Settings().Raw("ui.dockerBinary"); ok {
		t.Fatal("a project's .idea wrote a global setting")
	}
	if it := reportItem(t, rep, "Docker binary"); it.Status != ideimport.StatusFailed || !strings.Contains(it.Detail, "cannot change global settings") || len(it.Changes) != 0 {
		t.Errorf("global change from the project = %+v", it)
	}
	ps := a.ProjectSettings()
	for _, name := range []string{"Project tool", "Run configuration Lint"} {
		it := reportItem(t, rep, name)
		if it.Status != ideimport.StatusSkipped || it.Executable == "" || !strings.Contains(it.Detail, "executes a program from the repository") ||
			!strings.Contains(it.Detail, "allowProjectExecutables") {
			t.Errorf("%s without opt-in = %+v", name, it)
		}
	}
	if _, ok := ps.Raw("ui.projectTool"); ok {
		t.Error("program path imported without opt-in")
	}
	if list, _ := a.storedRunConfigs(); len(list) != 0 {
		t.Errorf("external tool from the project imported without opt-in: %+v", list)
	}
	// With the opt-in the project items are written; the global one is
	// still refused.
	params.AllowProjectExecutables = true
	call(t, a, "import.ide.apply", params, &rep)
	if it := reportItem(t, rep, "Project tool"); it.Status != ideimport.StatusImported || it.Executable != "tools/lint" {
		t.Errorf("project tool with opt-in = %+v", it)
	}
	if v := settings.Get(ps, "ui.projectTool", ""); v != "tools/lint" {
		t.Errorf("project tool = %q", v)
	}
	if list, _ := a.storedRunConfigs(); len(list) != 1 || list[0].Type != run.TypeExternalTool {
		t.Errorf("run configs with opt-in = %+v", list)
	}
	if _, ok := a.Settings().Raw("ui.dockerBinary"); ok {
		t.Fatal("opt-in let the project write a global setting")
	}
}

// A local interpreter named by the project's .idea is a program from the
// repository as well.
func TestIDEImportProjectInterpreterNeedsOptIn(t *testing.T) {
	a := newTestApp(t)
	writeProject(t, a, map[string]string{
		".idea/php.xml": `<project version="4"><component name="PhpInterpreters">
  <interpreters><interpreter name="repo php" home="$PROJECT_DIR$/bin/php" /></interpreters>
</component></project>`,
		"bin/php": "#!/bin/sh\n",
	})
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{NoApp: true}, &rep)
	it := reportItem(t, rep, "PHP interpreter repo php")
	if it.Status != ideimport.StatusSkipped || !strings.HasSuffix(filepath.ToSlash(it.Executable), "/bin/php") {
		t.Errorf("interpreter without opt-in = %+v", it)
	}
	if _, ok := a.ProjectSettings().Raw(SettingInterpreter); ok {
		t.Fatal("interpreter from the repository imported without opt-in")
	}
	call(t, a, "import.ide.apply", IDEImportParams{NoApp: true, AllowProjectExecutables: true}, &rep)
	if v := settings.Get(a.ProjectSettings(), SettingInterpreter, ""); !strings.HasPrefix(v, "local:") {
		t.Errorf("interpreter with opt-in = %q", v)
	}
}

// An interpreter the project only selects from the user's own
// application configuration needs no opt-in.
func TestIDEImportAppInterpreterTrusted(t *testing.T) {
	a := newTestApp(t)
	php := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(php, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(t.TempDir(), "RefIDE2030.1")
	if err := os.MkdirAll(filepath.Join(from, "options"), 0o755); err != nil {
		t.Fatal(err)
	}
	appPHP := `<application><component name="PhpInterpreters"><interpreters><interpreter name="system" home="` + filepath.ToSlash(php) + `" /></interpreters></component></application>`
	if err := os.WriteFile(filepath.Join(from, "options", "php.xml"), []byte(appPHP), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProject(t, a, map[string]string{
		".idea/workspace.xml": `<project version="4"><component name="PhpWorkspaceProjectConfiguration" interpreter_name="system" /></project>`,
	})
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{From: from, Areas: []string{ideimport.AreaInterpreter}}, &rep)
	if it := reportItem(t, rep, "PHP interpreter system"); it.Status != ideimport.StatusImported || it.Executable != "" {
		t.Errorf("app interpreter = %+v", it)
	}
	if v := settings.Get(a.ProjectSettings(), SettingInterpreter, ""); v != "local:"+php {
		t.Errorf("interpreter = %q", v)
	}
}

func TestIDEImportInsideProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "p")
	for prog, want := range map[string]bool{
		filepath.Join(root, "bin", "x"): true,
		"$ProjectFileDir$/bin/x":        true,
		"vendor/bin/phpstan":            true,
		"make":                          false,
		filepath.Join(root, "..", "x"):  false,
		filepath.Join(t.TempDir(), "x"): false,
	} {
		if got := ideInsideProject(prog, root); got != want {
			t.Errorf("inside(%q) = %v", prog, got)
		}
	}
	if ideInsideProject(filepath.Join(root, "x"), "") {
		t.Error("inside without a project")
	}
}

// Global settings are saved before an import changes them (review wave
// 8, finding 3).
func TestIDEImportBacksUpGlobalSettings(t *testing.T) {
	a := newTestApp(t)
	if err := settings.Set(a.Settings(), settings.KeyTheme, "light"); err != nil {
		t.Fatal(err)
	}
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t), DryRun: true}, &rep)
	if rep.Backup != "" {
		t.Errorf("dry run made a backup: %s", rep.Backup)
	}
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t)}, &rep)
	if v := settings.Get(a.Settings(), settings.KeyTheme, ""); v != "dark" {
		t.Fatalf("theme = %q", v)
	}
	if rep.Backup == "" || !slices.ContainsFunc(rep.Notes, func(n string) bool { return strings.Contains(n, rep.Backup) }) {
		t.Fatalf("no backup reported: %q %q", rep.Backup, rep.Notes)
	}
	data, err := os.ReadFile(rep.Backup)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if string(saved[settings.KeyTheme]) != `"light"` {
		t.Errorf("backup theme = %s", saved[settings.KeyTheme])
	}
	first := rep.Backup
	// Nothing global changes on the second run: no new backup.
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t)}, &rep)
	if rep.Backup != "" {
		t.Errorf("backup without changes: %s", rep.Backup)
	}
	if fi, err := os.Stat(first); err != nil || fi.Mode().Perm()&0o077 != 0 && filepath.Separator == '/' {
		t.Errorf("backup file = %v, %v", fi, err)
	}
}

// A run configuration of another type with the same name is kept; the
// import gets a new name (review wave 8, finding 7).
func TestIDEImportRunConfigOtherTypeKept(t *testing.T) {
	a := newTestApp(t)
	dir := copyFixture(t, filepath.Join("..", "ideimport", "testdata", "project"))
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	mine := run.Config{Name: "cache CLEAR", Type: run.TypeCommand, Values: map[string]any{"command": "make clear"}}
	call(t, a, "run.configs.save", mine, nil)
	list, _ := a.storedRunConfigs()
	mineID := list[0].ID
	params := IDEImportParams{NoApp: true, Areas: []string{ideimport.AreaRun}}
	var rep IDEImportReport
	call(t, a, "import.ide.scan", params, &rep)
	it := reportItem(t, rep, "Run configuration Cache clear")
	if it.Status != ideimport.StatusReady || !strings.Contains(it.Detail, `imported as "Cache clear (imported)"`) || !strings.Contains(it.Detail, run.TypeCommand) {
		t.Errorf("preview = %+v", it)
	}
	call(t, a, "import.ide.apply", params, &rep)
	list, _ = a.storedRunConfigs()
	byName := map[string]run.Config{}
	for _, c := range list {
		byName[c.Name] = c
	}
	if c := byName["cache CLEAR"]; c.ID != mineID || c.Type != run.TypeCommand {
		t.Errorf("own configuration replaced: %+v", list)
	}
	if c := byName["Cache clear (imported)"]; c.Type != run.TypePHPScript || c.ID == mineID {
		t.Errorf("imported configuration = %+v", list)
	}
	// Again: the earlier import is found, nothing changes.
	call(t, a, "import.ide.apply", params, &rep)
	if it := reportItem(t, rep, "Run configuration Cache clear"); it.Status != ideimport.StatusSkipped || !strings.Contains(it.Detail, "already set") {
		t.Errorf("again = %+v", it)
	}
	if list, _ := a.storedRunConfigs(); len(list) != 2 {
		t.Errorf("configs = %+v", list)
	}
	// The earlier import, changed, is updated in place.
	c := byName["Cache clear (imported)"]
	c.Values = map[string]any{"file": "other.php"}
	call(t, a, "run.configs.save", c, nil)
	call(t, a, "import.ide.apply", params, &rep)
	list, _ = a.storedRunConfigs()
	if len(list) != 2 || list[1].ID != c.ID || list[1].String("file", "") != "bin/console" {
		t.Errorf("update of the earlier import = %+v", list)
	}
}

func TestIDERunTarget(t *testing.T) {
	cfg := run.Config{Name: "Tests", Type: "phpunit"}
	list := []run.Config{{ID: "1", Name: "tests", Type: "phpunit"}}
	got, old, note := ideRunTarget(cfg, list)
	if got.ID != "1" || got.Name != "Tests" || old == nil || !strings.Contains(note, `replaces the existing configuration "tests" (phpunit)`) {
		t.Errorf("same type = %+v %v %q", got, old, note)
	}
	list = []run.Config{{ID: "1", Name: "Tests", Type: "composer-script"}, {ID: "2", Name: "Tests (imported)", Type: "command"}}
	got, old, note = ideRunTarget(cfg, list)
	if got.ID != "" || got.Name != "Tests (imported 2)" || old != nil || !strings.Contains(note, "composer-script") {
		t.Errorf("other types = %+v %v %q", got, old, note)
	}
	got, old, note = ideRunTarget(cfg, nil)
	if got.Name != "Tests" || old != nil || note != "" {
		t.Errorf("new = %+v %v %q", got, old, note)
	}
}

// External tools of the application configuration become external tool
// run configurations of the open project (review wave 8, finding 8).
func TestIDEImportExternalTools(t *testing.T) {
	a := newTestApp(t)
	writeProject(t, a, map[string]string{"index.php": "<?php\n"})
	params := IDEImportParams{From: fixtureConfig(t), Areas: []string{ideimport.AreaTools}}
	var rep IDEImportReport
	call(t, a, "import.ide.apply", params, &rep)
	it := reportItem(t, rep, "External tool Make Docs")
	if it.Status != ideimport.StatusImported || it.Executable != "" {
		t.Fatalf("external tool = %+v", it)
	}
	list, _ := a.storedRunConfigs()
	if len(list) != 1 {
		t.Fatalf("configs = %+v", list)
	}
	c := list[0]
	if c.Name != "Make Docs" || c.Type != run.TypeExternalTool || c.String(run.ToolFieldProgram, "") != "make" ||
		c.String(run.ToolFieldArgs, "") != "docs" || c.String(run.ToolFieldDescription, "") != "Builds the docs" {
		t.Errorf("config = %+v", c)
	}
	if _, ok := c.Values[run.ToolFieldDir]; ok {
		t.Errorf("project root as working directory stored: %+v", c.Values)
	}
}
