package search

// FindAllBytes returns the matches in text (up to limit; 0 means all). It is
// the byte-slice form of FindAll for content that is not in a buffer, e.g.
// files scanned by a project-wide search.
func (q *Query) FindAllBytes(text []byte, limit int) []Match {
	return q.find(text, 0, len(text), limit)
}

// ExpandBytes returns the replacement text for a match found in text (see
// Expand).
func (q *Query) ExpandBytes(text []byte, m Match, repl string) []byte {
	if !q.opts.Regex {
		return []byte(repl)
	}
	rel := make([]int, len(m.Groups))
	for i, g := range m.Groups {
		rel[i] = g
		if g >= 0 {
			rel[i] = g - m.Start
		}
	}
	return q.re.Expand(nil, []byte(unescape(repl)), text[m.Start:m.End], rel)
}
