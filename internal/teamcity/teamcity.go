// Package teamcity parses TeamCity service messages as test runners for
// PHP print them with --teamcity (##teamcity[testStarted name='…' …])
// and builds a result tree from them: suites, test classes, test methods
// and data sets with status, duration, failure message, expected/actual
// values and the reported failure location.
//
// The package is a pure parser: it reads text lines and never runs
// anything. Paths are kept as reported (they may be container paths);
// Tree.MapPath lets the caller map them to host files.
//
// The package imports no Gio and no API code.
package teamcity

import (
	"strconv"
	"strings"
)

// Prefix starts every service message.
const Prefix = "##teamcity["

// Message is one service message: ##teamcity[name key='value' …].
type Message struct {
	// Name is the message name (testStarted, testFailed, …).
	Name string
	// Attrs are the unescaped attribute values.
	Attrs map[string]string
}

// Attr returns attribute key ("" when missing).
func (m Message) Attr(key string) string { return m.Attrs[key] }

// Find returns the service messages in line. Test output may precede a
// message on the same line ("hello##teamcity[testFinished …]"), so the
// prefix is searched anywhere; Before is the text in front of the first
// message (test output that was not terminated by a newline).
func Find(line string) (msgs []Message, before string) {
	rest := line
	first := true
	for {
		i := strings.Index(rest, Prefix)
		if i < 0 {
			if first {
				return nil, line
			}
			return msgs, before
		}
		if first {
			before, first = rest[:i], false
		}
		m, n, ok := parseAt(rest[i+len(Prefix):])
		if !ok {
			// Not a well-formed message: skip the prefix and go on.
			rest = rest[i+len(Prefix):]
			continue
		}
		msgs = append(msgs, m)
		rest = rest[i+len(Prefix)+n:]
	}
}

// Parse parses a line that holds exactly one message (surrounding white
// space allowed).
func Parse(line string) (Message, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, Prefix) {
		return Message{}, false
	}
	m, n, ok := parseAt(s[len(Prefix):])
	if !ok || strings.TrimSpace(s[len(Prefix)+n:]) != "" {
		return Message{}, false
	}
	return m, true
}

// parseAt parses "name key='v' …]" and returns the message and the number
// of bytes consumed including the closing bracket.
func parseAt(s string) (Message, int, bool) {
	i := 0
	for i < len(s) && isNameByte(s[i]) {
		i++
	}
	if i == 0 {
		return Message{}, 0, false
	}
	m := Message{Name: s[:i], Attrs: map[string]string{}}
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return Message{}, 0, false
		}
		if s[i] == ']' {
			return m, i + 1, true
		}
		if s[i] == '\'' {
			// Single-value form: ##teamcity[message 'text'].
			v, n, ok := quoted(s[i:])
			if !ok {
				return Message{}, 0, false
			}
			m.Attrs[""] = v
			i += n
			continue
		}
		k := i
		for i < len(s) && isNameByte(s[i]) {
			i++
		}
		if k == i || i >= len(s) || s[i] != '=' {
			return Message{}, 0, false
		}
		key := s[k:i]
		i++
		if i >= len(s) || s[i] != '\'' {
			return Message{}, 0, false
		}
		v, n, ok := quoted(s[i:])
		if !ok {
			return Message{}, 0, false
		}
		m.Attrs[key] = v
		i += n
	}
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// quoted reads a '…' value starting at s[0] == '\” and returns the
// unescaped value and the bytes consumed.
func quoted(s string) (string, int, bool) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			return b.String(), i + 1, true
		case '|':
			if i+1 >= len(s) {
				return "", 0, false
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case '\'', '|', '[', ']':
				b.WriteByte(s[i])
			case 'l':
				b.WriteRune(' ')
			case 'p':
				b.WriteRune(' ')
			case 'x':
				b.WriteRune('\u0085')
			case '0':
				// |0xXXXX: a unicode code point.
				if i+5 < len(s) && s[i+1] == 'x' {
					if r, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
						b.WriteRune(rune(r))
						i += 5
						continue
					}
				}
				b.WriteByte('0')
			default:
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, false
}

// Escape escapes s for a service message value (the inverse of the
// unescaping done by Parse; used by tests and fake runners).
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\'', '|', '[', ']':
			b.WriteByte('|')
			b.WriteRune(r)
		case '\n':
			b.WriteString("|n")
		case '\r':
			b.WriteString("|r")
		case '\u0085':
			b.WriteString("|x")
		case ' ':
			b.WriteString("|l")
		case ' ':
			b.WriteString("|p")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Format renders a message (attributes in the given key order).
func Format(name string, kv ...string) string {
	var b strings.Builder
	b.WriteString(Prefix)
	b.WriteString(name)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(kv[i])
		b.WriteString("='")
		b.WriteString(Escape(kv[i+1]))
		b.WriteByte('\'')
	}
	b.WriteByte(']')
	return b.String()
}
