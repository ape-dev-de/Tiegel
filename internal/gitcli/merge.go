package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidRevision is returned for a revision that git would read as an
// option (leading "-") or that is empty.
var ErrInvalidRevision = errors.New("gitcli: invalid revision")

// OpResult is the outcome of a merge or rebase (or of continuing one).
type OpResult struct {
	// Head is HEAD afterwards.
	Head string
	// State is the operation still in progress: StateMerging or
	// StateRebasing when git stopped on conflicts, StateClean when done.
	State RepoState
	// Conflicts are the unmerged paths (repository relative, slash
	// separated) when git stopped.
	Conflicts []string
	// UpToDate: there was nothing to merge or rebase.
	UpToDate bool
	// FastForward: the merge moved the branch without a merge commit.
	FastForward bool
}

// MergeOptions configures [Repo.Merge]. The zero value is git's default
// behaviour (fast-forward when possible, otherwise a merge commit with
// the default message).
type MergeOptions struct {
	// NoFF always creates a merge commit.
	NoFF bool
	// FFOnly refuses to merge unless it is a fast-forward.
	FFOnly bool
}

// checkRev rejects revisions git would parse as options and verifies
// that rev names a commit.
func (r *Repo) checkRev(ctx context.Context, rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return fmt.Errorf("%w: %q", ErrInvalidRevision, rev)
	}
	_, err := r.resolve(ctx, rev)
	return err
}

// clean returns ErrOperationInProgress unless no merge, rebase,
// cherry-pick, revert or am is in progress.
func (r *Repo) clean() error {
	if st := r.state(); st != StateClean && st != StateBisecting {
		return fmt.Errorf("%w: %s", ErrOperationInProgress, st)
	}
	return nil
}

// Merge merges rev (a branch, remote-tracking branch or commit) into the
// current branch. Conflicts are no error: git stops with the merge in
// progress and the result lists the unmerged paths (finish with
// [Repo.MergeContinue] after resolving, or [Repo.MergeAbort]). Other
// failures, e.g. local changes that would be overwritten, are errors and
// leave the work tree as it was.
func (r *Repo) Merge(ctx context.Context, rev string, opts MergeOptions) (*OpResult, error) {
	if err := r.clean(); err != nil {
		return nil, err
	}
	if err := r.checkRev(ctx, rev); err != nil {
		return nil, fmt.Errorf("gitcli: merge: %w", err)
	}
	args := []string{"merge", "--no-edit"}
	switch {
	case opts.NoFF:
		args = append(args, "--no-ff")
	case opts.FFOnly:
		args = append(args, "--ff-only")
	}
	out, err := r.run(ctx, runOpts{}, append(args, rev)...)
	return r.outcome(ctx, "merge", out, err)
}

// MergeContinue concludes a merge whose conflicts are resolved (all paths
// staged) with the prepared message.
func (r *Repo) MergeContinue(ctx context.Context) (*OpResult, error) {
	if r.state() != StateMerging {
		return nil, fmt.Errorf("gitcli: merge --continue: %w", ErrNoOperation)
	}
	out, err := r.run(ctx, runOpts{}, "merge", "--continue")
	return r.outcome(ctx, "merge --continue", out, err)
}

// MergeAbort aborts the merge in progress and restores the pre-merge
// state.
func (r *Repo) MergeAbort(ctx context.Context) error {
	if r.state() != StateMerging {
		return fmt.Errorf("gitcli: merge --abort: %w", ErrNoOperation)
	}
	if _, err := r.run(ctx, runOpts{}, "merge", "--abort"); err != nil {
		return fmt.Errorf("gitcli: merge --abort: %w", err)
	}
	return nil
}

// Rebase rebases the current branch onto upstream (git's default: the
// commits not in upstream are replayed on top of it). On conflicts git
// stops with the rebase in progress; the result lists the unmerged paths
// (resolve and [Repo.RebaseContinue], or [Repo.RebaseAbort]).
func (r *Repo) Rebase(ctx context.Context, upstream string) (*OpResult, error) {
	if err := r.clean(); err != nil {
		return nil, err
	}
	if err := r.checkRev(ctx, upstream); err != nil {
		return nil, fmt.Errorf("gitcli: rebase: %w", err)
	}
	out, err := r.run(ctx, runOpts{}, "rebase", upstream)
	return r.outcome(ctx, "rebase", out, err)
}

// RebaseContinue continues the rebase in progress after the conflicts of
// the current step are resolved (all paths staged).
func (r *Repo) RebaseContinue(ctx context.Context) (*OpResult, error) {
	if !r.rebasing() {
		return nil, fmt.Errorf("gitcli: rebase --continue: %w", ErrNoOperation)
	}
	out, err := r.run(ctx, runOpts{}, "rebase", "--continue")
	return r.outcome(ctx, "rebase --continue", out, err)
}

// RebaseAbort aborts the rebase in progress and restores the original
// branch.
func (r *Repo) RebaseAbort(ctx context.Context) error {
	if !r.rebasing() {
		return fmt.Errorf("gitcli: rebase --abort: %w", ErrNoOperation)
	}
	if _, err := r.run(ctx, runOpts{}, "rebase", "--abort"); err != nil {
		return fmt.Errorf("gitcli: rebase --abort: %w", err)
	}
	return nil
}

// ErrNoOperation is returned when continuing or aborting a merge or
// rebase that is not in progress.
var ErrNoOperation = errors.New("no operation in progress")

func (r *Repo) rebasing() bool {
	st := r.state()
	return st == StateRebasing || st == StateRebaseInter
}

// UnmergedPaths returns the paths with conflicts (repository relative,
// slash separated, sorted as git lists them).
func (r *Repo) UnmergedPaths(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, runOpts{}, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitcli: unmerged paths: %w", err)
	}
	var paths []string
	seen := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		if s := string(p); s != "" && !seen[s] {
			seen[s] = true
			paths = append(paths, s)
		}
	}
	return paths, nil
}

// outcome turns the result of a merge or rebase command into a
// OpResult: stopping on conflicts is a result, anything else an error.
func (r *Repo) outcome(ctx context.Context, op string, out []byte, runErr error) (*OpResult, error) {
	res := &OpResult{State: r.state()}
	if res.State == StateRebaseInter {
		res.State = StateRebasing
	}
	if res.State == StateBisecting {
		res.State = StateClean
	}
	conflicts, err := r.UnmergedPaths(ctx)
	if err != nil {
		return nil, err
	}
	res.Conflicts = conflicts
	if runErr != nil && (res.State == StateClean || len(conflicts) == 0) {
		return nil, fmt.Errorf("gitcli: %s: %w", op, runErr)
	}
	text := string(out)
	res.UpToDate = strings.Contains(text, "Already up to date") || strings.Contains(text, "is up to date")
	res.FastForward = strings.Contains(text, "Fast-forward")
	if head, err := r.resolve(ctx, "HEAD"); err == nil {
		res.Head = head
	}
	return res, nil
}
