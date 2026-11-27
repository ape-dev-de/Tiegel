package gitcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Worktrees and snapshots for isolated work (plan mode, D569).

// tiegelIdentity is the author of commits Tiegel itself makes inside its
// own branches (checkpoints of a plan); the user's identity is not used
// and no hooks run.
var tiegelIdentity = []string{
	"GIT_AUTHOR_NAME=Tiegel", "GIT_AUTHOR_EMAIL=tiegel@localhost",
	"GIT_COMMITTER_NAME=Tiegel", "GIT_COMMITTER_EMAIL=tiegel@localhost",
}

// WorktreeAdd creates a linked worktree at dir on a new branch that
// starts at base (git worktree add -b branch dir base).
func (r *Repo) WorktreeAdd(ctx context.Context, dir, branch, base string) error {
	args := []string{"worktree", "add", "-q"}
	if branch != "" {
		args = append(args, "-b", branch)
	} else {
		args = append(args, "--detach")
	}
	args = append(args, dir, base)
	if _, err := r.run(ctx, runOpts{}, args...); err != nil {
		return fmt.Errorf("gitcli: worktree add: %w", err)
	}
	return nil
}

// WorktreeRemove removes the linked worktree at dir (force: also with
// local changes) and prunes stale entries.
func (r *Repo) WorktreeRemove(ctx context.Context, dir string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, dir)
	_, err := r.run(ctx, runOpts{}, args...)
	_, _ = r.run(ctx, runOpts{}, "worktree", "prune")
	if err != nil {
		if _, serr := os.Stat(dir); os.IsNotExist(serr) {
			return nil
		}
		return fmt.Errorf("gitcli: worktree remove: %w", err)
	}
	return nil
}

// BranchDelete deletes a local branch (force: also when not merged).
func (r *Repo) BranchDelete(ctx context.Context, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := r.run(ctx, runOpts{}, "branch", flag, name); err != nil {
		return fmt.Errorf("gitcli: branch delete: %w", err)
	}
	return nil
}

// StashCreate makes a commit of the uncommitted changes of tracked files
// without touching the work tree, the index or the stash list (git stash
// create). It returns "" when there is nothing to save.
func (r *Repo) StashCreate(ctx context.Context) (string, error) {
	out, err := r.run(ctx, runOpts{env: tiegelIdentity}, "stash", "create", "tiegel snapshot")
	if err != nil {
		return "", fmt.Errorf("gitcli: stash create: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Dirty reports whether the work tree has uncommitted changes of tracked
// files or untracked files (not ignored).
func (r *Repo) Dirty(ctx context.Context) (tracked, untracked bool, err error) {
	out, err := r.run(ctx, runOpts{}, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return false, false, fmt.Errorf("gitcli: status: %w", err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "??"):
			untracked = true
		case strings.TrimSpace(l) != "":
			tracked = true
		}
	}
	return tracked, untracked, nil
}

// CheckpointAll stages everything in the work tree and commits it with
// Tiegel as author, without hooks (only for Tiegel's own branches). It
// returns the new HEAD, or the old one when nothing changed.
func (r *Repo) CheckpointAll(ctx context.Context, message string) (string, error) {
	if _, err := r.run(ctx, runOpts{}, "add", "-A"); err != nil {
		return "", fmt.Errorf("gitcli: add: %w", err)
	}
	if !r.stagedChanges(ctx) {
		return r.resolve(ctx, "HEAD")
	}
	if _, err := r.run(ctx, runOpts{env: tiegelIdentity}, "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", message); err != nil {
		return "", fmt.Errorf("gitcli: checkpoint: %w", err)
	}
	return r.resolve(ctx, "HEAD")
}

// stagedChanges reports whether the index differs from HEAD.
func (r *Repo) stagedChanges(ctx context.Context) bool {
	_, err := r.run(ctx, runOpts{}, "diff", "--cached", "--quiet")
	return err != nil
}

// Exclude adds pattern to the repository's info/exclude (shared by all
// worktrees) unless it is there already.
func (r *Repo) Exclude(pattern string) error {
	file := filepath.Join(r.CommonDir, "info", "exclude")
	data, _ := os.ReadFile(file)
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("gitcli: exclude: %w", err)
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += pattern + "\n"
	if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
		return fmt.Errorf("gitcli: exclude: %w", err)
	}
	return nil
}

// Clean resets the work tree to HEAD: tracked changes are discarded and
// untracked files removed (ignored files stay).
func (r *Repo) Clean(ctx context.Context) error {
	if _, err := r.run(ctx, runOpts{}, "checkout", "-q", "--", "."); err != nil {
		return fmt.Errorf("gitcli: checkout: %w", err)
	}
	if _, err := r.run(ctx, runOpts{}, "clean", "-q", "-fd"); err != nil {
		return fmt.Errorf("gitcli: clean: %w", err)
	}
	return nil
}

// ApplyThreeWay applies patch to the work tree (and index) with a
// three-way merge fallback (git apply --3way).
func (r *Repo) ApplyThreeWay(ctx context.Context, patch []byte) error {
	if _, err := r.run(ctx, runOpts{stdin: patch}, "apply", "--3way", "--whitespace=nowarn"); err != nil {
		return fmt.Errorf("gitcli: apply: %w", err)
	}
	return nil
}
