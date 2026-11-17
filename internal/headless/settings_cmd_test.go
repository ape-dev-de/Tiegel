package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestSettingsCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	cfg := t.TempDir()
	runS := func(args ...string) (int, string, string) {
		return run(t, "", append(args, "--project", dir, "--config-dir", cfg)...)
	}
	code, out, errs := runS("settings")
	if code != ExitOK || !strings.Contains(out, "Core › Editor") || !strings.Contains(out, "editor.tabWidth") ||
		!strings.Contains(out, "Plugins › git") || !strings.Contains(out, "git.updateMethod") {
		t.Fatalf("schema: %d %s %s", code, out, errs)
	}
	code, out, _ = runS("settings", "--schema", "--json", "--module", "git")
	var s api.SettingsSchema
	if code != ExitOK || json.Unmarshal([]byte(out), &s) != nil || len(s.Modules) != 1 || len(s.Core) != 0 {
		t.Fatalf("json: %d %s", code, out)
	}
	if code, out, _ = runS("settings", "--query", "line height"); code != ExitOK || !strings.Contains(out, "editor.lineHeight") || strings.Contains(out, "tabWidth") {
		t.Fatalf("query: %s", out)
	}

	if code, out, _ = runS("settings", "set", "ui.theme", "light"); code != ExitOK || !strings.Contains(out, `"light"`) {
		t.Fatalf("set: %d %s", code, out)
	}
	if code, _, errs = runS("settings", "set", "ui.theme", "pink"); code == ExitOK || !strings.Contains(errs, "not one of") {
		t.Fatalf("invalid set: %d %s", code, errs)
	}
	if code, out, _ = runS("settings", "set", "git.updateMethod", "rebase", "--scope", "project"); code != ExitOK {
		t.Fatalf("set project: %s", out)
	}
	if code, out, _ = runS("settings", "get", "git.updateMethod", "--effective"); code != ExitOK || !strings.Contains(out, `"rebase" (project)`) {
		t.Fatalf("get effective: %s", out)
	}
	if code, out, _ = runS("settings", "get", "ui.theme"); code != ExitOK || !strings.Contains(out, `"light" (global)`) {
		t.Fatalf("get: %s", out)
	}
	if code, out, _ = runS("settings", "reset", "ui.theme"); code != ExitOK || !strings.Contains(out, "reset") {
		t.Fatalf("reset: %s", out)
	}
	if code, out, _ = runS("settings", "get", "ui.theme"); code != ExitOK || !strings.Contains(out, "not set") {
		t.Fatalf("after reset: %s", out)
	}
	if code, _, _ = runS("settings", "bogus"); code != ExitUsage {
		t.Fatalf("usage: %d", code)
	}
}

func TestEditorConfigCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		".editorconfig": "root = true\n[*.php]\nindent_style = space\nindent_size = 2\ninsert_final_newline = true\ntrim_trailing_whitespace = false\nend_of_line = lf\ncharset = utf-8\n",
		"a.php":         "<?php\n", "b.txt": "x",
	})
	code, out, errs := run(t, "", "editorconfig", "a.php", "--project", dir)
	for _, want := range []string{"indent_style", "space", "indent_size", "tab_width", "insert_final_newline", "true", "trim_trailing_whitespace", "end_of_line", "charset", "from "} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %d %s %s", want, code, out, errs)
		}
	}
	if _, out, _ = run(t, "", "editorconfig", "b.txt", "--project", dir); !strings.Contains(out, "no .editorconfig properties") {
		t.Fatalf("none: %s", out)
	}
	cfg := t.TempDir()
	run(t, "", "settings", "set", "files.editorconfig", "false", "--scope", "project", "--project", dir, "--config-dir", cfg)
	if _, out, _ = run(t, "", "editorconfig", "a.php", "--project", dir, "--config-dir", cfg); !strings.Contains(out, "is off") {
		t.Fatalf("off: %s", out)
	}
	if _, out, _ = run(t, "", "editorconfig", "a.php", "--json", "--project", dir); !strings.Contains(out, `"indentSize": 2`) {
		t.Fatalf("json: %s", out)
	}
}
