package highlight

import (
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
)

// PHPDemo is a small line-based PHP highlighter for tests, the component
// catalog and as a fallback until Tree-sitter results are available. It
// knows keywords, variables, strings, numbers, comments, function calls,
// types, constants, properties, operators and the <?php tags. Constructs
// spanning several lines (block comments, heredocs) are only recognised on
// their own line.
var PHPDemo Highlighter = Func(phpSpans)

var phpKeywords = setOf("abstract", "and", "as", "break", "case", "catch", "class", "clone", "const",
	"continue", "declare", "default", "do", "echo", "else", "elseif", "enum", "extends", "final",
	"finally", "fn", "for", "foreach", "function", "global", "goto", "if", "implements", "include",
	"include_once", "instanceof", "insteadof", "interface", "match", "namespace", "new", "or",
	"print", "private", "protected", "public", "readonly", "require", "require_once", "return",
	"static", "switch", "throw", "trait", "try", "use", "var", "while", "xor", "yield", "self",
	"parent", "array", "list", "isset", "unset", "empty")

var phpTypes = setOf("int", "float", "string", "bool", "void", "mixed", "never", "object",
	"iterable", "callable", "null", "true", "false")

var phpConstants = setOf("true", "false", "null", "TRUE", "FALSE", "NULL")

// typeAfter marks keywords followed by a type name.
var typeAfter = setOf("new", "class", "extends", "implements", "interface", "trait", "enum",
	"instanceof", "use", "namespace", "catch")

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func phpSpans(s buffer.Snapshot, start, end int) []Span {
	first := s.OffsetToPos(start).Line
	last := s.OffsetToPos(end).Line
	var out []Span
	var line []byte
	for l := first; l <= last; l++ {
		line = s.AppendLine(line[:0], l)
		base := s.LineStart(l)
		for _, sp := range scanPHPLine(line) {
			sp.Start += base
			sp.End += base
			out = append(out, sp)
		}
	}
	return Normalize(out, start, end)
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isIdent(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isUpperIdent(w string) bool {
	hasLetter := false
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c >= 'a' && c <= 'z' {
			return false
		}
		if c >= 'A' && c <= 'Z' {
			hasLetter = true
		}
	}
	return hasLetter && len(w) > 1
}

// scanPHPLine returns spans relative to the line start.
func scanPHPLine(line []byte) []Span {
	var out []Span
	add := func(a, b int, r Role) { out = append(out, Span{a, b, r}) }
	prevWord := ""
	n := len(line)
	for i := 0; i < n; {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
			continue
		case c == '<' && hasPrefixAt(line, i, "<?php"):
			add(i, i+5, Tag)
			i += 5
		case c == '<' && hasPrefixAt(line, i, "<?="):
			add(i, i+3, Tag)
			i += 3
		case c == '?' && hasPrefixAt(line, i, "?>"):
			add(i, i+2, Tag)
			i += 2
		case c == '/' && hasPrefixAt(line, i, "//"), c == '#' && !hasPrefixAt(line, i, "#["):
			add(i, n, Comment)
			i = n
		case c == '/' && hasPrefixAt(line, i, "/*"):
			j := indexFrom(line, i+2, "*/")
			if j < 0 {
				j = n
			} else {
				j += 2
			}
			add(i, j, Comment)
			i = j
		case c == '*' && i == firstNonBlank(line) && (i+1 >= n || line[i+1] == ' ' || line[i+1] == '/'):
			// Continuation line of a doc block.
			add(i, n, Comment)
			i = n
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < n && line[j] != c {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, n)
			add(i, j, String)
			i = j
		case c == '$' && i+1 < n && isIdentStart(line[i+1]):
			j := i + 1
			for j < n && isIdent(line[j]) {
				j++
			}
			add(i, j, Variable)
			i = j
		case isDigit(c) || c == '.' && i+1 < n && isDigit(line[i+1]):
			j := i + 1
			for j < n && (isIdent(line[j]) || line[j] == '.') {
				j++
			}
			add(i, j, Number)
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < n && (isIdent(line[j]) || line[j] == '\\') {
				j++
			}
			w := string(line[i:j])
			k := j
			for k < n && line[k] == ' ' {
				k++
			}
			role := Plain
			switch {
			case hasPrefixAt(line, i-2, "->"):
				role = Property
				if k < n && line[k] == '(' {
					role = Function
				}
			case phpConstants[w]:
				role = Constant
			case phpKeywords[w]:
				role = Keyword
			case typeAfter[prevWord]:
				role = Type
			case k < n && line[k] == '(':
				role = Function
			case phpTypes[w]:
				role = Type
			case hasPrefixAt(line, k, "::"):
				role = Type
			case isUpperIdent(w):
				role = Constant
			case w[0] >= 'A' && w[0] <= 'Z':
				role = Type
			}
			if role != Plain {
				add(i, j, role)
			}
			prevWord = w
			i = j
			continue
		case c == '(' || c == ')' || c == '[' || c == ']' || c == '{' || c == '}' || c == ';' || c == ',':
			add(i, i+1, Punctuation)
			i++
		case c == '#': // "#[" attribute
			add(i, i+2, Attribute)
			i += 2
		default:
			j := i + 1
			for j < n && isOperator(line[j]) {
				j++
			}
			if isOperator(c) {
				add(i, j, Operator)
				i = j
			} else {
				i++
			}
		}
		prevWord = ""
	}
	return out
}

func isOperator(c byte) bool {
	switch c {
	case '=', '+', '-', '*', '/', '%', '<', '>', '!', '&', '|', '^', '~', '?', ':', '.', '@':
		return true
	}
	return false
}

func hasPrefixAt(b []byte, i int, p string) bool {
	return i >= 0 && i+len(p) <= len(b) && string(b[i:i+len(p)]) == p
}

func indexFrom(b []byte, i int, p string) int {
	for j := i; j+len(p) <= len(b); j++ {
		if string(b[j:j+len(p)]) == p {
			return j
		}
	}
	return -1
}

func firstNonBlank(b []byte) int {
	for i, c := range b {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return len(b)
}

// BraceFolds returns fold ranges for every multi-line {…}, […] and (…)
// pair and every multi-line /* … */ comment, ignoring brackets inside
// strings and line comments. It is a simple fallback until Tree-sitter
// supplies folding ranges; it scans the whole document.
func BraceFolds(s buffer.Snapshot) []fold.Range {
	type open struct {
		line int
		ch   byte
	}
	var stack []open
	var out []fold.Range
	inBlock := -1 // line where the current block comment started
	var line []byte
	for l := 0; l < s.LineCount(); l++ {
		line = s.AppendLine(line[:0], l)
		var quote byte
	scan:
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case inBlock >= 0:
				if hasPrefixAt(line, i, "*/") {
					if l > inBlock {
						out = append(out, fold.Range{StartLine: inBlock, EndLine: l})
					}
					inBlock = -1
					i++
				}
			case quote != 0:
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case hasPrefixAt(line, i, "/*"):
				inBlock = l
				i++
			case hasPrefixAt(line, i, "//"), c == '#' && !hasPrefixAt(line, i, "#["):
				break scan
			case c == '{' || c == '[' || c == '(':
				stack = append(stack, open{l, c})
			case c == '}' || c == ']' || c == ')':
				if n := len(stack); n > 0 {
					o := stack[n-1]
					stack = stack[:n-1]
					if l > o.line {
						// The closing line stays visible when it starts with
						// the closer, so "}" remains on screen.
						endLine := l
						if firstNonBlank(line) == i {
							endLine = l - 1
						}
						if endLine > o.line {
							out = append(out, fold.Range{StartLine: o.line, EndLine: endLine})
						}
					}
				}
			}
		}
	}
	return out
}
