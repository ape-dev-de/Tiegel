package api

import (
	"context"
	"encoding/json"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Kernel methods of the commit tool window (D467,
// AP18.4): "vcs.changes" groups git.status into the change tree and
// "vcs.diffTab" resolves what the diff editor tab of one change compares,
// its neighbours for next/previous file, and loads the diff
// (git.fileDiff). Both are clients of the git module's methods; grouping,
// ordering and version choice are gitcli's pure functions, which the GUI
// uses as well, so GUI, API and headless commands agree.

// VcsChangesParams are the params of "vcs.changes".
type VcsChangesParams struct {
	// GroupBy is "directory" (default), "module" or "none".
	GroupBy string `json:"groupBy,omitempty"`
}

// VcsChangesResult is the result of "vcs.changes".
type VcsChangesResult struct {
	Branch string            `json:"branch,omitempty"`
	Tree   gitcli.ChangeTree `json:"tree"`
	// Order lists the file paths in display order.
	Order []string `json:"order"`
}

// VcsDiffTabParams are the params of "vcs.diffTab".
type VcsDiffTabParams struct {
	// Path is the repository-relative path of a changed file.
	Path string `json:"path"`
	// Mode is "local" (HEAD ↔ work tree, default), "staged" or
	// "unstaged".
	Mode string `json:"mode,omitempty"`
	// Step moves from Path to the next (1) or previous (-1) change in
	// display order before resolving (wrapping around).
	Step int `json:"step,omitempty"`
	// GroupBy is the grouping whose order Step follows.
	GroupBy string `json:"groupBy,omitempty"`
	// IgnoreWhitespace is passed to git.fileDiff.
	IgnoreWhitespace bool `json:"ignoreWhitespace,omitempty"`
}

// VcsDiffTabResult is the result of "vcs.diffTab": what the tab shows.
type VcsDiffTabResult struct {
	Path  string             `json:"path"`
	Mode  gitcli.DiffMode    `json:"mode"`
	State gitcli.ChangeState `json:"state"`
	gitcli.DiffVersions
	Title string `json:"title"`
	// Index is the 0-based position in Order of Total files; Prev and
	// Next are the neighbours (wrapping; equal to Path for one file).
	Index int    `json:"index"`
	Total int    `json:"total"`
	Prev  string `json:"prev"`
	Next  string `json:"next"`
	// Diff is the git.fileDiff result.
	Diff json.RawMessage `json:"diff"`
}

// vcsStatus is the part of git.status the kernel needs.
type vcsStatus struct {
	Branch string `json:"branch"`
	Files  []struct {
		Path     string `json:"path"`
		OrigPath string `json:"origPath"`
		Kind     string `json:"kind"`
		Index    string `json:"index"`
		Worktree string `json:"worktree"`
	} `json:"files"`
}

func (s vcsStatus) fileStatuses() []gitcli.FileStatus {
	out := make([]gitcli.FileStatus, 0, len(s.Files))
	letter := func(v string) byte {
		if v == "" {
			return '.'
		}
		return v[0]
	}
	for _, f := range s.Files {
		out = append(out, gitcli.FileStatus{Kind: gitcli.ParseEntryKind(f.Kind), Path: f.Path, OrigPath: f.OrigPath,
			Index: letter(f.Index), Worktree: letter(f.Worktree)})
	}
	return out
}

func (a *App) registerVcs() {
	mustHandle(a, "vcs.changes", "Change tree of the commit window: tracked changes and unversioned files grouped by directory, module or flat", a.vcsChanges)
	mustHandle(a, "vcs.diffTab", "Resolve and load the diff editor tab of a change (versions, title, previous/next file, git.fileDiff)", a.vcsDiffTab)
}

func (a *App) changeTree(ctx context.Context, groupBy string) (gitcli.ChangeTree, vcsStatus, error) {
	g, err := gitcli.ParseChangeGrouping(groupBy)
	if err != nil {
		return gitcli.ChangeTree{}, vcsStatus{}, Errorf(CodeInvalidParams, "%v", err)
	}
	var st vcsStatus
	if err := a.callValue(ctx, "git.status", json.RawMessage(`{}`), &st); err != nil {
		return gitcli.ChangeTree{}, st, err
	}
	return gitcli.BuildChangeTree(st.fileStatuses(), g, st.Branch), st, nil
}

func (a *App) vcsChanges(ctx context.Context, p VcsChangesParams) (VcsChangesResult, error) {
	tree, st, err := a.changeTree(ctx, p.GroupBy)
	if err != nil {
		return VcsChangesResult{}, err
	}
	order := tree.Order()
	if order == nil {
		order = []string{}
	}
	return VcsChangesResult{Branch: st.Branch, Tree: tree, Order: order}, nil
}

func (a *App) vcsDiffTab(ctx context.Context, p VcsDiffTabParams) (VcsDiffTabResult, error) {
	if p.Path == "" {
		return VcsDiffTabResult{}, Errorf(CodeInvalidParams, "path is required")
	}
	mode, err := gitcli.ParseDiffMode(p.Mode)
	if err != nil {
		return VcsDiffTabResult{}, Errorf(CodeInvalidParams, "%v", err)
	}
	tree, st, err := a.changeTree(ctx, p.GroupBy)
	if err != nil {
		return VcsDiffTabResult{}, err
	}
	order := tree.Order()
	cur := p.Path
	if p.Step != 0 {
		if next, _, ok := gitcli.Neighbor(order, cur, p.Step); ok {
			cur = next
		}
	}
	var status gitcli.FileStatus
	found := false
	for _, f := range st.fileStatuses() {
		if f.Path == cur {
			status, found = f, true
		}
	}
	if !found {
		return VcsDiffTabResult{}, Errorf(CodeNotFound, "%s has no changes", cur)
	}
	v := gitcli.ChangeDiff(status, mode)
	res := VcsDiffTabResult{Path: cur, Mode: mode, State: gitcli.StateOf(status), DiffVersions: v, Title: v.Title(cur), Index: -1, Total: len(order)}
	for i, o := range order {
		if o == cur {
			res.Index = i
		}
	}
	res.Prev, _, _ = gitcli.Neighbor(order, cur, -1)
	res.Next, _, _ = gitcli.Neighbor(order, cur, 1)
	params := map[string]any{"path": cur, "base": v.Base, "target": v.Target}
	if v.BasePath != "" {
		params["basePath"] = v.BasePath
	}
	if p.IgnoreWhitespace {
		params["ignoreWhitespace"] = true
	}
	if err := a.callValue(ctx, "git.fileDiff", params, &res.Diff); err != nil {
		return VcsDiffTabResult{}, err
	}
	return res, nil
}
