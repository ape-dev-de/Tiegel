package molecules

import (
	"image"
	"image/color"
	"strconv"
	"sync"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// LineTone selects the background of a CodeLine.
type LineTone int

// Line tones.
const (
	ToneContext  LineTone = iota // unchanged code on the editor background
	ToneAdded                    // added / taken line (green)
	ToneRemoved                  // removed line (red)
	ToneHeader                   // hunk or block header
	ToneFiller                   // empty counterpart of a line on the other side
	ToneConflict                 // unresolved merge conflict
	ToneChanged                  // modified on one side of a merge (info tint)
)

// LineTones lists all tones (for the catalog and tests).
var LineTones = []LineTone{ToneContext, ToneAdded, ToneRemoved, ToneHeader, ToneFiller, ToneConflict, ToneChanged}

// String returns the tone name.
func (t LineTone) String() string {
	return [...]string{"context", "added", "removed", "header", "filler", "conflict", "changed"}[t]
}

// Colors returns the background and marker colors of the tone.
func (t LineTone) Colors(th *tokens.Theme) (bg, marker color.NRGBA) {
	e := th.Editor
	switch t {
	case ToneAdded:
		return e.DiffAddedBg, e.DiffAddedFg
	case ToneRemoved:
		return e.DiffRemovedBg, e.DiffRemovedFg
	case ToneHeader:
		return e.DiffHeaderBg, th.Color.TextMuted
	case ToneFiller:
		return tokens.Mix(e.Background, th.Color.SurfaceAlt, 0.6), e.LineNumber
	case ToneConflict:
		return tokens.Over(tokens.WithAlpha(th.Color.Warning, 0x30), e.Background), th.Color.Warning
	case ToneChanged:
		return tokens.Over(tokens.WithAlpha(th.Color.Info, 0x26), e.Background), th.Color.Info
	}
	return e.Background, e.LineNumber
}

// Span is a byte range [Start, End) of CodeLine.Text that is emphasized,
// e.g. the changed words of a modified line.
type Span struct{ Start, End int }

// CodeLine is one line of code in a diff or merge view: up to two line
// number columns, a one character marker ("+", "-", " "), the text and a
// background chosen by Tone. Spans are drawn with a stronger tint. Rows
// of a virtualized list stay aligned when every row uses the same Digits.
//
// It is a value component; the line fills the available width and is one
// line tall (long text is truncated with "…").
type CodeLine struct {
	// No and No2 are line numbers; zero leaves the column empty.
	No, No2 int
	// Columns is the number of line number columns (0, 1 or 2).
	Columns int
	// Digits is the width of each number column in digits; zero derives it
	// from the numbers of this line.
	Digits int
	Marker string
	Text   string
	Tone   LineTone
	Spans  []Span
	// Muted draws the text in the muted color (headers, placeholders).
	Muted bool
}

// Layout draws the line.
func (l CodeLine) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	e := th.Editor
	bg, marker := l.Tone.Colors(th)
	w := draw.Width(gtx, gtx.Dp(480))
	pad := gtx.Dp(th.Space.S)
	digits := l.Digits
	if digits <= 0 {
		digits = max(1, len(strconv.Itoa(l.No)), len(strconv.Itoa(l.No2)))
	}
	numW := digits * monoAdvance(gtx, th)
	fg := e.Text
	if l.Muted {
		fg = th.Color.TextMuted
	}

	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-pad), draw.Unbounded)}
	num := func(n int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Constraints{Min: image.Pt(numW, 0), Max: image.Pt(numW, gtx.Constraints.Max.Y)}
			s := ""
			if n > 0 {
				s = strconv.Itoa(n)
			}
			return atoms.Label{Text: s, Role: atoms.RoleCode, Color: e.LineNumber, Alignment: text.End, MaxLines: 1}.Layout(gtx, th)
		})
	}
	children := make([]layout.FlexChild, 0, 5)
	if l.Columns >= 1 {
		children = append(children, num(l.No))
	}
	if l.Columns >= 2 {
		children = append(children, layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout), num(l.No2))
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			mk := l.Marker
			if mk == "" {
				mk = " "
			}
			return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx,
				atoms.Label{Text: mk, Role: atoms.RoleCode, Color: marker, MaxLines: 1}.Fn(th))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return l.layoutText(gtx, th, fg, bg, marker)
		}),
	)
	m := op.Record(gtx.Ops)
	d := layout.Flex{Alignment: layout.Baseline}.Layout(cgtx, children...)
	call := m.Stop()

	lineH := lineHeight(gtx, th)
	h := max(d.Size.Y, lineH) + 2*gtx.Dp(1)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, h), bg)
	if l.Columns > 0 {
		gutterW := pad + l.Columns*numW + (l.Columns-1)*gtx.Dp(th.Space.S) + gtx.Dp(th.Space.XS)
		draw.Rect(gtx.Ops, image.Rect(0, 0, gutterW, h), tokens.Over(tokens.WithAlpha(e.Gutter, 0x80), bg))
	}
	t := op.Offset(image.Pt(pad, gtx.Dp(1))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, h), Baseline: h - gtx.Dp(1) - d.Size.Y + d.Baseline}
}

// Fn adapts the line to a layout.Widget.
func (l CodeLine) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return l.Layout(gtx, th) }
}

// layoutText draws the text, painting span backgrounds behind emphasized
// ranges. Spans are clamped to the text and assumed sorted.
func (l CodeLine) layoutText(gtx layout.Context, th *tokens.Theme, fg, bg, marker color.NRGBA) layout.Dimensions {
	if len(l.Spans) == 0 || l.Text == "" {
		return atoms.Label{Text: l.Text, Role: atoms.RoleCode, Color: fg, MaxLines: 1}.Layout(gtx, th)
	}
	adv := monoAdvance(gtx, th)
	hi := tokens.Over(tokens.WithAlpha(marker, 0x48), bg)
	h := lineHeight(gtx, th)
	maxW := gtx.Constraints.Max.X
	for _, s := range l.Spans {
		start, end := max(0, s.Start), min(len(l.Text), s.End)
		if end <= start {
			continue
		}
		x0 := runeCount(l.Text[:start]) * adv
		x1 := x0 + runeCount(l.Text[start:end])*adv
		if x0 >= maxW {
			break
		}
		draw.RRect(gtx.Ops, image.Rect(x0, 0, min(x1, maxW), h), gtx.Dp(th.Radius.S), hi)
	}
	return atoms.Label{Text: l.Text, Role: atoms.RoleCode, Color: fg, MaxLines: 1}.Layout(gtx, th)
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// codeMetricsKey identifies a shaper, code font size and pixel density.
type codeMetricsKey struct {
	shaper *text.Shaper
	size   float32
	pxSp   float32
}

// codeMetrics caches the advance and line height of the code font; rows
// of large virtualized lists ask for them on every line.
var codeMetrics sync.Map // codeMetricsKey -> image.Point{advance, height}

func codeMetric(gtx layout.Context, th *tokens.Theme) image.Point {
	k := codeMetricsKey{th.Shaper, float32(th.Text.Code), gtx.Metric.PxPerSp}
	if v, ok := codeMetrics.Load(k); ok {
		return v.(image.Point)
	}
	sz := measureCode(gtx, th, "0000000000")
	m := image.Pt(sz.X/10, sz.Y)
	codeMetrics.Store(k, m)
	return m
}

// monoAdvance returns the advance of one monospace character in pixels.
func monoAdvance(gtx layout.Context, th *tokens.Theme) int { return codeMetric(gtx, th).X }

// lineHeight returns the height of one line of code text.
func lineHeight(gtx layout.Context, th *tokens.Theme) int { return codeMetric(gtx, th).Y }

func measureCode(gtx layout.Context, th *tokens.Theme, s string) image.Point {
	return draw.Measure(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Constraints{Max: image.Pt(draw.Unbounded, draw.Unbounded)}
		return atoms.Code(s).Layout(gtx, th)
	}).Size
}

// CodeLineHeight returns the height of a CodeLine in pixels, for callers
// that need to compute scroll positions.
func CodeLineHeight(gtx layout.Context, th *tokens.Theme) int {
	return lineHeight(gtx, th) + 2*gtx.Dp(1)
}
