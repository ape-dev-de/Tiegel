package gitcli

// Change is one difference between two line sequences: old lines
// [OldStart, OldEnd) are replaced by new lines [NewStart, NewEnd). Indexes
// are 0-based. A pure insertion has OldStart == OldEnd, a pure deletion
// NewStart == NewEnd.
type Change struct {
	OldStart, OldEnd int
	NewStart, NewEnd int
}

// SplitLines splits text into lines. Every line keeps its terminator
// ("\n", so "\r\n" stays intact); only the last line may lack one. Empty
// input yields no lines.
func SplitLines(text []byte) []string {
	var lines []string
	start := 0
	for i, c := range text {
		if c == '\n' {
			lines = append(lines, string(text[start:i+1]))
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, string(text[start:]))
	}
	return lines
}

// DiffLines computes a shortest edit script between a and b with Myers'
// linear space O(ND) algorithm and returns the differing regions in order.
// Lines that occur only on one side are discarded before the search (as
// git's xdiff does), which keeps unrelated inputs fast; the result is then
// a valid but not always minimal diff.
func DiffLines(a, b []string) []Change {
	ids := make(map[string]int, len(a)+len(b))
	intern := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	return diffInts(intern(a), intern(b))
}

func diffInts(a, b []int) []Change {
	delA := make([]bool, len(a))
	insB := make([]bool, len(b))

	// Discard lines without any counterpart on the other side.
	inA := make(map[int]bool, len(a))
	for _, v := range a {
		inA[v] = true
	}
	inB := make(map[int]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}
	var fa, fb []int // filtered sequences
	var ma, mb []int // filtered index -> original index
	for i, v := range a {
		if inB[v] {
			fa = append(fa, v)
			ma = append(ma, i)
		} else {
			delA[i] = true
		}
	}
	for j, v := range b {
		if inA[v] {
			fb = append(fb, v)
			mb = append(mb, j)
		} else {
			insB[j] = true
		}
	}

	d := &myers{a: fa, b: fb, delA: make([]bool, len(fa)), insB: make([]bool, len(fb))}
	// Diagonals reach |delta|+maxD+1 <= 1.5*(N+M)+2 in magnitude.
	off := 2*(len(fa)+len(fb)) + 4
	d.vf = make([]int, 2*off+1)
	d.vb = make([]int, 2*off+1)
	d.off = off
	d.compare(0, len(fa), 0, len(fb))
	for i, del := range d.delA {
		if del {
			delA[ma[i]] = true
		}
	}
	for j, ins := range d.insB {
		if ins {
			insB[mb[j]] = true
		}
	}
	return collectChanges(delA, insB)
}

// collectChanges turns deletion/insertion flags into change regions.
func collectChanges(delA, insB []bool) []Change {
	var out []Change
	i, j := 0, 0
	for i < len(delA) || j < len(insB) {
		if i < len(delA) && j < len(insB) && !delA[i] && !insB[j] {
			i++
			j++
			continue
		}
		si, sj := i, j
		for i < len(delA) && delA[i] {
			i++
		}
		for j < len(insB) && insB[j] {
			j++
		}
		if si == i && sj == j {
			// Inconsistent flags (cannot happen with a valid script);
			// consume the rest as one change to guarantee termination.
			i, j = len(delA), len(insB)
		}
		out = append(out, Change{OldStart: si, OldEnd: i, NewStart: sj, NewEnd: j})
	}
	return out
}

type myers struct {
	a, b   []int
	delA   []bool
	insB   []bool
	vf, vb []int
	off    int
}

// compare marks the edit script for a[aLo:aHi] and b[bLo:bHi].
func (m *myers) compare(aLo, aHi, bLo, bHi int) {
	for {
		for aLo < aHi && bLo < bHi && m.a[aLo] == m.b[bLo] {
			aLo++
			bLo++
		}
		for aLo < aHi && bLo < bHi && m.a[aHi-1] == m.b[bHi-1] {
			aHi--
			bHi--
		}
		if aLo == aHi {
			for j := bLo; j < bHi; j++ {
				m.insB[j] = true
			}
			return
		}
		if bLo == bHi {
			for i := aLo; i < aHi; i++ {
				m.delA[i] = true
			}
			return
		}
		x, y, ok := m.middleSnake(aLo, aHi, bLo, bHi)
		if !ok {
			for i := aLo; i < aHi; i++ {
				m.delA[i] = true
			}
			for j := bLo; j < bHi; j++ {
				m.insB[j] = true
			}
			return
		}
		// Recurse into the smaller half, loop on the larger one.
		if (x-aLo)+(y-bLo) < (aHi-x)+(bHi-y) {
			m.compare(aLo, x, bLo, y)
			aLo, bLo = x, y
		} else {
			m.compare(x, aHi, y, bHi)
			aHi, bHi = x, y
		}
	}
}

// middleSnake finds a point (x, y) on an optimal edit path of the
// sub-problem that splits it into two strictly smaller ones. Both ranges
// are non-empty and have no common prefix or suffix.
func (m *myers) middleSnake(aLo, aHi, bLo, bHi int) (x0, y0 int, ok bool) {
	n, mm := aHi-aLo, bHi-bLo
	delta := n - mm
	odd := delta&1 != 0
	maxD := (n + mm + 1) / 2
	vf, vb, off := m.vf, m.vb, m.off
	vf[off+1] = 0
	vb[off+delta-1] = n
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && vf[off+k-1] < vf[off+k+1]) {
				x = vf[off+k+1]
			} else {
				x = vf[off+k-1] + 1
			}
			y := x - k
			for x < n && y < mm && m.a[aLo+x] == m.b[bLo+y] {
				x++
				y++
			}
			vf[off+k] = x
			if odd && k >= delta-(d-1) && k <= delta+(d-1) && x >= vb[off+k] {
				return aLo + x, bLo + y, true
			}
		}
		for k := -d; k <= d; k += 2 {
			kk := k + delta
			var x int
			if k == d || (k != -d && vb[off+kk-1] < vb[off+kk+1]) {
				x = vb[off+kk-1]
			} else {
				x = vb[off+kk+1] - 1
			}
			y := x - kk
			for x > 0 && y > 0 && m.a[aLo+x-1] == m.b[bLo+y-1] {
				x--
				y--
			}
			vb[off+kk] = x
			if !odd && kk >= -d && kk <= d && x <= vf[off+kk] {
				return aLo + x, bLo + y, true
			}
		}
	}
	// Unreachable for valid input.
	return 0, 0, false
}
