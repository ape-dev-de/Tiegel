package mdview

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

const diagramDoc = "# Diagrams\n\n" +
	"```mermaid\nflowchart TD\n  A[Start] --> B{Is it?}\n  B -->|Yes| C[OK]\n  C --> D[Rethink]\n  D --> B\n  B ---->|No| E[End]\n```\n\n" +
	"```mermaid\nsequenceDiagram\n  autonumber\n  Alice->>+John: Hello John, how are you?\n  John-->>-Alice: Great!\n" +
	"  loop Every minute\n    John->>Alice: ping\n  end\n  Note right of John: thinks\n```\n\n" +
	"```mermaid\nflowchart LR\n  A --> \n```\n\n" +
	"```php\n<?php\nfunction greet(string $name): string {\n    // say hello\n    return \"Hello, $name\" . 42;\n}\n```\n\n" +
	"```bash\n# install\nexport PATH=$HOME/bin:$PATH\nif [ -f x ]; then echo \"ok\"; fi\n```\n\n" +
	"```yaml\nservices:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n```\n\n" +
	"```js\nconst add = (a, b) => a + b; // sum\nexport default class Foo { run() { return null } }\n```\n"

const moreDiagrams = "```mermaid\nstateDiagram-v2\n  [*] --> Idle\n  Idle --> Busy : job\n  Busy --> Idle : done\n  Busy --> [*]\n```\n\n" +
	"```mermaid\nclassDiagram\n  Animal <|-- Duck\n  Animal : +String name\n  Animal : +eat()\n  Duck : +swim()\n```\n\n" +
	"```mermaid\nerDiagram\n  CUSTOMER ||--o{ ORDER : places\n  ORDER {\n    int id PK\n  }\n```\n\n" +
	"```mermaid\npie title Pets\n  \"Dogs\" : 386\n  \"Cats\" : 85\n  \"Rats\" : 15\n```\n"

func diagramView(t *testing.T) *View { return docView(t, diagramDoc) }

func docView(t *testing.T, src string) *View {
	t.Helper()
	d := markdown.Parse([]byte(src))
	d.Highlight(context.Background())
	var v View
	v.SetDoc(d)
	return &v
}

func TestMermaidBlocksLayOut(t *testing.T) {
	for _, w := range []int{900, 280} {
		v := diagramView(t)
		uitest.Smoke(t, "mermaid", image.Pt(w, 6000), v.Layout)
		var diagrams, failed int
		for _, n := range v.nodes {
			if n.mm == nil {
				continue
			}
			if n.mm.err != nil {
				failed++
				continue
			}
			diagrams++
			if n.mm.diag == nil || len(n.mm.diag.Items) == 0 {
				t.Fatalf("empty diagram")
			}
		}
		if diagrams != 2 || failed != 1 {
			t.Fatalf("w=%d: %d diagrams, %d errors (want 2, 1)", w, diagrams, failed)
		}
	}
}

func TestMermaidToggleShowsSource(t *testing.T) {
	v := diagramView(t)
	uitest.Smoke(t, "diagram", image.Pt(900, 6000), v.Layout)
	var n0 *node
	for _, n := range v.nodes {
		if n.mm != nil && n.mm.err == nil {
			n0 = n
			break
		}
	}
	if n0 == nil || n0.mm.toggle.Text != "Source" {
		t.Fatal("no diagram with a Source toggle")
	}
	diagramH := uitest.Smoke(t, "diagram", image.Pt(900, 6000), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return v.layoutMermaid(gtx, th, n0, ctx{})
	}).Size.Y
	n0.mm.showSource = true
	d := uitest.Smoke(t, "source", image.Pt(900, 6000), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return v.layoutMermaid(gtx, th, n0, ctx{})
	})
	if n0.mm.toggle.Text != "Diagram" || d.Size.Y == diagramH {
		t.Fatalf("source view: toggle %q, height %d (diagram %d)", n0.mm.toggle.Text, d.Size.Y, diagramH)
	}
	// Unchanged blocks keep their state across SetDoc (live preview).
	v.SetDoc(v.doc)
	if !n0.mm.showSource {
		t.Fatal("SetDoc dropped the toggle state")
	}
}

func TestCodeHighlightRoles(t *testing.T) {
	v := diagramView(t)
	uitest.Smoke(t, "code", image.Pt(900, 6000), v.Layout)
	seen := map[tokens.SyntaxRole]bool{}
	for _, n := range v.nodes {
		if n.b.Kind != markdown.CodeBlock || n.mm != nil || n.text == nil {
			continue
		}
		for _, r := range n.text.runs {
			seen[r.role] = true
		}
	}
	for _, r := range []tokens.SyntaxRole{tokens.RoleKeyword, tokens.RoleString, tokens.RoleComment} {
		if !seen[r] {
			t.Errorf("no %s run in the highlighted code blocks", r)
		}
	}
}

func TestNewCodeRichSplitsAtTokens(t *testing.T) {
	code := "a := \"x\"\n\n\tb // c"
	toks := []markdown.Token{{Start: 5, End: 8, Role: "string"}, {Start: 13, End: 17, Role: "comment"}}
	rt := newCodeRich(code, toks)
	var got []string
	for i, s := range rt.spans {
		got = append(got, s.Text+"|"+rt.roles[i].String())
	}
	want := "a := |plain,\"x\"|string,|plain,    b |plain,// c|comment"
	if strings.Join(got, ",") != want {
		t.Fatalf("spans\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	if !rt.spans[1].Break || !rt.spans[2].Break {
		t.Fatalf("line breaks lost: %+v", rt.spans)
	}
}

// TestRenderDiagramsHeadless draws diagrams and highlighted code on the
// GPU in both themes. With TIEGEL_SHOTS=<dir> the PNGs are written for
// visual review.
func TestRenderDiagramsHeadless(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	size := image.Pt(900, 2300)
	for i, th := range append(themes(), themes()...) {
		v, name := diagramView(t), "mdview-mermaid-"
		if i >= 2 {
			v, name = docView(t, moreDiagrams), "mdview-mermaid-more-"
		}
		img, err := uitest.Render(th, size, v.Layout)
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if n := uitest.CountDiffering(img, th.Color.Background); n < 20000 {
			t.Errorf("%s: only %d pixels drawn", th.Mode, n)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, name+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
