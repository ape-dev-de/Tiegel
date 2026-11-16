package gitcli

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// BlockKind classifies a change block of [CompareTexts].
type BlockKind int

// Change block kinds.
const (
	BlockAdded    BlockKind = iota // lines exist only on the right side
	BlockDeleted                   // lines exist only on the left side
	BlockModified                  // lines differ on both sides
)

// String returns "added", "deleted" or "modified".
func (k BlockKind) String() string {
	switch k {
	case BlockAdded:
		return "added"
	case BlockDeleted:
		return "deleted"
	case BlockModified:
		return "modified"
	}
	return "unknown"
}

// ByteRange is a byte range [Start, End) within one line (without its
// terminator).
type ByteRange struct {
	Start, End int
}

// InlineRanges are the changed parts of one line of a modified block.
type InlineRanges struct {
	// Line is the 0-based line of the side the ranges belong to.
	Line   int
	Ranges []ByteRange
}

// DiffBlock is one change between the left and the right text. Lines are
// 0-based, ends exclusive. An added block has LeftStart == LeftEnd (the
// left line before which the right lines were inserted), a deleted block
// RightStart == RightEnd.
type DiffBlock struct {
	Kind                 BlockKind
	LeftStart, LeftEnd   int
	RightStart, RightEnd int
	// LeftInline and RightInline are the changed parts of the lines of a
	// modified block (only lines with changes, ascending).
	LeftInline, RightInline []InlineRanges
}

// TextDiffOptions configure [CompareTexts].
type TextDiffOptions struct {
	// IgnoreWhitespace compares lines with all white space removed (like
	// git diff -w). Lines that differ only in white space are not
	// reported.
	IgnoreWhitespace bool
	// Inline computes word and character ranges inside the lines of
	// modified blocks.
	Inline bool
}

// Limits of the inline diff: longer lines or larger blocks are shown with
// whole-line colors only.
const (
	maxInlineLineLen = 2000
	maxInlineBlock   = 500
)

// CompareTexts compares two texts line by line (see [DiffLines]) and
// returns the change blocks in order. Line numbers refer to
// [SplitLines] of the respective text. Lines are compared including their
// terminators unless IgnoreWhitespace is set.
func CompareTexts(left, right []byte, opts TextDiffOptions) []DiffBlock {
	a, b := SplitLines(left), SplitLines(right)
	ka, kb := a, b
	if opts.IgnoreWhitespace {
		ka, kb = stripSpaceAll(a), stripSpaceAll(b)
	}
	changes := DiffLines(ka, kb)
	out := make([]DiffBlock, 0, len(changes))
	for _, c := range changes {
		blk := DiffBlock{LeftStart: c.OldStart, LeftEnd: c.OldEnd, RightStart: c.NewStart, RightEnd: c.NewEnd}
		switch {
		case c.OldStart == c.OldEnd:
			blk.Kind = BlockAdded
		case c.NewStart == c.NewEnd:
			blk.Kind = BlockDeleted
		default:
			blk.Kind = BlockModified
		}
		if opts.Inline && blk.Kind == BlockModified {
			blk.LeftInline, blk.RightInline = blockInline(a[c.OldStart:c.OldEnd], b[c.NewStart:c.NewEnd], c.OldStart, c.NewStart)
		}
		out = append(out, blk)
	}
	return out
}

func stripSpaceAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, l)
	}
	return out
}

// blockInline pairs the lines of a modified block one by one (the k-th
// left line with the k-th right line) and computes their word ranges.
func blockInline(a, b []string, aStart, bStart int) (la, lb []InlineRanges) {
	if len(a) > maxInlineBlock || len(b) > maxInlineBlock {
		return nil, nil
	}
	for k := 0; k < min(len(a), len(b)); k++ {
		ra, rb := WordDiff(trimEOL(a[k]), trimEOL(b[k]))
		if len(ra) > 0 {
			la = append(la, InlineRanges{Line: aStart + k, Ranges: ra})
		}
		if len(rb) > 0 {
			lb = append(lb, InlineRanges{Line: bStart + k, Ranges: rb})
		}
	}
	return la, lb
}

func trimEOL(s string) string { return strings.TrimRight(s, "\r\n") }

// WordDiff returns the changed byte ranges of two versions of a line:
// words, white space runs and punctuation are compared as tokens; a
// changed word that is similar to its counterpart (e.g. a typo fix) is
// narrowed down to the changed characters. It returns nil for equal,
// empty or very long lines, and when the lines differ almost completely
// (whole-line coloring says enough then). Ranges consisting only of
// white space are dropped.
func WordDiff(a, b string) (ra, rb []ByteRange) {
	if a == b || a == "" || b == "" || len(a) > maxInlineLineLen || len(b) > maxInlineLineLen {
		return nil, nil
	}
	ta, tb := tokenize(a), tokenize(b)
	ch := DiffLines(ta, tb)
	changed := 0
	for _, c := range ch {
		changed += max(c.OldEnd-c.OldStart, c.NewEnd-c.NewStart)
	}
	if changed*10 > max(len(ta), len(tb))*6 {
		return nil, nil
	}
	oa, ob := tokenOffsets(ta), tokenOffsets(tb)
	for _, c := range ch {
		sa, ea := oa[c.OldStart], oa[c.OldEnd]
		sb, eb := ob[c.NewStart], ob[c.NewEnd]
		// One word replaced by one word: narrow to the changed characters.
		if c.OldEnd-c.OldStart == 1 && c.NewEnd-c.NewStart == 1 {
			p, s := commonAffixes(a[sa:ea], b[sb:eb])
			if p+s > 0 && p+s >= min(ea-sa, eb-sb)/2 {
				sa, ea, sb, eb = sa+p, ea-s, sb+p, eb-s
			}
		}
		if ea > sa && strings.TrimSpace(a[sa:ea]) != "" {
			ra = append(ra, ByteRange{Start: sa, End: ea})
		}
		if eb > sb && strings.TrimSpace(b[sb:eb]) != "" {
			rb = append(rb, ByteRange{Start: sb, End: eb})
		}
	}
	return ra, rb
}

// commonAffixes returns the byte lengths of the common prefix and suffix
// of a and b on rune boundaries, never overlapping.
func commonAffixes(a, b string) (p, s int) {
	for p < len(a) && p < len(b) {
		r1, n1 := utf8.DecodeRuneInString(a[p:])
		r2, n2 := utf8.DecodeRuneInString(b[p:])
		if r1 != r2 || n1 != n2 {
			break
		}
		p += n1
	}
	for s < len(a)-p && s < len(b)-p {
		r1, n1 := utf8.DecodeLastRuneInString(a[:len(a)-s])
		r2, n2 := utf8.DecodeLastRuneInString(b[:len(b)-s])
		if r1 != r2 || n1 != n2 || s+n1 > len(a)-p || s+n2 > len(b)-p {
			break
		}
		s += n1
	}
	return p, s
}

// tokenOffsets returns the byte offset of every token plus the total length.
func tokenOffsets(toks []string) []int {
	out := make([]int, len(toks)+1)
	for i, t := range toks {
		out[i+1] = out[i] + len(t)
	}
	return out
}

// tokenize splits s into words (letters, digits, '_', '$', non-ASCII),
// white space runs and single punctuation bytes.
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

// MapLine maps a 0-based line of one side to the other side using the
// blocks of [CompareTexts]: unchanged lines map one to one, a line inside a
// change block maps proportionally into the counterpart block. With
// fromLeft false it maps right to left. The result is fractional so
// scrolling can follow smoothly.
func MapLine(blocks []DiffBlock, line float64, fromLeft bool) float64 {
	shift := 0.0 // other - this, in the unchanged region before line
	for _, b := range blocks {
		s, e, os, oe := b.LeftStart, b.LeftEnd, b.RightStart, b.RightEnd
		if !fromLeft {
			s, e, os, oe = os, oe, s, e
		}
		if line < float64(s) {
			break
		}
		if line < float64(e) {
			return float64(os) + (line-float64(s))*float64(oe-os)/float64(e-s)
		}
		shift = float64(oe - e)
	}
	return line + shift
}
