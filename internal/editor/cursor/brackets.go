package cursor

import (
	"unicode"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// MaxBracketScan limits how many bytes MatchBracket inspects.
const MaxBracketScan = 4 << 20

var bracketPairs = map[byte]byte{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}

func isOpener(c byte) bool { return c == '(' || c == '[' || c == '{' }

// MatchBracket finds the bracket pair at off: the bracket right after off
// is preferred, then the one right before it. It returns the offsets of the
// opening and closing bracket. Brackets inside strings and comments are not
// treated specially (the syntax layer can refine this).
func MatchBracket(s buffer.Snapshot, off int) (openOff, closeOff int, ok bool) {
	for _, p := range []int{off, off - 1} {
		c, inDoc := s.ByteAt(p)
		if !inDoc {
			continue
		}
		other, isBracket := bracketPairs[c]
		if !isBracket {
			continue
		}
		if isOpener(c) {
			if q, found := scanBracket(s, p, c, other, true); found {
				return p, q, true
			}
		} else if q, found := scanBracket(s, p, c, other, false); found {
			return q, p, true
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// scanBracket looks for the partner of the bracket self at p.
func scanBracket(s buffer.Snapshot, p int, self, partner byte, forward bool) (int, bool) {
	depth, result := 0, -1
	visit := func(off int, chunk []byte, i int) bool {
		switch chunk[i] {
		case self:
			depth++
		case partner:
			if depth == 0 {
				result = off + i
				return false
			}
			depth--
		}
		return true
	}
	if forward {
		s.ForEachChunk(p+1, p+1+MaxBracketScan, func(off int, chunk []byte) bool {
			for i := range chunk {
				if !visit(off, chunk, i) {
					return false
				}
			}
			return true
		})
	} else {
		s.ForEachChunkReverse(p-MaxBracketScan, p, func(off int, chunk []byte) bool {
			for i := len(chunk) - 1; i >= 0; i-- {
				if !visit(off, chunk, i) {
					return false
				}
			}
			return true
		})
	}
	return result, result >= 0
}

// AutoCloseResult tells the editor what to do for a typed character.
type AutoCloseResult struct {
	// Insert is the text to insert at the caret (empty if Skip).
	Insert string
	// Caret is the caret position after the edit, relative to the start
	// of the insertion, in bytes.
	Caret int
	// Skip means: insert nothing, just step over the next character
	// (typing a closer that is already there).
	Skip bool
}

func closerOf(r rune) (rune, bool) {
	switch r {
	case '(':
		return ')', true
	case '[':
		return ']', true
	case '{':
		return '}', true
	case '"', '\'', '`':
		return r, true
	}
	return 0, false
}

func isQuote(r rune) bool { return r == '"' || r == '\'' || r == '`' }

// AutoClose decides how to handle the typed rune given the runes directly
// before and after the caret on the same line (0 at the line start/end).
// It is a pure function:
//   - an opening bracket inserts the pair if the next rune is blank, a
//     closing bracket, ',', ';' or the line end;
//   - a closing bracket or quote equal to the next rune is skipped over;
//   - a quote inserts a pair unless it is adjacent to a word character;
//   - everything else is inserted as typed.
func AutoClose(typed, prev, next rune) AutoCloseResult {
	plain := AutoCloseResult{Insert: string(typed), Caret: len(string(typed))}
	switch typed {
	case ')', ']', '}':
		if next == typed {
			return AutoCloseResult{Skip: true}
		}
		return plain
	case '(', '[', '{':
		if next == 0 || unicode.IsSpace(next) || next == ')' || next == ']' || next == '}' || next == ',' || next == ';' {
			c, _ := closerOf(typed)
			return AutoCloseResult{Insert: string(typed) + string(c), Caret: 1}
		}
		return plain
	}
	if isQuote(typed) {
		if next == typed {
			return AutoCloseResult{Skip: true}
		}
		if IsWordRune(prev) || IsWordRune(next) || prev == typed || prev == '\\' {
			return plain
		}
		return AutoCloseResult{Insert: string(typed) + string(typed), Caret: 1}
	}
	return plain
}

// AutoDeletePair reports whether backspace between prev and next should
// delete both because they form an empty auto-closed pair.
func AutoDeletePair(prev, next rune) bool {
	c, ok := closerOf(prev)
	return ok && next == c
}
