package gitcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// RangeKind is the kind of a gutter change marker.
type RangeKind int

// Gutter marker kinds.
const (
	RangeAdded    RangeKind = iota // lines exist only in the buffer
	RangeModified                  // lines differ
	RangeDeleted                   // base lines were removed before Start
)

// String returns the kind name.
func (k RangeKind) String() string {
	switch k {
	case RangeAdded:
		return "added"
	case RangeModified:
		return "modified"
	case RangeDeleted:
		return "deleted"
	}
	return "unknown"
}

// LineRange is a changed region of a buffer compared with a base version.
type LineRange struct {
	Kind RangeKind
	// Start and End are 0-based buffer lines [Start, End). For deletions
	// Start == End is the line before which base lines were removed (it
	// may equal the line count when lines at the end were removed).
	Start, End int
	// BaseStart and BaseEnd are the corresponding 0-based base lines.
	BaseStart, BaseEnd int
}

// LineChanges compares a buffer with a base version (both arbitrary bytes)
// and returns the changed regions in buffer order. Lines are compared
// including their terminators, so a changed or missing final newline marks
// the last line modified.
func LineChanges(base, current []byte) []LineRange {
	changes := DiffLines(SplitLines(base), SplitLines(current))
	out := make([]LineRange, len(changes))
	for i, c := range changes {
		r := LineRange{Start: c.NewStart, End: c.NewEnd, BaseStart: c.OldStart, BaseEnd: c.OldEnd}
		switch {
		case c.OldStart == c.OldEnd:
			r.Kind = RangeAdded
		case c.NewStart == c.NewEnd:
			r.Kind = RangeDeleted
		default:
			r.Kind = RangeModified
		}
		out[i] = r
	}
	return out
}

// Replacement replaces bytes [StartOffset, EndOffset) of a buffer (which
// are the lines [StartLine, EndLine)) with Text.
type Replacement struct {
	StartLine, EndLine     int
	StartOffset, EndOffset int
	Text                   []byte
}

// Rollback returns the edit that restores the base content of r in
// current. r must come from [LineChanges] for the same base and current.
func Rollback(base, current []byte, r LineRange) (Replacement, error) {
	baseLines := SplitLines(base)
	curLines := SplitLines(current)
	if r.Start < 0 || r.End < r.Start || r.End > len(curLines) ||
		r.BaseStart < 0 || r.BaseEnd < r.BaseStart || r.BaseEnd > len(baseLines) {
		return Replacement{}, fmt.Errorf("gitcli: rollback range %+v out of bounds", r)
	}
	rep := Replacement{StartLine: r.Start, EndLine: r.End}
	for i := 0; i < r.End; i++ {
		if i < r.Start {
			rep.StartOffset += len(curLines[i])
		}
		rep.EndOffset += len(curLines[i])
	}
	rep.Text = []byte(strings.Join(baseLines[r.BaseStart:r.BaseEnd], ""))
	return rep, nil
}

// Apply returns buf with the replacement applied.
func (rep Replacement) Apply(buf []byte) []byte {
	out := make([]byte, 0, len(buf)-(rep.EndOffset-rep.StartOffset)+len(rep.Text))
	out = append(out, buf[:rep.StartOffset]...)
	out = append(out, rep.Text...)
	return append(out, buf[rep.EndOffset:]...)
}

// GutterBase selects the version gutter markers compare against.
type GutterBase int

// Gutter base versions.
const (
	BaseHEAD  GutterBase = iota // last commit
	BaseIndex                   // staged version
)

// BaseContent returns the content of path in HEAD or the index with the
// work tree filters (eol conversion, smudge) applied, so it can be
// compared with a buffer loaded from disk. For a path that does not exist
// in the base, ok is false and the whole buffer counts as added.
func (r *Repo) BaseContent(ctx context.Context, path string, base GutterBase) (content []byte, ok bool, err error) {
	obj := ":0:" + path
	if base == BaseHEAD {
		obj = "HEAD:" + path
	}
	out, err := r.run(ctx, runOpts{}, "cat-file", "--filters", obj)
	if err != nil {
		if isNotFound(stderrOf(err)) || exitCode(err) == 128 {
			// Distinguish "missing" from real failures by checking existence.
			if _, verr := r.run(ctx, runOpts{}, "cat-file", "-e", obj); verr != nil {
				return nil, false, nil
			}
		}
		return nil, false, fmt.Errorf("gitcli: base content %s: %w", obj, err)
	}
	return out, true, nil
}

// GutterChanges compares buf (the editor content of path) with the HEAD or
// index version of path.
func (r *Repo) GutterChanges(ctx context.Context, path string, buf []byte, base GutterBase) ([]LineRange, error) {
	content, _, err := r.BaseContent(ctx, path, base)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return LineChanges(content, buf), nil
}
