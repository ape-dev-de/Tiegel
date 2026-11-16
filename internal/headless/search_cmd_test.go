package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestFindAndReplaceCommands(t *testing.T) {
	dir := project(t, map[string]string{
		"src/a.php":    "<?php\n$foo = 1;\n",
		"src/b.txt":    "foo\n",
		"vendor/v.php": "foo\n",
	})
	code, out, errs := run(t, "", "find", "foo", "--mask", "*.php", "--project", dir)
	if code != ExitOK || out != "src/a.php:2:2: $foo = 1;\n" {
		t.Fatalf("find %d %q %q", code, out, errs)
	}
	code, out, _ = run(t, "", "find", `f\w+`, "--regex", "--libraries", "--json", "--project", dir)
	var res api.ProjectSearchResult
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || res.FilesMatched != 3 {
		t.Fatalf("find json %d %q", code, out)
	}
	if code, out, _ = run(t, "", "find", "foo", "--limit", "1", "--project", dir); code != ExitOK || !strings.Contains(out, "limited to 1") {
		t.Fatalf("limit %d %q", code, out)
	}
	if code, _, _ := run(t, "", "find", "nothing", "--project", dir); code != ExitError {
		t.Fatalf("no match: %d", code)
	}
	if code, _, _ := run(t, "", "find", "x", "--max-size", "zero", "--project", dir); code != ExitUsage {
		t.Fatalf("bad max-size: %d", code)
	}
	if code, _, _ := run(t, "", "find", "x", "--limit", "z", "--project", dir); code != ExitUsage {
		t.Fatalf("bad limit: %d", code)
	}
	if code, out, _ := run(t, "", "find", "foo", "--open", "--project", dir); code != ExitError || out != "" {
		t.Fatalf("open scope without documents: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "find", "foo", "--dir", "src", "--max-size", "1000", "--case", "--word", "--ignored", "--project", dir); code != ExitOK || !strings.Contains(out, "src/b.txt:1:1: foo") {
		t.Fatalf("dir %d %q", code, out)
	}

	code, out, errs = run(t, "", "replace", "foo", "bar", "--preview", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "src/a.php:2:2: foo -> bar") || !strings.Contains(out, "would replace 2 in 2 files") {
		t.Fatalf("preview %d %q %q", code, out, errs)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "src/a.php")); strings.Contains(string(data), "bar") {
		t.Fatal("preview wrote")
	}
	code, out, errs = run(t, "", "replace", "foo", "bar", "src/b.txt", "--project", dir)
	if code != ExitOK || out != "src/b.txt: 1 replaced\nreplaced 1 in 1 files\n" {
		t.Fatalf("replace %d %q %q", code, out, errs)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "src/b.txt")); string(data) != "bar\n" {
		t.Fatalf("b.txt %q", data)
	}
	if code, _, _ := run(t, "", "replace", "nothing", "x", "--project", dir); code != ExitError {
		t.Fatalf("replace nothing: %d", code)
	}
	if code, _, _ := run(t, "", "replace", "x", "--project", dir); code != ExitUsage {
		t.Fatalf("replace usage: %d", code)
	}
	ro := filepath.Join(dir, "src")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o755)
	if os.Getuid() != 0 {
		if code, out, errs := run(t, "", "replace", "foo", "baz", "--project", dir); code != ExitError || !strings.Contains(out, "error:") || !strings.Contains(errs, "could not be changed") {
			t.Fatalf("replace failure %d %q %q", code, out, errs)
		}
	}
}
