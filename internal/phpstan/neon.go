package phpstan

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseNeon parses the NEON subset PHPStan configurations use: block
// mappings and lists (indented with tabs or spaces, "- key: value" items),
// inline [lists] and {mappings} (also spanning lines), quoted strings
// (” and "" with escapes, ”'/""" blocks), unquoted literals, comments
// and entities (Name(args) – kept as the name string). Mappings and lists
// are *Array.
func ParseNeon(src []byte) (any, error) {
	p := &neonParser{}
	p.split(string(src))
	if len(p.lines) == 0 {
		return &Array{}, nil
	}
	v, next, err := p.block(0, p.lines[0].indent)
	if err != nil {
		return nil, err
	}
	if next < len(p.lines) {
		return nil, p.errf(next, "unexpected indentation")
	}
	return v, nil
}

type neonLine struct {
	no     int // 1-based source line
	indent int
	text   string // without indentation and comment
}

type neonParser struct {
	lines []neonLine
}

func (p *neonParser) errf(i int, format string, a ...any) error {
	no := 0
	if i < len(p.lines) {
		no = p.lines[i].no
	} else if len(p.lines) > 0 {
		no = p.lines[len(p.lines)-1].no
	}
	return fmt.Errorf("neon line %d: %s", no, fmt.Sprintf(format, a...))
}

// split turns the source into significant lines; multi-line strings
// (”'/""") are folded into one line as a quoted literal.
func (p *neonParser) split(src string) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	raw := strings.Split(src, "\n")
	for i := 0; i < len(raw); i++ {
		l := raw[i]
		body := strings.TrimLeft(l, " \t")
		indent := len(l) - len(body)
		body = strings.TrimRight(stripComment(body), " \t")
		if body == "" {
			continue
		}
		for _, q := range []string{"'''", `"""`} {
			if strings.HasSuffix(body, q) && strings.Count(body, q) == 1 {
				var parts []string
				j := i + 1
				for ; j < len(raw); j++ {
					if strings.TrimSpace(raw[j]) == q {
						break
					}
					parts = append(parts, strings.TrimLeft(raw[j], " \t"))
				}
				body = strings.TrimSuffix(body, q) + quoteSingle(strings.Join(parts, "\n"))
				i = j
			}
		}
		p.lines = append(p.lines, neonLine{no: i + 1, indent: indent, text: body})
	}
}

func quoteSingle(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// stripComment removes a "#" comment that is not inside quotes and starts
// a token (at the beginning or after a blank).
func stripComment(s string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				q = 0
			} else if c == '\\' && q == '"' {
				i++
			}
		case c == '\'' || c == '"':
			if i == 0 || strings.ContainsRune(" \t:[{(,=-", rune(s[i-1])) {
				q = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

// block parses the lines starting at i with the given indentation: a
// list, a mapping or a single value.
func (p *neonParser) block(i, indent int) (any, int, error) {
	l := p.lines[i]
	if l.text == "-" || strings.HasPrefix(l.text, "- ") {
		return p.list(i, indent)
	}
	if _, _, ok := splitKey(l.text); ok {
		return p.mapping(i, indent)
	}
	v, next, err := p.inlineFrom(i)
	return v, next, err
}

func (p *neonParser) list(i, indent int) (any, int, error) {
	a := &Array{}
	for i < len(p.lines) && p.lines[i].indent == indent {
		l := p.lines[i]
		if l.text != "-" && !strings.HasPrefix(l.text, "- ") {
			break
		}
		rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " \t")
		if rest == "" {
			if i+1 < len(p.lines) && p.lines[i+1].indent > indent {
				v, next, err := p.block(i+1, p.lines[i+1].indent)
				if err != nil {
					return nil, 0, err
				}
				a.add(v)
				i = next
				continue
			}
			a.add(nil)
			i++
			continue
		}
		// "- key: value": a mapping whose further keys are indented to
		// the column of key.
		col := indent + (len(l.text) - len(rest))
		if _, _, ok := splitKey(rest); ok {
			p.lines[i] = neonLine{no: l.no, indent: col, text: rest}
			v, next, err := p.mapping(i, col)
			p.lines[i] = l
			if err != nil {
				return nil, 0, err
			}
			a.add(v)
			i = next
			continue
		}
		p.lines[i] = neonLine{no: l.no, indent: col, text: rest}
		v, next, err := p.inlineFrom(i)
		p.lines[i] = l
		if err != nil {
			return nil, 0, err
		}
		a.add(v)
		i = next
	}
	return a, i, nil
}

func (p *neonParser) mapping(i, indent int) (any, int, error) {
	a := &Array{}
	for i < len(p.lines) && p.lines[i].indent == indent {
		l := p.lines[i]
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, 0, p.errf(i, "expected key: value")
		}
		if rest == "" {
			if i+1 < len(p.lines) && p.lines[i+1].indent > indent {
				v, next, err := p.block(i+1, p.lines[i+1].indent)
				if err != nil {
					return nil, 0, err
				}
				a.set(key, v)
				i = next
				continue
			}
			// NEON also allows a list at the same indentation as its key.
			if i+1 < len(p.lines) && p.lines[i+1].indent == indent && strings.HasPrefix(p.lines[i+1].text, "-") {
				v, next, err := p.list(i+1, indent)
				if err != nil {
					return nil, 0, err
				}
				a.set(key, v)
				i = next
				continue
			}
			a.set(key, nil)
			i++
			continue
		}
		p.lines[i] = neonLine{no: l.no, indent: l.indent, text: rest}
		v, next, err := p.inlineFrom(i)
		p.lines[i] = l
		if err != nil {
			return nil, 0, err
		}
		a.set(key, v)
		i = next
	}
	return a, i, nil
}

// splitKey splits "key: rest" (key unquoted or quoted). Values of URLs
// or times ("a:b") are no keys: the colon must be followed by a blank or
// end the line.
func splitKey(s string) (key, rest string, ok bool) {
	if s == "" || s[0] == '[' || s[0] == '{' || s[0] == '-' && (len(s) == 1 || s[1] == ' ') {
		return "", "", false
	}
	if s[0] == '\'' || s[0] == '"' {
		sc := &neonScanner{s: s}
		v, err := sc.quoted()
		if err != nil {
			return "", "", false
		}
		r := s[sc.pos:]
		if strings.HasPrefix(r, ":") && (len(r) == 1 || r[1] == ' ' || r[1] == '\t') {
			return toString(v), strings.TrimSpace(r[1:]), true
		}
		return "", "", false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ':':
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' {
				k := strings.TrimSpace(s[:i])
				if k == "" || strings.ContainsAny(k, "[]{}(),") {
					return "", "", false
				}
				return k, strings.TrimSpace(s[i+1:]), true
			}
		case '[', '{', '(', ',', '=':
			return "", "", false
		}
	}
	return "", "", false
}

// inlineFrom parses an inline value starting at line i, joining following
// lines while brackets are open.
func (p *neonParser) inlineFrom(i int) (any, int, error) {
	text := p.lines[i].text
	next := i + 1
	for depth(text) > 0 && next < len(p.lines) {
		text += "\n" + p.lines[next].text
		next++
	}
	sc := &neonScanner{s: text}
	v, err := sc.value(0)
	if err != nil {
		return nil, 0, p.errf(i, "%v", err)
	}
	sc.ws()
	if sc.pos < len(sc.s) {
		return nil, 0, p.errf(i, "unexpected %q", truncate(sc.s[sc.pos:], 20))
	}
	return v, next, nil
}

// depth returns the number of open brackets outside quotes.
func depth(s string) int {
	d := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				q = 0
			} else if c == '\\' && q == '"' {
				i++
			}
		case c == '\'' || c == '"':
			q = c
		case c == '[' || c == '{' || c == '(':
			d++
		case c == ']' || c == '}' || c == ')':
			d--
		}
	}
	return d
}

// neonScanner reads inline NEON values.
type neonScanner struct {
	s   string
	pos int
}

func (sc *neonScanner) ws() {
	for sc.pos < len(sc.s) && strings.IndexByte(" \t\n", sc.s[sc.pos]) >= 0 {
		sc.pos++
	}
}

func (sc *neonScanner) value(d int) (any, error) {
	if d > maxDepth {
		return nil, fmt.Errorf("nested too deeply")
	}
	sc.ws()
	if sc.pos >= len(sc.s) {
		return nil, nil
	}
	switch sc.s[sc.pos] {
	case '\'', '"':
		return sc.quoted()
	case '[':
		sc.pos++
		return sc.items(']', d)
	case '{':
		sc.pos++
		return sc.items('}', d)
	}
	start := sc.pos
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		if c == ',' || c == ']' || c == '}' || c == ')' || c == '\n' {
			break
		}
		if c == '(' { // an entity: Name(args)
			name := strings.TrimSpace(sc.s[start:sc.pos])
			sc.pos++
			if _, err := sc.items(')', d); err != nil {
				return nil, err
			}
			return name, nil
		}
		if c == ':' && sc.pos+1 < len(sc.s) && (sc.s[sc.pos+1] == ' ' || sc.s[sc.pos+1] == '\t') {
			break
		}
		if c == '=' && sc.pos+1 < len(sc.s) && sc.s[sc.pos+1] == ' ' {
			break
		}
		sc.pos++
	}
	return literal(strings.TrimSpace(sc.s[start:sc.pos])), nil
}

// items reads the entries of an inline list or mapping up to close.
func (sc *neonScanner) items(close byte, d int) (*Array, error) {
	a := &Array{}
	for {
		sc.ws()
		if sc.pos >= len(sc.s) {
			return nil, fmt.Errorf("missing %q", close)
		}
		if sc.s[sc.pos] == close {
			sc.pos++
			return a, nil
		}
		v, err := sc.value(d + 1)
		if err != nil {
			return nil, err
		}
		sc.ws()
		if sc.pos < len(sc.s) && (sc.s[sc.pos] == ':' || sc.s[sc.pos] == '=') {
			sc.pos++
			w, err := sc.value(d + 1)
			if err != nil {
				return nil, err
			}
			a.set(toString(v), w)
		} else {
			a.add(v)
		}
		sc.ws()
		if sc.pos < len(sc.s) && sc.s[sc.pos] == ',' {
			sc.pos++
		}
	}
}

func (sc *neonScanner) quoted() (any, error) {
	q := sc.s[sc.pos]
	sc.pos++
	var b strings.Builder
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		switch {
		case c == q && q == '\'' && sc.pos+1 < len(sc.s) && sc.s[sc.pos+1] == '\'':
			b.WriteByte('\'')
			sc.pos += 2
		case c == q:
			sc.pos++
			return b.String(), nil
		case c == '\\' && q == '"' && sc.pos+1 < len(sc.s):
			e := sc.s[sc.pos+1]
			sc.pos += 2
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'u':
				if sc.pos+4 <= len(sc.s) {
					if n, err := strconv.ParseUint(sc.s[sc.pos:sc.pos+4], 16, 32); err == nil {
						b.WriteRune(rune(n))
						sc.pos += 4
						continue
					}
				}
				b.WriteString(`\u`)
			default:
				b.WriteByte('\\')
				b.WriteByte(e)
			}
		default:
			b.WriteByte(c)
			sc.pos++
		}
	}
	return nil, fmt.Errorf("unterminated string")
}

// literal converts an unquoted NEON literal.
func literal(s string) any {
	switch strings.ToLower(s) {
	case "", "null":
		if s == "" {
			return nil
		}
		return nil
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.ContainsAny(s, ".eE") {
		return f
	}
	return s
}
