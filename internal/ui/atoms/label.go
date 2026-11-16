// Package atoms contains Tiegel's smallest UI building blocks: labels,
// icons, buttons, text fields, checkboxes, badges, tooltips and dividers.
//
// See package internal/ui for the component conventions.
package atoms

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TextRole selects size, weight, font and default color of a Label.
type TextRole int

// Text roles.
const (
	RoleBody    TextRole = iota // default UI text
	RoleMuted                   // secondary text
	RoleSmall                   // captions, small muted text
	RoleStrong                  // emphasized body text
	RoleHeading                 // section heading
	RoleTitle                   // dialog / page title
	RoleCode                    // monospace text
)

// Label is a non-interactive piece of text. It is a value component: build
// it with Text/Muted/… or a literal and call Layout every frame.
type Label struct {
	Text string
	Role TextRole
	// Color overrides the role color when its alpha is non-zero.
	Color color.NRGBA
	// MaxLines truncates with "…"; zero means unlimited.
	MaxLines  int
	Alignment text.Alignment
}

// Text returns a body label.
func Text(s string) Label { return Label{Text: s, Role: RoleBody} }

// Muted returns a secondary label.
func Muted(s string) Label { return Label{Text: s, Role: RoleMuted} }

// Small returns a caption label.
func Small(s string) Label { return Label{Text: s, Role: RoleSmall} }

// Strong returns an emphasized label.
func Strong(s string) Label { return Label{Text: s, Role: RoleStrong} }

// Heading returns a section heading.
func Heading(s string) Label { return Label{Text: s, Role: RoleHeading} }

// Title returns a title label.
func Title(s string) Label { return Label{Text: s, Role: RoleTitle} }

// Code returns a monospace label.
func Code(s string) Label { return Label{Text: s, Role: RoleCode} }

// WithColor returns a copy of l using c.
func (l Label) WithColor(c color.NRGBA) Label { l.Color = c; return l }

// SingleLine returns a copy of l truncated to one line.
func (l Label) SingleLine() Label { l.MaxLines = 1; return l }

// Fn adapts the label to a layout.Widget for use in Gio layouts.
func (l Label) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return l.Layout(gtx, th) }
}

// Layout draws the label. In a disabled context the role color becomes
// th.Color.TextDisabled; an explicit Color always wins.
func (l Label) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	f, size, col := l.style(th)
	if !gtx.Enabled() {
		col = th.Color.TextDisabled
	}
	if l.Color.A != 0 {
		col = l.Color
	}
	return draw.Text(gtx, th.Shaper, draw.TextOpts{
		Font: f, Size: size, Color: col, MaxLines: l.MaxLines, Alignment: l.Alignment,
	}, l.Text)
}

func (l Label) style(th *tokens.Theme) (font.Font, unit.Sp, color.NRGBA) {
	f := th.Font.UI
	switch l.Role {
	case RoleMuted:
		return f, th.Text.Body, th.Color.TextMuted
	case RoleSmall:
		return f, th.Text.Small, th.Color.TextMuted
	case RoleStrong:
		f.Weight = font.Bold
		return f, th.Text.Body, th.Color.Text
	case RoleHeading:
		f.Weight = font.Bold
		return f, th.Text.Heading, th.Color.Text
	case RoleTitle:
		f.Weight = font.Bold
		return f, th.Text.Title, th.Color.Text
	case RoleCode:
		return th.Font.Mono, th.Text.Code, th.Editor.Text
	}
	return f, th.Text.Body, th.Color.Text
}
