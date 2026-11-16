package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/index"
)

func TestIndexCommands(t *testing.T) {
	dir := project(t, map[string]string{
		"src/Shape.php":  "<?php\nnamespace App;\n\ninterface Shape\n{\n    public function area(): float;\n}\n",
		"src/Square.php": "<?php\nnamespace App;\n\nclass Square implements Shape\n{\n    public function area(): float { return 1.0; }\n}\n",
		"src/Use.php":    "<?php\nnamespace App\\Other;\n\nuse App\\Square;\n\nfunction make(): Square\n{\n    return new Square();\n}\n",
	})
	cfg := t.TempDir()
	if !index.TreeSitter {
		if code, _, _ := run(t, "", "symbols", "Sq", "--project", dir, "--config-dir", cfg); code != ExitError {
			t.Fatal("symbols without tree-sitter should fail")
		}
		return
	}
	code, out, errOut := run(t, "", "index-status", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.Contains(out, "state:      ready") || !strings.Contains(out, "files:      3") || !strings.Contains(out, "from cache: no") {
		t.Fatalf("index-status %d %q %q", code, out, errOut)
	}
	// the second process loads the cache
	_, out, _ = run(t, "", "index-status", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "from cache: yes") {
		t.Fatalf("cache %q", out)
	}
	_, out, _ = run(t, "", "index-status", "--rebuild", "--full", "--json", "--project", dir, "--config-dir", cfg)
	var st struct {
		State string
		Files int
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || st.State != "ready" || st.Files != 3 {
		t.Fatalf("rebuild %q %v", out, err)
	}

	_, out, _ = run(t, "", "symbols", "sq", "--kind", "class", "--project", dir, "--config-dir", cfg)
	if strings.TrimSpace(out) != "class\tApp\\Square\tsrc/Square.php:4:7" {
		t.Fatalf("symbols %q", out)
	}
	if code, _, _ := run(t, "", "symbols", "zzzz", "--no-libraries", "--limit", "5", "--project", dir, "--config-dir", cfg); code != ExitError {
		t.Fatal("no match should exit 1")
	}
	if code, _, _ := run(t, "", "symbols", "x", "--kind", "bogus", "--project", dir, "--config-dir", cfg); code != ExitUsage {
		t.Fatal("bad kind")
	}

	_, out, _ = run(t, "", "definition", "src/Use.php", "8:17", "--project", dir, "--config-dir", cfg)
	if strings.TrimSpace(out) != "src/Square.php:4:7: class App\\Square [index]" {
		t.Fatalf("definition %q", out)
	}
	_, out, _ = run(t, "", "implementations", "src/Shape.php", "6:22", "--project", dir, "--config-dir", cfg)
	if strings.TrimSpace(out) != "src/Square.php:6:21: method App\\Square::area [index]" {
		t.Fatalf("implementations %q", out)
	}
	_, out, errOut = run(t, "", "implementations", "src/Shape.php", "6:22", "--source", "lsp", "--project", dir, "--config-dir", cfg)
	if strings.TrimSpace(out) != "" || !strings.Contains(errOut, "language server") {
		t.Fatalf("implementations lsp %q %q", out, errOut)
	}
	_, out, _ = run(t, "", "super", "src/Square.php", "6", "--json", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, `"locations": []`) {
		t.Fatalf("super at line start %q", out)
	}
	_, out, _ = run(t, "", "super", "src/Square.php", "6:22", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "App\\Shape::area") {
		t.Fatalf("super %q", out)
	}
	if code, _, _ := run(t, "", "definition", "src/Use.php", "x:1", "--project", dir, "--config-dir", cfg); code != ExitUsage {
		t.Fatal("bad position")
	}
	if code, _, _ := run(t, "", "definition", "src/Use.php", "1:0", "--project", dir, "--config-dir", cfg); code != ExitUsage {
		t.Fatal("bad column")
	}

	_, out, _ = run(t, "", "hierarchy", `App\Shape`, "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "interface App\\Shape\tsrc/Shape.php:4:11") || !strings.Contains(out, "subtypes:\n  class App\\Square\tsrc/Square.php:4:7") {
		t.Fatalf("hierarchy %q", out)
	}
	_, out, _ = run(t, "", "hierarchy", "src/Use.php", "8:17", "--project", dir, "--config-dir", cfg)
	if !strings.Contains(out, "supertypes:\n  interface App\\Shape") {
		t.Fatalf("hierarchy at %q", out)
	}
	if code, _, _ := run(t, "", "hierarchy", "src/Use.php", "8", "--project", dir, "--config-dir", cfg); code != ExitError {
		t.Fatal("no class at line start")
	}
	if code, _, _ := run(t, "", "hierarchy", "src/Use.php", "q", "--project", dir, "--config-dir", cfg); code != ExitUsage {
		t.Fatal("bad hierarchy position")
	}

	_, out, _ = run(t, "", "gutter", "src/Shape.php", "--project", dir, "--config-dir", cfg)
	if out != "4\tinterface App\\Shape\tup 0\tdown 1\n6\tmethod App\\Shape::area\tup 0\tdown 1\n" {
		t.Fatalf("gutter %q", out)
	}
}

func TestParseLineCol(t *testing.T) {
	if p, err := parseLineCol("3"); err != nil || p.Line != 3 || p.Col != 1 {
		t.Fatal(p, err)
	}
	if _, err := parseLineCol("0:1"); err == nil {
		t.Fatal("line 0")
	}
}
