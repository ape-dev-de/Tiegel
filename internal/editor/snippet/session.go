package snippet

// Session walks the tab stops of an expanded template in a document and
// keeps their ranges valid while the text changes. The current field is
// "inclusive": text typed at its start or end grows it, so linked editing
// (one caret per occurrence) keeps all occurrences inside their ranges.
// Other fields and the end position only move.
//
// A Session is not safe for concurrent use; feed it every edit of the
// document (Edit) in order.
type Session struct {
	fields []Field
	end    int
	cur    int // index of the current field; len(fields) = at the end
	done   bool
}

// NewSession starts a session on fields (document offsets) with the final
// caret at end. The first field is current; without fields the session
// is done at once.
func NewSession(fields []Field, end int) *Session {
	fs := make([]Field, 0, len(fields))
	for _, f := range fields {
		if len(f.Ranges) > 0 {
			fs = append(fs, Field{Name: f.Name, Ranges: append([]Range(nil), f.Ranges...)})
		}
	}
	s := &Session{fields: fs, end: end}
	s.done = len(fs) == 0
	return s
}

// Done reports whether the session ended (after the last field, Cancel
// or an edit that broke a field).
func (s *Session) Done() bool { return s.done }

// Cancel ends the session.
func (s *Session) Cancel() { s.done = true }

// Current returns the current field (ok false when done).
func (s *Session) Current() (Field, bool) {
	if s.done || s.cur >= len(s.fields) {
		return Field{}, false
	}
	f := s.fields[s.cur]
	return Field{Name: f.Name, Ranges: append([]Range(nil), f.Ranges...)}, true
}

// Index returns the position of the current field and the field count.
func (s *Session) Index() (cur, count int) { return s.cur, len(s.fields) }

// End returns the final caret offset.
func (s *Session) End() int { return s.end }

// Next moves to the next field. After the last one the session is done
// and Next returns false: the caret goes to End.
func (s *Session) Next() bool {
	if s.done {
		return false
	}
	s.cur++
	if s.cur >= len(s.fields) {
		s.done = true
		return false
	}
	return true
}

// Prev moves to the previous field (stays at the first).
func (s *Session) Prev() bool {
	if s.done || s.cur == 0 {
		return false
	}
	s.cur--
	return true
}

// Edit adjusts the ranges to a replacement of [start, oldEnd) by text
// ending at newEnd. An edit that touches a field other than inside the
// current one ends the session (it returns false then).
func (s *Session) Edit(start, oldEnd, newEnd int) bool {
	if s.done {
		return false
	}
	delta := newEnd - oldEnd
	for i := range s.fields {
		for j := range s.fields[i].Ranges {
			r := &s.fields[i].Ranges[j]
			switch {
			case i == s.cur && start >= r.Start && oldEnd <= r.End:
				r.End += delta
			case oldEnd <= r.Start:
				r.Start += delta
				r.End += delta
			case start >= r.End:
			default:
				s.done = true
				return false
			}
		}
	}
	switch {
	case oldEnd <= s.end && !(start == s.end && oldEnd == s.end && s.currentStartsAt(s.end)):
		s.end += delta
	case start >= s.end:
	default:
		s.end = start
	}
	return true
}

// currentStartsAt reports whether a range of the current field starts at
// off: text typed there belongs to the field, so an end position at off
// stays in front of it.
func (s *Session) currentStartsAt(off int) bool {
	if s.cur >= len(s.fields) {
		return false
	}
	for _, r := range s.fields[s.cur].Ranges {
		if r.Start == off {
			return true
		}
	}
	return false
}
