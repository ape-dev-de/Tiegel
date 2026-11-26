package controller

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Workspace is the file side of the GUI (AP10 GUI integration, part 2):
// file operations of the project tree (files.*), external changes of open
// documents (doc.externalConflict, doc.deletedOnDisk, watch.refresh), the
// file-centric VCS views (diff tabs, annotations, file history, Local
// History) and the remote git operations (fetch, pull, update, stash).
//
// It follows files.changed (tree refresh, also for watcher changes) and
// doc.renamed (tabs follow their document). Like the rest of the
// controller it is owned by the UI goroutine; blocking calls run through
// Controller.Go.
type Workspace struct {
	c *Controller

	// External maps the absolute files of open documents to a pending
	// external change (banner above the editor).
	External        map[string]*ExternalChange
	ExternalVersion uint64

	// Clip is the file clipboard of the tree (Copy/Cut, Paste).
	Clip FileClip

	renames []Rename

	// Blame holds the annotations (vcs.annotate) per absolute file.
	Blame        map[string]*BlameState
	BlameVersion uint64
	blameTimers  map[string]*time.Timer

	diffs     map[string]*DiffTab
	histories map[string]*HistoryTab
	locals    map[string]*LocalHistoryTab

	// Remote is the state of fetch/pull/update and the stash list.
	Remote Remote
}

// ExternalChange is a pending external change of an open document.
type ExternalChange struct {
	File string
	Path string
	// Kind is modified, created or deleted.
	Kind string
	// RenamedTo is the new display path of a file renamed on disk.
	RenamedTo string
	// Modified tells whether the buffer has unsaved changes.
	Modified bool
	// Added and Removed count the lines only on disk / only in the
	// buffer.
	Added, Removed int
	// Source is watcher, save or refresh.
	Source string
	// Busy is set while a resolution runs.
	Busy bool
}

// Deleted reports whether the file is gone on disk.
func (e *ExternalChange) Deleted() bool { return e.Kind == "deleted" }

// Rename is a document that followed a rename or move (doc.renamed); the
// view moves its editor widgets along.
type Rename struct{ From, To string }

// FileClip is the content of the tree's file clipboard.
type FileClip struct {
	// Paths are project-relative entries.
	Paths []string
	// Cut moves the entries on paste instead of copying them.
	Cut bool
}

// workspaceTopics are the events the workspace follows.
var workspaceTopics = []string{api.TopicFilesChanged, api.TopicDocRenamed, api.TopicDocExternalConflict,
	api.TopicDocDeletedOnDisk, api.TopicDocReloaded, api.TopicDocClosed, api.TopicFileSaved, api.TopicGitChanged, api.TopicDocEdited}

func newWorkspace(c *Controller) *Workspace {
	w := &Workspace{c: c}
	w.reset()
	unsub, err := c.be.Subscribe(c.ctx, workspaceTopics, func(e api.Event) {
		c.post(func() { w.handleEvent(e.Topic, e.Payload) })
	})
	if err == nil {
		go func() {
			<-c.ctx.Done()
			unsub()
		}()
	}
	return w
}

// reset forgets the state of the previous project.
func (w *Workspace) reset() {
	w.External = map[string]*ExternalChange{}
	w.ExternalVersion++
	w.Clip = FileClip{}
	w.renames = nil
	for _, t := range w.blameTimers {
		t.Stop()
	}
	w.Blame, w.blameTimers = map[string]*BlameState{}, map[string]*time.Timer{}
	w.BlameVersion++
	w.diffs, w.histories, w.locals = map[string]*DiffTab{}, map[string]*HistoryTab{}, map[string]*LocalHistoryTab{}
	w.Remote = Remote{}
}

// TakeRenames returns and clears the renames since the last call.
func (w *Workspace) TakeRenames() []Rename {
	r := w.renames
	w.renames = nil
	return r
}

func (w *Workspace) handleEvent(topic string, payload json.RawMessage) {
	c := w.c
	if !c.Project.Open {
		return
	}
	switch topic {
	case api.TopicFilesChanged:
		var ev api.FilesChangedEvent
		if json.Unmarshal(payload, &ev) == nil {
			w.filesChanged(ev)
		}
	case api.TopicDocRenamed:
		var ev api.DocRenamedEvent
		if json.Unmarshal(payload, &ev) == nil {
			w.docRenamed(ev)
		}
	case api.TopicDocExternalConflict, api.TopicDocDeletedOnDisk:
		var ev api.DocExternalEvent
		if json.Unmarshal(payload, &ev) == nil {
			w.externalEvent(ev)
		}
	case api.TopicDocReloaded, api.TopicDocClosed:
		var ev struct {
			Path string `json:"path"`
			File string `json:"file"`
		}
		if json.Unmarshal(payload, &ev) == nil {
			file := ev.File
			if file == "" {
				file = c.Abs(ev.Path)
			}
			w.clearExternal(file)
		}
	case api.TopicFileSaved, api.TopicDocEdited, api.TopicGitChanged:
		var ev struct {
			File string `json:"file"`
		}
		_ = json.Unmarshal(payload, &ev)
		w.blameChanged(topic, ev.File)
		if topic == api.TopicFileSaved && ev.File != "" {
			w.clearExternal(ev.File)
		}
	}
}

// ---- file tree ------------------------------------------------------------

// parentDir returns the project-relative parent of a slash path ("" for
// the root).
func parentDir(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// filesChanged refreshes the tree directories that contain changed
// entries (file operations and the watcher) and the git status.
func (w *Workspace) filesChanged(ev api.FilesChangedEvent) {
	c := w.c
	dirs := map[string]bool{}
	structural := false
	for _, ch := range ev.Changes {
		if ch.Kind == api.FileModified {
			continue
		}
		structural = true
		dirs[parentDir(ch.Path)] = true
		if ch.OldPath != "" {
			dirs[parentDir(ch.OldPath)] = true
		}
	}
	if c.Tree != nil {
		for d := range dirs {
			if n := c.Tree.Node(d); n != nil && n.Dir && n.Loaded {
				w.reloadDir(d)
			}
		}
	}
	if structural {
		c.Files = nil // quick-open index is stale
	}
	if len(ev.Changes) > 0 {
		c.refreshGit()
	}
}

// reloadDir lists a loaded directory again (like LoadDir, but also while
// a load is in flight, so no change is lost).
func (w *Workspace) reloadDir(rel string) {
	c := w.c
	n := c.Tree.Node(rel)
	if n == nil {
		return
	}
	n.Loading = true
	lib := n.Library
	tree := c.Tree
	c.Go(func(ctx context.Context) func() {
		var res filesListResult
		err := c.call(ctx, "files.list", map[string]any{"path": rel, "depth": 1, "libraries": lib}, &res)
		return func() {
			if c.Tree != tree {
				return
			}
			n.Loading = false
			if err != nil || res.Tree == nil {
				return
			}
			res.Tree.Path = rel
			res.Tree.Collapsed = false
			tree.merge(res.Tree)
		}
	})
}

// docRenamed moves the tabs (all groups), recent files and per-file state
// of a document that followed a rename or move.
func (w *Workspace) docRenamed(ev api.DocRenamedEvent) {
	c := w.c
	from, to := ev.FromFile, ev.File
	if from == "" || to == "" || from == to {
		return
	}
	for _, t := range c.allTabs() {
		if t.File == from && t.Virtual == "" {
			t.File, t.Path = to, c.Rel(to)
			if ev.Language != "" {
				t.Language = ev.Language
			}
		}
	}
	for i, f := range c.RecentFiles {
		if f == from {
			c.RecentFiles[i] = to
		}
	}
	if e := w.External[from]; e != nil {
		delete(w.External, from)
		e.File, e.Path = to, c.Rel(to)
		w.External[to] = e
		w.ExternalVersion++
	}
	if b := w.Blame[from]; b != nil {
		delete(w.Blame, from)
		b.File = to
		w.Blame[to] = b
		w.BlameVersion++
	}
	w.renames = append(w.renames, Rename{From: from, To: to})
	c.SplitVersion++
}

// ---- external changes -----------------------------------------------------

func (w *Workspace) externalEvent(ev api.DocExternalEvent) {
	c := w.c
	file := ev.File
	if file == "" {
		file = c.Abs(ev.Path)
	}
	e := &ExternalChange{File: file, Path: c.Rel(file), Kind: ev.Kind, RenamedTo: ev.RenamedTo, Modified: ev.Modified, Source: ev.Source}
	if ev.Diff != nil {
		e.Added, e.Removed = ev.Diff.Added, ev.Diff.Removed
	}
	if e.Kind == "" {
		e.Kind = "modified"
	}
	w.External[file] = e
	w.ExternalVersion++
}

func (w *Workspace) clearExternal(file string) {
	if _, ok := w.External[file]; ok {
		delete(w.External, file)
		w.ExternalVersion++
	}
}

// ExternalOf returns the pending external change of an open file.
func (w *Workspace) ExternalOf(file string) *ExternalChange { return w.External[file] }

// IsExternalChange reports whether err is doc.save's refusal to
// overwrite a file changed on disk (-32004 externalChange); kind is
// modified, created or deleted.
func IsExternalChange(err error) (kind string, ok bool) {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeConflict || ae.Data == nil {
		return "", false
	}
	var d api.ExternalChangeData
	if !decodeData(ae.Data, &d) || d.Reason != "externalChange" {
		return "", false
	}
	return d.Kind, true
}

// saveFailed turns a save refused because of an external change into the
// conflict banner instead of a bare error (true: handled).
func (w *Workspace) saveFailed(file string, err error) bool {
	kind, ok := IsExternalChange(err)
	if !ok {
		return false
	}
	if w.External[file] == nil {
		w.External[file] = &ExternalChange{File: file, Path: w.c.Rel(file), Kind: kind, Modified: true, Source: "save"}
		w.ExternalVersion++
	}
	w.c.Status.Notice = w.c.Rel(file) + " changed on disk – not saved. Choose Keep mine or Take disk."
	return true
}

// External resolutions (doc.resolveExternal and friends).
const (
	ResolveKeepMine = "keepMine"
	ResolveTakeDisk = "takeDisk"
)

// ResolveExternal resolves the external change of file: keepMine writes
// the buffer over the file, takeDisk replaces the buffer with the file
// (one undo step).
func (w *Workspace) ResolveExternal(file, action string) {
	c := w.c
	e := w.External[file]
	if e != nil {
		e.Busy = true
	}
	c.Go(func(ctx context.Context) func() {
		var st DocState
		err := c.call(ctx, "doc.resolveExternal", map[string]any{"path": file, "action": action}, &st)
		return func() {
			if e != nil {
				e.Busy = false
			}
			if err != nil {
				c.notice(err)
				return
			}
			w.clearExternal(file)
			for _, t := range c.allTabs() {
				if t.File == file {
					t.Modified = st.Modified
				}
			}
			if action == ResolveKeepMine {
				c.Status.Notice = "Saved " + c.Rel(file) + " over the changed file"
			} else {
				c.Status.Notice = "Reloaded " + c.Rel(file) + " from disk"
			}
		}
	})
}

// RecreateDeleted saves a document whose file was deleted on disk
// (doc.save force creates it again).
func (w *Workspace) RecreateDeleted(file string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		var st DocState
		err := c.call(ctx, "doc.save", map[string]any{"path": file, "force": true}, &st)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			w.clearExternal(file)
			c.Status.Notice = "Saved " + c.Rel(file) + " again"
		}
	})
}

// CloseDiscard closes the document of file without saving (doc.close
// discard) and removes its tabs from all groups – for files deleted on
// disk.
func (w *Workspace) CloseDiscard(file string) {
	c := w.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "doc.close", map[string]any{"path": file, "discard": true}, nil)
		return func() {
			if err != nil && api.ErrorCode(err) != api.CodeNotFound {
				c.notice(err)
				return
			}
			w.clearExternal(file)
			w.closeTabsOf(func(f string) bool { return f == file })
		}
	})
}

// closeTabsOf removes the file tabs matching from all groups without
// calling doc.save/doc.close (the backend already closed the documents).
func (w *Workspace) closeTabsOf(match func(file string) bool) {
	c := w.c
	c.store()
	active := c.ActiveGroup()
	for gi := 0; gi < len(c.groups); gi++ {
		g := c.groups[gi]
		c.FocusGroup(gi)
		for i := len(c.Tabs) - 1; i >= 0; i-- {
			if t := c.Tabs[i]; t.Virtual == "" && match(t.File) {
				t.Doc = nil
				c.CloseTab(i)
			}
		}
		// Closing the last tab of a split removes the group.
		if gi < len(c.groups) && c.groups[gi] != g {
			gi--
		}
	}
	if i := c.GroupIndex(active); i >= 0 {
		c.FocusGroup(i)
	}
}

// Refresh compares all open documents with the disk (watch.refresh; the
// window calls it when it gains the focus). Changes arrive as events.
func (w *Workspace) Refresh() {
	c := w.c
	if !c.Project.Open {
		return
	}
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "watch.refresh", nil, nil)
		if err != nil && api.ErrorCode(err) != api.CodeMethodNotFound {
			return func() { c.notice(err) }
		}
		return nil
	})
}

// Sync reloads from disk by hand ("Reload from Disk"): the watcher
// rescans the project and open documents are compared with the disk
// (watch.refresh), then every loaded tree directory at or below rel ("" =
// the whole project) is listed again, so nothing a missed notification
// left behind stays in the tree.
func (w *Workspace) Sync(rel string) {
	c := w.c
	if !c.Project.Open {
		return
	}
	c.Status.Notice = "Reloading from disk…"
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "watch.refresh", nil, nil)
		return func() {
			if err != nil && api.ErrorCode(err) != api.CodeMethodNotFound {
				c.notice(err)
				return
			}
			n := 0
			if c.Tree != nil {
				var walk func(nd *Node)
				walk = func(nd *Node) {
					if !nd.Dir || !nd.Loaded {
						return
					}
					if underAny(nd.Path, []string{rel}) || rel == "" {
						w.reloadDir(nd.Path)
						n++
					}
					for _, ch := range nd.Children {
						walk(ch)
					}
				}
				if root := c.Tree.Node(""); root != nil {
					walk(root)
				}
			}
			c.Files = nil // quick-open index
			c.refreshGit()
			where := "the project"
			if rel != "" {
				where = rel
			}
			folders := "1 folder"
			if n != 1 {
				folders = strconv.Itoa(n) + " folders"
			}
			c.Status.Notice = "Reloaded " + where + " from disk (" + folders + ")"
		}
	})
}

// underAny reports whether the project-relative path p is one of roots
// or inside one of them.
func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || r == "" || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
