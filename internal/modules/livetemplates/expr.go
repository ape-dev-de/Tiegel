package livetemplates

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Expressions computes the initial value of a variable:
//
//	"text"                      a literal
//	className()                 short name of the enclosing class, interface, trait or enum
//	classFQN()                  its fully qualified name
//	methodName()                the enclosing method
//	functionName()              the enclosing function or method
//	namespace()                 the namespace at the position
//	fileName()                  the file name
//	fileNameWithoutExtension()  the file name without extension
//	date() / date("Y-m-d")      today (PHP-style letters Y y m n d j H G i s)
//	time()                      the time (H:i)
//	user()                      the login name
//	clipboard()                 the clipboard text passed by the client
//	lineNumber()                the 1-based line
//	capitalize(VAR) decapitalize(VAR) camelCase(VAR) snakeCase(VAR)
//	                            derived from another variable
var Expressions = []string{`"text"`, "className()", "classFQN()", "methodName()", "functionName()", "namespace()",
	"fileName()", "fileNameWithoutExtension()", `date("Y-m-d")`, "time()", "user()", "clipboard()", "lineNumber()",
	"capitalize(VAR)", "decapitalize(VAR)", "camelCase(VAR)", "snakeCase(VAR)"}

// expr is a parsed expression.
type expr struct {
	fn  string // "" for a literal
	arg string // literal text, date format or variable name
}

var exprRe = regexp.MustCompile(`^([A-Za-z]+)\((.*)\)$`)

var exprFuncs = map[string]int{ // function → arguments: 0 none, 1 optional string, 2 variable
	"className": 0, "classFQN": 0, "methodName": 0, "functionName": 0, "namespace": 0, "fileName": 0,
	"fileNameWithoutExtension": 0, "date": 1, "time": 0, "user": 0, "clipboard": 0, "lineNumber": 0,
	"capitalize": 2, "decapitalize": 2, "camelCase": 2, "snakeCase": 2,
}

func parseExpr(s string) (expr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return expr{}, nil
	}
	if strings.HasPrefix(s, `"`) {
		v, err := strconv.Unquote(s)
		if err != nil {
			return expr{}, fmt.Errorf("bad string %s", s)
		}
		return expr{arg: v, fn: "literal"}, nil
	}
	m := exprRe.FindStringSubmatch(s)
	if m == nil {
		return expr{}, fmt.Errorf("unknown expression %q (e.g. %s)", s, strings.Join(Expressions[:4], ", "))
	}
	kind, ok := exprFuncs[m[1]]
	if !ok {
		return expr{}, fmt.Errorf("unknown function %s()", m[1])
	}
	arg := strings.TrimSpace(m[2])
	switch kind {
	case 0:
		if arg != "" {
			return expr{}, fmt.Errorf("%s() takes no argument", m[1])
		}
	case 1:
		if arg != "" {
			v, err := strconv.Unquote(arg)
			if err != nil {
				return expr{}, fmt.Errorf("%s(): argument must be a quoted string", m[1])
			}
			arg = v
		}
	case 2:
		if arg == "" || !validAbbreviation(arg) {
			return expr{}, fmt.Errorf("%s() needs a variable name", m[1])
		}
	}
	return expr{fn: m[1], arg: arg}, nil
}

// position describes where a template is expanded (input of eval).
type position struct {
	file      string
	line      int // 1-based
	class     string
	classFQN  string
	method    string
	function  string
	namespace string
	clipboard string
	now       time.Time
}

// eval computes the value of e; vars holds the values computed so far.
func (e expr) eval(p position, vars map[string]string) (string, bool) {
	switch e.fn {
	case "":
		return "", false
	case "literal":
		return e.arg, true
	case "className":
		return p.class, p.class != ""
	case "classFQN":
		return p.classFQN, p.classFQN != ""
	case "methodName":
		return p.method, p.method != ""
	case "functionName":
		if p.method != "" {
			return p.method, true
		}
		return p.function, p.function != ""
	case "namespace":
		return p.namespace, p.namespace != ""
	case "fileName":
		return filepath.Base(p.file), p.file != ""
	case "fileNameWithoutExtension":
		b := filepath.Base(p.file)
		return strings.TrimSuffix(b, filepath.Ext(b)), p.file != ""
	case "date":
		f := e.arg
		if f == "" {
			f = "Y-m-d"
		}
		return phpDate(p.now, f), true
	case "time":
		return phpDate(p.now, "H:i"), true
	case "user":
		u := os.Getenv("USER")
		if u == "" {
			u = os.Getenv("USERNAME")
		}
		return u, u != ""
	case "clipboard":
		return p.clipboard, p.clipboard != ""
	case "lineNumber":
		return strconv.Itoa(p.line), true
	}
	v, ok := vars[e.arg]
	if !ok || v == "" {
		v = e.arg
	}
	switch e.fn {
	case "capitalize":
		return upperFirst(v), true
	case "decapitalize":
		return lowerFirst(v), true
	case "camelCase":
		return camelCase(v), true
	case "snakeCase":
		return snakeCase(v), true
	}
	return "", false
}

// phpDate formats t with the PHP date letters Y y m n d j H G i s; other
// characters are copied, "\" escapes the next one.
func phpDate(t time.Time, f string) string {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		switch c := f[i]; c {
		case 'Y':
			b.WriteString(t.Format("2006"))
		case 'y':
			b.WriteString(t.Format("06"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'n':
			b.WriteString(strconv.Itoa(int(t.Month())))
		case 'd':
			b.WriteString(t.Format("02"))
		case 'j':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'H':
			b.WriteString(t.Format("15"))
		case 'G':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'i':
			b.WriteString(t.Format("04"))
		case 's':
			b.WriteString(t.Format("05"))
		case '\\':
			if i+1 < len(f) {
				i++
				b.WriteByte(f[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

func lowerFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToLower(r)) + s[i+len(string(r)):]
	}
	return s
}

// words splits s at '_', '-', spaces and lower→upper case changes.
func words(s string) []string {
	var out []string
	var cur []rune
	prevLower := false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || unicode.IsSpace(r):
			flush()
			prevLower = false
			continue
		case unicode.IsUpper(r) && prevLower:
			flush()
		}
		cur = append(cur, r)
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	flush()
	return out
}

func camelCase(s string) string {
	ws := words(s)
	for i, w := range ws {
		w = strings.ToLower(w)
		if i > 0 {
			w = upperFirst(w)
		}
		ws[i] = w
	}
	return strings.Join(ws, "")
}

func snakeCase(s string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "_")
}
