package headless

import (
	"strings"
	"testing"
)

func TestRuntimeDiagnoseMappingsOverride(t *testing.T) {
	dir := project(t, map[string]string{
		"compose.yaml":  "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
		"composer.json": `{"require":{"php":"^8.0"}}`,
	})
	cfg := t.TempDir()
	code, out, _ := run(t, "", "runtime-diagnose", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(out, "no-interpreter") {
		t.Fatalf("diagnose %d %q", code, out)
	}
	if code, _, _ := run(t, "", "runtime-diagnose", "nope", "--project", dir); code != ExitError {
		t.Fatal("unknown interpreter")
	}
	_, out, _ = run(t, "", "mappings", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "/var/www/html\tcompose") {
		t.Fatalf("mappings %q", out)
	}
	_, out, _ = run(t, "", "mappings", "set", "src=/app/src", "--exec-mode", "exec", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "/app/src\tsettings") {
		t.Fatalf("set %q", out)
	}
	// Persisted across processes.
	_, out, _ = run(t, "", "mappings", "--json", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, `"source": "settings"`) {
		t.Fatalf("persisted %q", out)
	}
	_, out, _ = run(t, "", "runtime-diagnose", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "exec-mode exec") {
		t.Fatalf("exec mode %q", out)
	}
	if _, out, _ = run(t, "", "mappings", "reset", "--project", dir, "--config-dir", cfg); !strings.Contains(out, "compose") {
		t.Fatalf("reset %q", out)
	}
	if code, _, _ := run(t, "", "mappings", "set", "broken", "--project", dir); code != ExitUsage {
		t.Fatal("bad mapping")
	}
	if code, _, _ := run(t, "", "mappings", "what", "--project", dir); code != ExitUsage {
		t.Fatal("bad subcommand")
	}
	if code, _, _ := run(t, "", "mappings", "--exec-mode", "x", "--project", dir); code != ExitUsage {
		t.Fatal("bad exec mode")
	}

	_, out, _ = run(t, "", "php-override", "--project", dir, "--config-dir", cfg)
	if !strings.HasPrefix(out, "8.0.0\tcomposer") {
		t.Fatalf("override show %q", out)
	}
	_, out, _ = run(t, "", "php-override", "8.2", "--project", dir, "--config-dir", cfg)
	if !strings.HasPrefix(out, "8.2.0\toverride") {
		t.Fatalf("override set %q", out)
	}
	_, out, _ = run(t, "", "php-version", "--project", dir, "--config-dir", cfg)
	if !strings.HasPrefix(out, "8.2.0") {
		t.Fatalf("override persisted %q", out)
	}
	_, out, _ = run(t, "", "php-override", "clear", "--project", dir, "--config-dir", cfg)
	if !strings.HasPrefix(out, "8.0.0") {
		t.Fatalf("override cleared %q", out)
	}
	if code, _, _ := run(t, "", "php-override", "x", "--project", dir); code != ExitUsage {
		t.Fatal("invalid override")
	}
	// A disabled module answers with "module disabled" (exit 1).
	run(t, "", "modules", "disable", "php-features", "--config-dir", cfg)
	code, _, errs := run(t, "", "php-check", "composer.json", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(errs, "module disabled") {
		t.Fatalf("disabled %d %q", code, errs)
	}
	run(t, "", "modules", "enable", "php-features", "--config-dir", cfg)
}
