package headless

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownCommand(t *testing.T) {
	dir := project(t, map[string]string{"README.md": "# Hello\n\nSome **bold** [link](https://x.org) text.\n\n## Usage\n\n- a\n- b\n"})
	code, out, errOut := run(t, "", "md", "README.md", "--project", dir)
	if code != 0 || !strings.Contains(out, "# Hello\n") || !strings.Contains(out, "link <https://x.org>") || !strings.Contains(out, "- a\n- b\n") {
		t.Fatalf("plain: %d %q %q", code, out, errOut)
	}
	code, out, _ = run(t, "", "md", "README.md", "--outline", "--project", dir)
	if code != 0 || !strings.Contains(out, "1  Hello  #hello") || !strings.Contains(out, "5    Usage  #usage") {
		t.Fatalf("outline: %d %q", code, out)
	}
	code, out, _ = run(t, "", "md", "README.md", "--json", "--project", dir)
	var r struct {
		Doc struct {
			Blocks []struct {
				Kind string `json:"kind"`
			} `json:"blocks"`
		} `json:"doc"`
		Plain string `json:"plain"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || len(r.Doc.Blocks) != 4 || r.Doc.Blocks[0].Kind != "heading" || r.Plain != "" {
		t.Fatalf("json: %d %q", code, out)
	}
	code, out, _ = run(t, "> "+strings.Repeat("word ", 20)+"\n", "md", "-", "--width", "30", "--project", dir)
	if code != 0 || !strings.HasPrefix(out, "> word") || strings.Count(out, "\n") < 3 {
		t.Fatalf("stdin: %d %q", code, out)
	}
	if code, out, _ = run(t, "", "md", "-", "--project", dir); code != 0 || out != "\n" {
		t.Fatalf("empty: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "md", "missing.md", "--project", dir); code == 0 {
		t.Fatal("missing file succeeded")
	}
	if code, _, _ := run(t, "", "md", "README.md", "--width", "x", "--project", dir); code == 0 {
		t.Fatal("bad width succeeded")
	}
}
