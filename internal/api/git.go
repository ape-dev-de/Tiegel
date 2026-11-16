package api

import (
	"context"
	"errors"
	"time"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// TopicGitCommitted is published after "git.commit".
const TopicGitCommitted = "git.committed"

// GitStatusParams are the params of "git.status".
type GitStatusParams struct {
	Ignored     bool     `json:"ignored,omitempty"`
	NoUntracked bool     `json:"noUntracked,omitempty"`
	Paths       []string `json:"paths,omitempty"`
}

// GitFileStatus is one changed file. Index and Worktree are the porcelain v2
// status letters ("." unchanged, M, A, D, R, C, U, "?" untracked, "!" ignored).
type GitFileStatus struct {
	Path     string `json:"path"`
	OrigPath string `json:"origPath,omitempty"`
	Kind     string `json:"kind"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Staged   bool   `json:"staged"`
	Unstaged bool   `json:"unstaged"`
	Conflict bool   `json:"conflict,omitempty"`
}

// GitStatus is the result of "git.status".
type GitStatus struct {
	Root     string          `json:"root"`
	Branch   string          `json:"branch,omitempty"`
	Head     string          `json:"head,omitempty"`
	Detached bool            `json:"detached,omitempty"`
	Initial  bool            `json:"initial,omitempty"`
	Upstream string          `json:"upstream,omitempty"`
	Ahead    int             `json:"ahead,omitempty"`
	Behind   int             `json:"behind,omitempty"`
	State    string          `json:"state,omitempty"`
	Files    []GitFileStatus `json:"files"`
}

// GitDiffParams are the params of "git.diff" (see gitcli.DiffOptions).
type GitDiffParams struct {
	Base    string   `json:"base,omitempty"`
	Target  string   `json:"target,omitempty"`
	Staged  bool     `json:"staged,omitempty"`
	Context int      `json:"context,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	// Raw returns the unified diff text instead of parsed files.
	Raw bool `json:"raw,omitempty"`
}

// GitDiffLine is one line of a hunk; Kind is " ", "+" or "-".
type GitDiffLine struct {
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

// GitHunk is one "@@" section.
type GitHunk struct {
	Header string        `json:"header"`
	Lines  []GitDiffLine `json:"lines"`
}

// GitFileDiff is the diff of one file.
type GitFileDiff struct {
	Path    string    `json:"path"`
	OldPath string    `json:"oldPath,omitempty"`
	New     bool      `json:"new,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
	Renamed bool      `json:"renamed,omitempty"`
	Binary  bool      `json:"binary,omitempty"`
	Added   int       `json:"added"`
	Removed int       `json:"removed"`
	Hunks   []GitHunk `json:"hunks"`
}

// GitDiffResult is the result of "git.diff".
type GitDiffResult struct {
	Files []GitFileDiff `json:"files"`
	Raw   string        `json:"raw,omitempty"`
}

// GitLogParams are the params of "git.log".
type GitLogParams struct {
	Rev    string `json:"rev,omitempty"`
	All    bool   `json:"all,omitempty"`
	Skip   int    `json:"skip,omitempty"`
	Max    int    `json:"max,omitempty"`
	Path   string `json:"path,omitempty"`
	Follow bool   `json:"follow,omitempty"`
}

// GitCommit is one log entry.
type GitCommit struct {
	Hash        string    `json:"hash"`
	Parents     []string  `json:"parents"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Refs        []string  `json:"refs,omitempty"`
}

// GitBranch is a local or remote-tracking branch.
type GitBranch struct {
	Name     string `json:"name"`
	Remote   bool   `json:"remote,omitempty"`
	Current  bool   `json:"current,omitempty"`
	Hash     string `json:"hash"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	Subject  string `json:"subject,omitempty"`
}

// GitCommitParams are the params of "git.commit". Open documents are saved
// first (autosave before commit).
type GitCommitParams struct {
	Message string `json:"message"`
	Amend   bool   `json:"amend,omitempty"`
	// Stage adds these paths to the index before committing.
	Stage      []string `json:"stage,omitempty"`
	AllowEmpty bool     `json:"allowEmpty,omitempty"`
	NoVerify   bool     `json:"noVerify,omitempty"`
	SignOff    bool     `json:"signOff,omitempty"`
}

// GitCommitResult is the result of "git.commit".
type GitCommitResult struct {
	Hash string `json:"hash"`
}

func (a *App) registerGit() {
	mustHandle(a, "git.status", "Branch and changed files (porcelain v2)", a.gitStatus)
	mustHandle(a, "git.diff", "Diff of work tree, index or revisions", a.gitDiff)
	mustHandle(a, "git.log", "Commit history", a.gitLog)
	mustHandle(a, "git.branches", "Local and remote-tracking branches", a.gitBranches)
	mustHandle(a, "git.commit", "Commit (saves open documents first; optionally stages paths)", a.gitCommit)
}

func (a *App) repo(ctx context.Context) (*gitcli.Repo, error) {
	pr, err := a.requireProject()
	if err != nil {
		return nil, err
	}
	r, err := gitcli.Open(ctx, pr.root)
	if err != nil {
		return nil, gitErr(err)
	}
	return r, nil
}

func gitErr(err error) error {
	switch {
	case errors.Is(err, gitcli.ErrGitNotFound):
		return Errorf(CodeUnavailable, "%w", err)
	case errors.Is(err, gitcli.ErrNotRepository), errors.Is(err, gitcli.ErrNotFound):
		return Errorf(CodeNotFound, "%w", err)
	case errors.Is(err, gitcli.ErrEmptyMessage):
		return Errorf(CodeInvalidParams, "%w", err)
	}
	return Errorf(CodeFailed, "git: %w", err)
}

func (a *App) gitStatus(ctx context.Context, p GitStatusParams) (GitStatus, error) {
	r, err := a.repo(ctx)
	if err != nil {
		return GitStatus{}, err
	}
	info, err := r.Info(ctx)
	if err != nil {
		return GitStatus{}, gitErr(err)
	}
	st, err := r.Status(ctx, gitcli.StatusOptions{Ignored: p.Ignored, NoUntracked: p.NoUntracked, Paths: p.Paths})
	if err != nil {
		return GitStatus{}, gitErr(err)
	}
	b := st.Branch
	res := GitStatus{
		Root: info.Root, Branch: b.Head, Head: b.OID, Detached: b.Detached, Initial: b.Initial,
		Upstream: b.Upstream, Ahead: b.Ahead, Behind: b.Behind, State: string(info.State),
		Files: make([]GitFileStatus, 0, len(st.Files)),
	}
	for i := range st.Files {
		f := &st.Files[i]
		res.Files = append(res.Files, GitFileStatus{
			Path: f.Path, OrigPath: f.OrigPath, Kind: f.Kind.String(),
			Index: string(f.Index), Worktree: string(f.Worktree),
			Staged: f.HasStaged(), Unstaged: f.HasUnstaged(), Conflict: f.IsConflict(),
		})
	}
	return res, nil
}

func (a *App) gitDiff(ctx context.Context, p GitDiffParams) (GitDiffResult, error) {
	r, err := a.repo(ctx)
	if err != nil {
		return GitDiffResult{}, err
	}
	opts := gitcli.DiffOptions{Base: p.Base, Target: p.Target, Staged: p.Staged, Context: p.Context, Paths: p.Paths}
	res := GitDiffResult{Files: []GitFileDiff{}}
	if p.Raw {
		raw, err := r.RawDiff(ctx, opts)
		if err != nil {
			return res, gitErr(err)
		}
		res.Raw = string(raw)
		return res, nil
	}
	files, err := r.Diff(ctx, opts)
	if err != nil {
		return res, gitErr(err)
	}
	for i := range files {
		f := &files[i]
		added, removed := f.Stats()
		fd := GitFileDiff{
			Path: f.Path(), New: f.IsNew, Deleted: f.IsDeleted, Renamed: f.IsRename, Binary: f.IsBinary,
			Added: added, Removed: removed, Hunks: []GitHunk{},
		}
		if f.IsRename || f.IsCopy {
			fd.OldPath = f.OldPath
		}
		for j := range f.Hunks {
			h := &f.Hunks[j]
			gh := GitHunk{Header: h.Header(), Lines: make([]GitDiffLine, 0, len(h.Lines))}
			for _, l := range h.Lines {
				gh.Lines = append(gh.Lines, GitDiffLine{Kind: string(rune(l.Kind)), Text: l.Text, OldLine: l.OldLine, NewLine: l.NewLine})
			}
			fd.Hunks = append(fd.Hunks, gh)
		}
		res.Files = append(res.Files, fd)
	}
	return res, nil
}

func (a *App) gitLog(ctx context.Context, p GitLogParams) ([]GitCommit, error) {
	r, err := a.repo(ctx)
	if err != nil {
		return nil, err
	}
	if p.Max == 0 {
		p.Max = 100
	}
	commits, err := r.Log(ctx, gitcli.LogOptions{Rev: p.Rev, All: p.All, Skip: p.Skip, Max: p.Max, Path: p.Path, Follow: p.Follow})
	if err != nil {
		return nil, gitErr(err)
	}
	out := make([]GitCommit, 0, len(commits))
	for _, c := range commits {
		parents := c.Parents
		if parents == nil {
			parents = []string{}
		}
		out = append(out, GitCommit{Hash: c.Hash, Parents: parents, Author: c.AuthorName, AuthorEmail: c.AuthorEmail, Date: c.AuthorDate, Subject: c.Subject, Refs: c.Refs})
	}
	return out, nil
}

func (a *App) gitBranches(ctx context.Context, _ struct{}) ([]GitBranch, error) {
	r, err := a.repo(ctx)
	if err != nil {
		return nil, err
	}
	branches, err := r.Branches(ctx)
	if err != nil {
		return nil, gitErr(err)
	}
	out := make([]GitBranch, 0, len(branches))
	for _, b := range branches {
		out = append(out, GitBranch{Name: b.Name, Remote: b.Remote, Current: b.Current, Hash: b.Hash, Upstream: b.Upstream, Ahead: b.Ahead, Behind: b.Behind, Subject: b.Subject})
	}
	return out, nil
}

func (a *App) gitCommit(ctx context.Context, p GitCommitParams) (GitCommitResult, error) {
	r, err := a.repo(ctx)
	if err != nil {
		return GitCommitResult{}, err
	}
	if err := a.saver.BeforeCommit(ctx); err != nil {
		return GitCommitResult{}, Errorf(CodeFailed, "saving documents before commit: %w", err)
	}
	if len(p.Stage) > 0 {
		if err := r.Stage(ctx, p.Stage...); err != nil {
			return GitCommitResult{}, gitErr(err)
		}
	}
	hash, err := r.Commit(ctx, gitcli.CommitOptions{Message: p.Message, Amend: p.Amend, AllowEmpty: p.AllowEmpty, NoVerify: p.NoVerify, SignOff: p.SignOff})
	if err != nil {
		return GitCommitResult{}, gitErr(err)
	}
	res := GitCommitResult{Hash: hash}
	a.bus.Publish(TopicGitCommitted, res)
	return res, nil
}
