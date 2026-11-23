package markdown

import (
	"strconv"
	"strings"
)

// PlainText renders d as readable plain text for terminals: headings
// underlined, lists with bullets or numbers, quotes with "> ", code
// indented, tables with aligned columns, links as "text <dest>" and
// images as "[image: alt] <src>". width > 0 wraps paragraphs.
func (d *Doc) PlainText(width int) string {
	var b strings.Builder
	p := plain{b: &b, width: width}
	p.blocks(d.Blocks, "", "", false)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

type plain struct {
	b     *strings.Builder
	width int
}

// blocks writes bs; first prefixes the first line, rest the others.
// Blocks are separated by a blank line unless tight (list items).
func (p plain) blocks(bs []Block, first, rest string, tight bool) {
	for i, bl := range bs {
		pre := rest
		if i == 0 {
			pre = first
		} else if !tight {
			p.b.WriteString(strings.TrimRight(rest, " ") + "\n")
		}
		p.block(bl, pre, rest)
	}
}

func (p plain) lines(text, first, rest string) {
	for i, l := range p.wrap(text, len(first)) {
		pre := rest
		if i == 0 {
			pre = first
		}
		p.b.WriteString(strings.TrimRight(pre+l, " ") + "\n")
	}
}

// wrap splits text at newlines and, with a width, at word boundaries.
func (p plain) wrap(text string, indent int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if p.width <= 0 || len(para)+indent <= p.width {
			out = append(out, para)
			continue
		}
		line := ""
		for _, w := range strings.Fields(para) {
			if line != "" && len(line)+1+len(w)+indent > p.width {
				out = append(out, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += w
		}
		out = append(out, line)
	}
	return out
}

func (p plain) block(bl Block, first, rest string) {
	switch bl.Kind {
	case Heading:
		t := inlineText(bl.Spans)
		p.b.WriteString(first + strings.Repeat("#", bl.Level) + " " + t + "\n")
	case Paragraph:
		p.lines(inlineText(bl.Spans), first, rest)
	case CodeBlock, HTML:
		for i, l := range strings.Split(bl.Code, "\n") {
			pre := rest
			if i == 0 {
				pre = first
			}
			p.b.WriteString(strings.TrimRight(pre+"    "+l, " ") + "\n")
		}
	case Quote:
		p.blocks(bl.Children, first+"> ", rest+"> ", false)
	case List:
		for i, it := range bl.Children {
			mark := "- "
			if bl.Ordered {
				mark = strconv.Itoa(bl.Start+i) + ". "
			}
			indent := strings.Repeat(" ", len(mark))
			if it.Task {
				if it.Checked {
					mark += "[x] "
				} else {
					mark += "[ ] "
				}
			}
			pre := rest
			if i == 0 {
				pre = first
			} else if !bl.Tight {
				p.b.WriteString(strings.TrimRight(rest, " ") + "\n")
			}
			p.blocks(it.Children, pre+mark, rest+indent, bl.Tight)
			if len(it.Children) == 0 {
				p.b.WriteString(strings.TrimRight(pre+mark, " ") + "\n")
			}
		}
	case Table:
		p.table(bl, first, rest)
	case Rule:
		p.b.WriteString(first + "----------\n")
	}
}

func (p plain) table(bl Block, first, rest string) {
	cells := make([][]string, len(bl.Rows))
	var w []int
	for r, row := range bl.Rows {
		for i, c := range row {
			t := inlineText(c.Spans)
			cells[r] = append(cells[r], t)
			if i >= len(w) {
				w = append(w, 0)
			}
			w[i] = max(w[i], len([]rune(t)))
		}
	}
	row := func(r []string) string {
		var s []string
		for i := range w {
			t := ""
			if i < len(r) {
				t = r[i]
			}
			pad := w[i] - len([]rune(t))
			if i < len(bl.Align) && bl.Align[i] == AlignRight {
				t = strings.Repeat(" ", pad) + t
			} else {
				t += strings.Repeat(" ", pad)
			}
			s = append(s, t)
		}
		return strings.TrimRight("| "+strings.Join(s, " | ")+" |", " ")
	}
	for i, r := range cells {
		pre := rest
		if i == 0 {
			pre = first
		}
		p.b.WriteString(pre + row(r) + "\n")
		if i == 0 {
			var sep []string
			for _, n := range w {
				sep = append(sep, strings.Repeat("-", max(n, 3)))
			}
			p.b.WriteString(rest + "| " + strings.Join(sep, " | ") + " |\n")
		}
	}
}

// inlineText renders spans with links and images spelled out.
func inlineText(sp []Span) string {
	var b strings.Builder
	for i, s := range sp {
		switch {
		case s.Image:
			b.WriteString("[image: " + s.Text + "] <" + s.Link + ">")
		case s.Link != "" && (i+1 == len(sp) || sp[i+1].Link != s.Link):
			b.WriteString(s.Text)
			if s.Text != s.Link && "mailto:"+s.Text != s.Link {
				b.WriteString(" <" + s.Link + ">")
			}
		case s.Style&Code != 0:
			b.WriteString("`" + s.Text + "`")
		default:
			b.WriteString(s.Text)
		}
		if s.Break {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
