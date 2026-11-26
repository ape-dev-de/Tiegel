package lspplugins

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestPluginsContributeServersAndRecipes(t *testing.T) {
	var mods []module.Module
	for _, p := range All() {
		mods = append(mods, p)
	}
	a := modtest.NewApp(t, mods, nil)
	modtest.Project(t, a, map[string]string{"a.php": "<?php\n"})
	ctx := context.Background()

	decls, err := api.HookLSPServers.Apply(ctx, a.Hooks(), nil)
	if err != nil || len(decls) != 3 {
		t.Fatalf("decls %+v %v", decls, err)
	}
	byID := map[string]api.LSPServerDecl{}
	for _, d := range decls {
		byID[d.ID] = d
	}
	pa := byID["phpactor"]
	if pa.Plugin != PhpactorID || !pa.AutoSelect || pa.Args != nil || pa.Path != "" || pa.PHPVersionOption != "php.version" {
		t.Fatalf("phpactor %+v", pa)
	}
	if in := byID["intelephense"]; in.AutoSelect || !in.Proprietary || !strings.Contains(in.Note, "proprietary") {
		t.Fatalf("intelephense %+v", in)
	}

	// Settings: project overrides global; invalid initOptions are ignored.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(settings.Set(a.Settings(), "lsp-phpactor.path", "/global/phpactor"))
	must(settings.Set(a.ProjectSettings(), "lsp-phpactor.path", "/project/phpactor"))
	must(settings.Set(a.Settings(), "lsp-phpactor.args", []string{"language-server", "-vvv"}))
	must(settings.Set(a.ProjectSettings(), "lsp-phpactor.initOptions", `{"language_server_phpstan.enabled": true}`))
	must(settings.Set(a.ProjectSettings(), "lsp-phpantom.initOptions", `{broken`))
	decls, _ = api.HookLSPServers.Apply(ctx, a.Hooks(), nil)
	for _, d := range decls {
		byID[d.ID] = d
	}
	pa = byID["phpactor"]
	if pa.Path != "/project/phpactor" || !reflect.DeepEqual(pa.Args, []string{"language-server", "-vvv"}) || pa.InitOptions["language_server_phpstan.enabled"] != true {
		t.Fatalf("settings %+v", pa)
	}
	if byID["phpantom"].InitOptions != nil {
		t.Fatal("broken initOptions used")
	}

	recipes, err := api.HookInstallRecipes.Apply(ctx, a.Hooks(), nil)
	if err != nil || len(recipes) != 3 {
		t.Fatalf("recipes %+v %v", recipes, err)
	}
	for _, r := range recipes {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	// Through the kernel service: plan only, nothing runs.
	var plan install.Plan
	modtest.Call(t, a, "install.plan", api.InstallPlanParams{Plugin: PhpactorID}, &plan)
	if !strings.Contains(plan.Display, "phpactor.phar") || !strings.HasSuffix(plan.Target, "phpactor") || plan.Scope != install.ScopeGlobal {
		t.Fatalf("plan %+v", plan)
	}
	modtest.Call(t, a, "install.plan", api.InstallPlanParams{Plugin: PHPantomID}, &plan)
	if plan.Available || !strings.Contains(plan.Hint, "does not download") {
		t.Fatalf("phpantom plan %+v", plan)
	}

	// A disabled plugin contributes nothing.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: IntelephenseID}, nil)
	decls, _ = api.HookLSPServers.Apply(ctx, a.Hooks(), nil)
	if len(decls) != 2 {
		t.Fatalf("after disable %+v", decls)
	}
	modtest.WantCode(t, a, "install.plan", api.InstallPlanParams{Plugin: IntelephenseID}, api.CodeNotFound)
}

func TestSpecsAndRegistration(t *testing.T) {
	for _, p := range All() {
		if p.ID() != "lsp-"+p.Definition().ID || p.DependsOn() != nil || p.Stop() != nil || p.Recipe().Plugin != p.ID() {
			t.Fatalf("plugin %s", p.ID())
		}
		for _, s := range p.SettingSpecs() {
			if err := s.Validate(); err != nil || !strings.HasPrefix(s.Key, p.ID()+".") {
				t.Fatalf("%s: %+v %v", p.ID(), s, err)
			}
		}
		if p.RegisterOption() == nil {
			t.Fatal("no register option")
		}
	}
	if !Phpactor().DefaultEnabled() || !PHPantom().DefaultEnabled() || Intelephense().DefaultEnabled() {
		t.Fatal("defaults")
	}
	// Without an App context the plugin refuses to start.
	r := module.NewRegistry(module.Env{})
	if err := r.Register(Phpactor(), module.DefaultEnabled()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err == nil {
		t.Fatal("started without App")
	}
	if lookup[string](nil, "x", "def") != "def" {
		t.Fatal("lookup without app")
	}
}
