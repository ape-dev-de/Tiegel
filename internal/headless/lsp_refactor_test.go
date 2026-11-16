package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

func TestLSPRefactorCommands(t *testing.T) {
	fake := buildFakeLSP(t)
	const a = "<?php\nfunction foo() {}\nfoo();  \necho 'ERR';\n"
	dir := project(t, map[string]string{"a.php": a, "x.txt": "x"})
	read := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "a.php"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	lsp := []string{"--command", fake, "--project", dir}

	code, out, errs := run(t, "", append([]string{"usages", "a.php", "3:2"}, lsp...)...)
	if code != ExitOK || out != "a.php:2:10: function foo() {}\na.php:3:1: foo();\n" {
		t.Fatalf("usages %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", append([]string{"usages", "a.php", "3:2", "--no-declaration", "--json"}, lsp...)...); code != ExitOK {
		t.Fatalf("usages json %d", code)
	}
	var us []langphp.Usage
	if json.Unmarshal([]byte(out), &us) != nil || len(us) != 1 {
		t.Fatalf("usages json %q", out)
	}
	if code, _, _ := run(t, "", append([]string{"usages", "a.php", "1:1"}, lsp...)...); code != ExitError {
		t.Fatalf("no usages: %d", code)
	}
	for _, bad := range []string{"x", "0:1", "2:x"} {
		if code, _, _ := run(t, "", append([]string{"usages", "a.php", bad}, lsp...)...); code != ExitUsage {
			t.Fatalf("position %q: %d", bad, code)
		}
	}
	if code, _, errs := run(t, "", append([]string{"usages", "x.txt", "1:1"}, lsp...)...); code != ExitError || !strings.Contains(errs, "not a PHP file") {
		t.Fatalf("non-PHP: %d %q", code, errs)
	}

	code, out, errs = run(t, "", append([]string{"code-actions", "a.php", "4:8", "--timeout", "5s"}, lsp...)...)
	if code != ExitOK || !strings.Contains(out, "1. Remove ERR [quickfix] (preferred)") || !strings.Contains(out, "(disabled: not here)") {
		t.Fatalf("code-actions %d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", append([]string{"code-actions", "a.php", "4:8", "--apply", "1"}, lsp...)...)
	if code != ExitOK || !strings.Contains(out, "applied Remove ERR") || !strings.Contains(read(), "echo '';") {
		t.Fatalf("apply %d %q %q %q", code, out, errs, read())
	}
	if code, _, _ := run(t, "", append([]string{"code-actions", "a.php", "4:8", "--apply", "nope"}, lsp...)...); code != ExitError {
		t.Fatalf("unknown action: %d", code)
	}
	if code, _, _ := run(t, "", append([]string{"code-actions", "a.php", "4:8", "--timeout", "x"}, lsp...)...); code != ExitUsage {
		t.Fatalf("bad timeout: %d", code)
	}

	code, out, errs = run(t, "", append([]string{"format", "a.php", "--preview"}, lsp...)...)
	if code != ExitOK || out != "a.php:3:7-3:9: \"\"\n" {
		t.Fatalf("format preview %d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", append([]string{"format", "a.php"}, lsp...)...)
	if code != ExitOK || out != "formatted a.php (1 edits)\n" || !strings.Contains(read(), "\nfoo();\n") {
		t.Fatalf("format %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", append([]string{"format", "a.php"}, lsp...)...); code != ExitOK || out != "a.php already formatted\n" {
		t.Fatalf("format again %d %q", code, out)
	}

	code, out, errs = run(t, "", append([]string{"rename-symbol", "a.php", "2:11", "bar", "--preview"}, lsp...)...)
	if code != ExitOK || out != "a.php:2:10: -> bar\na.php:3:1: -> bar\n" {
		t.Fatalf("rename preview %d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", append([]string{"rename-symbol", "a.php", "2:11", "bar"}, lsp...)...)
	if code != ExitOK || !strings.Contains(out, "a.php (2 edits)") || !strings.Contains(read(), "function bar()") {
		t.Fatalf("rename %d %q %q", code, out, errs)
	}
}
