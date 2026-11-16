// Package highlight defines the interface between syntax highlighters
// (Tree-sitter in internal/syntax, LSP semantic tokens, simple demo
// highlighters) and the editor widget: a Highlighter returns styled Spans
// with a Role for a byte range of a buffer snapshot. The package has no UI
// dependency; the widget maps roles to theme colors (tokens.SyntaxRole has
// the same names in the same order).
package highlight

import (
	"slices"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Role is a highlighting role.
type Role uint8

// Highlighting roles. The order and names match tokens.SyntaxRole.
const (
	Plain Role = iota
	Keyword
	String
	Number
	Comment
	Variable
	Function
	Type
	Constant
	Operator
	Punctuation
	Tag
	Attribute
	Property
	numRoles
)

var roleNames = [numRoles]string{"plain", "keyword", "string", "number", "comment", "variable",
	"function", "type", "constant", "operator", "punctuation", "tag", "attribute", "property"}

// String returns the lower-case role name.
func (r Role) String() string {
	if r >= numRoles {
		return "unknown"
	}
	return roleNames[r]
}

// RoleByName returns the role called name ("keyword", …); unknown names
// yield Plain and false.
func RoleByName(name string) (Role, bool) {
	for i, n := range roleNames {
		if n == name {
			return Role(i), true
		}
	}
	return Plain, false
}

// Roles returns all roles including Plain, in declaration order.
func Roles() []Role {
	out := make([]Role, numRoles)
	for i := range out {
		out[i] = Role(i)
	}
	return out
}

// Span styles the document bytes [Start, End) with Role.
type Span struct {
	Start, End int
	Role       Role
}

// Highlighter produces spans for a byte range of a snapshot.
//
// Spans must be sorted by Start and must not overlap; they may extend
// beyond [start, end) (the editor clips them). Bytes not covered by any
// span are Plain. Implementations are called on the UI goroutine for the
// visible lines of every frame and should answer from a cache (e.g. a
// parse tree for the snapshot's version) instead of parsing on demand.
type Highlighter interface {
	Spans(s buffer.Snapshot, start, end int) []Span
}

// Func adapts a function to Highlighter.
type Func func(s buffer.Snapshot, start, end int) []Span

// Spans calls f.
func (f Func) Spans(s buffer.Snapshot, start, end int) []Span { return f(s, start, end) }

// Normalize sorts spans, clips them to [start, end), drops empty ones and
// resolves overlaps in favour of the earlier span. It returns a new slice.
func Normalize(spans []Span, start, end int) []Span {
	out := make([]Span, 0, len(spans))
	for _, sp := range spans {
		sp.Start, sp.End = max(sp.Start, start), min(sp.End, end)
		if sp.End > sp.Start {
			out = append(out, sp)
		}
	}
	slices.SortStableFunc(out, func(a, b Span) int { return a.Start - b.Start })
	res := out[:0]
	for _, sp := range out {
		if n := len(res); n > 0 && sp.Start < res[n-1].End {
			if sp.End <= res[n-1].End {
				continue
			}
			sp.Start = res[n-1].End
		}
		res = append(res, sp)
	}
	return res
}
