package mdview

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var _ ui.Component = (*View)(nil)

func sp(s string) []markdown.Span { return []markdown.Span{{Text: s}} }

// sampleDoc contains every block kind and span style.
func sampleDoc() *markdown.Doc {
	return &markdown.Doc{Blocks: []markdown.Block{
		{Kind: markdown.Heading, Line: 1, Level: 1, Anchor: "title", Spans: sp("Title")},
		{Kind: markdown.Paragraph, Line: 3, Spans: []markdown.Span{
			{Text: "Plain, "}, {Text: "bold", Style: markdown.Bold}, {Text: ", "},
			{Text: "italic", Style: markdown.Italic}, {Text: ", "}, {Text: "struck", Style: markdown.Strike},
			{Text: ", "}, {Text: "code()", Style: markdown.Code}, {Text: " and a "},
			{Text: "link", Link: "https://example.org"}, {Text: " with a hard", Break: true},
			{Text: "break and a " + strings.Repeat("verylongwordwithoutanyspaces", 12) + " end."},
		}},
		{Kind: markdown.Heading, Line: 5, Level: 2, Anchor: "section", Spans: sp("Section")},
		{Kind: markdown.Heading, Line: 6, Level: 3, Anchor: "h3", Spans: sp("Three")},
		{Kind: markdown.Heading, Line: 7, Level: 4, Anchor: "h4", Spans: sp("Four")},
		{Kind: markdown.Heading, Line: 8, Level: 5, Anchor: "h5", Spans: sp("Five")},
		{Kind: markdown.Heading, Line: 9, Level: 6, Anchor: "h6", Spans: sp("Six")},
		{Kind: markdown.CodeBlock, Line: 10, Lang: "php", Code: "<?php\n\techo 'hi';\n\n" + strings.Repeat("$x = 1; ", 40)},
		{Kind: markdown.Quote, Line: 15, Children: []markdown.Block{
			{Kind: markdown.Paragraph, Line: 15, Spans: sp("Quoted text")},
			{Kind: markdown.Quote, Line: 16, Children: []markdown.Block{
				{Kind: markdown.Paragraph, Line: 16, Spans: sp("Nested quote")},
			}},
			{Kind: markdown.CodeBlock, Line: 17, Code: "quoted code"},
		}},
		{Kind: markdown.List, Line: 20, Tight: true, Children: []markdown.Block{
			{Kind: markdown.ListItem, Line: 20, Children: []markdown.Block{
				{Kind: markdown.Paragraph, Line: 20, Spans: sp("one")},
				{Kind: markdown.List, Line: 21, Tight: true, Children: []markdown.Block{
					{Kind: markdown.ListItem, Line: 21, Children: []markdown.Block{
						{Kind: markdown.Paragraph, Line: 21, Spans: sp("nested")},
						{Kind: markdown.List, Line: 22, Tight: true, Children: []markdown.Block{
							{Kind: markdown.ListItem, Line: 22, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 22, Spans: sp("deeper")}}},
						}},
					}},
				}},
			}},
			{Kind: markdown.ListItem, Line: 23},
		}},
		{Kind: markdown.List, Line: 25, Ordered: true, Start: 9, Children: []markdown.Block{
			{Kind: markdown.ListItem, Line: 25, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 25, Spans: sp("nine")}}},
			{Kind: markdown.ListItem, Line: 27, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 27, Spans: sp("ten")}}},
		}},
		{Kind: markdown.List, Line: 30, Tight: true, Children: []markdown.Block{
			{Kind: markdown.ListItem, Line: 30, Task: true, Checked: true, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 30, Spans: sp("done")}}},
			{Kind: markdown.ListItem, Line: 31, Task: true, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 31, Spans: sp("open")}}},
		}},
		{Kind: markdown.Table, Line: 33, Align: []markdown.Align{markdown.AlignLeft, markdown.AlignCenter, markdown.AlignRight, markdown.AlignNone},
			Rows: [][]markdown.Cell{
				{{Spans: sp("Name")}, {Spans: sp("Mid")}, {Spans: sp("Right")}, {Spans: sp("Long")}},
				{{Spans: sp("a")}, {Spans: []markdown.Span{{Text: "b", Style: markdown.Bold}}}, {Spans: sp("1")}, {Spans: sp(strings.Repeat("wrap me ", 40))}},
				{{Spans: sp("c")}},
				{{Spans: sp("d")}, {Spans: sp("e")}, {Spans: []markdown.Span{{Text: "x", Link: "#title"}}}, {}},
			}},
		{Kind: markdown.Rule, Line: 38},
		{Kind: markdown.HTML, Line: 40, Code: "<div class=\"x\">\n  <b>raw</b>\n</div>"},
		{Kind: markdown.Paragraph, Line: 44, Spans: []markdown.Span{
			{Text: "logo", Image: true, Link: "img/logo.png"}, {Text: " text after the image "},
			{Text: "", Image: true, Link: "missing.png"},
		}},
		{Kind: "future-kind", Line: 46, Children: []markdown.Block{{Kind: markdown.Paragraph, Line: 46, Spans: sp("child of an unknown kind")}}},
	}}
}

func themes() []*tokens.Theme {
	sh := tokens.NewBundledShaper()
	return []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)}
}

func TestEveryBlockKindLaysOut(t *testing.T) {
	for _, w := range []int{900, 260} {
		var v View
		v.LoadImage = func(src string) (image.Image, bool) {
			if src != "img/logo.png" {
				return nil, false
			}
			return image.NewNRGBA(image.Rect(0, 0, 400, 100)), true
		}
		v.SetDoc(sampleDoc())
		uitest.Smoke(t, fmt.Sprintf("doc w=%d", w), image.Pt(w, 8000), v.Layout)
		// Every block has been laid out and shaped.
		for i, n := range v.nodes {
			if n.text != nil && !n.text.shaped {
				t.Errorf("w=%d: block %d (%s) not shaped", w, i, n.b.Kind)
			}
		}
	}
}

func TestLayoutInline(t *testing.T) {
	var v View
	v.SetDoc(sampleDoc())
	var inline layout.Dimensions
	uitest.Smoke(t, "inline", image.Pt(300, 8000), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		inline = v.LayoutInline(gtx, th)
		return inline
	})
	if inline.Size.X != 300 || inline.Size.Y <= 0 || inline.Size.Y >= 8000 {
		t.Fatalf("inline size %v", inline.Size)
	}
	var w View
	w.SetDoc(&markdown.Doc{})
	full, err := uitest.Layout(tokens.Dark(tokens.NewBundledShaper()), image.Pt(300, 100), w.LayoutInline)
	if err != nil || full.Size.Y != 0 {
		t.Fatal(full.Size)
	}
}

func TestEmptyDocument(t *testing.T) {
	var v View
	uitest.Smoke(t, "zero", image.Pt(300, 200), v.Layout)
	v.SetDoc(&markdown.Doc{})
	uitest.Smoke(t, "empty", image.Pt(300, 200), v.Layout)
	v.SetDoc(nil)
	if v.TopLine() != 0 || v.ScrollToAnchor("x") {
		t.Fatal("empty doc has no lines or anchors")
	}
	v.ScrollToLine(5) // no panic
	if v.Doc() != nil {
		t.Fatal("Doc() should be nil")
	}
}

func TestUnboundedConstraints(t *testing.T) {
	var v View
	v.SetDoc(sampleDoc())
	d, err := uitest.Layout(tokens.Dark(nil), image.Pt(1<<24, 1<<24), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return v.Layout(gtx, th)
	})
	if err != nil || d.Size.X <= 0 || d.Size.Y <= 0 || d.Size.Y >= 1<<20 {
		t.Fatalf("dims %v err %v", d, err)
	}
}

func paragraphs(n int) *markdown.Doc {
	d := &markdown.Doc{}
	for i := range n {
		d.Blocks = append(d.Blocks, markdown.Block{Kind: markdown.Paragraph, Line: 1 + 2*i,
			Spans: sp(fmt.Sprintf("Paragraph %d with some words that will wrap across lines in a narrow view.", i))})
	}
	return d
}

func TestScrollSyncAndSetDocKeepsPosition(t *testing.T) {
	h := uitest.NewHarness(image.Pt(600, 400))
	var v View
	d := paragraphs(200)
	d.Blocks[150] = markdown.Block{Kind: markdown.Heading, Level: 2, Line: 301, Anchor: "target", Spans: sp("Target")}
	v.SetDoc(d)
	h.Frame(v.Layout)
	if got := v.TopLine(); got != 1 {
		t.Fatalf("TopLine = %d, want 1", got)
	}
	v.ScrollToLine(42) // blocks start at odd lines: 41 is the last ≤ 42
	h.Frame(v.Layout)
	if got := v.TopLine(); got != 41 {
		t.Fatalf("TopLine after ScrollToLine(42) = %d, want 41", got)
	}
	v.ScrollToLine(0)
	h.Frame(v.Layout)
	if got := v.TopLine(); got != 1 {
		t.Fatalf("TopLine after ScrollToLine(0) = %d", got)
	}
	if !v.ScrollToAnchor("#target") {
		t.Fatal("anchor not found")
	}
	h.Frame(v.Layout)
	if got := v.TopLine(); got != 301 {
		t.Fatalf("TopLine after anchor = %d, want 301", got)
	}
	if v.ScrollToAnchor("nope") {
		t.Fatal("unknown anchor found")
	}

	// A new document (one changed block) keeps the scroll position and
	// the caches of unchanged blocks.
	v.ScrollToLine(101)
	h.Frame(v.Layout)
	keep := v.nodes[51]
	if !keep.text.shaped {
		t.Fatal("visible block not shaped")
	}
	d2 := paragraphs(200)
	d2.Blocks[150] = d.Blocks[150]
	d2.Blocks[3].Spans = sp("edited")
	v.SetDoc(d2)
	h.Frame(v.Layout)
	if got := v.TopLine(); got != 101 {
		t.Fatalf("TopLine after SetDoc = %d, want 101", got)
	}
	if v.nodes[51] != keep {
		t.Fatal("unchanged block lost its cache")
	}
	// Inserting a block keeps the caches of the following blocks (suffix
	// match) with their new lines.
	d3 := &markdown.Doc{Blocks: append([]markdown.Block{{Kind: markdown.Rule, Line: 1}}, d2.Blocks...)}
	for i := 1; i < len(d3.Blocks); i++ {
		d3.Blocks[i].Line += 2
	}
	v.SetDoc(d3)
	if v.nodes[52] != keep || v.nodes[52].b.Line != 105 {
		t.Fatalf("inserted block: cache not kept (line %d)", v.nodes[52].b.Line)
	}
}

func TestLinkClick(t *testing.T) {
	h := uitest.NewHarness(image.Pt(600, 300))
	var v View
	v.SetDoc(&markdown.Doc{Blocks: []markdown.Block{{Kind: markdown.Paragraph, Line: 1, Spans: []markdown.Span{
		{Text: "See "}, {Text: "the ", Link: "docs/a.md#x"}, {Text: "docs", Style: markdown.Bold, Link: "docs/a.md#x"}, {Text: " please."},
	}}}})
	h.Frame(v.Layout)
	rt := v.nodes[0].text
	if len(rt.links) != 1 || len(rt.links[0].rects) == 0 {
		t.Fatalf("links: %+v", rt.links)
	}
	left, _ := contentBox(pxCtx(), h.Theme, 600)
	r := rt.links[0].rects[0].Add(image.Pt(left, 24)) // XL top padding
	h.Click(image.Pt(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2))
	evs := frameEvents(h, &v)
	if len(evs) != 1 || evs[0] != (LinkEvent{Dest: "docs/a.md#x"}) {
		t.Fatalf("events %v", evs)
	}
	// Click outside the link: no event.
	h.Click(image.Pt(left+1, 24+1))
	if miss := frameEvents(h, &v); len(miss) != 0 {
		t.Fatalf("click outside the link: %v", miss)
	}
}

// frameEvents runs a frame that collects the events before laying out.
func frameEvents(h *uitest.Harness, v *View) []Event {
	var evs []Event
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = v.Update(gtx)
		return v.Layout(gtx, th)
	})
	return evs
}

func pxCtx() layout.Context {
	return layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
}

func TestCopyButton(t *testing.T) {
	h := uitest.NewHarness(image.Pt(600, 300))
	var v View
	v.SetDoc(&markdown.Doc{Blocks: []markdown.Block{{Kind: markdown.CodeBlock, Line: 1, Lang: "go", Code: "fmt.Println(1)"}}})
	h.Frame(v.Layout)
	if len(v.hotCopy) != 1 {
		t.Fatalf("copy targets: %d", len(v.hotCopy))
	}
	left, cw := contentBox(pxCtx(), h.Theme, 600)
	// The button sits at the right end of the header (XS inset).
	h.Click(image.Pt(left+cw-4-10, 24+4+10))
	evs := frameEvents(h, &v)
	if len(evs) != 1 || evs[0] != (CopyEvent{Text: "fmt.Println(1)"}) {
		t.Fatalf("events %v", evs)
	}
}

func TestImageHook(t *testing.T) {
	doc := &markdown.Doc{Blocks: []markdown.Block{{Kind: markdown.Paragraph, Line: 1, Spans: []markdown.Span{
		{Text: "alt", Image: true, Link: "a.png"},
	}}}}
	run := func(v *View) runKind {
		v.SetDoc(doc)
		uitest.Smoke(t, "image", image.Pt(500, 400), v.Layout)
		return v.nodes[0].text.runs[0].kind
	}
	var plain View
	if k := run(&plain); k != runChip {
		t.Fatalf("no hook: kind %d, want chip", k)
	}
	calls := 0
	failing := View{LoadImage: func(string) (image.Image, bool) { calls++; return nil, false }}
	if k := run(&failing); k != runChip || calls == 0 {
		t.Fatalf("failing hook: kind %d calls %d", k, calls)
	}
	var srcs []string
	ok := View{LoadImage: func(src string) (image.Image, bool) {
		srcs = append(srcs, src)
		return image.NewNRGBA(image.Rect(0, 0, 2000, 1000)), true
	}}
	if k := run(&ok); k != runImage {
		t.Fatalf("hook: kind %d, want image", k)
	}
	if len(srcs) != 1 || srcs[0] != "a.png" {
		t.Fatalf("loader calls %v (want one, cached)", srcs)
	}
	// Scaled down to the content width, aspect kept.
	l := ok.nodes[0].text.lines[0]
	p := l.pieces[0]
	_, cw := contentBox(pxCtx(), tokens.Dark(nil), 500)
	if p.imgW != cw || p.imgH != cw/2 {
		t.Fatalf("image size %dx%d, want %dx%d", p.imgW, p.imgH, cw, cw/2)
	}
	ok.ReloadImages()
	uitest.Smoke(t, "reloaded", image.Pt(500, 400), ok.Layout)
	if len(srcs) != 2 {
		t.Fatalf("ReloadImages did not reload: %v", srcs)
	}
}

func TestPerformanceHugeDoc(t *testing.T) {
	var v View
	start := time.Now()
	v.SetDoc(paragraphs(5000))
	h := uitest.NewHarness(image.Pt(800, 900))
	for i := range 20 {
		v.ScrollToLine(i * 500)
		h.Frame(v.Layout)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("5000 paragraphs took %v", el)
	}
	shaped := 0
	for _, n := range v.nodes {
		if n.text.shaped {
			shaped++
		}
	}
	if shaped > 1000 {
		t.Fatalf("%d blocks shaped; virtualization broken", shaped)
	}
}

func TestAllocate(t *testing.T) {
	cases := []struct {
		mins, nats []int
		avail      int
		want       []int
	}{
		{[]int{10, 10}, []int{50, 60}, 200, []int{50, 60}},
		{[]int{10, 10}, []int{110, 60}, 100, []int{10 + 100*80/150, 10 + 50*80/150}},
		{[]int{100, 100}, []int{200, 200}, 100, []int{50, 50}},
	}
	for _, c := range cases {
		got := allocate(c.mins, c.nats, c.avail)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("allocate(%v, %v, %d) = %v, want %v", c.mins, c.nats, c.avail, got, c.want)
		}
	}
}

func TestWrapAndAlign(t *testing.T) {
	th := tokens.Dark(nil)
	var v View
	h := uitest.NewHarness(image.Pt(400, 400))
	rt := newRich([]markdown.Span{{Text: "aaa bbb ccc ddd"}, {Text: strings.Repeat("x", 200), Style: markdown.Code}}, styleBody)
	h.Frame(func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions {
		v.key = shapeKey{sh: th.Shaper}
		wide := rt.measure(gtx, th, &v, 10000)
		if len(rt.lines) != 1 {
			t.Errorf("wide: %d lines", len(rt.lines))
		}
		narrow := rt.measure(gtx, th, &v, 60)
		if len(rt.lines) < 5 || narrow.Y <= wide.Y {
			t.Errorf("narrow: %d lines, size %v (wide %v)", len(rt.lines), narrow, wide)
		}
		for _, l := range rt.lines {
			if l.w.Ceil() > 60+1 && len(l.pieces) > 1 {
				t.Errorf("line wider than 60: %v", l.w.Ceil())
			}
		}
		for _, a := range []text.Alignment{text.Start, text.Middle, text.End} {
			rt.layout(gtx, th, &v, 300, color.NRGBA{A: 255}, a)
		}
		return layout.Dimensions{Size: image.Pt(1, 1)}
	})
}
