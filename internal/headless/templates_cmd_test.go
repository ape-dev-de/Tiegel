package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func TestTemplatesCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"a.php": "<?php\nfunction f() {\n    fore\n}\n",
		"b.php": "<?php\nclass B {\n    \n}\n",
		"c.php": "<?php\nfunction g() {\n    work();\n}\n",
	})
	tpl := func(args ...string) (int, string, string) {
		return run(t, "", append(append([]string{"templates"}, args...), "--project", dir)...)
	}
	code, out, errs := tpl()
	if code != ExitOK || !strings.Contains(out, "forek") || !strings.Contains(out, "builtin,surround") {
		t.Fatalf("list %d %q %q", code, out, errs)
	}
	code, out, _ = tpl("list", "--surround", "--json")
	if code != ExitOK || strings.Contains(out, `"pubf"`) || !strings.Contains(out, `"try"`) {
		t.Fatalf("surround list %q", out)
	}
	if code, _, _ = tpl("expand", "a.php"); code != ExitUsage {
		t.Fatalf("expand without position: %d", code)
	}
	if code, _, _ = tpl("bogus"); code != ExitUsage {
		t.Fatalf("unknown sub command: %d", code)
	}
	if code, _, _ = tpl("expand", "a.php", "--at", "x"); code != ExitUsage {
		t.Fatalf("bad position: %d", code)
	}
	if !syntax.TreeSitter {
		return
	}
	code, out, _ = tpl("list", "b.php", "--at", "3:5")
	if code != ExitOK || !strings.Contains(out, "# context php-member") || !strings.Contains(out, "pubf") || strings.Contains(out, "forek") {
		t.Fatalf("list at %q", out)
	}
	code, out, errs = tpl("expand", "a.php", "--at", "3:9", "--set", "ITERABLE=rows", "--dry-run")
	if code != ExitOK || !strings.Contains(out, `would expand "fore"`) || !strings.Contains(out, "foreach ($rows as $item)") || !strings.Contains(out, "tab stop VALUE:") {
		t.Fatalf("dry run %d %q %q", code, out, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); !strings.Contains(string(b), "    fore\n") {
		t.Fatal("dry run wrote the file")
	}
	code, out, _ = tpl("expand", "a.php", "--at", "3:9", "--json")
	var res struct {
		Applied bool
		Text    string
	}
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || !res.Applied {
		t.Fatalf("expand %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); !strings.Contains(string(b), "    foreach ($items as $item) {\n        \n    }\n") {
		t.Fatalf("file %q", b)
	}
	// Surround With: lines 3 of c.php with try.
	code, out, errs = tpl("expand", "c.php", "--at", "3:1", "--end", "4:1", "--abbr", "try")
	if code != ExitOK {
		t.Fatalf("surround %d %q %q", code, out, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "c.php")); !strings.Contains(string(b), "    try {\n        work();\n    } catch (\\Throwable $e) {") {
		t.Fatalf("surrounded %q", b)
	}
	if code, _, _ = tpl("expand", "c.php", "--offset", "0", "--abbr", "nope"); code == ExitOK {
		t.Fatal("unknown template accepted")
	}
}
