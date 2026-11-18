package headless

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// importFixture returns the synthetic configuration and a copy of the
// synthetic project (internal/ideimport/testdata).
func importFixture(t *testing.T) (from, dir string) {
	t.Helper()
	base := filepath.Join("..", "ideimport", "testdata")
	from, _ = filepath.Abs(filepath.Join(base, "config", "RefIDE2025.2"))
	files := map[string]string{}
	root := filepath.Join(base, "project")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return from, project(t, files)
}

func TestImportIDECommand(t *testing.T) {
	from, dir := importFixture(t)
	cfg := t.TempDir() // shared between the calls

	// Without a configuration (empty home) and without a project.
	code, _, errOut := run(t, "", "import-ide", "--config-dir", cfg)
	if code != ExitError || !strings.Contains(errOut, "nothing to import") {
		t.Fatalf("nothing: %d %q", code, errOut)
	}
	code, out, _ := run(t, "", "import-ide", "--sources", "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "no configuration") {
		t.Fatalf("sources: %d %q", code, out)
	}
	code, out, _ = run(t, "", "import-ide", "--sources", "--from", from)
	if code != 0 || !strings.Contains(out, "RefIDE2025.2") || !strings.Contains(out, "(PHP)") {
		t.Fatalf("sources from: %d %q", code, out)
	}

	// Dry run: report only.
	code, out, errOut = run(t, "", "import-ide", "--from", from, "--project", dir, "--config-dir", cfg, "--dry-run")
	if code != 0 || !strings.Contains(out, "would import") || !strings.Contains(out, "dry run:") || !strings.Contains(out, "[ ] Recent projects") {
		t.Fatalf("dry run: %d %q %q", code, out, errOut)
	}
	code, out, _ = run(t, "", "--json", "settings", "get", "ui.theme", "--config-dir", cfg)
	if code != 0 || strings.Contains(out, "dark") {
		t.Fatalf("dry run wrote the theme: %q", out)
	}

	// Import, JSON report.
	code, out, errOut = run(t, "", "--json", "import-ide", "--from", from, "--project", dir, "--config-dir", cfg, "--areas", "editor,appearance,runConfigurations,plugins")
	var rep api.IDEImportReport
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil {
		t.Fatalf("import: %d %q %q", code, out, errOut)
	}
	if rep.DryRun || rep.Counts.Imported == 0 || rep.Counts.Failed != 0 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
	for _, it := range rep.Items {
		if it.Area == ideimport.AreaKeymap && it.Status == ideimport.StatusImported {
			t.Errorf("keymap imported without being selected: %+v", it)
		}
	}
	code, out, _ = run(t, "", "settings", "get", "ui.theme", "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "dark") {
		t.Fatalf("theme: %d %q", code, out)
	}
	code, out, _ = run(t, "", "run-configs", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "Cache clear") || !strings.Contains(out, "FooTest::testBar") {
		t.Fatalf("run configs: %d %q", code, out)
	}

	// Usage errors.
	if code, _, _ := run(t, "", "import-ide", "--from", from, "--areas", "nope"); code != ExitUsage {
		t.Errorf("unknown area: %d", code)
	}
	if code, _, _ := run(t, "", "import-ide", "extra"); code != ExitUsage {
		t.Errorf("argument: %d", code)
	}
}
