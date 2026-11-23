//go:build !windows

package qa

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// The tools of a PHP project in a subfolder come from its vendor/bin and
// run there (D515).
func TestNestedPHPRoot(t *testing.T) {
	var mods []module.Module
	for _, p := range All() {
		mods = append(mods, p)
	}
	a := modtest.NewApp(t, mods, nil)
	dir := modtest.Project(t, a, map[string]string{
		"README.md":                  "x",
		"project/composer.json":      "{}",
		"project/a.php":              source,
		"project/vendor/bin/phpstan": fakePHPStan,
	})
	if err := os.Chmod(filepath.Join(dir, "project", "vendor", "bin", "phpstan"), 0o755); err != nil {
		t.Fatal(err)
	}
	var list []api.QAToolInfo
	modtest.Call(t, a, "qa.list", nil, &list)
	found := false
	for _, i := range list {
		if i.Plugin == PHPStanID {
			found = i.Available && i.Command == "vendor/bin/phpstan"
		}
	}
	if !found {
		t.Fatalf("phpstan not found in project/vendor/bin: %+v", list)
	}
	var res api.QARunResult
	modtest.Call(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"project/a.php"}}, &res)
	if res.Count != 1 || len(res.Files) != 1 || res.Files[0].Path != "project/a.php" {
		t.Fatalf("result %+v", res)
	}
}
