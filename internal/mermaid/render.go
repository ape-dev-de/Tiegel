package mermaid

import (
	"strings"
)

// srcLine is a statement with its 1-based source line.
type srcLine struct {
	n    int
	text string
}

// Render parses src and lays out the diagram.
func Render(src string, o Options) (*Diagram, error) {
	lines, title := prepare(src)
	if len(lines) == 0 {
		return nil, errorf(0, "empty diagram")
	}
	head := lines[0]
	kw := strings.ToLower(strings.Fields(head.text)[0])
	e := newEnv(o)
	var d *Diagram
	var err error
	switch kw {
	case "flowchart", "graph", "flowchart-elk":
		d, err = renderFlowchart(e, lines)
	case "sequencediagram":
		d, err = renderSequence(e, lines)
	case "statediagram", "statediagram-v2":
		d, err = renderState(e, lines)
	case "classdiagram", "classdiagram-v2":
		d, err = renderClass(e, lines)
	case "erdiagram":
		d, err = renderER(e, lines)
	case "pie":
		d, err = renderPie(e, lines)
	default:
		return nil, errorf(head.n, "unsupported diagram type %q", strings.Fields(head.text)[0])
	}
	if err != nil {
		return nil, err
	}
	if title != "" {
		d.addTitle(e, title)
	}
	return d, nil
}

// Kind returns the diagram type of src ("flowchart", "sequence", …) or ""
// when it is not recognized; it does not parse the body.
func Kind(src string) string {
	lines, _ := prepare(src)
	if len(lines) == 0 {
		return ""
	}
	switch strings.ToLower(strings.Fields(lines[0].text)[0]) {
	case "flowchart", "graph", "flowchart-elk":
		return "flowchart"
	case "sequencediagram":
		return "sequence"
	case "statediagram", "statediagram-v2":
		return "state"
	case "classdiagram", "classdiagram-v2":
		return "class"
	case "erdiagram":
		return "er"
	case "pie":
		return "pie"
	}
	return ""
}

// prepare strips front matter (keeping its title), %%{init}%% directives
// and %% comments, and returns the non-empty lines.
func prepare(src string) ([]srcLine, string) {
	raw := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	title := ""
	i := 0
	for i < len(raw) && strings.TrimSpace(raw[i]) == "" {
		i++
	}
	if i < len(raw) && strings.TrimSpace(raw[i]) == "---" {
		for j := i + 1; j < len(raw); j++ {
			t := strings.TrimSpace(raw[j])
			if t == "---" {
				i = j + 1
				break
			}
			if v, ok := strings.CutPrefix(t, "title:"); ok {
				title = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	var out []srcLine
	for ; i < len(raw); i++ {
		t := strings.TrimSpace(raw[i])
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		out = append(out, srcLine{n: i + 1, text: t})
	}
	return out, title
}

// addTitle puts a centered bold title above the diagram.
func (d *Diagram) addTitle(e *env, title string) {
	size := e.size * 1.15
	lines := labelLines(title)
	tw, th := e.textSize(lines, size, true)
	shift := th + 12
	d.shift(0, shift)
	d.Width = max(d.Width, tw+16)
	d.Items = append(d.Items, Item{Text: e.textAt(lines, d.Width/2, 8, size, true, AnchorMiddle, Paint{Role: RoleText})})
	d.Height += shift
}

// shift moves every item, node and edge by (dx, dy).
func (d *Diagram) shift(dx, dy float64) {
	mv := func(p *Point) { p.X += dx; p.Y += dy }
	for i := range d.Items {
		it := &d.Items[i]
		for s := range it.Path {
			for k := range it.Path[s].Pts {
				mv(&it.Path[s].Pts[k])
			}
		}
		if it.Text != nil {
			it.Text.X += dx
			it.Text.Y += dy
		}
	}
	for i := range d.Nodes {
		d.Nodes[i].Box.X += dx
		d.Nodes[i].Box.Y += dy
	}
	for i := range d.Edges {
		for k := range d.Edges[i].Points {
			mv(&d.Edges[i].Points[k])
		}
	}
}

// splitStatements splits a line at semicolons outside quotes and brackets.
func splitStatements(l srcLine) []srcLine {
	if !strings.Contains(l.text, ";") {
		return []srcLine{l}
	}
	var out []srcLine
	depth, quote, start := 0, false, 0
	for i, r := range l.text {
		switch {
		case r == '"':
			quote = !quote
		case quote:
		case r == '[' || r == '(' || r == '{' || r == '|':
			if r == '|' {
				// |label| toggles like a quote.
				if depth > 0 {
					depth--
				} else {
					depth++
				}
				continue
			}
			depth++
		case r == ']' || r == ')' || r == '}':
			depth = max(0, depth-1)
		case r == ';' && depth == 0:
			if t := strings.TrimSpace(l.text[start:i]); t != "" {
				out = append(out, srcLine{l.n, t})
			}
			start = i + 1
		}
	}
	if t := strings.TrimSpace(l.text[start:]); t != "" {
		out = append(out, srcLine{l.n, t})
	}
	return out
}
