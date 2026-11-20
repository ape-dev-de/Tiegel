package terminal

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// FindOptions describe a search in scrollback and screen.
type FindOptions struct {
	Query string `json:"query"`
	// CaseSensitive matches case exactly (default: ignore case); Regex
	// takes Query as a regular expression (RE2).
	CaseSensitive bool `json:"caseSensitive,omitempty"`
	Regex         bool `json:"regex,omitempty"`
	// Limit bounds the number of matches (0: 1000).
	Limit int `json:"limit,omitempty"`
}

// Match is a found range: from (Line, Col) to (EndLine, EndCol), end
// column exclusive; lines are absolute indexes (scrollback + screen). A
// match may continue over wrapped lines.
type Match struct {
	Line    int `json:"line"`
	Col     int `json:"col"`
	EndLine int `json:"endLine"`
	EndCol  int `json:"endCol"`
}

// FindResult lists the matches in order; Truncated tells that the limit
// cut the list.
type FindResult struct {
	Matches   []Match `json:"matches"`
	Truncated bool    `json:"truncated,omitempty"`
	// Dropped is the scrollback drop count the line indexes refer to.
	Dropped int `json:"dropped"`
}

// Find searches the session's scrollback and screen.
func (s *Session) Find(o FindOptions) (FindResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return find(s.term, o)
}

// find searches t; lines joined by autowrap are searched as one.
func find(t *vt.Terminal, o FindOptions) (FindResult, error) {
	res := FindResult{Matches: []Match{}, Dropped: t.Dropped()}
	if o.Query == "" {
		return res, nil
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 1000
	}
	pat := o.Query
	if !o.Regex {
		pat = regexp.QuoteMeta(pat)
	}
	if !o.CaseSensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return res, fmt.Errorf("terminal: find: %w", err)
	}
	type pos struct{ line, col int }
	total := t.Total()
	var (
		b   strings.Builder
		at  []pos
		end []pos // position after the character of each byte
	)
	for i := 0; i < total; {
		b.Reset()
		at, end = at[:0], end[:0]
		j := i
		for ; j < total; j++ {
			l := t.Line(j)
			for c, cell := range l.Cells {
				if cell.Width == 0 {
					continue
				}
				g := t.Grapheme(cell)
				w := max(1, int(cell.Width))
				for range len(g) {
					at = append(at, pos{j, c})
					end = append(end, pos{j, c + w})
				}
				b.WriteString(g)
			}
			if !l.Wrapped {
				break
			}
		}
		text := b.String()
		for _, m := range re.FindAllStringIndex(text, -1) {
			if m[1] <= m[0] {
				continue
			}
			if len(res.Matches) >= limit {
				res.Truncated = true
				return res, nil
			}
			s, e := at[m[0]], end[m[1]-1]
			res.Matches = append(res.Matches, Match{Line: s.line, Col: s.col, EndLine: e.line, EndCol: e.col})
		}
		i = j + 1
	}
	return res, nil
}
