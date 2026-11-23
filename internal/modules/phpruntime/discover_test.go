package phpruntime

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// monorepo opens clients/app of a repository whose compose files are two
// levels up (shared volumes through an anchor, a builder service).
func monorepo(t *testing.T) (*api.App, string, string) {
	t.Helper()
	a, _ := newApp(t, []string{t.TempDir()}, Options{Docker: "/nonexistent/docker"})
	top := modtest.Project(t, a, map[string]string{
		"docker-compose.yaml": `x-vol: &vols
  - ./clients/app/project/:/var/www/project:rw
services:
  app-builder:
    build:
      context: ./clients/app
    image: app:latest
    entrypoint: ['true']
    volumes: *vols
  app:
    image: app:latest
    environment:
      XDEBUG_MODE: debug
    volumes: *vols
  cli:
    image: php:8.3-cli
    volumes: *vols
  db:
    image: mariadb:11
`,
		"docker-compose.override.yaml":      "services:\n  app:\n    environment:\n      XDEBUG_MODE: 'debug,develop'\n",
		"docker-compose.extra.yaml":         "services:\n  extra:\n    image: php:8.3\n",
		"clients/app/Dockerfile":            "FROM php:7.4-apache AS local\n",
		"clients/app/project/composer.json": "{}\n",
	})
	project := filepath.Join(top, "clients", "app")
	if _, err := a.OpenProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	return a, top, project
}

func TestComposeDiscoveryAncestor(t *testing.T) {
	a, top, project := monorepo(t)
	php := filepath.Join(project, "project")

	var det ComposeResult
	modtest.Call(t, a, "compose.detect", ComposeParams{}, &det)
	if !det.Found || det.Source != "discovered" || det.Dir != top || det.Relation != "ancestor" || det.Levels != 2 || len(det.Overlays) != 1 {
		t.Fatalf("detect %+v", det)
	}
	best := det.Candidates[0]
	if best.Service != "app" || best.Mount != "covers" || best.ProjectRemote != "/var/www/project" || best.ID != "compose:"+det.Name+":app" {
		t.Fatalf("best %+v", best)
	}
	for _, h := range best.XdebugHints {
		if h.Code == "project-not-mapped" {
			t.Fatalf("hint for a mounted PHP root: %+v", h)
		}
	}

	var disc DiscoverResult
	modtest.Call(t, a, "compose.discover", DiscoverParams{}, &disc)
	if len(disc.Sets) != 1 || disc.PHPRoot != php || disc.Sets[0].Configured || disc.Sets[0].Candidates[0].Service != "app" {
		t.Fatalf("discover %+v", disc)
	}

	// Not configured: interp.list lists them only with discover.
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", InterpListParams{}, &list)
	for _, it := range list {
		if it.Kind == "compose" {
			t.Fatalf("compose listed without configuration: %+v", it)
		}
	}
	modtest.Call(t, a, "interp.list", InterpListParams{Discover: true}, &list)
	var app, img *InterpreterInfo
	for i := range list {
		switch list[i].ID {
		case best.ID:
			app = &list[i]
		case "docker:php:8.3-cli":
			img = &list[i]
		}
	}
	if app == nil || !app.Discovered || app.Relation != "ancestor" || len(app.Files) != 2 || len(app.Mappings) == 0 {
		t.Fatalf("discovered app %+v in %+v", app, list)
	}
	if img == nil || img.Remote != "/var/www/project" || !img.Discovered {
		t.Fatalf("image %+v", img)
	}

	// The interpreter candidates recommend the service.
	var cands api.CandidatesResult
	modtest.Call(t, a, "settings.candidates", api.CandidatesParams{Key: SettingInterpreter}, &cands)
	if len(cands.Candidates) == 0 || cands.Candidates[0].Value != best.ID || !cands.Candidates[0].Recommended || !strings.Contains(cands.Candidates[0].Detail, "not configured") {
		t.Fatalf("candidates %+v", cands)
	}

	// Selecting the discovered ID configures the files (one action).
	var sel InterpreterInfo
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: best.ID}, &sel)
	if sel.ID != best.ID {
		t.Fatalf("select %+v", sel)
	}
	files := settings.Get(a.ProjectSettings(), api.SettingComposeFiles, []string{})
	if !reflect.DeepEqual(files, []string{"../../docker-compose.yaml", "../../docker-compose.override.yaml"}) {
		t.Fatalf("stored files %v", files)
	}
	var maps MappingsResult
	modtest.Call(t, a, "runtime.mappings.get", nil, &maps)
	if maps.Source != "compose" || maps.Service != "app" || len(maps.Mappings) == 0 || maps.Mappings[0].Host != php || maps.Mappings[0].Remote != "/var/www/project" {
		t.Fatalf("mappings %+v", maps)
	}
	modtest.Call(t, a, "compose.detect", ComposeParams{}, &det)
	if det.Source != "configured" || det.Dir != top {
		t.Fatalf("configured detect %+v", det)
	}
	modtest.Call(t, a, "interp.list", InterpListParams{Discover: true}, &list)
	n := 0
	for _, it := range list {
		if it.ID == best.ID {
			n++
			if it.Discovered || !it.Selected {
				t.Fatalf("configured entry %+v", it)
			}
		}
	}
	if n != 1 {
		t.Fatalf("listed %d times", n)
	}
	modtest.Call(t, a, "compose.discover", DiscoverParams{}, &disc)
	if !disc.Sets[0].Configured {
		t.Fatal("configured set not marked")
	}
}

func TestUseCompose(t *testing.T) {
	a, top, _ := monorepo(t)
	modtest.WantCode(t, a, "interp.useCompose", UseComposeParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.useCompose", UseComposeParams{Service: "nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "interp.useCompose", UseComposeParams{Service: "app", Files: []string{"missing.yaml"}}, api.CodeInvalidParams)
	var info InterpreterInfo
	modtest.Call(t, a, "interp.useCompose", UseComposeParams{Service: "cli", Files: []string{filepath.Join(top, "docker-compose.yaml"), filepath.Join(top, "docker-compose.extra.yaml")}}, &info)
	if !strings.HasSuffix(info.ID, ":cli") || !info.Selected {
		t.Fatalf("use %+v", info)
	}
	if files := settings.Get(a.ProjectSettings(), api.SettingComposeFiles, []string{}); len(files) != 2 || files[1] != "../../docker-compose.extra.yaml" {
		t.Fatalf("files %v", files)
	}
	// Unknown compose IDs stay unknown.
	modtest.WantCode(t, a, "interp.select", InterpSelectParams{ID: "compose:other:app"}, api.CodeNotFound)
	modtest.WantCode(t, a, "interp.select", InterpSelectParams{ID: "compose:broken"}, api.CodeNotFound)
}

func TestUseComposeRootFilesClearSetting(t *testing.T) {
	a, _ := newApp(t, []string{t.TempDir()}, Options{Docker: "/nonexistent/docker"})
	modtest.Project(t, a, map[string]string{"compose.yaml": "services:\n  php:\n    image: php:8.3\n    volumes:\n      - ./:/app\n"})
	if err := settings.Set(a.ProjectSettings(), api.SettingComposeFiles, []string{"compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	var info InterpreterInfo
	modtest.Call(t, a, "interp.useCompose", UseComposeParams{Service: "php"}, &info)
	if _, ok := a.ProjectSettings().Raw(api.SettingComposeFiles); ok {
		t.Fatal("root default files stored")
	}
	var disc DiscoverResult
	modtest.Call(t, a, "compose.discover", DiscoverParams{}, &disc)
	if len(disc.Sets) != 1 || disc.Sets[0].Relation != "root" || !disc.Sets[0].Configured {
		t.Fatalf("discover %+v", disc)
	}
	modtest.Call(t, a, "project.close", nil, nil)
	modtest.WantCode(t, a, "compose.discover", DiscoverParams{}, api.CodeNoProject)
	modtest.WantCode(t, a, "interp.useCompose", UseComposeParams{Service: "php"}, api.CodeNoProject)
}

func TestInterpreterCandidatesFallback(t *testing.T) {
	list := []InterpreterInfo{{ID: "local:/a", Label: "php", Kind: "local", Path: "/a", Version: "8.3"}, {ID: "docker:php", Label: "img", Kind: "docker", Selected: true}}
	c := interpreterCandidates(list)
	if !c[0].Recommended || c[0].Label != "php 8.3" || !c[1].Current {
		t.Fatalf("%+v", c)
	}
}
