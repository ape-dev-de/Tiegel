package livetemplates

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/snippet"
)

// Contexts a template applies in (Template.Contexts).
const (
	CtxAny           = "any"            // everywhere
	CtxPHP           = "php"            // PHP code: statement, member or expression
	CtxPHPStatement  = "php-statement"  // where a statement can start
	CtxPHPMember     = "php-member"     // in a class/interface/trait/enum body
	CtxPHPExpression = "php-expression" // where an expression can start
	CtxPHPComment    = "php-comment"    // in a PHP comment
	CtxHTML          = "html"           // text outside <?php ?> tags
	CtxOther         = "other"          // files that are not PHP
)

// Contexts lists the valid template contexts.
var Contexts = []string{CtxAny, CtxPHP, CtxPHPStatement, CtxPHPMember, CtxPHPExpression, CtxPHPComment, CtxHTML, CtxOther}

// Var is the definition of a template variable.
type Var struct {
	// Expr computes the initial value (see Expressions); empty shows the
	// variable name.
	Expr string `json:"expr,omitempty"`
	// Skip: the computed value is final, the variable gets no tab stop
	// (only when the value is not empty).
	Skip bool `json:"skip,omitempty"`
}

// Template is a live template.
type Template struct {
	Abbreviation string         `json:"abbreviation"`
	Description  string         `json:"description"`
	Contexts     []string       `json:"contexts"`
	Body         string         `json:"body"`
	Vars         map[string]Var `json:"vars,omitempty"`
	// Source is "builtin" or "user".
	Source string `json:"source"`
}

// Template sources.
const (
	SourceBuiltin = "builtin"
	SourceUser    = "user"
)

// Surround reports whether the template wraps a selection ($SELECTION$).
func (t Template) Surround() bool { return strings.Contains(t.Body, "$"+snippet.VarSelection+"$") }

// Validate checks abbreviation, contexts, body and variables.
func (t Template) Validate() error {
	if !validAbbreviation(t.Abbreviation) {
		return fmt.Errorf("abbreviation %q: use letters, digits, '_' or '-' (no spaces)", t.Abbreviation)
	}
	if len(t.Contexts) == 0 {
		return fmt.Errorf("%s: no context", t.Abbreviation)
	}
	for _, c := range t.Contexts {
		if !slices.Contains(Contexts, c) {
			return fmt.Errorf("%s: unknown context %q (one of %s)", t.Abbreviation, c, strings.Join(Contexts, ", "))
		}
	}
	if _, err := snippet.Parse(t.Body); err != nil {
		return fmt.Errorf("%s: %w", t.Abbreviation, err)
	}
	for name, v := range t.Vars {
		if _, err := parseExpr(v.Expr); err != nil {
			return fmt.Errorf("%s: variable %s: %w", t.Abbreviation, name, err)
		}
	}
	return nil
}

func validAbbreviation(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isWordRune(r) && r != '-' {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// User templates are stored one per line in the list setting
// live-templates.user (the settings dialog edits lists line by line):
//
//	abbreviation | contexts | description | variables | body
//
// contexts are comma separated, variables "NAME=expr" separated by ";"
// (a trailing "!" marks a variable without tab stop), the body is last and
// may contain "|"; in it \n is a line break, \t a tab (one indentation
// unit) and \\ a backslash.

// sep separates the columns of an encoded user template.
const sep = " | "

// ParseUser decodes one line of live-templates.user.
func ParseUser(line string) (Template, error) {
	cols := strings.SplitN(line, sep, 5)
	if len(cols) != 5 {
		return Template{}, fmt.Errorf("want \"abbreviation | contexts | description | variables | body\", got %d column(s)", len(cols))
	}
	t := Template{Abbreviation: strings.TrimSpace(cols[0]), Description: strings.TrimSpace(cols[2]), Body: unescape(cols[4]), Source: SourceUser}
	for _, c := range strings.Split(cols[1], ",") {
		if c = strings.TrimSpace(c); c != "" {
			t.Contexts = append(t.Contexts, c)
		}
	}
	vars, err := parseVars(cols[3])
	if err != nil {
		return Template{}, fmt.Errorf("%s: %w", t.Abbreviation, err)
	}
	t.Vars = vars
	return t, t.Validate()
}

// FormatUser encodes t as one line of live-templates.user.
func FormatUser(t Template) string {
	names := make([]string, 0, len(t.Vars))
	for n := range t.Vars {
		names = append(names, n)
	}
	slices.Sort(names)
	vars := make([]string, 0, len(names))
	for _, n := range names {
		v := t.Vars[n]
		s := n + "=" + v.Expr
		if v.Skip {
			s += "!"
		}
		vars = append(vars, s)
	}
	return strings.Join([]string{t.Abbreviation, strings.Join(t.Contexts, ","), t.Description, strings.Join(vars, "; "), escape(t.Body)}, sep)
}

func parseVars(s string) (map[string]Var, error) {
	out := map[string]Var{}
	for _, item := range splitOutsideQuotes(s, ';') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, expr, ok := strings.Cut(item, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("variable %q: want NAME=expression", item)
		}
		expr = strings.TrimSpace(expr)
		v := Var{Expr: expr}
		if strings.HasSuffix(expr, "!") {
			v.Expr, v.Skip = strings.TrimSpace(strings.TrimSuffix(expr, "!")), true
		}
		if _, err := parseExpr(v.Expr); err != nil {
			return nil, fmt.Errorf("variable %s: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// splitOutsideQuotes splits s at sep outside "double quotes".
func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	quoted, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && quoted:
			i++
		case s[i] == '"':
			quoted = !quoted
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func escape(s string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\t", "\\t").Replace(s)
}

// Builtin returns the built-in PHP templates (written for Tiegel; tabs
// stand for one indentation unit, "$$" for a literal "$").
func Builtin() []Template {
	st := []string{CtxPHPStatement}
	mem := []string{CtxPHPMember}
	ts := []Template{
		{Abbreviation: "fore", Description: "foreach over values", Contexts: st,
			Body: "foreach ($$$ITERABLE$ as $$$VALUE$) {\n\t$SELECTION$$END$\n}",
			Vars: map[string]Var{"ITERABLE": {Expr: `"items"`}, "VALUE": {Expr: `"item"`}}},
		{Abbreviation: "forek", Description: "foreach over keys and values", Contexts: st,
			Body: "foreach ($$$ITERABLE$ as $$$KEY$ => $$$VALUE$) {\n\t$SELECTION$$END$\n}",
			Vars: map[string]Var{"ITERABLE": {Expr: `"items"`}, "KEY": {Expr: `"key"`}, "VALUE": {Expr: `"value"`}}},
		{Abbreviation: "for", Description: "counting for loop", Contexts: st,
			Body: "for ($$$I$ = 0; $$$I$ < $LIMIT$; $$$I$++) {\n\t$SELECTION$$END$\n}",
			Vars: map[string]Var{"I": {Expr: `"i"`}, "LIMIT": {Expr: `"10"`}}},
		{Abbreviation: "while", Description: "while loop", Contexts: st,
			Body: "while ($CONDITION$) {\n\t$SELECTION$$END$\n}", Vars: map[string]Var{"CONDITION": {Expr: `"true"`}}},
		{Abbreviation: "if", Description: "if block", Contexts: st,
			Body: "if ($CONDITION$) {\n\t$SELECTION$$END$\n}"},
		{Abbreviation: "ife", Description: "if/else block", Contexts: st,
			Body: "if ($CONDITION$) {\n\t$SELECTION$$END$\n} else {\n\t\n}"},
		{Abbreviation: "try", Description: "try/catch block", Contexts: st,
			Body: "try {\n\t$SELECTION$$END$\n} catch (\\$EXCEPTION$ $$e) {\n\t\n}",
			Vars: map[string]Var{"EXCEPTION": {Expr: `"Throwable"`}}},
		{Abbreviation: "tryf", Description: "try/finally block", Contexts: st,
			Body: "try {\n\t$SELECTION$$END$\n} finally {\n\t\n}"},
		{Abbreviation: "thr", Description: "throw a new exception", Contexts: st,
			Body: "throw new \\$EXCEPTION$('$MESSAGE$');$END$", Vars: map[string]Var{"EXCEPTION": {Expr: `"RuntimeException"`}, "MESSAGE": {Expr: `""`}}},
		{Abbreviation: "vd", Description: "var_dump() of a value", Contexts: st,
			Body: "var_dump($SELECTION$$END$);"},
		{Abbreviation: "pubf", Description: "public method", Contexts: mem,
			Body: "public function $NAME$($PARAMS$): $RETURN$\n{\n\t$END$\n}", Vars: map[string]Var{"PARAMS": {Expr: `""`}, "RETURN": {Expr: `"void"`}}},
		{Abbreviation: "prof", Description: "protected method", Contexts: mem,
			Body: "protected function $NAME$($PARAMS$): $RETURN$\n{\n\t$END$\n}", Vars: map[string]Var{"PARAMS": {Expr: `""`}, "RETURN": {Expr: `"void"`}}},
		{Abbreviation: "prif", Description: "private method", Contexts: mem,
			Body: "private function $NAME$($PARAMS$): $RETURN$\n{\n\t$END$\n}", Vars: map[string]Var{"PARAMS": {Expr: `""`}, "RETURN": {Expr: `"void"`}}},
		{Abbreviation: "pubsf", Description: "public static method", Contexts: mem,
			Body: "public static function $NAME$($PARAMS$): $RETURN$\n{\n\t$END$\n}", Vars: map[string]Var{"PARAMS": {Expr: `""`}, "RETURN": {Expr: `"void"`}}},
		{Abbreviation: "construct", Description: "constructor", Contexts: mem,
			Body: "public function __construct($PARAMS$)\n{\n\t$END$\n}", Vars: map[string]Var{"PARAMS": {Expr: `""`}}},
		{Abbreviation: "test", Description: "test method", Contexts: mem,
			Body: "public function test$NAME$(): void\n{\n\t$END$\n}", Vars: map[string]Var{"NAME": {Expr: `"Something"`}}},
		{Abbreviation: "prop", Description: "private typed property", Contexts: mem,
			Body: "private $TYPE$ $$$NAME$;$END$", Vars: map[string]Var{"TYPE": {Expr: `"string"`}, "NAME": {Expr: `"value"`}}},
		{Abbreviation: "const", Description: "public class constant", Contexts: mem,
			Body: "public const $NAME$ = $VALUE$;$END$", Vars: map[string]Var{"VALUE": {Expr: `"''"`}}},
		{Abbreviation: "doc", Description: "doc comment", Contexts: []string{CtxPHPMember, CtxPHPStatement},
			Body: "/**\n * $END$\n */"},
		{Abbreviation: "fn", Description: "arrow function", Contexts: []string{CtxPHPExpression},
			Body: "fn($$$PARAM$) => $END$", Vars: map[string]Var{"PARAM": {Expr: `"x"`}}},
		{Abbreviation: "todo", Description: "TODO comment with date", Contexts: []string{CtxPHPStatement, CtxPHPMember, CtxPHPComment},
			Body: "// TODO($USER$, $DATE$): $END$", Vars: map[string]Var{"USER": {Expr: "user()"}, "DATE": {Expr: "date()", Skip: true}}},
		{Abbreviation: "php", Description: "PHP block in HTML", Contexts: []string{CtxHTML},
			Body: "<?php $SELECTION$$END$ ?>"},
		{Abbreviation: "phpe", Description: "echo tag in HTML", Contexts: []string{CtxHTML},
			Body: "<?= $SELECTION$$END$ ?>"},
	}
	for i := range ts {
		ts[i].Source = SourceBuiltin
	}
	return ts
}
