package modules

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/phpindex"
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
	want := []string{"php-runtime", "php-features", "lang-php", "lsp-phpactor", "lsp-phpantom", "lsp-intelephense", "git", "debug-xdebug", "syntax", "php-index", "docker", "phpunit", "composer",
		"qa-pint", "qa-php-cs-fixer", "qa-phpcs", "qa-phpstan", "qa-psalm", "qa-phpmd"}
	if len(list) != len(want) {
		t.Fatalf("modules %+v", list)
	}
	for i, m := range list {
		// The proprietary Intelephense plugin is opt-in.
		running := m.ID != "lsp-intelephense"
		if m.ID != want[i] || m.Running != running {
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

// Without a language server (switched off) navigation still works from
// the Tree-sitter index, and no server process is started.
func TestIndexNavigationWithoutServer(t *testing.T) {
	opts := modtest.Options(t)
	opts.LSP.PathEnv = t.TempDir() // nothing installed
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	modtest.Project(t, a, map[string]string{
		"src/A.php": "<?php\nnamespace App;\nclass A {}\n",
		"src/B.php": "<?php\nnamespace App;\nnew A();\n",
	})
	var st langphp.Status
	modtest.Call(t, a, "lsp.select", langphp.SelectParams{Server: "none"}, &st)
	if st.State != "off" || st.Running {
		t.Fatalf("lsp %+v", st)
	}
	var is phpindex.Status
	if err := modtest.TryCall(a, "index.status", phpindex.StatusParams{Wait: true}, &is); api.ErrorCode(err) == api.CodeUnavailable {
		t.Skip("index needs cgo (Tree-sitter)")
	}
	var res phpindex.NavResult
	modtest.Call(t, a, "nav.definition", phpindex.PosParams{Path: "src/B.php", At: api.Pos{Line: 3, Col: 5}}, &res)
	if len(res.Locations) != 1 || res.Locations[0].Path != "src/A.php" || res.LSP {
		t.Fatalf("definition %+v", res)
	}
	// Not found (auto, nothing installed): the reason is visible, navigation still works.
	modtest.Call(t, a, "lsp.select", langphp.SelectParams{Server: "auto"}, &st)
	if st.State != "not-found" || st.Reason == "" || len(st.Searched) == 0 {
		t.Fatalf("not found %+v", st)
	}
	modtest.Call(t, a, "nav.definition", phpindex.PosParams{Path: "src/B.php", At: api.Pos{Line: 3, Col: 5}}, &res)
	if len(res.Locations) != 1 {
		t.Fatalf("definition without server %+v", res)
	}
}
