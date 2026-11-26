package mermaid

import (
	"fmt"
	"strconv"
	"strings"
)

// style is a parsed CSS-like property list ("fill:#f9f,stroke:#333").
type style map[string]string

// parseStyle splits props at commas outside parentheses.
func parseStyle(s string) style {
	st := style{}
	depth, start := 0, 0
	flush := func(end int) {
		kv := strings.TrimSpace(s[start:end])
		k, v, ok := strings.Cut(kv, ":")
		if ok {
			st[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), ";"))
		}
	}
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',', ';':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return st
}

// merge copies o over st.
func (st style) merge(o style) {
	for k, v := range o {
		st[k] = v
	}
}

func (st style) color(k string) string { return parseColor(st[k]) }

// width returns stroke-width in units (0: unset).
func (st style) width() float64 {
	v := strings.TrimSuffix(strings.TrimSpace(st["stroke-width"]), "px")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return min(f, 8)
}

func (st style) dashed() bool { return strings.TrimSpace(st["stroke-dasharray"]) != "" }

var namedColors = map[string]string{
	"black": "#000000", "white": "#ffffff", "red": "#ff0000", "green": "#008000", "blue": "#0000ff",
	"yellow": "#ffff00", "orange": "#ffa500", "purple": "#800080", "pink": "#ffc0cb", "gray": "#808080",
	"grey": "#808080", "lightgray": "#d3d3d3", "lightgrey": "#d3d3d3", "darkgray": "#a9a9a9", "silver": "#c0c0c0",
	"lightblue": "#add8e6", "lightgreen": "#90ee90", "lightyellow": "#ffffe0", "lightpink": "#ffb6c1",
	"darkblue": "#00008b", "darkgreen": "#006400", "darkred": "#8b0000", "navy": "#000080", "teal": "#008080",
	"aqua": "#00ffff", "cyan": "#00ffff", "magenta": "#ff00ff", "fuchsia": "#ff00ff", "lime": "#00ff00",
	"maroon": "#800000", "olive": "#808000", "brown": "#a52a2a", "gold": "#ffd700", "coral": "#ff7f50",
	"salmon": "#fa8072", "violet": "#ee82ee", "indigo": "#4b0082", "beige": "#f5f5dc", "ivory": "#fffff0",
	"khaki": "#f0e68c", "lavender": "#e6e6fa", "tomato": "#ff6347", "skyblue": "#87ceeb", "steelblue": "#4682b4",
	"aliceblue": "#f0f8ff", "honeydew": "#f0fff0", "mintcream": "#f5fffa", "whitesmoke": "#f5f5f5",
	"transparent": "#00000000",
}

// parseColor normalizes a CSS color to "#rrggbb" or "#rrggbbaa"; unknown
// values give "".
func parseColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "!important")))
	if s == "" {
		return ""
	}
	if c, ok := namedColors[s]; ok {
		return c
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if _, err := strconv.ParseUint(h, 16, 64); err != nil {
			return ""
		}
		switch len(h) {
		case 3, 4:
			var b strings.Builder
			b.WriteByte('#')
			for _, c := range h {
				b.WriteRune(c)
				b.WriteRune(c)
			}
			return b.String()
		case 6, 8:
			return s
		}
		return ""
	}
	if strings.HasPrefix(s, "rgb") {
		open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || close < open {
			return ""
		}
		parts := strings.FieldsFunc(s[open+1:close], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(parts) < 3 {
			return ""
		}
		var v [4]int
		v[3] = 255
		for i, p := range parts[:min(4, len(parts))] {
			f, err := strconv.ParseFloat(strings.TrimSuffix(p, "%"), 64)
			if err != nil {
				return ""
			}
			switch {
			case i == 3 && strings.HasSuffix(p, "%"):
				f = f / 100 * 255
			case i == 3:
				f *= 255
			case strings.HasSuffix(p, "%"):
				f = f / 100 * 255
			}
			v[i] = int(min(max(f, 0), 255) + 0.5)
		}
		if v[3] == 255 {
			return fmt.Sprintf("#%02x%02x%02x", v[0], v[1], v[2])
		}
		return fmt.Sprintf("#%02x%02x%02x%02x", v[0], v[1], v[2], v[3])
	}
	return ""
}

// RGBA decodes a color produced by the parser ("#rrggbb[aa]").
func RGBA(c string) (r, g, b, a uint8, ok bool) {
	if len(c) != 7 && len(c) != 9 || c[0] != '#' {
		return 0, 0, 0, 0, false
	}
	v, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	if len(c) == 7 {
		return uint8(v >> 16), uint8(v >> 8), uint8(v), 255, true
	}
	return uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v), true
}

// contrastText returns black or white, whichever reads better on fill.
func contrastText(fill string) string {
	r, g, b, a, ok := RGBA(fill)
	if !ok || a < 128 {
		return ""
	}
	l := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if l > 150 {
		return "#1f2328"
	}
	return "#ffffff"
}
