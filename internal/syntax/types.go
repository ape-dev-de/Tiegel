package syntax

// This file holds the parts of the package that do not depend on
// Tree-sitter (and therefore not on cgo): positions, spans, outline types,
// language detection. The Tree-sitter backed implementation lives in the
// files tagged "cgo"; nocgo.go provides the same API without it (D80).

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
)

// ErrNoTreeSitter is returned for Tree-sitter languages (PHP, JSON, YAML,
// XML) when the binary was built without cgo and therefore without
// Tree-sitter (see TreeSitter). Such clients get syntax data from a backend
// via the syntax.* API methods.
var ErrNoTreeSitter = errors.New("syntax: built without Tree-sitter (cgo disabled)")

// treeLanguages are the languages backed by a Tree-sitter grammar.
var treeLanguages = map[Language]bool{LangPHP: true, LangJSON: true, LangYAML: true, LangXML: true}

// Point is a zero-based position: Line counts '\n'-separated lines, Column
// counts bytes from the start of the line (not runes).
type Point struct {
	Line   int
	Column int
}

// InputEdit describes a single text change, in the coordinates of the text
// before the change (start, old end) and after it (new end).
type InputEdit struct {
	StartByte   int
	OldEndByte  int
	NewEndByte  int
	StartPoint  Point
	OldEndPoint Point
	NewEndPoint Point
}

// NewInputEdit computes the InputEdit for replacing old[start:oldEnd] with
// inserted. old is the text before the change.
func NewInputEdit(old []byte, start, oldEnd int, inserted []byte) InputEdit {
	sp := PointAt(old, start)
	return InputEdit{
		StartByte:   start,
		OldEndByte:  oldEnd,
		NewEndByte:  start + len(inserted),
		StartPoint:  sp,
		OldEndPoint: PointAt(old, oldEnd),
		NewEndPoint: advance(sp, inserted),
	}
}

// PointAt returns the Point of byte offset off in src (clamped to len(src)).
func PointAt(src []byte, off int) Point {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	return advance(Point{}, src[:off])
}

func advance(p Point, text []byte) Point {
	n := bytes.Count(text, []byte{'\n'})
	if n == 0 {
		return Point{Line: p.Line, Column: p.Column + len(text)}
	}
	return Point{Line: p.Line + n, Column: len(text) - bytes.LastIndexByte(text, '\n') - 1}
}

// Span is a highlighted byte range [Start, End) of the document.
type Span struct {
	Start int
	End   int
	Role  Role
}

// Range is an inclusive, zero-based line range, used for folding regions.
type Range struct {
	StartLine int
	EndLine   int
}

// ErrClosed is returned by operations on a closed Document.
var ErrClosed = errors.New("syntax: document closed")

// compress turns a per-byte role slice into spans starting at offset base.
func compress(paint []Role, base int) []Span {
	var out []Span
	for i := 0; i < len(paint); {
		j := i + 1
		for j < len(paint) && paint[j] == paint[i] {
			j++
		}
		out = append(out, Span{Start: base + i, End: base + j, Role: paint[i]})
		i = j
	}
	return out
}

// Language identifies a supported source language.
type Language string

// Supported languages. LangPlain has no highlighting at all.
const (
	LangPlain Language = ""
	LangPHP   Language = "php"
	LangJSON  Language = "json"
	LangYAML  Language = "yaml"
	LangXML   Language = "xml"
	LangEnv   Language = "env"
)

// DetectLanguage guesses the language from the file name and, if needed, the
// first bytes of the content (shebang or "<?php").
func DetectLanguage(path string, head []byte) Language {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return LangEnv
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".php", ".phtml", ".php3", ".php4", ".php5", ".php7", ".php8", ".phps", ".inc":
		return LangPHP
	case ".json", ".jsonc", ".lock":
		if filepath.Ext(base) == ".lock" && base != "composer.lock" {
			break
		}
		return LangJSON
	case ".yml", ".yaml":
		return LangYAML
	case ".xml", ".xsd", ".xsl", ".xslt", ".svg", ".xlf", ".xliff":
		return LangXML
	case ".dist":
		// phpunit.xml.dist, phpcs.xml.dist, ...
		return DetectLanguage(strings.TrimSuffix(path, filepath.Ext(path)), head)
	}
	if bytes.HasPrefix(head, []byte("#!")) {
		line, _, _ := bytes.Cut(head, []byte("\n"))
		if bytes.Contains(line, []byte("php")) {
			return LangPHP
		}
	}
	if bytes.HasPrefix(bytes.TrimSpace(head), []byte("<?php")) {
		return LangPHP
	}
	if bytes.HasPrefix(bytes.TrimSpace(head), []byte("<?xml")) {
		return LangXML
	}
	return LangPlain
}

// Capture is one captured node of a query match.
type Capture struct {
	Name      string
	Kind      string // node kind, e.g. "enum_declaration"
	StartByte int
	EndByte   int
	Start     Point
	End       Point
}

// Match is one query match with its captures in pattern order.
type Match struct {
	Pattern  int
	Captures []Capture
}

// SymbolKind classifies an outline entry.
type SymbolKind uint8

// Outline symbol kinds.
const (
	SymbolNamespace SymbolKind = iota
	SymbolClass
	SymbolInterface
	SymbolTrait
	SymbolEnum
	SymbolEnumCase
	SymbolMethod
	SymbolFunction
	SymbolProperty
	SymbolConstant
)

var symbolKindNames = [...]string{"namespace", "class", "interface", "trait", "enum", "enum case", "method", "function", "property", "constant"}

// String returns a lower-case name such as "class".
func (k SymbolKind) String() string {
	if int(k) < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return "unknown"
}

// Symbol is one entry of the structure outline.
type Symbol struct {
	Kind SymbolKind
	Name string
	// Detail is a short signature: parameters and return type for functions
	// and methods, the declared type for properties and typed constants, the
	// backing type for enums.
	Detail string
	// Modifiers such as "public", "static", "abstract", "readonly",
	// "private(set)" in source order.
	Modifiers []string
	StartByte int
	EndByte   int
	Start     Point // start of the whole declaration
	End       Point
	NameStart Point // position of the name, for navigation
	Children  []Symbol
}

// HasTree reports whether documents of this language are backed by a
// Tree-sitter syntax tree (false for .env and plain text). It does not depend
// on the build: without cgo, NewDocument fails for these languages with
// ErrNoTreeSitter.
func (l Language) HasTree() bool { return treeLanguages[l] }
