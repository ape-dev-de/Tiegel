package index

import (
	"strings"
)

// Kind classifies an indexed symbol.
type Kind uint8

// Symbol kinds.
const (
	KindClass Kind = iota + 1
	KindInterface
	KindTrait
	KindEnum
	KindMethod
	KindProperty
	KindClassConst
	KindEnumCase
	KindFunction
	KindConst
)

var kindNames = [...]string{"", "class", "interface", "trait", "enum", "method", "property", "classConstant", "enumCase", "function", "constant"}

// String returns the kind's API name, e.g. "class" or "enumCase".
func (k Kind) String() string {
	if int(k) < len(kindNames) && k != 0 {
		return kindNames[k]
	}
	return "unknown"
}

// ParseKind maps an API name (see Kind.String) back to a Kind.
func ParseKind(s string) (Kind, bool) {
	for i, n := range kindNames {
		if i > 0 && strings.EqualFold(n, s) {
			return Kind(i), true
		}
	}
	return 0, false
}

// IsType reports whether k is a class-like kind (class, interface, trait,
// enum).
func (k Kind) IsType() bool { return k >= KindClass && k <= KindEnum }

// IsMember reports whether k is a member of a class-like declaration.
func (k Kind) IsMember() bool { return k >= KindMethod && k <= KindEnumCase }

// Pos is a position in a file: the 0-based byte offset and the 1-based line
// and column (the column counts characters, like api.TextPos).
type Pos struct {
	Offset int
	Line   int
	Col    int
}

// Range is a range of positions.
type Range struct {
	Start Pos
	End   Pos
}

// Member is a method, property, class constant or enum case.
type Member struct {
	Kind Kind
	Name string // without "$" for properties
	// Visibility is "public", "protected" or "private"; "" means implicit
	// public.
	Visibility string
	Static     bool
	Abstract   bool
	Final      bool
	// Detail is a short signature: parameters and return type of methods,
	// the declared type of properties and typed constants.
	Detail    string
	Range     Range // whole declaration
	NameRange Range
}

// Class is a class, interface, trait or enum declaration.
type Class struct {
	Kind Kind
	// FQN is the fully qualified name without leading backslash, as
	// declared (case preserved).
	FQN      string
	Name     string // short name
	Abstract bool
	Final    bool
	// Extends holds the resolved FQNs of the parent class (classes, at
	// most one) or the parent interfaces (interfaces).
	Extends []string
	// Implements holds the resolved FQNs of implemented interfaces
	// (classes and enums).
	Implements []string
	// Traits holds the resolved FQNs of used traits.
	Traits  []string
	Members []Member
	// Detail is the backing type of enums.
	Detail    string
	Range     Range
	NameRange Range
}

// Function is a global (namespaced) function.
type Function struct {
	FQN       string
	Name      string
	Detail    string
	Range     Range
	NameRange Range
}

// Constant is a global (namespaced) constant declared with const.
type Constant struct {
	FQN       string
	Name      string
	Range     Range
	NameRange Range
}

// UseKind distinguishes class, function and constant imports.
type UseKind uint8

// Import kinds.
const (
	UseClass UseKind = iota
	UseFunction
	UseConst
)

// Use is one import of a use statement (group uses are expanded).
type Use struct {
	Kind  UseKind
	Alias string // the local name (last segment or the "as" alias)
	FQN   string // without leading backslash
}

// Scope is a namespace region of a file with its imports. Files without a
// namespace declaration have one scope with Namespace "".
type Scope struct {
	Namespace string
	Start     int // byte offsets
	End       int
	Uses      []Use
}

// File is everything the index knows about one PHP file. A File is
// immutable once it is part of the index.
type File struct {
	Path string // absolute, OS separators
	// Library marks third-party code (vendor/).
	Library bool
	ModTime int64 // UnixNano of the indexed disk state
	Size    int64
	Hash    uint64 // FNV-1a of the indexed content
	Scopes  []Scope
	Classes []Class
	Funcs   []Function
	Consts  []Constant
}

// ScopeAt returns the namespace scope containing byte offset off (the last
// scope starting before it).
func (f *File) ScopeAt(off int) Scope {
	var best Scope
	for _, s := range f.Scopes {
		if s.Start <= off {
			best = s
		}
	}
	return best
}

// fold returns the case-insensitive key of a PHP name (class, function and
// namespace names are case-insensitive; constants are not, but treating
// them alike is harmless for navigation).
func fold(s string) string { return strings.ToLower(s) }
