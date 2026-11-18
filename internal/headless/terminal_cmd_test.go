package headless

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestTerminalCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	code, out, errOut := run(t, "", "terminal", "--project", dir, "--shell", "/bin/sh", "--env", "PS1=$ ",
		"--send", `echo "x\x41y"; ls\r`, "--send", `printf 'two\\n'\r`, "--cols", "40", "--rows", "6", "--idle", "200ms")
	if code != 0 || !strings.Contains(out, "xAy") || !strings.Contains(out, "a.php") || !strings.Contains(out, "two") {
		t.Fatalf("terminal: %d %q %q", code, out, errOut)
	}
	code, out, errOut = run(t, "", "--json", "terminal", "--project", dir, "--shell", "/bin/sh", "--send", `exit 3\r`, "--scrollback")
	var res terminalOutput
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Terminal.Running || res.Terminal.ExitCode != 3 || res.Screen.Top != 0 {
		t.Fatalf("json: %d %q %q", code, out, errOut)
	}
	for _, bad := range [][]string{
		{"terminal", "--project", dir, "--cols", "x"},
		{"terminal", "--project", dir, "--idle", "0"},
		{"terminal", "--project", dir, "--send", `\x4`},
		{"terminal", "--project", dir, "--send", `\xzz`},
		{"terminal", "--project", dir, "--service", "app", "--kind", "x"},
	} {
		if code, _, _ := run(t, "", bad...); code != 2 {
			t.Errorf("%v: exit %d, want 2", bad, code)
		}
	}
	// A plugin kind without a provider (docker missing or no compose file).
	if code, _, _ := run(t, "", "terminal", "--project", dir, "--kind", "nope.kind", "--target", "x"); code == 0 {
		t.Error("unknown kind accepted")
	}
}

func TestUnescapeSend(t *testing.T) {
	got, err := unescapeSend(`a\n\r\t\e\\\u00e4\q`)
	if err != nil || got != "a\n\r\t\x1b\\ä\\q" {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := unescapeSend(`end\`); got != `end\` {
		t.Fatalf("trailing backslash %q", got)
	}
}
