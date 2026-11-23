package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// monorepo mirrors a real layout: compose files two levels above the
// opened project, volumes shared through a YAML anchor, a builder service
// with the Dockerfile and a runtime service that only names the image,
// override and optional overlay files.
func monorepo(t *testing.T) (top, project string) {
	t.Helper()
	top = t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docker-compose.yaml", `x-env: &env
  XDEBUG_MODE: debug
x-vol: &vols
  - ./clients/app/project/:/var/www/project:rw
  - ./certs/ca.pem:/etc/ca.pem:ro
services:
  app-builder:
    build:
      context: ./clients/app
      target: local
    image: app:latest
    entrypoint: ['true']
    environment:
      <<: *env
    volumes: *vols
  app:
    image: app:latest
    environment:
      <<: *env
    volumes: *vols
  app-setup:
    image: app:latest
    volumes: *vols
  other:
    build:
      context: ./clients/other
    volumes:
      - ./clients/other/project:/var/www/project
  db:
    image: mariadb:11
`)
	write("docker-compose.override.yaml", "services:\n  app:\n    environment:\n      XDEBUG_MODE: 'off'\n")
	write("docker-compose.override.arm.yaml", "services:\n  app:\n    platform: linux/arm64\n")
	write("docker-compose.extra.yaml", "services:\n  extra:\n    image: php:8.3\n")
	write("docker-compose.override.yaml.dist", "services: {}\n")
	write("clients/app/Dockerfile", "FROM registry.example.com/base/osip-php:7.4-apache AS local\n")
	write("clients/other/Dockerfile", "FROM php:8.2-fpm\n")
	write("clients/app/project/composer.json", "{}\n")
	write("clients/app/vendor/compose.yaml", "services:\n  nope:\n    image: php:8\n")
	return top, filepath.Join(top, "clients", "app")
}

func TestDiscoverAncestorMonorepo(t *testing.T) {
	top, project := monorepo(t)
	d, err := Discover(context.Background(), project, DiscoverOptions{PHPRoot: "project", Home: filepath.Dir(top)})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sets) != 1 {
		t.Fatalf("sets %+v", d.Sets)
	}
	s := d.Sets[0]
	if s.Dir != top || s.Relation != RelationAncestor || s.Levels != 2 || s.Error != "" {
		t.Fatalf("set %+v", s)
	}
	if len(s.Files) != 2 || !strings.HasSuffix(s.Files[1], "docker-compose.override.yaml") {
		t.Fatalf("files %v", s.Files)
	}
	if len(s.Overlays) != 2 || !strings.HasSuffix(s.Overlays[0], "docker-compose.extra.yaml") || !strings.HasSuffix(s.Overlays[1], "override.arm.yaml") {
		t.Fatalf("overlays %v", s.Overlays)
	}
	set, best, ok := d.Best()
	if !ok || set.Dir != top || best.Service != "app" {
		t.Fatalf("best %+v in %+v", best, s.Candidates)
	}
	want := filepath.Join(project, "project")
	if best.Mount != MountCovers || best.ProjectRemote != "/var/www/project" || best.Mapping == nil || best.Mapping.Host != want {
		t.Fatalf("mapping %+v", best)
	}
	if !hasReason(best.Candidate, "built by service") || !hasReason(best.Candidate, "mounts the PHP root") {
		t.Fatalf("reasons %v", best.Reasons)
	}
	byName := map[string]DiscoveredService{}
	for _, c := range s.Candidates {
		byName[c.Service] = c
	}
	if _, ok := byName["db"]; ok {
		t.Fatal("db listed")
	}
	if b := byName["app-builder"]; b.Score >= best.Score || !hasReason(b.Candidate, "one-shot") {
		t.Fatalf("builder %+v", b)
	}
	if b := byName["app-setup"]; b.Score >= best.Score || !hasReason(b.Candidate, "setup task") {
		t.Fatalf("setup %+v", b)
	}
	if o := byName["other"]; o.Mount != MountNone || !hasReason(o.Candidate, "does not mount") {
		t.Fatalf("other %+v", o)
	}
	if set.Project() == nil {
		t.Fatal("project not kept")
	}
}

func TestDiscoverPHPRootDefaultsAndInside(t *testing.T) {
	top, project := monorepo(t)
	// Without a PHP root the project root is used: the mount lies inside.
	d, err := Discover(context.Background(), project, DiscoverOptions{Home: filepath.Dir(top)})
	if err != nil {
		t.Fatal(err)
	}
	_, best, ok := d.Best()
	if !ok || best.Service != "app" || best.Mount != MountInside || best.ProjectRemote != "" {
		t.Fatalf("best %+v", best)
	}
	// Home stops the walk: nothing above the project.
	d, _ = Discover(context.Background(), project, DiscoverOptions{Home: filepath.Join(top, "clients")})
	if len(d.Sets) != 0 {
		t.Fatalf("home not respected: %+v", d.Sets)
	}
	d, _ = Discover(context.Background(), project, DiscoverOptions{Home: "/nonexistent", MaxUp: 1})
	if len(d.Sets) != 0 {
		t.Fatalf("MaxUp not respected: %+v", d.Sets)
	}
}

func TestDiscoverSubdirAndBroken(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docker", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(p, c string) {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "docker", "compose.yaml"), "services:\n  php:\n    image: php:8.3-fpm\n    volumes:\n      - ..:/app\n")
	must(filepath.Join(root, "docker", "dev", "compose.yml"), "services: [broken\n")
	must(filepath.Join(root, "node_modules", "x", "compose.yaml"), "services:\n  php:\n    image: php\n")
	d, err := Discover(context.Background(), root, DiscoverOptions{Home: filepath.Dir(root)})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sets) != 2 {
		t.Fatalf("sets %+v", d.Sets)
	}
	if s := d.Sets[0]; s.Relation != RelationSubdir || s.Levels != 1 || len(s.Candidates) != 1 || s.Candidates[0].ProjectRemote != "/app" {
		t.Fatalf("subdir %+v", s)
	}
	if s := d.Sets[1]; s.Error == "" || s.Levels != 2 {
		t.Fatalf("broken %+v", s)
	}
	if _, ok := d.Sets[1].Best(); ok {
		t.Fatal("broken set has a best candidate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, root, DiscoverOptions{}); err == nil {
		t.Fatal("cancelled discovery")
	}
}

func TestResolveFilesAndLoadResolved(t *testing.T) {
	top, project := monorepo(t)
	f, err := ResolveFiles(project, []string{"../../docker-compose.yaml", " "}, nil)
	if err != nil || f.Dir != top || len(f.Paths) != 1 {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := ResolveFiles(project, []string{"missing.yaml"}, nil); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := ResolveFiles(project, nil, nil); err == nil {
		t.Fatal("no compose file in project")
	}
	p, err := LoadResolved(context.Background(), project, []string{filepath.Join(top, "docker-compose.yaml")}, LoadOptions{})
	if err != nil || p.Dir != top {
		t.Fatalf("%v %v", p, err)
	}
	if _, ok := p.Service("app"); !ok {
		t.Fatal("app missing")
	}
	if _, err := LoadResolved(context.Background(), project, []string{"x.yaml"}, LoadOptions{}); err == nil {
		t.Fatal("missing accepted")
	}
	if Rank(nil, project) != nil {
		t.Fatal("nil project")
	}
}
