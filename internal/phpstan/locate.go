package phpstan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Locate returns the byte range [start, end) within line that an error
// message is about: the method, function, property, variable or class it
// names when that occurs in the line, otherwise the line without leading
// and trailing blanks. ok is false for a blank line.
func Locate(line, message string) (start, end int, ok bool) {
	t := strings.TrimRight(line, " \t\r\n")
	s := len(t) - len(strings.TrimLeft(t, " \t"))
	if s >= len(t) {
		return 0, 0, false
	}
	for _, c := range candidates(message) {
		if i := strings.Index(t, c.needle); i >= 0 {
			// The needle may carry context ("->name("); mark the name.
			j := i + strings.Index(c.needle, c.mark)
			return j, j + len(c.mark), true
		}
	}
	return s, len(t), true
}

type candidate struct{ needle, mark string }

var (
	reMethod   = regexp.MustCompile(`(?:method|Method) (?:[\w\\]+)?::(\w+)\(\)`)
	reStatic   = regexp.MustCompile(`::(\w+)\(\)`)
	reFunction = regexp.MustCompile(`(?:function|Function) ([\w\\]+)(?:\(\))?`)
	reProperty = regexp.MustCompile(`(?:property|Property) [\w\\]+::\$(\w+)`)
	reVariable = regexp.MustCompile(`\$(\w+)`)
	reConstant = regexp.MustCompile(`(?:constant|Constant) [\w\\]*::(\w+)`)
	reClass    = regexp.MustCompile(`(?:class|interface|trait|enum) ([\w\\]+)`)
)

// candidates are the code fragments a message names, best first.
func candidates(msg string) []candidate {
	var out []candidate
	add := func(needle, mark string) {
		if mark != "" {
			out = append(out, candidate{needle, mark})
		}
	}
	for _, m := range reMethod.FindAllStringSubmatch(msg, -1) {
		add("->"+m[1]+"(", m[1])
		add("::"+m[1]+"(", m[1])
		add("function "+m[1]+"(", m[1])
	}
	for _, m := range reStatic.FindAllStringSubmatch(msg, -1) {
		add("::"+m[1]+"(", m[1])
		add("->"+m[1]+"(", m[1])
	}
	for _, m := range reProperty.FindAllStringSubmatch(msg, -1) {
		add("->"+m[1], m[1])
		add("::$"+m[1], "$"+m[1])
		add("$"+m[1], "$"+m[1])
	}
	for _, m := range reConstant.FindAllStringSubmatch(msg, -1) {
		add("::"+m[1], m[1])
	}
	for _, m := range reFunction.FindAllStringSubmatch(msg, -1) {
		name := m[1][strings.LastIndexByte(m[1], '\\')+1:]
		add(name+"(", name)
	}
	for _, m := range reVariable.FindAllStringSubmatch(msg, -1) {
		if m[1] != "this" {
			add("$"+m[1], "$"+m[1])
		}
	}
	for _, m := range reClass.FindAllStringSubmatch(msg, -1) {
		name := m[1][strings.LastIndexByte(m[1], '\\')+1:]
		add(name, name)
	}
	return out
}

// Resolver turns the paths of a report or cache into host files.
type Resolver struct {
	// Base is the directory relative paths are relative to (the working
	// directory of a report, PHPStan's %rootDir% for a 2.2+ cache).
	Base string
	// ToHost maps a path of the machine PHPStan ran on (e.g. a container)
	// to the host; nil: none.
	ToHost func(string) (string, bool)
	// Exists reports whether a host file exists (nil: os.Stat).
	Exists func(string) bool
}

func (r Resolver) exists(p string) bool {
	if r.Exists != nil {
		return r.Exists(p)
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Resolve returns the host file of p; ok is false when it does not exist
// on the host (deleted since, or a path nobody maps).
func (r Resolver) Resolve(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/") || filepath.IsAbs(p) {
		if r.exists(p) {
			return filepath.Clean(p), true
		}
		if r.ToHost != nil {
			if h, ok := r.ToHost(slash); ok && r.exists(h) {
				return h, true
			}
		}
		return p, false
	}
	if r.Base == "" {
		return p, false
	}
	h := filepath.Join(r.Base, filepath.FromSlash(slash))
	return h, r.exists(h)
}
