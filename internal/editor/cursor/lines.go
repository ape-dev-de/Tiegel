package cursor

import (
	"bytes"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// This file holds line-oriented editing commands: indenting, commenting,
// duplicating and moving lines. They work on every line touched by any
// selection and are applied as one undo step.

// prim is a primitive edit in document coordinates: replace del bytes at
// at with ins.
type prim struct {
	at, del int
	ins     string
}

// mapThrough maps off across the sorted, non-overlapping prims. Offsets
// inside a deleted range move to its start. For a pure insertion at off,
// stickLeft keeps off before the inserted text.
func mapThrough(off int, ps []prim, stickLeft bool) int {
	delta := 0
	for _, p := range ps {
		if p.at > off || (p.at == off && p.del == 0 && stickLeft) {
			break
		}
		switch {
		case p.at == off && p.del == 0:
			delta += len(p.ins)
		case off >= p.at+p.del:
			delta += len(p.ins) - p.del
		default:
			return p.at + delta
		}
	}
	return off + delta
}

// applyPrims applies ps (sorted by at, non-overlapping) as one undo step
// and installs sels (already in post-edit coordinates).
func (m *Model) applyPrims(kind buffer.GroupKind, ps []prim, sels []Selection, primary int) {
	if len(ps) == 0 {
		return
	}
	m.buf.BeginGroup(kind, m.State())
	m.editing = true
	for i := len(ps) - 1; i >= 0; i-- {
		p := ps[i]
		// Prims are built from clamped line offsets; a failing edit cannot
		// occur and would only leave the document unchanged.
		_, _ = m.buf.Replace(p.at, p.del, []byte(p.ins))
	}
	m.editing = false
	m.sels, m.primary = normalize(sels, primary, m.buf.Len())
	m.buf.EndGroup(m.State())
}

// touchedLines returns the sorted, de-duplicated document lines touched by
// any selection. A non-empty selection ending at column 0 does not touch
// that line.
func (m *Model) touchedLines(s buffer.Snapshot) []int {
	var lines []int
	for _, sel := range m.sels {
		first, last := selLines(s, sel)
		for l := first; l <= last; l++ {
			lines = append(lines, l)
		}
	}
	slices.Sort(lines)
	return slices.Compact(lines)
}

// selLines returns the first and last line touched by sel.
func selLines(s buffer.Snapshot, sel Selection) (int, int) {
	first := s.OffsetToPos(sel.Start()).Line
	endPos := s.OffsetToPos(sel.End())
	last := endPos.Line
	if !sel.Empty() && endPos.Col == 0 && last > first {
		last--
	}
	return first, last
}

// mapSels maps every selection through ps. Non-empty selections keep
// their start before text inserted exactly at it (so an indented or
// commented block stays fully selected); carets move with inserted text.
func (m *Model) mapSels(ps []prim) []Selection {
	out := make([]Selection, len(m.sels))
	for i, sel := range m.sels {
		start := mapThrough(sel.Start(), ps, !sel.Empty())
		end := mapThrough(sel.End(), ps, false)
		if sel.Head < sel.Anchor {
			out[i] = Range(end, start)
		} else {
			out[i] = Range(start, end)
		}
	}
	return out
}

func leadingWS(line []byte) int {
	return len(line) - len(bytes.TrimLeft(line, " \t"))
}

func isBlank(line []byte) bool { return leadingWS(line) == len(line) }

// IndentLines inserts unit (e.g. "\t" or four spaces) at the start of
// every line touched by a selection. Blank lines are left alone unless
// they are the only line. It returns false if nothing changed.
func (m *Model) IndentLines(unit string) bool {
	if unit == "" {
		return false
	}
	s := m.buf.Snapshot()
	lines := m.touchedLines(s)
	var ps []prim
	for _, l := range lines {
		if len(lines) > 1 && isBlank(s.Line(l)) {
			continue
		}
		ps = append(ps, prim{at: s.LineStart(l), ins: unit})
	}
	if len(ps) == 0 {
		return false
	}
	m.applyPrims(buffer.GroupOther, ps, m.mapSels(ps), m.primary)
	return true
}

// OutdentLines removes one indentation level – a tab or up to tabWidth
// spaces – from the start of every touched line. It returns false if no
// line was indented.
func (m *Model) OutdentLines() bool {
	s := m.buf.Snapshot()
	tw := m.tabWidth()
	var ps []prim
	for _, l := range m.touchedLines(s) {
		line := s.Line(l)
		n := 0
		if len(line) > 0 && line[0] == '\t' {
			n = 1
		} else {
			for n < len(line) && n < tw && line[n] == ' ' {
				n++
			}
			if n < len(line) && n < tw && line[n] == '\t' {
				n++
			}
		}
		if n > 0 {
			ps = append(ps, prim{at: s.LineStart(l), del: n})
		}
	}
	if len(ps) == 0 {
		return false
	}
	m.applyPrims(buffer.GroupOther, ps, m.mapSels(ps), m.primary)
	return true
}

// ToggleComment comments or uncomments the touched lines with the line
// comment prefix (e.g. "//" for PHP, "#" for YAML). If every non-blank
// line already starts with prefix (after indentation), the prefix and one
// following space are removed; otherwise "prefix " is inserted at the
// smallest indentation of the block. Blank lines are skipped. It returns
// false if nothing changed or prefix is empty.
func (m *Model) ToggleComment(prefix string) bool {
	if prefix == "" {
		return false
	}
	s := m.buf.Snapshot()
	lines := m.touchedLines(s)
	type info struct {
		line   int
		start  int
		indent int
		text   []byte
	}
	var nonBlank []info
	allCommented := true
	minIndent := -1
	for _, l := range lines {
		text := s.Line(l)
		if isBlank(text) {
			continue
		}
		ind := leadingWS(text)
		nonBlank = append(nonBlank, info{l, s.LineStart(l), ind, text})
		if !bytes.HasPrefix(text[ind:], []byte(prefix)) {
			allCommented = false
		}
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
	}
	if len(nonBlank) == 0 {
		// A single blank line gets a comment marker at the caret line.
		if len(lines) != 1 {
			return false
		}
		allCommented = false
		nonBlank = []info{{lines[0], s.LineStart(lines[0]), 0, nil}}
		minIndent = len(s.Line(lines[0]))
	}
	var ps []prim
	for _, in := range nonBlank {
		if allCommented {
			n := len(prefix)
			if rest := in.text[in.indent+n:]; len(rest) > 0 && rest[0] == ' ' {
				n++
			}
			ps = append(ps, prim{at: in.start + in.indent, del: n})
		} else {
			ps = append(ps, prim{at: in.start + minIndent, ins: prefix + " "})
		}
	}
	m.applyPrims(buffer.GroupOther, ps, m.mapSels(ps), m.primary)
	return true
}

// DuplicateLines duplicates the line of every caret below itself (the
// caret moves to the copy) and the text of every non-empty selection
// right after it (the copy becomes selected).
func (m *Model) DuplicateLines() {
	s := m.buf.Snapshot()
	eol := m.buf.EOL().String()
	type dup struct {
		p    prim
		sels []int
	}
	var dups []dup
	byLine := map[int]int{} // last line → index in dups for caret blocks
	for i, sel := range m.sels {
		if sel.Empty() {
			first, last := selLines(s, sel)
			if j, ok := byLine[last]; ok {
				dups[j].sels = append(dups[j].sels, i)
				continue
			}
			text := string(s.Slice(s.LineStart(first), s.LineEnd(last)))
			byLine[last] = len(dups)
			dups = append(dups, dup{prim{at: s.LineEnd(last), ins: eol + text}, []int{i}})
			continue
		}
		dups = append(dups, dup{prim{at: sel.End(), ins: string(s.Slice(sel.Start(), sel.End()))}, []int{i}})
	}
	slices.SortStableFunc(dups, func(a, b dup) int { return a.p.at - b.p.at })
	ps := make([]prim, 0, len(dups))
	for _, d := range dups {
		// Two insertions at the same offset are merged into one.
		if n := len(ps); n > 0 && ps[n-1].at == d.p.at {
			ps[n-1].ins += d.p.ins
			continue
		}
		ps = append(ps, d.p)
	}
	sels := make([]Selection, len(m.sels))
	for _, d := range dups {
		for _, i := range d.sels {
			sel := m.sels[i]
			if sel.Empty() {
				h := mapThrough(sel.Head, ps, true) + len(d.p.ins)
				sels[i] = Caret(h)
				continue
			}
			end := mapThrough(sel.End(), ps, true)
			sels[i] = Range(end, end+len(d.p.ins))
		}
	}
	m.applyPrims(buffer.GroupOther, ps, sels, m.primary)
}

// MoveLines moves the lines touched by the selections one line up
// (delta < 0) or down (delta > 0), swapping them with the neighbouring
// line. It returns false if a block is already at the document edge.
func (m *Model) MoveLines(delta int) bool {
	if delta == 0 {
		return false
	}
	s := m.buf.Snapshot()
	eol := m.buf.EOL().String()
	// Merge touched line ranges into contiguous blocks.
	type block struct{ first, last int }
	var blocks []block
	for _, l := range m.touchedLines(s) {
		if n := len(blocks); n > 0 && blocks[n-1].last+1 == l {
			blocks[n-1].last = l
			continue
		}
		blocks = append(blocks, block{l, l})
	}
	if len(blocks) == 0 {
		return false
	}
	if delta < 0 && blocks[0].first == 0 {
		return false
	}
	if delta > 0 && blocks[len(blocks)-1].last >= s.LineCount()-1 {
		return false
	}
	var ps []prim
	shift := make([]int, len(m.sels))
	for _, b := range blocks {
		first, last := b.first, b.last
		var neighbor int
		if delta < 0 {
			neighbor = first - 1
		} else {
			neighbor = last + 1
		}
		lo, hi := min(first, neighbor), max(last, neighbor)
		lines := make([]string, 0, hi-lo+1)
		for l := lo; l <= hi; l++ {
			lines = append(lines, string(s.Line(l)))
		}
		var moved []string
		nb := len(s.Line(neighbor)) + len(eol)
		d := 0
		if delta < 0 {
			moved = append(append(moved, lines[1:]...), lines[0])
			d = -nb
		} else {
			moved = append([]string{lines[len(lines)-1]}, lines[:len(lines)-1]...)
			d = nb
		}
		start, end := s.LineStart(lo), s.LineEnd(hi)
		ps = append(ps, prim{at: start, del: end - start, ins: strings.Join(moved, eol)})
		for i, sel := range m.sels {
			if f, _ := selLines(s, sel); f >= first && f <= last {
				shift[i] = d
			}
		}
	}
	sels := make([]Selection, len(m.sels))
	for i, sel := range m.sels {
		sels[i] = Range(sel.Anchor+shift[i], sel.Head+shift[i])
		sels[i].Goal, sels[i].HasGoal = sel.Goal, sel.HasGoal
	}
	m.applyPrims(buffer.GroupOther, ps, sels, m.primary)
	return true
}

// PasteLines inserts text (whole lines, e.g. copied without a selection)
// at the start of each caret's line; carets keep their position in their
// original line. A missing trailing line ending is added.
func (m *Model) PasteLines(text string) {
	eol := m.buf.EOL().String()
	text = string(buffer.NormalizeEOL([]byte(text), m.buf.EOL()))
	if !strings.HasSuffix(text, eol) {
		text += eol
	}
	s := m.buf.Snapshot()
	var ps []prim
	for _, sel := range m.sels {
		at := s.LineStart(s.OffsetToPos(sel.Start()).Line)
		if n := len(ps); n > 0 && ps[n-1].at == at {
			continue
		}
		ps = append(ps, prim{at: at, ins: text})
	}
	sels := make([]Selection, len(m.sels))
	for i, sel := range m.sels {
		sels[i] = Range(mapThrough(sel.Anchor, ps, false), mapThrough(sel.Head, ps, false))
	}
	m.applyPrims(buffer.GroupOther, ps, sels, m.primary)
}

// SelectAllOccurrences selects every occurrence of the primary
// selection's text (or the word at the caret). It returns the number of
// selections.
func (m *Model) SelectAllOccurrences() int {
	if !m.AddNextOccurrence() {
		return len(m.sels)
	}
	const limit = 10000
	for len(m.sels) < limit && m.AddNextOccurrence() {
	}
	return len(m.sels)
}
