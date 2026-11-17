package headless

import (
	"strings"
	"testing"
)

// TestKeymapEditorCommands covers the keymap subcommands added for the
// keymap editor: check, add --exclusive, remove and drop-invalid.
func TestKeymapEditorCommands(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	km := func(args ...string) (int, string, string) {
		return run(t, "", append([]string{"keymap", "--config-dir", cfg}, args...)...)
	}
	code, out, _ := km("check", "app.settings", "Cmd+B")
	if code != ExitOK || !strings.Contains(out, "conflict Cmd+B: app.settings (global) and navigate.definition (editor)") {
		t.Fatalf("check %d %q", code, out)
	}
	if _, out, _ = km("check", "app.settings", "Cmd+Ctrl+Alt+Shift+F9"); strings.TrimSpace(out) != "free" {
		t.Fatalf("free %q", out)
	}
	code, out, _ = km("add", "--exclusive", "app.settings", "Cmd+B")
	if code != ExitOK || !strings.Contains(out, "removed from navigate.definition") || strings.Contains(out, "conflict") {
		t.Fatalf("exclusive %d %q", code, out)
	}
	code, out, _ = km("remove", "navigate.back", "MouseBack")
	if code != ExitOK || !strings.Contains(out, "navigate.back: Cmd+[, Cmd+Alt+Left, SwipeBack") {
		t.Fatalf("remove %d %q", code, out)
	}
	if code, _, _ := km("remove", "navigate.back", "F9"); code != ExitUsage {
		t.Errorf("remove unbound chord: code %d", code)
	}
	// Shadowed chords are listed.
	km("add", "app.settings", "Cmd+[")
	if _, out, _ = km("list", "--query", "navigate.back"); !strings.Contains(out, "shadowed: Cmd+[") {
		t.Fatalf("shadowed %q", out)
	}
	// An invalid stored line (written by hand) can be dropped.
	code, out, errOut := run(t, "", "settings", "--config-dir", cfg, "set", "keymap.overrides", `["app.settings Cmd+B","app.find Cmd+Nope"]`)
	if code != ExitOK {
		t.Fatalf("settings set %d %q %q", code, out, errOut)
	}
	if _, out, _ = km("list"); !strings.Contains(out, "# ignored (unparseable, kept): app.find Cmd+Nope") {
		t.Fatalf("invalid listed %q", out)
	}
	code, out, _ = km("drop-invalid", "app.find Cmd+Nope")
	if code != ExitOK || !strings.Contains(out, `removed "app.find Cmd+Nope"`) {
		t.Fatalf("drop-invalid %d %q", code, out)
	}
	if code, _, _ := km("drop-invalid", "app.find Cmd+Nope"); code != ExitError {
		t.Errorf("drop again: code %d", code)
	}
	for _, bad := range [][]string{{"check", "x"}, {"remove", "x"}, {"drop-invalid"}} {
		if code, _, _ := km(bad...); code != ExitUsage {
			t.Errorf("%v: code %d", bad, code)
		}
	}
}
