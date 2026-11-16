// Package search implements find and replace on editor buffers: plain
// text or regular expressions (Go RE2 syntax), case-(in)sensitive, whole
// words, all matches, next/previous from an offset, and replace-all as a
// single undo step.
package search

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// ErrEmptyPattern is returned by Compile for an empty pattern.
var ErrEmptyPattern = errors.New("empty search pattern")

// Options control how a pattern matches.
type Options struct {
	// CaseSensitive distinguishes upper and lower case.
	CaseSensitive bool
	// WholeWord only accepts matches not directly preceded or followed
	// by an identifier character.
	WholeWord bool
	// Regex interprets the pattern as a regular expression (RE2 syntax,
	// multi-line mode: ^ and $ match at line boundaries).
	Regex bool
}

// Match is one search hit.
type Match struct {
	Start, End int
	// Groups holds absolute start/end offset pairs of all submatches,
	// with the whole match first (as in regexp.FindSubmatchIndex). -1
	// marks a group that did not participate.
	Groups []int
}

// Empty reports whether the match is zero-length.
func (m Match) Empty() bool { return m.Start == m.End }

// Query is a compiled search pattern. It is immutable and safe for
// concurrent use, e.g. on a background goroutine with a Snapshot.
type Query struct {
	pattern string
	opts    Options
	re      *regexp.Regexp
}

// Compile prepares a pattern.
func Compile(pattern string, opts Options) (*Query, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	expr := pattern
	if !opts.Regex {
		expr = regexp.QuoteMeta(pattern)
	}
	flags := "m"
	if !opts.CaseSensitive {
		flags += "i"
	}
	re, err := regexp.Compile("(?" + flags + ")" + expr)
	if err != nil {
		return nil, fmt.Errorf("search: compile %q: %w", pattern, err)
	}
	return &Query{pattern: pattern, opts: opts, re: re}, nil
}

// Pattern returns the pattern as given to Compile.
func (q *Query) Pattern() string { return q.pattern }

// Options returns the options given to Compile.
func (q *Query) Options() Options { return q.opts }

// FindAll returns up to limit matches in the document (all if limit <= 0).
func (q *Query) FindAll(s buffer.Snapshot, limit int) []Match {
	return q.find(s.Bytes(), 0, s.Len(), limit)
}

// FindAllIn returns the matches lying completely inside [start, end).
// Anchors and word boundaries see the surrounding text.
func (q *Query) FindAllIn(s buffer.Snapshot, start, end int, limit int) []Match {
	return q.find(s.Bytes(), start, end, limit)
}

func (q *Query) find(text []byte, start, end, limit int) []Match {
	var out []Match
	for _, loc := range q.re.FindAllSubmatchIndex(text, -1) {
		if loc[0] < start {
			continue
		}
		if loc[1] > end {
			break
		}
		if q.opts.WholeWord && !wordBounded(text, loc[0], loc[1]) {
			continue
		}
		out = append(out, Match{Start: loc[0], End: loc[1], Groups: loc})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Next returns the first match starting at or after off. An empty match
// exactly at off is skipped, so calling Next with the end of the previous
// match always advances. With wrap, the search continues at the document
// start.
func (q *Query) Next(s buffer.Snapshot, off int, wrap bool) (Match, bool) {
	all := q.FindAll(s, 0)
	for _, m := range all {
		if m.Start > off || (m.Start == off && !m.Empty()) {
			return m, true
		}
	}
	if wrap && len(all) > 0 {
		return all[0], true
	}
	return Match{}, false
}

// Prev returns the last match starting before off, wrapping to the last
// match of the document if wrap is set.
func (q *Query) Prev(s buffer.Snapshot, off int, wrap bool) (Match, bool) {
	all := q.FindAll(s, 0)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Start < off {
			return all[i], true
		}
	}
	if wrap && len(all) > 0 {
		return all[len(all)-1], true
	}
	return Match{}, false
}

// Expand returns the replacement text for m. For regex queries, $1,
// ${name} and $$ refer to groups (as in regexp.Expand) and \n, \t and \\
// are unescaped; for plain queries repl is used literally.
func (q *Query) Expand(s buffer.Snapshot, m Match, repl string) []byte {
	if !q.opts.Regex {
		return []byte(repl)
	}
	text := s.Slice(m.Start, m.End)
	rel := make([]int, len(m.Groups))
	for i, g := range m.Groups {
		rel[i] = g
		if g >= 0 {
			rel[i] = g - m.Start
		}
	}
	return q.re.Expand(nil, []byte(unescape(repl)), text, rel)
}

// unescape converts \n, \t and \\ in a regex replacement template.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				sb.WriteByte('\n')
				i++
				continue
			case 't':
				sb.WriteByte('\t')
				i++
				continue
			case '\\':
				sb.WriteByte('\\')
				i++
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// Replace replaces a single match (e.g. the current one) as its own undo
// step.
func Replace(b *buffer.Buffer, q *Query, m Match, repl string) (buffer.Edit, error) {
	text := q.Expand(b.Snapshot(), m, repl)
	b.BreakUndo()
	e, err := b.Replace(m.Start, m.End-m.Start, text)
	if err != nil {
		return e, fmt.Errorf("search: replace: %w", err)
	}
	b.BreakUndo()
	return e, nil
}

// ReplaceAll replaces every match in the document as one undo step and
// returns the number of replacements.
func ReplaceAll(b *buffer.Buffer, q *Query, repl string) (int, error) {
	return ReplaceAllIn(b, q, repl, 0, b.Len())
}

// ReplaceAllIn replaces every match inside [start, end) (e.g. a selection)
// as one undo step and returns the number of replacements.
func ReplaceAllIn(b *buffer.Buffer, q *Query, repl string, start, end int) (int, error) {
	s := b.Snapshot()
	matches := q.FindAllIn(s, start, end, 0)
	if len(matches) == 0 {
		return 0, nil
	}
	b.BeginGroup(buffer.GroupOther, nil)
	defer b.EndGroup(nil)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		if _, err := b.Replace(m.Start, m.End-m.Start, q.Expand(s, m, repl)); err != nil {
			return len(matches) - 1 - i, fmt.Errorf("search: replace all: %w", err)
		}
	}
	return len(matches), nil
}

// isWordRune reports whether r is an identifier character.
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBounded reports whether [start, end) is not glued to surrounding
// identifier characters.
func wordBounded(text []byte, start, end int) bool {
	if start > 0 && start < len(text) {
		before, _ := utf8.DecodeLastRune(text[:start])
		first, _ := utf8.DecodeRune(text[start:])
		if isWordRune(before) && isWordRune(first) {
			return false
		}
	}
	if end < len(text) && end > 0 {
		last, _ := utf8.DecodeLastRune(text[:end])
		after, _ := utf8.DecodeRune(text[end:])
		if isWordRune(last) && isWordRune(after) {
			return false
		}
	}
	return true
}
