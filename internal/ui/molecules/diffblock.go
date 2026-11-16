package molecules

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DiffKind classifies a diff line.
type DiffKind int

// Diff line kinds.
const (
	DiffContext DiffKind = iota
	DiffAdded
	DiffRemoved
)

// Marker returns the unified-diff prefix of the kind.
func (k DiffKind) Marker() string {
	switch k {
	case DiffAdded:
		return "+"
	case DiffRemoved:
		return "-"
	}
	return " "
}

// DiffLine is one line of a hunk. OldNo/NewNo are 1-based line numbers;
// zero means "not present on that side" (added lines have no OldNo, removed
// lines no NewNo).
type DiffLine struct {
	Kind  DiffKind
	OldNo int
	NewNo int
	Text  string
}

// DiffBlock renders one hunk: an optional header ("@@ -1,4 +1,5 @@ func")
// followed by lines with old/new line numbers, +/- markers and colored
// backgrounds. With Selectable set, the header carries a checkbox to
// include/exclude the hunk (commit dialog).
//
// DiffBlock lays out all its lines; it is meant for hunks, not whole files.
// Large diffs belong in a virtualized list of blocks.
type DiffBlock struct {
	Header     string
	Lines      []DiffLine
	Selectable bool
	// Include is the hunk checkbox, used when Selectable is set.
	Include atoms.Checkbox
}

// Update processes input and reports whether the Include checkbox changed.
func (b *DiffBlock) Update(gtx layout.Context) bool {
	if !b.Selectable {
		return false
	}
	return b.Include.Update(gtx)
}

// Layout draws the block across the available width.
func (b *DiffBlock) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.Update(gtx)
	w := draw.Width(gtx, gtx.Dp(480))
	gtx.Constraints.Min.X = w
	gtx.Constraints.Max.X = w

	digits := 1
	for _, l := range b.Lines {
		digits = max(digits, len(strconv.Itoa(l.OldNo)), len(strconv.Itoa(l.NewNo)))
	}
	numW := draw.Measure(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return atoms.Code(strings.Repeat("0", digits)).Layout(gtx, th)
	}).Size.X

	children := make([]layout.FlexChild, 0, len(b.Lines)+1)
	if b.Header != "" || b.Selectable {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return b.layoutHeader(gtx, th)
		}))
	}
	for i := range b.Lines {
		l := b.Lines[i]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutDiffLine(gtx, th, l, numW)
		}))
	}
	d := layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	draw.Border(gtx.Ops, image.Rectangle{Max: d.Size}, 0, gtx.Dp(th.Size.BorderWidth), th.Color.Border)
	return d
}

func (b *DiffBlock) layoutHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	h := gtx.Dp(th.Size.RowH)
	w := gtx.Constraints.Max.X
	padX := gtx.Dp(th.Space.S)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, h), th.Editor.DiffHeaderBg)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*padX), h)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !b.Selectable {
				return layout.Dimensions{}
			}
			return layout.Inset{Right: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return b.Include.Layout(gtx, th)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return atoms.Label{Text: b.Header, Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Layout(gtx, th)
		}),
	)
	call := m.Stop()
	t := op.Offset(image.Pt(padX, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func layoutDiffLine(gtx layout.Context, th *tokens.Theme, l DiffLine, numW int) layout.Dimensions {
	e := th.Editor
	var bg, marker color.NRGBA
	switch l.Kind {
	case DiffAdded:
		bg, marker = e.DiffAddedBg, e.DiffAddedFg
	case DiffRemoved:
		bg, marker = e.DiffRemovedBg, e.DiffRemovedFg
	default:
		bg, marker = e.Background, e.LineNumber
	}
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(th.Space.S)
	gap := gtx.Dp(th.Space.XS)
	num := func(n int) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(numW, gtx.Constraints.Max.Y))
			gtx.Constraints.Min.Y = 0
			s := ""
			if n > 0 {
				s = strconv.Itoa(n)
			}
			return atoms.Label{Text: s, Role: atoms.RoleCode, Color: e.LineNumber, Alignment: text.End}.Layout(gtx, th)
		}
	}
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-pad), draw.Unbounded)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{Alignment: layout.Baseline}.Layout(cgtx,
		layout.Rigid(num(l.OldNo)),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(num(l.NewNo)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx,
				atoms.Label{Text: l.Kind.Marker(), Role: atoms.RoleCode, Color: marker}.Fn(th))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return atoms.Label{Text: l.Text, Role: atoms.RoleCode, MaxLines: 1}.Layout(gtx, th)
		}),
	)
	call := m.Stop()
	h := d.Size.Y + 2*gtx.Dp(1)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, h), bg)
	// Gutter background behind the numbers.
	gutterW := pad + 2*numW + gtx.Dp(th.Space.S) + gap/2
	draw.Rect(gtx.Ops, image.Rect(0, 0, gutterW, h), tokens.Over(tokens.WithAlpha(e.Gutter, 0x80), bg))
	t := op.Offset(image.Pt(pad, gtx.Dp(1))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, h)}
}
