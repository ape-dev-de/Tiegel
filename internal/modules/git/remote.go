package git

import (
	"context"
	"errors"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Network access (fetch, pull, update) only happens in these user-triggered
// methods; the module never fetches on its own.
func (m *Module) registerRemote(p *api.Provider) {
	api.Add(p, "git.fetch", "Fetch all remotes or one (prune, tags; network access)", withTask(m, true, fetchTitle, m.gitFetch))
	api.Add(p, "git.pull", "Fetch and integrate a remote branch into the current branch (merge, rebase, ff-only)", withTask(m, true, titled[PullParams]("Pulling"), m.gitPull))
	api.Add(p, "git.updateProject", "Save documents, fetch the upstream and integrate it into the current branch", withTask(m, true, titled[UpdateParams]("Updating project"), m.gitUpdateProject))
	api.Add(p, "git.stash", "Stash local changes (optionally untracked files, keep index, paths)", m.gitStash)
	api.Add(p, "git.stashList", "Stash entries, newest first", m.gitStashList)
	api.Add(p, "git.stashApply", "Apply a stash entry (conflicts are reported, not an error)", m.gitStashApply)
	api.Add(p, "git.stashPop", "Apply and drop a stash entry (kept on conflicts)", m.gitStashPop)
	api.Add(p, "git.stashDrop", "Drop a stash entry", m.gitStashDrop)
}

// RemoteErrorInfo is the error data of a failed fetch, pull or stash
// operation. Kind is auth, network, repoNotFound, noRemote, noUpstream,
// localChanges, diverged, detached or failed.
type RemoteErrorInfo = PushErrorInfo

// remoteErr maps gitcli errors of remote/stash operations to API errors.
func remoteErr(err error) error {
	var re *gitcli.RemoteError
	if errors.As(err, &re) {
		info := RemoteErrorInfo{Kind: string(re.Kind), Message: re.Message, Stderr: re.Stderr}
		return api.Errorf(api.CodeFailed, "%s failed (%s): %s", re.Op, info.Kind, info.Message).WithData(info)
	}
	if errors.Is(err, gitcli.ErrStaleStash) {
		return api.Errorf(api.CodeConflict, "%v; reload the stash list", err).WithData(map[string]string{"reason": "staleStash"})
	}
	return gitErr(err)
}

// saveDocs saves all modified documents before the work tree changes.
func (m *Module) saveDocs(ctx context.Context) error {
	if err := m.app.SaveAllDocuments(ctx, "beforeUpdate"); err != nil {
		return api.Errorf(api.CodeFailed, "%w", err)
	}
	return nil
}

// FetchParams are the params of "git.fetch".
type FetchParams struct {
	// Remote selects one remote; empty fetches all.
	Remote string `json:"remote,omitempty"`
	Prune  bool   `json:"prune,omitempty"`
	Tags   bool   `json:"tags,omitempty"`
}

// RefUpdate is a ref changed by a fetch (old empty: new ref, new empty:
// pruned).
type RefUpdate struct {
	Ref string `json:"ref"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// FetchResult is the result of "git.fetch".
type FetchResult struct {
	Remotes []string    `json:"remotes"`
	Updated []RefUpdate `json:"updated"`
}

func toFetchResult(fr *gitcli.FetchResult) FetchResult {
	res := FetchResult{Remotes: fr.Remotes, Updated: []RefUpdate{}}
	for _, u := range fr.Updated {
		res.Updated = append(res.Updated, RefUpdate(u))
	}
	return res
}

func (m *Module) gitFetch(ctx context.Context, p FetchParams) (FetchResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return FetchResult{}, err
	}
	fr, err := r.Fetch(ctx, gitcli.FetchOptions{Remote: p.Remote, Prune: p.Prune, Tags: p.Tags})
	if err != nil {
		return FetchResult{}, remoteErr(err)
	}
	m.changed("fetch")
	return toFetchResult(fr), nil
}

// PullParams are the params of "git.pull".
type PullParams struct {
	// Remote and Branch default to the upstream of the current branch.
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Strategy is merge, rebase or ff-only; empty uses the setting
	// git.updateMethod (default merge).
	Strategy  string `json:"strategy,omitempty"`
	Autostash bool   `json:"autostash,omitempty"`
	Prune     bool   `json:"prune,omitempty"`
}

// UpdateParams are the params of "git.updateProject".
type UpdateParams struct {
	// Strategy as in PullParams (empty: git.updateMethod).
	Strategy  string `json:"strategy,omitempty"`
	Prune     bool   `json:"prune,omitempty"`
	Autostash bool   `json:"autostash,omitempty"`
}

// PullResult is the result of "git.pull" and "git.updateProject". With
// conflicts the operation stops (state merging/rebasing): resolve them with
// git.conflicts, git.mergeData and git.resolve.
type PullResult struct {
	Branch       string      `json:"branch"`
	Remote       string      `json:"remote"`
	RemoteBranch string      `json:"remoteBranch"`
	Before       string      `json:"before,omitempty"`
	After        string      `json:"after,omitempty"`
	UpToDate     bool        `json:"upToDate"`
	FastForward  bool        `json:"fastForward,omitempty"`
	Changed      []string    `json:"changed"`
	Conflicts    []string    `json:"conflicts"`
	State        string      `json:"state,omitempty"`
	Fetched      []RefUpdate `json:"fetched"`
	Ahead        int         `json:"ahead"`
	Behind       int         `json:"behind"`
}

func (m *Module) gitPull(ctx context.Context, p PullParams) (PullResult, error) {
	return m.pull(ctx, "pull", gitcli.PullOptions{Remote: p.Remote, Branch: p.Branch, Autostash: p.Autostash, Prune: p.Prune}, p.Strategy)
}

func (m *Module) gitUpdateProject(ctx context.Context, p UpdateParams) (PullResult, error) {
	return m.pull(ctx, "update", gitcli.PullOptions{Autostash: p.Autostash, Prune: p.Prune}, p.Strategy)
}

// pull saves the documents, fetches (outside the save lock: network), then
// integrates and reloads the open documents under the save lock.
func (m *Module) pull(ctx context.Context, op string, opts gitcli.PullOptions, strategy string) (PullResult, error) {
	if strategy == "" {
		strategy = api.Setting(m.app, SettingUpdateMethod, "merge")
	}
	s, err := gitcli.ParseStrategy(strategy)
	if err != nil {
		return PullResult{}, api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	opts.Strategy = s
	r, err := m.repo(ctx)
	if err != nil {
		return PullResult{}, err
	}
	t, err := r.ResolvePull(ctx, opts)
	if err != nil {
		return PullResult{}, remoteErr(err)
	}
	if err := m.saveDocs(ctx); err != nil {
		return PullResult{}, err
	}
	res := PullResult{Branch: t.Branch, Remote: t.Remote, RemoteBranch: t.RemoteBranch, Changed: []string{}, Conflicts: []string{}, Fetched: []RefUpdate{}}
	if t.Remote != "." {
		fr, err := r.Fetch(ctx, gitcli.FetchOptions{Remote: t.Remote, Prune: opts.Prune})
		if err != nil {
			return PullResult{}, remoteErr(err)
		}
		res.Fetched = toFetchResult(fr).Updated
	}
	var pr *gitcli.PullResult
	if err := m.app.Exclusive(func() error {
		var err error
		pr, err = r.Integrate(ctx, gitcli.IntegrateOptions{Target: t.Ref, Strategy: opts.Strategy, Autostash: opts.Autostash})
		if err != nil {
			return remoteErr(err)
		}
		if !pr.UpToDate {
			m.reloadAll()
		}
		return nil
	}); err != nil {
		return PullResult{}, err
	}
	res.Before, res.After, res.UpToDate, res.FastForward, res.State = pr.Before, pr.After, pr.UpToDate, pr.FastForward, string(pr.State)
	res.Changed = append(res.Changed, pr.Changed...)
	res.Conflicts = append(res.Conflicts, pr.Conflicts...)
	if st, err := r.Status(ctx, gitcli.StatusOptions{NoUntracked: true}); err == nil {
		res.Ahead, res.Behind = st.Branch.Ahead, st.Branch.Behind
	}
	paths := append(append([]string(nil), res.Changed...), res.Conflicts...)
	m.changed(op, paths...)
	return res, nil
}

// StashParams are the params of "git.stash".
type StashParams struct {
	Message          string   `json:"message,omitempty"`
	IncludeUntracked bool     `json:"includeUntracked,omitempty"`
	KeepIndex        bool     `json:"keepIndex,omitempty"`
	Paths            []string `json:"paths,omitempty"`
}

// StashEntry is one stash entry.
type StashEntry struct {
	Index   int       `json:"index"`
	Ref     string    `json:"ref"`
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
}

// StashResult is the result of "git.stash".
type StashResult struct {
	// Created is false when there was nothing to stash.
	Created bool        `json:"created"`
	Entry   *StashEntry `json:"entry,omitempty"`
}

// StashRefParams address a stash entry for apply, pop and drop. Hash, if
// set, must be (a prefix of) the entry's commit; otherwise the stash stack
// changed and the call fails with CodeConflict.
type StashRefParams struct {
	Index        int    `json:"index,omitempty"`
	Hash         string `json:"hash,omitempty"`
	RestoreIndex bool   `json:"restoreIndex,omitempty"`
}

// StashApplyResult is the result of "git.stashApply" and "git.stashPop".
type StashApplyResult struct {
	Conflicts []string `json:"conflicts"`
	Dropped   bool     `json:"dropped"`
}

func (m *Module) gitStash(ctx context.Context, p StashParams) (StashResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return StashResult{}, err
	}
	paths, err := m.rels(r, p.Paths)
	if err != nil {
		return StashResult{}, err
	}
	if err := m.saveDocs(ctx); err != nil {
		return StashResult{}, err
	}
	var created bool
	if err := m.app.Exclusive(func() error {
		var err error
		created, err = r.StashPush(ctx, gitcli.StashOptions{Message: p.Message, IncludeUntracked: p.IncludeUntracked, KeepIndex: p.KeepIndex, Paths: paths})
		if err != nil {
			return remoteErr(err)
		}
		if created {
			m.reloadAll()
		}
		return nil
	}); err != nil {
		return StashResult{}, err
	}
	res := StashResult{Created: created}
	if created {
		if list, err := m.stashList(ctx, r); err == nil && len(list) > 0 {
			res.Entry = &list[0]
		}
		m.changed("stash", paths...)
	}
	return res, nil
}

func (m *Module) stashList(ctx context.Context, r *gitcli.Repo) ([]StashEntry, error) {
	list, err := r.StashList(ctx)
	if err != nil {
		return nil, gitErr(err)
	}
	out := make([]StashEntry, 0, len(list))
	for _, e := range list {
		out = append(out, StashEntry(e))
	}
	return out, nil
}

func (m *Module) gitStashList(ctx context.Context, _ struct{}) ([]StashEntry, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, err
	}
	return m.stashList(ctx, r)
}

func (m *Module) gitStashApply(ctx context.Context, p StashRefParams) (StashApplyResult, error) {
	return m.stashApply(ctx, p, false)
}

func (m *Module) gitStashPop(ctx context.Context, p StashRefParams) (StashApplyResult, error) {
	return m.stashApply(ctx, p, true)
}

func (m *Module) stashApply(ctx context.Context, p StashRefParams, pop bool) (StashApplyResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return StashApplyResult{}, err
	}
	if err := r.CheckStash(ctx, p.Index, p.Hash); err != nil {
		return StashApplyResult{}, remoteErr(err)
	}
	if err := m.saveDocs(ctx); err != nil {
		return StashApplyResult{}, err
	}
	var res *gitcli.StashResult
	if err := m.app.Exclusive(func() error {
		var err error
		if pop {
			res, err = r.StashPop(ctx, p.Index, p.RestoreIndex)
		} else {
			res, err = r.StashApply(ctx, p.Index, p.RestoreIndex)
		}
		if err != nil {
			return remoteErr(err)
		}
		m.reloadAll()
		return nil
	}); err != nil {
		return StashApplyResult{}, err
	}
	op := "stashApply"
	if pop {
		op = "stashPop"
	}
	m.changed(op, res.Conflicts...)
	return StashApplyResult{Conflicts: append([]string{}, res.Conflicts...), Dropped: res.Dropped}, nil
}

func (m *Module) gitStashDrop(ctx context.Context, p StashRefParams) ([]StashEntry, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.CheckStash(ctx, p.Index, p.Hash); err != nil {
		return nil, remoteErr(err)
	}
	if err := r.StashDrop(ctx, p.Index); err != nil {
		return nil, remoteErr(err)
	}
	m.changed("stashDrop")
	return m.stashList(ctx, r)
}
