package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ape-dev-de/tiegel/internal/core/fspath"
)

// Candidate is a service that probably runs PHP.
type Candidate struct {
	// Service is the compose service name.
	Service string
	// Score ranks candidates; higher is more likely. Only positive scores
	// are reported.
	Score int
	// Reasons explain the score in plain English, for tooltips.
	Reasons []string
	// Image is the service image, if any.
	Image string
	// Mappings are derived from the service's bind mounts.
	Mappings Mappings
}

// Score weights of the heuristics.
const (
	scoreOfficialImage = 50
	scoreKnownImage    = 50
	scoreNamedImage    = 40
	scoreWeakImage     = 20
	scoreNonPHPImage   = -50
	scoreStrongName    = 25
	scoreWeakName      = 15
	scoreProjectMount  = 15
	scorePHPEnv        = 5
)

// knownPHPImages are repository prefixes of popular PHP images.
var knownPHPImages = []string{
	"bitnami/php-fpm",
	"webdevops/php",
	"thecodingmachine/php",
	"serversideup/php",
	"dunglas/frankenphp",
	"shinsenter/php",
	"laravelsail/php",
}

// nonPHPImages are repository names (last path component) of images that
// commonly sit next to PHP but never are the PHP runtime.
var nonPHPImages = map[string]bool{
	"nginx": true, "httpd": true, "apache": true, "caddy": true, "traefik": true,
	"mysql": true, "mysql-server": true, "mariadb": true, "postgres": true, "postgis": true,
	"redis": true, "valkey": true, "memcached": true, "mongo": true,
	"mailpit": true, "mailhog": true, "phpmyadmin": true, "adminer": true,
	"node": true, "selenium": true, "standalone-chrome": true, "minio": true,
	"meilisearch": true, "elasticsearch": true, "opensearch": true, "rabbitmq": true,
	"soketi": true, "typesense": true, "busybox": true, "alpine": true, "composer": true,
}

var sailImage = regexp.MustCompile(`^sail-[0-9.]+/app$`)

// imageRepo strips registry, tag and digest from an image reference and
// lowercases it: "docker.io/library/php:8.3-fpm" → "php".
func imageRepo(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		ref = ref[:i]
	}
	parts := strings.Split(ref, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		parts = parts[1:]
	}
	if len(parts) > 1 && parts[0] == "library" {
		parts = parts[1:]
	}
	return strings.Join(parts, "/")
}

// imageScore rates an image reference. It returns 0 and "" when the image
// says nothing about PHP.
func imageScore(ref string) (int, string) {
	// Sail names its local images "sail-8.3/app"; the dot would otherwise
	// make the first component look like a registry host.
	if sailImage.MatchString(strings.ToLower(strings.SplitN(ref, ":", 2)[0])) {
		return scoreKnownImage, fmt.Sprintf("Laravel Sail image %q", ref)
	}
	repo := imageRepo(ref)
	if repo == "" {
		return 0, ""
	}
	last := repo[strings.LastIndex(repo, "/")+1:]
	switch {
	case nonPHPImages[last]:
		return scoreNonPHPImage, fmt.Sprintf("image %q is not a PHP runtime", ref)
	case repo == "php":
		return scoreOfficialImage, fmt.Sprintf("official PHP image %q", ref)
	}
	for _, k := range knownPHPImages {
		if repo == k || strings.HasPrefix(repo, k) {
			return scoreKnownImage, fmt.Sprintf("well-known PHP image %q", ref)
		}
	}
	if last == "php" || last == "php-fpm" || last == "frankenphp" ||
		strings.HasPrefix(last, "php-") || strings.HasSuffix(last, "-php") || strings.HasSuffix(last, "-php-fpm") {
		return scoreNamedImage, fmt.Sprintf("image name %q suggests PHP", ref)
	}
	if strings.Contains(repo, "php") {
		return scoreWeakImage, fmt.Sprintf("image name %q mentions PHP", ref)
	}
	return 0, ""
}

var fromLine = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--\S+\s+)*(\S+)(?:\s+AS\s+(\S+))?`)

// dockerfileBases returns the external base images of a Dockerfile, skipping
// references to earlier build stages.
func dockerfileBases(content string) []string {
	stages := map[string]bool{}
	var bases []string
	for _, m := range fromLine.FindAllStringSubmatch(content, -1) {
		ref := m[1]
		if !stages[strings.ToLower(ref)] {
			bases = append(bases, ref)
		}
		if m[2] != "" {
			stages[strings.ToLower(m[2])] = true
		}
	}
	return bases
}

// buildScore inspects the build section of a service.
func buildScore(b *types.BuildConfig, projectDir string) (int, []string) {
	if b == nil {
		return 0, nil
	}
	best := 0
	var reasons []string
	ctxDir := b.Context
	if ctxDir != "" && !fspath.IsAbs(ctxDir) && !strings.Contains(ctxDir, "://") {
		ctxDir = filepath.Join(projectDir, ctxDir)
	}
	if strings.Contains(filepath.ToSlash(ctxDir), "laravel/sail/runtimes") {
		best = scoreKnownImage
		reasons = append(reasons, "built from a Laravel Sail runtime")
	}

	content := b.DockerfileInline
	if content == "" && ctxDir != "" && !strings.Contains(ctxDir, "://") {
		df := b.Dockerfile
		if df == "" {
			df = "Dockerfile"
		}
		if !fspath.IsAbs(df) {
			df = filepath.Join(ctxDir, df)
		}
		if data, err := os.ReadFile(df); err == nil {
			content = string(data)
		}
	}
	for _, base := range dockerfileBases(content) {
		s, _ := imageScore(base)
		if s > 0 {
			reasons = append(reasons, fmt.Sprintf("Dockerfile builds FROM %q", base))
			if s > best {
				best = s
			}
		}
	}
	return best, reasons
}

func nameScore(name string) (int, string) {
	n := strings.ToLower(name)
	switch n {
	case "php", "php-fpm", "phpfpm", "fpm", "laravel.test":
		return scoreStrongName, fmt.Sprintf("service name %q", name)
	case "app":
		return scoreWeakName, fmt.Sprintf("service name %q", name)
	}
	if strings.Contains(n, "php") {
		return scoreWeakName, fmt.Sprintf("service name %q mentions PHP", name)
	}
	return 0, ""
}

// mountsProject reports the container path where the project root (or a
// parent of it) is bind-mounted.
func mountsProject(projectDir string, m Mappings) (string, bool) {
	for _, pm := range m {
		if hasPrefix(projectDir, pm.Host, string(filepath.Separator)) {
			return pm.Remote, true
		}
	}
	return "", false
}

func hasPHPEnv(env types.MappingWithEquals) bool {
	for k := range env {
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "XDEBUG_") || strings.HasPrefix(u, "PHP_") {
			return true
		}
	}
	return false
}

// ScoreService rates a single service. projectDir resolves relative build
// contexts and bind mounts.
func ScoreService(projectDir, name string, svc types.ServiceConfig) Candidate {
	c := Candidate{Service: name, Image: svc.Image, Mappings: ServiceMappings(projectDir, svc)}

	// Runtime evidence: image and build. Positive evidence wins over a
	// non-PHP image; the strongest single piece counts.
	evidence := 0
	if s, why := imageScore(svc.Image); why != "" {
		evidence = s
		c.Reasons = append(c.Reasons, why)
	}
	if s, why := buildScore(svc.Build, projectDir); len(why) > 0 {
		if s > evidence || evidence < 0 {
			evidence = s
		}
		c.Reasons = append(c.Reasons, why...)
	}
	c.Score += evidence

	if s, why := nameScore(name); s > 0 {
		c.Score += s
		c.Reasons = append(c.Reasons, why)
	}
	if remote, ok := mountsProject(projectDir, c.Mappings); ok {
		c.Score += scoreProjectMount
		c.Reasons = append(c.Reasons, fmt.Sprintf("mounts the project at %s", remote))
	}
	if hasPHPEnv(svc.Environment) {
		c.Score += scorePHPEnv
		c.Reasons = append(c.Reasons, "environment sets PHP_*/XDEBUG_* variables")
	}
	return c
}

// Detect returns the enabled services of p that probably run PHP, best first.
// Ties are ordered by service name.
func Detect(p *Project) []Candidate {
	if p == nil || p.Model == nil {
		return nil
	}
	var out []Candidate
	for _, name := range p.ServiceNames() {
		c := ScoreService(p.Dir, name, p.Model.Services[name])
		if c.Score > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].Service < out[b].Service
	})
	return out
}
