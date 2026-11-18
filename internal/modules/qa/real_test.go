package qa

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/qa/qakit"
)

// TestRealTools runs every tool that is installed on this machine (PATH)
// on the host: the formatter's result and the analyzer's report must be
// readable. Tools that are not installed are skipped; nothing is
// downloaded.
func TestRealTools(t *testing.T) {
	if testing.Short() {
		t.Skip("real tools")
	}
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not installed")
	}
	ran := 0
	for _, p := range All() {
		def := p.Definition()
		progs := []string{def.Program}
		if def.FormatProgram != "" {
			progs = append(progs, def.FormatProgram)
		}
		paths := map[string]string{}
		for _, prog := range progs {
			if found, err := exec.LookPath(prog); err == nil {
				paths[prog] = found
			}
		}
		if len(paths) != len(progs) {
			continue
		}
		ran++
		t.Run(def.ID, func(t *testing.T) {
			a := modtest.NewApp(t, []module.Module{qakit.New(def)}, nil)
			modtest.Project(t, a, map[string]string{
				"src/A.php": "<?php\n\nnamespace App;\n\nclass A\n{\n    public function run($x)\n    {\n        $unused = array(1,2);\n        return $x;\n    }\n}\n",
			})
			setRaw := func(key string, v any) {
				raw, _ := json.Marshal(v)
				modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: def.ID + "." + key, Scope: "project", Value: raw}, nil)
			}
			setRaw("useInterpreter", false)
			setRaw("path", paths[def.Program])
			if def.FormatProgram != "" {
				setRaw("formatPath", paths[def.FormatProgram])
			}
			setRaw("timeout", 300)
			if def.Format != nil {
				var r api.QAFormatResult
				modtest.Call(t, a, "qa.format", api.QAFormatParams{Tool: def.ID, Path: "src/A.php", Apply: new(bool)}, &r)
				if r.Text == "" {
					t.Fatalf("empty result %+v", r)
				}
			}
			if def.Analyze != nil {
				var r api.QARunResult
				modtest.Call(t, a, "qa.run", api.QARunParams{Tool: def.ID, Paths: []string{"src"}}, &r)
				t.Logf("%s: %d findings", def.ID, r.Count)
			}
		})
	}
	if ran == 0 {
		t.Skip("no quality tool installed on PATH")
	}
}
