package compose

import (
	"reflect"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func services(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Service
	}
	return out
}

func hasReason(c Candidate, substr string) bool {
	for _, r := range c.Reasons {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

func TestDetectFixtures(t *testing.T) {
	cases := []struct {
		dir     string
		want    []string
		reasons []string
	}{
		{"sail", []string{"laravel.test"}, []string{"Laravel Sail image", "Laravel Sail runtime", `service name "laravel.test"`, "/var/www/html"}},
		{"fpm-nginx", []string{"app"}, []string{`Dockerfile builds FROM "php:8.2-fpm-alpine"`, `service name "app"`}},
		{"symfony", []string{"php"}, []string{"dunglas/frankenphp", `"app-php" suggests PHP`, "/app"}},
		{"multi", []string{"php"}, []string{"official PHP image", "XDEBUG_"}},
		{"composefile-env", []string{"worker"}, []string{"well-known PHP image"}},
		{"envinterp", []string{"fpm"}, []string{`official PHP image "php:8.1-fpm"`}},
	}
	for _, c := range cases {
		p := load(t, c.dir, LoadOptions{})
		got := Detect(p)
		if !reflect.DeepEqual(services(got), c.want) {
			t.Errorf("%s: candidates %v, want %v", c.dir, services(got), c.want)
			continue
		}
		for _, r := range c.reasons {
			if !hasReason(got[0], r) {
				t.Errorf("%s: missing reason %q in %v", c.dir, r, got[0].Reasons)
			}
		}
		if got[0].Score <= 0 || len(got[0].Mappings) == 0 {
			t.Errorf("%s: candidate %+v", c.dir, got[0])
		}
	}
}

func TestDetectOrdering(t *testing.T) {
	p := load(t, "envinterp", LoadOptions{Profiles: []string{"debug"}})
	got := Detect(p)
	if !reflect.DeepEqual(services(got), []string{"fpm", "debug"}) {
		t.Fatalf("order = %v", services(got))
	}
	if got[0].Score <= got[1].Score {
		t.Errorf("scores %d <= %d", got[0].Score, got[1].Score)
	}

	// Equal scores are ordered by name.
	dir := t.TempDir()
	write(t, dir, "compose.yaml", "services:\n  b:\n    image: php:8.3\n  a:\n    image: php:8.3\n")
	p = load(t, dir, LoadOptions{})
	if got := services(Detect(p)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("tie order = %v", got)
	}
}

func TestImageRepo(t *testing.T) {
	cases := map[string]string{
		"php":                               "php",
		"php:8.3-fpm":                       "php",
		"docker.io/library/php:8.3":         "php",
		"localhost/myapp:dev":               "myapp",
		"localhost:5000/team/php-app:1":     "team/php-app",
		"ghcr.io/acme/backend@sha256:abcd":  "acme/backend",
		"Bitnami/PHP-FPM:8.2":               "bitnami/php-fpm",
		"registry.example.com:443/php:8.3":  "php",
		"":                                  "",
		"serversideup/php:8.3-fpm-nginx":    "serversideup/php",
		"webdevops/php-apache-dev:8.2":      "webdevops/php-apache-dev",
		"thecodingmachine/php:8.3-v4-slim":  "thecodingmachine/php",
		"dunglas/frankenphp:1-php8.3":       "dunglas/frankenphp",
		"mysql/mysql-server:8.0":            "mysql/mysql-server",
		"phpmyadmin/phpmyadmin:latest":      "phpmyadmin/phpmyadmin",
		"example.com/custom-php-fpm:latest": "custom-php-fpm",
	}
	for in, want := range cases {
		if got := imageRepo(in); got != want {
			t.Errorf("imageRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImageScore(t *testing.T) {
	cases := map[string]int{
		"php:8.3-fpm":                    scoreOfficialImage,
		"sail-8.4/app":                   scoreKnownImage,
		"bitnami/php-fpm:8.2":            scoreKnownImage,
		"webdevops/php-nginx-dev:8.3":    scoreKnownImage,
		"serversideup/php:8.3-fpm-nginx": scoreKnownImage,
		"app-php":                        scoreNamedImage,
		"acme/php-runtime":               scoreNamedImage,
		"acme/api-php-fpm":               scoreNamedImage,
		"acme/myphpstack":                scoreWeakImage,
		"nginx:alpine":                   scoreNonPHPImage,
		"phpmyadmin/phpmyadmin":          scoreNonPHPImage,
		"node:20":                        scoreNonPHPImage,
		"acme/backend":                   0,
		"":                               0,
	}
	for in, want := range cases {
		if got, why := imageScore(in); got != want || (want != 0) != (why != "") {
			t.Errorf("imageScore(%q) = %d %q, want %d", in, got, why, want)
		}
	}
}

func TestDockerfileBases(t *testing.T) {
	df := `# syntax=docker/dockerfile:1
ARG PHP_VERSION=8.3
FROM composer:2 AS composer
from --platform=$BUILDPLATFORM php:${PHP_VERSION}-fpm as base
FROM base AS dev
FROM COMPOSER
`
	want := []string{"composer:2", "php:${PHP_VERSION}-fpm"}
	if got := dockerfileBases(df); !reflect.DeepEqual(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestScoreServiceBuildVariants(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docker/Dockerfile.php", "FROM serversideup/php:8.3-fpm\n")

	// Inline Dockerfile.
	c := ScoreService(dir, "worker", svcWith("", &types.BuildConfig{DockerfileInline: "FROM php:8.2-cli\n"}))
	if c.Score != scoreOfficialImage || !hasReason(c, "php:8.2-cli") {
		t.Errorf("inline: %+v", c)
	}
	// Custom dockerfile name, relative context; overrides a non-PHP image name.
	c = ScoreService(dir, "backend", svcWith("node:20", &types.BuildConfig{Context: "docker", Dockerfile: "Dockerfile.php"}))
	if c.Score != scoreKnownImage {
		t.Errorf("custom dockerfile: %+v", c)
	}
	// Missing Dockerfile and remote context are ignored.
	c = ScoreService(dir, "x", svcWith("", &types.BuildConfig{Context: "nope"}))
	if c.Score != 0 {
		t.Errorf("missing dockerfile: %+v", c)
	}
	c = ScoreService(dir, "x", svcWith("", &types.BuildConfig{Context: "https://example.com/repo.git"}))
	if c.Score != 0 {
		t.Errorf("remote context: %+v", c)
	}
}

func TestNameScore(t *testing.T) {
	cases := map[string]int{
		"php": scoreStrongName, "PHP-FPM": scoreStrongName, "laravel.test": scoreStrongName,
		"app": scoreWeakName, "php83": scoreWeakName, "db": 0,
	}
	for in, want := range cases {
		if got, _ := nameScore(in); got != want {
			t.Errorf("nameScore(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNginxWithProjectMountIsNotACandidate(t *testing.T) {
	p := load(t, "fpm-nginx", LoadOptions{})
	web, _ := p.Service("web")
	c := ScoreService(p.Dir, "web", web)
	if c.Score > 0 || !hasReason(c, "not a PHP runtime") || !hasReason(c, "mounts the project") {
		t.Errorf("web: %+v", c)
	}
}

func svcWith(image string, build *types.BuildConfig) types.ServiceConfig {
	var s types.ServiceConfig
	s.Image = image
	s.Build = build
	return s
}
