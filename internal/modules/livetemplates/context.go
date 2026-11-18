package livetemplates

import (
	"context"
	"regexp"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// Detected contexts of a position (besides the template contexts html,
// php-comment and other).
const (
	posStatement  = "php-statement"
	posMember     = "php-member"
	posExpression = "php-expression"
	posComment    = "php-comment"
	posString     = "php-string"
	posHTML       = "html"
	posOther      = "other"
	// posUnknown: PHP without a syntax tree (build without cgo); every
	// PHP context applies.
	posUnknown = "php"
)

// applies reports whether template t may be expanded at a position of
// context pos.
func applies(t Template, pos string) bool {
	for _, c := range t.Contexts {
		switch c {
		case CtxAny:
			return true
		case CtxOther, CtxHTML, CtxPHPComment:
			if pos == c {
				return true
			}
		case CtxPHP:
			if pos == posStatement || pos == posMember || pos == posExpression || pos == posUnknown {
				return true
			}
		case CtxPHPStatement, CtxPHPMember:
			if pos == c || pos == posUnknown {
				return true
			}
		case CtxPHPExpression:
			if pos == posStatement || pos == posExpression || pos == posUnknown {
				return true
			}
		}
	}
	return false
}

// analysis is what the syntax tree tells about a position.
type analysis struct {
	context string
	pos     position
}

var (
	classNameRe = regexp.MustCompile(`\b(?:class|interface|trait|enum)\s+([A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)`)
	funcNameRe  = regexp.MustCompile(`\bfunction\s*&?\s*([A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)`)
	namespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff\\]*)\s*[;{]`)
)

// analyze determines the context at offset off of text (path decides the
// language) and the names around it; wordStart is where the abbreviation
// (or the inserted template) starts.
func analyze(ctx context.Context, path string, text []byte, off, wordStart int) analysis {
	var a analysis
	a.pos.file = path
	a.pos.line = 1
	for _, c := range text[:off] {
		if c == '\n' {
			a.pos.line++
		}
	}
	if syntax.DetectLanguage(path, head(text)) != syntax.LangPHP {
		a.context = posOther
		return a
	}
	for _, m := range namespaceRe.FindAllSubmatchIndex(text[:off], -1) {
		a.pos.namespace = string(text[m[2]:m[3]])
	}
	doc, err := syntax.NewDocument(ctx, syntax.LangPHP, text)
	if err != nil {
		a.context = posUnknown
		return a
	}
	defer doc.Close()
	nodes := doc.NodesAt(off)
	a.context = classify(nodes, wordStart)
	for _, n := range nodes {
		src := text[n.Start:n.End]
		switch n.Kind {
		case "method_declaration":
			if a.pos.method == "" {
				if m := funcNameRe.FindSubmatch(src); m != nil {
					a.pos.method = string(m[1])
				}
			}
		case "function_definition":
			if a.pos.function == "" {
				if m := funcNameRe.FindSubmatch(src); m != nil {
					a.pos.function = string(m[1])
				}
			}
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			if a.pos.class == "" {
				if m := classNameRe.FindSubmatch(src); m != nil {
					a.pos.class = string(m[1])
				}
			}
		}
	}
	if a.pos.class != "" {
		a.pos.classFQN = a.pos.class
		if a.pos.namespace != "" {
			a.pos.classFQN = a.pos.namespace + `\` + a.pos.class
		}
	}
	return a
}

// classify maps the nodes at a position (innermost first) to a context.
func classify(nodes []syntax.Node, wordStart int) string {
	if nodes == nil {
		return posUnknown
	}
	for _, n := range nodes {
		if !n.Named {
			continue // a keyword the abbreviation spells ("for", "if", …)
		}
		switch n.Kind {
		case "name", "ERROR", "qualified_name", "namespace_name":
			continue
		case "text", "text_interpolation":
			return posHTML
		case "comment":
			return posComment
		case "string", "string_content", "string_value", "encapsed_string", "heredoc", "heredoc_body", "nowdoc", "nowdoc_body", "shell_command_expression":
			return posString
		case "expression_statement":
			if n.Start == wordStart {
				return posStatement
			}
			return posExpression
		case "compound_statement", "program", "php_tag", "case_statement", "default_statement", "colon_block":
			return posStatement
		case "declaration_list", "enum_declaration_list":
			return posMember
		default:
			return posExpression
		}
	}
	return posStatement
}

func head(b []byte) []byte {
	if len(b) > 512 {
		return b[:512]
	}
	return b
}
