package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/terminal"
)

func TestTerminalEmulateCommand(t *testing.T) {
	// Text from --send, replies on stderr.
	code, out, errOut := run(t, "", "terminal-emulate", "--send", `a\e[1;31mb\e[0m\r\nc\e[6n`, "--cols", "10", "--rows", "3")
	if code != 0 || out != "ab\nc\n" || !strings.Contains(errOut, `\x1b[2;2R`) {
		t.Fatalf("send: %d %q %q", code, out, errOut)
	}
	// Stdin, JSON with spans and modes.
	code, out, errOut = run(t, "\x1b[?2004hx\x1b[4:3my", "--json", "terminal-emulate", "-", "--chunk", "1")
	var res terminal.EmulateResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || !res.Screen.Modes.BracketedPaste || res.Text != "xy" {
		t.Fatalf("stdin: %d %q %q", code, out, errOut)
	}
	if sp := res.Screen.Lines[0].Spans; len(sp) != 2 || sp[1].UnderlineStyle != 3 {
		t.Fatalf("spans %+v", sp)
	}
	// A recorded stream from a file, reflowed and with scrollback.
	f := filepath.Join(t.TempDir(), "s.vt")
	if err := os.WriteFile(f, []byte("abcdefghij\r\n12"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, "", "terminal-emulate", f, "--cols", "10", "--rows", "2", "--resize", "4x2", "--scrollback", "--history", "100")
	if code != 0 || out != "abcdefghij\n12\n" {
		t.Fatalf("file: %d %q %q", code, out, errOut)
	}
	// The fixtures of the terminal package load.
	if runtime.GOOS != "windows" {
		code, out, _ = run(t, "", "terminal-emulate", filepath.Join("..", "terminal", "testdata", "agent.vt"), "--cols", "32", "--rows", "8")
		if code != 0 || !strings.Contains(out, "│ > fix the bug") {
			t.Fatalf("fixture: %d %q", code, out)
		}
	}
	for _, bad := range [][]string{
		{"terminal-emulate"},
		{"terminal-emulate", "--send", "x", "--resize", "4"},
		{"terminal-emulate", "--send", "x", "--chunk", "-1"},
		{"terminal-emulate", "--send", `\x4`},
	} {
		if code, _, _ := run(t, "", bad...); code != 2 {
			t.Errorf("%v: exit %d, want 2", bad, code)
		}
	}
	if code, _, _ := run(t, "", "terminal-emulate", filepath.Join(t.TempDir(), "missing")); code == 0 {
		t.Error("missing file accepted")
	}
}
