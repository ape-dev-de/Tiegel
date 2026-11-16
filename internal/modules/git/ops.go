package git

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func (m *Module) registerOps(p *api.Provider) {
	api.Add(p, "git.stage", "Stage whole paths or selected hunks/lines of one file", m.gitStage)
	api.Add(p, "git.unstage", "Unstage whole paths or selected hunks/lines of one file", m.gitUnstage)
	api.Add(p, "git.discard", "Discard selected hunks/lines (or all changes) of a file in the work tree", m.gitDiscard)
	api.Add(p, "git.commitSelection", "Commit selected files and hunks (amend, push; commit.before, commit.message)", m.gitCommitSelection)
	api.Add(p, "git.push", "Push a branch (error data carries the failure kind)", m.gitPush)
	api.Add(p, "git.recentMessages", "Recent commit messages, newest first", m.gitRecentMessages)
	api.Add(p, "git.gutter", "Changed line ranges of a document (buffer content) against HEAD or the index", m.gitGutter)
	api.Add(p, "git.rollback", "Restore the base version of one changed range in an open document (undoable edit)", m.gitRollback)
	api.Add(p, "git.show", "Content of a file at a revision", m.gitShow)
	api.Add(p, "git.checkout", "Switch to a branch or revision", m.gitCheckout)
	api.Add(p, "git.createBranch", "Create a branch (optionally check it out)", m.gitCreateBranch)
	api.Add(p, "git.deleteBranch", "Delete a local branch", m.gitDeleteBranch)
	api.Add(p, "git.conflicts", "Files with merge conflicts", m.gitConflicts)
	api.Add(p, "git.mergeData", "Base/ours/theirs of a conflicted file with three-way merge blocks", m.gitMergeData)
	api.Add(p, "git.resolve", "Resolve a conflicted file with content or per-block resolutions", m.gitResolve)
}

// changed publishes git.changed.
func (m *Module) changed(op string, paths ...string) {
	m.app.Bus().Publish(api.TopicGitChanged, api.GitChangedEvent{Op: op, Paths: paths})
}

// rel converts an API path (project relative or absolute) to a repository
// relative slash path.
func (m *Module) rel(r *gitcli.Repo, p string) (string, error) {
	abs, err := m.app.ResolvePath(p)
	if err != nil {
		return "", err
	}
	rel, err := r.RelPath(abs)
	if err != nil {
		return "", api.Errorf(api.CodeInvalidParams, "%s: %w", p, err)
	}
	return rel, nil
}

func (m *Module) rels(r *gitcli.Repo, ps []string) ([]string, error) {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		rp, err := m.rel(r, p)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, nil
}

// HunkSel selects one hunk of a file diff (index into the hunks of the
// corresponding git.diff result) and optionally single lines of it (indexes
// into the hunk's lines; context lines are always kept).
//
// Header and Hash are optional (backwards compatible) but recommended:
// when given they must match the hunk's header and hash in the current
// diff, otherwise the operation fails with CodeConflict instead of acting
// on a different change (e.g. after a format-on-save shifted the hunks).
type HunkSel struct {
	Hunk   int    `json:"hunk"`
	Lines  []int  `json:"lines,omitempty"`
	Header string `json:"header,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

// ReasonStaleSelection is the Reason of StaleSelection.
const ReasonStaleSelection = "staleSelection"

// StaleSelection is the error data of a CodeConflict caused by a hunk
// selection (Header/Hash) that no longer matches the current diff: the
// client should reload the diff and let the user select again.
type StaleSelection struct {
	Reason string `json:"reason"`
	Path   string `json:"path"`
	Hunk   int    `json:"hunk"`
}

// checkSel verifies the optional Header/Hash of each selection against the
// current diff.
func checkSel(f *gitcli.FileDiff, hs []HunkSel) error {
	for _, h := range hs {
		if h.Header == "" && h.Hash == "" {
			continue
		}
		data := StaleSelection{Reason: ReasonStaleSelection, Path: f.Path(), Hunk: h.Hunk}
		if h.Hunk < 0 || h.Hunk >= len(f.Hunks) {
			return api.Errorf(api.CodeConflict, "%s: hunk %d no longer exists; refresh the diff", f.Path(), h.Hunk).WithData(data)
		}
		cur := &f.Hunks[h.Hunk]
		if h.Header != "" && h.Header != cur.Header() {
			return api.Errorf(api.CodeConflict, "%s: hunk %d changed (was %q, now %q); refresh the diff", f.Path(), h.Hunk, h.Header, cur.Header()).WithData(data)
		}
		if h.Hash != "" && h.Hash != cur.Hash() {
			return api.Errorf(api.CodeConflict, "%s: hunk %d changed (hash mismatch); refresh the diff", f.Path(), h.Hunk).WithData(data)
		}
	}
	return nil
}

func toSel(hs []HunkSel) []gitcli.HunkSelection {
	out := make([]gitcli.HunkSelection, 0, len(hs))
	for _, h := range hs {
		out = append(out, gitcli.HunkSelection{Hunk: h.Hunk, Lines: h.Lines})
	}
	return out
}

// StageParams are the params of "git.stage" and "git.unstage": either
// whole Paths, or one Path with a Hunks selection. Hunk indexes refer to
// git.diff of the same file (stage: work tree against index, i.e. no base;
// unstage: staged:true).
type StageParams struct {
	Paths []string  `json:"paths,omitempty"`
	Path  string    `json:"path,omitempty"`
	Hunks []HunkSel `json:"hunks,omitempty"`
}

// fileDiff returns the diff of one path; untracked files (stage) are
// returned as creation.
func (m *Module) fileDiff(ctx context.Context, r *gitcli.Repo, rel string, opts gitcli.DiffOptions, untracked bool) (*gitcli.FileDiff, error) {
	opts.Paths = []string{rel}
	files, err := r.Diff(ctx, opts)
	if err != nil {
		return nil, gitErr(err)
	}
	if len(files) > 0 {
		return &files[0], nil
	}
	if untracked {
		f, err := r.DiffUntracked(ctx, rel)
		if err == nil && f != nil {
			return f, nil
		}
	}
	return nil, api.Errorf(api.CodeNotFound, "no changes in %s", rel)
}

func (m *Module) stageOp(ctx context.Context, p StageParams, unstage bool) (GitStatus, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return GitStatus{}, err
	}
	op := "stage"
	if unstage {
		op = "unstage"
	}
	var touched []string
	switch {
	case len(p.Hunks) > 0:
		if p.Path == "" {
			return GitStatus{}, api.Errorf(api.CodeInvalidParams, "hunks need path")
		}
		rel, err := m.rel(r, p.Path)
		if err != nil {
			return GitStatus{}, err
		}
		f, err := m.fileDiff(ctx, r, rel, gitcli.DiffOptions{Staged: unstage}, !unstage)
		if err != nil {
			return GitStatus{}, err
		}
		if err := checkSel(f, p.Hunks); err != nil {
			return GitStatus{}, err
		}
		if unstage {
			err = r.UnstageSelection(ctx, f, toSel(p.Hunks))
		} else {
			err = r.StageSelection(ctx, f, toSel(p.Hunks))
		}
		if err != nil {
			return GitStatus{}, gitErr(err)
		}
		touched = []string{rel}
	default:
		paths := p.Paths
		if p.Path != "" {
			paths = append(paths, p.Path)
		}
		if len(paths) == 0 {
			return GitStatus{}, api.Errorf(api.CodeInvalidParams, "paths or path is required")
		}
		if touched, err = m.rels(r, paths); err != nil {
			return GitStatus{}, err
		}
		if unstage {
			err = r.Unstage(ctx, touched...)
		} else {
			err = r.Stage(ctx, touched...)
		}
		if err != nil {
			return GitStatus{}, gitErr(err)
		}
	}
	m.changed(op, touched...)
	return m.gitStatus(ctx, GitStatusParams{})
}

func (m *Module) gitStage(ctx context.Context, p StageParams) (GitStatus, error) {
	return m.stageOp(ctx, p, false)
}

func (m *Module) gitUnstage(ctx context.Context, p StageParams) (GitStatus, error) {
	return m.stageOp(ctx, p, true)
}

// DiscardParams are the params of "git.discard". Hunks refer to git.diff of
// the file without base (work tree against index). All discards every
// unstaged change of the file instead (it must be given explicitly).
type DiscardParams struct {
	Path  string    `json:"path"`
	Hunks []HunkSel `json:"hunks,omitempty"`
	All   bool      `json:"all,omitempty"`
}

// DiscardResult is the result of "git.discard".
type DiscardResult struct {
	Path string `json:"path"`
	// Reloaded is true if the open document was updated from disk (one
	// undo step). An open document with unsaved changes is left alone.
	Reloaded bool `json:"reloaded"`
}

func (m *Module) gitDiscard(ctx context.Context, p DiscardParams) (DiscardResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return DiscardResult{}, err
	}
	if p.Path == "" || (len(p.Hunks) == 0 && !p.All) {
		return DiscardResult{}, api.Errorf(api.CodeInvalidParams, "path and hunks (or all:true) are required")
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return DiscardResult{}, err
	}
	f, err := m.fileDiff(ctx, r, rel, gitcli.DiffOptions{}, false)
	if err != nil {
		return DiscardResult{}, err
	}
	if err := checkSel(f, p.Hunks); err != nil {
		return DiscardResult{}, err
	}
	sel := toSel(p.Hunks)
	if p.All {
		sel = f.SelectAll()
	}
	// Write and reload under the save lock, so a save in flight cannot
	// overwrite the result with an older snapshot.
	var reloaded bool
	if err := m.app.Exclusive(func() error {
		if err := r.DiscardSelection(ctx, f, sel); err != nil {
			return gitErr(err)
		}
		reloaded = m.reload(p.Path)
		return nil
	}); err != nil {
		return DiscardResult{}, err
	}
	m.changed("discard", rel)
	return DiscardResult{Path: rel, Reloaded: reloaded}, nil
}

// reload replaces the content of an open, unmodified document with the
// file on disk as one undo step and marks it saved. It reports whether the
// document was updated. Callers hold the save lock (api.App.Exclusive)
// together with the write that changed the file.
func (m *Module) reload(path string) bool {
	d, err := m.app.Document(path)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(d.Path())
	if err != nil {
		return false
	}
	d.Lock()
	defer d.Unlock()
	b := d.Buffer()
	if b.Modified() {
		return false
	}
	if string(b.Bytes()) != string(data) {
		b.BreakUndo()
		if _, err := b.Replace(0, b.Len(), data); err != nil {
			return false
		}
		b.BreakUndo()
	}
	b.MarkSaved()
	return true
}

// FileHunks selects hunks of one file for git.commitSelection. Hunk
// indexes refer to git.diff with base "HEAD" (work tree against HEAD).
type FileHunks struct {
	Path  string    `json:"path"`
	Hunks []HunkSel `json:"hunks"`
}

// CommitSelectionParams are the params of "git.commitSelection": the commit
// contains HEAD plus the whole Paths plus the selected Hunks, independent of
// the index.
type CommitSelectionParams struct {
	Message  string      `json:"message"`
	Paths    []string    `json:"paths,omitempty"`
	Hunks    []FileHunks `json:"hunks,omitempty"`
	Amend    bool        `json:"amend,omitempty"`
	Push     bool        `json:"push,omitempty"`
	NoVerify bool        `json:"noVerify,omitempty"`
	SignOff  bool        `json:"signOff,omitempty"`
	// AllowEmpty permits a commit without changes (e.g. amend message only).
	AllowEmpty bool `json:"allowEmpty,omitempty"`
}

// PushResult describes a successful push.
type PushResult struct {
	Remote       string `json:"remote"`
	Branch       string `json:"branch"`
	RemoteBranch string `json:"remoteBranch"`
	SetUpstream  bool   `json:"setUpstream,omitempty"`
	Output       string `json:"output,omitempty"`
}

// PushErrorInfo is the error data of a failed push.
type PushErrorInfo struct {
	// Kind is rejected, remoteRejected, auth, network, repoNotFound,
	// noRemote or failed.
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Stderr  string `json:"stderr,omitempty"`
}

// CommitSelectionResult is the result of "git.commitSelection". A failed
// push does not fail the method: the commit exists, PushError explains.
type CommitSelectionResult struct {
	Hash      string         `json:"hash"`
	Message   string         `json:"message"`
	Pushed    bool           `json:"pushed"`
	Push      *PushResult    `json:"push,omitempty"`
	PushError *PushErrorInfo `json:"pushError,omitempty"`
}

func (m *Module) gitCommitSelection(ctx context.Context, p CommitSelectionParams) (CommitSelectionResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return CommitSelectionResult{}, err
	}
	if len(p.Paths) == 0 && len(p.Hunks) == 0 && !p.Amend && !p.AllowEmpty {
		return CommitSelectionResult{}, api.Errorf(api.CodeInvalidParams, "nothing selected (paths or hunks)")
	}
	// Refuse early (before the save guard runs): a selection commit during
	// a merge/rebase/cherry-pick/revert would drop the operation's changes.
	info, err := r.Info(ctx)
	if err != nil {
		return CommitSelectionResult{}, gitErr(err)
	}
	if info.State != gitcli.StateClean && info.State != gitcli.StateBisecting {
		return CommitSelectionResult{}, api.Errorf(api.CodeConflict,
			"git.commitSelection is not possible while %s is in progress; finish or abort it first (git.commit commits the whole index)", info.State)
	}
	all := append([]string(nil), p.Paths...)
	for _, fh := range p.Hunks {
		all = append(all, fh.Path)
	}
	// The guard saves dirty documents (autosave), so diffs are computed
	// afterwards.
	if err := api.HookCommitBefore.Check(ctx, m.app.Hooks(), api.CommitRequest{Message: p.Message, Paths: all, Amend: p.Amend, Push: p.Push}); err != nil {
		return CommitSelectionResult{}, api.Errorf(api.CodeFailed, "commit.before: %w", err)
	}
	if msg, err := api.HookCommitMessage.Apply(ctx, m.app.Hooks(), p.Message); err == nil {
		p.Message = msg
	}
	var sel gitcli.CommitSelection
	if sel.Paths, err = m.rels(r, p.Paths); err != nil {
		return CommitSelectionResult{}, err
	}
	touched := append([]string(nil), sel.Paths...)
	for _, fh := range p.Hunks {
		rel, err := m.rel(r, fh.Path)
		if err != nil {
			return CommitSelectionResult{}, err
		}
		f, err := m.fileDiff(ctx, r, rel, gitcli.DiffOptions{Base: "HEAD"}, true)
		if err != nil {
			return CommitSelectionResult{}, err
		}
		if err := checkSel(f, fh.Hunks); err != nil {
			return CommitSelectionResult{}, err
		}
		patch, err := f.Patch(toSel(fh.Hunks))
		if err != nil {
			return CommitSelectionResult{}, api.Errorf(api.CodeInvalidParams, "%s: %w", rel, err)
		}
		sel.Patches = append(sel.Patches, patch)
		touched = append(touched, rel)
	}
	hash, err := r.CommitSelection(ctx, gitcli.CommitOptions{
		Message: p.Message, Amend: p.Amend, NoVerify: p.NoVerify, SignOff: p.SignOff, AllowEmpty: p.AllowEmpty,
	}, sel)
	if err != nil {
		return CommitSelectionResult{}, gitErr(err)
	}
	res := CommitSelectionResult{Hash: hash, Message: p.Message}
	m.app.Bus().Publish(TopicGitCommitted, GitCommitResult{Hash: hash})
	m.changed("commit", touched...)
	if p.Push {
		pr, err := r.Push(ctx, gitcli.PushOptions{})
		if err != nil {
			info := pushErrorInfo(err)
			res.PushError = &info
		} else {
			res.Pushed, res.Push = true, toPushResult(pr)
			m.changed("push")
		}
	}
	return res, nil
}

func toPushResult(pr *gitcli.PushResult) *PushResult {
	return &PushResult{Remote: pr.Remote, Branch: pr.Branch, RemoteBranch: pr.RemoteBranch, SetUpstream: pr.SetUpstream, Output: pr.Output}
}

// PushKind returns the API name of a push failure kind.
func PushKind(k gitcli.PushErrorKind) string {
	switch k {
	case gitcli.PushRejected:
		return "rejected"
	case gitcli.PushRemoteRejected:
		return "remoteRejected"
	case gitcli.PushAuthFailed:
		return "auth"
	case gitcli.PushNetwork:
		return "network"
	case gitcli.PushRepoNotFound:
		return "repoNotFound"
	}
	return "failed"
}

func pushErrorInfo(err error) PushErrorInfo {
	var pe *gitcli.PushError
	switch {
	case errors.As(err, &pe):
		return PushErrorInfo{Kind: PushKind(pe.Kind), Message: pe.Message, Stderr: pe.Stderr}
	case errors.Is(err, gitcli.ErrNoRemote):
		return PushErrorInfo{Kind: "noRemote", Message: err.Error()}
	}
	return PushErrorInfo{Kind: "failed", Message: err.Error()}
}

// PushParams are the params of "git.push" (see gitcli.PushOptions).
type PushParams struct {
	Remote         string `json:"remote,omitempty"`
	Branch         string `json:"branch,omitempty"`
	RemoteBranch   string `json:"remoteBranch,omitempty"`
	SetUpstream    bool   `json:"setUpstream,omitempty"`
	ForceWithLease bool   `json:"forceWithLease,omitempty"`
	Tags           bool   `json:"tags,omitempty"`
}

func (m *Module) gitPush(ctx context.Context, p PushParams) (PushResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return PushResult{}, err
	}
	pr, err := r.Push(ctx, gitcli.PushOptions{
		Remote: p.Remote, Branch: p.Branch, RemoteBranch: p.RemoteBranch,
		SetUpstream: p.SetUpstream, ForceWithLease: p.ForceWithLease, Tags: p.Tags,
	})
	if err != nil {
		info := pushErrorInfo(err)
		return PushResult{}, api.Errorf(api.CodeFailed, "push failed (%s): %s", info.Kind, info.Message).WithData(info)
	}
	m.changed("push")
	return *toPushResult(pr), nil
}

// RecentMessagesParams are the params of "git.recentMessages".
type RecentMessagesParams struct {
	// Max is the number of messages (default 20).
	Max int `json:"max,omitempty"`
}

func (m *Module) gitRecentMessages(ctx context.Context, p RecentMessagesParams) ([]string, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, err
	}
	if p.Max <= 0 {
		p.Max = 20
	}
	msgs, err := r.RecentMessages(ctx, p.Max)
	if err != nil {
		return nil, gitErr(err)
	}
	if msgs == nil {
		msgs = []string{}
	}
	return msgs, nil
}

// GutterParams are the params of "git.gutter" and "git.rollback".
type GutterParams struct {
	Path string `json:"path"`
	// Base is "head" (default) or "index".
	Base string `json:"base,omitempty"`
}

// GutterRange is a changed region. Start and End are 1-based buffer lines,
// End exclusive ([Start, End)); a deletion has Start == End, the line
// before which base lines were removed. BaseStart/BaseEnd are the same for
// the base version.
type GutterRange struct {
	Kind      string `json:"kind"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	BaseStart int    `json:"baseStart"`
	BaseEnd   int    `json:"baseEnd"`
}

// GutterResult is the result of "git.gutter".
type GutterResult struct {
	Path string `json:"path"`
	// Open is true if the content came from the open document's buffer.
	Open   bool          `json:"open"`
	Ranges []GutterRange `json:"ranges"`
}

func gutterBase(s string) (gitcli.GutterBase, error) {
	switch s {
	case "", "head", "HEAD":
		return gitcli.BaseHEAD, nil
	case "index":
		return gitcli.BaseIndex, nil
	}
	return 0, api.Errorf(api.CodeInvalidParams, "base must be head or index, got %q", s)
}

func (m *Module) gitGutter(ctx context.Context, p GutterParams) (GutterResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return GutterResult{}, err
	}
	base, err := gutterBase(p.Base)
	if err != nil {
		return GutterResult{}, err
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return GutterResult{}, err
	}
	text, open, err := m.app.ReadText(p.Path)
	if err != nil {
		return GutterResult{}, err
	}
	ranges, err := r.GutterChanges(ctx, rel, text, base)
	if err != nil {
		return GutterResult{}, gitErr(err)
	}
	res := GutterResult{Path: rel, Open: open, Ranges: []GutterRange{}}
	for _, lr := range ranges {
		res.Ranges = append(res.Ranges, GutterRange{Kind: lr.Kind.String(), Start: lr.Start + 1, End: lr.End + 1, BaseStart: lr.BaseStart + 1, BaseEnd: lr.BaseEnd + 1})
	}
	return res, nil
}

// RollbackParams are the params of "git.rollback".
type RollbackParams struct {
	Path string `json:"path"`
	// Line is a 1-based line inside the changed range (for a deletion the
	// line reported as its start).
	Line int    `json:"line"`
	Base string `json:"base,omitempty"`
}

func (m *Module) gitRollback(ctx context.Context, p RollbackParams) (api.DocState, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return api.DocState{}, err
	}
	base, err := gutterBase(p.Base)
	if err != nil {
		return api.DocState{}, err
	}
	d, err := m.app.Document(p.Path)
	if err != nil {
		return api.DocState{}, err
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return api.DocState{}, err
	}
	snap := d.Snapshot()
	baseText, ok, err := r.BaseContent(ctx, rel, base)
	if err != nil {
		return api.DocState{}, gitErr(err)
	}
	if !ok {
		baseText = nil
	}
	line := p.Line - 1
	for _, lr := range gitcli.LineChanges(baseText, snap.Text) {
		if (line >= lr.Start && line < lr.End) || (lr.Start == lr.End && line == lr.Start) {
			rep, err := gitcli.Rollback(baseText, snap.Text, lr)
			if err != nil {
				return api.DocState{}, api.Errorf(api.CodeFailed, "rollback: %w", err)
			}
			return m.app.EditDocument(p.Path, snap.Version, rep.StartOffset, rep.EndOffset, string(rep.Text))
		}
	}
	return api.DocState{}, api.Errorf(api.CodeNotFound, "no change at line %d of %s", p.Line, p.Path)
}

// ShowParams are the params of "git.show".
type ShowParams struct {
	Rev  string `json:"rev"`
	Path string `json:"path"`
}

// ShowResult is the result of "git.show".
type ShowResult struct {
	Rev  string `json:"rev"`
	Path string `json:"path"`
	// Encoding is "utf-8" or, for binary content, "base64".
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

func (m *Module) gitShow(ctx context.Context, p ShowParams) (ShowResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return ShowResult{}, err
	}
	if p.Rev == "" {
		p.Rev = "HEAD"
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return ShowResult{}, err
	}
	data, err := r.ShowFile(ctx, p.Rev, rel)
	if err != nil {
		return ShowResult{}, gitErr(err)
	}
	res := ShowResult{Rev: p.Rev, Path: rel, Encoding: "utf-8", Content: string(data)}
	if !utf8.Valid(data) {
		res.Encoding, res.Content = "base64", base64.StdEncoding.EncodeToString(data)
	}
	return res, nil
}

// CheckoutParams are the params of "git.checkout".
type CheckoutParams struct {
	Ref string `json:"ref"`
}

// BranchParams are the params of "git.createBranch" and "git.deleteBranch".
type BranchParams struct {
	Name string `json:"name"`
	// Start is the start revision of a new branch (default HEAD).
	Start    string `json:"start,omitempty"`
	Checkout bool   `json:"checkout,omitempty"`
	// Force deletes an unmerged branch.
	Force bool `json:"force,omitempty"`
}

func (m *Module) gitCheckout(ctx context.Context, p CheckoutParams) (GitStatus, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return GitStatus{}, err
	}
	if p.Ref == "" {
		return GitStatus{}, api.Errorf(api.CodeInvalidParams, "ref is required")
	}
	if err := m.app.Exclusive(func() error {
		if err := r.Checkout(ctx, p.Ref); err != nil {
			return gitErr(err)
		}
		m.reloadAll()
		return nil
	}); err != nil {
		return GitStatus{}, err
	}
	m.changed("checkout")
	return m.gitStatus(ctx, GitStatusParams{})
}

// reloadAll reloads every open, unmodified document from disk (after a
// checkout changed the work tree).
func (m *Module) reloadAll() {
	for _, d := range m.app.Documents() {
		if !d.Modified {
			m.reload(d.File)
		}
	}
}

func (m *Module) gitCreateBranch(ctx context.Context, p BranchParams) ([]GitBranch, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, api.Errorf(api.CodeInvalidParams, "name is required")
	}
	if err := m.app.Exclusive(func() error {
		if err := r.CreateBranch(ctx, p.Name, p.Start, p.Checkout); err != nil {
			return gitErr(err)
		}
		if p.Checkout {
			m.reloadAll()
		}
		return nil
	}); err != nil {
		return nil, err
	}
	m.changed("createBranch")
	return m.gitBranches(ctx, struct{}{})
}

func (m *Module) gitDeleteBranch(ctx context.Context, p BranchParams) ([]GitBranch, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, api.Errorf(api.CodeInvalidParams, "name is required")
	}
	if err := r.DeleteBranch(ctx, p.Name, p.Force); err != nil {
		return nil, gitErr(err)
	}
	m.changed("deleteBranch")
	return m.gitBranches(ctx, struct{}{})
}

func (m *Module) gitConflicts(ctx context.Context, _ struct{}) ([]GitFileStatus, error) {
	st, err := m.gitStatus(ctx, GitStatusParams{NoUntracked: true})
	if err != nil {
		return nil, err
	}
	out := []GitFileStatus{}
	for _, f := range st.Files {
		if f.Conflict {
			out = append(out, f)
		}
	}
	return out, nil
}

// MergeBlock is one region of the three-way merge. Texts keep their line
// terminators; the Start fields are 1-based lines in each version.
type MergeBlock struct {
	Index       int    `json:"index"`
	Kind        string `json:"kind"`
	Conflict    bool   `json:"conflict"`
	Base        string `json:"base"`
	Ours        string `json:"ours"`
	Theirs      string `json:"theirs"`
	BaseStart   int    `json:"baseStart"`
	OursStart   int    `json:"oursStart"`
	TheirsStart int    `json:"theirsStart"`
}

// MergeData is the result of "git.mergeData".
type MergeData struct {
	Path      string       `json:"path"`
	Base      string       `json:"base"`
	Ours      string       `json:"ours"`
	Theirs    string       `json:"theirs"`
	HasBase   bool         `json:"hasBase"`
	HasOurs   bool         `json:"hasOurs"`
	HasTheirs bool         `json:"hasTheirs"`
	Blocks    []MergeBlock `json:"blocks"`
	// Merged is the automatic result with diff3 markers for conflicts.
	Merged    string `json:"merged"`
	Conflicts int    `json:"conflicts"`
}

var markerLabels = gitcli.MarkerLabels{Ours: "ours", Base: "base", Theirs: "theirs"}

func (m *Module) merge(ctx context.Context, path string) (*gitcli.Repo, string, *gitcli.ConflictVersions, *gitcli.MergeResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return nil, "", nil, nil, err
	}
	rel, err := m.rel(r, path)
	if err != nil {
		return nil, "", nil, nil, err
	}
	cv, err := r.ConflictVersions(ctx, rel)
	if err != nil {
		return nil, "", nil, nil, gitErr(err)
	}
	return r, rel, cv, gitcli.Merge3(cv.Base, cv.Ours, cv.Theirs), nil
}

func (m *Module) gitMergeData(ctx context.Context, p api.PathParams) (MergeData, error) {
	_, rel, cv, mr, err := m.merge(ctx, p.Path)
	if err != nil {
		return MergeData{}, err
	}
	res := MergeData{
		Path: rel, Base: string(cv.Base), Ours: string(cv.Ours), Theirs: string(cv.Theirs),
		HasBase: cv.HasBase, HasOurs: cv.HasOurs, HasTheirs: cv.HasTheirs,
		Blocks: make([]MergeBlock, 0, len(mr.Blocks)), Merged: string(mr.Render(markerLabels)), Conflicts: mr.Conflicts(),
	}
	for i, b := range mr.Blocks {
		res.Blocks = append(res.Blocks, MergeBlock{
			Index: i, Kind: b.Kind.String(), Conflict: b.Kind == gitcli.MergeConflict,
			Base: strings.Join(b.Base, ""), Ours: strings.Join(b.Ours, ""), Theirs: strings.Join(b.Theirs, ""),
			BaseStart: b.BaseStart + 1, OursStart: b.OursStart + 1, TheirsStart: b.TheirsStart + 1,
		})
	}
	return res, nil
}

// Resolution resolves one conflict block of git.mergeData.
type Resolution struct {
	Block int `json:"block"`
	// Resolution is ours, theirs, oursFirst, theirsFirst, base or custom.
	Resolution string `json:"resolution"`
	// Custom is the text for "custom".
	Custom string `json:"custom,omitempty"`
}

// ResolveParams are the params of "git.resolve": either the final Content
// or Resolutions for every conflict block.
type ResolveParams struct {
	Path        string       `json:"path"`
	Content     *string      `json:"content,omitempty"`
	Resolutions []Resolution `json:"resolutions,omitempty"`
}

// ResolveResult is the result of "git.resolve".
type ResolveResult struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Reloaded bool   `json:"reloaded"`
}

var resolutions = map[string]gitcli.Resolution{
	"ours": gitcli.TakeOurs, "theirs": gitcli.TakeTheirs, "oursFirst": gitcli.TakeOursFirst,
	"theirsFirst": gitcli.TakeTheirsFirst, "base": gitcli.TakeBase, "custom": gitcli.TakeCustom,
}

func (m *Module) gitResolve(ctx context.Context, p ResolveParams) (ResolveResult, error) {
	r, rel, _, mr, err := m.merge(ctx, p.Path)
	if err != nil {
		return ResolveResult{}, err
	}
	var content []byte
	if p.Content != nil {
		content = []byte(*p.Content)
	} else {
		for _, res := range p.Resolutions {
			if res.Block < 0 || res.Block >= len(mr.Blocks) {
				return ResolveResult{}, api.Errorf(api.CodeInvalidParams, "block %d out of range (%d blocks)", res.Block, len(mr.Blocks))
			}
			kind, ok := resolutions[res.Resolution]
			if !ok {
				return ResolveResult{}, api.Errorf(api.CodeInvalidParams, "unknown resolution %q", res.Resolution)
			}
			mr.Blocks[res.Block].Resolution = kind
			mr.Blocks[res.Block].Custom = []byte(res.Custom)
		}
		if n := mr.Unresolved(); n > 0 {
			return ResolveResult{}, api.Errorf(api.CodeInvalidParams, "%d conflict block(s) unresolved", n)
		}
		content = mr.Render(markerLabels)
	}
	var reloaded bool
	if err := m.app.Exclusive(func() error {
		if err := r.ResolveConflict(ctx, rel, content); err != nil {
			return gitErr(err)
		}
		reloaded = m.reload(p.Path)
		return nil
	}); err != nil {
		return ResolveResult{}, err
	}
	m.changed("resolve", rel)
	return ResolveResult{Path: rel, Content: string(content), Reloaded: reloaded}, nil
}
