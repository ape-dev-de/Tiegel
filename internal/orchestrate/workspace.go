package orchestrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// ErrNoBase means the workspace cannot provide the base code (in-place
// execution): the red check is skipped.
var ErrNoBase = errors.New("no base tree (in-place execution)")

// FileChange is a file changed by the execution.
type FileChange struct {
	// Path is relative to the workspace directory (= project relative).
	Path    string
	Kind    string
	Test    bool
	Old     []byte
	New     []byte
	Changes []gitcli.Change
}

// AddedLines returns the 1-based numbers of added lines (new side).
func (f FileChange) AddedLines() []int {
	var out []int
	for _, c := range f.Changes {
		for l := c.NewStart; l < c.NewEnd; l++ {
			out = append(out, l+1)
		}
	}
	return out
}

// Unified is the unified diff of the file.
func (f FileChange) Unified() string {
	return gitcli.UnifiedDiff(f.Path, f.Old, f.New, f.Kind != api.ChangeCreated, f.Kind != api.ChangeDeleted)
}

// Stat counts added and removed lines.
func (f FileChange) Stat() (added, removed int) {
	for _, c := range f.Changes {
		added += c.NewEnd - c.NewStart
		removed += c.OldEnd - c.OldStart
	}
	return
}

// Info returns the API form.
func (f FileChange) Info() api.PlanChange {
	a, r := f.Stat()
	return api.PlanChange{Path: f.Path, Kind: f.Kind, Added: a, Removed: r, Test: f.Test}
}

// IsTestPath tells whether a project relative path is a test file.
func IsTestPath(p string) bool {
	p = filepath.ToSlash(p)
	base := filepath.Base(p)
	return strings.HasSuffix(base, "Test.php") || strings.HasPrefix(p, "tests/") || strings.Contains(p, "/tests/") ||
		strings.HasPrefix(p, "test/") || strings.Contains(p, "/test/")
}

func newChange(path string, old, cur []byte, existed, exists bool) FileChange {
	f := FileChange{Path: filepath.ToSlash(path), Old: old, New: cur, Test: IsTestPath(path)}
	switch {
	case !existed:
		f.Kind = api.ChangeCreated
	case !exists:
		f.Kind = api.ChangeDeleted
	default:
		f.Kind = api.ChangeModified
	}
	f.Changes = gitcli.DiffLines(gitcli.SplitLines(old), gitcli.SplitLines(cur))
	return f
}

// Workspace is where the execution changes files.
type Workspace interface {
	// Dir is the project directory the agents work in.
	Dir() string
	Info() api.PlanWorkspace
	// Changes lists the changed files compared with the base.
	Changes(ctx context.Context) ([]FileChange, error)
	// Checkpoint records the current state (a commit in the plan branch).
	Checkpoint(ctx context.Context, msg string) error
	// BaseDir returns a directory with the base code (ErrNoBase when not
	// available); ResetBase undoes what was copied into it.
	BaseDir(ctx context.Context) (string, error)
	ResetBase(ctx context.Context) error
	// Revert restores the base version of one file.
	Revert(ctx context.Context, path string) error
	// BeforeEdit is called before an agent changes abs.
	BeforeEdit(abs string)
}

// ---- in place -------------------------------------------------------------------

// InPlace executes directly in the project and keeps the content of every
// file before its first change (plans/<id>/orig.json), so that changes
// can be listed, diffed and reverted. It has no base tree (no red check).
type InPlace struct {
	Root string
	// File persists the originals ("" = memory only).
	File string
	Note string

	mu   sync.Mutex
	orig map[string]origFile
}

type origFile struct {
	Data    []byte `json:"data,omitempty"`
	Existed bool   `json:"existed"`
}

// NewInPlace returns an in-place workspace, loading persisted originals.
func NewInPlace(root, file, note string) *InPlace {
	w := &InPlace{Root: root, File: file, Note: note, orig: map[string]origFile{}}
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			_ = json.Unmarshal(b, &w.orig)
		}
	}
	return w
}

// Dir implements Workspace.
func (w *InPlace) Dir() string { return w.Root }

// Info implements Workspace.
func (w *InPlace) Info() api.PlanWorkspace {
	return api.PlanWorkspace{Mode: "in-place", Dir: w.Root, Note: w.Note}
}

// BeforeEdit implements Workspace: the first content of a file is kept.
func (w *InPlace) BeforeEdit(abs string) {
	rel, err := filepath.Rel(w.Root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	rel = filepath.ToSlash(rel)
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.orig[rel]; ok {
		return
	}
	data, err := os.ReadFile(abs)
	w.orig[rel] = origFile{Data: data, Existed: err == nil}
	w.saveLocked()
}

func (w *InPlace) saveLocked() {
	if w.File == "" {
		return
	}
	if b, err := json.Marshal(w.orig); err == nil {
		_ = writeAtomic(w.File, b)
	}
}

// Changes implements Workspace.
func (w *InPlace) Changes(context.Context) ([]FileChange, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []FileChange
	for rel, o := range w.orig {
		cur, err := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(rel)))
		exists := err == nil
		if exists == o.Existed && string(cur) == string(o.Data) {
			continue
		}
		out = append(out, newChange(rel, o.Data, cur, o.Existed, exists))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Checkpoint implements Workspace (nothing to do in place).
func (w *InPlace) Checkpoint(context.Context, string) error { return nil }

// BaseDir implements Workspace.
func (w *InPlace) BaseDir(context.Context) (string, error) { return "", ErrNoBase }

// ResetBase implements Workspace.
func (w *InPlace) ResetBase(context.Context) error { return nil }

// Revert implements Workspace.
func (w *InPlace) Revert(_ context.Context, path string) error {
	rel := filepath.ToSlash(filepath.Clean(path))
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.orig[rel]
	if !ok {
		return fmt.Errorf("orchestrate: %s was not changed", path)
	}
	abs := filepath.Join(w.Root, filepath.FromSlash(rel))
	if !o.Existed {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("orchestrate: revert %s: %w", path, err)
		}
	} else if err := os.WriteFile(abs, o.Data, 0o644); err != nil {
		return fmt.Errorf("orchestrate: revert %s: %w", path, err)
	}
	delete(w.orig, rel)
	w.saveLocked()
	return nil
}

// ---- git worktree ---------------------------------------------------------------

// WorktreeDirName is the directory inside the project that holds the
// plan worktrees (inside, so that a container that mounts the project
// sees them too).
const WorktreeDirName = ".tiegel/worktrees"

// Worktree executes in a linked git worktree on the branch
// tiegel/plan-<id> inside the project (<project>/.tiegel/worktrees/<id>).
// Its base is HEAD or, with local changes, a snapshot commit of them
// (git stash create). Tiegel commits checkpoints in the plan branch; the
// user's branch is never touched.
type Worktree struct {
	// Repo is the user's repository, WT the plan worktree.
	Repo *gitcli.Repo
	WT   *gitcli.Repo
	// Root is the worktree root; Sub the project's path inside the
	// repository ("" when the project is the repository root).
	Root    string
	Sub     string
	Branch  string
	BaseRev string
	Note    string

	baseOnce sync.Mutex
	baseDir  string
}

// DirtyPolicy says what happens with uncommitted changes of the user.
type DirtyPolicy string

// Dirty policies (planmode.dirtyTree).
const (
	// DirtySnapshot starts the plan branch from a snapshot commit of the
	// tracked local changes (untracked files are not included).
	DirtySnapshot DirtyPolicy = "snapshot"
	// DirtyHead starts from HEAD and ignores local changes.
	DirtyHead DirtyPolicy = "head"
	// DirtyRefuse refuses to execute with local changes.
	DirtyRefuse DirtyPolicy = "refuse"
)

// ErrDirty is returned by OpenWorktree with DirtyRefuse.
var ErrDirty = errors.New("the working tree has uncommitted changes")

// OpenWorktree creates (or reopens) the worktree of plan id for the
// project at root.
func OpenWorktree(ctx context.Context, root, id string, dirty DirtyPolicy) (*Worktree, error) {
	repo, err := gitcli.Open(ctx, root)
	if err != nil {
		return nil, err
	}
	rr, _ := filepath.EvalSymlinks(repo.Root)
	pr, _ := filepath.EvalSymlinks(root)
	sub, err := filepath.Rel(rr, pr)
	if err != nil || strings.HasPrefix(sub, "..") {
		return nil, fmt.Errorf("orchestrate: project %s is not inside %s", root, repo.Root)
	}
	if sub == "." {
		sub = ""
	}
	wtRoot := filepath.Join(root, filepath.FromSlash(WorktreeDirName), id)
	w := &Worktree{Repo: repo, Root: wtRoot, Sub: sub, Branch: "tiegel/plan-" + id}
	exclude := filepath.ToSlash(filepath.Join(sub, WorktreeDirName)) + "/"
	if err := repo.Exclude("/" + strings.TrimPrefix(exclude, "/")); err != nil {
		return nil, err
	}
	// Files the test runner generates in the worktree.
	if err := repo.Exclude(".tiegel-plan-*"); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(wtRoot, ".git")); err == nil {
		if w.WT, err = gitcli.Open(ctx, wtRoot); err != nil {
			return nil, err
		}
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(wtRoot), id+".base")); err == nil {
			w.BaseRev = strings.TrimSpace(string(b))
		}
		w.Note = "resumed the existing plan worktree"
		return w, nil
	}
	info, err := repo.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.Unborn {
		return nil, errors.New("orchestrate: the repository has no commit yet")
	}
	base := info.Head
	tracked, untracked, err := repo.Dirty(ctx)
	if err != nil {
		return nil, err
	}
	if tracked || untracked {
		switch dirty {
		case DirtyRefuse:
			return nil, ErrDirty
		case DirtyHead:
			w.Note = "local changes are not part of the plan workspace (started from HEAD)"
		default:
			if tracked {
				snap, err := repo.StashCreate(ctx)
				if err != nil {
					return nil, err
				}
				if snap != "" {
					base = snap
					w.Note = "the plan workspace starts from a snapshot of your uncommitted changes"
				}
			}
			if untracked {
				w.Note = strings.TrimPrefix(w.Note+"; untracked files are not included", "; ")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(wtRoot), 0o755); err != nil {
		return nil, fmt.Errorf("orchestrate: worktree: %w", err)
	}
	if err := repo.WorktreeAdd(ctx, wtRoot, w.Branch, base); err != nil {
		return nil, err
	}
	w.BaseRev = base
	_ = os.WriteFile(filepath.Join(filepath.Dir(wtRoot), id+".base"), []byte(base+"\n"), 0o644)
	if w.WT, err = gitcli.Open(ctx, wtRoot); err != nil {
		return nil, err
	}
	return w, nil
}

// Dir implements Workspace.
func (w *Worktree) Dir() string { return filepath.Join(w.Root, filepath.FromSlash(w.Sub)) }

// Info implements Workspace.
func (w *Worktree) Info() api.PlanWorkspace {
	return api.PlanWorkspace{Mode: "worktree", Dir: w.Dir(), Branch: w.Branch, Base: w.BaseRev, Note: w.Note}
}

// BeforeEdit implements Workspace (git keeps the history).
func (w *Worktree) BeforeEdit(string) {}

// repoPath returns the repository path of a project relative path.
func (w *Worktree) repoPath(p string) string {
	if w.Sub == "" {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(filepath.Join(w.Sub, p))
}

// Changes implements Workspace: base against the worktree (including
// uncommitted and untracked files).
func (w *Worktree) Changes(ctx context.Context) ([]FileChange, error) {
	if _, err := w.WT.CheckpointAll(ctx, "plan: work in progress"); err != nil {
		return nil, err
	}
	files, err := w.WT.CompareRevisions(ctx, w.BaseRev, "HEAD")
	if err != nil {
		return nil, err
	}
	var out []FileChange
	for _, f := range files {
		rp := f.Path
		rel := rp
		if w.Sub != "" {
			r, err := filepath.Rel(w.Sub, rp)
			if err != nil || strings.HasPrefix(r, "..") {
				continue
			}
			rel = filepath.ToSlash(r)
		}
		old, existed, _ := w.WT.FileAt(ctx, w.BaseRev, rp)
		cur, err := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(rp)))
		out = append(out, newChange(rel, old, cur, existed, err == nil))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Checkpoint implements Workspace.
func (w *Worktree) Checkpoint(ctx context.Context, msg string) error {
	_, err := w.WT.CheckpointAll(ctx, msg)
	return err
}

// BaseDir implements Workspace: a detached worktree at the base commit
// (<id>-base), created on first use.
func (w *Worktree) BaseDir(ctx context.Context) (string, error) {
	w.baseOnce.Lock()
	defer w.baseOnce.Unlock()
	if w.baseDir != "" {
		return w.baseDir, nil
	}
	dir := w.Root + "-base"
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := w.Repo.WorktreeAdd(ctx, dir, "", w.BaseRev); err != nil {
			return "", err
		}
	}
	w.baseDir = filepath.Join(dir, filepath.FromSlash(w.Sub))
	return w.baseDir, nil
}

// ResetBase implements Workspace.
func (w *Worktree) ResetBase(ctx context.Context) error {
	w.baseOnce.Lock()
	dir := w.baseDir
	w.baseOnce.Unlock()
	if dir == "" {
		return nil
	}
	r, err := gitcli.Open(ctx, dir)
	if err != nil {
		return err
	}
	return r.Clean(ctx)
}

// Revert implements Workspace: the base version of the file.
func (w *Worktree) Revert(ctx context.Context, path string) error {
	rp := w.repoPath(path)
	abs := filepath.Join(w.Root, filepath.FromSlash(rp))
	data, ok, err := w.WT.FileAt(ctx, w.BaseRev, rp)
	if err != nil {
		return err
	}
	if !ok {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("orchestrate: revert %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("orchestrate: revert %s: %w", path, err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("orchestrate: revert %s: %w", path, err)
	}
	return nil
}

// Patch returns the plan's changes as a patch (base..plan branch, after
// a checkpoint) for applying to the user's working copy.
func (w *Worktree) Patch(ctx context.Context) ([]byte, error) {
	if _, err := w.WT.CheckpointAll(ctx, "plan: final state"); err != nil {
		return nil, err
	}
	return w.WT.RawDiff(ctx, gitcli.DiffOptions{Base: w.BaseRev, Target: "HEAD", NoRenames: true})
}

// Apply applies the plan's changes to the user's working copy (git apply
// --3way; nothing is committed on the user's branch).
func (w *Worktree) Apply(ctx context.Context) error {
	patch, err := w.Patch(ctx)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(patch))) == 0 {
		return nil
	}
	return w.Repo.ApplyThreeWay(ctx, patch)
}

// Remove deletes the worktrees; keepBranch keeps the plan branch.
func (w *Worktree) Remove(ctx context.Context, keepBranch bool) error {
	_ = w.Repo.WorktreeRemove(ctx, w.Root+"-base", true)
	if err := w.Repo.WorktreeRemove(ctx, w.Root, true); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(filepath.Dir(w.Root), filepath.Base(w.Root)+".base"))
	if keepBranch {
		return nil
	}
	return w.Repo.BranchDelete(ctx, w.Branch, true)
}
