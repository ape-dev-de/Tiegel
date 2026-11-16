package gitcli

import "strings"

// MergeBlockKind classifies a block of a three-way merge.
type MergeBlockKind int

// Merge block kinds.
const (
	MergeUnchanged  MergeBlockKind = iota // all three versions equal
	MergeOursOnly                         // only ours changed: take ours
	MergeTheirsOnly                       // only theirs changed: take theirs
	MergeBothSame                         // both changed identically
	MergeConflict                         // both changed differently
)

// String returns the kind name.
func (k MergeBlockKind) String() string {
	switch k {
	case MergeUnchanged:
		return "unchanged"
	case MergeOursOnly:
		return "ours"
	case MergeTheirsOnly:
		return "theirs"
	case MergeBothSame:
		return "both"
	case MergeConflict:
		return "conflict"
	}
	return "unknown"
}

// Resolution is how a conflict block is resolved when rendering.
type Resolution int

// Conflict resolutions.
const (
	Unresolved      Resolution = iota // write conflict markers
	TakeOurs                          // ours lines
	TakeTheirs                        // theirs lines
	TakeOursFirst                     // ours, then theirs
	TakeTheirsFirst                   // theirs, then ours
	TakeBase                          // base lines
	TakeCustom                        // MergeBlock.Custom
)

// MergeBlock is one region of a three-way merge. Lines keep their
// terminators (see [SplitLines]). Start fields are 0-based line indexes of
// the block in the respective version.
type MergeBlock struct {
	Kind                              MergeBlockKind
	Base, Ours, Theirs                []string
	BaseStart, OursStart, TheirsStart int
	Resolution                        Resolution
	Custom                            []byte
}

// Merged returns the lines this block contributes to the result. ok is
// false for an unresolved conflict.
func (b *MergeBlock) Merged() (lines []string, ok bool) {
	switch b.Kind {
	case MergeUnchanged:
		return b.Base, true
	case MergeOursOnly, MergeBothSame:
		return b.Ours, true
	case MergeTheirsOnly:
		return b.Theirs, true
	}
	switch b.Resolution {
	case TakeOurs:
		return b.Ours, true
	case TakeTheirs:
		return b.Theirs, true
	case TakeOursFirst:
		return joinBlocks(b.Ours, b.Theirs), true
	case TakeTheirsFirst:
		return joinBlocks(b.Theirs, b.Ours), true
	case TakeBase:
		return b.Base, true
	case TakeCustom:
		return SplitLines(b.Custom), true
	}
	return nil, false
}

// joinBlocks concatenates two line lists, adding a newline between them if
// the first one ends without.
func joinBlocks(a, b []string) []string {
	out := append([]string(nil), a...)
	if n := len(out); n > 0 && len(b) > 0 && !strings.HasSuffix(out[n-1], "\n") {
		out[n-1] += "\n"
	}
	return append(out, b...)
}

// MergeResult is the outcome of [Merge3].
type MergeResult struct {
	Blocks []MergeBlock
}

// Conflicts returns the number of conflict blocks.
func (m *MergeResult) Conflicts() int {
	n := 0
	for i := range m.Blocks {
		if m.Blocks[i].Kind == MergeConflict {
			n++
		}
	}
	return n
}

// Unresolved returns the number of conflict blocks without resolution.
func (m *MergeResult) Unresolved() int {
	n := 0
	for i := range m.Blocks {
		if _, ok := m.Blocks[i].Merged(); !ok {
			n++
		}
	}
	return n
}

// MarkerLabels are the names written after conflict markers.
type MarkerLabels struct {
	Ours, Base, Theirs string
}

// Render produces the merged text. Unresolved conflicts are written with
// diff3 style markers (<<<<<<<, |||||||, =======, >>>>>>>).
func (m *MergeResult) Render(labels MarkerLabels) []byte {
	if labels.Ours == "" {
		labels.Ours = "ours"
	}
	if labels.Base == "" {
		labels.Base = "base"
	}
	if labels.Theirs == "" {
		labels.Theirs = "theirs"
	}
	var b strings.Builder
	writeLines := func(lines []string) {
		for _, l := range lines {
			b.WriteString(l)
		}
	}
	marker := func(m, label string) {
		s := b.String()
		if len(s) > 0 && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(m)
		if label != "" {
			b.WriteString(" " + label)
		}
		b.WriteByte('\n')
	}
	for i := range m.Blocks {
		blk := &m.Blocks[i]
		if lines, ok := blk.Merged(); ok {
			writeLines(lines)
			continue
		}
		marker("<<<<<<<", labels.Ours)
		writeLines(blk.Ours)
		marker("|||||||", labels.Base)
		writeLines(blk.Base)
		marker("=======", "")
		writeLines(blk.Theirs)
		marker(">>>>>>>", labels.Theirs)
	}
	return []byte(b.String())
}

// Merge3 performs a line based three-way merge (diff3 style): the changes
// from base to ours and from base to theirs are computed with
// [DiffLines]; overlapping or adjacent changes form one block which is a
// conflict unless both sides made the same change.
func Merge3(base, ours, theirs []byte) *MergeResult {
	bl, ol, tl := SplitLines(base), SplitLines(ours), SplitLines(theirs)
	co := DiffLines(bl, ol)
	ct := DiffLines(bl, tl)
	res := &MergeResult{}
	pos := 0 // next base line not yet emitted
	// offsets: side index = base index + offset outside of changes
	offO, offT := 0, 0
	i, j := 0, 0
	for i < len(co) || j < len(ct) {
		// Start a region with the change that begins first in base.
		var start, end int
		if j >= len(ct) || (i < len(co) && co[i].OldStart <= ct[j].OldStart) {
			start, end = co[i].OldStart, co[i].OldEnd
		} else {
			start, end = ct[j].OldStart, ct[j].OldEnd
		}
		i0, j0 := i, j
		// Absorb all changes that overlap or touch the region (changes are
		// sorted, so only the end can grow).
		for {
			grew := false
			if i < len(co) && co[i].OldStart <= end {
				if co[i].OldEnd > end {
					end = co[i].OldEnd
				}
				i++
				grew = true
			}
			if j < len(ct) && ct[j].OldStart <= end {
				if ct[j].OldEnd > end {
					end = ct[j].OldEnd
				}
				j++
				grew = true
			}
			if !grew {
				break
			}
		}
		if pos < start {
			res.Blocks = append(res.Blocks, MergeBlock{
				Kind: MergeUnchanged, Base: bl[pos:start],
				BaseStart: pos, OursStart: pos + offO, TheirsStart: pos + offT,
			})
		}
		oS, oE, newOffO := sideRange(co[i0:i], start, end, offO)
		tS, tE, newOffT := sideRange(ct[j0:j], start, end, offT)
		blk := MergeBlock{
			Base: bl[start:end], Ours: ol[oS:oE], Theirs: tl[tS:tE],
			BaseStart: start, OursStart: oS, TheirsStart: tS,
		}
		switch {
		case i == i0:
			blk.Kind = MergeTheirsOnly
		case j == j0:
			blk.Kind = MergeOursOnly
		case equalLines(blk.Ours, blk.Theirs):
			blk.Kind = MergeBothSame
		default:
			blk.Kind = MergeConflict
		}
		res.Blocks = append(res.Blocks, blk)
		offO, offT = newOffO, newOffT
		pos = end
	}
	if pos < len(bl) {
		res.Blocks = append(res.Blocks, MergeBlock{
			Kind: MergeUnchanged, Base: bl[pos:],
			BaseStart: pos, OursStart: pos + offO, TheirsStart: pos + offT,
		})
	}
	return res
}

// sideRange maps the base region [start, end) to the side's line range
// given the side's changes inside the region and the offset before it.
// It returns the range and the offset after the region.
func sideRange(changes []Change, start, end, off int) (s, e, newOff int) {
	if len(changes) == 0 {
		return start + off, end + off, off
	}
	first, last := changes[0], changes[len(changes)-1]
	s = first.NewStart - (first.OldStart - start)
	e = last.NewEnd + (end - last.OldEnd)
	return s, e, e - end
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
