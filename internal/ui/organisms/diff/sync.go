package diff

import "image"

// scroller is the part of editorview.Editor the scroll synchronization
// needs (an interface so it can be tested without editors).
type scroller interface {
	ScrollOffset() image.Point
	SetScrollOffset(image.Point)
}

// scrollSync keeps two editors aligned: whichever side the user scrolls
// (wheel, scrollbar, caret movement) leads, the other follows through a
// line mapping. A follower's offset may still be clamped by its next
// Layout; that change must not be mistaken for a user scroll, otherwise
// the two sides would push each other around.
type scrollSync struct {
	init             bool
	lastL, lastR     image.Point // offsets after the last frame
	preL, preR       image.Point // offsets before this frame's Layout
	followL, followR bool        // side was positioned by the sync
}

// before runs after the editors processed their input and before their
// Layout. mapLR and mapRL map a vertical offset of one side to the other.
func (s *scrollSync) before(l, r scroller, mapLR, mapRL func(int) int) {
	pl, pr := l.ScrollOffset(), r.ScrollOffset()
	if !s.init {
		s.lastL, s.lastR, s.init = pl, pr, true
	}
	switch {
	case pl != s.lastL:
		r.SetScrollOffset(image.Pt(pl.X, mapLR(pl.Y)))
		s.followR, s.followL = true, false
	case pr != s.lastR:
		l.SetScrollOffset(image.Pt(pr.X, mapRL(pr.Y)))
		s.followL, s.followR = true, false
	}
	s.preL, s.preR = l.ScrollOffset(), r.ScrollOffset()
}

// after runs after both Layouts. It reports whether a side moved during
// its Layout (e.g. to reveal the caret) so the other side was adjusted and
// needs another frame.
func (s *scrollSync) after(l, r scroller, mapLR, mapRL func(int) int) bool {
	al, ar := l.ScrollOffset(), r.ScrollOffset()
	again := false
	switch {
	case !s.followL && al != s.preL:
		r.SetScrollOffset(image.Pt(al.X, mapLR(al.Y)))
		s.followR, s.followL, again = true, false, true
	case !s.followR && ar != s.preR:
		l.SetScrollOffset(image.Pt(ar.X, mapRL(ar.Y)))
		s.followL, s.followR, again = true, false, true
	default:
		s.followL, s.followR = false, false
	}
	s.lastL, s.lastR = l.ScrollOffset(), r.ScrollOffset()
	return again
}

// reset forgets the offsets (new content).
func (s *scrollSync) reset() { *s = scrollSync{} }
