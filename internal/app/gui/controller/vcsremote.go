package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Remote git operations of the GUI (D154): fetch, pull, update project
// and the stash. Network access only happens on these explicit user
// actions. Failures are classified by the git module (RemoteErrorInfo
// kinds) and turned into readable problems.

// Remote is the state of the remote operations and the stash list.
type Remote struct {
	// Busy names the running operation ("" when idle).
	Busy string
	// Problem is the last failure (nil after a success); ProblemVersion
	// changes with it so the view can show it once.
	Problem        *RemoteProblem
	ProblemVersion uint64
	// Conflicts of the last pull/update/stash apply; ConflictVersion
	// changes when new conflicts appear (the view opens the merge
	// window).
	Conflicts       []string
	ConflictVersion uint64
	// Stashes is the stash list (newest first).
	Stashes      []StashEntry
	StashVersion uint64
	// Pull remembers the options of the pull/update dialog.
	Pull PullOptions
}

// PullOptions are the choices of the pull and update dialogs.
type PullOptions struct {
	// Strategy is merge, rebase or ff-only ("" = merge).
	Strategy  string
	Autostash bool
	Prune     bool
}

// PullStrategies are the strategies in dialog order.
var PullStrategies = []string{"merge", "rebase", "ff-only"}

// StashEntry is an entry of git.stashList.
type StashEntry struct {
	Index   int       `json:"index"`
	Ref     string    `json:"ref"`
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
}

// RemoteProblem is a readable failure of a remote operation.
type RemoteProblem struct {
	// Op is fetch, pull, update, stash, …; Kind the module's
	// classification (auth, network, repoNotFound, noRemote, noUpstream,
	// localChanges, diverged, detached, staleStash, failed).
	Op, Kind string
	Title    string
	Detail   string
}

// DescribeRemoteError turns a failure of op into a problem with an
// explanation and the next step.
func DescribeRemoteError(op string, err error) *RemoteProblem {
	p := &RemoteProblem{Op: op, Kind: "failed", Detail: err.Error()}
	var ae *api.Error
	if errors.As(err, &ae) && ae.Data != nil {
		var info pushErrorInfo
		var stale struct {
			Reason string `json:"reason"`
		}
		switch {
		case decodeData(ae.Data, &stale) && stale.Reason == "staleStash":
			p.Kind = "staleStash"
		case decodeData(ae.Data, &info) && info.Kind != "":
			p.Kind = info.Kind
			p.Detail = strings.TrimSpace(info.Message + "\n" + strings.TrimSpace(info.Stderr))
		}
	}
	verb := strings.ToUpper(op[:1]) + op[1:]
	switch p.Kind {
	case "auth":
		p.Title = verb + " failed: authentication failed. Check your credentials or SSH key (Tiegel never asks in a terminal)."
	case "network":
		p.Title = verb + " failed: the remote could not be reached. Check the network connection."
	case "repoNotFound":
		p.Title = verb + " failed: the remote repository was not found."
	case "noRemote":
		p.Title = verb + " failed: the repository has no remote."
	case "noUpstream":
		p.Title = verb + " failed: the current branch has no upstream branch. Push it with an upstream first."
	case "localChanges":
		p.Title = verb + " failed: local changes would be overwritten. Commit or stash them, or enable Autostash."
	case "diverged":
		p.Title = verb + " failed: the branches have diverged. Choose Merge or Rebase instead of fast-forward only."
	case "detached":
		p.Title = verb + " failed: HEAD is detached. Check out a branch first."
	case "staleStash":
		p.Title = "The stash list changed in the meantime. It has been reloaded; choose the entry again."
	default:
		p.Title = verb + " failed."
	}
	return p
}

func (w *Workspace) remoteFailed(op string, err error) {
	r := &w.Remote
	r.Problem = DescribeRemoteError(op, err)
	r.ProblemVersion++
	w.c.Status.Notice = r.Problem.Title
}

func (w *Workspace) remoteOK() {
	if w.Remote.Problem != nil {
		w.Remote.Problem = nil
		w.Remote.ProblemVersion++
	}
}

// DismissRemoteProblem clears the shown problem.
func (w *Workspace) DismissRemoteProblem() { w.remoteOK() }

func (w *Workspace) setConflicts(paths []string) {
	w.Remote.Conflicts = paths
	if len(paths) > 0 {
		w.Remote.ConflictVersion++
	}
}

// Fetch fetches all remotes (git.fetch).
func (w *Workspace) Fetch() {
	c, r := w.c, &w.Remote
	if r.Busy != "" {
		return
	}
	r.Busy = "fetch"
	c.Status.Notice = "Fetching…"
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Remotes []string   `json:"remotes"`
			Updated []struct{} `json:"updated"`
		}
		err := c.call(ctx, "git.fetch", map[string]any{"prune": r.Pull.Prune}, &res)
		return func() {
			r.Busy = ""
			if err != nil {
				w.remoteFailed("fetch", err)
				return
			}
			w.remoteOK()
			if len(res.Updated) == 0 {
				c.Status.Notice = "Fetched: everything is up to date"
			} else {
				c.Status.Notice = fmt.Sprintf("Fetched %d updated ref(s) from %s", len(res.Updated), strings.Join(res.Remotes, ", "))
			}
			c.Vcs.afterChange()
		}
	})
}

// pullResult is the result of git.pull and git.updateProject.
type pullResult struct {
	Branch       string   `json:"branch"`
	Remote       string   `json:"remote"`
	RemoteBranch string   `json:"remoteBranch"`
	UpToDate     bool     `json:"upToDate"`
	FastForward  bool     `json:"fastForward"`
	Changed      []string `json:"changed"`
	Conflicts    []string `json:"conflicts"`
	State        string   `json:"state"`
}

// Pull fetches and integrates the upstream (git.pull).
func (w *Workspace) Pull(o PullOptions) { w.integrate("pull", "git.pull", o) }

// UpdateProject saves, fetches and integrates the upstream
// (git.updateProject).
func (w *Workspace) UpdateProject(o PullOptions) { w.integrate("update", "git.updateProject", o) }

func (w *Workspace) integrate(op, method string, o PullOptions) {
	c, r := w.c, &w.Remote
	if r.Busy != "" {
		return
	}
	r.Pull = o
	r.Busy = op
	if op == "update" {
		c.Status.Notice = "Updating project…"
	} else {
		c.Status.Notice = "Pulling…"
	}
	params := map[string]any{"autostash": o.Autostash, "prune": o.Prune}
	if o.Strategy != "" {
		params["strategy"] = o.Strategy
	}
	c.Go(func(ctx context.Context) func() {
		var res pullResult
		err := c.call(ctx, method, params, &res)
		return func() {
			r.Busy = ""
			if err != nil {
				w.remoteFailed(op, err)
				return
			}
			w.remoteOK()
			w.setConflicts(res.Conflicts)
			switch {
			case len(res.Conflicts) > 0:
				c.Status.Notice = fmt.Sprintf("%d file(s) have conflicts: resolve them in the merge window", len(res.Conflicts))
			case res.UpToDate:
				c.Status.Notice = "Already up to date"
			default:
				c.Status.Notice = fmt.Sprintf("Updated %s from %s/%s: %d file(s) changed", res.Branch, res.Remote, res.RemoteBranch, len(res.Changed))
			}
			c.Vcs.afterChange()
		}
	})
}

// LoadStashes loads the stash list.
func (w *Workspace) LoadStashes() {
	c, r := w.c, &w.Remote
	c.Go(func(ctx context.Context) func() {
		var list []StashEntry
		err := c.call(ctx, "git.stashList", nil, &list)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			r.Stashes = list
			r.StashVersion++
		}
	})
}

// StashOptions are the choices of "Stash Changes…".
type StashOptions struct {
	Message          string
	IncludeUntracked bool
	KeepIndex        bool
}

// Stash stashes the local changes (git.stash; documents are saved first
// by the backend).
func (w *Workspace) Stash(o StashOptions) {
	c, r := w.c, &w.Remote
	if r.Busy != "" {
		return
	}
	r.Busy = "stash"
	params := map[string]any{"includeUntracked": o.IncludeUntracked, "keepIndex": o.KeepIndex}
	if m := strings.TrimSpace(o.Message); m != "" {
		params["message"] = m
	}
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Created bool `json:"created"`
		}
		err := c.call(ctx, "git.stash", params, &res)
		return func() {
			r.Busy = ""
			if err != nil {
				w.remoteFailed("stash", err)
				return
			}
			w.remoteOK()
			if res.Created {
				c.Status.Notice = "Stashed the local changes"
			} else {
				c.Status.Notice = "No local changes to stash"
			}
			w.LoadStashes()
			c.Vcs.afterChange()
		}
	})
}

// StashApply applies (pop: and drops) a stash entry.
func (w *Workspace) StashApply(e StashEntry, pop bool) {
	c, r := w.c, &w.Remote
	if r.Busy != "" {
		return
	}
	method, op := "git.stashApply", "stash apply"
	if pop {
		method, op = "git.stashPop", "stash pop"
	}
	r.Busy = op
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Conflicts []string `json:"conflicts"`
			Dropped   bool     `json:"dropped"`
		}
		err := c.call(ctx, method, map[string]any{"index": e.Index, "hash": e.Hash}, &res)
		return func() {
			r.Busy = ""
			if err != nil {
				w.remoteFailed(op, err)
				w.LoadStashes()
				return
			}
			w.remoteOK()
			w.setConflicts(res.Conflicts)
			switch {
			case len(res.Conflicts) > 0:
				c.Status.Notice = fmt.Sprintf("Applied %s with conflicts in %d file(s); the entry was kept", e.Ref, len(res.Conflicts))
			case res.Dropped:
				c.Status.Notice = "Popped " + e.Ref
			default:
				c.Status.Notice = "Applied " + e.Ref
			}
			w.LoadStashes()
			c.Vcs.afterChange()
		}
	})
}

// StashDrop removes a stash entry.
func (w *Workspace) StashDrop(e StashEntry) {
	c, r := w.c, &w.Remote
	c.Go(func(ctx context.Context) func() {
		var rest []StashEntry
		err := c.call(ctx, "git.stashDrop", map[string]any{"index": e.Index, "hash": e.Hash}, &rest)
		return func() {
			if err != nil {
				w.remoteFailed("stash drop", err)
				w.LoadStashes()
				return
			}
			w.remoteOK()
			r.Stashes = rest
			r.StashVersion++
			c.Status.Notice = "Dropped " + e.Ref
		}
	})
}
