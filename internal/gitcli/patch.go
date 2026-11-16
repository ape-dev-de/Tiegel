package gitcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptySelection is returned by [FileDiff.Patch] when the selection
// contains no change.
var ErrEmptySelection = errors.New("gitcli: selection contains no changes")

// HunkSelection selects one hunk of a [FileDiff] for a patch.
type HunkSelection struct {
	// Hunk is the index into FileDiff.Hunks.
	Hunk int
	// Lines optionally restricts the hunk to these indexes into Hunk.Lines.
	// Only added and deleted lines are relevant; context lines are always
	// kept. nil selects the whole hunk.
	Lines []int
}

// SelectAll returns a selection of every hunk of f.
func (f *FileDiff) SelectAll() []HunkSelection {
	sel := make([]HunkSelection, len(f.Hunks))
	for i := range f.Hunks {
		sel[i].Hunk = i
	}
	return sel
}

// Patch builds a patch that applies the selected hunks (or lines) of f to
// the old side of the diff, suitable for `git apply` / [Repo.ApplyPatch].
//
// Unselected added lines are dropped, unselected deleted lines become
// context, and the hunk headers are recomputed. A deletion that is only
// partially selected is emitted as a modification so the file survives. A
// rename or mode change without selected hunks yields a header-only patch.
func (f *FileDiff) Patch(sel []HunkSelection) ([]byte, error) {
	if f.IsBinary || f.IsCombined {
		return nil, fmt.Errorf("gitcli: cannot build a patch for %s (binary or combined diff)", f.Path())
	}
	var hunks []Hunk
	for _, s := range sel {
		if s.Hunk < 0 || s.Hunk >= len(f.Hunks) {
			return nil, fmt.Errorf("gitcli: hunk %d out of range for %s", s.Hunk, f.Path())
		}
		h, ok := filterHunk(&f.Hunks[s.Hunk], s.Lines)
		if ok {
			hunks = append(hunks, h)
		}
	}
	sortHunks(hunks)
	full := len(hunks) == len(f.Hunks) && f.coversAll(sel)
	metaOnly := f.IsRename || f.IsCopy || f.ModeChanged() || (f.IsNew && len(f.Hunks) == 0)
	if len(hunks) == 0 && !metaOnly {
		return nil, ErrEmptySelection
	}

	var b strings.Builder
	oldP, newP := f.OldPath, f.NewPath
	deleted := f.IsDeleted
	if deleted && !full {
		// Partial deletion: keep the file, remove only the selected lines.
		deleted = false
		newP = oldP
	}
	hdrOld, hdrNew := oldP, newP
	if hdrOld == "" {
		hdrOld = hdrNew
	}
	if hdrNew == "" {
		hdrNew = hdrOld
	}
	fmt.Fprintf(&b, "diff --git %s %s\n", quotePath("a/", hdrOld), quotePath("b/", hdrNew))
	switch {
	case f.IsNew:
		fmt.Fprintf(&b, "new file mode %s\n", modeOr(f.NewMode))
	case deleted:
		fmt.Fprintf(&b, "deleted file mode %s\n", modeOr(f.OldMode))
	case f.ModeChanged():
		fmt.Fprintf(&b, "old mode %s\nnew mode %s\n", f.OldMode, f.NewMode)
	}
	if f.IsRename || f.IsCopy {
		verb := "rename"
		if f.IsCopy {
			verb = "copy"
		}
		fmt.Fprintf(&b, "similarity index %d%%\n", f.Similarity)
		fmt.Fprintf(&b, "%s from %s\n%s to %s\n", verb, quotePath("", f.OldPath), verb, quotePath("", f.NewPath))
	}
	if len(hunks) > 0 {
		if f.IsNew {
			b.WriteString("--- /dev/null\n")
		} else {
			fmt.Fprintf(&b, "--- %s\n", quotePath("a/", oldP))
		}
		if deleted {
			b.WriteString("+++ /dev/null\n")
		} else {
			fmt.Fprintf(&b, "+++ %s\n", quotePath("b/", newP))
		}
	}
	delta := 0
	for i := range hunks {
		h := &hunks[i]
		oldFirst := h.OldStart
		if h.OldLines == 0 {
			oldFirst++
		}
		newFirst := oldFirst + delta
		h.NewStart = newFirst
		if h.NewLines == 0 {
			h.NewStart = newFirst - 1
		}
		delta += h.NewLines - h.OldLines
		b.WriteString(h.Header())
		b.WriteByte('\n')
		for _, l := range h.Lines {
			b.WriteByte(byte(l.Kind))
			b.WriteString(l.Text)
			b.WriteByte('\n')
			if l.NoEOL {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return []byte(b.String()), nil
}

func modeOr(m string) string {
	if m == "" {
		return "100644"
	}
	return m
}

// coversAll reports whether sel selects every changed line of f.
func (f *FileDiff) coversAll(sel []HunkSelection) bool {
	whole := map[int]bool{}
	lines := map[int]map[int]bool{}
	for _, s := range sel {
		if s.Lines == nil {
			whole[s.Hunk] = true
			continue
		}
		if lines[s.Hunk] == nil {
			lines[s.Hunk] = map[int]bool{}
		}
		for _, i := range s.Lines {
			lines[s.Hunk][i] = true
		}
	}
	for hi, h := range f.Hunks {
		if whole[hi] {
			continue
		}
		for li, l := range h.Lines {
			if l.Kind != LineContext && !lines[hi][li] {
				return false
			}
		}
	}
	return true
}

func sortHunks(h []Hunk) {
	for i := 1; i < len(h); i++ {
		for j := i; j > 0 && h[j].OldStart < h[j-1].OldStart; j-- {
			h[j], h[j-1] = h[j-1], h[j]
		}
	}
}

// filterHunk returns h restricted to the selected change lines. ok is
// false when no change remains.
func filterHunk(h *Hunk, lines []int) (Hunk, bool) {
	var keep map[int]bool
	if lines != nil {
		keep = make(map[int]bool, len(lines))
		for _, i := range lines {
			keep[i] = true
		}
	}
	out := Hunk{OldStart: h.OldStart, Section: h.Section}
	changes := 0
	for i, l := range h.Lines {
		selected := keep == nil || keep[i]
		switch {
		case l.Kind == LineContext:
			out.Lines = append(out.Lines, l)
		case l.Kind == LineAdded && selected:
			out.Lines = append(out.Lines, l)
			changes++
		case l.Kind == LineAdded:
			// dropped
		case l.Kind == LineDeleted && selected:
			out.Lines = append(out.Lines, l)
			changes++
		default: // unselected deletion stays as context
			l.Kind = LineContext
			out.Lines = append(out.Lines, l)
		}
	}
	if changes == 0 {
		return Hunk{}, false
	}
	out.Lines = fixNoEOL(out.Lines)
	for _, l := range out.Lines {
		if l.Kind != LineAdded {
			out.OldLines++
		}
		if l.Kind != LineDeleted {
			out.NewLines++
		}
	}
	return out, true
}

// fixNoEOL repairs "no newline at end of file" markers after a partial
// selection: a line without newline must be the last line of its side.
// A context line without newline that is followed by other lines is turned
// into a deletion of the old text plus an addition with newline; markers
// on the new side are dropped when new side lines follow.
func fixNoEOL(lines []DiffLine) []DiffLine {
	out := make([]DiffLine, 0, len(lines)+1)
	for i, l := range lines {
		if l.NoEOL {
			laterNew, laterOld := false, false
			for _, m := range lines[i+1:] {
				if m.Kind != LineDeleted {
					laterNew = true
				}
				if m.Kind != LineAdded {
					laterOld = true
				}
			}
			switch l.Kind {
			case LineContext:
				if laterNew {
					del := l
					del.Kind = LineDeleted
					add := l
					add.Kind = LineAdded
					add.NoEOL = false
					// The old side keeps its missing newline; the deleted
					// line must come last on the old side, so emit the
					// addition after all remaining deletions.
					out = append(out, del)
					rest := lines[i+1:]
					var dels, others []DiffLine
					for _, m := range rest {
						if m.Kind == LineDeleted {
							dels = append(dels, m)
						} else {
							others = append(others, m)
						}
					}
					out = append(out, dels...)
					out = append(out, add)
					out = append(out, others...)
					return out
				}
			case LineAdded:
				if laterNew {
					l.NoEOL = false
				}
			case LineDeleted:
				if laterOld {
					l.NoEOL = false
				}
			}
		}
		out = append(out, l)
	}
	return out
}

// ApplyOptions configures [Repo.ApplyPatch].
type ApplyOptions struct {
	// Cached applies to the index only (git apply --cached).
	Cached bool
	// Reverse applies the patch in reverse (unstage / revert).
	Reverse bool
	// Check only verifies that the patch applies.
	Check bool
	// IndexFile uses an alternative index file (GIT_INDEX_FILE).
	IndexFile string
}

// ApplyPatch runs `git apply` with patch on stdin.
func (r *Repo) ApplyPatch(ctx context.Context, patch []byte, opts ApplyOptions) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if opts.Cached {
		args = append(args, "--cached")
	}
	if opts.Reverse {
		args = append(args, "--reverse")
	}
	if opts.Check {
		args = append(args, "--check")
	}
	args = append(args, "-")
	o := runOpts{stdin: patch}
	if opts.IndexFile != "" {
		o.env = []string{"GIT_INDEX_FILE=" + opts.IndexFile}
	}
	if _, err := r.run(ctx, o, args...); err != nil {
		return fmt.Errorf("gitcli: apply patch: %w", err)
	}
	return nil
}

// DiffOptions configures [Repo.Diff].
type DiffOptions struct {
	// Base is the revision to compare against. Empty compares the index
	// (or with Staged the index against HEAD). With Base set and Staged
	// false, Base is compared against the work tree. "HEAD" works for
	// unborn branches as well (the empty tree is used).
	Base string
	// Target is an optional second revision (Base..Target).
	Target string
	// Staged compares the index against Base (default HEAD): --cached.
	Staged bool
	// Context is the number of context lines; 0 means git's default (3).
	Context int
	// NoRenames disables rename detection.
	NoRenames bool
	// Paths limits the diff.
	Paths []string
}

// Diff runs `git diff` and parses the result.
func (r *Repo) Diff(ctx context.Context, opts DiffOptions) ([]FileDiff, error) {
	out, err := r.RawDiff(ctx, opts)
	if err != nil {
		return nil, err
	}
	return ParseDiff(out)
}

// RawDiff runs `git diff` and returns the unparsed unified diff.
func (r *Repo) RawDiff(ctx context.Context, opts DiffOptions) ([]byte, error) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=short"}
	if opts.NoRenames {
		args = append(args, "--no-renames")
	} else {
		args = append(args, "--find-renames")
	}
	if opts.Context > 0 {
		args = append(args, fmt.Sprintf("-U%d", opts.Context))
	}
	base := opts.Base
	if base == "HEAD" || (base == "" && opts.Staged) {
		b, err := r.headOrEmptyTree(ctx)
		if err != nil {
			return nil, err
		}
		base = b
	}
	if opts.Staged {
		args = append(args, "--cached")
	}
	if base != "" {
		args = append(args, base)
	}
	if opts.Target != "" {
		args = append(args, opts.Target)
	}
	args = append(args, "--")
	args = append(args, opts.Paths...)
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: diff: %w", err)
	}
	return out, nil
}

// DiffUntracked returns the diff of an untracked file as a creation
// (git diff --no-index /dev/null path). It returns nil for an empty file.
func (r *Repo) DiffUntracked(ctx context.Context, path string) (*FileDiff, error) {
	out, err := r.run(ctx, runOpts{okCodes: []int{1}}, "diff", "--no-index", "--no-color", "--no-ext-diff",
		"--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--", "/dev/null", path)
	if err != nil {
		return nil, fmt.Errorf("gitcli: diff %s: %w", path, err)
	}
	files, err := ParseDiff(out)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	f := files[0]
	f.NewPath, f.OldPath, f.IsNew = path, "", true
	return &f, nil
}

// Stage adds whole paths to the index (git add -A, also for deletions).
func (r *Repo) Stage(ctx context.Context, paths ...string) error {
	return r.addPaths(ctx, "", paths)
}

func (r *Repo) addPaths(ctx context.Context, indexFile string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	o := runOpts{}
	if indexFile != "" {
		o.env = []string{"GIT_INDEX_FILE=" + indexFile}
	}
	args := append([]string{"add", "-A", "--"}, paths...)
	if _, err := r.run(ctx, o, args...); err != nil {
		return fmt.Errorf("gitcli: add: %w", err)
	}
	return nil
}

// Unstage resets the index entries of paths to HEAD (removes them from the
// index on an unborn branch). The work tree is not touched.
func (r *Repo) Unstage(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if _, err := r.resolve(ctx, "HEAD"); errors.Is(err, ErrNotFound) {
		args := append([]string{"rm", "-q", "--cached", "-r", "--ignore-unmatch", "--"}, paths...)
		if _, err := r.run(ctx, runOpts{}, args...); err != nil {
			return fmt.Errorf("gitcli: unstage: %w", err)
		}
		return nil
	}
	args := append([]string{"reset", "-q", "HEAD", "--"}, paths...)
	if _, err := r.run(ctx, runOpts{}, args...); err != nil {
		return fmt.Errorf("gitcli: unstage: %w", err)
	}
	return nil
}

// StageSelection stages selected hunks/lines of a work tree diff (a diff
// of the index against the work tree, see [Repo.Diff] with empty Base).
func (r *Repo) StageSelection(ctx context.Context, f *FileDiff, sel []HunkSelection) error {
	patch, err := f.Patch(sel)
	if err != nil {
		return err
	}
	return r.ApplyPatch(ctx, patch, ApplyOptions{Cached: true})
}

// UnstageSelection removes selected hunks/lines of a staged diff (a diff
// with Staged: true) from the index.
func (r *Repo) UnstageSelection(ctx context.Context, f *FileDiff, sel []HunkSelection) error {
	patch, err := f.Invert().Patch(sel)
	if err != nil {
		return err
	}
	return r.ApplyPatch(ctx, patch, ApplyOptions{Cached: true})
}

// DiscardSelection reverts selected hunks/lines of a work tree diff (index
// against work tree) in the work tree file.
func (r *Repo) DiscardSelection(ctx context.Context, f *FileDiff, sel []HunkSelection) error {
	patch, err := f.Invert().Patch(sel)
	if err != nil {
		return err
	}
	return r.ApplyPatch(ctx, patch, ApplyOptions{})
}

// Invert returns the reverse diff (new → old). Line indexes inside hunks
// are preserved, so a [HunkSelection] made on f is valid for the result;
// f.Invert().Patch(sel) undoes exactly the selected changes on the new
// side.
func (f *FileDiff) Invert() *FileDiff {
	inv := *f
	inv.OldPath, inv.NewPath = f.NewPath, f.OldPath
	inv.OldMode, inv.NewMode = f.NewMode, f.OldMode
	inv.OldHash, inv.NewHash = f.NewHash, f.OldHash
	inv.IsNew, inv.IsDeleted = f.IsDeleted, f.IsNew
	inv.Hunks = make([]Hunk, len(f.Hunks))
	for i, h := range f.Hunks {
		ih := Hunk{OldStart: h.NewStart, OldLines: h.NewLines, NewStart: h.OldStart, NewLines: h.OldLines, Section: h.Section}
		ih.Lines = make([]DiffLine, len(h.Lines))
		for j, l := range h.Lines {
			il := l
			il.OldLine, il.NewLine = l.NewLine, l.OldLine
			switch l.Kind {
			case LineAdded:
				il.Kind = LineDeleted
			case LineDeleted:
				il.Kind = LineAdded
			}
			ih.Lines[j] = il
		}
		inv.Hunks[i] = ih
	}
	return &inv
}
