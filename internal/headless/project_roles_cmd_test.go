package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRolesCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"composer.json":     `{"autoload":{"psr-4":{"App\\":"lib/"}},"autoload-dev":{"classmap":["spec"]}}`,
		"lib/A.php":         "<?php\n",
		"spec/ATest.php":    "<?php\n",
		"public/index.php":  "<?php\n",
		"tests/unused.php":  "<?php\n",
		"vendor/autoload.x": "",
	})
	code, out, errs := run(t, "", "project-roles", "--project", dir)
	want := "source   lib (composer)\nresource public (name)\ntest     spec (composer)\nlibrary  vendor (composer)\n"
	if code != ExitOK || out != want {
		t.Fatalf("project-roles %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "project-roles", "--json", "--project", dir); code != ExitOK || !strings.Contains(out, `"role": "source"`) {
		t.Fatalf("json %d %q", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errs = run(t, "", "project-roles", "--project", dir); code != ExitOK || !strings.Contains(errs, "warning: composer.json") || !strings.Contains(out, "test     tests (name)") {
		t.Fatalf("invalid composer %d %q %q", code, out, errs)
	}
}
