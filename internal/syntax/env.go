package syntax

import (
	"bytes"
	"strconv"
)

// envHighlights tokenizes dotenv files (.env): comments, an optional
// "export" keyword, KEY (property), "=" (operator) and the value (string,
// number or constant, with ${VAR} / $VAR references as variables). The
// result tiles [start, end).
func envHighlights(src []byte, start, end int) []Span {
	ls := bytes.LastIndexByte(src[:start], '\n') + 1
	le := end
	if i := bytes.IndexByte(src[end:], '\n'); i >= 0 {
		le = end + i
	} else {
		le = len(src)
	}
	paint := make([]Role, le-ls)
	for off := ls; off <= le; {
		nl := bytes.IndexByte(src[off:le], '\n')
		lineEnd := le
		if nl >= 0 {
			lineEnd = off + nl
		}
		envLine(src[off:lineEnd], paint[off-ls:lineEnd-ls])
		off = lineEnd + 1
	}
	return compress(paint[start-ls:end-ls], start)
}

func envLine(line []byte, paint []Role) {
	set := func(s, e int, r Role) {
		for i := s; i < e; i++ {
			paint[i] = r
		}
	}
	i := skipBlank(line, 0)
	if i >= len(line) {
		return
	}
	if line[i] == '#' {
		set(i, len(line), RoleComment)
		return
	}
	if bytes.HasPrefix(line[i:], []byte("export ")) {
		set(i, i+6, RoleKeyword)
		i = skipBlank(line, i+6)
	}
	k := i
	for k < len(line) && isEnvKeyByte(line[k]) {
		k++
	}
	set(i, k, RoleProperty)
	i = skipBlank(line, k)
	if i >= len(line) || line[i] != '=' {
		return
	}
	set(i, i+1, RoleOperator)
	i = skipBlank(line, i+1)
	if i >= len(line) {
		return
	}
	switch q := line[i]; q {
	case '"', '\'', '`':
		e := i + 1
		for e < len(line) && line[e] != q {
			if line[e] == '\\' && q == '"' {
				e++
			}
			e++
		}
		e = min(e+1, len(line))
		set(i, e, RoleString)
		if q != '\'' {
			envRefs(line, paint, i, e)
		}
		if c := bytes.IndexByte(line[e:], '#'); c >= 0 {
			set(e+c, len(line), RoleComment)
		}
	default:
		e := len(line)
		if c := bytes.Index(line[i:], []byte(" #")); c >= 0 {
			e = i + c
			set(e+1, len(line), RoleComment)
		}
		v := bytes.TrimRight(line[i:e], " \t")
		ve := i + len(v)
		switch {
		case isEnvNumber(v):
			set(i, ve, RoleNumber)
		case isEnvConstant(v):
			set(i, ve, RoleConstant)
		default:
			set(i, ve, RoleString)
			envRefs(line, paint, i, ve)
		}
	}
}

// envRefs marks ${NAME} and $NAME references in line[s:e] as variables.
func envRefs(line []byte, paint []Role, s, e int) {
	for i := s; i < e-1; i++ {
		if line[i] != '$' {
			continue
		}
		j := i + 1
		if line[j] == '{' {
			c := bytes.IndexByte(line[j:e], '}')
			if c < 0 {
				continue
			}
			j += c + 1
		} else {
			for j < e && isEnvKeyByte(line[j]) && line[j] != '.' && line[j] != '-' {
				j++
			}
			if j == i+1 {
				continue
			}
		}
		for k := i; k < j; k++ {
			paint[k] = RoleVariable
		}
		i = j - 1
	}
}

func skipBlank(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return i
}

func isEnvKeyByte(c byte) bool {
	return c == '_' || c == '.' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isEnvNumber(v []byte) bool {
	_, err := strconv.ParseFloat(string(v), 64)
	return err == nil && len(v) > 0 && v[0] != '+' && !bytes.ContainsAny(v, "xXnN_")
}

func isEnvConstant(v []byte) bool {
	switch string(bytes.ToLower(v)) {
	case "true", "false", "null", "on", "off", "yes", "no", "empty":
		return true
	}
	return false
}
