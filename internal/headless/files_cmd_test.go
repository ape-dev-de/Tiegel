package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"composer.json": `{"autoload": {"psr-4": {"App\\": "src/"}}}`,
		"a.txt":         "a",
	})
	p := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	ok := func(want string, args ...string) string {
		t.Helper()
		code, out, errs := run(t, "", append(args, "--project", dir)...)
		if code != ExitOK || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %q %q", args, code, out, errs)
		}
		return out
	}
	ok("notes/todo.md", "new-file", "notes/todo.md", "--content", "# todo")
	if b, _ := os.ReadFile(p("notes/todo.md")); string(b) != "# todo" {
		t.Fatalf("content %q", b)
	}
	ok("build", "new-dir", "build")
	ok("a.txt -> b.txt", "rename", "a.txt", "b.txt")
	ok("b.txt -> notes/c.txt", "rename", "b.txt", "notes/c.txt")
	ok("notes/c.txt -> build/c.txt", "move", "notes/c.txt", "build")
	ok("build/c.txt -> notes/c.txt", "copy", "build/c.txt", "notes/c.txt")
	out := ok("src/Domain/Order.php", "new-class", "src/Domain", "Order", "--kind", "enum")
	if !strings.Contains(out, `enum App\Domain\Order`) {
		t.Fatalf("new-class %q", out)
	}
	if b, _ := os.ReadFile(p("src/Domain/Order.php")); !strings.Contains(string(b), "namespace App\\Domain;\n\nenum Order\n") {
		t.Fatalf("class file %q", b)
	}
	ok("Plain", "new-class", ".", "Plain", "--namespace", "")
	if b, _ := os.ReadFile(p("Plain.php")); strings.Contains(string(b), "namespace") {
		t.Fatal("--namespace '' ignored")
	}
	ok("deleted: build", "delete", "build", "notes/todo.md", "--permanent")
	if _, err := os.Stat(p("build")); err == nil {
		t.Fatal("not deleted")
	}
	code, out, _ := run(t, "", "new-file", "x.json", "--json", "--project", dir)
	var res struct{ Path string }
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || res.Path != "x.json" {
		t.Fatalf("json %d %q", code, out)
	}
	for _, args := range [][]string{
		{"new-file", "x.json"},     // exists
		{"new-file", "../out.txt"}, // outside the project
		{"rename", "missing", "y"}, // not found
		{"delete", "missing", "--permanent"},
		{"new-class", "src", "class"}, // reserved word
	} {
		if code, _, _ := run(t, "", append(args, "--project", dir)...); code == ExitOK {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code, _, _ := run(t, "", "rename", "a", "--project", dir); code != ExitUsage {
		t.Error("rename with one argument")
	}
}
