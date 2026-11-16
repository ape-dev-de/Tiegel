package molecules

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ToolbarItem describes one toolbar entry. Items are data: the host may
// change them between frames (e.g. Active or Disabled).
type ToolbarItem struct {
	// ID is reported when the item is clicked.
	ID    string
	Glyph *atoms.Glyph
	// Label is the tooltip and accessible name.
	Label string
	// Shortcut is shown in the tooltip, e.g. "F8".
	Shortcut string
	// Active draws the item as a pressed toggle.
	Active   bool
	Disabled bool
	// Separator draws a vertical divider instead of a button.
	Separator bool
}

// Tooltip returns the tooltip text: label plus shortcut.
func (it ToolbarItem) Tooltip() string {
	if it.Shortcut == "" {
		return it.Label
	}
	return it.Label + " (" + it.Shortcut + ")"
}

// Toolbar is a row of icon buttons with tooltips and separators.
type Toolbar struct {
	Items []ToolbarItem
	// ButtonSize is the edge of the square buttons; zero means
	// th.Size.ControlH. Use th.Size.RowH for compact section headers.
	ButtonSize unit.Dp

	btns []atoms.IconButton
	tips []atoms.Tooltip
}

func (t *Toolbar) sync() {
	if len(t.btns) != len(t.Items) {
		t.btns = make([]atoms.IconButton, len(t.Items))
		t.tips = make([]atoms.Tooltip, len(t.Items))
	}
	for i, it := range t.Items {
		b := &t.btns[i]
		b.Glyph, b.Description, b.Active, b.Disabled = it.Glyph, it.Label, it.Active, it.Disabled
		b.Size = t.ButtonSize
		t.tips[i].Text = it.Tooltip()
	}
}

// Update returns the IDs of the items clicked since the last call.
func (t *Toolbar) Update(gtx layout.Context) []string {
	t.sync()
	var out []string
	for i := range t.btns {
		if !t.Items[i].Separator && t.btns[i].Clicked(gtx) {
			out = append(out, t.Items[i].ID)
		}
	}
	return out
}

// Layout draws the toolbar at its intrinsic width.
func (t *Toolbar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t.Update(gtx)
	h := gtx.Dp(th.Size.ControlH)
	if t.ButtonSize > 0 {
		h = gtx.Dp(t.ButtonSize)
	}
	fc := make([]layout.FlexChild, 0, len(t.Items))
	for i := range t.Items {
		if t.Items[i].Separator {
			fc = append(fc, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(th.Size.BorderWidth), h))
					return atoms.Divider{Vertical: true, Inset: th.Space.XS}.Layout(gtx, th)
				})
			}))
			continue
		}
		fc = append(fc, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return t.tips[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return t.btns[i].Layout(gtx, th)
			})
		}))
	}
	gtx.Constraints.Min = image.Point{}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, fc...)
}
