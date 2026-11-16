package api

import (
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// interpOptions restricts interpreter discovery to dirs (none if nil).
func interpOptions(dirs []string) interp.DiscoverOptions {
	if dirs == nil {
		dirs = []string{}
	}
	return interp.DiscoverOptions{Dirs: dirs, Timeout: 5 * time.Second}
}

type fakeModule struct {
	id   string
	deps []string
}

func (m fakeModule) ID() string                 { return m.id }
func (m fakeModule) DependsOn() []string        { return m.deps }
func (m fakeModule) Start(module.Context) error { return nil }
func (m fakeModule) Stop() error                { return nil }

func TestModules(t *testing.T) {
	a := newTestApp(t)
	var list []ModuleInfo
	call(t, a, "modules.list", nil, &list)
	if len(list) != 0 {
		t.Fatal("no modules expected")
	}
	if err := a.Modules().Register(fakeModule{id: "php-runtime"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Modules().Register(fakeModule{id: "debug-xdebug", deps: []string{"php-runtime"}}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a, "modules.enable", ModuleParams{ID: "debug-xdebug"}, CodeFailed)
	var m ModuleInfo
	call(t, a, "modules.enable", ModuleParams{ID: "debug-xdebug", WithDependencies: true}, &m)
	if !m.Enabled || m.ID != "debug-xdebug" {
		t.Fatalf("enable %+v", m)
	}
	wantCode(t, a, "modules.disable", ModuleParams{ID: "php-runtime"}, CodeFailed)
	call(t, a, "modules.disable", ModuleParams{ID: "debug-xdebug"}, &m)
	if m.Enabled {
		t.Fatal("still enabled")
	}
	call(t, a, "modules.list", nil, &list)
	if len(list) != 2 || list[1].DependsOn[0] != "php-runtime" || !list[0].Enabled {
		t.Fatalf("list %+v", list)
	}
	wantCode(t, a, "modules.enable", ModuleParams{ID: "nope"}, CodeNotFound)
	if _, err := a.moduleResult("nope"); ErrorCode(err) != CodeNotFound {
		t.Fatal(err)
	}
}
