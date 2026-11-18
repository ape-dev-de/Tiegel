package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func TestGenerateCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"A.php": "<?php\nclass A\n{\n    private int $n;\n    private string $s;\n}\n",
	})
	cli := func(args ...string) (int, string, string) {
		return run(t, "", append(append([]string{"generate"}, args...), "--project", dir)...)
	}
	if code, _, _ := cli("A.php"); code != ExitUsage {
		t.Fatalf("without position: %d", code)
	}
	if !syntax.TreeSitter {
		return
	}
	code, out, errs := cli("A.php", "--at", "3")
	if code != ExitOK || !strings.Contains(out, "# class A") || !strings.Contains(out, "getters          Getters (n, s)") {
		t.Fatalf("list %d %q %q", code, out, errs)
	}
	code, out, _ = cli("A.php", "--at", "3", "--kind", "getters", "--property", "s", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "would generate getS in A") {
		t.Fatalf("dry run %q", out)
	}
	code, out, _ = cli("A.php", "--at", "3", "--kind", "constructor")
	if code != ExitOK || !strings.Contains(out, "generated __construct") {
		t.Fatalf("constructor %q", out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "A.php"))
	if !strings.Contains(string(b), "public function __construct(int $n, string $s)") {
		t.Fatalf("file %q", b)
	}
	if code, _, _ = cli("A.php", "--at", "3", "--kind", "constructor"); code == ExitOK {
		t.Fatal("second constructor")
	}
}
