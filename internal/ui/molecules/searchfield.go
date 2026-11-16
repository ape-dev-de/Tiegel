// Package molecules combines atoms into small, reusable units: search
// field, file row, editor tab, breakpoint row and diff block.
//
// See package internal/ui for the component conventions.
package molecules

import (
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// SearchField is a single-line text field with a search glyph and a clear
// button that appears once text was entered.
type SearchField struct {
	Field atoms.TextField
	clear atoms.IconButton
}

// NewSearchField returns a search field with hint text.
func NewSearchField(hint string) *SearchField {
	s := &SearchField{}
	s.Field.Hint = hint
	s.Field.Leading = atoms.GlyphSearch
	s.Field.Editor.SingleLine = true
	s.Field.Editor.Submit = true
	s.clear = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Clear search"}
	return s
}

// Text returns the query.
func (s *SearchField) Text() string { return s.Field.Text() }

// SetText replaces the query.
func (s *SearchField) SetText(q string) { s.Field.SetText(q) }

// Update processes events and reports whether the query changed (typing or
// clearing) and whether Enter was pressed.
func (s *SearchField) Update(gtx layout.Context) (changed, submitted bool) {
	if s.clear.Clicked(gtx) {
		s.Field.SetText("")
		changed = true
	}
	for {
		ev, ok := s.Field.Editor.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.ChangeEvent:
			changed = true
		case widget.SubmitEvent:
			submitted = true
		}
	}
	return changed, submitted
}

// Layout draws the field. Call Update before Layout to observe changes.
func (s *SearchField) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	// Handle a pending clear click even if the caller skipped Update.
	if s.clear.Clicked(gtx) {
		s.Field.SetText("")
	}
	s.clear.Size = th.Size.ControlH - th.Space.S
	s.clear.IconSize = th.Size.IconSmall
	s.Field.Trailing = nil
	if s.Field.Editor.Len() > 0 && !s.Field.Disabled {
		s.Field.Trailing = func(gtx layout.Context) layout.Dimensions { return s.clear.Layout(gtx, th) }
	}
	return s.Field.Layout(gtx, th)
}
