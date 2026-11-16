package api

import (
	"context"
	"encoding/base64"
	"errors"

	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/fspath"
	"github.com/ape-dev-de/tiegel/internal/core/gitignore"
	"github.com/ape-dev-de/tiegel/internal/core/history"
	"github.com/ape-dev-de/tiegel/internal/core/langdetect"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/watch"
)

// project is the open project. Its history store is opened lazily so that
// read-only commands do not create directories.
type project struct {
	root     string
	settings *settings.Store
	hist     *history.Store
}

// OpenProject opens dir as the current project (closing a previous one) and
// returns its info. It is the Go form of "project.open".
func (a *App) OpenProject(ctx context.Context, dir string) (ProjectInfo, error) {
	return a.projectOpen(ctx, PathParams{Path: dir})
}

// PathParams is the params type of methods that take a single path. Relative
// paths are resolved against the project root (or the working directory if
// no project is open).
type PathParams struct {
	Path string `json:"path"`
}

// ProjectInfo is the result of "project.open" and "project.info".
type ProjectInfo struct {
	Open         bool     `json:"open"`
	Root         string   `json:"root,omitempty"`
	Name         string   `json:"name,omitempty"`
	SettingsDir  string   `json:"settingsDir,omitempty"`
	Git          bool     `json:"git"`
	Composer     bool     `json:"composer"`
	ComposeFiles []string `json:"composeFiles,omitempty"`
	Interpreter  string   `json:"interpreter,omitempty"`
	OpenDocs     int      `json:"openDocs"`
}

func (a *App) registerProject() {
	mustHandle(a, "project.open", "Open a project directory", a.projectOpen)
	mustHandle(a, "project.info", "Information about the open project", a.projectInfo)
	mustHandle(a, "project.close", "Close the project (open documents are discarded)", a.projectClose)
	mustHandle(a, "files.list", "Project file tree (.gitignore respected, vendor/node_modules marked)", a.filesList)
	mustHandle(a, "files.read", "Read a file from disk", a.filesRead)
	mustHandle(a, "files.detectLanguage", "Detect the language of a file", a.filesDetectLanguage)
}

func (a *App) projectOpen(ctx context.Context, p PathParams) (ProjectInfo, error) {
	if p.Path == "" {
		return ProjectInfo{}, Errorf(CodeInvalidParams, "path is required")
	}
	root, err := filepath.Abs(p.Path)
	if err != nil {
		return ProjectInfo{}, Errorf(CodeInvalidParams, "path: %v", err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	st, err := os.Stat(root)
	if err != nil {
		return ProjectInfo{}, Errorf(CodeNotFound, "project: %v", err)
	}
	if !st.IsDir() {
		return ProjectInfo{}, Errorf(CodeInvalidParams, "project: %s is not a directory", root)
	}
	// Switching to another project closes the previous one first, so its
	// documents do not leak into the new project (unsaved changes are
	// kept as recovery points like on project.close).
	if cur := a.ProjectRoot(); cur != "" && cur != root {
		if _, err := a.projectClose(ctx, struct{}{}); err != nil {
			return ProjectInfo{}, err
		}
	}
	ps, err := settings.OpenProject(a.opts.ConfigDir, root, a.bus)
	if err != nil {
		return ProjectInfo{}, Errorf(CodeFailed, "project settings: %w", err)
	}
	a.stopWatch()
	a.mu.Lock()
	a.project = &project{root: root, settings: ps}
	a.mu.Unlock()
	a.bus.Publish(TopicProjectOpened, ProjectEvent{Root: root, Name: filepath.Base(root)})
	if a.opts.Watch {
		be, _ := watch.ParseBackend(a.opts.WatchBackend) // validated in New
		if err := a.startWatch(root, be, nil); err != nil {
			a.log.Warn("watch", "root", root, "err", err)
		}
	}
	return a.projectInfo(ctx, struct{}{})
}

func (a *App) projectInfo(_ context.Context, _ struct{}) (ProjectInfo, error) {
	a.mu.Lock()
	pr := a.project
	docs := len(a.docs)
	a.mu.Unlock()
	if pr == nil {
		return ProjectInfo{OpenDocs: docs}, nil
	}
	info := ProjectInfo{
		Open:        true,
		Root:        pr.root,
		Name:        filepath.Base(pr.root),
		SettingsDir: pr.settings.Dir(),
		Git:         exists(filepath.Join(pr.root, ".git")),
		Composer:    exists(filepath.Join(pr.root, "composer.json")),
		Interpreter: settings.Get(pr.settings, SettingInterpreter, ""),
		OpenDocs:    docs,
	}
	if files, err := compose.FindFiles(pr.root, a.opts.ComposeEnv); err == nil {
		info.ComposeFiles = files.Paths
	}
	return info, nil
}

func (a *App) projectClose(_ context.Context, _ struct{}) (ProjectInfo, error) {
	a.stopWatch()
	a.preserveUnsaved("unsaved on project close", nil)
	a.mu.Lock()
	pr := a.project
	a.project = nil
	docs := a.docs
	a.docs = make(map[string]*Doc)
	a.mu.Unlock()
	for _, d := range docs {
		d.mu.Lock()
		a.bus.Publish(TopicDocClosed, DocCloseResult{Path: d.path, Discarded: d.buf.Modified()})
		d.mu.Unlock()
	}
	if pr != nil {
		a.bus.Publish(TopicProjectClosed, ProjectEvent{Root: pr.root, Name: filepath.Base(pr.root)})
	}
	return ProjectInfo{}, nil
}

// requireProject returns the open project or a CodeNoProject error.
func (a *App) requireProject() (*project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.project == nil {
		return nil, Errorf(CodeNoProject, "no project open (use project.open or --project)")
	}
	return a.project, nil
}

// projectHistory returns the Local History store of the open project,
// opening it on first use.
func (a *App) projectHistory() (*history.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.project == nil {
		return nil, Errorf(CodeNoProject, "no project open: Local History needs a project")
	}
	if a.project.hist == nil {
		h, err := history.Open(filepath.Join(a.project.settings.Dir(), "history"))
		if err != nil {
			return nil, Errorf(CodeFailed, "local history: %w", err)
		}
		a.project.hist = h
	}
	return a.project.hist, nil
}

// resolvePath makes p absolute: relative paths are taken relative to the
// project root, or to the working directory without a project.
func (a *App) resolvePath(p string) (string, error) {
	if p == "" {
		return "", Errorf(CodeInvalidParams, "path is required")
	}
	if fspath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	if root := a.ProjectRoot(); root != "" {
		return fspath.Resolve(root, p), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", Errorf(CodeInvalidParams, "path: %v", err)
	}
	return abs, nil
}

// displayPath returns abs relative to the project root (slash separated) if
// it lies inside, otherwise abs itself.
func (a *App) displayPath(abs string) string {
	root := a.ProjectRoot()
	if root == "" {
		return abs
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return filepath.ToSlash(rel)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func notFoundOr(err error, format string, args ...any) error {
	code := CodeFailed
	if errors.Is(err, fs.ErrNotExist) {
		code = CodeNotFound
	}
	return Errorf(code, format+": %w", append(args, err)...)
}

// FilesListParams are the params of "files.list".
type FilesListParams struct {
	// Path is a directory below the project root; empty means the root.
	Path string `json:"path,omitempty"`
	// Depth limits the recursion (1 = only direct children); 0 is unlimited.
	Depth int `json:"depth,omitempty"`
	// Ignored includes entries matched by .gitignore (marked "ignored").
	Ignored bool `json:"ignored,omitempty"`
	// Libraries descends into vendor/ and node_modules/ (always listed, but
	// by default not expanded).
	Libraries bool `json:"libraries,omitempty"`
	// Limit caps the number of entries (default 50000).
	Limit int `json:"limit,omitempty"`
}

// FileNode is one entry of the file tree.
type FileNode struct {
	Name string `json:"name"`
	// Path is relative to the project root, slash separated.
	Path    string `json:"path"`
	Dir     bool   `json:"dir,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Ignored bool   `json:"ignored,omitempty"`
	// Library marks vendor/ and node_modules/ directories (and their
	// content when expanded).
	Library bool `json:"library,omitempty"`
	// Collapsed marks a directory whose children were not listed (depth
	// limit or library).
	Collapsed bool        `json:"collapsed,omitempty"`
	Children  []*FileNode `json:"children,omitempty"`
}

// FilesListResult is the result of "files.list".
type FilesListResult struct {
	Root      string    `json:"root"`
	Tree      *FileNode `json:"tree"`
	Files     int       `json:"files"`
	Dirs      int       `json:"dirs"`
	Truncated bool      `json:"truncated,omitempty"`
}

// libraryDirs are directories that hold third-party code.
var libraryDirs = map[string]bool{"vendor": true, "node_modules": true}

// alwaysSkipped are never listed.
var alwaysSkipped = map[string]bool{".git": true, ".hg": true, ".svn": true}

func (a *App) filesList(ctx context.Context, p FilesListParams) (FilesListResult, error) {
	pr, err := a.requireProject()
	if err != nil {
		return FilesListResult{}, err
	}
	if p.Limit <= 0 {
		p.Limit = 50000
	}
	rel := filepath.ToSlash(filepath.Clean(p.Path))
	if rel == "." || rel == "/" {
		rel = ""
	}
	if strings.HasPrefix(rel, "../") || rel == ".." || fspath.IsAbs(p.Path) {
		return FilesListResult{}, Errorf(CodeInvalidParams, "path must be relative to the project root")
	}
	dir := filepath.Join(pr.root, filepath.FromSlash(rel))
	st, err := os.Stat(dir)
	if err != nil {
		return FilesListResult{}, notFoundOr(err, "files.list")
	}
	if !st.IsDir() {
		return FilesListResult{}, Errorf(CodeInvalidParams, "%s is not a directory", rel)
	}
	w := &walker{root: pr.root, p: p, ctx: ctx}
	// Load .gitignore files from the root down to the requested directory.
	w.ign.Load(pr.root, "")
	if rel != "" {
		parts := strings.Split(rel, "/")
		for i := range parts {
			w.ign.Load(pr.root, strings.Join(parts[:i+1], "/"))
		}
	}
	name := filepath.Base(dir)
	node := &FileNode{Name: name, Path: rel, Dir: true}
	if err := w.walk(node, 1, false); err != nil {
		return FilesListResult{}, err
	}
	return FilesListResult{Root: pr.root, Tree: node, Files: w.files, Dirs: w.dirs, Truncated: w.truncated}, nil
}

type walker struct {
	ctx       context.Context
	root      string
	p         FilesListParams
	ign       gitignore.Set
	files     int
	dirs      int
	truncated bool
}

func (w *walker) walk(node *FileNode, depth int, inLibrary bool) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	abs := filepath.Join(w.root, filepath.FromSlash(node.Path))
	if node.Path != "" {
		w.ign.Load(w.root, node.Path)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return notFoundOr(err, "files.list")
	}
	sort.SliceStable(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, e := range entries {
		if w.files+w.dirs >= w.p.Limit {
			w.truncated = true
			return nil
		}
		name := e.Name()
		if alwaysSkipped[name] {
			continue
		}
		rel := name
		if node.Path != "" {
			rel = node.Path + "/" + name
		}
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(abs, name)); err == nil {
				isDir = st.IsDir()
			}
		}
		ignored := node.Ignored || w.ign.Match(rel, isDir)
		if ignored && !w.p.Ignored {
			continue
		}
		child := &FileNode{Name: name, Path: rel, Dir: isDir, Ignored: ignored, Library: inLibrary || (isDir && libraryDirs[name])}
		node.Children = append(node.Children, child)
		if !isDir {
			w.files++
			if info, err := e.Info(); err == nil {
				child.Size = info.Size()
			}
			continue
		}
		w.dirs++
		switch {
		case child.Library && !inLibrary && !w.p.Libraries,
			w.p.Depth > 0 && depth >= w.p.Depth,
			e.Type()&fs.ModeSymlink != 0: // do not follow directory symlinks (cycles)
			child.Collapsed = true
			continue
		}
		if err := w.walk(child, depth+1, child.Library); err != nil {
			return err
		}
	}
	return nil
}

// FileContent is the result of "files.read".
type FileContent struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
	// Encoding is "utf-8" or, for binary content, "base64".
	Encoding  string `json:"encoding"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FilesReadParams are the params of "files.read".
type FilesReadParams struct {
	Path string `json:"path"`
	// MaxBytes limits the returned content (default 16 MiB).
	MaxBytes int64 `json:"maxBytes,omitempty"`
}

func (a *App) filesRead(_ context.Context, p FilesReadParams) (FileContent, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return FileContent{}, err
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 16 << 20
	}
	f, err := os.Open(abs)
	if err != nil {
		return FileContent{}, notFoundOr(err, "files.read")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return FileContent{}, Errorf(CodeFailed, "files.read: %w", err)
	}
	if st.IsDir() {
		return FileContent{}, Errorf(CodeInvalidParams, "files.read: %s is a directory", p.Path)
	}
	data, err := io.ReadAll(io.LimitReader(f, p.MaxBytes))
	if err != nil {
		return FileContent{}, Errorf(CodeFailed, "files.read: %w", err)
	}
	res := FileContent{
		Path:      a.displayPath(abs),
		Language:  string(langdetect.Detect(abs, head(data))),
		Size:      st.Size(),
		Encoding:  "utf-8",
		Content:   string(data),
		Truncated: int64(len(data)) < st.Size(),
	}
	if !utf8.Valid(data) {
		res.Encoding = "base64"
		res.Content = base64.StdEncoding.EncodeToString(data)
	}
	return res, nil
}

func head(b []byte) []byte {
	if len(b) > langdetect.HeadSize {
		return b[:langdetect.HeadSize]
	}
	return b
}

// DetectLanguageParams are the params of "files.detectLanguage".
type DetectLanguageParams struct {
	Path string `json:"path"`
	// Content, if set, is used instead of reading the file.
	Content *string `json:"content,omitempty"`
}

// LanguageResult is the result of "files.detectLanguage".
type LanguageResult struct {
	Path     string `json:"path"`
	Language string `json:"language"`
}

func (a *App) filesDetectLanguage(_ context.Context, p DetectLanguageParams) (LanguageResult, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return LanguageResult{}, err
	}
	var h []byte
	if p.Content != nil {
		h = head([]byte(*p.Content))
	} else if f, err := os.Open(abs); err == nil {
		buf := make([]byte, langdetect.HeadSize)
		n, _ := io.ReadFull(f, buf)
		_ = f.Close()
		h = buf[:n]
	}
	return LanguageResult{Path: a.displayPath(abs), Language: string(langdetect.Detect(abs, h))}, nil
}
