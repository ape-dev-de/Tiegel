package filetree

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sample() *Node {
	return &Node{Name: "shop", Dir: true, Loaded: true, Children: []*Node{
		{Name: "src", Path: "src", Dir: true},
		{Name: "index.php", Path: "index.php", Status: molecules.StatusModified},
		{Name: ".env", Path: ".env", Ignored: true},
	}}
}

func TestTreeEvents(t *testing.T) {
	tr := &Tree{Root: sample()}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, tr.Update(gtx)...)
		return tr.Layout(gtx, th)
	}
	uitest.Smoke(t, "tree", image.Pt(300, 400), w)
	h := uitest.NewHarness(image.Pt(300, 400))
	h.Frame(w)
	row := func(i int) image.Point { return image.Pt(150, i*24+12) }
	// Double click on the src directory expands it (lazy load request).
	h.DoubleClick(row(1))
	h.Frame(w)
	var expand *ExpandEvent
	for _, e := range evs {
		if x, ok := e.(ExpandEvent); ok {
			expand = &x
		}
	}
	if expand == nil || expand.Path != "src" || !expand.Expanded || expand.Loaded {
		t.Fatalf("expand %+v (%v)", expand, evs)
	}
	if !tr.Expanded("src") {
		t.Fatal("src should be expanded")
	}
	// src has no loaded children: rows are root, src, index.php, .env.
	evs = nil
	h.DoubleClick(row(2))
	h.Frame(w)
	opened := false
	for _, e := range evs {
		if o, ok := e.(OpenEvent); ok && o.Path == "index.php" {
			opened = true
		}
	}
	if !opened || tr.Selected() != "index.php" {
		t.Fatalf("open %v selected %q", evs, tr.Selected())
	}
	tr.Select("a/b/c.php")
	if !tr.Expanded("a") || !tr.Expanded("a/b") {
		t.Fatal("select reveals parents")
	}
	uitest.Smoke(t, "empty", image.Pt(200, 100), (&Tree{}).Layout)
}

// The icon provider is asked for every row, with the expanded state of
// directories; nil falls back to the built-in glyphs.
func TestIconProvider(t *testing.T) {
	tr := &Tree{Root: sample()}
	tr.SetExpanded("", true)
	asked := map[string]bool{}
	tr.Icon = func(n *Node, open bool) *atoms.Glyph {
		asked[n.Path] = open
		if n.Path == "index.php" {
			return atoms.GlyphRun
		}
		return nil
	}
	uitest.Smoke(t, "icons", image.Pt(300, 400), tr.Layout)
	if open, ok := asked[""]; !ok || !open {
		t.Fatalf("root not asked as open: %v", asked)
	}
	if _, ok := asked["index.php"]; !ok || len(asked) != 4 {
		t.Fatalf("asked %v", asked)
	}
}

func TestGlyphAndStatus(t *testing.T) {
	cases := map[string]*atoms.Glyph{"a.php": atoms.GlyphFileCode, "README.md": atoms.GlyphFileText, ".env.local": atoms.GlyphFileText, "x.bin": atoms.GlyphFile}
	for name, g := range cases {
		if Glyph(&Node{Name: name}) != g {
			t.Errorf("glyph %s", name)
		}
	}
	if Glyph(&Node{Dir: true}) != nil {
		t.Error("dir glyph")
	}
	for s, want := range map[string]molecules.FileStatus{"M": molecules.StatusModified, "A": molecules.StatusAdded, "D": molecules.StatusDeleted,
		"?": molecules.StatusUntracked, "!": molecules.StatusIgnored, "U": molecules.StatusConflict, "": molecules.StatusNone} {
		if StatusFromGit(s) != want {
			t.Errorf("status %q", s)
		}
	}
}
