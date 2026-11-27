package editorview

import (
	"bytes"
	"image"
	"slices"

	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Highlight marks a usage of the symbol under the caret (byte range),
// e.g. from the language server's document highlights.
type Highlight struct {
	Start, End int
	// Write marks a write access (drawn stronger).
	Write bool
}

// SetHighlights shows usage highlights for the given buffer version; they
// disappear as soon as the buffer changes (the host requests new ones).
// nil clears them.
func (e *Editor) SetHighlights(version uint64, hs []Highlight) {
	e.hls, e.hlVersion = slices.Clone(hs), version
}

// Highlights returns the current usage highlights.
func (e *Editor) Highlights() []Highlight { return slices.Clone(e.hls) }

func (e *Editor) paintHighlights(ops *op.Ops, th *tokens.Theme, s buffer.Snapshot, vis []visLine, ox int) {
	if len(e.hls) == 0 || len(vis) == 0 {
		return
	}
	if e.hlVersion != s.Version() {
		e.hls = nil
		return
	}
	lo, hi := vis[0].start, vis[len(vis)-1].end
	for _, h := range e.hls {
		if h.End <= lo || h.Start > hi || h.End <= h.Start || h.End > s.Len() {
			continue
		}
		c := tokens.WithAlpha(th.Color.Accent, 0x30)
		if h.Write {
			c = tokens.WithAlpha(th.Color.Danger, 0x30)
		}
		e.rangeCols(vis, h.Start, h.End, func(v visLine, c0, c1 int) {
			draw.RRect(ops, image.Rect(ox+e.met.colX(c0), v.y+1, ox+e.met.colX(c1), v.y+e.geo.lineH-1), 2, c)
		})
	}
}

// maxOccurrenceLen caps the selection whose other occurrences are marked.
const maxOccurrenceLen = 200

// selectionOccurrences returns the byte ranges in [lo, hi) that equal the
// selected text of the primary selection (single line, not blank), except
// the selection itself. A selected whole identifier ("$request", "name")
// matches whole words only, so $request does not mark $requestId.
func selectionOccurrences(s buffer.Snapshot, sel cursor.Selection, lo, hi int) [][2]int {
	if sel.Empty() || sel.End()-sel.Start() > maxOccurrenceLen {
		return nil
	}
	needle := s.Slice(sel.Start(), sel.End())
	if bytes.ContainsAny(needle, "\n\r") || len(bytes.TrimSpace(needle)) == 0 {
		return nil
	}
	// Whole words only when the selection is a whole word itself.
	st, en := sel.Start(), sel.End()
	word := isWordLike(needle) && (st == 0 || !isWordByte(s.Slice(st-1, st)[0])) && (en >= s.Len() || !isWordByte(s.Slice(en, en+1)[0]))
	lo, hi = max(lo-len(needle), 0), min(hi+len(needle), s.Len())
	text := s.Slice(lo, hi)
	var out [][2]int
	for i := 0; i <= len(text)-len(needle); {
		j := bytes.Index(text[i:], needle)
		if j < 0 {
			break
		}
		a := lo + i + j
		b := a + len(needle)
		i += j + 1
		if a == sel.Start() {
			continue
		}
		if word && (a > 0 && isWordByte(s.Slice(a-1, a)[0]) || b < s.Len() && isWordByte(s.Slice(b, b+1)[0])) {
			continue
		}
		out = append(out, [2]int{a, b})
	}
	return out
}

// isWordLike reports identifier-like text: an optional "$" followed by
// letters, digits and underscores.
func isWordLike(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("$"))
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !isWordByte(c) {
			return false
		}
	}
	return true
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// paintOccurrences marks the other occurrences of the selected text in
// the visible rows.
func (e *Editor) paintOccurrences(ops *op.Ops, th *tokens.Theme, s buffer.Snapshot, vis []visLine, ox int) {
	if len(vis) == 0 || len(e.cur.Selections()) != 1 {
		return
	}
	occ := selectionOccurrences(s, e.cur.Primary(), vis[0].start, vis[len(vis)-1].end)
	c := tokens.WithAlpha(th.Color.Accent, 0x26)
	for _, o := range occ {
		e.rangeCols(vis, o[0], o[1], func(v visLine, c0, c1 int) {
			draw.RRect(ops, image.Rect(ox+e.met.colX(c0), v.y+1, ox+e.met.colX(c1), v.y+e.geo.lineH-1), 2, c)
		})
	}
}
