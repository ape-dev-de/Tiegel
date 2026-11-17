package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeymapCommand(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	km := func(args ...string) (int, string, string) {
		return run(t, "", append([]string{"keymap", "--config-dir", cfg}, args...)...)
	}
	code, out, _ := km()
	if code != ExitOK || !strings.Contains(out, "# preset mac") || !strings.Contains(out, "navigate.back") || !strings.Contains(out, "Cmd+Alt+Left") {
		t.Fatalf("list %d %q", code, out)
	}
	_, out, _ = km("list", "--context", "editor", "--query", "toggle case", "--json")
	var st struct {
		Preset   string
		Bindings []struct {
			Action string
			Chords []string
			Source string
		}
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Bindings) != 1 || st.Bindings[0].Chords[0] != "Cmd+Shift+U" {
		t.Fatalf("filtered %q %v", out, err)
	}
	code, out, _ = km("set", "app.settings", "Cmd+B")
	if code != ExitOK || !strings.Contains(out, "app.settings: Cmd+B") || !strings.Contains(out, "conflict Cmd+B: app.settings (global) and navigate.definition (editor)") {
		t.Fatalf("set %d %q", code, out)
	}
	code, out, _ = km("add", "navigate.back", "F5")
	if code != ExitOK || !strings.Contains(out, "navigate.back: Cmd+[, Cmd+Alt+Left, MouseBack, SwipeBack, F5") {
		t.Fatalf("add %d %q", code, out)
	}
	code, out, _ = km("set", "tab.close")
	if code != ExitOK || !strings.Contains(out, "tab.close: (none)") {
		t.Fatalf("unbind %d %q", code, out)
	}
	// A new process (same config dir) sees the overrides.
	_, out, _ = km("list", "--user")
	if strings.Count(out, "user") != 3 {
		t.Fatalf("user list %q", out)
	}
	_, out, _ = km("list", "--chord", "Cmd+B")
	if !strings.Contains(out, "app.settings") || !strings.Contains(out, "navigate.definition") {
		t.Fatalf("by chord %q", out)
	}
	_, out, _ = km("conflicts")
	if !strings.Contains(out, "conflict Cmd+B") {
		t.Fatalf("conflicts %q", out)
	}
	_, out, _ = km("preset", "pc")
	if strings.TrimSpace(out) != "preset pc" {
		t.Fatalf("preset %q", out)
	}
	_, out, _ = km("reset", "app.settings")
	if !strings.Contains(out, "reset 1 override(s), preset pc") {
		t.Fatalf("reset one %q", out)
	}
	_, out, _ = km("reset")
	if !strings.Contains(out, "reset 2 override(s), preset mac") {
		t.Fatalf("reset all %q", out)
	}
	_, out, _ = km("conflicts")
	if strings.TrimSpace(out) != "no conflicts" {
		t.Fatalf("no conflicts %q", out)
	}
	for _, bad := range [][]string{{"bogus"}, {"set"}, {"add", "x"}, {"preset"}, {"reset", "a", "b"}} {
		if code, _, _ := km(bad...); code != ExitUsage {
			t.Errorf("%v: code %d", bad, code)
		}
	}
	if code, _, _ := km("set", "nope", "F1"); code != ExitError {
		t.Error("unknown action")
	}
	if code, _, _ := km("set", "app.settings", "Cmd+Nope"); code != ExitUsage {
		t.Error("bad chord")
	}
}

func TestJoinLinesToggleCaseCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\nfoo(\n    1,\n    2\n);\n"})
	code, out, _ := run(t, "", "join-lines", "a.php", "--from", "2", "--to", "5:2", "--project", dir)
	if code != ExitOK || out != "<?php\nfoo(1, 2);\n" {
		t.Fatalf("join %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); strings.Contains(string(b), "foo(1") {
		t.Fatal("join without --write changed the file")
	}
	code, out, _ = run(t, "", "toggle-case", "a.php", "--from", "2:2", "--write", "--json", "--project", dir)
	var res struct {
		Changed, Written bool
		Text             string
	}
	if err := json.Unmarshal([]byte(out), &res); code != ExitOK || err != nil || !res.Written || !strings.Contains(res.Text, "FOO(") {
		t.Fatalf("toggle %d %q %v", code, out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); !strings.Contains(string(b), "FOO(") {
		t.Fatalf("not written: %q", b)
	}
	_, out, _ = run(t, "", "join-lines", "a.php", "--from", "6", "--write", "--project", dir)
	if strings.TrimSpace(out) != "nothing to change" {
		t.Fatalf("nothing %q", out)
	}
	_, out, _ = run(t, "", "join-lines", "a.php", "--from", "1", "--write", "--project", dir)
	if strings.TrimSpace(out) != "saved a.php" {
		t.Fatalf("saved %q", out)
	}
	for _, bad := range [][]string{{"join-lines", "a.php"}, {"join-lines", "a.php", "--from", "x"}, {"toggle-case", "a.php", "--from", "1", "--to", "y"}} {
		if code, _, _ := run(t, "", append(bad, "--project", dir)...); code != ExitUsage {
			t.Errorf("%v: code %d", bad, code)
		}
	}
	if code, _, _ := run(t, "", "join-lines", "missing.php", "--from", "1", "--project", dir); code != ExitError {
		t.Error("missing file")
	}
}
