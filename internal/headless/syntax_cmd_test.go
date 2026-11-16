package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func TestSyntaxCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		".env":      "A=1\n# c\nB=2\n",
		"k.php":     "<?php\nclass K\n{\n    public function f(): void\n    {\n    }\n}\n",
		"data.json": "{\n  \"a\": 1\n}\n",
	})
	code, out, _ := run(t, "", "highlights", ".env", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "0\t1\tproperty\t\"A\"") {
		t.Fatalf("highlights %d %q", code, out)
	}
	_, out, _ = run(t, "", "highlights", ".env", "--lines", "2:2", "--project", dir)
	if strings.TrimSpace(out) != "4\t7\tcomment\t\"# c\"" {
		t.Fatalf("highlights lines %q", out)
	}
	_, out, _ = run(t, "", "highlights", ".env", "--lines", "3", "--json", "--project", dir)
	var h struct {
		Start, End int
		Language   string
	}
	if err := json.Unmarshal([]byte(out), &h); err != nil || h.Start != 8 || h.End != 12 || h.Language != "env" {
		t.Fatalf("json %q %v", out, err)
	}
	if code, _, _ := run(t, "", "highlights", ".env", "--lines", "x", "--project", dir); code != ExitUsage {
		t.Fatal("bad --lines")
	}
	if code, _, _ := run(t, "", "folds", ".env", "--language", "cobol", "--project", dir); code != ExitUsage {
		t.Fatal("bad --language")
	}
	if code, _, _ := run(t, "", "outline", "missing.php", "--project", dir); code != ExitError {
		t.Fatal("missing file")
	}
	if !syntax.TreeSitter {
		if code, _, _ := run(t, "", "outline", "k.php", "--project", dir); code != ExitError {
			t.Fatal("outline without tree-sitter should fail")
		}
		return
	}
	_, out, _ = run(t, "", "outline", "k.php", "--project", dir)
	if !strings.Contains(out, "2:7 class K") || !strings.Contains(out, "  4:21 public method f(): void") {
		t.Fatalf("outline %q", out)
	}
	_, out, _ = run(t, "", "folds", "data.json", "--project", dir)
	if strings.TrimSpace(out) != "1-3" {
		t.Fatalf("folds %q", out)
	}
}
