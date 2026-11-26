package phpstan

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Unserialize parses the output of PHP's serialize() (the subset PHPStan
// writes: null, bool, int, float, string, array, object, enum; references
// become nil; custom serialized objects keep only their class). It never
// executes code or instantiates classes.
func Unserialize(data []byte) (any, error) {
	p := &serParser{b: data}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	return v, nil
}

type serParser struct {
	b   []byte
	pos int
}

// maxDepth bounds nesting (hostile or broken input).
const maxDepth = 512

func (p *serParser) errf(format string, a ...any) error {
	return fmt.Errorf("unserialize at %d: %s", p.pos, fmt.Sprintf(format, a...))
}

func (p *serParser) expect(c byte) error {
	if p.pos >= len(p.b) || p.b[p.pos] != c {
		return p.errf("expected %q", c)
	}
	p.pos++
	return nil
}

// until returns the bytes up to (not including) c and skips c.
func (p *serParser) until(c byte) ([]byte, error) {
	i := bytes.IndexByte(p.b[p.pos:], c)
	if i < 0 {
		return nil, p.errf("missing %q", c)
	}
	s := p.b[p.pos : p.pos+i]
	p.pos += i + 1
	return s, nil
}

func (p *serParser) int(end byte) (int64, error) {
	s, err := p.until(end)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(string(s), 10, 64)
	if err != nil {
		return 0, p.errf("bad number %q", s)
	}
	return n, nil
}

// quoted reads `"<n bytes>"` (the length was read before).
func (p *serParser) quoted(n int64) (string, error) {
	if err := p.expect('"'); err != nil {
		return "", err
	}
	if n < 0 || int64(len(p.b)-p.pos) < n+1 {
		return "", p.errf("string of %d bytes exceeds the input", n)
	}
	s := string(p.b[p.pos : p.pos+int(n)])
	p.pos += int(n)
	if err := p.expect('"'); err != nil {
		return "", err
	}
	return s, nil
}

func (p *serParser) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, p.errf("nested too deeply")
	}
	if p.pos+1 >= len(p.b) {
		return nil, p.errf("unexpected end")
	}
	t := p.b[p.pos]
	if t == 'N' {
		p.pos++
		return nil, p.expect(';')
	}
	p.pos++
	if err := p.expect(':'); err != nil {
		return nil, err
	}
	switch t {
	case 'b':
		n, err := p.int(';')
		return n != 0, err
	case 'i':
		return p.int(';')
	case 'd':
		s, err := p.until(';')
		if err != nil {
			return nil, err
		}
		f, err := strconv.ParseFloat(string(s), 64)
		if err != nil {
			switch string(s) {
			case "INF":
				return 0.0, nil
			case "NAN", "-INF":
				return 0.0, nil
			}
			return nil, p.errf("bad float %q", s)
		}
		return f, nil
	case 'r', 'R':
		_, err := p.int(';')
		return nil, err
	case 's', 'S':
		n, err := p.int(':')
		if err != nil {
			return nil, err
		}
		s, err := p.quoted(n)
		if err != nil {
			return nil, err
		}
		return s, p.expect(';')
	case 'a':
		n, err := p.int(':')
		if err != nil {
			return nil, err
		}
		return p.entries(n, depth)
	case 'O':
		n, err := p.int(':')
		if err != nil {
			return nil, err
		}
		class, err := p.quoted(n)
		if err != nil {
			return nil, err
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		cnt, err := p.int(':')
		if err != nil {
			return nil, err
		}
		props, err := p.entries(cnt, depth)
		if err != nil {
			return nil, err
		}
		for i, k := range props.Keys {
			if s, ok := k.(string); ok {
				props.Keys[i] = unmangle(s)
			}
		}
		return &Object{Class: class, Props: props}, nil
	case 'C':
		n, err := p.int(':')
		if err != nil {
			return nil, err
		}
		class, err := p.quoted(n)
		if err != nil {
			return nil, err
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		l, err := p.int(':')
		if err != nil {
			return nil, err
		}
		if err := p.expect('{'); err != nil {
			return nil, err
		}
		if l < 0 || int64(len(p.b)-p.pos) < l+1 {
			return nil, p.errf("custom data exceeds the input")
		}
		p.pos += int(l)
		return &Object{Class: class, Props: &Array{}}, p.expect('}')
	case 'E':
		n, err := p.int(':')
		if err != nil {
			return nil, err
		}
		s, err := p.quoted(n)
		if err != nil {
			return nil, err
		}
		return s, p.expect(';')
	}
	return nil, p.errf("unknown type %q", t)
}

// entries reads `{key;value…}` with n pairs.
func (p *serParser) entries(n int64, depth int) (*Array, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	if n < 0 || n > int64(len(p.b)) {
		return nil, p.errf("bad count %d", n)
	}
	a := &Array{Keys: make([]any, 0, n), Values: make([]any, 0, n)}
	for i := int64(0); i < n; i++ {
		k, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		switch k.(type) {
		case int64, string:
		default:
			return nil, p.errf("bad array key %T", k)
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		a.Keys = append(a.Keys, k)
		a.Values = append(a.Values, v)
	}
	return a, p.expect('}')
}

// unmangle removes the visibility markers of serialized property names
// ("\0Class\0name" private, "\0*\0name" protected).
func unmangle(s string) string {
	if len(s) > 0 && s[0] == 0 {
		if i := strings.IndexByte(s[1:], 0); i >= 0 {
			return s[i+2:]
		}
	}
	return s
}
