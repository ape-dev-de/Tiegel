package controller

import (
	"context"
	"fmt"
)

// GitOpOutcome is the result of git.merge and git.rebase: State is
// "merging" or "rebasing" while git stopped on Conflicts (repository
// relative paths), empty when the operation finished.
type GitOpOutcome struct {
	Op          string   `json:"op"`
	Head        string   `json:"head"`
	State       string   `json:"state"`
	Conflicts   []string `json:"conflicts"`
	UpToDate    bool     `json:"upToDate"`
	FastForward bool     `json:"fastForward"`
	Aborted     bool     `json:"aborted"`
}

// Merge merges branch into the current branch (git's default behaviour).
// On conflicts the first conflicted file opens in a merge window; done
// (optional) runs on the UI goroutine with the outcome.
func (g *Git) Merge(branch string, done func(GitOpOutcome)) {
	g.gitOp("git.merge", map[string]any{"branch": branch}, done)
}

// Rebase rebases the current branch onto branch, like Merge.
func (g *Git) Rebase(onto string, done func(GitOpOutcome)) {
	g.gitOp("git.rebase", map[string]any{"onto": onto}, done)
}

// ContinueOp continues the merge or rebase in progress (Info.State)
// after its conflicts are resolved.
func (g *Git) ContinueOp(done func(GitOpOutcome)) {
	if m := g.opMethod(); m != "" {
		g.gitOp(m, map[string]any{"continue": true}, done)
	}
}

// AbortOp aborts the merge or rebase in progress (Info.State).
func (g *Git) AbortOp(done func(GitOpOutcome)) {
	if m := g.opMethod(); m != "" {
		g.gitOp(m, map[string]any{"abort": true}, done)
	}
}

// opMethod returns the API method of the operation in progress, or ""
// (with a notice) when none is.
func (g *Git) opMethod() string {
	switch g.Info.State {
	case "merging":
		return "git.merge"
	case "rebasing", "rebasing-interactive":
		return "git.rebase"
	}
	g.c.Status.Notice = "No merge or rebase in progress"
	return ""
}

// InProgress returns "merge" or "rebase" while one is in progress.
func (g *Git) InProgress() string {
	switch g.Info.State {
	case "merging":
		return "merge"
	case "rebasing", "rebasing-interactive":
		return "rebase"
	}
	return ""
}

func (g *Git) gitOp(method string, params map[string]any, done func(GitOpOutcome)) {
	c := g.c
	c.Status.Notice = "Running " + method + "…"
	c.Go(func(ctx context.Context) func() {
		var out GitOpOutcome
		err := c.call(ctx, method, params, &out)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = describeOutcome(out)
			if len(out.Conflicts) > 0 {
				g.OpenMerge(out.Conflicts[0])
			}
			g.afterChange()
			if done != nil {
				done(out)
			}
		}
	})
}

// describeOutcome is the status line of a merge or rebase result.
func describeOutcome(o GitOpOutcome) string {
	switch {
	case o.Aborted:
		return "Aborted the " + o.Op
	case len(o.Conflicts) > 0:
		return fmt.Sprintf("%s stopped: conflicts in %d file(s); resolve them, then continue the %s", capital(o.Op), len(o.Conflicts), o.Op)
	case o.UpToDate:
		return "Already up to date"
	case o.FastForward:
		return "Fast-forwarded"
	}
	return capital(o.Op) + " done"
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
