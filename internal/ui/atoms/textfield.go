package atoms

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TextField is a stateful single- or multi-line text input with border,
// hint text and optional leading glyph / trailing widget.
//
// The field fills the maximum width of its constraints (like a block
// element); constrain the width from the outside.
type TextField struct {
	// Editor holds the text and cursor state. Configure SingleLine, Submit,
	// Filter, MaxLen etc. directly.
	Editor widget.Editor
	// Hint is shown while the field is empty.
	Hint string
	// Leading is an optional glyph drawn before the text.
	Leading *Glyph
	// Trailing is an optional widget drawn after the text (e.g. a clear
	// button). It is laid out with loose constraints.
	Trailing layout.Widget
	Disabled bool
	// Invalid draws the border in the danger color.
	Invalid bool
	// Mono uses the editor font (for paths, regexes, expressions).
	Mono bool
}

// fallbackFieldWidth is used when the width is unbounded.
const fallbackFieldWidth = unit.Dp(240)

// NewTextField returns a single-line field that emits SubmitEvents on Enter.
func NewTextField(hint string) *TextField {
	tf := &TextField{Hint: hint}
	tf.Editor.SingleLine = true
	tf.Editor.Submit = true
	return tf
}

// Text returns the current content.
func (t *TextField) Text() string { return t.Editor.Text() }

// SetText replaces the content.
func (t *TextField) SetText(s string) { t.Editor.SetText(s) }

// Focused reports whether the field has keyboard focus.
func (t *TextField) Focused(gtx layout.Context) bool { return gtx.Focused(&t.Editor) }

// Layout draws the field.
func (t *TextField) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if t.Disabled {
		gtx = gtx.Disabled()
	}
	c := th.Color
	w := draw.Width(gtx, gtx.Dp(fallbackFieldWidth))
	minH := gtx.Dp(th.Size.ControlH)
	padX := gtx.Dp(th.Space.S)
	padY := gtx.Dp(th.Space.XS)
	gap := gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)

	f := th.Font.UI
	size := th.Text.Body
	if t.Mono {
		f, size = th.Font.Mono, th.Text.Code
	}
	fg := c.Text
	if t.Disabled {
		fg = c.TextDisabled
	}

	// Leading icon.
	leadW := 0
	var leadCall op.CallOp
	var leadSize image.Point
	if t.Leading != nil {
		m := op.Record(gtx.Ops)
		d := Icon{Glyph: t.Leading, Color: c.TextMuted}.Layout(gtx, th)
		leadCall, leadSize = m.Stop(), d.Size
		leadW = d.Size.X + gap
	}
	// Trailing widget.
	trailW := 0
	var trailCall op.CallOp
	var trailSize image.Point
	if t.Trailing != nil {
		tg := gtx
		tg.Constraints = layout.Constraints{Max: image.Pt(w/2, minH)}
		m := op.Record(gtx.Ops)
		d := t.Trailing(tg)
		trailCall, trailSize = m.Stop(), d.Size
		trailW = d.Size.X + gap/2
	}

	// Text area.
	textW := max(0, w-2*padX-leadW-trailW)
	eg := gtx
	eg.Constraints = layout.Constraints{Min: image.Pt(textW, 0), Max: image.Pt(textW, draw.Unbounded)}
	if t.Editor.SingleLine {
		eg.Constraints.Max.Y = minH
	}
	m := op.Record(gtx.Ops)
	ed := t.layoutEditor(eg, th, f, size, fg)
	edCall := m.Stop()

	h := max(minH, ed.Size.Y+2*padY)
	sz := gtx.Constraints.Constrain(image.Pt(w, h))
	r := image.Rectangle{Max: sz}
	rad := gtx.Dp(th.Radius.M)

	bg, border := c.Surface, c.BorderStrong
	focused := gtx.Focused(&t.Editor)
	switch {
	case t.Disabled:
		bg, border = c.SurfaceAlt, c.Border
	case t.Invalid:
		border = c.Danger
	case focused:
		border = c.Focus
	}
	draw.RRect(gtx.Ops, r, rad, bg)
	bw := gtx.Dp(th.Size.BorderWidth)
	if focused || t.Invalid {
		bw = max(bw, gtx.Dp(1.5))
	}
	draw.Border(gtx.Ops, r, rad, bw, border)

	x := padX
	if t.Leading != nil {
		tr := op.Offset(image.Pt(x, (sz.Y-leadSize.Y)/2)).Push(gtx.Ops)
		leadCall.Add(gtx.Ops)
		tr.Pop()
		x += leadW
	}
	ty := (sz.Y - ed.Size.Y) / 2
	if !t.Editor.SingleLine {
		ty = padY
	}
	tr := op.Offset(image.Pt(x, ty)).Push(gtx.Ops)
	edCall.Add(gtx.Ops)
	tr.Pop()
	if t.Trailing != nil {
		tr := op.Offset(image.Pt(sz.X-padX/2-trailSize.X, (sz.Y-trailSize.Y)/2)).Push(gtx.Ops)
		trailCall.Add(gtx.Ops)
		tr.Pop()
	}
	return layout.Dimensions{Size: sz, Baseline: sz.Y - ty - ed.Size.Y + ed.Baseline}
}

func (t *TextField) layoutEditor(gtx layout.Context, th *tokens.Theme, f font.Font, size unit.Sp, fg color.NRGBA) layout.Dimensions {
	c := th.Color
	textMat := material(gtx, fg)
	selMat := material(gtx, tokens.WithAlpha(c.Accent, 0x50))
	hintMat := material(gtx, c.TextMuted)

	var maxLines int
	if t.Editor.SingleLine {
		maxLines = 1
	}
	// Hint is measured so an empty field keeps the hint's height.
	m := op.Record(gtx.Ops)
	hint := widget.Label{MaxLines: maxLines}.Layout(gtx, th.Shaper, f, size, t.Hint, hintMat)
	hintCall := m.Stop()
	if gtx.Constraints.Min.Y < hint.Size.Y {
		gtx.Constraints.Min.Y = hint.Size.Y
	}
	d := t.Editor.Layout(gtx, th.Shaper, f, size, textMat, selMat)
	if t.Editor.Len() == 0 {
		hintCall.Add(gtx.Ops)
	}
	return d
}

func material(gtx layout.Context, col color.NRGBA) op.CallOp {
	m := op.Record(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	return m.Stop()
}
