package gitcli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// LineKind is the kind of a diff line.
type LineKind byte

// Diff line kinds; the values are the unified diff prefixes.
const (
	LineContext LineKind = ' '
	LineAdded   LineKind = '+'
	LineDeleted LineKind = '-'
)

// DiffLine is one line of a hunk.
type DiffLine struct {
	Kind LineKind
	// Text is the line content without prefix and without line terminator.
	Text string
	// OldLine and NewLine are 1-based line numbers; 0 when the line does not
	// exist on that side (added lines have no OldLine, deleted no NewLine).
	OldLine, NewLine int
	// NoEOL marks that this line has no trailing newline
	// ("\ No newline at end of file" followed it).
	NoEOL bool
}

// Hunk is one "@@ -a,b +c,d @@" section.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	// Section is the text after the closing "@@" (function context).
	Section string
	Lines   []DiffLine
}

// Header returns the "@@ ... @@" line of the hunk without newline.
func (h *Hunk) Header() string {
	s := fmt.Sprintf("@@ -%s +%s @@", rangeStr(h.OldStart, h.OldLines), rangeStr(h.NewStart, h.NewLines))
	if h.Section != "" {
		s += " " + h.Section
	}
	return s
}

// Hash identifies the hunk's content: a short hex digest of its header
// and all lines (kind, text and missing-newline marker). Clients echo it
// in a selection to detect that the diff changed in between (for example
// after a format-on-save), so hunk indexes cannot silently point to other
// changes.
func (h *Hunk) Hash() string {
	d := sha256.New()
	d.Write([]byte(h.Header()))
	d.Write([]byte{'\n'})
	for _, l := range h.Lines {
		d.Write([]byte{byte(l.Kind)})
		d.Write([]byte(l.Text))
		if l.NoEOL {
			d.Write([]byte("\n\\"))
		}
		d.Write([]byte{'\n'})
	}
	return hex.EncodeToString(d.Sum(nil)[:8])
}

func rangeStr(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(n)
}

// FileDiff is the diff of one file.
type FileDiff struct {
	// OldPath and NewPath are slash separated paths without "a/" and "b/"
	// prefix. A created file has OldPath == "" and a deleted file
	// NewPath == "" (both are /dev/null in the diff).
	OldPath, NewPath string
	// OldMode and NewMode are the octal file modes ("100644") if known.
	OldMode, NewMode string
	// OldHash and NewHash are the abbreviated blob names of the index line.
	OldHash, NewHash string
	IsNew, IsDeleted bool
	IsRename, IsCopy bool
	IsBinary         bool
	// IsCombined marks a combined diff (diff --cc) of an unmerged file; its
	// hunks are not parsed.
	IsCombined bool
	// Similarity is the rename/copy similarity index in percent.
	Similarity int
	Hunks      []Hunk
}

// Path returns the new path, or the old path for deletions.
func (f *FileDiff) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// ModeChanged reports whether only or also the file mode changed.
func (f *FileDiff) ModeChanged() bool {
	return f.OldMode != "" && f.NewMode != "" && f.OldMode != f.NewMode
}

// Stats returns the number of added and deleted lines.
func (f *FileDiff) Stats() (added, deleted int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case LineAdded:
				added++
			case LineDeleted:
				deleted++
			}
		}
	}
	return added, deleted
}

// ParseDiff parses unified diff output as produced by `git diff` (including
// rename, copy, mode, binary and "\ No newline at end of file" markers).
// Plain unified diffs without "diff --git" headers are accepted as well.
func ParseDiff(data []byte) ([]FileDiff, error) {
	p := &diffParser{sc: bufio.NewScanner(bytes.NewReader(data))}
	p.sc.Buffer(make([]byte, 64*1024), 1<<30)
	return p.parse()
}

type diffParser struct {
	sc      *bufio.Scanner
	line    string
	hasLine bool // p.line holds an unconsumed line
	lineNo  int
	files   []FileDiff
	plain   bool // current file has no "diff --git" header
}

func (p *diffParser) next() bool {
	if p.hasLine {
		p.hasLine = false
		return true
	}
	if !p.sc.Scan() {
		return false
	}
	p.lineNo++
	// Content lines keep a trailing "\r"; header parsing strips it.
	p.line = p.sc.Text()
	return true
}

func (p *diffParser) unread() { p.hasLine = true }

func (p *diffParser) errorf(format string, args ...any) error {
	return fmt.Errorf("gitcli: diff line %d: %s", p.lineNo, fmt.Sprintf(format, args...))
}

func (p *diffParser) parse() ([]FileDiff, error) {
	var cur *FileDiff
	for p.next() {
		l := p.line
		switch {
		case strings.HasPrefix(l, "diff --git "):
			p.files = append(p.files, FileDiff{})
			cur = &p.files[len(p.files)-1]
			p.plain = false
			cur.OldPath, cur.NewPath = parseGitHeaderPaths(strings.TrimPrefix(l, "diff --git "))
		case strings.HasPrefix(l, "diff --cc ") || strings.HasPrefix(l, "diff --combined "):
			p.files = append(p.files, FileDiff{IsCombined: true})
			cur = &p.files[len(p.files)-1]
			p.plain = false
			name := strings.TrimPrefix(strings.TrimPrefix(l, "diff --cc "), "diff --combined ")
			cur.OldPath, cur.NewPath = unquotePath(name), unquotePath(name)
			p.skipCombined()
		case strings.HasPrefix(l, "--- ") && (cur == nil || len(cur.Hunks) > 0 || cur.IsCombined):
			// Plain unified diff without git header.
			p.files = append(p.files, FileDiff{})
			cur = &p.files[len(p.files)-1]
			p.plain = true
			p.unread()
			if err := p.parseFileHeaderLine(cur); err != nil {
				return nil, err
			}
		case cur == nil:
			// Preamble (e.g. commit message of format-patch); ignore.
		case strings.HasPrefix(l, "@@ "):
			h, err := p.parseHunk(l)
			if err != nil {
				return nil, err
			}
			cur.Hunks = append(cur.Hunks, h)
		default:
			p.unread()
			if err := p.parseFileHeaderLine(cur); err != nil {
				return nil, err
			}
		}
	}
	if err := p.sc.Err(); err != nil {
		return nil, fmt.Errorf("gitcli: read diff: %w", err)
	}
	return p.files, nil
}

// skipCombined skips the body of a combined diff.
func (p *diffParser) skipCombined() {
	for p.next() {
		if strings.HasPrefix(p.line, "diff ") {
			p.unread()
			return
		}
	}
}

// parseFileHeaderLine handles one extended header line of cur.
func (p *diffParser) parseFileHeaderLine(cur *FileDiff) error {
	if !p.next() {
		return nil
	}
	l := strings.TrimSuffix(p.line, "\r")
	switch {
	case strings.HasPrefix(l, "--- "):
		path := strings.TrimPrefix(l, "--- ")
		if path == "/dev/null" {
			cur.OldPath = ""
			cur.IsNew = true
		} else {
			cur.OldPath = p.stripPath(unquotePath(trimTimestamp(path)))
		}
	case strings.HasPrefix(l, "+++ "):
		path := strings.TrimPrefix(l, "+++ ")
		if path == "/dev/null" {
			cur.NewPath = ""
			cur.IsDeleted = true
		} else {
			cur.NewPath = p.stripPath(unquotePath(trimTimestamp(path)))
		}
	case strings.HasPrefix(l, "old mode "):
		cur.OldMode = strings.TrimPrefix(l, "old mode ")
	case strings.HasPrefix(l, "new mode "):
		cur.NewMode = strings.TrimPrefix(l, "new mode ")
	case strings.HasPrefix(l, "new file mode "):
		cur.IsNew = true
		cur.NewMode = strings.TrimPrefix(l, "new file mode ")
		cur.OldPath = ""
	case strings.HasPrefix(l, "deleted file mode "):
		cur.IsDeleted = true
		cur.OldMode = strings.TrimPrefix(l, "deleted file mode ")
		cur.NewPath = ""
	case strings.HasPrefix(l, "rename from "):
		cur.IsRename = true
		cur.OldPath = unquotePath(strings.TrimPrefix(l, "rename from "))
	case strings.HasPrefix(l, "rename to "):
		cur.IsRename = true
		cur.NewPath = unquotePath(strings.TrimPrefix(l, "rename to "))
	case strings.HasPrefix(l, "copy from "):
		cur.IsCopy = true
		cur.OldPath = unquotePath(strings.TrimPrefix(l, "copy from "))
	case strings.HasPrefix(l, "copy to "):
		cur.IsCopy = true
		cur.NewPath = unquotePath(strings.TrimPrefix(l, "copy to "))
	case strings.HasPrefix(l, "similarity index "), strings.HasPrefix(l, "dissimilarity index "):
		v := strings.TrimSuffix(l[strings.LastIndex(l, " ")+1:], "%")
		n, err := strconv.Atoi(v)
		if err != nil {
			return p.errorf("bad similarity %q", l)
		}
		cur.Similarity = n
	case strings.HasPrefix(l, "index "):
		parseIndexLine(cur, strings.TrimPrefix(l, "index "))
	case strings.HasPrefix(l, "Binary files ") || l == "GIT binary patch":
		cur.IsBinary = true
		if l == "GIT binary patch" {
			p.skipBinaryPatch()
		}
	default:
		// Unknown or trailing lines (e.g. "-- \n2.39.2" of format-patch).
	}
	return nil
}

// skipBinaryPatch skips the base85 payload of a binary patch.
func (p *diffParser) skipBinaryPatch() {
	for p.next() {
		if strings.HasPrefix(p.line, "diff ") {
			p.unread()
			return
		}
	}
}

func parseIndexLine(cur *FileDiff, s string) {
	hashes, mode, _ := strings.Cut(s, " ")
	if a, b, ok := strings.Cut(hashes, ".."); ok {
		cur.OldHash, cur.NewHash = a, b
	}
	if mode != "" {
		if cur.OldMode == "" {
			cur.OldMode = mode
		}
		if cur.NewMode == "" {
			cur.NewMode = mode
		}
	}
}

// parseHunk parses a hunk starting at header line h.
func (p *diffParser) parseHunk(header string) (Hunk, error) {
	var h Hunk
	rest := strings.TrimPrefix(header, "@@ ")
	ranges, section, ok := strings.Cut(rest, " @@")
	if !ok {
		return h, p.errorf("bad hunk header %q", header)
	}
	h.Section = strings.TrimPrefix(section, " ")
	oldR, newR, ok := strings.Cut(ranges, " ")
	if !ok || !strings.HasPrefix(oldR, "-") || !strings.HasPrefix(newR, "+") {
		return h, p.errorf("bad hunk header %q", header)
	}
	var err1, err2 error
	h.OldStart, h.OldLines, err1 = parseRange(oldR[1:])
	h.NewStart, h.NewLines, err2 = parseRange(newR[1:])
	if err1 != nil || err2 != nil {
		return h, p.errorf("bad hunk header %q", header)
	}
	oldLeft, newLeft := h.OldLines, h.NewLines
	oldNo, newNo := h.OldStart, h.NewStart
	if h.OldLines == 0 {
		oldNo++
	}
	if h.NewLines == 0 {
		newNo++
	}
	for oldLeft > 0 || newLeft > 0 {
		if !p.next() {
			return h, p.errorf("hunk %q truncated", header)
		}
		l := p.line
		if strings.HasPrefix(l, "\\") {
			markNoEOL(&h)
			continue
		}
		if l == "" {
			// Some tools strip the trailing space of empty context lines.
			l = " "
		}
		dl := DiffLine{Kind: LineKind(l[0]), Text: l[1:]}
		switch dl.Kind {
		case LineContext:
			dl.OldLine, dl.NewLine = oldNo, newNo
			oldNo++
			newNo++
			oldLeft--
			newLeft--
		case LineDeleted:
			dl.OldLine = oldNo
			oldNo++
			oldLeft--
		case LineAdded:
			dl.NewLine = newNo
			newNo++
			newLeft--
		default:
			return h, p.errorf("unexpected line %q in hunk", l)
		}
		if oldLeft < 0 || newLeft < 0 {
			return h, p.errorf("hunk %q has more lines than announced", header)
		}
		h.Lines = append(h.Lines, dl)
	}
	// A "\ No newline" marker may follow the last line.
	if p.next() {
		if strings.HasPrefix(p.line, "\\") {
			markNoEOL(&h)
		} else {
			p.unread()
		}
	}
	return h, nil
}

func markNoEOL(h *Hunk) {
	if n := len(h.Lines); n > 0 {
		h.Lines[n-1].NoEOL = true
	}
}

func parseRange(s string) (start, n int, err error) {
	a, b, hasCount := strings.Cut(s, ",")
	start, err = strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	n = 1
	if hasCount {
		n, err = strconv.Atoi(b)
	}
	return start, n, err
}

// trimTimestamp removes a tab separated timestamp of plain diff -u output.
func trimTimestamp(s string) string {
	if strings.HasPrefix(s, `"`) {
		return s
	}
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		return s[:i]
	}
	return s
}

// stripPath removes the diff prefix: "a/" and "b/" for git diffs, the
// first path component (like patch -p1) for plain unified diffs.
func (p *diffParser) stripPath(s string) string {
	if p.plain {
		if i := strings.IndexByte(s, '/'); i >= 0 {
			return s[i+1:]
		}
		return s
	}
	return stripPrefix(s)
}

// stripPrefix removes the "a/" or "b/" prefix of git diff paths.
func stripPrefix(s string) string {
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}

// parseGitHeaderPaths splits "a/old b/new" of a diff --git line. Paths
// with spaces are ambiguous; the result is overwritten by ---/+++ or
// rename headers when present.
func parseGitHeaderPaths(s string) (oldPath, newPath string) {
	if strings.HasPrefix(s, `"`) {
		a, rest, ok := splitQuoted(s)
		if !ok {
			return "", ""
		}
		rest = strings.TrimPrefix(rest, " ")
		return stripPrefix(a), stripPrefix(unquotePath(rest))
	}
	if strings.HasSuffix(s, `"`) {
		if i := strings.LastIndex(s, ` "`); i >= 0 {
			return stripPrefix(s[:i]), stripPrefix(unquotePath(s[i+1:]))
		}
	}
	// Same name on both sides: "a/P b/P".
	if (len(s)-1)%2 == 0 {
		half := (len(s) - 1) / 2
		a, b := s[:half], s[half+1:]
		if s[half] == ' ' && len(a) > 2 && a[2:] == b[2:] {
			return stripPrefix(a), stripPrefix(b)
		}
	}
	if i := strings.Index(s, " b/"); i >= 0 {
		return stripPrefix(s[:i]), stripPrefix(s[i+1:])
	}
	a, b, _ := strings.Cut(s, " ")
	return stripPrefix(a), stripPrefix(b)
}

// splitQuoted splits a leading C-quoted string from s.
func splitQuoted(s string) (unq, rest string, ok bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return unquotePath(s[:i+1]), s[i+1:], true
		}
	}
	return "", "", false
}

// unquotePath decodes a C-style quoted path as written by git.
func unquotePath(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch c = s[i]; c {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '0', '1', '2', '3':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i:i+3], 8, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// quotePath quotes a path for a patch header when needed (C-style, like
// git).
func quotePath(prefix, p string) string {
	need := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == '"' || c == '\\' || c == 0x7f {
			need = true
			break
		}
	}
	if !need {
		return prefix + p
	}
	var b strings.Builder
	b.WriteByte('"')
	s := prefix + p
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
