// Package snippet implements the template syntax of live templates and
// the tab-stop session of an expanded template (AP17.3).
//
// Template syntax: $NAME$ is a variable (letters, digits, underscore; not
// starting with a digit), $$ is a literal dollar sign, any other $ is
// literal too. Two variables are predefined: $END$ is the final caret
// position and $SELECTION$ the text a template surrounds. Every other
// variable becomes a tab stop; all occurrences of the same variable are
// edited together (linked editing).
//
// The package knows nothing about languages or editors: Parse and Expand
// work on strings, Session tracks byte ranges through buffer edits. It
// does not import Gio.
package snippet

import (
	"fmt"
	"strings"
)

// Predefined variables.
const (
	VarEnd       = "END"
	VarSelection = "SELECTION"
)

// Part is a piece of a parsed template: literal text or a variable.
type Part struct {
	Text string // literal text (Var == "")
	Var  string // variable name
}

// Template is a parsed template body.
type Template struct {
	Parts []Part
	// Vars are the variable names in order of first occurrence, without
	// END and SELECTION.
	Vars []string
	// HasEnd and HasSelection report the predefined variables.
	HasEnd, HasSelection bool
}

func isIdentByte(c byte, first bool) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (!first && c >= '0' && c <= '9')
}

// Parse parses a template body. It never fails on stray dollar signs (they
// stay literal); it only rejects an empty body.
func Parse(body string) (Template, error) {
	if body == "" {
		return Template{}, fmt.Errorf("snippet: empty template")
	}
	var t Template
	var lit strings.Builder
	seen := map[string]bool{}
	flush := func() {
		if lit.Len() > 0 {
			t.Parts = append(t.Parts, Part{Text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(body); {
		c := body[i]
		if c != '$' {
			lit.WriteByte(c)
			i++
			continue
		}
		if i+1 < len(body) && body[i+1] == '$' {
			lit.WriteByte('$')
			i += 2
			continue
		}
		j := i + 1
		for j < len(body) && isIdentByte(body[j], j == i+1) {
			j++
		}
		if j == i+1 || j >= len(body) || body[j] != '$' {
			lit.WriteByte('$')
			i++
			continue
		}
		name := body[i+1 : j]
		flush()
		t.Parts = append(t.Parts, Part{Var: name})
		switch name {
		case VarEnd:
			t.HasEnd = true
		case VarSelection:
			t.HasSelection = true
		default:
			if !seen[name] {
				seen[name] = true
				t.Vars = append(t.Vars, name)
			}
		}
		i = j + 1
	}
	flush()
	return t, nil
}

// Range is a byte range [Start, End).
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Field is a tab stop: one variable with the ranges of all its
// occurrences.
type Field struct {
	Name   string  `json:"name"`
	Ranges []Range `json:"ranges"`
}

// Expansion is an expanded template.
type Expansion struct {
	Text string
	// Fields are the tab stops in order (offsets relative to Text).
	Fields []Field
	// End is the final caret offset in Text.
	End int
}

// ExpandOptions control Expand.
type ExpandOptions struct {
	// Values are the initial texts of the variables (missing: the name
	// itself is shown so the stop is visible).
	Values map[string]string
	// Skip lists variables that get no tab stop (their value is final).
	Skip map[string]bool
	// Selection replaces $SELECTION$; its lines after the first get the
	// indentation of the template line.
	Selection string
	// Indent is prepended to every line after the first (the indentation
	// of the line the template is inserted in).
	Indent string
	// Tab replaces every tab character of the body (one indentation
	// unit, e.g. four spaces); empty keeps tabs.
	Tab string
}

// Expand renders t. Without $END$ the caret ends after the text.
func Expand(t Template, o ExpandOptions) Expansion {
	var b strings.Builder
	idx := map[string]int{}
	var fields []Field
	end := -1
	write := func(s string) {
		if o.Tab != "" {
			s = strings.ReplaceAll(s, "\t", o.Tab)
		}
		if o.Indent != "" {
			s = strings.ReplaceAll(s, "\n", "\n"+o.Indent)
		}
		b.WriteString(s)
	}
	for _, p := range t.Parts {
		switch p.Var {
		case "":
			write(p.Text)
		case VarEnd:
			end = b.Len()
		case VarSelection:
			// Lines after the first get the indentation of the line
			// $SELECTION$ stands in (relative indentation is kept).
			out := b.String()
			line := out[strings.LastIndexByte(out, '\n')+1:]
			ind := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			b.WriteString(strings.ReplaceAll(o.Selection, "\n", "\n"+ind))
		default:
			v, ok := o.Values[p.Var]
			if !ok {
				v = p.Var
			}
			start := b.Len()
			b.WriteString(v)
			if o.Skip[p.Var] {
				continue
			}
			i, ok := idx[p.Var]
			if !ok {
				i = len(fields)
				idx[p.Var] = i
				fields = append(fields, Field{Name: p.Var})
			}
			fields[i].Ranges = append(fields[i].Ranges, Range{Start: start, End: b.Len()})
		}
	}
	if end < 0 {
		end = b.Len()
	}
	return Expansion{Text: b.String(), Fields: fields, End: end}
}

// Shift returns e with all offsets moved by delta (e.g. to document
// offsets after inserting at delta).
func (e Expansion) Shift(delta int) Expansion {
	out := Expansion{Text: e.Text, End: e.End + delta, Fields: make([]Field, len(e.Fields))}
	for i, f := range e.Fields {
		rs := make([]Range, len(f.Ranges))
		for j, r := range f.Ranges {
			rs[j] = Range{Start: r.Start + delta, End: r.End + delta}
		}
		out.Fields[i] = Field{Name: f.Name, Ranges: rs}
	}
	return out
}
