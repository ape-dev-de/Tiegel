package mdview

import (
	"strings"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// roleByName maps the syntax role names of markdown.Token to theme roles.
var roleByName = func() map[string]tokens.SyntaxRole {
	m := map[string]tokens.SyntaxRole{}
	for _, r := range tokens.SyntaxRoles {
		m[r.String()] = r
	}
	return m
}()

// newCodeRich builds the rich text of a code block: one span per line,
// split further at token boundaries so each piece carries its syntax role
// (painted with the editor's syntax colors).
func newCodeRich(code string, toks []markdown.Token) *richText {
	if len(toks) == 0 {
		return newRich(codeSpans(code), styleCode)
	}
	var spans []markdown.Span
	var roles []tokens.SyntaxRole
	emit := func(s string, r tokens.SyntaxRole) {
		if s == "" {
			return
		}
		spans = append(spans, markdown.Span{Text: strings.ReplaceAll(s, "\t", "    ")})
		roles = append(roles, r)
	}
	lineStart := 0
	ti := 0
	for lineStart <= len(code) {
		end := strings.IndexByte(code[lineStart:], '\n')
		last := end < 0
		if last {
			end = len(code)
		} else {
			end += lineStart
		}
		lineEnd := end
		if lineEnd > lineStart && code[lineEnd-1] == '\r' {
			lineEnd--
		}
		pos := lineStart
		n0 := len(spans)
		for ti < len(toks) && toks[ti].End <= lineStart {
			ti++
		}
		for k := ti; k < len(toks) && toks[k].Start < lineEnd; k++ {
			s, e := max(toks[k].Start, pos), min(toks[k].End, lineEnd)
			if e <= s {
				continue
			}
			emit(code[pos:s], tokens.RolePlain)
			emit(code[s:e], roleByName[toks[k].Role])
			pos = e
		}
		emit(code[pos:lineEnd], tokens.RolePlain)
		if len(spans) == n0 {
			// Empty line: keep it as an empty span so the break counts.
			spans = append(spans, markdown.Span{})
			roles = append(roles, tokens.RolePlain)
		}
		if !last {
			spans[len(spans)-1].Break = true
		}
		if last {
			break
		}
		lineStart = end + 1
	}
	rt := newRich(spans, styleCode)
	rt.roles = roles
	return rt
}
