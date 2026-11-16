package diff_test

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff/diffsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestUnifiedNavigation(t *testing.T) {
	w, _ := sampleWindow(t, false)
	w.SetUnified(true)
	h := uitest.NewHarness(image.Pt(900, 200))
	frame(h, w)
	for w.Next() {
		frame(h, w)
	}
	frame(h, w)
	if w.Current() != w.Count()-1 {
		t.Fatalf("current %d", w.Current())
	}
	// Switching modes keeps the current block in view.
	w.SetUnified(false)
	frame(h, w)
	frame(h, w)
	_, right := w.Editors()
	b := w.Blocks()[w.Current()]
	if y := right.LineTop(b.RightStart); y < 0 || y > right.ViewportHeight() {
		t.Fatalf("block not visible after switch: %d", y)
	}
}

// TestLargeFilePerformance compares a 10k-line file and checks that a
// frame (with scrolling) stays cheap: only visible lines and blocks are
// drawn.
func TestLargeFilePerformance(t *testing.T) {
	left, right := diffsample.Large(10000, 40)
	start := time.Now()
	blocks := diff.Compute(left, right, false)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("compute took %v", d)
	}
	if len(blocks) < 500 {
		t.Fatalf("only %d blocks", len(blocks))
	}
	w := diff.NewDiffWindow()
	defer w.Close()
	w.NewHighlighter = func(*buffer.Buffer, string) highlight.Highlighter { return highlight.PHPDemo }
	w.SetContent(diff.Content{Left: diff.Side{Label: "HEAD", Path: "big.php", Text: left},
		Right: diff.Side{Label: "Working tree", Path: "big.php", Text: right}, Blocks: blocks}, buffer.NewString(right))
	h := uitest.NewHarness(area)
	frame(h, w)
	le, _ := w.Editors()
	start = time.Now()
	frames := 60
	if raceEnabled {
		frames = 5 // the race detector slows rendering down 20–50×
	}
	for i := 0; i < frames; i++ {
		le.SetScrollOffset(image.Pt(0, i*1500))
		frame(h, w)
	}
	per := time.Since(start) / time.Duration(frames)
	if !raceEnabled && per > 50*time.Millisecond {
		t.Fatalf("frame took %v", per)
	}
	t.Logf("10k lines, %d blocks: %v per frame", len(blocks), per)
	for w.Next() {
	}
	if w.Current() != len(blocks)-1 {
		t.Fatal("navigation")
	}
}

func BenchmarkDiffWindowFrame10k(b *testing.B) {
	left, right := diffsample.Large(10000, 40)
	w := diff.NewDiffWindow()
	defer w.Close()
	w.SetContent(diff.Content{Left: diff.Side{Text: left}, Right: diff.Side{Text: right}, Blocks: diff.Compute(left, right, false)}, nil)
	h := uitest.NewHarness(area)
	frame(h, w)
	le, _ := w.Editors()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		le.SetScrollOffset(image.Pt(0, (i*977)%200000))
		frame(h, w)
	}
}

func BenchmarkCompute10k(b *testing.B) {
	left, right := diffsample.Large(10000, 40)
	for i := 0; i < b.N; i++ {
		diff.Compute(left, right, false)
	}
}

func TestBlameGutter(t *testing.T) {
	commits, hashes := diffsample.Blame()
	g := diff.NewBlameGutter()
	g.SetBlame(commits, hashes)
	if c, ok := g.CommitAt(13); !ok || c.Author != "Jonas Weber" {
		t.Fatalf("commit at 13: %+v", c)
	}
	if _, ok := g.CommitAt(9999); ok {
		t.Fatal("out of range")
	}
	if !strings.Contains(g.Text(0), "2026-03-01") || !strings.Contains(g.Text(0), "Mara Lindqvist") {
		t.Fatalf("text %q", g.Text(0))
	}
	if g.Text(1) != "" {
		t.Fatal("run continuation should be blank")
	}
	if g.Text(22) != "Not committed yet" {
		t.Fatalf("uncommitted %q", g.Text(22))
	}
	g.AuthorChars = 4
	if !strings.Contains(g.Text(13), "Jon…") {
		t.Fatalf("truncated %q", g.Text(13))
	}
	g.AuthorChars = 0

	ed := editorview.New(buffer.NewString(diffsample.New))
	defer ed.Close()
	ed.SetGutterColumn(g)
	uitest.Smoke(t, "blame", image.Pt(900, 500), ed.Layout)
	h := uitest.NewHarness(image.Pt(900, 500))
	var evs []editorview.Event
	widget := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			evs = append(evs, ev)
		}
		return ed.Layout(gtx, th)
	}
	h.Frame(widget)
	lh := ed.LineHeight()
	h.Click(image.Pt(20, 13*lh+lh/2))
	h.Frame(widget)
	var got []diff.Event
	for _, ev := range evs {
		if e, ok := g.HandleEditorEvent(ev); ok {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0] != (diff.CommitClicked{Hash: commits[1].Hash, Line: 13}) {
		t.Fatalf("click events %v (editor %v)", got, evs)
	}
	if _, ok := g.HandleEditorEvent(editorview.GutterClickEvent{Line: 22, Area: editorview.GutterAnnotation}); ok {
		t.Fatal("uncommitted line clicked")
	}
	if _, ok := g.HandleEditorEvent(editorview.FocusEvent{}); ok {
		t.Fatal("other event translated")
	}
	// Rendering in both themes: the age bars use the accent color.
	dir := os.Getenv("TIEGEL_SHOTS")
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		img, err := uitest.Render(th, image.Pt(900, 500), ed.Layout)
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if n := countNear(img, th.Color.Accent); n < 50 {
			t.Errorf("%s: age bars barely visible (%d px)", th.Mode, n)
		}
		if dir != "" {
			writePNG(t, filepath.Join(dir, "blame-"+th.Mode.String()+".png"), img)
		}
	}
}

func TestFileHistoryView(t *testing.T) {
	v := diff.NewFileHistoryView()
	defer v.Close()
	uitest.Smoke(t, "history empty", area, v.Layout)
	v.SetHistory(diffsample.Path, diffsample.History(0, 30), true)
	if v.Len() != 30 || v.Path() != diffsample.Path {
		t.Fatal("set")
	}
	h := uitest.NewHarness(area)
	frame(h, v)
	// Select the first commit (log header + first row).
	h.Click(image.Pt(300, 23+1+12))
	ev := frame(h, v)
	var req *diff.HistoryDiffRequested
	for _, e := range ev {
		if r, ok := e.(diff.HistoryDiffRequested); ok {
			req = &r
		}
	}
	if req == nil {
		t.Fatalf("no diff request: %v", ev)
	}
	first := diffsample.History(0, 1)[0]
	if req.Commit != first.Hash || req.Base != first.Parents[0] || req.Path != diffsample.Path || req.ParentPath != diffsample.Path {
		t.Fatalf("request %+v", req)
	}
	if v.SelectedHash() != first.Hash {
		t.Fatal("selection")
	}
	v.Diff.SetContent(diffsample.Content(), nil)
	uitest.Smoke(t, "history with diff", area, v.Layout)
	v.AppendHistory(diffsample.History(30, 10), false)
	if v.Len() != 40 {
		t.Fatal("append")
	}
	// A root commit is compared with the empty version.
	root := diffsample.History(0, 1)
	root[0].Hash, root[0].Parents, root[0].Status = "root", nil, 'A'
	v.SetHistory("x.php", root, false)
	frame(h, v)
	h.Click(image.Pt(300, 23+1+12))
	ev = frame(h, v)
	found := false
	for _, e := range ev {
		if r, ok := e.(diff.HistoryDiffRequested); ok {
			found = true
			if r.Base != diff.EmptyVersion || r.Path != diffsample.Path {
				t.Fatalf("root request %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("no root request: %v", ev)
	}
}

func TestHistoryPaging(t *testing.T) {
	v := diff.NewFileHistoryView()
	defer v.Close()
	v.SetHistory(diffsample.Path, diffsample.History(0, 25), true)
	h := uitest.NewHarness(image.Pt(900, 2000))
	var page *diff.HistoryPageRequested
	for i := 0; i < 4 && page == nil; i++ {
		for _, e := range frame(h, v) {
			if p, ok := e.(diff.HistoryPageRequested); ok {
				page = &p
			}
		}
	}
	if page == nil || page.Loaded != 25 || page.Path != diffsample.Path {
		t.Fatalf("page %+v", page)
	}
}

func TestAPIConversion(t *testing.T) {
	raw := `{"path":"a.php","left":{"version":"HEAD","label":"HEAD (1)","path":"a.php","exists":true,"text":"a\nb\n"},
	"right":{"version":"working","label":"Working tree","path":"a.php","exists":true,"text":"a\nc\n"},
	"blocks":[{"kind":"modified","leftStart":2,"leftEnd":3,"rightStart":2,"rightEnd":3,"leftInline":[{"line":2,"ranges":[{"start":0,"end":1}]}]}]}`
	var r diff.APIFileDiff
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	c := r.Content()
	if c.Left.Label != "HEAD (1)" || c.Right.Missing || len(c.Blocks) != 1 {
		t.Fatalf("content %+v", c)
	}
	b := c.Blocks[0]
	if b.Kind != diff.Modified || b.LeftStart != 1 || b.LeftEnd != 2 || len(b.LeftInline) != 1 || b.LeftInline[0].Line != 1 {
		t.Fatalf("block %+v", b)
	}
	blame := diff.APIBlame{Lines: []diff.APIBlameLine{{Line: 1, Hash: "h1"}, {Line: 2, Hash: "h2"}},
		Commits: []diff.APIBlameCommit{{Hash: "h1", Author: "A", Date: time.Unix(10, 0)}, {Hash: "h2", Author: "B", Date: time.Unix(20, 0)}}}
	g := diff.NewBlameGutter()
	blame.Apply(g)
	if c, ok := g.CommitAt(1); !ok || c.Author != "B" {
		t.Fatal("blame apply")
	}
	hist := diff.APIHistory{Commits: []diff.APIHistoryEntry{{Hash: "h", Parents: []string{"p"}, Author: "A", Path: "b", OldPath: "a", Status: "R"}}}
	es := hist.Entries()
	if len(es) != 1 || es[0].Status != 'R' || es[0].ParentPath() != "a" || es[0].AuthorName != "A" {
		t.Fatalf("history %+v", es)
	}
	var _ gitcli.FileCommit = es[0]
}
