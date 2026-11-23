package git

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

// Long-running git operations are background tasks (D507). Network
// operations (fetch, pull, update, push) can be cancelled: the task's
// context ends and gitcli kills the git process. Commits, merges and
// rebases are listed but not cancellable, so git never stops halfway
// through writing the repository (index.lock, half-applied rebase).

// withTask wraps an API handler so that every call is a background task
// titled by title(params).
func withTask[P, R any](m *Module, cancellable bool, title func(P) string, fn func(context.Context, P) (R, error)) func(context.Context, P) (R, error) {
	return func(ctx context.Context, p P) (R, error) {
		t := m.app.BeginTask(ctx, tasks.Options{Title: title(p), Module: ID, Cancellable: cancellable})
		r, err := fn(t.Context(), p)
		t.End(err)
		return r, err
	}
}

func titled[P any](s string) func(P) string { return func(P) string { return s } }

func commitTitle(push bool) string {
	if push {
		return "Committing and pushing"
	}
	return "Committing"
}

func pushTitle(p PushParams) string {
	if p.Branch != "" {
		return "Pushing " + p.Branch
	}
	return "Pushing"
}

func fetchTitle(p FetchParams) string {
	if p.Remote != "" {
		return "Fetching " + p.Remote
	}
	return "Fetching"
}

func mergeTitle(p MergeParams) string {
	switch {
	case p.Abort:
		return "Aborting merge"
	case p.Continue:
		return "Continuing merge"
	}
	return "Merging " + p.Branch
}

func rebaseTitle(p RebaseParams) string {
	switch {
	case p.Abort:
		return "Aborting rebase"
	case p.Continue:
		return "Continuing rebase"
	}
	return "Rebasing onto " + p.Onto
}
