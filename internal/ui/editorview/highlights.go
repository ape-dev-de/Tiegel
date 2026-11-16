package editorview

import (
	"image"
	"slices"

	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
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
