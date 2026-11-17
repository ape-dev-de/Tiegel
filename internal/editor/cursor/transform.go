package cursor

import (
	"bytes"
	"slices"
	"strings"
	"unicode"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// JoinLines joins lines like the reference IDE's Join Lines: a caret or a
// selection within one line joins its line with the next one; a selection
// over several lines joins all of them. The line break, trailing
// whitespace of the upper line and leading whitespace of the lower line
// become a single space (none next to an empty side, after an opening or
// before a closing bracket). Carets move to the (first) join point. It
// returns false if nothing could be joined (last line).
func (m *Model) JoinLines() bool {
	s := m.buf.Snapshot()
	n := s.LineCount()
	joins := map[int]bool{} // upper line of every join
	firstJoin := make([]int, len(m.sels))
	for i, sel := range m.sels {
		first, last := selLines(s, sel)
		if last == first {
			last++
		}
		firstJoin[i] = -1
		for l := first; l < last && l+1 < n; l++ {
			joins[l] = true
			if firstJoin[i] < 0 {
				firstJoin[i] = l
			}
		}
	}
	if len(joins) == 0 {
		return false
	}
	lines := make([]int, 0, len(joins))
	for l := range joins {
		lines = append(lines, l)
	}
	slices.Sort(lines)
	ps := make([]prim, 0, len(lines))
	joinAt := map[int]int{} // upper line → index in ps
	for _, l := range lines {
		upper, lower := s.Line(l), s.Line(l+1)
		keep := len(bytes.TrimRight(upper, " \t"))
		ws := leadingWS(lower)
		at := s.LineStart(l) + keep
		del := s.LineStart(l+1) + ws - at
		sep := " "
		rest := lower[ws:]
		if keep == 0 || len(rest) == 0 || bytes.IndexAny(rest[:1], ")]}") == 0 || bytes.IndexAny(upper[keep-1:keep], "([{") == 0 {
			sep = ""
		}
		joinAt[l] = len(ps)
		ps = append(ps, prim{at: at, del: del, ins: sep})
	}
	sels := m.mapSels(ps)
	for i, sel := range m.sels {
		if !sel.Empty() || firstJoin[i] < 0 {
			continue
		}
		p := ps[joinAt[firstJoin[i]]]
		sels[i] = Caret(mapThrough(p.at, ps, true) + len(p.ins))
	}
	m.applyPrims(buffer.GroupOther, ps, sels, m.primary)
	return true
}

// ToggleCase upper-cases every selection that contains a lower-case
// letter and lower-cases the others. An empty selection works on the word
// at the caret (the caret stays). It returns false if nothing changed.
func (m *Model) ToggleCase() bool {
	s := m.buf.Snapshot()
	changed := false
	edits := make([]rangeEdit, 0, len(m.sels))
	seen := map[[2]int]bool{}
	for i, sel := range m.sels {
		start, end := sel.Start(), sel.End()
		if sel.Empty() {
			start, end = WordAt(s, sel.Head)
		}
		if seen[[2]int{start, end}] {
			continue // two carets in one word
		}
		seen[[2]int{start, end}] = true
		text := string(s.Slice(start, end))
		out := ToggledCase(text)
		changed = changed || out != text
		e := rangeEdit{start: start, end: end, text: out, primary: i == m.primary}
		switch {
		case sel.Empty():
			off := min(sel.Head-start, len(out))
			e.anchor, e.head = off, off
		case sel.Head < sel.Anchor:
			e.anchor, e.head = len(out), 0
		default:
			e.anchor, e.head = 0, len(out)
		}
		edits = append(edits, e)
	}
	if !changed {
		return false
	}
	m.applyEdits(buffer.GroupOther, edits)
	return true
}

// ToggledCase returns text upper-cased if it contains a lower-case
// letter, else lower-cased.
func ToggledCase(text string) string {
	if strings.IndexFunc(text, unicode.IsLower) >= 0 {
		return strings.ToUpper(text)
	}
	return strings.ToLower(text)
}
