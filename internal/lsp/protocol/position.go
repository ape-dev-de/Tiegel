// Package protocol contains the hand-written subset of Language Server
// Protocol 3.17 types used by Tiegel, plus helpers for converting between
// byte offsets and the UTF-16 based positions LSP uses.
package protocol

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Position is a zero-based line and character offset. Character counts
// UTF-16 code units (the LSP default position encoding).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open range [Start, End) in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// DocumentURI is a URI identifying a document, usually file://.
type DocumentURI string

// URIFromPath converts an absolute file system path into a file URI.
func URIFromPath(path string) DocumentURI {
	p := filepath.ToSlash(path)
	if runtime.GOOS == "windows" || (len(p) >= 2 && p[1] == ':') {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
	}
	u := url.URL{Scheme: "file", Path: p}
	return DocumentURI(u.String())
}

// Path returns the file system path of a file URI. It returns an error for
// other schemes.
func (u DocumentURI) Path() (string, error) {
	parsed, err := url.Parse(string(u))
	if err != nil {
		return "", fmt.Errorf("protocol: parsing uri %q: %w", u, err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("protocol: uri %q is not a file uri", u)
	}
	p := parsed.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // Windows drive letter
	}
	return filepath.FromSlash(p), nil
}

// Location is a range inside a document.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// LocationLink is a link from an origin range to a target location.
type LocationLink struct {
	OriginSelectionRange *Range      `json:"originSelectionRange,omitempty"`
	TargetURI            DocumentURI `json:"targetUri"`
	TargetRange          Range       `json:"targetRange"`
	TargetSelectionRange Range       `json:"targetSelectionRange"`
}

// UTF16Len returns the length of s in UTF-16 code units. Invalid UTF-8
// bytes count as one unit each.
func UTF16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		n += runeUTF16Len(r, size)
		i += size
	}
	return n
}

func runeUTF16Len(r rune, size int) int {
	if r == utf8.RuneError && size <= 1 {
		return 1
	}
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// ByteToUTF16 converts a byte offset within line to a UTF-16 column. Offsets
// beyond the line are clamped; an offset inside a multi-byte character is
// rounded down to the character start.
func ByteToUTF16(line string, byteOff int) int {
	if byteOff > len(line) {
		byteOff = len(line)
	}
	col := 0
	for i := 0; i < byteOff; {
		r, size := utf8.DecodeRuneInString(line[i:])
		if i+size > byteOff {
			break
		}
		col += runeUTF16Len(r, size)
		i += size
	}
	return col
}

// UTF16ToByte converts a UTF-16 column within line to a byte offset.
// Columns beyond the line are clamped to len(line); a column pointing into
// the middle of a surrogate pair is rounded down to the character start.
func UTF16ToByte(line string, col int) int {
	units := 0
	for i := 0; i < len(line); {
		if units >= col {
			return i
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		u := runeUTF16Len(r, size)
		if units+u > col {
			return i
		}
		units += u
		i += size
	}
	return len(line)
}

// lineStart returns the byte offset at which zero-based line starts, or -1
// if text has fewer lines. Lines are terminated by "\n" ("\r\n" works since
// the "\r" is treated as part of the line content).
func lineStart(text string, line int) int {
	off := 0
	for l := 0; l < line; l++ {
		i := strings.IndexByte(text[off:], '\n')
		if i < 0 {
			return -1
		}
		off += i + 1
	}
	return off
}

func lineAt(text string, start int) string {
	rest := text[start:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSuffix(rest, "\r")
}

// OffsetOf converts pos to a byte offset into text. Characters beyond the
// end of a line are clamped to the line end (as the LSP spec requires); a
// line beyond the end of the text is an error, except for the position
// directly after the last line.
func OffsetOf(text string, pos Position) (int, error) {
	if pos.Line < 0 || pos.Character < 0 {
		return 0, fmt.Errorf("protocol: negative position %d:%d", pos.Line, pos.Character)
	}
	start := lineStart(text, pos.Line)
	if start < 0 {
		return 0, fmt.Errorf("protocol: line %d out of range", pos.Line)
	}
	return start + UTF16ToByte(lineAt(text, start), pos.Character), nil
}

// PositionOf converts a byte offset into text to a Position. Offsets beyond
// the text are clamped to its end.
func PositionOf(text string, offset int) Position {
	if offset > len(text) {
		offset = len(text)
	}
	if offset < 0 {
		offset = 0
	}
	line := strings.Count(text[:offset], "\n")
	start := strings.LastIndexByte(text[:offset], '\n') + 1
	return Position{Line: line, Character: ByteToUTF16(text[start:offset], offset-start)}
}

// ApplyChanges applies content changes in order to text and returns the
// result. A change without range replaces the whole document.
func ApplyChanges(text string, changes []TextDocumentContentChangeEvent) (string, error) {
	for i, ch := range changes {
		if ch.Range == nil {
			text = ch.Text
			continue
		}
		start, err := OffsetOf(text, ch.Range.Start)
		if err != nil {
			return "", fmt.Errorf("protocol: change %d start: %w", i, err)
		}
		end, err := OffsetOf(text, ch.Range.End)
		if err != nil {
			return "", fmt.Errorf("protocol: change %d end: %w", i, err)
		}
		if end < start {
			return "", fmt.Errorf("protocol: change %d has end before start", i)
		}
		text = text[:start] + ch.Text + text[end:]
	}
	return text, nil
}
