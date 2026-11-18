package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// Programs from the repository (here a local interpreter in the project)
// need --allow-project-executables (review wave 8, finding 1).
func TestImportIDEProjectExecutables(t *testing.T) {
	dir := project(t, map[string]string{
		".idea/php.xml": `<project version="4"><component name="PhpInterpreters">
  <interpreters><interpreter name="repo php" home="$PROJECT_DIR$/bin/php" /></interpreters>
</component></project>`,
		"bin/php": "#!/bin/sh\n",
	})
	cfg := t.TempDir()
	code, out, errOut := run(t, "", "import-ide", "--no-app", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "executes a program from the repository") || !strings.Contains(out, "--allow-project-executables") {
		t.Fatalf("without flag: %d %q %q", code, out, errOut)
	}
	code, out, errOut = run(t, "", "--json", "import-ide", "--no-app", "--project", dir, "--config-dir", cfg, "--allow-project-executables")
	var rep api.IDEImportReport
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil {
		t.Fatalf("with flag: %d %q %q", code, out, errOut)
	}
	ok := false
	for _, it := range rep.Items {
		if it.Name == "PHP interpreter repo php" {
			ok = it.Status == ideimport.StatusImported && it.Executable != ""
		}
	}
	if !ok {
		t.Errorf("interpreter not imported with the flag: %+v", rep.Items)
	}
}
