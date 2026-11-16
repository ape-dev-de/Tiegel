package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// The git part of the controller is a client of the git module's API
// (git.*): it loads status, diffs, log, branches, gutter ranges and merge
// data and turns user intents (commit selection, push, rollback, checkout,
// resolve) into API calls. Diffs are requested as raw unified text and
// parsed with gitcli.ParseDiff, so hunk indexes are exactly the ones the
// module uses for git.commitSelection. gitcli is used for its data types
// only; no git command runs in the GUI.

// GutterDelay debounces gutter updates after document edits.
var GutterDelay = 250 * time.Millisecond

// LogPage is the number of commits loaded per log page.
const LogPage = 100

// emptyTree is git's empty tree object (base of a root commit's diff).
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// GitInfo is the repository state shown in the status bar.
type GitInfo struct {
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Detached bool   `json:"detached"`
	Upstream string `json:"upstream"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	State    string `json:"state"`
}

// GitChange is one entry of the commit window: status and, when known,
// the diff of the work tree against HEAD.
type GitChange struct {
	Status gitcli.FileStatus
	Diff   *gitcli.FileDiff
}

// GitFileSelection is the part of a file chosen for a commit (mirrors the
// commit panel's selection without UI types).
type GitFileSelection struct {
	Path, OrigPath string
	Untracked      bool
	Whole          bool
	Hunks          []gitcli.HunkSelection
}

// GitCommitRequest is a commit from the commit window.
type GitCommitRequest struct {
	Message string
	Amend   bool
	Push    bool
	Files   []GitFileSelection
}

// GitProblem describes a failed commit or push for the commit window.
type GitProblem struct {
	// Err is the error of a failed commit (or other operation).
	Err error
	// Push is set when pushing failed; Committed tells whether the commit
	// before it succeeded.
	Push      bool
	Committed bool
	PushKind  gitcli.PushErrorKind
	Message   string
	Stderr    string
}

// GutterRange is a changed line range of a document (git.gutter): Start
// and End are 1-based, End exclusive; a deletion has Start == End.
type GutterRange struct {
	Kind      string `json:"kind"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	BaseStart int    `json:"baseStart"`
	BaseEnd   int    `json:"baseEnd"`
}

// Contains reports whether the 1-based line belongs to r (a deletion
// belongs to the line it is reported at).
func (r GutterRange) Contains(line int) bool {
	if r.Start == r.End {
		return line == r.Start
	}
	return line >= r.Start && line < r.End
}

// GitBlock is the change under a gutter click: the range and its diff.
type GitBlock struct {
	File  string
	Range GutterRange
	Diff  *gitcli.FileDiff
}

// GitMerge is the three-way merge of a conflicted file.
type GitMerge struct {
	// Path is project relative.
	Path   string
	Result *gitcli.MergeResult
}

// Git is the git state of the window. Fields belong to the UI goroutine;
// the *Version counters change whenever the corresponding data changes.
type Git struct {
	c *Controller

	// Available is true while the git module runs and the project is a
	// repository.
	Available bool
	Info      GitInfo
	// Conflicts lists the project-relative paths with merge conflicts.
	Conflicts []string

	Changes        []GitChange
	ChangesVersion uint64
	// Diffs are diffs loaded later (untracked files), keyed by path.
	Diffs        map[string]*gitcli.FileDiff
	DiffsVersion uint64

	History      []string
	AmendMessage string
	AmendVersion uint64
	Busy         bool
	Problem      *GitProblem
	// ProblemVersion changes with Problem.
	ProblemVersion uint64
	// Committed counts successful commits (the window resets its form).
	Committed  uint64
	LastCommit string

	Log        []gitcli.Commit
	LogMore    bool
	LogVersion uint64 // changes when Log is replaced (appends keep it)
	logLoading bool

	Branches        []gitcli.Branch
	BranchesVersion uint64

	SelectedCommit string
	CommitFiles    []gitcli.FileDiff
	CommitVersion  uint64

	Merges       map[string]*GitMerge
	MergeVersion uint64

	Block        *GitBlock
	BlockVersion uint64

	gutters       map[string]*gutterState
	GutterVersion uint64
}

type gutterState struct {
	ranges  []GutterRange
	loading bool
	again   bool
	timer   *time.Timer
}

func newGit(c *Controller) *Git {
	return &Git{c: c, Diffs: map[string]*gitcli.FileDiff{}, Merges: map[string]*GitMerge{}, gutters: map[string]*gutterState{}}
}

// fullStatus is the result of git.status.
type fullStatus struct {
	GitInfo
	Files []GitFileStatus `json:"files"`
}

// GitFileStatus is an entry of git.status.
type GitFileStatus struct {
	Path     string `json:"path"`
	OrigPath string `json:"origPath"`
	Kind     string `json:"kind"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Conflict bool   `json:"conflict"`
}

// FileStatus converts the entry to gitcli's type.
func (f GitFileStatus) FileStatus() gitcli.FileStatus {
	st := gitcli.FileStatus{Path: f.Path, OrigPath: f.OrigPath, Index: letter(f.Index), Worktree: letter(f.Worktree)}
	switch f.Kind {
	case "renamed":
		st.Kind = gitcli.KindRenamed
	case "unmerged":
		st.Kind = gitcli.KindUnmerged
	case "untracked":
		st.Kind = gitcli.KindUntracked
	case "ignored":
		st.Kind = gitcli.KindIgnored
	}
	return st
}

func letter(s string) byte {
	if s == "" {
		return '.'
	}
	return s[0]
}

// setStatus applies a git.status result (from refreshGit).
func (g *Git) setStatus(st fullStatus, err error) {
	was := g.Available
	g.Available = err == nil
	g.Info = st.GitInfo
	var conflicts []string
	for _, f := range st.Files {
		if f.Conflict {
			conflicts = append(conflicts, f.Path)
		}
	}
	if len(conflicts) > 0 && len(g.Conflicts) == 0 {
		g.c.Status.Notice = fmt.Sprintf("Merge conflicts in %d file(s): open the Git window to resolve them", len(conflicts))
	}
	g.Conflicts = conflicts
	if !g.Available {
		g.dropGutters()
		return
	}
	if !was {
		g.reloadGutters()
	}
}

// ---- events ---------------------------------------------------------------

func (g *Git) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "git.changed", "git.committed":
		g.reloadGutters()
	case api.TopicDocEdited:
		var ev struct {
			File string `json:"file"`
		}
		if json.Unmarshal(payload, &ev) == nil {
			g.scheduleGutter(ev.File)
		}
	}
}

// ---- commit window --------------------------------------------------------

// LoadChanges saves modified documents (like the commit itself does) and
// loads the status with the diffs against HEAD for the commit window.
func (g *Git) LoadChanges() {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		_ = c.call(ctx, "autosave.trigger", map[string]string{"trigger": "beforeCommit"}, nil)
		var st fullStatus
		err := c.call(ctx, "git.status", nil, &st)
		var raw struct {
			Raw string `json:"raw"`
		}
		var diffs []gitcli.FileDiff
		if err == nil {
			if derr := c.call(ctx, "git.diff", map[string]any{"base": "HEAD", "raw": true}, &raw); derr == nil {
				diffs, _ = gitcli.ParseDiff([]byte(raw.Raw))
			}
		}
		var hist []string
		_ = c.call(ctx, "git.recentMessages", map[string]int{"max": 20}, &hist)
		return func() {
			if err != nil {
				if !errIsDisabled(err) {
					c.notice(err)
				}
				return
			}
			byPath := map[string]*gitcli.FileDiff{}
			for i := range diffs {
				byPath[diffs[i].Path()] = &diffs[i]
			}
			g.Changes = g.Changes[:0]
			for _, f := range st.Files {
				g.Changes = append(g.Changes, GitChange{Status: f.FileStatus(), Diff: byPath[f.Path]})
			}
			g.ChangesVersion++
			g.History = hist
			g.Info = st.GitInfo
		}
	})
}

// LoadUntrackedDiff loads a new file's content and offers it as a diff
// that adds every line.
func (g *Git) LoadUntrackedDiff(path string) {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		err := c.call(ctx, "files.read", map[string]any{"path": path, "maxBytes": 1 << 20}, &r)
		return func() {
			if err != nil {
				return
			}
			d := &gitcli.FileDiff{NewPath: path, IsNew: true}
			if r.Encoding != "utf-8" {
				d.IsBinary = true
			} else if lines := gitcli.SplitLines([]byte(r.Content)); len(lines) > 0 {
				h := gitcli.Hunk{NewStart: 1, NewLines: len(lines)}
				for i, l := range lines {
					h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineAdded, Text: strings.TrimRight(l, "\r\n"), NewLine: i + 1})
				}
				d.Hunks = []gitcli.Hunk{h}
			}
			g.Diffs[path] = d
			g.DiffsVersion++
		}
	})
}

// CommitParams builds the git.commitSelection parameters of a request:
// whole files (and the source of a rename) as paths, partial files as
// hunk selections (indexes into git.diff with base HEAD).
func CommitParams(req GitCommitRequest) map[string]any {
	var paths []string
	type hunkSel struct {
		Hunk  int   `json:"hunk"`
		Lines []int `json:"lines,omitempty"`
	}
	type fileHunks struct {
		Path  string    `json:"path"`
		Hunks []hunkSel `json:"hunks"`
	}
	var hunks []fileHunks
	for _, f := range req.Files {
		if f.Whole || len(f.Hunks) == 0 {
			paths = append(paths, f.Path)
			if f.OrigPath != "" && f.OrigPath != f.Path {
				paths = append(paths, f.OrigPath)
			}
			continue
		}
		fh := fileHunks{Path: f.Path}
		for _, h := range f.Hunks {
			fh.Hunks = append(fh.Hunks, hunkSel{Hunk: h.Hunk, Lines: h.Lines})
		}
		hunks = append(hunks, fh)
	}
	p := map[string]any{"message": req.Message}
	if len(paths) > 0 {
		p["paths"] = paths
	}
	if len(hunks) > 0 {
		p["hunks"] = hunks
	}
	if req.Amend {
		p["amend"] = true
		if len(paths) == 0 && len(hunks) == 0 {
			p["allowEmpty"] = true
		}
	}
	if req.Push {
		p["push"] = true
	}
	return p
}

// pushErrorInfo is git.push error data and git.commitSelection pushError.
type pushErrorInfo struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Stderr  string `json:"stderr"`
}

// PushKind maps the API name of a push failure to gitcli's kind.
func PushKind(kind string) gitcli.PushErrorKind {
	switch kind {
	case "rejected":
		return gitcli.PushRejected
	case "remoteRejected":
		return gitcli.PushRemoteRejected
	case "auth":
		return gitcli.PushAuthFailed
	case "network":
		return gitcli.PushNetwork
	case "repoNotFound":
		return gitcli.PushRepoNotFound
	}
	return gitcli.PushFailed
}

func (g *Git) setProblem(p *GitProblem) {
	g.Problem = p
	g.ProblemVersion++
}

// Commit commits the selection (git.commitSelection; the commit.before
// guard saves documents in the backend), optionally pushing afterwards.
func (g *Git) Commit(req GitCommitRequest) {
	c := g.c
	g.Busy = true
	g.setProblem(nil)
	params := CommitParams(req)
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Hash      string         `json:"hash"`
			Pushed    bool           `json:"pushed"`
			PushError *pushErrorInfo `json:"pushError"`
		}
		err := c.call(ctx, "git.commitSelection", params, &res)
		return func() {
			g.Busy = false
			if err != nil {
				g.setProblem(&GitProblem{Err: err})
				return
			}
			g.Committed++
			g.LastCommit = res.Hash
			short := res.Hash
			if len(short) > 7 {
				short = short[:7]
			}
			c.Status.Notice = "Committed " + short
			if res.PushError != nil {
				pe := res.PushError
				g.setProblem(&GitProblem{Push: true, Committed: true, PushKind: PushKind(pe.Kind), Message: pe.Message, Stderr: pe.Stderr})
			} else if res.Pushed {
				c.Status.Notice = "Committed " + short + " and pushed"
			}
			g.afterChange()
		}
	})
}

// Push pushes the current branch (git.push); failures are reported as a
// problem of the commit window and in the status bar.
func (g *Git) Push() {
	c := g.c
	g.Busy = true
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Remote       string `json:"remote"`
			Branch       string `json:"branch"`
			RemoteBranch string `json:"remoteBranch"`
		}
		err := c.call(ctx, "git.push", map[string]any{}, &res)
		return func() {
			g.Busy = false
			if err != nil {
				p := &GitProblem{Err: err}
				var ae *api.Error
				var info pushErrorInfo
				if errors.As(err, &ae) && ae.Data != nil && decodeData(ae.Data, &info) {
					p = &GitProblem{Push: true, PushKind: PushKind(info.Kind), Message: info.Message, Stderr: info.Stderr}
				}
				g.setProblem(p)
				c.Status.Notice = "Push failed: " + err.Error()
				return
			}
			g.setProblem(nil)
			c.Status.Notice = fmt.Sprintf("Pushed %s to %s/%s", res.Branch, res.Remote, res.RemoteBranch)
			c.refreshGit()
		}
	})
}

// decodeData converts error data (a Go value in-process, JSON remotely).
func decodeData(data any, out any) bool {
	raw, ok := data.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(data); err != nil {
			return false
		}
	}
	return json.Unmarshal(raw, out) == nil
}

// LoadAmendMessage loads the last commit message (for Amend).
func (g *Git) LoadAmendMessage() {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var msgs []string
		err := c.call(ctx, "git.recentMessages", map[string]int{"max": 1}, &msgs)
		return func() {
			if err == nil && len(msgs) > 0 {
				g.AmendMessage = msgs[0]
				g.AmendVersion++
			}
		}
	})
}

// afterChange reloads what a repository change affects.
func (g *Git) afterChange() {
	g.c.refreshGit()
	g.LoadChanges()
	g.LoadLog(true)
	g.LoadBranches()
	g.reloadGutters()
}

// ---- log and branches -----------------------------------------------------

type wireCommit struct {
	Hash        string    `json:"hash"`
	Parents     []string  `json:"parents"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Refs        []string  `json:"refs"`
}

// LoadLog loads the first page (reset) or the next page of the log.
func (g *Git) LoadLog(reset bool) {
	if g.logLoading && !reset {
		return
	}
	c := g.c
	skip := len(g.Log)
	if reset {
		skip = 0
	}
	g.logLoading = true
	c.Go(func(ctx context.Context) func() {
		var list []wireCommit
		err := c.call(ctx, "git.log", map[string]int{"skip": skip, "max": LogPage}, &list)
		return func() {
			g.logLoading = false
			if err != nil {
				if reset {
					g.Log, g.LogMore = nil, false
					g.LogVersion++
				}
				return
			}
			out := make([]gitcli.Commit, 0, len(list))
			for _, w := range list {
				out = append(out, gitcli.Commit{Hash: w.Hash, Parents: w.Parents, AuthorName: w.Author, AuthorEmail: w.AuthorEmail, AuthorDate: w.Date, Subject: w.Subject, Refs: w.Refs})
			}
			if reset {
				g.Log = out
				g.LogVersion++
			} else {
				g.Log = append(g.Log, out...)
			}
			g.LogMore = len(list) == LogPage
		}
	})
}

// SelectCommit loads the diff of a commit against its first parent.
func (g *Git) SelectCommit(hash string) {
	c := g.c
	base := emptyTree
	for _, cm := range g.Log {
		if cm.Hash == hash && len(cm.Parents) > 0 {
			base = cm.Parents[0]
		}
	}
	g.SelectedCommit = hash
	c.Go(func(ctx context.Context) func() {
		var raw struct {
			Raw string `json:"raw"`
		}
		err := c.call(ctx, "git.diff", map[string]any{"base": base, "target": hash, "raw": true}, &raw)
		var files []gitcli.FileDiff
		if err == nil {
			files, err = gitcli.ParseDiff([]byte(raw.Raw))
		}
		return func() {
			if g.SelectedCommit != hash {
				return
			}
			if err != nil {
				c.notice(err)
				files = nil
			}
			g.CommitFiles = files
			g.CommitVersion++
		}
	})
}

type wireBranch struct {
	Name     string `json:"name"`
	Remote   bool   `json:"remote"`
	Current  bool   `json:"current"`
	Hash     string `json:"hash"`
	Upstream string `json:"upstream"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Subject  string `json:"subject"`
}

func toBranches(list []wireBranch) []gitcli.Branch {
	out := make([]gitcli.Branch, 0, len(list))
	for _, b := range list {
		ref := "refs/heads/" + b.Name
		if b.Remote {
			ref = "refs/remotes/" + b.Name
		}
		out = append(out, gitcli.Branch{Name: b.Name, Ref: ref, Remote: b.Remote, Current: b.Current, Hash: b.Hash, Upstream: b.Upstream, Ahead: b.Ahead, Behind: b.Behind, Subject: b.Subject})
	}
	return out
}

// LoadBranches loads the branch list.
func (g *Git) LoadBranches() {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var list []wireBranch
		err := c.call(ctx, "git.branches", nil, &list)
		return func() {
			if err != nil {
				return
			}
			g.Branches = toBranches(list)
			g.BranchesVersion++
		}
	})
}

// branchOp runs a branch method and reloads.
func (g *Git) branchOp(method string, params any, done string) {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, method, params, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			if done != "" {
				c.Status.Notice = done
			}
			g.afterChange()
		}
	})
}

// Checkout switches to a branch. A remote branch ("origin/x") is checked
// out as a new local tracking branch ("x") unless that exists.
func (g *Git) Checkout(name string, remote bool) {
	if remote {
		local := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			local = name[i+1:]
		}
		for _, b := range g.Branches {
			if !b.Remote && b.Name == local {
				g.branchOp("git.checkout", map[string]string{"ref": local}, "Checked out "+local)
				return
			}
		}
		g.branchOp("git.createBranch", map[string]any{"name": local, "start": name, "checkout": true}, "Checked out "+local)
		return
	}
	g.branchOp("git.checkout", map[string]string{"ref": name}, "Checked out "+name)
}

// CreateBranch creates a branch at start ("" = HEAD).
func (g *Git) CreateBranch(name, start string, checkout bool) {
	g.branchOp("git.createBranch", map[string]any{"name": name, "start": start, "checkout": checkout}, "Created branch "+name)
}

// DeleteBranch deletes a local branch.
func (g *Git) DeleteBranch(name string, force bool) {
	g.branchOp("git.deleteBranch", map[string]any{"name": name, "force": force}, "Deleted branch "+name)
}

// ---- merge ----------------------------------------------------------------

type wireMergeBlock struct {
	Kind        string `json:"kind"`
	Base        string `json:"base"`
	Ours        string `json:"ours"`
	Theirs      string `json:"theirs"`
	BaseStart   int    `json:"baseStart"`
	OursStart   int    `json:"oursStart"`
	TheirsStart int    `json:"theirsStart"`
}

var mergeKinds = map[string]gitcli.MergeBlockKind{
	"unchanged": gitcli.MergeUnchanged, "ours": gitcli.MergeOursOnly, "theirs": gitcli.MergeTheirsOnly,
	"both": gitcli.MergeBothSame, "conflict": gitcli.MergeConflict,
}

// OpenMerge loads the three-way merge of a conflicted file
// (git.mergeData); the result appears in Merges.
func (g *Git) OpenMerge(path string) {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var md struct {
			Path   string           `json:"path"`
			Blocks []wireMergeBlock `json:"blocks"`
		}
		err := c.call(ctx, "git.mergeData", map[string]string{"path": path}, &md)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			res := &gitcli.MergeResult{}
			for _, b := range md.Blocks {
				res.Blocks = append(res.Blocks, gitcli.MergeBlock{
					Kind: mergeKinds[b.Kind], Base: gitcli.SplitLines([]byte(b.Base)), Ours: gitcli.SplitLines([]byte(b.Ours)),
					Theirs: gitcli.SplitLines([]byte(b.Theirs)), BaseStart: b.BaseStart - 1, OursStart: b.OursStart - 1, TheirsStart: b.TheirsStart - 1,
				})
			}
			g.Merges[path] = &GitMerge{Path: path, Result: res}
			g.MergeVersion++
		}
	})
}

// ApplyMerge writes the merged content and marks the file resolved
// (git.resolve).
func (g *Git) ApplyMerge(path string, content []byte) {
	c := g.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "git.resolve", map[string]any{"path": path, "content": string(content)}, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Resolved " + path
			g.CloseMerge(path)
			g.afterChange()
		}
	})
}

// CloseMerge forgets a merge (window closed).
func (g *Git) CloseMerge(path string) {
	if _, ok := g.Merges[path]; ok {
		delete(g.Merges, path)
		g.MergeVersion++
	}
}

// ---- gutter ---------------------------------------------------------------

// Gutter returns the changed ranges of an open file (absolute path),
// loading them on first use.
func (g *Git) Gutter(file string) []GutterRange {
	if !g.Available || file == "" {
		return nil
	}
	st := g.gutters[file]
	if st == nil {
		st = &gutterState{}
		g.gutters[file] = st
		g.loadGutter(file)
	}
	return st.ranges
}

// RangeAt returns the changed range containing the 1-based line.
func (g *Git) RangeAt(file string, line int) (GutterRange, bool) {
	for _, r := range g.Gutter(file) {
		if r.Contains(line) {
			return r, true
		}
	}
	return GutterRange{}, false
}

func (g *Git) loadGutter(file string) {
	st := g.gutters[file]
	if st == nil {
		return
	}
	if st.loading {
		st.again = true
		return
	}
	st.loading = true
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Ranges []GutterRange `json:"ranges"`
		}
		err := c.call(ctx, "git.gutter", map[string]string{"path": file}, &res)
		return func() {
			st.loading = false
			if g.gutters[file] != st {
				return
			}
			if err != nil {
				res.Ranges = nil
			}
			st.ranges = res.Ranges
			g.GutterVersion++
			if st.again {
				st.again = false
				g.loadGutter(file)
			}
		}
	})
}

// scheduleGutter reloads the gutter of file after GutterDelay (coalesced).
func (g *Git) scheduleGutter(file string) {
	st := g.gutters[file]
	if st == nil || !g.Available || st.timer != nil {
		return
	}
	c := g.c
	st.timer = time.AfterFunc(GutterDelay, func() {
		c.post(func() {
			st.timer = nil
			g.loadGutter(file)
		})
	})
}

// reloadGutters reloads the gutters of open tabs and forgets the others.
func (g *Git) reloadGutters() {
	for file := range g.gutters {
		if g.c.Tab(file) == nil {
			delete(g.gutters, file)
			continue
		}
		g.loadGutter(file)
	}
}

func (g *Git) dropGutters() {
	for f, st := range g.gutters {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(g.gutters, f)
	}
	g.GutterVersion++
}

// ShowBlock loads the diff of the change at the 1-based line of file
// (HEAD version against the document) into Block.
func (g *Git) ShowBlock(file string, line int) {
	r, ok := g.RangeAt(file, line)
	if !ok {
		return
	}
	c := g.c
	c.Go(func(ctx context.Context) func() {
		var base struct {
			Content string `json:"content"`
		}
		var cur struct {
			Text string `json:"text"`
		}
		_ = c.call(ctx, "git.show", map[string]string{"rev": "HEAD", "path": file}, &base)
		err := c.call(ctx, "doc.text", map[string]string{"path": file}, &cur)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			g.Block = &GitBlock{File: file, Range: r, Diff: blockDiff(c.Rel(file), base.Content, cur.Text, r)}
			g.BlockVersion++
		}
	})
}

// CloseBlock hides the block popup.
func (g *Git) CloseBlock() {
	if g.Block != nil {
		g.Block = nil
		g.BlockVersion++
	}
}

// blockDiff builds a one-hunk diff of a changed range: the base lines as
// deleted, the current lines as added.
func blockDiff(path, base, cur string, r GutterRange) *gitcli.FileDiff {
	bl, cl := gitcli.SplitLines([]byte(base)), gitcli.SplitLines([]byte(cur))
	sub := func(ls []string, from, to int) []string {
		from, to = max(from-1, 0), min(to-1, len(ls))
		if from >= to {
			return nil
		}
		return ls[from:to]
	}
	old, now := sub(bl, r.BaseStart, r.BaseEnd), sub(cl, r.Start, r.End)
	h := gitcli.Hunk{OldStart: r.BaseStart, OldLines: len(old), NewStart: r.Start, NewLines: len(now)}
	for i, l := range old {
		h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineDeleted, Text: strings.TrimRight(l, "\r\n"), OldLine: r.BaseStart + i})
	}
	for i, l := range now {
		h.Lines = append(h.Lines, gitcli.DiffLine{Kind: gitcli.LineAdded, Text: strings.TrimRight(l, "\r\n"), NewLine: r.Start + i})
	}
	return &gitcli.FileDiff{OldPath: path, NewPath: path, Hunks: []gitcli.Hunk{h}}
}

// Rollback restores the HEAD version of the change at the 1-based line
// (git.rollback; one undoable edit of the open document).
func (g *Git) Rollback(file string, line int) {
	c := g.c
	g.CloseBlock()
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "git.rollback", map[string]any{"path": file, "line": line}, nil)
		return func() {
			c.notice(err)
			if st := g.gutters[file]; st != nil {
				g.loadGutter(file)
			}
		}
	})
}

// RollbackFile rolls back every change of file, bottom up so the line
// numbers of the remaining ranges stay valid.
func (g *Git) RollbackFile(file string) {
	ranges := append([]GutterRange(nil), g.Gutter(file)...)
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start > ranges[j].Start })
	c := g.c
	g.CloseBlock()
	c.Go(func(ctx context.Context) func() {
		var err error
		for _, r := range ranges {
			if e := c.call(ctx, "git.rollback", map[string]any{"path": file, "line": r.Start}, nil); e != nil && err == nil {
				err = e
			}
		}
		return func() {
			c.notice(err)
			g.loadGutter(file)
		}
	})
}

// reset forgets the state of the previous project.
func (g *Git) reset() {
	g.dropGutters()
	g.Changes, g.Diffs, g.History, g.Log, g.Branches = nil, map[string]*gitcli.FileDiff{}, nil, nil, nil
	g.CommitFiles, g.SelectedCommit, g.Conflicts, g.Block = nil, "", nil, nil
	g.Merges = map[string]*GitMerge{}
	g.ChangesVersion++
	g.LogVersion++
	g.BranchesVersion++
	g.CommitVersion++
	g.MergeVersion++
	g.BlockVersion++
	g.setProblem(nil)
}

// IsConflict reports whether a project-relative path has a conflict.
func (g *Git) IsConflict(path string) bool {
	for _, p := range g.Conflicts {
		if p == path || filepath.ToSlash(p) == path {
			return true
		}
	}
	return false
}
