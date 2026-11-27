package orchestrate

import (
	"bytes"
	"regexp"
	"strings"
)

// A small PHP scanner for the mechanical gates (D568). Tree-sitter needs
// cgo and the gates must run in every build, so the scanner works on a
// "code view" of the source: comments and the contents of strings are
// blanked (newlines kept, so offsets and lines stay valid), "#[" starts
// an attribute, not a comment. Functions are found in the code view and
// their bodies matched by braces. That is enough for the questions the
// gates ask (is there an assertion, is the body empty, what does a
// function return), not for a full parse.

// PHPFunc is a function or method of a PHP file.
type PHPFunc struct {
	Class string
	Name  string
	// Line and EndLine are 1-based (declaration and closing brace).
	Line, EndLine int
	// Body is the raw text between the braces, Code the same range of the
	// code view (comments and string contents blanked).
	Body, Code string
	// BodyStart is the byte offset of Body in the source.
	BodyStart int
	// Doc is the raw text between the previous declaration and the
	// function keyword (doc comment, attributes, modifiers).
	Doc string
	// Abstract has no body (interface, abstract).
	Abstract bool
}

// IsTest tells whether the function is a test method (test* name, @test
// annotation or #[Test] attribute).
func (f PHPFunc) IsTest() bool {
	return strings.HasPrefix(f.Name, "test") || strings.Contains(f.Doc, "@test") || strings.Contains(f.Doc, "#[Test") ||
		strings.Contains(f.Doc, "#[\\PHPUnit\\Framework\\Attributes\\Test")
}

// CodeView returns src with comments and string contents replaced by
// spaces (quotes and newlines are kept).
func CodeView(src []byte) []byte {
	out := bytes.Clone(src)
	blank := func(i, j int) {
		for k := i; k < j && k < len(out); k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/', c == '#' && !(i+1 < n && src[i+1] == '['):
			j := i
			for j < n && src[j] != '\n' && !(src[j] == '?' && j+1 < n && src[j+1] == '>') {
				j++
			}
			blank(i, j)
			i = j
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := bytes.Index(src[i+2:], []byte("*/"))
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			blank(i, end)
			i = end
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			blank(i+1, j)
			i = j + 1
		case c == '<' && bytes.HasPrefix(src[i:], []byte("<<<")):
			j := i + 3
			for j < n && (src[j] == ' ' || src[j] == '"' || src[j] == '\'') {
				j++
			}
			k := j
			for k < n && (isWord(src[k])) {
				k++
			}
			id := string(src[j:k])
			eol := bytes.IndexByte(src[k:], '\n')
			if id == "" || eol < 0 {
				i = k
				continue
			}
			start := k + eol + 1
			end := n
			for p := start; p < n; {
				line := p
				for line < n && (src[line] == ' ' || src[line] == '\t') {
					line++
				}
				if bytes.HasPrefix(src[line:], []byte(id)) && (line+len(id) >= n || !isWord(src[line+len(id)])) {
					end = line
					break
				}
				nl := bytes.IndexByte(src[p:], '\n')
				if nl < 0 {
					break
				}
				p += nl + 1
			}
			blank(start, end)
			i = end + len(id)
		default:
			i++
		}
	}
	return out
}

func isWord(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

var (
	classRE = regexp.MustCompile(`(?i)\b(?:class|trait|interface|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	funcRE  = regexp.MustCompile(`(?i)\bfunction\s*&?\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// matching returns the offset of the bracket closing the one at i.
func matching(code []byte, i int) int {
	open, close := code[i], byte(')')
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	}
	depth := 0
	for j := i; j < len(code); j++ {
		switch code[j] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

func lineAt(src []byte, off int) int { return bytes.Count(src[:min(off, len(src))], []byte("\n")) + 1 }

// ScanPHP returns the named functions and methods of a PHP file.
func ScanPHP(src []byte) []PHPFunc {
	code := CodeView(src)
	type span struct {
		name       string
		start, end int
	}
	var classes []span
	for _, m := range classRE.FindAllSubmatchIndex(code, -1) {
		br := bytes.IndexByte(code[m[1]:], '{')
		if br < 0 {
			continue
		}
		open := m[1] + br
		if semi := bytes.IndexByte(code[m[1]:open], ';'); semi >= 0 {
			continue
		}
		end := matching(code, open)
		if end < 0 {
			end = len(code)
		}
		classes = append(classes, span{string(code[m[2]:m[3]]), open, end})
	}
	var out []PHPFunc
	prevEnd := 0
	for _, m := range funcRE.FindAllSubmatchIndex(code, -1) {
		paren := m[1] - 1
		pe := matching(code, paren)
		if pe < 0 {
			continue
		}
		f := PHPFunc{Name: string(code[m[2]:m[3]]), Line: lineAt(src, m[0])}
		for _, c := range classes {
			if m[0] > c.start && m[0] < c.end {
				f.Class = c.name
			}
		}
		docFrom := prevEnd
		if k := bytes.LastIndexAny(code[:m[0]], "{};"); k+1 > docFrom {
			docFrom = k + 1
		}
		f.Doc = string(src[docFrom:m[0]])
		rest := code[pe+1:]
		br := bytes.IndexByte(rest, '{')
		semi := bytes.IndexByte(rest, ';')
		if br < 0 || semi >= 0 && semi < br {
			f.Abstract = true
			f.EndLine = f.Line
			out = append(out, f)
			continue
		}
		open := pe + 1 + br
		end := matching(code, open)
		if end < 0 {
			continue
		}
		f.Body, f.Code, f.BodyStart = string(src[open+1:end]), string(code[open+1:end]), open+1
		f.EndLine = lineAt(src, end)
		prevEnd = end
		out = append(out, f)
	}
	return out
}

// PHPCall is a call found in a function body.
type PHPCall struct {
	Name string
	// Args is the raw argument text, Line the 1-based line in the file.
	Args string
	Line int
}

// callsIn returns the calls of the functions whose name matches re in a
// function's body (offsets relative to the file).
func callsIn(f PHPFunc, src []byte, re *regexp.Regexp) []PHPCall {
	var out []PHPCall
	code := []byte(f.Code)
	for _, m := range re.FindAllSubmatchIndex(code, -1) {
		open := m[1] - 1
		if open < 0 || code[open] != '(' {
			continue
		}
		end := matching(code, open)
		if end < 0 {
			continue
		}
		out = append(out, PHPCall{Name: string(code[m[2]:m[3]]), Args: strings.TrimSpace(f.Body[open+1 : end]), Line: lineAt(src, f.BodyStart+m[0])})
	}
	return out
}

// splitArgs splits a raw argument list at top-level commas.
func splitArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	code := CodeView([]byte("<?php " + s))[6:]
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out
}
