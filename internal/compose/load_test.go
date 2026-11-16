package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	if filepath.IsAbs(name) {
		return name
	}
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func load(t *testing.T, name string, opts LoadOptions) *Project {
	t.Helper()
	p, err := Load(context.Background(), fixture(t, name), opts)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return p
}

func bases(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

func TestFindFiles(t *testing.T) {
	cases := []struct {
		dir     string
		env     map[string]string
		want    []string
		fromEnv bool
	}{
		{"sail", nil, []string{"docker-compose.yml"}, false},
		{"symfony", nil, []string{"compose.yaml", "compose.override.yaml"}, false},
		// compose.override.yml wins over docker-compose.override.yaml.
		{"multi", nil, []string{"compose.yaml", "compose.override.yml"}, false},
		// COMPOSE_FILE from .env with a custom separator.
		{"composefile-env", nil, []string{"base.yml", "dev.yml"}, true},
		// Explicit environment beats .env.
		{"composefile-env", map[string]string{"COMPOSE_FILE": "base.yml"}, []string{"base.yml"}, true},
		{"multi", map[string]string{"COMPOSE_FILE": "compose.yaml"}, []string{"compose.yaml"}, true},
	}
	for _, c := range cases {
		f, err := FindFiles(fixture(t, c.dir), c.env)
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}
		if got := bases(f.Paths); !reflect.DeepEqual(got, c.want) || f.FromEnv != c.fromEnv {
			t.Errorf("%s %v: got %v fromEnv=%v, want %v %v", c.dir, c.env, got, f.FromEnv, c.want, c.fromEnv)
		}
		for _, p := range f.Paths {
			if !filepath.IsAbs(p) {
				t.Errorf("%s: path %q not absolute", c.dir, p)
			}
		}
	}
}

func TestFindFilesErrors(t *testing.T) {
	empty := t.TempDir()
	if _, err := FindFiles(empty, nil); !errors.Is(err, ErrNoComposeFile) {
		t.Errorf("empty dir: %v", err)
	}
	if _, err := Load(context.Background(), empty, LoadOptions{}); !errors.Is(err, ErrNoComposeFile) {
		t.Errorf("Load empty dir: %v", err)
	}
	if _, err := FindFiles(empty, map[string]string{"COMPOSE_FILE": "missing.yml"}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing COMPOSE_FILE entry: %v", err)
	}
	// Empty entries are skipped.
	dir := t.TempDir()
	write(t, dir, "a.yml", "services: {}\n")
	f, err := FindFiles(dir, map[string]string{"COMPOSE_FILE": "a.yml" + string(os.PathListSeparator)})
	if err != nil || len(f.Paths) != 1 {
		t.Errorf("trailing separator: %v %v", f.Paths, err)
	}
	// A broken .env is reported.
	bad := t.TempDir()
	write(t, bad, ".env", "FOO=\"unterminated\n")
	write(t, bad, "compose.yaml", "services: {}\n")
	if _, err := FindFiles(bad, nil); err == nil {
		t.Error("want error for broken .env")
	}
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadInterpolation(t *testing.T) {
	p := load(t, "envinterp", LoadOptions{})
	if p.Name != "envinterp" {
		t.Errorf("Name = %q", p.Name)
	}
	if got := p.ServiceNames(); !reflect.DeepEqual(got, []string{"fpm"}) {
		t.Errorf("enabled services = %v (debug profile must be disabled)", got)
	}
	fpm, ok := p.Service("fpm")
	if !ok || fpm.Image != "php:8.1-fpm" {
		t.Fatalf("fpm image = %q", fpm.Image)
	}
	m := ServiceMappings(p.Dir, fpm)
	if r, ok := m.ToRemote(filepath.Join(p.Dir, "src", "index.php")); !ok || r != "/srv/app/index.php" {
		t.Errorf("ToRemote = %q %v (mappings %v)", r, ok, m)
	}

	// Explicit environment overrides .env.
	p = load(t, "envinterp", LoadOptions{Env: map[string]string{"PHP_VERSION": "8.4"}, Name: "custom"})
	if fpm, _ := p.Service("fpm"); fpm.Image != "php:8.4-fpm" {
		t.Errorf("override image = %q", fpm.Image)
	}
	if p.Name != "custom" {
		t.Errorf("Name override = %q", p.Name)
	}
}

func TestLoadProfiles(t *testing.T) {
	p := load(t, "envinterp", LoadOptions{Profiles: []string{"debug"}})
	if got := p.ServiceNames(); !reflect.DeepEqual(got, []string{"debug", "fpm"}) {
		t.Errorf("services = %v", got)
	}
	if !reflect.DeepEqual(p.Profiles, []string{"debug"}) {
		t.Errorf("Profiles = %v", p.Profiles)
	}
	p = load(t, "fpm-nginx", LoadOptions{Profiles: []string{"*"}})
	if _, ok := p.Service("mailpit"); !ok {
		t.Error("mailpit should be enabled with profile *")
	}
}

func TestLoadSailDotEnv(t *testing.T) {
	p := load(t, "sail", LoadOptions{})
	svc, ok := p.Service("laravel.test")
	if !ok {
		t.Fatal("laravel.test missing")
	}
	if v := svc.Environment["XDEBUG_MODE"]; v == nil || *v != "develop,debug" {
		t.Errorf("XDEBUG_MODE = %v", v)
	}
	if v := svc.Environment["IGNITION_LOCAL_SITES_PATH"]; v == nil || *v != p.Dir {
		t.Errorf("PWD default not applied: %v", v)
	}
}

func TestLoadExplicitFilesAndErrors(t *testing.T) {
	dir := fixture(t, "multi")
	p, err := Load(context.Background(), dir, LoadOptions{Files: []string{filepath.Join(dir, "compose.yaml")}})
	if err != nil {
		t.Fatal(err)
	}
	php, _ := p.Service("php")
	if _, ok := php.Environment["XDEBUG_MODE"]; ok {
		t.Error("override must not be loaded when Files is explicit")
	}

	bad := t.TempDir()
	write(t, bad, "compose.yaml", "services:\n  php:\n    image: [unclosed\n")
	if _, err := Load(context.Background(), bad, LoadOptions{}); err == nil {
		t.Error("want error for invalid yaml")
	}
}

func TestNilProject(t *testing.T) {
	var p *Project
	if _, ok := p.Service("x"); ok {
		t.Error("nil project has no services")
	}
	if p.ServiceNames() != nil {
		t.Error("nil project names")
	}
	if Detect(p) != nil {
		t.Error("nil project candidates")
	}
}
