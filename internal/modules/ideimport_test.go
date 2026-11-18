package modules

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fixtureFiles reads the synthetic project of internal/ideimport/testdata.
func fixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "ideimport", "testdata", "project")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestIDEImportWithPlugins imports the fixture configuration and project
// with all compiled-in modules: every plugin takes its components.
func TestIDEImportWithPlugins(t *testing.T) {
	opts := modtest.Options(t)
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	modtest.Project(t, a, fixtureFiles(t))
	from, _ := filepath.Abs(filepath.Join("..", "ideimport", "testdata", "config", "RefIDE2025.2"))
	params := api.IDEImportParams{From: from, Areas: []string{"all"}}

	var dry api.IDEImportReport
	params.DryRun = true
	modtest.Call(t, a, "import.ide.apply", params, &dry)
	ps := a.ProjectSettings()
	if _, ok := ps.Raw("qa-pint.config"); ok {
		t.Fatal("dry run wrote plugin settings")
	}
	// The tool path of the project's .idea points into the repository:
	// held back without the opt-in.
	held := false
	for _, it := range dry.Items {
		if it.Name == "PHP-CS-Fixer: tool" {
			held = it.Status == ideimport.StatusSkipped && it.Executable == "tools/php-cs-fixer"
		}
	}
	if !held {
		t.Error("the tool path from the repository was not held back without opt-in")
	}
	params.AllowProjectExecutables = true
	modtest.Call(t, a, "import.ide.apply", params, &dry)
	params.DryRun = false
	var rep api.IDEImportReport
	modtest.Call(t, a, "import.ide.apply", params, &rep)
	if rep.Counts.Failed != 0 || rep.Counts.Imported != dry.Counts.Ready {
		for _, it := range rep.Items {
			if it.Status == ideimport.StatusFailed {
				t.Errorf("failed: %+v", it)
			}
		}
		t.Fatalf("counts = %+v, dry run %+v", rep.Counts, dry.Counts)
	}
	byName := map[string]ideimport.Item{}
	for _, it := range rep.Items {
		byName[it.Name] = it
	}
	for name, plugin := range map[string]string{
		"PHPUnit settings": "phpunit", "Laravel Pint: tool": "qa-pint", "PHPStan: configuration": "qa-phpstan",
		"Xdebug port": "debug-xdebug", "Docker connection": "docker", "Git update method": "git",
		"Run configuration Unit": "phpunit",
	} {
		if it := byName[name]; it.Plugin != plugin || it.Status != ideimport.StatusImported {
			t.Errorf("%s = %+v", name, it)
		}
	}
	for name, status := range map[string]ideimport.Status{
		"PHPUnit version cache": ideimport.StatusSkipped, "Version control roots": ideimport.StatusSkipped,
		"PhpIncludePathManager": ideimport.StatusUnsupported, "SomeThirdPartyComponent": ideimport.StatusUnsupported,
		"Run configuration npm dev": ideimport.StatusUnsupported, "Run configuration All checks": ideimport.StatusUnsupported,
		"External tool Make Docs": ideimport.StatusImported,
	} {
		if it := byName[name]; it.Status != status {
			t.Errorf("%s = %+v, want %s", name, it, status)
		}
	}
	want := map[string]any{
		"phpunit.configuration":   "phpunit.xml",
		"qa-pint.useInterpreter":  true,
		"qa-pint.timeout":         30.0,
		"qa-pint.config":          "pint.json",
		"qa-pint.reformat":        true,
		"qa-phpstan.config":       "phpstan.neon",
		"qa-phpstan.timeout":      120.0,
		"qa-psalm.config":         "psalm.xml",
		"qa-php-cs-fixer.path":    "tools/php-cs-fixer",
		"qa-php-cs-fixer.timeout": 5.0,
		"qa-php-cs-fixer.config":  ".php-cs-fixer.dist.php",
		"qa-phpcs.config":         "phpcs.xml",
		"qa-phpmd.config":         "phpmd.xml",
		"git.updateMethod":        "rebase",
	}
	for k, v := range want {
		if got := settings.Get[any](ps, k, nil); got != v {
			t.Errorf("project %s = %v, want %v", k, got, v)
		}
	}
	if v := settings.Get(ps, "qa-php-cs-fixer.useInterpreter", true); v {
		t.Error("php-cs-fixer runs on the host")
	}
	for k, v := range map[string]any{"debug-xdebug.port": 9005.0, "docker.context": "orbstack", "git.updateMethod": "rebase"} {
		if got := settings.Get[any](a.Settings(), k, nil); got != v {
			t.Errorf("global %s = %v, want %v", k, got, v)
		}
	}
	var cfgs []api.RunConfigInfo
	modtest.Call(t, a, "run.configs.list", nil, &cfgs)
	got := map[string]api.RunConfigInfo{}
	for _, c := range cfgs {
		got[c.Name] = c
	}
	for name, scope := range map[string]string{"Unit": "suite", "FooTest::testBar": "method", "Unit directory": "path", "BarTest file": "path"} {
		c, ok := got[name]
		if !ok || c.Type != "phpunit" || c.Values["scope"] != scope || !c.Available {
			t.Errorf("run config %s = %+v", name, c)
		}
	}
	if c := got["FooTest::testBar"]; c.Values["class"] != `Tests\Feature\FooTest` || c.Values["method"] != "testBar" ||
		c.Values["path"] != "tests/Feature/FooTest.php" || c.Values["args"] != "--stop-on-failure" {
		t.Errorf("method config = %+v", c.Values)
	}
	if c := got["Unit directory"]; c.Values["path"] != "tests/Unit" || !strings.Contains(strings.Join(anyStrings(c.Values["env"]), ","), "XDEBUG_MODE=coverage") {
		t.Errorf("directory config = %+v", c.Values)
	}
	if _, ok := got["FooTest"]; ok {
		t.Error("temporary configuration imported")
	}
	if len(cfgs) != 6 {
		t.Errorf("configs = %d", len(cfgs))
	}
}

func anyStrings(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// pluginComponents are the reference IDE's component names the plugins
// import; the core (importer, kernel, headless, GUI) must not name them
// (D190, D191).
var pluginComponents = []string{
	"PhpUnit", "PhpTestFrameworkVersionCache", "PHPUnitRunConfigurationType",
	"LaravelPint", "PhpStan", "Psalm", "PhpCodeSniffer", "MessDetector", "PhpCSFixer",
	"PhpExternalFormatter", "PhpDebugGeneral", "DockerSettings", "DockerToolSettings",
	"VcsDirectoryMappings", "Git.Settings",
}

func TestCoreNamesNoPluginComponent(t *testing.T) {
	files := 0
	for _, dir := range []string{"../ideimport", "../api", "../headless", "../app", "../ui", "../core", "../run"} {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files++
			fset := token.NewFileSet()
			var s scanner.Scanner
			s.Init(fset.AddFile(p, -1, len(src)), src, nil, 0)
			for {
				pos, tok, lit := s.Scan()
				if tok == token.EOF {
					break
				}
				if tok != token.STRING {
					continue
				}
				for _, n := range pluginComponents {
					if strings.Contains(lit, n) {
						t.Errorf("%s: names plugin component %s", fset.Position(pos), n)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 100 {
		t.Fatalf("only %d files scanned", files)
	}
}

// A cloned repository's .idea must not set the global docker binary to a
// program inside the repository (review wave 8, finding 1), not even
// with the opt-in for programs from the repository.
func TestIDEImportProjectDockerPathNotGlobal(t *testing.T) {
	opts := modtest.Options(t)
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	modtest.Project(t, a, map[string]string{
		".idea/misc.xml": `<project version="4"><component name="DockerToolSettings">
  <option name="DOCKER_PATH" value="$PROJECT_DIR$/tools/docker" />
</component></project>`,
		"tools/docker": "#!/bin/sh\n",
	})
	if err := os.Chmod(filepath.Join(a.ProjectRoot(), "tools", "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	var rep api.IDEImportReport
	modtest.Call(t, a, "import.ide.apply", api.IDEImportParams{NoApp: true, Areas: []string{"all"}, AllowProjectExecutables: true}, &rep)
	if _, ok := a.Settings().Raw("docker.path"); ok {
		t.Fatalf("the project's .idea set the global docker.path: %+v", rep.Items)
	}
	if _, ok := a.ProjectSettings().Raw("docker.path"); ok {
		t.Fatal("docker.path written to the project")
	}
}
