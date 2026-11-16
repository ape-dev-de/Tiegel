package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// TextChange replaces the bytes [Start, End) of a text with Text.
type TextChange struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// File operation kinds of FileOp.
const (
	FileOpEdit   = "edit"
	FileOpCreate = "create"
	FileOpRename = "rename"
	FileOpDelete = "delete"
)

// FileOp is one step of a multi-file change (search.replaceInProject, LSP
// workspace edits). Edits of open documents become one undoable edit of the
// shared buffer each; closed files are rewritten atomically with Local
// History revisions before and after the change.
type FileOp struct {
	// Kind is FileOpEdit (default), FileOpCreate, FileOpRename or
	// FileOpDelete.
	Kind string
	// Path is the file (absolute or relative to the project root).
	Path string
	// NewPath is the target of a rename.
	NewPath string
	// Version, if set, must equal the version of the open document
	// (otherwise the step fails with CodeConflict). Ignored for closed
	// files.
	Version *uint64
	// Changes computes the edits against the current text (the open
	// buffer or the file content) while it is locked. Changes must not
	// overlap; inserts at the same offset keep their order.
	Changes func(text []byte) ([]TextChange, error)
	// Overwrite and IgnoreIfExists apply to create and rename;
	// IgnoreIfNotExists and Recursive to delete.
	Overwrite, IgnoreIfExists, IgnoreIfNotExists, Recursive bool
}

// EditStepResult reports one applied FileOp.
type EditStepResult struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	File    string `json:"file"`
	NewPath string `json:"newPath,omitempty"`
	// Open is true if the step changed an open document (undoable, not
	// saved).
	Open bool `json:"open,omitempty"`
	// Edits is the number of applied text changes.
	Edits int `json:"edits,omitempty"`
	// Version is the document version after an edit of an open document.
	Version uint64 `json:"version,omitempty"`
	// Revision is the Local History revision of the new content of a
	// closed file (0 without project).
	Revision uint64 `json:"revision,omitempty"`
	// Skipped is set when the step changed nothing (no edits, file exists
	// and ignoreIfExists, ...).
	Skipped bool `json:"skipped,omitempty"`
}

// ApplyFileOps applies ops in order and stops at the first failing step.
// It returns the results of the applied steps and the error of the failed
// one (which names the step). label marks the Local History revisions of
// closed files ("<label>" for the new content, "before <label>" for the
// old one). Closed files are written under the save lock (Exclusive), so
// they cannot race with a save of a document opened in the meantime.
func (a *App) ApplyFileOps(ctx context.Context, label string, ops []FileOp) ([]EditStepResult, error) {
	out := make([]EditStepResult, 0, len(ops))
	for i, op := range ops {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var res EditStepResult
		var err error
		switch op.Kind {
		case "", FileOpEdit:
			res, err = a.applyEditOp(op, label)
		case FileOpCreate:
			res, err = a.applyCreateOp(op)
		case FileOpRename:
			res, err = a.applyRenameOp(ctx, op)
		case FileOpDelete:
			res, err = a.applyDeleteOp(ctx, op, label)
		default:
			err = Errorf(CodeInvalidParams, "unknown file operation %q", op.Kind)
		}
		if err != nil {
			var ae *Error
			if errors.As(err, &ae) {
				return out, Errorf(ae.Code, "step %d (%s %s): %s", i+1, kindOr(op.Kind), op.Path, ae.Message).WithData(ae.Data)
			}
			return out, Errorf(CodeFailed, "step %d (%s %s): %w", i+1, kindOr(op.Kind), op.Path, err)
		}
		out = append(out, res)
	}
	return out, nil
}

func kindOr(k string) string {
	if k == "" {
		return FileOpEdit
	}
	return k
}

// ApplyChanges applies non-overlapping changes to text and returns the
// result. Changes are sorted by start offset (stable, so inserts at the same
// offset keep their order).
func ApplyChanges(text []byte, changes []TextChange) ([]byte, error) {
	sorted, err := sortChanges(changes, len(text))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Grow(len(text))
	prev := 0
	for _, c := range sorted {
		b.Write(text[prev:c.Start])
		b.WriteString(c.Text)
		prev = c.End
	}
	b.Write(text[prev:])
	return b.Bytes(), nil
}

func sortChanges(changes []TextChange, n int) ([]TextChange, error) {
	sorted := append([]TextChange(nil), changes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	prevEnd := 0
	for _, c := range sorted {
		if c.Start < 0 || c.End < c.Start || c.End > n {
			return nil, Errorf(CodeInvalidParams, "change [%d,%d) out of bounds (text has %d bytes)", c.Start, c.End, n)
		}
		if c.Start < prevEnd {
			return nil, Errorf(CodeInvalidParams, "overlapping changes at offset %d", c.Start)
		}
		prevEnd = c.End
	}
	return sorted, nil
}

// applyBuffer applies changes to an open buffer as one undo step.
func applyBuffer(b *buffer.Buffer, changes []TextChange) (int, error) {
	sorted, err := sortChanges(changes, b.Len())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range sorted {
		if string(b.Slice(c.Start, c.End)) != c.Text {
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	b.BreakUndo()
	b.BeginGroup(buffer.GroupOther, nil)
	defer func() {
		b.EndGroup(nil)
		b.BreakUndo()
	}()
	for i := len(sorted) - 1; i >= 0; i-- {
		c := sorted[i]
		if string(b.Slice(c.Start, c.End)) == c.Text {
			continue
		}
		if _, err := b.Replace(c.Start, c.End-c.Start, []byte(c.Text)); err != nil {
			return 0, Errorf(CodeFailed, "edit: %w", err)
		}
	}
	return n, nil
}

func (a *App) openDoc(abs string) *Doc {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.docs[abs]
}

func (a *App) applyEditOp(op FileOp, label string) (EditStepResult, error) {
	abs, err := a.resolvePath(op.Path)
	if err != nil {
		return EditStepResult{}, err
	}
	if op.Changes == nil {
		return EditStepResult{}, Errorf(CodeInvalidParams, "no changes")
	}
	res := EditStepResult{Kind: FileOpEdit, Path: a.displayPath(abs), File: abs}
	size := 0
	err = a.saver.Exclusive(func() error {
		if d := a.openDoc(abs); d != nil {
			d.mu.Lock()
			defer d.mu.Unlock()
			if op.Version != nil && *op.Version != d.buf.Version() {
				return Errorf(CodeConflict, "version mismatch: %s is at %d, edit expects %d", res.Path, d.buf.Version(), *op.Version).
					WithData(map[string]uint64{"version": d.buf.Version()})
			}
			changes, err := op.Changes(d.buf.Bytes())
			if err != nil {
				return err
			}
			n, err := applyBuffer(d.buf, changes)
			if err != nil {
				return err
			}
			res.Open, res.Edits, res.Version, res.Skipped = true, n, d.buf.Version(), n == 0
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return notFoundOr(err, "read %s", res.Path)
		}
		changes, err := op.Changes(data)
		if err != nil {
			return err
		}
		updated, err := ApplyChanges(data, changes)
		if err != nil {
			return err
		}
		res.Edits = len(changes)
		if bytes.Equal(updated, data) {
			res.Skipped, res.Edits = true, 0
			return nil
		}
		rev, err := a.writeWithHistory(abs, data, updated, label)
		res.Revision, size = rev, len(updated)
		return err
	})
	if err == nil && !res.Open && !res.Skipped {
		a.bus.Publish(TopicFileSaved, FileSavedEvent{Path: res.Path, File: abs, Trigger: label, Size: size})
	}
	return res, err
}

// writeWithHistory records old (if any) and updated in Local History and
// writes updated atomically. Without a project nothing is recorded.
func (a *App) writeWithHistory(abs string, old, updated []byte, label string) (uint64, error) {
	if label == "" {
		label = "external change"
	}
	h := historySink{a}
	if old != nil {
		if _, _, err := h.Save(abs, old, "before "+label); err != nil {
			return 0, Errorf(CodeFailed, "local history: %w", err)
		}
	}
	if _, err := writeFileAtomic(abs, updated); err != nil {
		return 0, Errorf(CodeFailed, "%w", err)
	}
	rev, _, err := h.Save(abs, updated, label)
	if err != nil {
		return 0, Errorf(CodeFailed, "local history: %w", err)
	}
	return rev.ID, nil
}

func (a *App) applyCreateOp(op FileOp) (EditStepResult, error) {
	abs, err := a.resolvePath(op.Path)
	if err != nil {
		return EditStepResult{}, err
	}
	res := EditStepResult{Kind: FileOpCreate, Path: a.displayPath(abs), File: abs}
	var changes []FileChange
	err = a.saver.Exclusive(func() error {
		if _, err := os.Stat(abs); err == nil {
			switch {
			case op.Overwrite:
			case op.IgnoreIfExists:
				res.Skipped = true
				return nil
			default:
				return Errorf(CodeConflict, "%s already exists", res.Path)
			}
		}
		if d := a.openDoc(abs); d != nil {
			// An open document of a not yet existing file (doc.open
			// create:true) or an overwrite: empty the buffer.
			d.mu.Lock()
			_, err := applyBuffer(d.buf, []TextChange{{Start: 0, End: d.buf.Len()}})
			res.Open = true
			d.mu.Unlock()
			return err
		}
		parents := missingParents(filepath.Dir(abs))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Errorf(CodeFailed, "create %s: %w", res.Path, err)
		}
		var old []byte
		if data, err := os.ReadFile(abs); err == nil {
			old = data
		}
		if _, err := a.writeWithHistory(abs, old, nil, "create"); err != nil {
			return err
		}
		for _, d := range parents {
			changes = append(changes, a.created(d, true))
		}
		if old == nil {
			changes = append(changes, a.created(abs, false))
		}
		return nil
	})
	a.publishChanges(changes)
	return res, err
}

// applyRenameOp renames a file or directory with the move of the file
// operations API (files.rename): open documents, also modified ones, follow
// the rename (doc.renamed, their undo history stays), Local History moves
// along and files.changed is published.
func (a *App) applyRenameOp(_ context.Context, op FileOp) (EditStepResult, error) {
	from, err := a.resolvePath(op.Path)
	if err != nil {
		return EditStepResult{}, err
	}
	to, err := a.resolvePath(op.NewPath)
	if err != nil {
		return EditStepResult{}, err
	}
	res := EditStepResult{Kind: FileOpRename, Path: a.displayPath(from), File: from, NewPath: a.displayPath(to)}
	if _, err := os.Stat(to); err == nil && !op.Overwrite && !strings.EqualFold(from, to) {
		if op.IgnoreIfExists {
			res.Skipped = true
			return res, nil
		}
		return res, Errorf(CodeConflict, "%s already exists", res.NewPath)
	}
	if a.ProjectRoot() == "" {
		err = a.saver.Exclusive(func() error {
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return Errorf(CodeFailed, "rename: %w", err)
			}
			if err := os.Rename(from, to); err != nil {
				return notFoundOr(err, "rename %s", res.Path)
			}
			res.Open = len(a.rehome(from, to)) > 0
			return nil
		})
		return res, err
	}
	mr, changes, err := a.move(from, to, op.Overwrite)
	a.publishChanges(changes)
	if err != nil {
		return res, err
	}
	res.Open = len(mr.Docs) > 0
	return res, nil
}

// applyDeleteOp deletes a file or directory. The content of a file is kept
// in Local History; an open document is closed (its unsaved changes are
// kept in Local History too).
func (a *App) applyDeleteOp(ctx context.Context, op FileOp, label string) (EditStepResult, error) {
	abs, err := a.resolvePath(op.Path)
	if err != nil {
		return EditStepResult{}, err
	}
	res := EditStepResult{Kind: FileOpDelete, Path: a.displayPath(abs), File: abs}
	st, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) && op.IgnoreIfNotExists {
		res.Skipped = true
		return res, nil
	}
	if err != nil {
		return res, notFoundOr(err, "delete %s", res.Path)
	}
	if label == "" {
		label = "delete"
	}
	h := historySink{a}
	if d := a.openDoc(abs); d != nil {
		snap := d.Snapshot()
		if _, _, err := h.Save(abs, snap.Text, "before "+label); err != nil {
			return res, Errorf(CodeFailed, "local history: %w", err)
		}
		if _, err := a.docClose(ctx, DocCloseParams{Path: abs, Discard: true}); err != nil {
			return res, err
		}
		res.Open = true
	}
	err = a.saver.Exclusive(func() error {
		if st.IsDir() {
			if !op.Recursive {
				if err := os.Remove(abs); err != nil {
					return Errorf(CodeConflict, "delete %s: %v (directory not empty? use recursive)", res.Path, err)
				}
				return nil
			}
			return wrapFS(os.RemoveAll(abs), "delete "+res.Path)
		}
		if data, err := os.ReadFile(abs); err == nil {
			if _, _, err := h.Save(abs, data, "before "+label); err != nil {
				return Errorf(CodeFailed, "local history: %w", err)
			}
		}
		return wrapFS(os.Remove(abs), "delete "+res.Path)
	})
	if err == nil {
		a.publishChanges([]FileChange{{Kind: FileDeleted, Path: res.Path, File: abs, Dir: st.IsDir()}})
	}
	return res, err
}

func wrapFS(err error, what string) error {
	if err == nil {
		return nil
	}
	return Errorf(CodeFailed, "%s: %w", what, err)
}

// SaveAllDocuments saves every modified document through the autosave
// pipeline (file.beforeSave hooks, Local History, file.saved) with the given
// trigger, e.g. before a VCS update.
func (a *App) SaveAllDocuments(ctx context.Context, trigger string) error {
	if err := a.saver.SaveAll(ctx, autosave.Trigger(trigger)); err != nil {
		return fmt.Errorf("save documents: %w", err)
	}
	return nil
}
