package git

import (
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
)

// tabWidth is the number of spaces a tab expands to in diff rows.
const tabWidth = 4

// maxWordDiffLen bounds the line length for word highlighting.
const maxWordDiffLen = 2000

// side is one half of a side-by-side row (or a unified row's content).
type side struct {
	no      int
	text    string
	tone    molecules.LineTone
	present bool
	spans   []molecules.Span
}

// diffRow is one row of the unified or the side-by-side rendering.
type diffRow struct {
	header bool
	hunk   int    // hunk index (header rows and lines)
	text   string // header text
	// Unified: left holds the old number, right the new number and both
	// the same text; marker is "+", "-" or " ".
	left, right side
	marker      string
	// pair links a removed line to its added counterpart for word
	// highlighting (unified: row index; side-by-side: same row, -2).
	pair      int
	spansDone bool
}

// diffModel holds both renderings of a FileDiff.
type diffModel struct {
	unified, split []diffRow
	// Change block starts (row indexes) per rendering.
	changesU, changesS []int
	// First row of each hunk per rendering.
	hunksU, hunksS []int
	digits         int
	added, deleted int
}

// expandTabs replaces tabs by spaces (to the next tab stop).
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

func buildDiffModel(f *gitcli.FileDiff) *diffModel {
	m := &diffModel{}
	if f == nil {
		return m
	}
	m.added, m.deleted = f.Stats()
	maxNo := 0
	for hi := range f.Hunks {
		h := &f.Hunks[hi]
		maxNo = max(maxNo, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
		hdr := diffRow{header: true, hunk: hi, text: h.Header(), pair: -1}
		m.hunksU = append(m.hunksU, len(m.unified))
		m.hunksS = append(m.hunksS, len(m.split))
		m.unified = append(m.unified, hdr)
		m.split = append(m.split, hdr)

		lines := h.Lines
		inChangeU, inChangeS := false, false
		for i := 0; i < len(lines); {
			l := lines[i]
			if l.Kind == gitcli.LineContext {
				txt := expandTabs(l.Text)
				m.unified = append(m.unified, diffRow{hunk: hi, marker: " ", pair: -1,
					left:  side{no: l.OldLine, text: txt, present: true},
					right: side{no: l.NewLine, text: txt, present: true}})
				m.split = append(m.split, diffRow{hunk: hi, pair: -1,
					left:  side{no: l.OldLine, text: txt, present: true},
					right: side{no: l.NewLine, text: txt, present: true}})
				inChangeU, inChangeS = false, false
				i++
				continue
			}
			// A change run: deletions followed by additions.
			var del, add []gitcli.DiffLine
			for i < len(lines) && lines[i].Kind == gitcli.LineDeleted {
				del = append(del, lines[i])
				i++
			}
			for i < len(lines) && lines[i].Kind == gitcli.LineAdded {
				add = append(add, lines[i])
				i++
			}
			if !inChangeU {
				m.changesU = append(m.changesU, len(m.unified))
				inChangeU = true
			}
			if !inChangeS {
				m.changesS = append(m.changesS, len(m.split))
				inChangeS = true
			}
			firstDel := len(m.unified)
			for _, d := range del {
				t := expandTabs(d.Text)
				m.unified = append(m.unified, diffRow{hunk: hi, marker: "-", pair: -1,
					left:  side{no: d.OldLine, text: t, tone: molecules.ToneRemoved, present: true},
					right: side{text: t, tone: molecules.ToneRemoved, present: true}})
			}
			firstAdd := len(m.unified)
			for k, a := range add {
				t := expandTabs(a.Text)
				row := diffRow{hunk: hi, marker: "+", pair: -1,
					left:  side{text: t, tone: molecules.ToneAdded, present: true},
					right: side{no: a.NewLine, text: t, tone: molecules.ToneAdded, present: true}}
				if k < len(del) {
					row.pair = firstDel + k
					m.unified[firstDel+k].pair = firstAdd + k
				}
				m.unified = append(m.unified, row)
			}
			for k := 0; k < max(len(del), len(add)); k++ {
				row := diffRow{hunk: hi, pair: -1}
				if k < len(del) {
					row.left = side{no: del[k].OldLine, text: expandTabs(del[k].Text), tone: molecules.ToneRemoved, present: true}
				} else {
					row.left = side{tone: molecules.ToneFiller}
				}
				if k < len(add) {
					row.right = side{no: add[k].NewLine, text: expandTabs(add[k].Text), tone: molecules.ToneAdded, present: true}
				} else {
					row.right = side{tone: molecules.ToneFiller}
				}
				if k < len(del) && k < len(add) {
					row.pair = -2
				}
				m.split = append(m.split, row)
			}
		}
	}
	m.digits = max(3, len(strconv.Itoa(maxNo)))
	return m
}

// wordSpans computes the changed byte ranges of a (removed, added) line
// pair. It returns nil spans when the lines are too long or differ almost
// completely (then whole-line coloring says enough).
func wordSpans(a, b string) (sa, sb []molecules.Span) {
	if a == b || len(a) > maxWordDiffLen || len(b) > maxWordDiffLen || a == "" || b == "" {
		return nil, nil
	}
	ta, tb := tokenize(a), tokenize(b)
	ch := gitcli.DiffLines(ta, tb)
	changed := 0
	for _, c := range ch {
		changed += max(c.OldEnd-c.OldStart, c.NewEnd-c.NewStart)
	}
	if changed*10 > max(len(ta), len(tb))*6 {
		return nil, nil
	}
	oa, ob := offsets(ta), offsets(tb)
	for _, c := range ch {
		if c.OldEnd > c.OldStart && !blank(ta[c.OldStart:c.OldEnd]) {
			sa = append(sa, molecules.Span{Start: oa[c.OldStart], End: oa[c.OldEnd]})
		}
		if c.NewEnd > c.NewStart && !blank(tb[c.NewStart:c.NewEnd]) {
			sb = append(sb, molecules.Span{Start: ob[c.NewStart], End: ob[c.NewEnd]})
		}
	}
	return sa, sb
}

func blank(toks []string) bool {
	for _, t := range toks {
		if strings.TrimSpace(t) != "" {
			return false
		}
	}
	return true
}

// offsets returns the byte offset of every token plus the total length.
func offsets(toks []string) []int {
	out := make([]int, len(toks)+1)
	for i, t := range toks {
		out[i+1] = out[i] + len(t)
	}
	return out
}

// tokenize splits s into words (letters, digits, '_', '$', non-ASCII),
// whitespace runs and single punctuation bytes.
func tokenize(s string) []string {
	var toks []string
	cls := func(c byte) int {
		switch {
		case c == ' ' || c == '\t':
			return 1
		case c == '_' || c == '$' || c >= 0x80 ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			return 2
		}
		return 3
	}
	for i := 0; i < len(s); {
		k := cls(s[i])
		j := i + 1
		if k != 3 {
			for j < len(s) && cls(s[j]) == k {
				j++
			}
		}
		toks = append(toks, s[i:j])
		i = j
	}
	return toks
}

// WholeFileDiff builds a FileDiff with a single hunk that covers the
// complete old and new content (every unchanged line as context), so a
// DiffView shows the entire file. Paths are informational.
func WholeFileDiff(oldPath, newPath string, oldText, newText []byte) *gitcli.FileDiff {
	a, b := gitcli.SplitLines(oldText), gitcli.SplitLines(newText)
	f := &gitcli.FileDiff{OldPath: oldPath, NewPath: newPath, IsNew: len(a) == 0 && len(b) > 0}
	if len(a) == 0 && len(b) == 0 {
		return f
	}
	h := gitcli.Hunk{OldStart: 1, OldLines: len(a), NewStart: 1, NewLines: len(b)}
	if len(a) == 0 {
		h.OldStart = 0
	}
	if len(b) == 0 {
		h.NewStart = 0
	}
	trim := func(s string) string { return strings.TrimRight(s, "\r\n") }
	ia, ib := 0, 0
	ctx := func(endA int) {
		for ia < endA {
			h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineContext, Text: trim(a[ia]), OldLine: ia + 1, NewLine: ib + 1})
			ia++
			ib++
		}
	}
	for _, c := range gitcli.DiffLines(a, b) {
		ctx(c.OldStart)
		for ; ia < c.OldEnd; ia++ {
			h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineDeleted, Text: trim(a[ia]), OldLine: ia + 1})
		}
		for ; ib < c.NewEnd; ib++ {
			h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineAdded, Text: trim(b[ib]), NewLine: ib + 1})
		}
	}
	ctx(len(a))
	f.Hunks = []gitcli.Hunk{h}
	return f
}
