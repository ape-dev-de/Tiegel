package diff

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// uniLine holds the 1-based old and new line numbers of a unified row
// (0 = the line does not exist on that side).
type uniLine struct{ old, new int }

// unifiedModel is the unified rendering: one text with removed lines
// before added lines, plus the line numbers of both sides.
type unifiedModel struct {
	text   string
	lines  []uniLine
	decos  []editorview.LineDecoration
	starts []int // first row of every block
	digits int
}

func buildUnified(left, right string, blocks []Block) *unifiedModel {
	a, b := gitcli.SplitLines([]byte(left)), gitcli.SplitLines([]byte(right))
	m := &unifiedModel{digits: max(3, len(strconv.Itoa(max(len(a), len(b)))))}
	var sb strings.Builder
	add := func(s string, old, new int) {
		sb.WriteString(strings.TrimRight(s, "\r\n"))
		sb.WriteByte('\n')
		m.lines = append(m.lines, uniLine{old: old, new: new})
	}
	li, ri := 0, 0
	same := func(to int) {
		for ; ri < to && ri < len(b); ri++ {
			add(b[ri], li+1, ri+1)
			li++
		}
	}
	for _, bl := range blocks {
		same(bl.RightStart)
		li, ri = bl.LeftStart, bl.RightStart
		row := len(m.lines)
		m.starts = append(m.starts, row)
		if bl.LeftEnd > bl.LeftStart {
			d := editorview.LineDecoration{StartLine: row, EndLine: row + bl.LeftEnd - bl.LeftStart, Kind: editorview.DecorationDeleted}
			for _, in := range bl.LeftInline {
				d.Inline = append(d.Inline, editorview.InlineRange{Line: row + in.Line - bl.LeftStart, Start: in.Start, End: in.End})
			}
			m.decos = append(m.decos, d)
			for ; li < bl.LeftEnd && li < len(a); li++ {
				add(a[li], li+1, 0)
			}
		}
		row = len(m.lines)
		if bl.RightEnd > bl.RightStart {
			d := editorview.LineDecoration{StartLine: row, EndLine: row + bl.RightEnd - bl.RightStart, Kind: editorview.DecorationAdded}
			for _, in := range bl.RightInline {
				d.Inline = append(d.Inline, editorview.InlineRange{Line: row + in.Line - bl.RightStart, Start: in.Start, End: in.End})
			}
			m.decos = append(m.decos, d)
			for ; ri < bl.RightEnd && ri < len(b); ri++ {
				add(b[ri], 0, ri+1)
			}
		}
		li, ri = bl.LeftEnd, bl.RightEnd
	}
	same(len(b))
	m.text = strings.TrimSuffix(sb.String(), "\n")
	return m
}

// GutterWidth implements editorview.GutterColumn: two number columns.
func (m *unifiedModel) GutterWidth(gtx layout.Context, th *tokens.Theme) int {
	return 2*m.numW(gtx, th) + gtx.Dp(th.Space.S)
}

func (m *unifiedModel) numW(gtx layout.Context, th *tokens.Theme) int {
	// Go Mono advances 0.6 em.
	return gtx.Sp(th.Text.Code)*6*m.digits/10 + gtx.Dp(th.Space.S)
}

// LayoutGutterLine implements editorview.GutterColumn.
func (m *unifiedModel) LayoutGutterLine(gtx layout.Context, th *tokens.Theme, line int, active bool) {
	if line < 0 || line >= len(m.lines) {
		return
	}
	l := m.lines[line]
	w := m.numW(gtx, th)
	c := th.Editor.LineNumber
	if active {
		c = th.Editor.LineNumberActive
	}
	cell := func(n int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
			if n > 0 {
				atoms.Label{Text: strconv.Itoa(n), Role: atoms.RoleCode, Color: c, MaxLines: 1, Alignment: text.End}.Layout(gtx, th)
			}
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	}
	layout.Flex{}.Layout(gtx, cell(l.old), cell(l.new))
}
