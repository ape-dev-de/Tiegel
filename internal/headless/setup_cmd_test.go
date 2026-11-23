package headless

import (
	"path/filepath"
	"strings"
	"testing"
)

// monorepoProject: compose files two levels above the opened project,
// volumes through an anchor, PHP application in project/ with a custom
// vendor-dir.
func monorepoProject(t *testing.T) string {
	t.Helper()
	top := project(t, map[string]string{
		"docker-compose.yaml": `x-vol: &vols
  - ./clients/app/project/:/var/www/project
services:
  app:
    image: php:8.3-fpm
    volumes: *vols
`,
		"docker-compose.override.arm.yaml":       "services:\n  app:\n    platform: linux/arm64\n",
		"clients/app/project/composer.json":      `{"config":{"vendor-dir":"Vendor"}}`,
		"clients/app/project/Vendor/bin/phpunit": "#!/bin/sh\n",
		"clients/app/project/phpunit.xml.dist":   "<phpunit/>",
		"clients/app/project/phpstan.neon":       "",
		"clients/app/project/Vendor/bin/phpstan": "#!/bin/sh\n",
		"clients/app/README.md":                  "",
	})
	return filepath.Join(top, "clients", "app")
}

func TestComposeDiscoveryCommands(t *testing.T) {
	t.Parallel()
	dir := monorepoProject(t)
	code, out, errOut := run(t, "", "compose", "--project", dir)
	for _, want := range []string{"Found ancestor (2 level(s)", "app (score", "map ", "/var/www/project", "compose use app", "Optional: "} {
		if !strings.Contains(out, want) {
			t.Errorf("compose missing %q (code %d):\n%s\n%s", want, code, out, errOut)
		}
	}
	_, out, _ = run(t, "", "compose", "discover", "--project", dir)
	if !strings.Contains(out, "PHP root project") || !strings.Contains(out, "../.. (ancestor, 2 level(s))") || !strings.Contains(out, "optional: ../../docker-compose.override.arm.yaml") {
		t.Fatalf("discover:\n%s", out)
	}
	_, out, _ = run(t, "", "interpreters", "--discover", "--project", dir)
	if !strings.Contains(out, "? compose") || !strings.Contains(out, "interpreters use ID") {
		t.Fatalf("interpreters --discover:\n%s", out)
	}
	if code, out, _ = run(t, "", "compose", "use", "app", "--project", dir); code != ExitOK || !strings.Contains(out, "selected compose:") {
		t.Fatalf("use %d:\n%s", code, out)
	}
	if code, _, _ = run(t, "", "compose", "bad", "args", "--project", dir); code != ExitUsage {
		t.Fatalf("usage %d", code)
	}
	if _, out, _ = run(t, "", "compose", "discover", "--project", t.TempDir()); !strings.Contains(out, "no compose file in or around") {
		t.Fatalf("none:\n%s", out)
	}
}

func TestSettingsCandidatesAndSetupCommands(t *testing.T) {
	t.Parallel()
	dir := monorepoProject(t)
	_, out, _ := run(t, "", "settings", "candidates", "phpunit.executable", "--project", dir)
	if !strings.Contains(out, "+ project/Vendor/bin/phpunit") {
		t.Fatalf("candidates:\n%s", out)
	}
	_, out, _ = run(t, "", "settings", "candidates", "qa-phpstan.config", "--project", dir)
	if !strings.Contains(out, "project/phpstan.neon") {
		t.Fatalf("phpstan config:\n%s", out)
	}
	_, out, _ = run(t, "", "settings", "candidates", "docker.composeFiles", "--project", dir)
	if !strings.Contains(out, `"../../docker-compose.yaml"`) {
		t.Fatalf("compose files:\n%s", out)
	}
	if _, out, _ = run(t, "", "settings", "candidates", "editor.tabWidth", "--project", dir); !strings.Contains(out, "no candidates") {
		t.Fatalf("none:\n%s", out)
	}
	if _, out, _ = run(t, "", "settings", "candidates", "ui.theme", "--json"); !strings.Contains(out, `"custom": false`) && !strings.Contains(out, `"custom":false`) {
		t.Fatalf("enum json:\n%s", out)
	}
	_, out, _ = run(t, "", "setup", "--project", dir)
	for _, want := range []string{"project.phpRoot", "php-runtime.interpreter", "phpunit.executable", "phpunit.configuration", "qa-phpstan.config", "setup apply"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup missing %q:\n%s", want, out)
		}
	}
	code, out, _ := run(t, "", "setup", "apply", "phpunit.executable", "php-runtime.interpreter", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "applied phpunit.executable") || !strings.Contains(out, "applied php-runtime.interpreter") {
		t.Fatalf("apply %d:\n%s", code, out)
	}
	if code, out, _ = run(t, "", "setup", "dismiss", "--project", dir); code != ExitOK || !strings.Contains(out, "dismissed") {
		t.Fatalf("dismiss %d:\n%s", code, out)
	}
	if code, _, _ = run(t, "", "setup", "bogus", "--project", dir); code != ExitUsage {
		t.Fatalf("usage %d", code)
	}
	if _, out, _ = run(t, "", "setup", "--dry-run", "--project", t.TempDir()); !strings.Contains(out, "nothing to set up") {
		t.Fatalf("empty:\n%s", out)
	}
}
