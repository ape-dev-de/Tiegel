package gui

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// This file holds the generic helpers features use besides the Registry:
// a chooser with a callback (branch switcher, breakpoint condition), small
// popups (confirmations, the git change popup), the caret line and the
// clipboard.

// ChooserSpec describes a chooser opened by a feature.
type ChooserSpec struct {
	Title, Hint, Query string
	Items              []palette.Item
	// NoList shows only the input (e.g. a name or an expression).
	NoList bool
	// OnQuery returns the items for a changed query (nil: static items).
	OnQuery func(q string) []palette.Item
	// OnChoose receives the chosen item index (-1 without list) and the
	// query.
	OnChoose func(index int, query string)
	// Tabs are categories (Tab/Shift+Tab switch); OnTab returns the items
	// of category index for the query.
	Tabs  []string
	OnTab func(index int, query string) []palette.Item
}

// OpenChooser shows a chooser.
func (v *View) OpenChooser(cs ChooserSpec) {
	v.closeOverlay()
	v.focusEditor = false
	v.custom = &cs
	v.chooseMode = chooseCustom
	v.chooser.Open(cs.Title, cs.Hint, cs.Query)
	v.chooser.NoList = cs.NoList
	v.chooser.Tabs, v.chooser.Tab = cs.Tabs, 0
	v.chooser.Items = append(v.chooser.Items[:0], cs.Items...)
}

// PopupButton is a button of a popup; the popup closes after OnClick.
type PopupButton struct {
	Text    string
	Kind    atoms.ButtonKind
	OnClick func()
	btn     atoms.Button
}

// Popup is a small dialog above the workbench (Escape or a click outside
// closes it).
type Popup struct {
	Title, Detail string
	// Body is optional content below the detail text.
	Body func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	// Width is the popup width (default 420dp); BodyHeight the height of
	// Body (default: its own height).
	Width, BodyHeight unit.Dp
	Buttons           []*PopupButton
	Centered          bool
	// OnClose runs when the popup closes without a button.
	OnClose func()
}

// OpenPopup shows p (replacing other overlays).
func (v *View) OpenPopup(p *Popup) {
	v.closeOverlay()
	v.focusEditor = false
	v.popup = p
}

// ClosePopup closes the popup, if any.
func (v *View) ClosePopup() { v.closeOverlay() }

// PopupOpen reports whether a popup is shown.
func (v *View) PopupOpen() bool { return v.popup != nil }

// Confirm asks before a destructive action.
func (v *View) Confirm(title, detail, ok string, onOK func()) {
	v.OpenPopup(&Popup{Title: title, Detail: detail, Centered: true, Buttons: []*PopupButton{
		{Text: ok, Kind: atoms.Primary, OnClick: onOK},
		{Text: "Cancel", Kind: atoms.Secondary},
	}})
}

// handlePopup processes popup buttons and Escape.
func (v *View) handlePopup(gtx layout.Context) {
	p := v.popup
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			v.closeOverlay()
			return
		}
	}
	for _, b := range p.Buttons {
		if b.btn.Clicked(gtx) {
			v.popup = nil
			v.focusEditor = true
			if b.OnClick != nil {
				b.OnClick()
			}
			return
		}
	}
}

// Layout draws the popup card.
func (p *Popup) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w := p.Width
	if w == 0 {
		w = 420
	}
	width := min(gtx.Dp(w), gtx.Constraints.Max.X)
	gtx.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, gtx.Constraints.Max.Y)}
	return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(th.Space.M).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			var rows []layout.FlexChild
			if p.Title != "" {
				rows = append(rows, layout.Rigid(atoms.Strong(p.Title).Fn(th)))
			}
			if p.Detail != "" {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, atoms.Muted(p.Detail).Fn(th))
				}))
			}
			if p.Body != nil {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if p.BodyHeight > 0 {
							h := min(gtx.Dp(p.BodyHeight), gtx.Constraints.Max.Y)
							gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
						}
						return p.Body(gtx, th)
					})
				}))
			}
			if len(p.Buttons) > 0 {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						btns := make([]layout.FlexChild, 0, 2*len(p.Buttons)+1)
						btns = append(btns, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
						}))
						for i, b := range p.Buttons {
							b.btn.Text, b.btn.Kind = b.Text, b.Kind
							if i > 0 {
								btns = append(btns, layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout))
							}
							btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.btn.Layout(gtx, th) }))
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, btns...)
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
}

// CaretLine returns the active editor's file and 1-based caret line.
func (v *View) CaretLine() (file string, line int) {
	s := v.activeSession()
	if s == nil {
		return "", 0
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	b := s.doc.Buffer()
	return s.file, b.OffsetToPos(s.ed.Cursor().Primary().Head).Line + 1
}

// CopyText puts s on the clipboard (next frame).
func (v *View) CopyText(s string) { v.clipboard = &s }
