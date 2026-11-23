package git

import (
	"context"
	"errors"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func (m *Module) registerMerge(p *api.Provider) {
	api.Add(p, "git.merge", "Merge a branch into the current one, or continue/abort a merge (conflicts are reported, not errors)", withTask(m, false, mergeTitle, m.gitMerge))
	api.Add(p, "git.rebase", "Rebase the current branch onto a branch, or continue/abort a rebase (conflicts are reported, not errors)", withTask(m, false, rebaseTitle, m.gitRebase))
}

// MergeParams are the params of "git.merge": either Branch (merged into
// the current branch) or one of Continue and Abort.
type MergeParams struct {
	Branch string `json:"branch,omitempty"`
	// NoFF always creates a merge commit; FFOnly refuses anything but a
	// fast-forward. Default: git's behaviour.
	NoFF     bool `json:"noFF,omitempty"`
	FFOnly   bool `json:"ffOnly,omitempty"`
	Continue bool `json:"continue,omitempty"`
	Abort    bool `json:"abort,omitempty"`
}

// RebaseParams are the params of "git.rebase": either Onto (the branch
// the current branch is rebased onto) or one of Continue and Abort.
type RebaseParams struct {
	Onto     string `json:"onto,omitempty"`
	Continue bool   `json:"continue,omitempty"`
	Abort    bool   `json:"abort,omitempty"`
}

// MergeOutcome is the result of "git.merge" and "git.rebase". When git
// stopped on conflicts, State is "merging" or "rebasing" and Conflicts
// lists the unmerged paths (resolve them with git.mergeData/git.resolve,
// then continue); State is empty when the operation finished.
type MergeOutcome struct {
	Op          string   `json:"op"`
	Head        string   `json:"head,omitempty"`
	State       string   `json:"state"`
	Conflicts   []string `json:"conflicts"`
	UpToDate    bool     `json:"upToDate,omitempty"`
	FastForward bool     `json:"fastForward,omitempty"`
	Aborted     bool     `json:"aborted,omitempty"`
}

// opErr maps the merge/rebase errors to API codes.
func opErr(err error) error {
	switch {
	case errors.Is(err, gitcli.ErrInvalidRevision):
		return api.Errorf(api.CodeInvalidParams, "%w", err)
	case errors.Is(err, gitcli.ErrNoOperation):
		return api.Errorf(api.CodeConflict, "%w", err)
	}
	return gitErr(err)
}

// exclusiveOp runs a merge or rebase step under the exclusive lock,
// reloads unmodified documents and publishes git.changed.
func (m *Module) exclusiveOp(ctx context.Context, op string, abort bool, fn func(r *gitcli.Repo) (*gitcli.OpResult, error)) (MergeOutcome, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return MergeOutcome{}, err
	}
	var res *gitcli.OpResult
	if err := m.app.Exclusive(func() error {
		var err error
		if res, err = fn(r); err != nil {
			return opErr(err)
		}
		m.reloadAll()
		return nil
	}); err != nil {
		return MergeOutcome{}, err
	}
	m.changed(op)
	out := MergeOutcome{Op: op, Conflicts: []string{}, Aborted: abort}
	if res != nil {
		out.Head, out.State, out.UpToDate, out.FastForward = res.Head, string(res.State), res.UpToDate, res.FastForward
		if res.Conflicts != nil {
			out.Conflicts = res.Conflicts
		}
	}
	return out, nil
}

func modes(name string, set ...bool) error {
	n := 0
	for _, s := range set {
		if s {
			n++
		}
	}
	if n != 1 {
		return api.Errorf(api.CodeInvalidParams, "exactly one of %s is required", name)
	}
	return nil
}

func (m *Module) gitMerge(ctx context.Context, p MergeParams) (MergeOutcome, error) {
	if err := modes("branch, continue, abort", p.Branch != "", p.Continue, p.Abort); err != nil {
		return MergeOutcome{}, err
	}
	if p.NoFF && p.FFOnly {
		return MergeOutcome{}, api.Errorf(api.CodeInvalidParams, "noFF and ffOnly exclude each other")
	}
	return m.exclusiveOp(ctx, "merge", p.Abort, func(r *gitcli.Repo) (*gitcli.OpResult, error) {
		switch {
		case p.Abort:
			return nil, r.MergeAbort(ctx)
		case p.Continue:
			return r.MergeContinue(ctx)
		}
		return r.Merge(ctx, p.Branch, gitcli.MergeOptions{NoFF: p.NoFF, FFOnly: p.FFOnly})
	})
}

func (m *Module) gitRebase(ctx context.Context, p RebaseParams) (MergeOutcome, error) {
	if err := modes("onto, continue, abort", p.Onto != "", p.Continue, p.Abort); err != nil {
		return MergeOutcome{}, err
	}
	return m.exclusiveOp(ctx, "rebase", p.Abort, func(r *gitcli.Repo) (*gitcli.OpResult, error) {
		switch {
		case p.Abort:
			return nil, r.RebaseAbort(ctx)
		case p.Continue:
			return r.RebaseContinue(ctx)
		}
		return r.Rebase(ctx, p.Onto)
	})
}
