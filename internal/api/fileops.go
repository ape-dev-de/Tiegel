package api

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/fspath"
	"github.com/ape-dev-de/tiegel/internal/core/langdetect"
	"github.com/ape-dev-de/tiegel/internal/core/trash"
)

// This file implements the file operations of the project tree (decision
// D131): files.createFile, createDir, rename, move, copy, delete, reveal,
// copyPath. All paths must lie inside the open project; open documents
// follow renames and moves, and every change is announced as files.changed.

func (a *App) registerFileOps() {
	mustHandle(a, "files.createFile", "Create a file (parents as needed; fails if it exists)", a.filesCreateFile)
	mustHandle(a, "files.createDir", "Create a directory (parents as needed; fails if it exists)", a.filesCreateDir)
	mustHandle(a, "files.rename", "Rename a file or directory (newName in place, or to a path); open documents follow", a.filesRename)
	mustHandle(a, "files.move", "Move files or directories into a directory (or one to a new path); open documents follow", a.filesMove)
	mustHandle(a, "files.copy", "Copy files or directories into a directory (or one to a new path)", a.filesCopy)
	mustHandle(a, "files.delete", "Move files or directories to the trash (permanent:true deletes); unsaved open documents block", a.filesDelete)
	mustHandle(a, "files.reveal", "Show a file in the system file manager (Finder, Explorer, xdg-open)", a.filesReveal)
	mustHandle(a, "files.copyPath", "Absolute, project-relative and file:// forms of paths (the client copies them)", a.filesCopyPath)
	mustHandle(a, "files.createPhpClass", "Create a PHP class/interface/trait/enum file with the PSR-4 namespace from composer.json", a.filesCreatePHPClass)
	mustHandle(a, "files.phpNamespace", "PSR-4 namespace of a directory according to composer.json", a.filesPHPNamespace)
}

// DocRenamedEvent is the payload of doc.renamed: an open document now
// belongs to another file (rename or move). It is followed by doc.closed
// for the old path (with RenamedTo set) and doc.opened for the new one, so
// consumers that only track open/close (language server, syntax) follow
// without knowing about renames.
type DocRenamedEvent struct {
	From       string `json:"from"`
	To         string `json:"to"`
	FromFile   string `json:"fromFile"`
	File       string `json:"file"`
	Generation uint64 `json:"generation"`
	Language   string `json:"language"`
}

// FileOpResult describes one created, moved or copied entry.
type FileOpResult struct {
	Path string `json:"path"`
	File string `json:"file"`
	Dir  bool   `json:"dir,omitempty"`
	// From is the source (rename, move, copy).
	From string `json:"from,omitempty"`
	// Docs lists open documents that now have a new path.
	Docs []string `json:"docs,omitempty"`
	// Doc is the opened document (open:true).
	Doc *DocState `json:"doc,omitempty"`
}

// FilesOpResult is the result of files.move and files.copy.
type FilesOpResult struct {
	Results []FileOpResult `json:"results"`
}

// projectPath resolves p (project relative or absolute) and requires it to
// lie inside the open project, also after resolving symlinks of its
// existing ancestors. The root itself is only accepted with allowRoot.
func (a *App) projectPath(p string, allowRoot bool) (string, error) {
	pr, err := a.requireProject()
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", Errorf(CodeInvalidParams, "path is required")
	}
	if strings.ContainsRune(p, 0) {
		return "", Errorf(CodeInvalidParams, "invalid path %q", p)
	}
	abs := fspath.Resolve(pr.root, p)
	if !fspath.Within(pr.root, abs) {
		return "", Errorf(CodeInvalidParams, "%s is outside the project", p)
	}
	if abs == pr.root && !allowRoot {
		return "", Errorf(CodeInvalidParams, "not allowed on the project root")
	}
	if abs == pr.root {
		return abs, nil
	}
	// A symlinked directory on the way must not lead out of the project.
	dir := filepath.Dir(abs)
	for dir != pr.root && fspath.Within(pr.root, dir) {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && !fspath.Within(pr.root, real) {
		return "", Errorf(CodeInvalidParams, "%s leads outside the project through a symlink", p)
	}
	return abs, nil
}

// validNewPath checks every component of abs below the project root with
// the naming rules of the host OS (fspath.ValidName).
func (a *App) validNewPath(abs string) error {
	rel, err := filepath.Rel(a.ProjectRoot(), abs)
	if err != nil {
		return Errorf(CodeInvalidParams, "path: %v", err)
	}
	for _, c := range strings.Split(rel, string(filepath.Separator)) {
		if err := fspath.ValidName(c, runtime.GOOS); err != nil {
			return Errorf(CodeInvalidParams, "%v", err)
		}
	}
	return nil
}

func (a *App) publishChanges(changes []FileChange) {
	for i := range changes {
		changes[i].Source = FileSourceTiegel
	}
	if len(changes) > 0 {
		a.bus.Publish(TopicFilesChanged, FilesChangedEvent{Changes: changes})
	}
}

// missingParents returns the directories that MkdirAll(dir) would create,
// outermost first.
func missingParents(dir string) []string {
	var out []string
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		out = append([]string{dir}, out...)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

func (a *App) created(abs string, dir bool) FileChange {
	return FileChange{Kind: FileCreated, Path: a.displayPath(abs), File: abs, Dir: dir}
}

// CreateFileParams are the params of "files.createFile".
type CreateFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	// Open opens the new file as document.
	Open bool `json:"open,omitempty"`
}

func (a *App) filesCreateFile(ctx context.Context, p CreateFileParams) (FileOpResult, error) {
	abs, err := a.createFile(p.Path, []byte(p.Content))
	if err != nil {
		return FileOpResult{}, err
	}
	return a.openResult(ctx, abs, p.Open)
}

// createFile creates abs with data, reporting the new parents and the file.
func (a *App) createFile(path string, data []byte) (string, error) {
	abs, err := a.projectPath(path, false)
	if err != nil {
		return "", err
	}
	if err := a.validNewPath(abs); err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", Errorf(CodeConflict, "%s already exists", a.displayPath(abs))
	}
	parents := missingParents(filepath.Dir(abs))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", Errorf(CodeFailed, "files.createFile: %w", err)
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", Errorf(CodeConflict, "%s already exists", a.displayPath(abs))
		}
		return "", Errorf(CodeFailed, "files.createFile: %w", err)
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(abs)
		return "", Errorf(CodeFailed, "files.createFile: %w", werr)
	}
	var changes []FileChange
	for _, d := range parents {
		changes = append(changes, a.created(d, true))
	}
	a.publishChanges(append(changes, a.created(abs, false)))
	return abs, nil
}

func (a *App) openResult(ctx context.Context, abs string, open bool) (FileOpResult, error) {
	res := FileOpResult{Path: a.displayPath(abs), File: abs}
	if open {
		d, err := a.docOpen(ctx, DocOpenParams{Path: abs})
		if err != nil {
			return res, err
		}
		res.Doc = &d.DocState
	}
	return res, nil
}

func (a *App) filesCreateDir(_ context.Context, p PathParams) (FileOpResult, error) {
	abs, err := a.projectPath(p.Path, false)
	if err != nil {
		return FileOpResult{}, err
	}
	if err := a.validNewPath(abs); err != nil {
		return FileOpResult{}, err
	}
	if _, err := os.Lstat(abs); err == nil {
		return FileOpResult{}, Errorf(CodeConflict, "%s already exists", a.displayPath(abs))
	}
	parents := missingParents(abs)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return FileOpResult{}, Errorf(CodeFailed, "files.createDir: %w", err)
	}
	var changes []FileChange
	for _, d := range parents {
		changes = append(changes, a.created(d, true))
	}
	a.publishChanges(changes)
	return FileOpResult{Path: a.displayPath(abs), File: abs, Dir: true}, nil
}

// RenameParams are the params of "files.rename". Give either NewName (a
// plain name, the entry stays in its directory) or To (a path).
type RenameParams struct {
	Path    string `json:"path"`
	NewName string `json:"newName,omitempty"`
	To      string `json:"to,omitempty"`
	// Overwrite replaces an existing file at the target (never a
	// directory, never a document with unsaved changes).
	Overwrite bool `json:"overwrite,omitempty"`
}

func (a *App) filesRename(_ context.Context, p RenameParams) (FileOpResult, error) {
	src, err := a.projectPath(p.Path, false)
	if err != nil {
		return FileOpResult{}, err
	}
	var dst string
	switch {
	case (p.NewName == "") == (p.To == ""):
		return FileOpResult{}, Errorf(CodeInvalidParams, "give either newName or to")
	case p.NewName != "":
		if err := fspath.ValidName(p.NewName, runtime.GOOS); err != nil || strings.ContainsRune(p.NewName, filepath.Separator) {
			return FileOpResult{}, Errorf(CodeInvalidParams, "invalid name %q", p.NewName)
		}
		if dst, err = a.projectPath(filepath.Join(filepath.Dir(src), p.NewName), false); err != nil {
			return FileOpResult{}, err
		}
	default:
		if dst, err = a.projectPath(p.To, false); err != nil {
			return FileOpResult{}, err
		}
	}
	res, changes, err := a.move(src, dst, p.Overwrite)
	a.publishChanges(changes)
	return res, err
}

// TransferParams are the params of "files.move" and "files.copy": Paths
// (or Path) go into the directory To; a single path whose To does not
// exist as a directory is moved/copied to exactly that path.
type TransferParams struct {
	Path      string   `json:"path,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	To        string   `json:"to"`
	Overwrite bool     `json:"overwrite,omitempty"`
}

// targets resolves the sources and destinations of a transfer.
func (a *App) targets(p TransferParams) (src, dst []string, err error) {
	paths := p.Paths
	if p.Path != "" {
		paths = append([]string{p.Path}, paths...)
	}
	if len(paths) == 0 {
		return nil, nil, Errorf(CodeInvalidParams, "path or paths is required")
	}
	to, err := a.projectPath(p.To, true)
	if err != nil {
		return nil, nil, err
	}
	st, statErr := os.Stat(to)
	intoDir := statErr == nil && st.IsDir()
	if len(paths) > 1 && !intoDir {
		return nil, nil, Errorf(CodeInvalidParams, "%s is not a directory", p.To)
	}
	for _, s := range paths {
		abs, err := a.projectPath(s, false)
		if err != nil {
			return nil, nil, err
		}
		d := to
		if intoDir {
			d = filepath.Join(to, filepath.Base(abs))
		}
		src, dst = append(src, abs), append(dst, d)
	}
	return src, dst, nil
}

func (a *App) filesMove(_ context.Context, p TransferParams) (FilesOpResult, error) {
	src, dst, err := a.targets(p)
	if err != nil {
		return FilesOpResult{}, err
	}
	res := FilesOpResult{Results: []FileOpResult{}}
	var changes []FileChange
	defer func() { a.publishChanges(changes) }()
	for i := range src {
		r, ch, err := a.move(src[i], dst[i], p.Overwrite)
		changes = append(changes, ch...)
		if err != nil {
			return res, err
		}
		res.Results = append(res.Results, r)
	}
	return res, nil
}

// openDocsUnder returns the open documents at path or below it.
func (a *App) openDocsUnder(path string) []*Doc {
	prefix := path + string(filepath.Separator)
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*Doc
	for p, d := range a.docs {
		if p == path || strings.HasPrefix(p, prefix) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// modifiedDocs returns the display paths of modified documents at or below
// each of paths.
func (a *App) modifiedDocs(paths ...string) []string {
	var out []string
	for _, p := range paths {
		for _, d := range a.openDocsUnder(p) {
			d.mu.Lock()
			if d.buf.Modified() {
				out = append(out, a.displayPath(d.path))
			}
			d.mu.Unlock()
		}
	}
	return out
}

// move renames src to dst (also across directories inside the project),
// re-homes open documents and moves the Local History along.
func (a *App) move(src, dst string, overwrite bool) (FileOpResult, []FileChange, error) {
	sst, err := os.Lstat(src)
	if err != nil {
		return FileOpResult{}, nil, notFoundOr(err, "move %s", a.displayPath(src))
	}
	if src == dst {
		return FileOpResult{}, nil, Errorf(CodeInvalidParams, "%s: source and target are the same", a.displayPath(src))
	}
	// filepath.Rel compares case-insensitively on Windows, so a case-only
	// rename (user.php → User.php) looks like "into itself" there.
	if fspath.Within(src, dst) && !strings.EqualFold(src, dst) {
		return FileOpResult{}, nil, Errorf(CodeInvalidParams, "cannot move %s into itself", a.displayPath(src))
	}
	if err := a.validNewPath(dst); err != nil {
		return FileOpResult{}, nil, err
	}
	// A case-only rename on a case-insensitive file system finds the
	// source at the target.
	dstSt, dstErr := os.Lstat(dst)
	caseOnly := dstErr == nil && strings.EqualFold(src, dst) && os.SameFile(sst, dstSt)
	var closeDst []*Doc
	if dstErr == nil && !caseOnly {
		switch {
		case !overwrite:
			return FileOpResult{}, nil, Errorf(CodeConflict, "%s already exists", a.displayPath(dst))
		case dstSt.IsDir() || sst.IsDir():
			return FileOpResult{}, nil, Errorf(CodeConflict, "%s already exists (directories are never overwritten)", a.displayPath(dst))
		}
		if m := a.modifiedDocs(dst); len(m) > 0 {
			return FileOpResult{}, nil, Errorf(CodeConflict, "%s has unsaved changes", m[0]).WithData(m)
		}
		closeDst = a.openDocsUnder(dst)
	}
	res := FileOpResult{Path: a.displayPath(dst), File: dst, Dir: sst.IsDir(), From: a.displayPath(src)}
	parents := missingParents(filepath.Dir(dst))
	err = a.saver.Exclusive(func() error {
		if dstErr == nil && !caseOnly {
			// Checked again under the save lock: edited in between?
			if m := a.modifiedDocs(dst); len(m) > 0 {
				return Errorf(CodeConflict, "%s has unsaved changes", m[0]).WithData(m)
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Errorf(CodeFailed, "move: %w", err)
		}
		if err := os.Rename(src, dst); err != nil {
			return Errorf(CodeFailed, "move %s: %w", a.displayPath(src), err)
		}
		for _, d := range closeDst {
			a.dropDoc(d)
		}
		res.Docs = a.rehome(src, dst)
		return nil
	})
	var changes []FileChange
	for _, d := range parents {
		changes = append(changes, a.created(d, true))
	}
	if err != nil {
		return FileOpResult{}, changes, err
	}
	if h, herr := a.projectHistory(); herr == nil {
		if _, herr := h.Move(src, dst); herr != nil {
			a.log.Warn("local history not moved", "from", src, "to", dst, "err", herr)
		}
	}
	changes = append(changes, FileChange{Kind: FileRenamed, Path: res.Path, File: dst, OldPath: res.From, OldFile: src, Dir: res.Dir})
	return res, changes, nil
}

// rehome moves the open documents at or below from to the corresponding
// path below to (the files were renamed already) and announces it with
// doc.renamed, doc.closed (old path) and doc.opened (new path).
func (a *App) rehome(from, to string) []string {
	var out []string
	for _, d := range a.openDocsUnder(from) {
		d.mu.Lock()
		old := d.path
		np := to + old[len(from):]
		a.mu.Lock()
		if a.docs[old] == d {
			delete(a.docs, old)
			a.docs[np] = d
		}
		d.setName(np, langdetect.Detect(np, d.buf.Slice(0, min(d.buf.Len(), langdetect.HeadSize))))
		a.mu.Unlock()
		oldDisplay, newDisplay := a.displayPath(old), a.displayPath(np)
		a.bus.Publish(TopicDocRenamed, DocRenamedEvent{From: oldDisplay, To: newDisplay, FromFile: old, File: np, Generation: d.gen, Language: string(d.lang)})
		a.bus.Publish(TopicDocClosed, DocCloseResult{Path: oldDisplay, Generation: d.gen, RenamedTo: newDisplay})
		a.bus.Publish(TopicDocOpened, a.stateLocked(d))
		d.mu.Unlock()
		out = append(out, newDisplay)
	}
	return out
}

// dropDoc closes a document whose file is gone or replaced. A document
// that has unsaved changes (e.g. edited after the check) stays open: its
// changes are never discarded, saving recreates the file.
func (a *App) dropDoc(d *Doc) (closed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf.Modified() {
		return false
	}
	a.mu.Lock()
	current := a.docs[d.path] == d
	if current {
		delete(a.docs, d.path)
	}
	a.mu.Unlock()
	if current {
		a.bus.Publish(TopicDocClosed, DocCloseResult{Path: a.displayPath(d.path), Generation: d.gen})
	}
	return current
}

func (a *App) filesCopy(_ context.Context, p TransferParams) (FilesOpResult, error) {
	src, dst, err := a.targets(p)
	if err != nil {
		return FilesOpResult{}, err
	}
	res := FilesOpResult{Results: []FileOpResult{}}
	var changes []FileChange
	defer func() { a.publishChanges(changes) }()
	for i := range src {
		s, d := src[i], dst[i]
		sst, err := os.Lstat(s)
		if err != nil {
			return res, notFoundOr(err, "copy %s", a.displayPath(s))
		}
		if s == d || fspath.Within(s, d) {
			return res, Errorf(CodeInvalidParams, "cannot copy %s into itself", a.displayPath(s))
		}
		if err := a.validNewPath(d); err != nil {
			return res, err
		}
		if dstSt, err := os.Lstat(d); err == nil {
			switch {
			case os.SameFile(sst, dstSt):
				// A case variant on a case-insensitive file system
				// (macOS, Windows): copying would truncate the source.
				return res, Errorf(CodeInvalidParams, "%s: source and target are the same file", a.displayPath(s))
			case !p.Overwrite:
				return res, Errorf(CodeConflict, "%s already exists", a.displayPath(d))
			case dstSt.IsDir() || sst.IsDir():
				return res, Errorf(CodeConflict, "%s already exists (directories are never overwritten)", a.displayPath(d))
			}
			if m := a.modifiedDocs(d); len(m) > 0 {
				return res, Errorf(CodeConflict, "%s has unsaved changes", m[0]).WithData(m)
			}
		}
		for _, dir := range missingParents(filepath.Dir(d)) {
			changes = append(changes, a.created(dir, true))
		}
		if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
			return res, Errorf(CodeFailed, "copy: %w", err)
		}
		if err := copyTree(s, d, p.Overwrite); err != nil {
			return res, Errorf(CodeFailed, "copy %s: %w", a.displayPath(s), err)
		}
		// An open (unmodified) document of an overwritten file shows the
		// new content.
		// reload records the new disk state, so the watcher does not
		// report the copy as an external change.
		a.reload(d)
		changes = append(changes, a.created(d, sst.IsDir()))
		res.Results = append(res.Results, FileOpResult{Path: a.displayPath(d), File: d, Dir: sst.IsDir(), From: a.displayPath(s)})
	}
	return res, nil
}

// copyTree copies a file, symlink or directory tree. Files keep their
// permission bits; symlinks are copied as links.
func copyTree(src, dst string, overwrite bool) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if overwrite {
			_ = os.Remove(dst)
		}
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.Mkdir(dst, st.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), false); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	out, err := os.OpenFile(dst, flags, st.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// DeleteParams are the params of "files.delete".
type DeleteParams struct {
	Path  string   `json:"path,omitempty"`
	Paths []string `json:"paths,omitempty"`
	// Permanent deletes instead of moving to the trash.
	Permanent bool `json:"permanent,omitempty"`
}

// DeleteResult is the result of "files.delete".
type DeleteResult struct {
	Deleted []string `json:"deleted"`
	// Trashed is true if the entries went to the trash.
	Trashed bool `json:"trashed"`
	// Closed lists open documents that were closed.
	Closed []string `json:"closed,omitempty"`
}

func (a *App) filesDelete(_ context.Context, p DeleteParams) (DeleteResult, error) {
	paths := p.Paths
	if p.Path != "" {
		paths = append([]string{p.Path}, paths...)
	}
	if len(paths) == 0 {
		return DeleteResult{}, Errorf(CodeInvalidParams, "path or paths is required")
	}
	var abs []string
	for _, s := range paths {
		a1, err := a.projectPath(s, false)
		if err != nil {
			return DeleteResult{}, err
		}
		if _, err := os.Lstat(a1); err != nil {
			return DeleteResult{}, notFoundOr(err, "delete %s", s)
		}
		abs = append(abs, a1)
	}
	res := DeleteResult{Deleted: []string{}, Trashed: !p.Permanent}
	var changes []FileChange
	defer func() { a.publishChanges(changes) }()
	err := a.saver.Exclusive(func() error {
		// Unsaved changes are never thrown away: check everything first.
		if m := a.modifiedDocs(abs...); len(m) > 0 {
			return Errorf(CodeConflict, "%s has unsaved changes: save or close it first", m[0]).WithData(m)
		}
		for _, f := range abs {
			st, err := os.Lstat(f)
			if err != nil {
				return notFoundOr(err, "delete %s", a.displayPath(f))
			}
			if p.Permanent {
				err = os.RemoveAll(f)
			} else {
				err = a.trash(f)
			}
			if errors.Is(err, trash.ErrUnavailable) {
				return Errorf(CodeUnavailable, "%v; delete permanently with permanent:true", err)
			}
			if err != nil {
				return Errorf(CodeFailed, "delete %s: %w", a.displayPath(f), err)
			}
			for _, d := range a.openDocsUnder(f) {
				if a.dropDoc(d) {
					res.Closed = append(res.Closed, a.displayPath(d.Path()))
				}
			}
			res.Deleted = append(res.Deleted, a.displayPath(f))
			changes = append(changes, FileChange{Kind: FileDeleted, Path: a.displayPath(f), File: f, Dir: st.IsDir(), Trashed: !p.Permanent})
		}
		return nil
	})
	return res, err
}

// RevealResult is the result of "files.reveal".
type RevealResult struct {
	Path string `json:"path"`
	// Command is the started program with its arguments.
	Command []string `json:"command"`
}

// revealCommand returns the file manager command that shows abs on goos.
func revealCommand(goos, abs string, dir bool) []string {
	switch goos {
	case "darwin":
		return []string{"open", "-R", abs}
	case "windows":
		return []string{"explorer", "/select," + abs}
	}
	if !dir {
		abs = filepath.Dir(abs)
	}
	return []string{"xdg-open", abs}
}

// startDetached starts a program without waiting for it.
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (a *App) filesReveal(_ context.Context, p PathParams) (RevealResult, error) {
	abs, err := a.projectPath(p.Path, true)
	if err != nil {
		return RevealResult{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return RevealResult{}, notFoundOr(err, "reveal %s", p.Path)
	}
	cmd := revealCommand(a.opts.GOOS, abs, st.IsDir())
	if err := a.start(cmd[0], cmd[1:]...); err != nil {
		code := CodeFailed
		if errors.Is(err, exec.ErrNotFound) {
			code = CodeUnavailable
		}
		return RevealResult{}, Errorf(code, "reveal: %w", err)
	}
	return RevealResult{Path: a.displayPath(abs), Command: cmd}, nil
}

// CopyPathParams are the params of "files.copyPath".
type CopyPathParams struct {
	Path  string   `json:"path,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// PathForms are the copyable forms of one path.
type PathForms struct {
	Absolute string `json:"absolute"`
	// Relative is project relative with slashes ("" outside the project).
	Relative string `json:"relative,omitempty"`
	Name     string `json:"name"`
	URI      string `json:"uri"`
}

// CopyPathResult is the result of "files.copyPath". Text joins the
// absolute paths with newlines, RelativeText the relative ones.
type CopyPathResult struct {
	Paths        []PathForms `json:"paths"`
	Text         string      `json:"text"`
	RelativeText string      `json:"relativeText"`
}

func (a *App) filesCopyPath(_ context.Context, p CopyPathParams) (CopyPathResult, error) {
	paths := p.Paths
	if p.Path != "" {
		paths = append([]string{p.Path}, paths...)
	}
	if len(paths) == 0 {
		return CopyPathResult{}, Errorf(CodeInvalidParams, "path or paths is required")
	}
	root := a.ProjectRoot()
	res := CopyPathResult{Paths: []PathForms{}}
	var abs, rel []string
	for _, s := range paths {
		f, err := a.resolvePath(s)
		if err != nil {
			return CopyPathResult{}, err
		}
		pf := PathForms{Absolute: f, Name: filepath.Base(f), URI: fileURI(f)}
		if root != "" && fspath.Within(root, f) {
			r, _ := filepath.Rel(root, f)
			pf.Relative = filepath.ToSlash(r)
		}
		res.Paths = append(res.Paths, pf)
		abs, rel = append(abs, pf.Absolute), append(rel, cmpOr(pf.Relative, pf.Absolute))
	}
	res.Text, res.RelativeText = strings.Join(abs, "\n"), strings.Join(rel, "\n")
	return res, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// fileURI returns the file:// URI of an absolute path.
func fileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive letter
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
