// Package editorconfig reads .editorconfig files (https://editorconfig.org)
// and resolves the properties that apply to a file.
//
// Supported properties: indent_style, indent_size, tab_width, end_of_line,
// charset, trim_trailing_whitespace and insert_final_newline. "unset"
// removes a property set by a file further up. Files are searched from the
// file's directory upwards until a file with root = true or the stop
// directory (the project root) is reached; closer files and later sections
// win.
//
// Glob syntax: * (no /), ** (anything), ?, [abc], [!abc], {a,b} and
// {1..9}. A section without "/" matches the base name at any depth; one
// with "/" is relative to the directory of its .editorconfig.
package editorconfig

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FileName is the name of the configuration files.
const FileName = ".editorconfig"

// Props are the properties that apply to one file. Zero values mean
// "not set".
type Props struct {
	// IndentStyle is "tab" or "space".
	IndentStyle string `json:"indentStyle,omitempty"`
	// IndentSize is the indent width in columns.
	IndentSize int `json:"indentSize,omitempty"`
	// TabWidth is the display width of a tab.
	TabWidth int `json:"tabWidth,omitempty"`
	// EndOfLine is "lf", "crlf" or "cr".
	EndOfLine string `json:"endOfLine,omitempty"`
	// Charset is "utf-8", "utf-8-bom", "latin1", "utf-16be" or "utf-16le".
	Charset                string `json:"charset,omitempty"`
	TrimTrailingWhitespace *bool  `json:"trimTrailingWhitespace,omitempty"`
	// MaxLineLength is max_line_length (0: not set or "off"); the editor
	// draws its right margin ruler there (D287).
	MaxLineLength      int   `json:"maxLineLength,omitempty"`
	InsertFinalNewline *bool `json:"insertFinalNewline,omitempty"`
	// Files lists the .editorconfig files that were read, closest first.
	Files []string `json:"files,omitempty"`
}

// Empty reports whether no property is set.
func (p Props) Empty() bool {
	return p.IndentStyle == "" && p.IndentSize == 0 && p.TabWidth == 0 && p.EndOfLine == "" &&
		p.Charset == "" && p.TrimTrailingWhitespace == nil && p.InsertFinalNewline == nil && p.MaxLineLength == 0
}

type section struct {
	re    *regexp.Regexp
	props map[string]string
}

type file struct {
	dir      string
	root     bool
	sections []section
}

// parse parses the content of an .editorconfig file located in dir.
// Sections with invalid globs are skipped.
func parse(dir string, data []byte) (*file, error) {
	f := &file{dir: filepath.ToSlash(dir)}
	sc := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	var cur *section
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.LastIndexByte(line, ']')
			if end < 1 {
				cur = nil
				continue
			}
			re, err := compileGlob(f.dir, line[1:end])
			if err != nil {
				cur = nil
				continue
			}
			f.sections = append(f.sections, section{re: re, props: map[string]string{}})
			cur = &f.sections[len(f.sections)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			k, v, ok = strings.Cut(line, ":")
		}
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if i := strings.IndexAny(v, "#;"); i > 0 && (v[i-1] == ' ' || v[i-1] == '\t') {
			v = strings.TrimSpace(v[:i]) // inline comment
		}
		if cur == nil {
			if k == "root" {
				f.root = strings.EqualFold(v, "true")
			}
			continue
		}
		cur.props[k] = strings.ToLower(v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("editorconfig: %w", err)
	}
	return f, nil
}

// compileGlob turns an editorconfig section glob into an anchored regexp
// for slash-separated absolute paths.
func compileGlob(dir, glob string) (*regexp.Regexp, error) {
	if glob == "" {
		return nil, errors.New("editorconfig: empty glob")
	}
	var prefix string
	switch {
	case strings.HasPrefix(glob, "/"):
		prefix = regexp.QuoteMeta(strings.TrimSuffix(dir, "/"))
	case strings.Contains(glob, "/"):
		prefix = regexp.QuoteMeta(strings.TrimSuffix(dir, "/")) + "/"
	default:
		prefix = regexp.QuoteMeta(strings.TrimSuffix(dir, "/")) + "/(?:.*/)?"
	}
	var b strings.Builder
	b.WriteString("^" + prefix)
	depth := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(string(glob[i])))
			}
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			i += end + 1
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
		case '{':
			end := strings.IndexByte(glob[i+1:], '}')
			if end >= 0 {
				inner := glob[i+1 : i+1+end]
				if lo, hi, ok := numRange(inner); ok {
					b.WriteString(numAlternation(lo, hi))
					i += end + 1
					continue
				}
				if !strings.Contains(inner, ",") {
					b.WriteString(regexp.QuoteMeta("{" + inner + "}"))
					i += end + 1
					continue
				}
			}
			b.WriteString("(?:")
			depth++
		case '}':
			if depth > 0 {
				b.WriteString(")")
				depth--
			} else {
				b.WriteString(`\}`)
			}
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for ; depth > 0; depth-- {
		b.WriteString(")")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func numRange(s string) (lo, hi int, ok bool) {
	a, z, found := strings.Cut(s, "..")
	if !found {
		return 0, 0, false
	}
	lo, err1 := strconv.Atoi(a)
	hi, err2 := strconv.Atoi(z)
	if err1 != nil || err2 != nil || hi < lo || hi-lo > 10000 {
		return 0, 0, false
	}
	return lo, hi, true
}

func numAlternation(lo, hi int) string {
	parts := make([]string, 0, hi-lo+1)
	for n := lo; n <= hi; n++ {
		parts = append(parts, strconv.Itoa(n))
	}
	return "(?:" + strings.Join(parts, "|") + ")"
}

// Resolve returns the properties for the file at path (absolute). The
// search stops at a root = true file or after the directory stop (if not
// empty). Unreadable files are skipped; only read errors of the scanner
// are returned.
func Resolve(path, stop string) (Props, error) {
	path = filepath.Clean(path)
	if stop != "" {
		stop = filepath.Clean(stop)
	}
	var files []*file
	var names []string
	dir := filepath.Dir(path)
	for {
		name := filepath.Join(dir, FileName)
		if data, err := os.ReadFile(name); err == nil {
			f, perr := parse(dir, data)
			if perr != nil {
				return Props{}, fmt.Errorf("editorconfig: %s: %w", name, perr)
			}
			files = append(files, f)
			names = append(names, name)
			if f.root {
				break
			}
		}
		parent := filepath.Dir(dir)
		if (stop != "" && dir == stop) || parent == dir {
			break
		}
		dir = parent
	}
	// Apply from the farthest file to the closest; later sections win.
	raw := map[string]string{}
	target := filepath.ToSlash(path)
	for i := len(files) - 1; i >= 0; i-- {
		for _, s := range files[i].sections {
			if !s.re.MatchString(target) {
				continue
			}
			for k, v := range s.props {
				raw[k] = v
			}
		}
	}
	p := fromRaw(raw)
	p.Files = names
	return p, nil
}

func fromRaw(raw map[string]string) Props {
	var p Props
	for k, v := range raw {
		if v == "unset" {
			delete(raw, k)
		}
	}
	switch v := raw["indent_style"]; v {
	case "tab", "space":
		p.IndentStyle = v
	}
	if n, err := strconv.Atoi(raw["tab_width"]); err == nil && n > 0 {
		p.TabWidth = n
	}
	switch v := raw["indent_size"]; {
	case v == "tab":
		p.IndentSize = p.TabWidth
	case v != "":
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.IndentSize = n
		}
	}
	if p.TabWidth == 0 && p.IndentSize > 0 {
		p.TabWidth = p.IndentSize
	}
	if p.IndentSize == 0 && p.IndentStyle == "tab" && p.TabWidth > 0 {
		p.IndentSize = p.TabWidth
	}
	switch v := raw["end_of_line"]; v {
	case "lf", "crlf", "cr":
		p.EndOfLine = v
	}
	switch v := raw["charset"]; v {
	case "utf-8", "utf-8-bom", "latin1", "utf-16be", "utf-16le":
		p.Charset = v
	}
	if n, err := strconv.Atoi(raw["max_line_length"]); err == nil && n > 0 {
		p.MaxLineLength = n
	}
	p.TrimTrailingWhitespace = boolProp(raw["trim_trailing_whitespace"])
	p.InsertFinalNewline = boolProp(raw["insert_final_newline"])
	return p
}

func boolProp(v string) *bool {
	switch v {
	case "true":
		b := true
		return &b
	case "false":
		b := false
		return &b
	}
	return nil
}

// Apply applies the save-time properties to content: trailing whitespace
// is removed from every line (trim_trailing_whitespace = true) and a final
// newline is added (insert_final_newline = true; the file's line ending,
// or end_of_line for files without one). insert_final_newline = false
// leaves the content alone. The result shares no memory with content when
// it differs.
func (p Props) Apply(content []byte) []byte {
	out := content
	if p.TrimTrailingWhitespace != nil && *p.TrimTrailingWhitespace {
		out = trimTrailing(out)
	}
	if p.InsertFinalNewline != nil && *p.InsertFinalNewline && len(out) > 0 {
		last := out[len(out)-1]
		if last != '\n' && last != '\r' {
			eol := "\n"
			switch {
			case bytes.Contains(out, []byte("\r\n")):
				eol = "\r\n"
			case bytes.Contains(out, []byte("\n")):
			case p.EndOfLine == "crlf":
				eol = "\r\n"
			case p.EndOfLine == "cr":
				eol = "\r"
			}
			out = append(append([]byte(nil), out...), eol...)
		}
	}
	return out
}

func trimTrailing(content []byte) []byte {
	var b bytes.Buffer
	changed := false
	rest := content
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		var line, eol []byte
		if i < 0 {
			line, rest = rest, nil
		} else {
			line, eol, rest = rest[:i], rest[i:i+1], rest[i+1:]
		}
		cr := bytes.HasSuffix(line, []byte("\r"))
		if cr {
			line = line[:len(line)-1]
		}
		trimmed := bytes.TrimRight(line, " \t")
		if len(trimmed) != len(line) {
			changed = true
		}
		b.Write(trimmed)
		if cr {
			b.WriteByte('\r')
		}
		b.Write(eol)
	}
	if !changed {
		return content
	}
	return b.Bytes()
}

// CheckCharset compares content with the charset property. Tiegel reads
// and writes UTF-8 only, so this is a read check: it returns a warning
// (or "" when the content fits).
func CheckCharset(charset string, content []byte) string {
	bom := bytes.HasPrefix(content, []byte("\xEF\xBB\xBF"))
	switch charset {
	case "":
		return ""
	case "utf-8":
		if !utf8.Valid(content) {
			return "file is not valid UTF-8 (.editorconfig: charset = utf-8)"
		}
		if bom {
			return "file starts with a UTF-8 BOM (.editorconfig: charset = utf-8)"
		}
	case "utf-8-bom":
		if !bom && len(content) > 0 {
			return "file has no UTF-8 BOM (.editorconfig: charset = utf-8-bom)"
		}
	default:
		return "charset " + charset + " is not supported; the file is edited as UTF-8"
	}
	return ""
}
