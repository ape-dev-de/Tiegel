package docker

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestCandidatesAndConfiguredComposeFiles(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	a, m := newApp(t, Options{Binary: f.Binary, Env: f.Env, CommonDirs: []string{filepath.Dir(f.Binary)}})
	top := modtest.Project(t, a, map[string]string{
		"docker-compose.yaml":              "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./app:/var/www\n",
		"docker-compose.override.arm.yaml": "services:\n  app:\n    platform: linux/arm64\n",
		"app/composer.json":                "{}",
	})
	if _, err := a.OpenProject(context.Background(), filepath.Join(top, "app")); err != nil {
		t.Fatal(err)
	}
	var res api.CandidatesResult
	modtest.Call(t, a, "settings.candidates", api.CandidatesParams{Key: api.SettingComposeFiles}, &res)
	if len(res.Candidates) != 2 || !res.Candidates[0].Recommended ||
		!reflect.DeepEqual(res.Candidates[0].Value, []any{"../docker-compose.yaml"}) || len(res.Candidates[1].Value.([]any)) != 2 {
		t.Fatalf("compose files %+v", res)
	}
	modtest.Call(t, a, "settings.candidates", api.CandidatesParams{Key: SettingPath}, &res)
	if len(res.Candidates) == 0 {
		t.Fatalf("docker path %+v", res)
	}
	modtest.Call(t, a, "settings.candidates", api.CandidatesParams{Key: SettingContext}, &res)
	if len(res.Candidates) != 4 || res.Candidates[0].Value != "orbstack" || !res.Candidates[0].Recommended {
		t.Fatalf("contexts %+v", res)
	}

	// Configured files are what the Services window loads.
	if p, err := m.composeProject(context.Background()); p != nil || err != nil {
		t.Fatalf("unconfigured %v %v", p, err)
	}
	if err := settings.Set(a.ProjectSettings(), api.SettingComposeFiles, []string{"../docker-compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	p, err := m.composeProject(context.Background())
	if err != nil || p == nil || p.Dir != top {
		t.Fatalf("configured %+v %v", p, err)
	}
	if _, ok := p.Service("app"); !ok {
		t.Fatal("service app missing")
	}
}
