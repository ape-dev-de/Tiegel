package controller

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// File operations of the project tree. All paths are project relative
// (slash separated); the backend (files.*, D131) validates names and
// keeps open documents in step. Results arrive on the UI goroutine; the
// tree itself follows files.changed.

// PHPClassKinds are the kinds of files.createPhpClass (the file templates,
// api.FileTemplateKinds) in menu order.
var PHPClassKinds = []string{"class", "abstract", "interface", "trait", "enum", "test"}

// PHPClassKindLabel is the menu label of a kind.
func PHPClassKindLabel(kind string) string {
	switch kind {
	case "class":
		return "Class (final)"
	case "abstract":
		return "Abstract Class"
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// fileOpResult is the result of files.createFile/createDir/rename.
type fileOpResult struct {
	Path string   `json:"path"`
	File string   `json:"file"`
	Dir  bool     `json:"dir"`
	From string   `json:"from"`
	Docs []string `json:"docs"`
}

// joinRel joins a project-relative directory and a name.
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// DirOf returns the directory a "New …" action creates in: the entry
// itself if it is a directory, else its parent.
func (w *Workspace) DirOf(rel string) string {
	if t := w.c.Tree; t != nil {
		if n := t.Node(rel); n != nil && n.Dir {
			return rel
		}
	}
	return parentDir(rel)
}

// fileOp runs a files.* call and reports the outcome; done runs on the UI
// goroutine after success.
func (w *Workspace) fileOp(method string, params map[string]any, done func(res fileOpResult)) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var res fileOpResult
		err := c.call(ctx, method, params, &res)
		return func() {
			if err != nil {
				c.Status.Notice = describeFileError(err)
				return
			}
			if done != nil {
				done(res)
			}
		}
	})
}

// describeFileError makes the common file operation errors readable.
func describeFileError(err error) string {
	switch api.ErrorCode(err) {
	case api.CodeConflict:
		return "Already exists or has unsaved changes: " + err.Error()
	case api.CodeInvalidParams:
		return "Invalid name: " + err.Error()
	}
	return err.Error()
}

// CreateFile creates dir/name (an empty file) and opens it.
func (w *Workspace) CreateFile(dir, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	w.fileOp("files.createFile", map[string]any{"path": joinRel(dir, name)}, func(res fileOpResult) {
		w.c.OpenFile(res.File, nil)
	})
}

// CreateDir creates dir/name (parents as needed).
func (w *Workspace) CreateDir(dir, name string) {
	name = strings.TrimSpace(strings.Trim(name, "/"))
	if name == "" {
		return
	}
	w.fileOp("files.createDir", map[string]any{"path": joinRel(dir, name)}, func(res fileOpResult) {
		w.c.Status.Notice = "Created " + res.Path
	})
}

// PHPNamespace is the suggestion of files.phpNamespace for a directory.
type PHPNamespace struct {
	Dir       string `json:"dir"`
	Namespace string `json:"namespace"`
	Found     bool   `json:"found"`
}

// LoadNamespace asks for the PSR-4 namespace of dir (composer.json); done
// runs on the UI goroutine (with the zero value on errors).
func (w *Workspace) LoadNamespace(dir string, done func(PHPNamespace)) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var ns PHPNamespace
		err := c.call(ctx, "files.phpNamespace", map[string]any{"path": dir}, &ns)
		return func() {
			if err != nil {
				ns = PHPNamespace{Dir: dir}
			}
			done(ns)
		}
	})
}

// CreatePHPClass creates NAME.php in dir with the PSR-4 namespace (ns nil:
// the backend's suggestion) and opens it.
func (w *Workspace) CreatePHPClass(dir, name, kind string, ns *string) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".php")
	if name == "" {
		return
	}
	params := map[string]any{"dir": dir, "name": name, "kind": kind}
	if ns != nil {
		params["namespace"] = *ns
	}
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Path  string `json:"path"`
			File  string `json:"file"`
			Class string `json:"class"`
		}
		err := c.call(ctx, "files.createPhpClass", params, &res)
		return func() {
			if err != nil {
				c.Status.Notice = describeFileError(err)
				return
			}
			c.Status.Notice = "Created " + res.Class
			c.OpenFile(res.File, nil)
		}
	})
}

// Rename renames the entry rel to newName (same directory); a name with
// "/" moves it (project relative target).
func (w *Workspace) Rename(rel, newName string) {
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == path.Base(rel) {
		return
	}
	params := map[string]any{"path": rel}
	if strings.Contains(newName, "/") {
		params["to"] = newName
	} else {
		params["newName"] = newName
	}
	w.fileOp("files.rename", params, func(res fileOpResult) {
		w.c.Status.Notice = fmt.Sprintf("Renamed %s to %s", rel, res.Path)
	})
}

// Move moves entries into the directory dir.
func (w *Workspace) Move(paths []string, dir string) { w.moveCopy("files.move", paths, dir) }

// Copy copies entries into the directory dir.
func (w *Workspace) Copy(paths []string, dir string) { w.moveCopy("files.copy", paths, dir) }

func (w *Workspace) moveCopy(method string, paths []string, dir string) {
	if len(paths) == 0 {
		return
	}
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Results []fileOpResult `json:"results"`
		}
		err := c.call(ctx, method, map[string]any{"paths": paths, "to": dir}, &res)
		return func() {
			if err != nil {
				c.Status.Notice = describeFileError(err)
				return
			}
			verb := "Copied"
			if method == "files.move" {
				verb = "Moved"
			}
			c.Status.Notice = fmt.Sprintf("%s %d item(s) to %s", verb, len(res.Results), orRoot(dir))
		}
	})
}

func orRoot(dir string) string {
	if dir == "" {
		return "the project root"
	}
	return dir
}

// Duplicate copies rel next to itself as newName.
func (w *Workspace) Duplicate(rel, newName string) {
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == path.Base(rel) {
		return
	}
	w.fileOp("files.copy", map[string]any{"path": rel, "to": joinRel(parentDir(rel), newName)}, func(fileOpResult) {
		w.c.Status.Notice = "Copied " + rel + " to " + newName
	})
}

// CopyToClip and CutToClip fill the file clipboard; Paste copies or moves
// its entries into dir (a cut clipboard is emptied by the paste).
func (w *Workspace) CopyToClip(paths []string) {
	w.Clip = FileClip{Paths: append([]string(nil), paths...)}
}

// CutToClip marks entries to be moved by the next Paste.
func (w *Workspace) CutToClip(paths []string) {
	w.Clip = FileClip{Paths: append([]string(nil), paths...), Cut: true}
}

// Paste copies or moves the clipboard entries into dir.
func (w *Workspace) Paste(dir string) {
	clip := w.Clip
	if len(clip.Paths) == 0 {
		return
	}
	if clip.Cut {
		if underAny(dir, clip.Paths) {
			w.c.Status.Notice = "Cannot move a directory into itself"
			return
		}
		w.Clip = FileClip{}
		w.Move(clip.Paths, dir)
		return
	}
	w.Copy(clip.Paths, dir)
}

// DeleteResult is the outcome of Delete reported to the caller.
type DeleteResult struct {
	// NoTrash: the backend has no trash for these files (CodeUnavailable);
	// ask before deleting permanently.
	NoTrash bool
	Err     error
}

// Delete moves entries to the trash (permanent: deletes them). Modified
// documents below the entries are saved first, so their content ends up
// in the trash; open documents of deleted files are closed. done (may be
// nil) runs on the UI goroutine.
func (w *Workspace) Delete(paths []string, permanent bool, done func(DeleteResult)) {
	if len(paths) == 0 {
		return
	}
	c := w.c
	var save []string
	for _, t := range c.allTabs() {
		if t.Virtual == "" && t.Modified && underAny(c.Rel(t.File), paths) {
			save = append(save, t.File)
		}
	}
	c.Go(func(ctx context.Context) func() {
		for _, f := range save {
			_ = c.call(ctx, "doc.save", pathParams{Path: f}, nil)
		}
		var res struct {
			Deleted []string `json:"deleted"`
			Trashed bool     `json:"trashed"`
			Closed  []string `json:"closed"`
		}
		err := c.call(ctx, "files.delete", map[string]any{"paths": paths, "permanent": permanent}, &res)
		return func() {
			r := DeleteResult{Err: err}
			switch {
			case err != nil && api.ErrorCode(err) == api.CodeUnavailable && !permanent:
				r.NoTrash = true
			case err != nil:
				c.Status.Notice = describeFileError(err)
			default:
				w.closeTabsOf(func(f string) bool { return underAny(c.Rel(f), paths) })
				where := "to the trash"
				if !res.Trashed {
					where = "permanently"
				}
				c.Status.Notice = fmt.Sprintf("Deleted %d item(s) %s", max(len(res.Deleted), len(paths)), where)
			}
			if done != nil {
				done(r)
			}
		}
	})
}

// Reveal shows the entry in the file manager (files.reveal).
func (w *Workspace) Reveal(rel string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "files.reveal", map[string]any{"path": rel}, nil)
		return func() { c.notice(err) }
	})
}

// Path forms of CopyPath.
const (
	PathAbsolute = "absolute"
	PathRelative = "relative"
	PathName     = "name"
)

// PathText returns the clipboard text of entries (one per line) in the
// given form without a backend round trip.
func (w *Workspace) PathText(paths []string, form string) string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		switch form {
		case PathRelative:
			out = append(out, p)
		case PathName:
			if p == "" {
				out = append(out, w.c.Project.Name)
			} else {
				out = append(out, path.Base(p))
			}
		default:
			out = append(out, w.c.Abs(p))
		}
	}
	return strings.Join(out, "\n")
}

// Stage adds entries to the index (git.stage).
func (w *Workspace) Stage(paths []string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "git.stage", map[string]any{"paths": paths}, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = fmt.Sprintf("Added %d item(s) to Git", len(paths))
			c.refreshGit()
		}
	})
}

// Rollback discards the local changes of entries: open files through
// their documents (undoable, git.rollback per change), others with
// git.discard.
func (w *Workspace) Rollback(paths []string) {
	c := w.c
	var closed []string
	for _, p := range paths {
		abs := c.Abs(p)
		if c.sharedTab(abs, nil) != nil {
			c.Vcs.RollbackFile(abs)
			continue
		}
		closed = append(closed, p)
	}
	if len(closed) == 0 {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var err error
		for _, p := range closed {
			if e := c.call(ctx, "git.discard", map[string]any{"path": p, "all": true}, nil); e != nil && err == nil {
				err = e
			}
		}
		return func() {
			if err != nil {
				c.notice(err)
			} else {
				c.Status.Notice = fmt.Sprintf("Rolled back %d file(s)", len(closed))
			}
			c.refreshGit()
		}
	})
}
