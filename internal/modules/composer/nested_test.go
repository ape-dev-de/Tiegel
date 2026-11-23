package composer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// The scripts and runs come from the PHP project root (D515).
func TestNestedPHPRoot(t *testing.T) {
	m := New()
	a := modtest.NewApp(t, []module.Module{m}, nil)
	root := modtest.Project(t, a, map[string]string{"project/composer.json": composerJSON, "README.md": "x"})
	base := filepath.Join(root, "project")
	var res ScriptsResult
	modtest.Call(t, a, "composer.scripts", nil, &res)
	if !res.Exists || len(res.Scripts) != 3 || res.File != filepath.Join(base, "composer.json") {
		t.Fatalf("scripts %+v", res)
	}
	fake := fakeComposer(t, t.TempDir())
	setSetting(t, a, SettingPath, fake)
	var info run.Info
	modtest.Call(t, a, "composer.run", RunParams{Script: "test", Interpreter: run.InterpreterHost}, &info)
	if info.Spec.Dir != base {
		t.Fatalf("dir %s", info.Spec.Dir)
	}
	end, out := waitRun(t, a, info.ID)
	if end.ExitCode != 3 || !strings.Contains(out, "args: run-script test") {
		t.Fatalf("run %+v %q", end, out)
	}
	spec, err := m.configType().Build(context.Background(), run.Config{Name: "T", Type: TypeScript, Values: map[string]any{FieldScript: "lint"}}, run.BuildEnv{ProjectDir: root})
	if err != nil || spec.Dir != base {
		t.Fatalf("config spec %+v %v", spec, err)
	}
	if got := m.configType().Suggest(context.Background(), run.BuildEnv{ProjectDir: root}, FieldScript); len(got) != 3 {
		t.Fatalf("suggestions %v", got)
	}
	// Another directory than the open project keeps its own root.
	if m.phpRoot("/elsewhere") != "/elsewhere" {
		t.Fatal("foreign dir")
	}
}
