package syntax

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// lexer is a small, table-driven tokenizer for snippet languages without a
// Tree-sitter grammar. It knows comments, strings, numbers, keywords,
// constants, types, variables, function calls, operators and punctuation;
// that is enough to make code blocks in documentation readable.
type lexer struct {
	line      []string    // line comment starts ("//", "#", "--")
	block     [][2]string // block comment delimiters
	quotes    string      // string delimiters
	keywords  map[string]bool
	constants map[string]bool
	types     map[string]bool
	fold      bool // case-insensitive keywords (SQL, Dockerfile)
	dollar    bool // "$name" / "${…}" are variables (shell)
	capType   bool // capitalized identifiers are types
	propColon bool // "name:" inside braces is a property (CSS)
	hashLine  bool // "#" starts a comment only at line start or after a space (shell)
	lineKw    bool // keywords only as the first word of a line
	parenVar  bool // "$(name)" is a variable reference (make)
	assign    bool // a first word before "=", ":=", "?=", "+=" is a property, before ":" a target (make)
	// identExtra are characters that start or continue identifiers in
	// addition to letters, digits and "_" (make targets: ".PHONY", "a/b.o").
	identExtra string
	// special handles a whole line itself (diff, INI); it returns false to
	// fall back to the normal tokenizer.
	special func(line string, base int, out *[]Span) bool
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

const cFamilyKeywords = `abstract as async await break case catch class const continue default defer delete do else enum
export extends final finally fn for func go goto if impl implements import in instanceof interface internal let loop
match mod module mut namespace new of override package private protected pub public readonly return select static
struct super switch template this throw throws trait try type typedef typeof union unsafe use using var virtual void
volatile where while with yield chan map range fallthrough val fun object companion sealed data when is extern sizeof
inline operator friend constexpr auto register signed unsigned crate self Self dyn ref move lazy get set declare
keyof infer satisfies from`

var lexers = map[string]*lexer{
	"js": {
		line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`", capType: true,
		keywords: words(`async await break case catch class const continue debugger default delete do else enum export
extends finally for from function get if implements import in instanceof interface let new of package private protected
public readonly return set static super switch this throw try type typeof var void while with yield as declare keyof
abstract namespace module satisfies`),
		constants: words("true false null undefined NaN Infinity"),
		types:     words("string number boolean any unknown never object symbol bigint void"),
	},
	"go": {
		line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: words(`break case chan const continue default defer else fallthrough for func go goto if import
interface map package range return select struct switch type var`),
		constants: words("true false nil iota"),
		types: words(`bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint
uint8 uint16 uint32 uint64 uintptr any comparable`),
	},
	"c": {
		line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'", capType: true,
		keywords:  words(cFamilyKeywords),
		constants: words("true false null nil NULL nullptr None"),
		types: words(`int long short char float double bool boolean byte string String void size_t i8 i16 i32 i64 u8 u16
u32 u64 f32 f64 usize isize str Vec Option Result`),
	},
	"py": {
		line: []string{"#"}, quotes: "\"'", capType: true,
		keywords: words(`and as assert async await break class continue def del elif else except finally for from global
if import in is lambda nonlocal not or pass raise return try while with yield match case`),
		constants: words("True False None self cls"),
		types:     words("int float str bool list dict set tuple bytes object"),
	},
	"sql": {
		line: []string{"--", "#"}, block: [][2]string{{"/*", "*/"}}, quotes: "'\"`", fold: true,
		keywords: words(`select from where and or not insert into values update set delete create table drop alter add
column index primary key foreign references join left right inner outer full cross on as order by group having limit
offset distinct union all exists in is like between case when then else end begin commit rollback transaction view
database schema if default unique constraint check grant revoke with recursive returning asc desc truncate replace
procedure function trigger declare cascade auto_increment engine charset collate unsigned using natural`),
		constants: words("null true false"),
		types: words(`int integer bigint smallint tinyint decimal numeric float real double varchar char text blob date
datetime timestamp time boolean bool json jsonb uuid serial bigserial bytea enum longtext mediumtext`),
	},
	"sh": {
		line: []string{"#"}, quotes: "\"'`", dollar: true, hashLine: true,
		keywords: words(`if then else elif fi for while until do done case esac in function return local export
declare readonly unset shift break continue exit source alias set trap eval exec select time`),
		constants: words("true false"),
	},
	"css": {
		line: nil, block: [][2]string{{"/*", "*/"}}, quotes: "\"'", propColon: true,
		keywords:  words("important !important"),
		constants: words("none auto inherit initial unset transparent"),
	},
	"ini": {
		line: []string{";", "#"}, quotes: "\"'",
		constants: iniConstants,
		special:   iniLine,
	},
	"make": {
		line: []string{"#"}, quotes: "\"'", dollar: true, parenVar: true, assign: true, identExtra: "./%-",
		keywords: words(`ifeq ifneq ifdef ifndef else endif include sinclude define endef export unexport override
vpath undefine private`),
	},
	"ignore": {special: ignoreLine},
	"diff":   {special: diffLine},
}

// highlight tokenizes src and returns the non-plain spans.
func (lx *lexer) highlight(src []byte) []Span {
	var out []Span
	s := string(src)
	st := lexState{}
	off := 0
	for off <= len(s) {
		end := strings.IndexByte(s[off:], '\n')
		if end < 0 {
			end = len(s) - off
		}
		ln := s[off : off+end]
		if !(lx.special != nil && st.blockEnd == "" && lx.special(ln, off, &out)) {
			lx.lineSpans(ln, off, &st, &out)
		}
		off += end + 1
	}
	return mergeSpans(out)
}

// lexState carries an open block comment across lines.
type lexState struct {
	blockEnd string
	depth    int // brace depth (CSS properties)
}

func add(out *[]Span, s, e int, r Role) {
	if e > s && r != RolePlain {
		*out = append(*out, Span{Start: s, End: e, Role: r})
	}
}

func mergeSpans(in []Span) []Span {
	var out []Span
	for _, s := range in {
		if n := len(out); n > 0 && out[n-1].End == s.Start && out[n-1].Role == s.Role {
			out[n-1].End = s.End
			continue
		}
		out = append(out, s)
	}
	return out
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdent(r rune) bool      { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// lineSpans tokenizes one line starting at byte base of the snippet.
func (lx *lexer) lineSpans(ln string, base int, st *lexState, out *[]Span) {
	i := 0
	firstWord := true
	for i < len(ln) {
		if st.blockEnd != "" {
			j := strings.Index(ln[i:], st.blockEnd)
			if j < 0 {
				add(out, base+i, base+len(ln), RoleComment)
				return
			}
			add(out, base+i, base+i+j+len(st.blockEnd), RoleComment)
			i += j + len(st.blockEnd)
			st.blockEnd = ""
			continue
		}
		rest := ln[i:]
		if lc := lx.lineComment(ln, i); lc {
			add(out, base+i, base+len(ln), RoleComment)
			return
		}
		opened := false
		for _, b := range lx.block {
			if strings.HasPrefix(rest, b[0]) {
				st.blockEnd = b[1]
				add(out, base+i, base+i+len(b[0]), RoleComment)
				i += len(b[0])
				opened = true
				break
			}
		}
		if opened {
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			i += size
			continue
		case strings.ContainsRune(lx.quotes, r):
			j := stringEnd(ln, i, r)
			add(out, base+i, base+j, RoleString)
			i = j
		case lx.dollar && r == '$' && i+1 < len(ln):
			j := i + 1
			if ln[j] == '{' || lx.parenVar && ln[j] == '(' {
				j = closeParen(ln, j)
			} else {
				for j < len(ln) {
					c, sz := utf8.DecodeRuneInString(ln[j:])
					if !isIdent(c) && !(j == i+1 && strings.ContainsRune("?#@*!$0123456789", c)) {
						break
					}
					j += sz
				}
			}
			if j == i+1 {
				add(out, base+i, base+j, RoleOperator)
			} else {
				add(out, base+i, base+j, RoleVariable)
			}
			i = j
		case unicode.IsDigit(r) || (r == '.' && i+1 < len(ln) && ln[i+1] >= '0' && ln[i+1] <= '9'):
			j := i + 1
			for j < len(ln) {
				c := ln[j]
				if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '.' || c == '_' || c == '%' {
					j++
					continue
				}
				break
			}
			add(out, base+i, base+j, RoleNumber)
			i = j
		case isIdentStart(r) || (lx.propColon && r == '-') || strings.ContainsRune(lx.identExtra, r):
			j := i + size
			for j < len(ln) {
				c, sz := utf8.DecodeRuneInString(ln[j:])
				if strings.ContainsRune(lx.identExtra, c) {
					j += sz
					continue
				}
				if !isIdent(c) && c != '-' || (c == '-' && !lx.propColon && !lx.dollar) {
					break
				}
				j += sz
			}
			lx.word(ln, i, j, base, st, firstWord, out)
			i = j
		default:
			switch {
			case strings.ContainsRune("{}()[];,", r):
				if r == '{' {
					st.depth++
				} else if r == '}' && st.depth > 0 {
					st.depth--
				}
				add(out, base+i, base+i+size, RolePunctuation)
			case strings.ContainsRune("+-*/%=<>!&|^~?:.@#", r):
				add(out, base+i, base+i+size, RoleOperator)
			}
			i += size
		}
		firstWord = false
	}
}

func (lx *lexer) lineComment(ln string, i int) bool {
	for _, c := range lx.line {
		if !strings.HasPrefix(ln[i:], c) {
			continue
		}
		if c == "#" && lx.hashLine && i > 0 && ln[i-1] != ' ' && ln[i-1] != '\t' {
			continue
		}
		return true
	}
	return false
}

// stringEnd returns the end of the string starting with quote q at i
// (backslash escapes; unterminated strings end with the line).
func stringEnd(ln string, i int, q rune) int {
	j := i + utf8.RuneLen(q)
	for j < len(ln) {
		switch {
		case ln[j] == '\\' && q != '`':
			j += 2
		case rune(ln[j]) == q:
			return j + 1
		default:
			j++
		}
	}
	return len(ln)
}

func (lx *lexer) word(ln string, i, j, base int, st *lexState, first bool, out *[]Span) {
	w := ln[i:j]
	key := w
	if lx.fold {
		key = strings.ToLower(w)
	}
	next := strings.TrimLeft(ln[j:], " \t")
	switch {
	case lx.assign && first && !lx.keywords[key] && isAssignment(next):
		add(out, base+i, base+j, RoleProperty)
	case lx.assign && first && !lx.keywords[key] && strings.HasPrefix(next, ":"):
		add(out, base+i, base+j, RoleFunction)
	case lx.keywords[key] && (!lx.lineKw || first):
		add(out, base+i, base+j, RoleKeyword)
	case lx.constants[key]:
		add(out, base+i, base+j, RoleConstant)
	case lx.types[key]:
		add(out, base+i, base+j, RoleType)
	case lx.propColon && st.depth > 0 && strings.HasPrefix(next, ":"):
		add(out, base+i, base+j, RoleProperty)
	case strings.HasPrefix(next, "("):
		add(out, base+i, base+j, RoleFunction)
	case lx.capType && unicode.IsUpper([]rune(w)[0]):
		add(out, base+i, base+j, RoleType)
	}
}

// closeParen returns the end of the bracketed reference starting with "{"
// or "(" at j, honouring nesting ("$(shell $(CC) -v)"); unterminated
// references end with the line.
func closeParen(ln string, j int) int {
	open, shut := ln[j], byte('}')
	if open == '(' {
		shut = ')'
	}
	depth := 0
	for k := j; k < len(ln); k++ {
		switch ln[k] {
		case open:
			depth++
		case shut:
			depth--
			if depth == 0 {
				return k + 1
			}
		}
	}
	return len(ln)
}

// isAssignment reports whether rest (after a name) starts a make variable
// assignment: "=", ":=", "::=", "?=", "+=", "!=".
func isAssignment(rest string) bool {
	for _, op := range []string{"=", ":=", "::=", "?=", "+=", "!="} {
		if strings.HasPrefix(rest, op) {
			return true
		}
	}
	return false
}

// ignoreLine highlights gitignore-style patterns (.dockerignore and
// friends): comments, "!" negation and glob characters.
func ignoreLine(ln string, base int, out *[]Span) bool {
	t := strings.TrimLeft(ln, " \t")
	lead := len(ln) - len(t)
	if strings.HasPrefix(t, "#") {
		add(out, base+lead, base+len(ln), RoleComment)
		return true
	}
	for i := lead; i < len(ln); i++ {
		switch c := ln[i]; {
		case c == '!' && i == lead:
			add(out, base+i, base+i+1, RoleOperator)
		case c == '\\' && i+1 < len(ln):
			add(out, base+i, base+i+2, RoleString)
			i++
		case c == '*' || c == '?':
			add(out, base+i, base+i+1, RoleOperator)
		case c == '[' || c == ']':
			add(out, base+i, base+i+1, RolePunctuation)
		}
	}
	return true
}

// iniLine highlights "[section]" headers and "key = value" lines.
func iniLine(ln string, base int, out *[]Span) bool {
	t := strings.TrimSpace(ln)
	lead := strings.Index(ln, t)
	switch {
	case t == "":
		return true
	case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
		add(out, base+lead, base+lead+len(t), RoleType)
		return true
	case strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#"):
		add(out, base+lead, base+len(ln), RoleComment)
		return true
	}
	k := strings.IndexAny(ln, "=:")
	if k <= 0 {
		return false
	}
	key := strings.TrimRight(ln[:k], " \t")
	add(out, base+lead, base+len(key), RoleProperty)
	add(out, base+k, base+k+1, RoleOperator)
	v := strings.TrimSpace(ln[k+1:])
	if v == "" {
		return true
	}
	vs := k + 1 + strings.Index(ln[k+1:], v)
	vl := strings.ToLower(v)
	switch {
	case strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'"):
		add(out, base+vs, base+vs+stringEnd(v, 0, rune(v[0])), RoleString)
	case iniConstants[vl]:
		add(out, base+vs, base+vs+len(v), RoleConstant)
	case len(v) > 0 && (v[0] >= '0' && v[0] <= '9' || v[0] == '-'):
		add(out, base+vs, base+vs+len(v), RoleNumber)
	default:
		add(out, base+vs, base+vs+len(v), RoleString)
	}
	return true
}

// diffLine colors added, removed, hunk and header lines of a unified diff.
func diffLine(ln string, base int, out *[]Span) bool {
	switch {
	case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, "diff ") ||
		strings.HasPrefix(ln, "index ") || strings.HasPrefix(ln, "new file") || strings.HasPrefix(ln, "deleted file"):
		add(out, base, base+len(ln), RoleKeyword)
	case strings.HasPrefix(ln, "@@"):
		add(out, base, base+len(ln), RoleFunction)
	case strings.HasPrefix(ln, "+"):
		add(out, base, base+len(ln), RoleString)
	case strings.HasPrefix(ln, "-"):
		add(out, base, base+len(ln), RoleTag)
	case strings.HasPrefix(ln, "\\"):
		add(out, base, base+len(ln), RoleComment)
	}
	return true
}

var iniConstants = words("true false yes no on off null")
