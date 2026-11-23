// Package mdview is the Markdown reader: it renders a parsed
// markdown.Doc read-only with Gio – headings, paragraphs with wrapping
// rich text (bold, italic, strikethrough, inline code, links), code
// blocks with a copy button, block quotes, ordered/unordered/task lists,
// tables, thematic breaks, raw HTML (as muted source text) and images.
//
// The view is virtualized by top-level block: only visible blocks are
// shaped and painted, so documents of thousands of lines scroll smoothly.
// Shaped and wrapped text is cached per block; SetDoc keeps the caches of
// unchanged blocks and the scroll position (live preview while typing).
//
// Code blocks wrap long lines (at spaces, or between characters when a
// word is longer than the line) instead of scrolling horizontally, so
// the reader never needs a second scroll axis.
//
// Images are never fetched by the view: View.LoadImage (optional) hands
// out decoded images; without it, or when it fails, an image is shown as
// a placeholder chip with icon, alt text and source.
package mdview

import (
	"image"
	"slices"
	"sort"
	"strings"

	"gioui.org/gesture"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Event is reported by View.Update.
type Event interface{ isMDEvent() }

// LinkEvent: the user clicked a link. Dest is the span's Link as written
// in the document (URL, relative path or "#anchor"); the host decides what
// to do (ScrollToAnchor, open a file, open the browser after asking).
type LinkEvent struct{ Dest string }

// CopyEvent: the user clicked "Copy" on a code block. Text is the code;
// the host puts it on the clipboard.
type CopyEvent struct{ Text string }

func (LinkEvent) isMDEvent() {}
func (CopyEvent) isMDEvent() {}

// View is the Markdown reader organism. Keep it in the caller's state
// across frames; the zero value shows an empty document.
type View struct {
	// LoadImage, when set, resolves an image source (as written in the
	// document) to a decoded image. The view never touches the network or
	// the filesystem itself. Results that return an image are cached per
	// source until ReloadImages; failures are retried after SetDoc for
	// changed blocks or after ReloadImages.
	LoadImage func(src string) (image.Image, bool)

	list    molecules.ScrollList
	doc     *markdown.Doc
	nodes   []*node
	anchors map[string]int

	key    shapeKey
	images map[string]*loadedImage
	failed map[string]bool
	imgGen int

	// Interaction targets of the last layout (queried by Update).
	hotLinks []*link
	hotCopy  []*node
}

// loadedImage is a cached image of LoadImage.
type loadedImage struct {
	op   paint.ImageOp
	size image.Point
}

// Doc returns the current document (nil before SetDoc).
func (v *View) Doc() *markdown.Doc { return v.doc }

// SetDoc replaces the document. The scroll position is kept, and blocks
// whose content did not change keep their shaped text, so calling SetDoc
// on every keystroke of a live preview is cheap. Treat d as immutable
// afterwards.
func (v *View) SetDoc(d *markdown.Doc) {
	var blocks []markdown.Block
	if d != nil {
		blocks = d.Blocks
	}
	old := v.nodes
	nodes := make([]*node, len(blocks))
	p := 0
	for p < len(old) && p < len(blocks) && sameBlock(&old[p].b, &blocks[p]) {
		nodes[p] = old[p]
		nodes[p].rebind(&blocks[p])
		p++
	}
	for s := 0; s < len(old)-p && s < len(blocks)-p; s++ {
		o, n := old[len(old)-1-s], len(blocks)-1-s
		if !sameBlock(&o.b, &blocks[n]) {
			break
		}
		nodes[n] = o
		o.rebind(&blocks[n])
	}
	for i := range nodes {
		if nodes[i] == nil {
			nodes[i] = buildNode(&blocks[i])
		}
	}
	v.doc, v.nodes = d, nodes
	clear(v.failed)
	v.anchors = map[string]int{}
	for i := range blocks {
		collectAnchors(&blocks[i], i, v.anchors)
	}
}

func collectAnchors(b *markdown.Block, top int, m map[string]int) {
	if b.Kind == markdown.Heading && b.Anchor != "" {
		if _, ok := m[b.Anchor]; !ok {
			m[b.Anchor] = top
		}
	}
	for i := range b.Children {
		collectAnchors(&b.Children[i], top, m)
	}
}

// ReloadImages drops all cached images and reshapes the text, so
// LoadImage is asked again (e.g. after an image file changed or the
// loader was replaced).
func (v *View) ReloadImages() {
	v.images, v.failed = nil, nil
	v.imgGen++
}

// image returns the cached or freshly loaded image for src, or nil.
func (v *View) image(src string) *loadedImage {
	if img, ok := v.images[src]; ok {
		return img
	}
	if v.LoadImage == nil || src == "" || v.failed[src] {
		return nil
	}
	img, ok := v.LoadImage(src)
	if !ok || img == nil || img.Bounds().Empty() {
		if v.failed == nil {
			v.failed = map[string]bool{}
		}
		v.failed[src] = true
		return nil
	}
	if v.images == nil {
		v.images = map[string]*loadedImage{}
	}
	li := &loadedImage{op: paint.NewImageOp(img), size: img.Bounds().Size()}
	v.images[src] = li
	return li
}

// ScrollToLine scrolls to the top-level block containing source line
// line: the last block whose Line is ≤ line (editor → preview sync).
func (v *View) ScrollToLine(line int) {
	if v.doc == nil || len(v.doc.Blocks) == 0 {
		return
	}
	bs := v.doc.Blocks
	i := sort.Search(len(bs), func(i int) bool { return bs[i].Line > line }) - 1
	v.list.ScrollTo(max(0, i))
}

// ScrollToAnchor scrolls to the heading with the given anchor (a leading
// "#" is ignored) and reports whether it exists.
func (v *View) ScrollToAnchor(anchor string) bool {
	i, ok := v.anchors[strings.TrimPrefix(anchor, "#")]
	if ok {
		v.list.ScrollTo(i)
	}
	return ok
}

// TopLine returns the source line of the first visible top-level block
// (preview → editor sync), or 0 for an empty document.
func (v *View) TopLine() int {
	if v.doc == nil || len(v.doc.Blocks) == 0 {
		return 0
	}
	i := min(max(0, v.list.FirstVisible()), len(v.doc.Blocks)-1)
	return v.doc.Blocks[i].Line
}

// Update returns the user's intentions since the last frame. Layout calls
// it too (discarding the events); call it before Layout to receive them.
func (v *View) Update(gtx layout.Context) []Event {
	var out []Event
	for _, l := range v.hotLinks {
		for _, h := range l.hits {
			for {
				e, ok := h.Update(gtx.Source)
				if !ok {
					break
				}
				if e.Kind == gesture.KindClick {
					out = append(out, LinkEvent{Dest: l.dest})
				}
			}
		}
	}
	for _, n := range v.hotCopy {
		for n.copy.Clicked(gtx) {
			out = append(out, CopyEvent{Text: n.b.Code})
		}
	}
	return out
}

// Layout draws the document filling the constraints, vertically
// scrollable, with the content centered at a readable maximum width.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.Update(gtx)
	v.hotLinks, v.hotCopy = v.hotLinks[:0], v.hotCopy[:0]
	size := image.Pt(draw.Width(gtx, gtx.Dp(480)), gtx.Constraints.Max.Y)
	if size.Y >= draw.Unbounded {
		size.Y = max(gtx.Constraints.Min.Y, gtx.Dp(320))
	}
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Background)
	v.key = shapeKey{
		sh: th.Shaper, ui: th.Font.UI, mono: th.Font.Mono, body: readerSize(th), code: th.Text.Code,
		pxPerSp: gtx.Metric.PxPerSp, pxDp: gtx.Metric.PxPerDp, imgGen: v.imgGen, hasImageLoader: v.LoadImage != nil,
	}
	gtx.Constraints = layout.Exact(size)
	if len(v.nodes) == 0 {
		l := atoms.Muted("Empty document")
		m := op.Record(gtx.Ops)
		lg := gtx
		lg.Constraints.Min = image.Point{}
		d := l.Layout(lg, th)
		call := m.Stop()
		draw.Centered(gtx.Ops, size, d.Size, call)
		return layout.Dimensions{Size: size}
	}
	v.list.Layout(gtx, th, len(v.nodes), func(gtx layout.Context, i int) layout.Dimensions {
		return v.element(gtx, th, i)
	})
	// Register the pointer filters of link areas that appeared in this
	// frame (Gio routes events only to targets that asked for them); the
	// pending events were consumed by Update above.
	for _, l := range v.hotLinks {
		for _, h := range l.hits[:len(l.rects)] {
			for {
				if _, ok := h.Update(gtx.Source); !ok {
					break
				}
			}
		}
	}
	return layout.Dimensions{Size: size}
}

// contentBox returns the left offset and width of the content column for
// a view of width w.
func contentBox(gtx layout.Context, th *tokens.Theme, w int) (left, cw int) {
	pad := gtx.Dp(th.Space.XL)
	// About 100 characters of reader text.
	maxW := gtx.Sp(readerSize(th)) * 56
	cw = max(1, min(w-2*pad, maxW))
	return max(0, (w-cw)/2), cw
}

// element lays out top-level block i with its outer spacing.
func (v *View) element(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	w := gtx.Constraints.Max.X
	left, cw := contentBox(gtx, th, w)
	top := gtx.Dp(th.Space.XL)
	if i > 0 {
		top = v.gap(gtx, th, v.nodes[i], false)
	}
	bottom := 0
	if i == len(v.nodes)-1 {
		bottom = gtx.Dp(th.Space.XXL)
	}
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(cw, draw.Unbounded)}
	t := op.Offset(image.Pt(left, top)).Push(gtx.Ops)
	d := v.layoutNode(cg, th, v.nodes[i], ctx{})
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, top+d.Size.Y+bottom)}
}

// gap is the space above block n inside a stack.
func (v *View) gap(gtx layout.Context, th *tokens.Theme, n *node, tight bool) int {
	switch {
	case tight:
		return gtx.Dp(th.Space.XS)
	case n.b.Kind == markdown.Heading:
		return gtx.Dp(th.Space.XL)
	}
	return gtx.Dp(th.Space.L)
}

// sameBlock reports whether a and b render identically (Line ignored).
func sameBlock(a, b *markdown.Block) bool {
	if a.Kind != b.Kind || a.Level != b.Level || a.Anchor != b.Anchor || a.Lang != b.Lang ||
		a.Code != b.Code || a.Ordered != b.Ordered || a.Start != b.Start || a.Tight != b.Tight ||
		a.Task != b.Task || a.Checked != b.Checked || !slices.Equal(a.Spans, b.Spans) ||
		!slices.Equal(a.Align, b.Align) || len(a.Rows) != len(b.Rows) || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Rows {
		if len(a.Rows[i]) != len(b.Rows[i]) {
			return false
		}
		for j := range a.Rows[i] {
			if !slices.Equal(a.Rows[i][j].Spans, b.Rows[i][j].Spans) {
				return false
			}
		}
	}
	for i := range a.Children {
		if !sameBlock(&a.Children[i], &b.Children[i]) {
			return false
		}
	}
	return true
}
