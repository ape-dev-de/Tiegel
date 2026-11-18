//go:build !windows

package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// The qa command with fake tools in vendor/bin (shell scripts with the
// plugins' command lines).
func TestQACommand(t *testing.T) {
	dir := project(t, map[string]string{
		"a.php":              "<?php\n$x = array(1,2);\n",
		"vendor/bin/pint":    "#!/bin/sh\nfor a in \"$@\"; do f=\"$a\"; done\nsed 's/array(1,2)/[1, 2]/' \"$f\" > \"$f.o\" && mv \"$f.o\" \"$f\"\n",
		"vendor/bin/phpstan": "#!/bin/sh\nprintf '{\"files\":{\"%s/a.php\":{\"messages\":[{\"message\":\"Bad\",\"line\":2,\"identifier\":\"x.y\"}]}},\"errors\":[]}' \"$PWD\"\nexit 1\n",
	})
	cfg := t.TempDir() // shared: project settings live in the config directory
	for _, f := range []string{"pint", "phpstan"} {
		if err := os.Chmod(filepath.Join(dir, "vendor", "bin", f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := run(t, "", "qa", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "qa-pint") || !strings.Contains(out, "install Psalm: tiegel --headless install qa-psalm") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = run(t, "", "--json", "qa", "list", "--project", dir, "--config-dir", cfg)
	var list []api.QAToolInfo
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 6 {
		t.Fatalf("list json: %d %q", code, out)
	}

	// Findings: printed as path:line:col, exit 1.
	// Without any PHP interpreter the tool cannot run in it.
	code, _, errOut := run(t, "", "qa", "run", "a.php", "--tool", "qa-phpstan", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(errOut, "interpreter") {
		t.Fatalf("no interpreter: %d %q", code, errOut)
	}
	for _, id := range []string{"qa-phpstan", "qa-pint"} {
		if code, _, e := run(t, "", "settings", "set", id+".useInterpreter", "false", "--scope", "project", "--project", dir, "--config-dir", cfg); code != 0 {
			t.Fatalf("settings: %d %s", code, e)
		}
	}
	code, out, errOut = run(t, "", "qa", "run", "a.php", "--tool", "qa-phpstan", "--project", dir, "--config-dir", cfg)
	if code != ExitError || out != "a.php:2:1: error: Bad [x.y] (qa-phpstan)\n" {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	// A missing tool fails with its reason.
	code, _, errOut = run(t, "", "qa", "run", "--tool", "qa-psalm", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(errOut, "not found") {
		t.Fatalf("missing: %d %q", code, errOut)
	}

	code, out, _ = run(t, "", "qa", "format", "a.php", "--preview", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "$x = [1, 2];") {
		t.Fatalf("preview: %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); strings.Contains(string(b), "[1, 2]") {
		t.Fatal("preview wrote the file")
	}
	code, out, _ = run(t, "", "qa", "format", "a.php", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "formatted with qa-pint") {
		t.Fatalf("format: %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != "<?php\n$x = [1, 2];\n" {
		t.Fatalf("file %q", b)
	}
	code, out, _ = run(t, "", "qa", "diagnostics", "--project", dir, "--config-dir", cfg)
	if code != 0 || out != "" {
		t.Fatalf("diagnostics (new process, nothing stored): %d %q", code, out)
	}
	code, out, _ = run(t, "", "qa", "clear", "--project", dir, "--config-dir", cfg)
	if code != 0 || !strings.Contains(out, "cleared 0") {
		t.Fatalf("clear: %d %q", code, out)
	}
	for _, bad := range [][]string{{"qa", "nope"}, {"qa", "format"}, {"qa", "list", "x"}, {"qa", "diagnostics", "a", "b"}} {
		if code, _, _ := run(t, "", append(bad, "--project", dir, "--config-dir", cfg)...); code != ExitUsage {
			t.Fatalf("%v: %d", bad, code)
		}
	}
}
