package atoms

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BadgeKind selects the semantic color of a Badge.
type BadgeKind int

// Badge kinds.
const (
	BadgeNeutral BadgeKind = iota
	BadgeAccent
	BadgeSuccess
	BadgeWarning
	BadgeDanger
	BadgeInfo
)

// BadgeKinds lists all kinds (for the catalog and tests).
var BadgeKinds = []BadgeKind{BadgeNeutral, BadgeAccent, BadgeSuccess, BadgeWarning, BadgeDanger, BadgeInfo}

// String returns the kind name.
func (k BadgeKind) String() string {
	return [...]string{"neutral", "accent", "success", "warning", "danger", "info"}[k]
}

// Badge is a small pill with a short text: counts, PHP versions, states.
// It is a value component.
type Badge struct {
	Text string
	Kind BadgeKind
	// Solid uses a filled background instead of a subtle tint.
	Solid bool
}

// Layout draws the badge.
func (b Badge) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	bg, fg := b.colors(th)
	padX := gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)
	padY := gtx.Dp(th.Space.XXS)

	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	l := Label{Text: b.Text, Role: RoleSmall, Color: fg, MaxLines: 1}
	td := l.Layout(cgtx, th)
	call := m.Stop()

	size := image.Pt(td.Size.X+2*padX, td.Size.Y+2*padY)
	size.X = max(size.X, size.Y)
	size = gtx.Constraints.Constrain(size)
	draw.RRect(gtx.Ops, image.Rectangle{Max: size}, gtx.Dp(th.Radius.Pill), bg)
	draw.Centered(gtx.Ops, size, td.Size, call)
	return layout.Dimensions{Size: size, Baseline: (size.Y-td.Size.Y)/2 + td.Baseline}
}

func (b Badge) colors(th *tokens.Theme) (bg, fg color.NRGBA) {
	c := th.Color
	var base color.NRGBA
	switch b.Kind {
	case BadgeAccent:
		base = c.Accent
	case BadgeSuccess:
		base = c.Success
	case BadgeWarning:
		base = c.Warning
	case BadgeDanger:
		base = c.Danger
	case BadgeInfo:
		base = c.Info
	default:
		if b.Solid {
			return c.TextMuted, c.Surface
		}
		return c.SurfaceAlt, c.TextMuted
	}
	if b.Solid {
		return base, c.OnAccent
	}
	// Subtle: tinted background, text in the kind color (contrast is
	// guaranteed by the token tests against the surfaces).
	return tokens.Over(tokens.WithAlpha(base, 0x2E), c.Surface), base
}

// Fn adapts the badge to a layout.Widget.
func (b Badge) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
}
