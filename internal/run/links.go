package run

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/compose"
)

// Link is a file location found in a line of output. Start and End are
// byte offsets of the location text (path plus line suffix) in the line.
type Link struct {
	Start int `json:"start"`
	End   int `json:"end"`
	// Path is the path as printed (e.g. a container path).
	Path string `json:"path"`
	// File is the absolute host path.
	File string `json:"file"`
	// Line and Column are 1-based; Column is 0 when unknown.
	Line   int `json:"line"`
	Column int `json:"column,omitempty"`
}

// linkRe matches "path:line[:col]", "path(line)" (PHP stack traces) and
// "path on line N" (PHP error messages). The path needs an extension so
// that plain words followed by a colon are not taken for files.
var linkRe = regexp.MustCompile(`((?:[A-Za-z]:[\\/])?[^\s:()"'<>|,;\[\]{}=]+\.[A-Za-z][A-Za-z0-9]*)(?::(\d+)(?::(\d+))?|\((\d+)\)| on line (\d+))`)

// ansiRe matches terminal escape sequences (colors, cursor movement).
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b[()][A-Za-z0-9]|\x1b[=>]")

// StripANSI removes terminal escape sequences from s.
func StripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRe.ReplaceAllString(s, "")
}

// Linker finds file locations in output lines and maps them to host
// files. It is a pure function of its fields.
type Linker struct {
	// Mappings translate container paths (Remote) to host paths.
	Mappings compose.Mappings
	// Dirs are the bases for relative paths, tried in order (working
	// directory first, then the project root).
	Dirs []string
	// Exists reports whether a host file exists; nil accepts every
	// candidate without checking (the first base wins).
	Exists func(string) bool
}

// FileExists reports whether p is an existing regular file.
func FileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Links returns the locations in text, in order of appearance.
func (l Linker) Links(text string) []Link {
	var out []Link
	for _, m := range linkRe.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[0], m[1]
		// "scheme://host:port" is a URL, not a file.
		p := text[m[2]:m[3]]
		if start > 0 && text[start-1] == ':' || strings.HasPrefix(text[start:], "//") || isURL(text, start, p) {
			continue
		}
		lineNo, col := 0, 0
		switch {
		case m[4] >= 0:
			lineNo, _ = strconv.Atoi(text[m[4]:m[5]])
			if m[6] >= 0 {
				col, _ = strconv.Atoi(text[m[6]:m[7]])
			}
		case m[8] >= 0:
			lineNo, _ = strconv.Atoi(text[m[8]:m[9]])
		case m[10] >= 0:
			lineNo, _ = strconv.Atoi(text[m[10]:m[11]])
			end = m[11]
		}
		if lineNo <= 0 {
			continue
		}
		file, ok := l.resolve(p)
		if !ok {
			continue
		}
		out = append(out, Link{Start: start, End: end, Path: p, File: file, Line: lineNo, Column: col})
	}
	return out
}

// isURL reports whether the path p found at start is part of a URL
// ("http://…": the drive-letter form matched "p:/").
func isURL(text string, start int, p string) bool {
	if len(p) < 3 || p[1] != ':' {
		return false
	}
	if strings.HasPrefix(p[2:], "//") {
		return true
	}
	if start > 0 {
		c := text[start-1]
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	return false
}

// resolve maps a printed path to an absolute host file.
func (l Linker) resolve(p string) (string, bool) {
	p = strings.TrimPrefix(p, "file://")
	exists := func(f string) bool { return l.Exists == nil || l.Exists(f) }
	// Container paths first: a mapped remote path wins over a host path
	// that happens to look the same.
	if strings.HasPrefix(p, "/") {
		if host, ok := l.Mappings.ToHost(path.Clean(p)); ok && exists(host) {
			return host, true
		}
	}
	if filepath.IsAbs(p) {
		f := filepath.Clean(p)
		return f, exists(f)
	}
	if strings.HasPrefix(p, "/") {
		return "", false // POSIX path on Windows without mapping
	}
	for _, d := range l.Dirs {
		if d == "" {
			continue
		}
		f := filepath.Join(d, filepath.FromSlash(p))
		if exists(f) {
			return f, true
		}
	}
	return "", false
}
