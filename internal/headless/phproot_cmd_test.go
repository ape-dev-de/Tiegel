package headless

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPRootCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"README.md":                    "# repo\n",
		"project/composer.json":        `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"project/vendor/autoload":      "",
		"project/phpunit.xml":          "<phpunit/>",
		"project/src/A.php":            "<?php\n",
		"tools/qa/composer.json":       `{}`,
		"node_modules/x/composer.json": `{}`,
	})
	cfg := t.TempDir() // settings survive between the invocations
	code, out, errs := run(t, "", "php-root", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.HasPrefix(out, "PHP root: project (detected)\n") ||
		!strings.Contains(out, "* project") || !strings.Contains(out, "composer.json, vendor/, phpunit.xml") || !strings.Contains(out, "tools/qa") ||
		strings.Contains(out, "node_modules") {
		t.Fatalf("show %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "php-root", "--json", "--project", dir, "--config-dir", cfg); code != ExitOK || !strings.Contains(out, `"auto": true`) {
		t.Fatalf("json %d %q", code, out)
	}
	if code, out, errs = run(t, "", "php-root", "set", filepath.Join(dir, "tools", "qa"), "--project", dir, "--config-dir", cfg); code != ExitOK ||
		!strings.HasPrefix(out, "PHP root: tools/qa (setting project.phpRoot)") {
		t.Fatalf("set %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "php-root", "--project", dir, "--config-dir", cfg); !strings.HasPrefix(out, "PHP root: tools/qa (setting") {
		t.Fatalf("persisted %d %q", code, out)
	}
	if code, _, errs = run(t, "", "php-root", "set", "nope", "--project", dir, "--config-dir", cfg); code == ExitOK || !strings.Contains(errs, "no folder inside the project") {
		t.Fatalf("invalid %d %q", code, errs)
	}
	if code, out, _ = run(t, "", "php-root", "reset", "--project", dir, "--config-dir", cfg); code != ExitOK || !strings.HasPrefix(out, "PHP root: project (detected)") {
		t.Fatalf("reset %d %q", code, out)
	}
	if code, _, _ = run(t, "", "php-root", "bogus", "--project", dir); code != ExitUsage {
		t.Fatalf("usage %d", code)
	}
}
