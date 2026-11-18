package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// File-centric VCS views: diff tabs (git.fileDiff, buffer vs. disk),
// annotations (git.blame), the file history (git.fileHistory) and the
// Local History (history.*). Results of git.fileDiff and git.blame are
// kept as raw JSON; the view decodes them into the organisms' API mirror
// types (organisms/diff convert.go), so both sides read the same wire
// format.

// Virtual tab kinds of the workspace.
const (
	TabDiff         = "diff"
	TabFileHistory  = "fileHistory"
	TabLocalHistory = "localHistory"
)

// Versions of git.fileDiff.
const (
	VersionHead    = "HEAD"
	VersionIndex   = "index"
	VersionWorking = "working"
	VersionEmpty   = "empty"
)

// DiffSpec describes the comparison of a diff tab.
type DiffSpec struct {
	// Path is the project-relative file (right side).
	Path string
	// BasePath is the path on the left side after a rename ("" = Path).
	BasePath string
	// Base and Target are git.fileDiff versions (HEAD, index, working,
	// empty or a revision); Target "" means working.
	Base, Target string
	// External compares the editor buffer (right) with the file on disk
	// (left) instead of git versions.
	External bool
	// IgnoreWhitespace asks git.fileDiff to ignore white space.
	IgnoreWhitespace bool
	// Title overrides the tab title.
	Title string
	// Texts compares two texts that are no files (expected/actual of a
	// failed test) via diff.texts; Path names the related file.
	Texts *DiffTexts
}

// DiffTexts are the sides of a text comparison.
type DiffTexts struct {
	Left, Right           string
	LeftLabel, RightLabel string
	// Key distinguishes comparisons of the same file (e.g. the test ID).
	Key string
}

func (s DiffSpec) target() string {
	if s.Target == "" {
		return VersionWorking
	}
	return s.Target
}

// key identifies the tab of a comparison (ignoring the white space
// option and the title).
func (s DiffSpec) key() string {
	if s.Texts != nil {
		return "texts:" + s.Path + ":" + s.Texts.Key
	}
	if s.External {
		return "disk:" + s.Path
	}
	return s.Base + ".." + s.target() + ":" + s.BasePath + ":" + s.Path
}

// title is the tab title.
func (s DiffSpec) title() string {
	if s.Title != "" {
		return s.Title
	}
	name := path.Base(s.Path)
	if s.External {
		return name + " (disk ↔ editor)"
	}
	return fmt.Sprintf("%s (%s ↔ %s)", name, shortRev(s.Base), shortRev(s.target()))
}

// shortRev abbreviates a hash for titles.
func shortRev(r string) string {
	switch {
	case r == VersionWorking:
		return "local"
	case len(r) == 40:
		return r[:8]
	}
	return r
}

// DiffTab is the state of a diff tab (Tab.Data).
type DiffTab struct {
	Spec DiffSpec
	// File is the absolute right-side file.
	File string
	// Result is the raw git.fileDiff result (decode with
	// organisms/diff.APIFileDiff); nil for External.
	Result json.RawMessage
	// Disk is the file content on disk (External).
	Disk string
	// Doc is the shared document of the right side when it is the work
	// tree (editable in the diff); nil otherwise.
	Doc     DocHandle
	Loading bool
	Err     error
	// Version changes whenever the loaded content changes.
	Version uint64
	// ownGen is the doc.open generation of a document the tab opened
	// itself (closed again with the tab).
	ownGen uint64
	closed bool
	// seq numbers the loads; only the latest is applied.
	seq uint64
}

// Editable reports whether the right side is the shared work tree
// document.
func (d *DiffTab) Editable() bool { return d.Doc != nil }

// OpenDiff opens (or re-activates and reloads) the diff tab of spec in
// the active group.
func (w *Workspace) OpenDiff(spec DiffSpec) *DiffTab {
	c := w.c
	key := spec.key()
	dt := w.diffs[key]
	if dt == nil {
		dt = &DiffTab{Spec: spec, File: c.Abs(spec.Path)}
		w.diffs[key] = dt
	} else {
		dt.Spec.IgnoreWhitespace = spec.IgnoreWhitespace
	}
	c.OpenVirtual(TabDiff, key, spec.title(), dt)
	w.loadDiff(dt)
	return dt
}

// Diffs returns the open diff tabs (tests).
func (w *Workspace) Diffs() []*DiffTab {
	out := make([]*DiffTab, 0, len(w.diffs))
	for _, d := range w.diffs {
		out = append(out, d)
	}
	return out
}

// SetIgnoreWhitespace reloads a diff tab with or without white space.
func (w *Workspace) SetIgnoreWhitespace(dt *DiffTab, on bool) {
	if dt.Spec.IgnoreWhitespace == on {
		return
	}
	dt.Spec.IgnoreWhitespace = on
	w.loadDiff(dt)
}

// ReloadDiff loads a diff tab again.
func (w *Workspace) ReloadDiff(dt *DiffTab) { w.loadDiff(dt) }

// documentFor returns the open document of file (any group).
func (c *Controller) documentFor(file string) DocHandle {
	if t := c.sharedTab(file, nil); t != nil && t.Doc != nil {
		return t.Doc
	}
	return nil
}

func (w *Workspace) loadDiff(dt *DiffTab) {
	c := w.c
	spec := dt.Spec
	dt.Loading = true
	dt.seq++
	seq := dt.seq
	doc := c.documentFor(dt.File)
	needDoc := spec.Texts == nil && (spec.External || spec.target() == VersionWorking)
	own := dt.ownGen
	c.Go(func(ctx context.Context) func() {
		var err error
		gen := own
		if needDoc && doc == nil && gen == 0 {
			var st struct {
				Generation uint64 `json:"generation"`
			}
			if e := c.call(ctx, "doc.open", map[string]any{"path": dt.File}, &st); e == nil {
				gen = st.Generation
				doc, _ = c.be.Document(dt.File)
			}
		} else if needDoc && doc == nil {
			doc, _ = c.be.Document(dt.File)
		}
		var raw json.RawMessage
		var disk string
		if spec.Texts != nil {
			tx := spec.Texts
			err = c.call(ctx, "diff.texts", api.DiffTextsParams{Left: tx.Left, Right: tx.Right, LeftLabel: tx.LeftLabel,
				RightLabel: tx.RightLabel, IgnoreWhitespace: spec.IgnoreWhitespace}, &raw)
		} else if spec.External {
			var rd struct {
				Content string `json:"content"`
			}
			err = c.call(ctx, "files.read", map[string]any{"path": dt.File}, &rd)
			disk = rd.Content
		} else {
			params := map[string]any{"path": dt.File, "base": spec.Base, "target": spec.target()}
			if spec.BasePath != "" {
				params["basePath"] = spec.BasePath
			}
			if spec.IgnoreWhitespace {
				params["ignoreWhitespace"] = true
			}
			err = c.call(ctx, "git.fileDiff", params, &raw)
		}
		return func() {
			if dt.closed {
				if gen != 0 && gen != own {
					w.closeOwnDoc(dt.File, gen)
				}
				return
			}
			if gen != own && dt.ownGen == 0 {
				dt.ownGen = gen
			} else if gen != own && gen != dt.ownGen {
				w.closeOwnDoc(dt.File, gen) // a concurrent load opened it as well
			}
			if seq != dt.seq {
				return
			}
			dt.Loading = false
			dt.Err = err
			dt.Result, dt.Disk = raw, disk
			if needDoc {
				dt.Doc = doc
			}
			dt.Version++
			if err != nil {
				c.notice(err)
			}
		}
	})
}

// CloseDiff forgets a diff tab (its tab was closed); a document the tab
// opened itself is saved and closed unless an editor tab uses it.
func (w *Workspace) CloseDiff(dt *DiffTab) {
	dt.closed = true
	delete(w.diffs, dt.Spec.key())
	if dt.ownGen != 0 && w.c.documentFor(dt.File) == nil {
		w.closeOwnDoc(dt.File, dt.ownGen)
	}
	dt.ownGen = 0
}

func (w *Workspace) closeOwnDoc(file string, gen uint64) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		_ = c.call(ctx, "doc.save", pathParams{Path: file}, nil)
		_ = c.call(ctx, "doc.close", map[string]any{"path": file, "generation": gen}, nil)
		return nil
	})
}

// CommitFileDiff opens the diff of one file of a commit against its first
// parent (or the empty file for a root commit / an added file).
func (w *Workspace) CommitFileDiff(hash string, parents []string, p, oldPath string, added bool) {
	base := VersionEmpty
	if len(parents) > 0 && !added {
		base = parents[0]
	}
	w.OpenDiff(DiffSpec{Path: p, BasePath: oldPath, Base: base, Target: hash,
		Title: fmt.Sprintf("%s (%s)", path.Base(p), shortRev(hash))})
}

// ---- annotations (git.blame) ----------------------------------------------

// BlameState is the annotation of one open file.
type BlameState struct {
	File string
	// Result is the raw git.blame result (organisms/diff.APIBlame).
	Result  json.RawMessage
	Err     error
	Version uint64
}

// BlameDelay is the pause after an edit before the annotation is
// recomputed (tests shorten it).
var BlameDelay = 600 * time.Millisecond

// Annotated reports whether file is annotated.
func (w *Workspace) Annotated(file string) bool { return w.Blame[file] != nil }

// ToggleAnnotate switches the annotation of file on or off.
func (w *Workspace) ToggleAnnotate(file string) {
	if w.Blame[file] != nil {
		delete(w.Blame, file)
		if t := w.blameTimers[file]; t != nil {
			t.Stop()
			delete(w.blameTimers, file)
		}
		w.BlameVersion++
		return
	}
	w.Blame[file] = &BlameState{File: file}
	w.BlameVersion++
	w.loadBlame(file)
}

// loadBlame computes the annotation with the current buffer content.
func (w *Workspace) loadBlame(file string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var raw json.RawMessage
		err := c.call(ctx, "git.blame", map[string]any{"path": file}, &raw)
		return func() {
			b := w.Blame[file]
			if b == nil {
				return
			}
			b.Err = err
			if err == nil {
				b.Result = raw
			} else {
				c.notice(err)
			}
			b.Version++
			w.BlameVersion++
		}
	})
}

// blameChanged schedules a recomputation after edits, saves and git
// changes.
func (w *Workspace) blameChanged(topic, file string) {
	if len(w.Blame) == 0 {
		return
	}
	if topic == api.TopicGitChanged || file == "" {
		for f := range w.Blame {
			w.scheduleBlame(f)
		}
		return
	}
	if w.Blame[file] != nil {
		w.scheduleBlame(file)
	}
}

func (w *Workspace) scheduleBlame(file string) {
	if t := w.blameTimers[file]; t != nil {
		t.Stop()
	}
	w.blameTimers[file] = time.AfterFunc(BlameDelay, func() {
		w.c.post(func() {
			delete(w.blameTimers, file)
			if w.Blame[file] != nil {
				w.loadBlame(file)
			}
		})
	})
}

// BlameCommit is the part of a git.blame commit the controller needs.
type BlameCommit struct {
	Hash     string `json:"hash"`
	Author   string `json:"author"`
	Summary  string `json:"summary"`
	Path     string `json:"path"`
	Previous string `json:"previous"`
	PrevPath string `json:"prevPath"`
	Boundary bool   `json:"boundary"`
}

// ShowBlameCommit opens the diff of the commit that last changed a line
// of an annotated file.
func (w *Workspace) ShowBlameCommit(file, hash string) {
	b := w.Blame[file]
	if b == nil || b.Result == nil {
		return
	}
	var res struct {
		Commits []BlameCommit `json:"commits"`
	}
	if json.Unmarshal(b.Result, &res) != nil {
		return
	}
	for _, cm := range res.Commits {
		if cm.Hash != hash {
			continue
		}
		p := cm.Path
		if p == "" {
			p = w.c.Rel(file)
		}
		var parents []string
		if cm.Previous != "" {
			parents = []string{cm.Previous}
		}
		w.CommitFileDiff(hash, parents, p, cm.PrevPath, cm.Previous == "")
		w.c.Vcs.SelectCommit(hash)
		return
	}
}

// ---- file history (git.fileHistory) ------------------------------------------

// HistoryTab is the state of a file history tab.
type HistoryTab struct {
	File, Path string
	Entries    []gitcli.FileCommit
	More       bool
	// Version changes when Entries are replaced; appended pages keep it
	// (compare len(Entries)).
	Version uint64
	// Diff is the raw git.fileDiff of the selected commit (DiffFor).
	Diff        json.RawMessage
	DiffFor     string
	DiffVersion uint64
	Err         error
	loading     bool
}

type wireFileCommit struct {
	Hash        string    `json:"hash"`
	Parents     []string  `json:"parents"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Refs        []string  `json:"refs"`
	Path        string    `json:"path"`
	OldPath     string    `json:"oldPath"`
	Status      string    `json:"status"`
}

func (wc wireFileCommit) fileCommit() gitcli.FileCommit {
	fc := gitcli.FileCommit{Commit: gitcli.Commit{Hash: wc.Hash, Parents: wc.Parents, AuthorName: wc.Author, AuthorEmail: wc.AuthorEmail,
		AuthorDate: wc.Date, Subject: wc.Subject, Refs: wc.Refs}, Path: wc.Path, OldPath: wc.OldPath}
	if wc.Status != "" {
		fc.Status = wc.Status[0]
	}
	return fc
}

// OpenFileHistory opens the history tab of a project-relative file.
func (w *Workspace) OpenFileHistory(rel string) *HistoryTab {
	c := w.c
	ht := w.histories[rel]
	if ht == nil {
		ht = &HistoryTab{File: c.Abs(rel), Path: rel}
		w.histories[rel] = ht
	}
	c.OpenVirtual(TabFileHistory, rel, "History: "+path.Base(rel), ht)
	ht.Entries, ht.More = nil, false
	w.loadHistory(ht, 0)
	return ht
}

// MoreHistory loads the next page.
func (w *Workspace) MoreHistory(ht *HistoryTab) {
	if ht.More && !ht.loading {
		w.loadHistory(ht, len(ht.Entries))
	}
}

func (w *Workspace) loadHistory(ht *HistoryTab, skip int) {
	c := w.c
	ht.loading = true
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Commits []wireFileCommit `json:"commits"`
			More    bool             `json:"more"`
		}
		err := c.call(ctx, "git.fileHistory", map[string]any{"path": ht.Path, "skip": skip, "max": 100}, &res)
		return func() {
			ht.loading = false
			ht.Err = err
			if err != nil {
				c.notice(err)
				return
			}
			if skip == 0 {
				ht.Entries = ht.Entries[:0]
				ht.Version++
			}
			for _, cm := range res.Commits {
				ht.Entries = append(ht.Entries, cm.fileCommit())
			}
			ht.More = res.More
		}
	})
}

// HistoryDiff loads the diff of commit (the answer to the view's
// HistoryDiffRequested).
func (w *Workspace) HistoryDiff(ht *HistoryTab, commit, base, p, parentPath string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		params := map[string]any{"path": p, "base": base, "target": commit}
		if parentPath != "" && parentPath != p {
			params["basePath"] = parentPath
		}
		var raw json.RawMessage
		err := c.call(ctx, "git.fileDiff", params, &raw)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			ht.Diff, ht.DiffFor = raw, commit
			ht.DiffVersion++
		}
	})
}

// CloseFileHistory forgets a history tab.
func (w *Workspace) CloseFileHistory(ht *HistoryTab) { delete(w.histories, ht.Path) }

// ---- Local History (history.*) ---------------------------------------------

// LocalRevision is a Local History revision of a file.
type LocalRevision struct {
	ID    uint64    `json:"id"`
	Time  time.Time `json:"time"`
	Size  int64     `json:"size"`
	Label string    `json:"label"`
}

// LocalHistoryTab is the state of a Local History tab: the revisions, the
// selected one and its text against the current content.
type LocalHistoryTab struct {
	File, Path string
	Revisions  []LocalRevision
	Version    uint64
	// Selected is the shown revision; RevText its content and Current the
	// current content (open document or file).
	Selected    uint64
	RevText     string
	Current     string
	DiffVersion uint64
	Err         error
}

// OpenLocalHistory opens the Local History of a project-relative file;
// selectID (0: newest) is shown first.
func (w *Workspace) OpenLocalHistory(rel string, selectID uint64) *LocalHistoryTab {
	c := w.c
	lt := w.locals[rel]
	if lt == nil {
		lt = &LocalHistoryTab{File: c.Abs(rel), Path: rel}
		w.locals[rel] = lt
	}
	c.OpenVirtual(TabLocalHistory, rel, "Local History: "+path.Base(rel), lt)
	w.loadLocalHistory(lt, selectID)
	return lt
}

func (w *Workspace) loadLocalHistory(lt *LocalHistoryTab, selectID uint64) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var revs []LocalRevision
		err := c.call(ctx, "history.list", map[string]any{"path": lt.File}, &revs)
		return func() {
			lt.Err = err
			if err != nil {
				c.notice(err)
				return
			}
			lt.Revisions = revs
			lt.Version++
			sel := selectID
			if sel == 0 && len(revs) > 0 {
				sel = revs[0].ID
			}
			if sel != 0 {
				w.SelectLocalRevision(lt, sel)
			}
		}
	})
}

// SelectLocalRevision loads a revision and the current content.
func (w *Workspace) SelectLocalRevision(lt *LocalHistoryTab, id uint64) {
	c := w.c
	open := c.documentFor(lt.File) != nil
	c.Go(func(ctx context.Context) func() {
		var rev struct {
			Text string `json:"text"`
		}
		err := c.call(ctx, "history.content", map[string]any{"path": lt.File, "id": id}, &rev)
		cur := ""
		if open {
			var dt struct {
				Text string `json:"text"`
			}
			if e := c.call(ctx, "doc.text", map[string]any{"path": lt.File}, &dt); e == nil {
				cur = dt.Text
			}
		} else {
			var rd struct {
				Content string `json:"content"`
			}
			if e := c.call(ctx, "files.read", map[string]any{"path": lt.File}, &rd); e == nil {
				cur = rd.Content
			}
		}
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			lt.Selected, lt.RevText, lt.Current = id, rev.Text, cur
			lt.DiffVersion++
		}
	})
}

// RevertToLocalRevision restores a revision (history.restore; an open
// document is reloaded as one undo step) and reloads the list.
func (w *Workspace) RevertToLocalRevision(lt *LocalHistoryTab, id uint64) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "history.restore", map[string]any{"path": lt.File, "id": id}, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = fmt.Sprintf("Reverted %s to revision #%d", lt.Path, id)
			for _, pt := range c.Recovery {
				if pt.File == lt.File && pt.Revision == id {
					c.DismissRecovery(lt.File)
				}
			}
			w.loadLocalHistory(lt, 0)
		}
	})
}

// CloseLocalHistory forgets a Local History tab.
func (w *Workspace) CloseLocalHistory(lt *LocalHistoryTab) { delete(w.locals, lt.Path) }

// RevisionLabel is the list label of a revision.
func RevisionLabel(r LocalRevision) string {
	l := r.Label
	if l == "" {
		l = "revision"
	}
	return strings.TrimSpace(fmt.Sprintf("#%d  %s", r.ID, l))
}
