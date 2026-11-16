package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotRepository is returned by [Open] when the directory is not inside a
// git work tree.
var ErrNotRepository = errors.New("gitcli: not a git repository")

// ErrNotFound is returned when a revision or a path at a revision does not
// exist.
var ErrNotFound = errors.New("gitcli: not found")

// Repo is a git work tree. It is immutable after [Open] and safe for
// concurrent use; every method starts its own git process.
type Repo struct {
	// Root is the absolute path of the work tree (rev-parse --show-toplevel).
	Root string
	// GitDir is the absolute git directory of this work tree (for linked
	// worktrees the per-worktree directory).
	GitDir string
	// CommonDir is the absolute shared git directory (equals GitDir unless
	// the work tree is a linked worktree).
	CommonDir string
}

// Open finds the repository containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	out, err := runGit(ctx, dir, runOpts{}, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		if errors.Is(err, ErrGitNotFound) {
			return nil, err
		}
		if se := stderrOf(err); strings.Contains(se, "not a git repository") || strings.Contains(se, "must be run in a work tree") {
			return nil, fmt.Errorf("%s: %w", dir, ErrNotRepository)
		}
		return nil, fmt.Errorf("gitcli: open %s: %w", dir, err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 3 || lines[0] == "" {
		// --show-toplevel prints nothing inside a bare repository.
		return nil, fmt.Errorf("%s: %w (no work tree)", dir, ErrNotRepository)
	}
	r := &Repo{Root: filepath.Clean(lines[0]), GitDir: filepath.Clean(lines[1])}
	common := lines[2]
	if !filepath.IsAbs(common) {
		if abs, err := filepath.Abs(filepath.Join(dir, common)); err == nil {
			common = abs
		}
	}
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		common = resolved
	}
	r.CommonDir = filepath.Clean(common)
	return r, nil
}

// run executes git in the work tree root.
func (r *Repo) run(ctx context.Context, o runOpts, args ...string) ([]byte, error) {
	return runGit(ctx, r.Root, o, args...)
}

// RepoState is an operation in progress in the work tree.
type RepoState string

// Repository states, see [Info].
const (
	StateClean       RepoState = ""
	StateMerging     RepoState = "merging"
	StateRebasing    RepoState = "rebasing"
	StateApplyingAM  RepoState = "am"
	StateCherryPick  RepoState = "cherry-picking"
	StateReverting   RepoState = "reverting"
	StateBisecting   RepoState = "bisecting"
	StateRebaseInter RepoState = "rebasing-interactive"
)

// Info describes the current HEAD and repository state.
type Info struct {
	Root     string
	GitDir   string
	Branch   string // short branch name, "" when detached
	Head     string // commit hash of HEAD, "" for an unborn branch
	Detached bool
	Unborn   bool // HEAD points to a branch without commits
	State    RepoState
}

// Info reads branch, HEAD and the operation in progress.
func (r *Repo) Info(ctx context.Context) (*Info, error) {
	info := &Info{Root: r.Root, GitDir: r.GitDir}
	out, err := r.run(ctx, runOpts{okCodes: []int{1}}, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("gitcli: read HEAD: %w", err)
	}
	info.Branch = strings.TrimSpace(string(out))
	info.Detached = info.Branch == ""
	head, err := r.resolve(ctx, "HEAD")
	switch {
	case errors.Is(err, ErrNotFound):
		info.Unborn = true
	case err != nil:
		return nil, err
	default:
		info.Head = head
	}
	info.State = r.state()
	return info, nil
}

// resolve returns the commit hash for rev or ErrNotFound.
func (r *Repo) resolve(ctx context.Context, rev string) (string, error) {
	out, err := r.run(ctx, runOpts{}, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", fmt.Errorf("gitcli: resolve %s: %w", rev, ErrNotFound)
		}
		return "", fmt.Errorf("gitcli: resolve %s: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveCommit returns the full hash of the commit rev names.
func (r *Repo) ResolveCommit(ctx context.Context, rev string) (string, error) {
	return r.resolve(ctx, rev)
}

func (r *Repo) exists(name string) bool {
	_, err := os.Stat(filepath.Join(r.GitDir, name))
	return err == nil
}

func (r *Repo) state() RepoState {
	switch {
	case r.exists("rebase-merge"):
		if r.exists(filepath.Join("rebase-merge", "interactive")) {
			return StateRebaseInter
		}
		return StateRebasing
	case r.exists("rebase-apply"):
		if r.exists(filepath.Join("rebase-apply", "applying")) {
			return StateApplyingAM
		}
		return StateRebasing
	case r.exists("MERGE_HEAD"):
		return StateMerging
	case r.exists("CHERRY_PICK_HEAD"):
		return StateCherryPick
	case r.exists("REVERT_HEAD"):
		return StateReverting
	case r.exists("BISECT_LOG"):
		return StateBisecting
	}
	return StateClean
}

// emptyTree returns the hash of the empty tree for the repository's hash
// algorithm.
func (r *Repo) emptyTree(ctx context.Context) (string, error) {
	out, err := r.run(ctx, runOpts{stdin: []byte{}}, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", fmt.Errorf("gitcli: empty tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// headOrEmptyTree returns "HEAD" or, for an unborn branch, the empty tree.
func (r *Repo) headOrEmptyTree(ctx context.Context) (string, error) {
	if _, err := r.resolve(ctx, "HEAD"); err != nil {
		if errors.Is(err, ErrNotFound) {
			return r.emptyTree(ctx)
		}
		return "", err
	}
	return "HEAD", nil
}

// abs returns the absolute path of a repository relative path.
func (r *Repo) abs(path string) string {
	return filepath.Join(r.Root, filepath.FromSlash(path))
}

// RelPath converts an absolute file system path into a repository relative
// slash path as git uses it.
func (r *Repo) RelPath(absPath string) (string, error) {
	if rel, ok := relInside(r.Root, absPath); ok {
		return rel, nil
	}
	// The root reported by git has symlinks resolved (e.g. /private/var on
	// macOS); resolve the deepest existing parent of absPath as well.
	dir, rest := filepath.Clean(absPath), ""
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if rel, ok := relInside(r.Root, filepath.Join(resolved, rest)); ok {
				return rel, nil
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
	return "", fmt.Errorf("gitcli: %s is outside of %s", absPath, r.Root)
}

func relInside(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
