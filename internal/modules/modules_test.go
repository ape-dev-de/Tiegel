package modules

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestInstall(t *testing.T) {
	opts := modtest.Options(t)
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var list []api.ModuleInfo
	modtest.Call(t, a, "modules.list", nil, &list)
	want := []string{"php-runtime", "php-features", "lang-php", "git", "debug-xdebug", "syntax", "php-index", "docker"}
	if len(list) != len(want) {
		t.Fatalf("modules %+v", list)
	}
	for i, m := range list {
		if m.ID != want[i] || !m.Running {
			t.Fatalf("module %d: %+v", i, m)
		}
	}
	// Dependencies are enforced live.
	modtest.WantCode(t, a, "modules.disable", api.ModuleParams{ID: "php-runtime"}, api.CodeFailed)
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: "debug-xdebug"}, nil)
	modtest.WantCode(t, a, "debug.sessions", nil, api.CodeModuleDisabled)
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: "debug-xdebug"}, nil)
	modtest.Call(t, a, "debug.sessions", nil, nil)
	if err := Install(a); err == nil {
		t.Fatal("second install accepted")
	}
}
