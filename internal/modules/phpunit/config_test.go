package phpunit

import (
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
)

func TestConfigFor(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	for _, c := range []struct {
		sc    api.TestScope
		scope string
	}{
		{api.TestScope{Path: filepath.Join(root, "tests", "CalcTest.php"), Class: `Tests\CalcTest`, Method: "testPairs", DataSet: "1"}, scopeMethod},
		{api.TestScope{Path: "tests/CalcTest.php", Class: `Tests\CalcTest`}, scopeClass},
		{api.TestScope{Path: "tests"}, scopePath},
		{api.TestScope{Suite: "unit", Filter: "x"}, scopeSuite},
		{api.TestScope{}, scopeAll},
	} {
		var cfg run.Config
		modtest.Call(t, a, "test.configFor", c.sc, &cfg)
		if cfg.Type != ID || cfg.Values[fieldScope] != c.scope || cfg.Name == "" {
			t.Fatalf("%+v: %+v", c.sc, cfg)
		}
		if c.scope == scopeMethod && (cfg.Values[fieldPath] != "tests/CalcTest.php" || cfg.Values[fieldDataSet] != "1") {
			t.Fatalf("method %+v", cfg)
		}
		// It saves as is.
		modtest.Call(t, a, "run.configs.save", cfg, &cfg)
		modtest.Call(t, a, "run.configs.delete", api.RunConfigIDParams{ID: cfg.ID}, nil)
	}
}
