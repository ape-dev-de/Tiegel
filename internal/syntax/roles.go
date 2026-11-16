package syntax

import "strings"

// Role is the semantic category of a highlighted source span. The UI maps
// roles to theme colours; the syntax package never deals with colours.
type Role uint8

// Syntax roles. RolePlain marks text without special highlighting.
const (
	RolePlain Role = iota
	RoleKeyword
	RoleString
	RoleNumber
	RoleComment
	RoleVariable
	RoleFunction
	RoleType
	RoleConstant
	RoleOperator
	RolePunctuation
	RoleTag
	RoleAttribute
	RoleProperty
	roleCount
)

var roleNames = [...]string{
	RolePlain:       "plain",
	RoleKeyword:     "keyword",
	RoleString:      "string",
	RoleNumber:      "number",
	RoleComment:     "comment",
	RoleVariable:    "variable",
	RoleFunction:    "function",
	RoleType:        "type",
	RoleConstant:    "constant",
	RoleOperator:    "operator",
	RolePunctuation: "punctuation",
	RoleTag:         "tag",
	RoleAttribute:   "attribute",
	RoleProperty:    "property",
}

// String returns the lower-case role name, e.g. "keyword".
func (r Role) String() string {
	if r >= roleCount {
		return "plain"
	}
	return roleNames[r]
}

// Roles returns all roles in declaration order, starting with RolePlain.
func Roles() []Role {
	out := make([]Role, roleCount)
	for i := range out {
		out[i] = Role(i)
	}
	return out
}

// captureRoles maps Tree-sitter capture names (as used by the upstream
// highlights.scm files) to roles. Lookup uses the longest dotted prefix, so
// "function.method" falls back to "function" unless listed explicitly.
var captureRoles = map[string]Role{
	"keyword":               RoleKeyword,
	"string":                RoleString,
	"string.special.key":    RoleProperty,
	"string.special.symbol": RoleConstant,
	"escape":                RoleConstant,
	"number":                RoleNumber,
	"boolean":               RoleConstant,
	"comment":               RoleComment,
	"variable":              RoleVariable,
	"variable.builtin":      RoleKeyword,
	"function":              RoleFunction,
	"constructor":           RoleFunction,
	"type":                  RoleType,
	"module":                RoleType,
	"module.builtin":        RoleKeyword,
	"constant":              RoleConstant,
	"label":                 RoleConstant,
	"operator":              RoleOperator,
	"punctuation":           RolePunctuation,
	"tag":                   RoleTag,
	"attribute":             RoleAttribute,
	"property":              RoleProperty,
	"markup.heading":        RolePunctuation,
	"markup.link":           RoleString,
	"markup.raw":            RoleString,
	// Deliberately plain: "markup", "embedded", "error".
}

// RoleForCapture maps a capture name such as "function.method" or
// "punctuation.bracket" to a Role. Unknown names map to RolePlain.
func RoleForCapture(name string) Role {
	for {
		if r, ok := captureRoles[name]; ok {
			return r
		}
		i := strings.LastIndexByte(name, '.')
		if i < 0 {
			return RolePlain
		}
		name = name[:i]
	}
}
