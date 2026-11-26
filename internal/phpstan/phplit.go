package phpstan

import (
	"fmt"
	"strconv"
	"strings"
)

// litParser reads PHP literal expressions as var_export() writes them
// (result cache before PHPStan 2.2) and as PHP baselines use them: arrays
// (array(…) and […]), strings, numbers, null/true/false, objects restored
// with Class::__set_state(array(…)) (kept as Object, never instantiated),
// (object) casts, class constants and enum cases (kept as "Class::NAME"),
// __DIR__ and string concatenation with ".". Nothing is executed.
type litParser struct {
	b   []byte
	pos int
	// dir is the value of __DIR__.
	dir string
}

// ParseLiteral parses one PHP literal expression (see litParser).
func ParseLiteral(src []byte, dir string) (any, error) {
	p := &litParser{b: src, dir: dir}
	v, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (p *litParser) errf(format string, a ...any) error {
	line := 1 + strings.Count(string(p.b[:min(p.pos, len(p.b))]), "\n")
	return fmt.Errorf("php literal, line %d: %s", line, fmt.Sprintf(format, a...))
}

// skip skips blanks and comments.
func (p *litParser) skip() {
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '#' || c == '/' && p.pos+1 < len(p.b) && p.b[p.pos+1] == '/':
			for p.pos < len(p.b) && p.b[p.pos] != '\n' {
				p.pos++
			}
		case c == '/' && p.pos+1 < len(p.b) && p.b[p.pos+1] == '*':
			end := strings.Index(string(p.b[p.pos+2:]), "*/")
			if end < 0 {
				p.pos = len(p.b)
				return
			}
			p.pos += end + 4
		default:
			return
		}
	}
}

func (p *litParser) peek() byte {
	p.skip()
	if p.pos >= len(p.b) {
		return 0
	}
	return p.b[p.pos]
}

func (p *litParser) eat(s string) bool {
	p.skip()
	if strings.HasPrefix(string(p.b[p.pos:min(len(p.b), p.pos+len(s))]), s) {
		p.pos += len(s)
		return true
	}
	return false
}

// expr is a term followed by "." concatenations.
func (p *litParser) expr(depth int) (any, error) {
	v, err := p.term(depth)
	if err != nil {
		return nil, err
	}
	for p.peek() == '.' && !p.isNumberStart(p.pos) {
		p.pos++
		w, err := p.term(depth)
		if err != nil {
			return nil, err
		}
		v = toString(v) + toString(w)
	}
	return v, nil
}

func (p *litParser) isNumberStart(i int) bool {
	return i+1 < len(p.b) && p.b[i] == '.' && p.b[i+1] >= '0' && p.b[i+1] <= '9'
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		if t {
			return "1"
		}
	}
	return ""
}

func isIdent(c byte) bool {
	return c == '_' || c == '\\' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func (p *litParser) term(depth int) (any, error) {
	if depth > maxDepth {
		return nil, p.errf("nested too deeply")
	}
	c := p.peek()
	switch {
	case c == 0:
		return nil, p.errf("unexpected end")
	case c == '\'':
		return p.single()
	case c == '"':
		return p.double()
	case c == '[':
		p.pos++
		return p.entries(']', depth)
	case c == '(':
		// (object) array(…) or a parenthesised expression.
		save := p.pos
		p.pos++
		if p.eat("object") && p.eat(")") {
			v, err := p.term(depth + 1)
			if err != nil {
				return nil, err
			}
			a, _ := v.(*Array)
			if a == nil {
				a = &Array{}
			}
			return &Object{Class: "stdClass", Props: a}, nil
		}
		p.pos = save + 1
		v, err := p.expr(depth + 1)
		if err != nil {
			return nil, err
		}
		if !p.eat(")") {
			return nil, p.errf("missing )")
		}
		return v, nil
	case c == '-' || c == '+' || c >= '0' && c <= '9' || p.isNumberStart(p.pos):
		return p.number()
	case isIdent(c):
		return p.name(depth)
	}
	return nil, p.errf("unexpected %q", c)
}

func (p *litParser) number() (any, error) {
	start := p.pos
	if p.b[p.pos] == '-' || p.b[p.pos] == '+' {
		p.pos++
	}
	float := false
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		switch {
		case c >= '0' && c <= '9' || c == '_':
		case c == '.' || c == 'e' || c == 'E':
			float = true
		case (c == '-' || c == '+') && (p.b[p.pos-1] == 'e' || p.b[p.pos-1] == 'E'):
		default:
			goto done
		}
		p.pos++
	}
done:
	s := strings.ReplaceAll(string(p.b[start:p.pos]), "_", "")
	if !float {
		if n, err := strconv.ParseInt(s, 0, 64); err == nil {
			return n, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, p.errf("bad number %q", s)
	}
	return f, nil
}

// name reads constants (null, true, false, __DIR__, INF, NAN), the
// array(…) construct, Class::__set_state(…), Class::CONSTANT and other
// static calls (their value is nil).
func (p *litParser) name(depth int) (any, error) {
	start := p.pos
	for p.pos < len(p.b) && isIdent(p.b[p.pos]) {
		p.pos++
	}
	n := string(p.b[start:p.pos])
	switch strings.ToLower(strings.TrimPrefix(n, "\\")) {
	case "null":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "__dir__":
		return p.dir, nil
	case "inf", "nan":
		return 0.0, nil
	case "array":
		if !p.eat("(") {
			return nil, p.errf("missing ( after array")
		}
		return p.entries(')', depth)
	}
	class := strings.TrimPrefix(n, "\\")
	if !p.eat("::") {
		if p.peek() == '(' { // a function call: skip it
			p.pos++
			if _, err := p.entries(')', depth); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return class, nil // a global constant
	}
	p.skip()
	start = p.pos
	for p.pos < len(p.b) && isIdent(p.b[p.pos]) && p.b[p.pos] != '\\' {
		p.pos++
	}
	member := string(p.b[start:p.pos])
	if member == "" {
		return nil, p.errf("missing member after %s::", class)
	}
	if p.peek() != '(' {
		return class + "::" + member, nil
	}
	p.pos++
	args, err := p.entries(')', depth)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(member, "__set_state") {
		props := &Array{}
		if args.Len() > 0 {
			if a, ok := args.Values[0].(*Array); ok {
				props = a
			}
		}
		return &Object{Class: class, Props: props}, nil
	}
	return nil, nil
}

// entries reads array entries (key => value or value) up to close.
func (p *litParser) entries(close byte, depth int) (*Array, error) {
	a := &Array{}
	for {
		if c := p.peek(); c == close {
			p.pos++
			return a, nil
		} else if c == 0 {
			return nil, p.errf("missing %q", close)
		}
		v, err := p.expr(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.eat("=>") {
			w, err := p.expr(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k := v.(type) {
			case int64, string:
				a.set(k, w)
			case bool:
				if k {
					a.set(int64(1), w)
				} else {
					a.set(int64(0), w)
				}
			case nil:
				a.set("", w)
			default:
				a.set(toString(k), w)
			}
		} else {
			a.add(v)
		}
		if p.peek() == ',' {
			p.pos++
		}
	}
}

// single reads a single-quoted string (escapes \\ and \').
func (p *litParser) single() (any, error) {
	p.pos++
	var sb strings.Builder
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		switch {
		case c == '\\' && p.pos+1 < len(p.b) && (p.b[p.pos+1] == '\\' || p.b[p.pos+1] == '\''):
			sb.WriteByte(p.b[p.pos+1])
			p.pos += 2
		case c == '\'':
			p.pos++
			return sb.String(), nil
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
	return nil, p.errf("unterminated string")
}

// double reads a double-quoted string with the usual escapes (no
// interpolation: var_export only writes "\0").
func (p *litParser) double() (any, error) {
	p.pos++
	var sb strings.Builder
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		switch {
		case c == '"':
			p.pos++
			return sb.String(), nil
		case c == '\\' && p.pos+1 < len(p.b):
			e := p.b[p.pos+1]
			p.pos += 2
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case 'v':
				sb.WriteByte('\v')
			case 'f':
				sb.WriteByte('\f')
			case 'e':
				sb.WriteByte(0x1b)
			case '\\', '"', '$':
				sb.WriteByte(e)
			case 'x':
				j := p.pos
				for j < len(p.b) && j < p.pos+2 && isHex(p.b[j]) {
					j++
				}
				if n, err := strconv.ParseUint(string(p.b[p.pos:j]), 16, 8); err == nil && j > p.pos {
					sb.WriteByte(byte(n))
					p.pos = j
				} else {
					sb.WriteString("\\x")
				}
			case '0', '1', '2', '3', '4', '5', '6', '7':
				j := p.pos - 1
				for j < len(p.b) && j < p.pos+2 && p.b[j] >= '0' && p.b[j] <= '7' {
					j++
				}
				n, _ := strconv.ParseUint(string(p.b[p.pos-1:j]), 8, 8)
				sb.WriteByte(byte(n))
				p.pos = j
			default:
				sb.WriteByte('\\')
				sb.WriteByte(e)
			}
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
	return nil, p.errf("unterminated string")
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
