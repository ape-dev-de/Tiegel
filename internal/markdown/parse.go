package markdown

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// parser is goldmark with the GitHub extensions; goldmark parsers are
// safe for concurrent use.
var parser = goldmark.New(goldmark.WithExtensions(
	extension.Table, extension.Strikethrough, extension.TaskList, extension.Linkify,
)).Parser()

// Parse parses Markdown source into a document. It never fails: invalid
// Markdown is text.
func Parse(src []byte) *Doc {
	root := parser.Parse(text.NewReader(src))
	c := conv{src: src, lines: lineStarts(src), anchors: map[string]int{}}
	return &Doc{Blocks: c.blocks(root)}
}

type conv struct {
	src     []byte
	lines   []int
	anchors map[string]int
}

// lineStarts returns the byte offsets at which lines start.
func lineStarts(src []byte) []int {
	out := []int{0}
	for i, b := range src {
		if b == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

// lineOf returns the 1-based line of byte offset off.
func (c *conv) lineOf(off int) int {
	lo, hi := 0, len(c.lines)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if c.lines[mid] <= off {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + 1
}

// line returns the first source line of n (or of its first descendant
// that carries lines); 0 when unknown.
func (c *conv) line(n ast.Node) int {
	if n.Type() == ast.TypeBlock {
		if l := n.Lines(); l != nil && l.Len() > 0 {
			return c.lineOf(l.At(0).Start)
		}
	} else if t, ok := n.(*ast.Text); ok {
		return c.lineOf(t.Segment.Start)
	}
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if l := c.line(ch); l > 0 {
			return l
		}
	}
	return 0
}

// endLine returns the last source line of n's content (0 = unknown); a
// setext heading's underline and a closing code fence count.
func (c *conv) endLine(n ast.Node) int {
	end := 0
	if n.Type() == ast.TypeBlock {
		if l := n.Lines(); l != nil && l.Len() > 0 {
			end = c.lineOf(max(l.At(l.Len()-1).Stop-1, 0))
		}
	}
	for ch := n.LastChild(); ch != nil && end == 0; ch = ch.PreviousSibling() {
		end = c.endLine(ch)
	}
	switch n.(type) {
	case *ast.Heading:
		// A setext heading's underline.
		if end > 0 && !bytes.HasPrefix(bytes.TrimSpace(c.lineText(end)), []byte("#")) {
			end++
		}
	case *ast.FencedCodeBlock:
		// The closing fence (the opening one precedes the lines).
		if end > 0 {
			end++
		}
	}
	return end
}

func (c *conv) blocks(parent ast.Node) []Block {
	var out []Block
	prev := c.line(parent)
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		if b, ok := c.block(n); ok {
			if b.Kind == Rule {
				// goldmark keeps no position of thematic breaks.
				b.Line = c.ruleLine(prev)
			}
			if e := c.endLine(n); e > 0 {
				prev = e
			} else if b.Line > 0 {
				prev = b.Line
			}
			out = append(out, b)
		}
	}
	return out
}

// ruleLine finds the first thematic break line after line from (1-based;
// 0 = from the start).
func (c *conv) ruleLine(from int) int {
	for l := max(from, 0) + 1; l <= len(c.lines); l++ {
		if isRule(c.lineText(l)) {
			return l
		}
	}
	return from
}

// lineText returns the 1-based source line l.
func (c *conv) lineText(l int) []byte {
	if l < 1 || l > len(c.lines) {
		return nil
	}
	end := len(c.src)
	if l < len(c.lines) {
		end = c.lines[l]
	}
	return c.src[c.lines[l-1]:end]
}

// isRule reports whether line is a thematic break (3+ of one of -*_).
func isRule(line []byte) bool {
	var mark byte
	n := 0
	for _, b := range bytes.TrimSpace(line) {
		switch {
		case b == ' ' || b == '\t':
		case (b == '-' || b == '*' || b == '_') && (mark == 0 || mark == b):
			mark = b
			n++
		default:
			return false
		}
	}
	return n >= 3
}

func (c *conv) block(n ast.Node) (Block, bool) {
	b := Block{Line: c.line(n)}
	switch n := n.(type) {
	case *ast.Heading:
		b.Kind, b.Level, b.Spans = Heading, n.Level, c.inlines(n)
		b.Anchor = c.anchor(spansText(b.Spans))
	case *ast.Paragraph, *ast.TextBlock:
		b.Kind, b.Spans = Paragraph, c.inlines(n)
		if len(b.Spans) == 0 {
			return b, false
		}
	case *ast.FencedCodeBlock:
		b.Kind, b.Code = CodeBlock, c.code(n)
		b.Lang = string(n.Language(c.src))
		if b.Line > 1 {
			// The fence line precedes the first code line.
			b.Line--
		}
	case *ast.CodeBlock:
		b.Kind, b.Code = CodeBlock, c.code(n)
	case *ast.HTMLBlock:
		b.Kind, b.Code = HTML, c.code(n)
		if n.HasClosure() {
			b.Code += "\n" + strings.TrimRight(string(n.ClosureLine.Value(c.src)), "\n")
		}
	case *ast.Blockquote:
		b.Kind, b.Children = Quote, c.blocks(n)
	case *ast.List:
		b.Kind, b.Ordered, b.Tight = List, n.IsOrdered(), n.IsTight
		if b.Ordered {
			b.Start = n.Start
		}
		for it := n.FirstChild(); it != nil; it = it.NextSibling() {
			b.Children = append(b.Children, c.item(it))
		}
	case *ast.ThematicBreak:
		b.Kind = Rule
	case *east.Table:
		b.Kind = Table
		for _, a := range n.Alignments {
			b.Align = append(b.Align, align(a))
		}
		for r := n.FirstChild(); r != nil; r = r.NextSibling() {
			var row []Cell
			for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
				row = append(row, Cell{Spans: c.inlines(cell)})
			}
			b.Rows = append(b.Rows, row)
		}
	default:
		// Unknown block (e.g. link reference definitions): its children.
		kids := c.blocks(n)
		if len(kids) == 0 {
			return b, false
		}
		return Block{Kind: Quote, Line: b.Line, Children: kids}, true
	}
	return b, true
}

func (c *conv) item(n ast.Node) Block {
	b := Block{Kind: ListItem, Line: c.line(n), Children: c.blocks(n)}
	if len(b.Children) > 0 && b.Children[0].Kind == Paragraph {
		// The task checkbox is the first inline of the first paragraph.
		if p := n.FirstChild(); p != nil {
			if cb, ok := p.FirstChild().(*east.TaskCheckBox); ok {
				b.Task, b.Checked = true, cb.IsChecked
				sp := b.Children[0].Spans
				if len(sp) > 0 {
					sp[0].Text = strings.TrimLeft(sp[0].Text, " ")
				}
			}
		}
	}
	return b
}

func align(a east.Alignment) Align {
	switch a {
	case east.AlignLeft:
		return AlignLeft
	case east.AlignCenter:
		return AlignCenter
	case east.AlignRight:
		return AlignRight
	}
	return AlignNone
}

// code joins the lines of a code or HTML block without the final newline.
func (c *conv) code(n ast.Node) string {
	var buf bytes.Buffer
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		buf.Write(seg.Value(c.src))
	}
	return strings.TrimRight(buf.String(), "\n")
}

// inlines converts the inline children of n into spans.
func (c *conv) inlines(n ast.Node) []Span {
	var out []Span
	c.walkInline(n, 0, "", &out)
	return mergeSpans(out)
}

func (c *conv) walkInline(n ast.Node, st Style, link string, out *[]Span) {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		switch ch := ch.(type) {
		case *ast.Text:
			s := Span{Text: string(ch.Segment.Value(c.src)), Style: st, Link: link}
			if ch.HardLineBreak() {
				s.Break = true
			} else if ch.SoftLineBreak() {
				s.Text += " "
			}
			*out = append(*out, s)
		case *ast.String:
			*out = append(*out, Span{Text: string(ch.Value), Style: st, Link: link})
		case *ast.Emphasis:
			s := st | Italic
			if ch.Level >= 2 {
				s = st | Bold
			}
			c.walkInline(ch, s, link, out)
		case *east.Strikethrough:
			c.walkInline(ch, st|Strike, link, out)
		case *ast.CodeSpan:
			var buf strings.Builder
			for t := ch.FirstChild(); t != nil; t = t.NextSibling() {
				switch t := t.(type) {
				case *ast.Text:
					buf.Write(t.Segment.Value(c.src))
				case *ast.String:
					buf.Write(t.Value)
				}
			}
			*out = append(*out, Span{Text: buf.String(), Style: st | Code, Link: link})
		case *ast.Link:
			c.walkInline(ch, st, string(ch.Destination), out)
		case *ast.AutoLink:
			dest := string(ch.URL(c.src))
			label := string(ch.Label(c.src))
			if ch.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(dest), "mailto:") {
				dest = "mailto:" + dest
			}
			*out = append(*out, Span{Text: label, Style: st, Link: dest})
		case *ast.Image:
			var alt []Span
			c.walkInline(ch, 0, "", &alt)
			*out = append(*out, Span{Text: spansText(alt), Image: true, Link: string(ch.Destination)})
		case *ast.RawHTML:
			var buf strings.Builder
			for i := 0; i < ch.Segments.Len(); i++ {
				seg := ch.Segments.At(i)
				buf.Write(seg.Value(c.src))
			}
			*out = append(*out, Span{Text: buf.String(), Style: st | Code, Link: link})
		case *east.TaskCheckBox:
			// Reported on the list item.
		default:
			c.walkInline(ch, st, link, out)
		}
	}
}

// mergeSpans joins neighbours with equal style and link and drops empty
// spans; a trailing soft break space is trimmed.
func mergeSpans(in []Span) []Span {
	var out []Span
	for _, s := range in {
		if s.Text == "" && !s.Break {
			continue
		}
		if n := len(out); n > 0 {
			p := &out[n-1]
			if !p.Break && !p.Image && !s.Image && p.Style == s.Style && p.Link == s.Link {
				p.Text += s.Text
				p.Break = s.Break
				continue
			}
		}
		out = append(out, s)
	}
	if n := len(out); n > 0 && !out[n-1].Break {
		out[n-1].Text = strings.TrimRight(out[n-1].Text, " ")
	}
	return out
}

// spansText returns the text of spans.
func spansText(sp []Span) string {
	var b strings.Builder
	for _, s := range sp {
		b.WriteString(s.Text)
		if s.Break {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Slug returns the GitHub-style anchor of a heading text: lower case,
// punctuation removed, spaces as hyphens.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// anchor returns the unique anchor of a heading.
func (c *conv) anchor(title string) string {
	s := Slug(title)
	n := c.anchors[s]
	c.anchors[s] = n + 1
	if n > 0 {
		return s + "-" + strconv.Itoa(n)
	}
	return s
}

// Outline returns the headings of d in document order.
func (d *Doc) Outline() []OutlineEntry {
	var out []OutlineEntry
	var walk func([]Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			if b.Kind == Heading {
				out = append(out, OutlineEntry{Level: b.Level, Text: spansText(b.Spans), Anchor: b.Anchor, Line: b.Line})
			}
			walk(b.Children)
		}
	}
	walk(d.Blocks)
	return out
}
