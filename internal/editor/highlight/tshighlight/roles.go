package tshighlight

import (
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// roles maps syntax roles to editor highlight roles. Both lists have the
// same names; the table keeps them decoupled should either grow.
var roles = map[syntax.Role]highlight.Role{
	syntax.RolePlain:       highlight.Plain,
	syntax.RoleKeyword:     highlight.Keyword,
	syntax.RoleString:      highlight.String,
	syntax.RoleNumber:      highlight.Number,
	syntax.RoleComment:     highlight.Comment,
	syntax.RoleVariable:    highlight.Variable,
	syntax.RoleFunction:    highlight.Function,
	syntax.RoleType:        highlight.Type,
	syntax.RoleConstant:    highlight.Constant,
	syntax.RoleOperator:    highlight.Operator,
	syntax.RolePunctuation: highlight.Punctuation,
	syntax.RoleTag:         highlight.Tag,
	syntax.RoleAttribute:   highlight.Attribute,
	syntax.RoleProperty:    highlight.Property,
}

// Role maps a syntax role to the editor's highlight role; unknown roles
// map to highlight.Plain.
func Role(r syntax.Role) highlight.Role { return roles[r] }
