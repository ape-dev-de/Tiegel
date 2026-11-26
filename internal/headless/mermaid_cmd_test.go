package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidCommand(t *testing.T) {
	dir := project(t, map[string]string{
		"README.md": "# Doc\n\n```mermaid\nflowchart LR\nA-->B\n```\n\n```mermaid\nsequenceDiagram\nA->>B: hi\n```\n",
		"bad.mmd":   "flowchart TD\nA -->\n",
	})
	code, out, errOut := run(t, "", "mermaid", "README.md", "--project", dir)
	if code != 0 || !strings.Contains(out, "3  flowchart") || !strings.Contains(out, "8  sequence") {
		t.Fatalf("summary: %d %q %q", code, out, errOut)
	}
	svg := filepath.Join(t.TempDir(), "d.svg")
	code, _, errOut = run(t, "", "mermaid", "README.md", "--svg", svg, "--theme", "dark", "--project", dir)
	if code != 0 || !strings.Contains(errOut, "wrote") {
		t.Fatalf("svg: %d %q", code, errOut)
	}
	for _, f := range []string{svg, strings.TrimSuffix(svg, ".svg") + "-2.svg"} {
		b, err := os.ReadFile(f)
		if err != nil || !strings.HasPrefix(string(b), "<svg") {
			t.Fatalf("%s: %v", f, err)
		}
	}
	code, out, _ = run(t, "graph TD\nA-->B\n", "mermaid", "-", "--svg", "-", "--project", dir)
	if code != 0 || !strings.HasPrefix(out, "<svg") {
		t.Fatalf("stdin svg: %d %q", code, out)
	}
	code, out, _ = run(t, "pie\n\"a\": 1\n", "mermaid", "-", "--json", "--project", dir)
	var r struct {
		Diagrams []struct {
			Kind string `json:"kind"`
		} `json:"diagrams"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || len(r.Diagrams) != 1 || r.Diagrams[0].Kind != "pie" {
		t.Fatalf("json: %d %q", code, out)
	}
	if code, out, _ = run(t, "", "mermaid", "bad.mmd", "--project", dir); code == 0 || !strings.Contains(out, "error: mermaid: line 2") {
		t.Fatalf("bad: %d %q", code, out)
	}
	if code, _, _ = run(t, "", "mermaid", "bad.mmd", "--svg", "-", "--project", dir); code == 0 {
		t.Fatal("bad svg succeeded")
	}
	if code, _, _ = run(t, "  ", "mermaid", "-", "--project", dir); code == 0 {
		t.Fatal("empty stdin succeeded")
	}
	if code, out, _ = run(t, "# nothing\n", "mermaid", "-", "--markdown", "--project", dir); code != 0 || !strings.Contains(out, "no mermaid diagrams") {
		t.Fatalf("no diagrams: %d %q", code, out)
	}
}
