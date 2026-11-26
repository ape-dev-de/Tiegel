package mermaid

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reBreak    = regexp.MustCompile(`(?i)<br\s*/?>`)
	reTag      = regexp.MustCompile(`</?[a-zA-Z][^<>]*>`)
	reEntity   = regexp.MustCompile(`#([0-9]+|[a-z]+);`)
	reIcon     = regexp.MustCompile(`\bfa[bklrs]?:fa-[a-z0-9-]+\s*`)
	reEmphasis = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|\b_([^_]+)_\b`)
)

var namedEntities = map[string]string{
	"quot": `"`, "amp": "&", "lt": "<", "gt": ">", "nbsp": " ", "apos": "'", "hash": "#", "semi": ";",
}

// labelLines turns a Mermaid label (quoted, markdown string, with <br>
// tags, entity codes and simple HTML) into plain text lines.
func labelLines(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	markdown := false
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		s, markdown = s[1:len(s)-1], true
	}
	s = reBreak.ReplaceAllString(s, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = reIcon.ReplaceAllString(s, "")
	s = decodeEntities(s)
	s = reEmphasis.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Trim(m, "*_")
	})
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if markdown && l == "" && len(out) == 0 {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 1 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// decodeEntities replaces Mermaid entity codes ("#quot;", "#9829;") and
// the common HTML entities ("&amp;").
func decodeEntities(s string) string {
	s = reEntity.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		if n, err := strconv.Atoi(name); err == nil && n > 0 && n < 0x110000 {
			return string(rune(n))
		}
		if v, ok := namedEntities[name]; ok {
			return v
		}
		return m
	})
	for k, v := range namedEntities {
		s = strings.ReplaceAll(s, "&"+k+";", v)
	}
	return s
}

// wrap breaks lines wider than maxW at spaces.
func (e *env) wrap(lines []string, size float64, bold bool, maxW float64) []string {
	var out []string
	for _, l := range lines {
		if e.measure(l, size, bold) <= maxW {
			out = append(out, l)
			continue
		}
		cur := ""
		for _, w := range strings.Fields(l) {
			next := w
			if cur != "" {
				next = cur + " " + w
			}
			if cur != "" && e.measure(next, size, bold) > maxW {
				out = append(out, cur)
				cur = w
				continue
			}
			cur = next
		}
		out = append(out, cur)
	}
	return out
}

// joinLines is the single-string form of a label (for NodeInfo/EdgeInfo).
func joinLines(ls []string) string { return strings.Join(ls, "\n") }
