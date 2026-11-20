package headless

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// TestTerminalEmulateLongScrollback: --scrollback prints every line
// through the visible screen, also beyond 2000 lines; JSON reports the
// lines left out of its line list (review wave 9 B9).
func TestTerminalEmulateLongScrollback(t *testing.T) {
	var b strings.Builder
	for i := range 2500 {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	b.WriteString("prompt$")
	f := filepath.Join(t.TempDir(), "long.vt")
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "", "terminal-emulate", f, "--cols", "20", "--rows", "4", "--scrollback")
	if code != 0 || !strings.HasPrefix(out, "line 0\n") || !strings.HasSuffix(out, "line 2499\nprompt$\n") {
		t.Fatalf("%d %q %q", code, out[:min(len(out), 40)], errOut)
	}
	code, out, _ = run(t, "", "--json", "terminal-emulate", f, "--cols", "20", "--rows", "4", "--scrollback")
	var res terminal.EmulateResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil {
		t.Fatalf("json %d", code)
	}
	n := len(res.Screen.Lines)
	if res.Omitted == 0 || n+res.Omitted != res.Screen.Total || res.Screen.Lines[n-1].Text != "prompt$" {
		t.Fatalf("%d lines, %d omitted, total %d", n, res.Omitted, res.Screen.Total)
	}
}
