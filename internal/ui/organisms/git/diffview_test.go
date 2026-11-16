package git

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestDiffModelRows(t *testing.T) {
	d := diffFor(t, controller)
	m := buildDiffModel(d)
	lines := 0
	for _, h := range d.Hunks {
		lines += len(h.Lines)
	}
	if len(m.unified) != lines+len(d.Hunks) {
		t.Fatalf("unified rows %d, want %d", len(m.unified), lines+len(d.Hunks))
	}
	if len(m.hunksU) != 2 || !m.unified[m.hunksU[1]].header {
		t.Fatal("hunk starts")
	}
	// Side by side pairs deletions with additions: never more rows.
	if len(m.split) >= len(m.unified) {
		t.Errorf("split %d rows, unified %d", len(m.split), len(m.unified))
	}
	for _, r := range m.split {
		if r.header {
			continue
		}
		if r.left.tone == molecules.ToneRemoved && r.right.tone == molecules.ToneContext {
			t.Fatal("removed line paired with context")
		}
		if !r.left.present && r.left.tone != molecules.ToneFiller {
			t.Fatal("missing side must be filler")
		}
	}
	// Change blocks: hunk 0 has three runs, hunk 1 two.
	if len(m.changesU) != 5 || len(m.changesS) != 5 {
		t.Errorf("change blocks %d/%d", len(m.changesU), len(m.changesS))
	}
	// Unified pairing links the "-" line to the "+" line.
	r := m.unified[m.changesU[0]]
	if r.marker != "-" || r.pair < 0 || m.unified[r.pair].marker != "+" {
		t.Errorf("pairing %+v", r)
	}
	if a, del := d.Stats(); m.added != a || m.deleted != del || m.digits != 3 {
		t.Errorf("stats %d/%d digits %d", m.added, m.deleted, m.digits)
	}
	if e := buildDiffModel(nil); len(e.unified) != 0 {
		t.Error("nil diff")
	}
}

func TestWordSpans(t *testing.T) {
	a := "        private UserRepository $users,"
	b := "        private readonly UserRepository $users,"
	sa, sb := wordSpans(a, b)
	if len(sa) != 0 || len(sb) != 1 || strings.TrimSpace(b[sb[0].Start:sb[0].End]) != "readonly" {
		t.Fatalf("spans %v %v", sa, sb)
	}
	sa, sb = wordSpans("return $this->redirectToRoute('user_list');", "return $this->redirectToRoute('user_show');")
	if len(sa) != 1 || len(sb) != 1 {
		t.Fatalf("replace spans %v %v", sa, sb)
	}
	if sa, sb := wordSpans("abc", "xyz"); sa != nil || sb != nil {
		t.Error("completely different lines need no spans")
	}
	if sa, _ := wordSpans(strings.Repeat("a ", 2000), "b"); sa != nil {
		t.Error("long lines are skipped")
	}
	if got := strings.Join(tokenize("$a = foo(1);"), "|"); got != "$a| |=| |foo|(|1|)|;" {
		t.Errorf("tokens %q", got)
	}
	if expandTabs("\tx\ty") != "    x   y" {
		t.Errorf("tabs %q", expandTabs("\tx\ty"))
	}
}

func TestWholeFileDiff(t *testing.T) {
	base, ours, _ := gitsample.MergeVersions()
	f := WholeFileDiff("a.php", "a.php", base, ours)
	if len(f.Hunks) != 1 {
		t.Fatal("one hunk")
	}
	h := f.Hunks[0]
	nb, no := len(gitcli.SplitLines(base)), len(gitcli.SplitLines(ours))
	old, nw := 0, 0
	for _, l := range h.Lines {
		if l.Kind != gitcli.LineAdded {
			old++
		}
		if l.Kind != gitcli.LineDeleted {
			nw++
		}
	}
	if old != nb || nw != no || h.OldLines != nb || h.NewLines != no {
		t.Fatalf("lines old %d/%d new %d/%d", old, nb, nw, no)
	}
	// The hunk must be a valid patch of base.
	if _, err := f.Patch(f.SelectAll()); err != nil {
		t.Error(err)
	}
	if e := WholeFileDiff("x", "x", nil, nil); len(e.Hunks) != 0 {
		t.Error("empty")
	}
	n := WholeFileDiff("", "n", nil, []byte("a\nb\n"))
	if !n.IsNew || n.Hunks[0].OldStart != 0 || len(n.Hunks[0].Lines) != 2 {
		t.Errorf("new file %+v", n.Hunks[0])
	}
}

func TestDiffViewSmokeAndNavigation(t *testing.T) {
	v := NewDiffView()
	uitest.Smoke(t, "diff empty", area, v.Layout)
	v.SetDiff(diffFor(t, controller))
	for _, m := range []DiffMode{Unified, SideBySide} {
		v.SetMode(m)
		uitest.Smoke(t, "diff "+m.String(), area, v.Layout)
		uitest.Smoke(t, "diff narrow "+m.String(), image.Pt(260, 200), v.Layout)
	}
	v.SetMode(Unified)
	if v.Changes() != 5 || v.Current() != -1 {
		t.Fatalf("changes %d current %d", v.Changes(), v.Current())
	}
	if v.PrevChange() {
		t.Error("prev before first")
	}
	for i := 0; i < 5; i++ {
		if !v.NextChange() {
			t.Fatalf("next %d", i)
		}
	}
	if v.NextChange() || v.Current() != 4 {
		t.Error("next past end")
	}
	if !v.PrevChange() || v.Current() != 3 {
		t.Error("prev")
	}
	v.SetMode(SideBySide)
	if v.Current() != 3 || v.pending < 0 {
		t.Error("mode switch keeps the current change and scrolls to it")
	}
	v.ScrollToHunk(1)
	if v.Current() != 2 {
		t.Errorf("after ScrollToHunk(1) current %d", v.Current())
	}
	// Binary, rename-only and combined diffs show a placeholder.
	for _, d := range []*gitcli.FileDiff{
		{NewPath: "a.png", IsBinary: true},
		{OldPath: "a", NewPath: "b", IsRename: true},
		{NewPath: "c", IsCombined: true},
		{NewPath: "d", OldMode: "100644", NewMode: "100755"},
	} {
		v.SetDiff(d)
		if v.placeholderText() == "No content changes" {
			t.Errorf("%+v: generic placeholder", d)
		}
		uitest.Smoke(t, "diff placeholder", area, v.Layout)
	}
	if v.Mode.String() != "side-by-side" || Unified.String() != "unified" {
		t.Error("mode names")
	}
}

func TestDiffViewToolbarClicks(t *testing.T) {
	h := uitest.NewHarness(area)
	v := NewDiffView()
	v.SetDiff(diffFor(t, mailer))
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return v.Layout(gtx, th) }
	h.Frame(w)
	// Toolbar from the right: mode button, words checkbox, next, prev.
	// Find the mode button by clicking from the far right.
	h.Click(image.Pt(area.X-40, 16))
	h.Frame(w)
	if v.Mode != SideBySide {
		t.Fatalf("mode button: %v", v.Mode)
	}
}

func TestDiffViewVirtualizes10kLines(t *testing.T) {
	d, err := gitsample.LargeDiff(10_000)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	v := NewDiffView()
	v.SetDiff(d)
	build := time.Since(start)
	h := uitest.NewHarness(image.Pt(1000, 800))
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return v.Layout(gtx, th) }
	for _, m := range []DiffMode{Unified, SideBySide} {
		v.SetMode(m)
		h.Frame(w)
		if v.drawn == 0 || v.drawn > 80 {
			t.Errorf("%s: %d rows laid out for an 800px viewport", m, v.drawn)
		}
	}
	// Jump to the last change: still only visible rows.
	for v.NextChange() {
	}
	h.Frame(w)
	if v.drawn > 80 || v.list.LastVisible() < len(v.rows())-60 {
		t.Errorf("after jump: drawn %d, last visible %d of %d", v.drawn, v.list.LastVisible(), len(v.rows()))
	}
	// Generous bound; the benchmark has the real numbers.
	if build > 2*time.Second {
		t.Errorf("building rows took %v", build)
	}
}

func BenchmarkDiffViewBuild10k(b *testing.B) {
	d, err := gitsample.LargeDiff(10_000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buildDiffModel(d)
	}
}

func BenchmarkDiffViewFrame10k(b *testing.B) {
	d, err := gitsample.LargeDiff(10_000)
	if err != nil {
		b.Fatal(err)
	}
	for _, m := range []DiffMode{Unified, SideBySide} {
		b.Run(m.String(), func(b *testing.B) {
			v := NewDiffView()
			v.SetDiff(d)
			v.SetMode(m)
			h := uitest.NewHarness(image.Pt(1200, 900))
			w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return v.Layout(gtx, th) }
			h.Frame(w)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Scroll through the file while laying out.
				v.list.ScrollTo((i * 97) % 10_000)
				h.Frame(w)
			}
		})
	}
}
