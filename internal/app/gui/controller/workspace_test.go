package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// ---- file operations ---------------------------------------------------------

func TestFileOpsCreateRenameAndErrors(t *testing.T) {
	f := projectFake()
	f.on("files.createFile", func(raw json.RawMessage) (any, error) {
		var p struct{ Path string }
		_ = json.Unmarshal(raw, &p)
		if p.Path == "src/Exists.php" {
			return nil, api.Errorf(api.CodeConflict, "src/Exists.php exists")
		}
		return map[string]any{"path": p.Path, "file": filepath.Join(root, filepath.FromSlash(p.Path))}, nil
	})
	f.answer("files.createDir", map[string]any{"path": "src/Sub", "dir": true})
	f.answer("files.rename", map[string]any{"path": "src/Renamed.php"})
	f.answer("files.phpNamespace", PHPNamespace{Dir: "src", Namespace: `App`, Found: true})
	f.answer("files.createPhpClass", map[string]any{"path": "src/Foo.php", "file": filepath.Join(root, "src/Foo.php"), "class": `App\Foo`})
	c := started(t, f)
	w := c.Work

	if got := w.DirOf("src/Mailer.php"); got != "src" {
		c.LoadDir("src")
		c.Settle(time.Second)
	}
	if got := w.DirOf("src"); got != "src" {
		t.Fatalf("DirOf dir %q", got)
	}
	if got := w.DirOf("index.php"); got != "" {
		t.Fatalf("DirOf root file %q", got)
	}

	w.CreateFile("src", " New.php ")
	waitFor(t, c, "new file tab", func() bool { return c.Tab(filepath.Join(root, "src/New.php")) != nil })
	var cp struct{ Path string }
	lastParams(t, f, "files.createFile", &cp)
	if cp.Path != "src/New.php" {
		t.Fatalf("createFile path %q", cp.Path)
	}

	w.CreateFile("src", "Exists.php")
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "Already exists") {
		t.Fatalf("conflict notice %q", c.Status.Notice)
	}

	w.CreateDir("src", "Sub/")
	c.Settle(time.Second)
	if c.Status.Notice != "Created src/Sub" {
		t.Fatalf("dir notice %q", c.Status.Notice)
	}

	var ns PHPNamespace
	w.LoadNamespace("src", func(n PHPNamespace) { ns = n })
	c.Settle(time.Second)
	if ns.Namespace != "App" {
		t.Fatalf("namespace %+v", ns)
	}
	w.CreatePHPClass("src", "Foo.php", "interface", nil)
	waitFor(t, c, "class tab", func() bool { return c.Tab(filepath.Join(root, "src/Foo.php")) != nil })
	var pc map[string]any
	lastParams(t, f, "files.createPhpClass", &pc)
	if pc["name"] != "Foo" || pc["kind"] != "interface" || pc["dir"] != "src" {
		t.Fatalf("createPhpClass %v", pc)
	}
	if _, ok := pc["namespace"]; ok {
		t.Fatalf("namespace must come from composer.json: %v", pc)
	}

	w.Rename("src/Mailer.php", "Renamed.php")
	w.Rename("src/Mailer.php", "Mailer.php") // unchanged: no call
	c.Settle(time.Second)
	w.Rename("index.php", "public/index.php")
	c.Settle(time.Second)
	if n := f.called("files.rename"); n != 2 {
		t.Fatalf("rename calls %d", n)
	}
	var rp map[string]any
	lastParams(t, f, "files.rename", &rp)
	if rp["to"] != "public/index.php" || rp["newName"] != nil {
		t.Fatalf("move by rename %v", rp)
	}
}

func TestDeleteTrashFallbackAndClosesTabs(t *testing.T) {
	f := projectFake()
	f.on("files.delete", func(raw json.RawMessage) (any, error) {
		var p struct {
			Paths     []string
			Permanent bool
		}
		_ = json.Unmarshal(raw, &p)
		if !p.Permanent {
			return nil, api.Errorf(api.CodeUnavailable, "no trash on this file system")
		}
		return map[string]any{"deleted": p.Paths, "trashed": false, "closed": []string{"src/Mailer.php"}}, nil
	})
	c := started(t, f)
	c.OpenFile("src/Mailer.php", nil)
	c.OpenFile("index.php", nil)
	waitFor(t, c, "tabs", func() bool { return len(c.Tabs) == 2 })
	c.Tab(filepath.Join(root, "src/Mailer.php")).Modified = true // saved before deleting

	var res DeleteResult
	c.Work.Delete([]string{"src"}, false, func(r DeleteResult) { res = r })
	c.Settle(time.Second)
	if !res.NoTrash || len(c.Tabs) != 2 {
		t.Fatalf("no trash: %+v tabs %d", res, len(c.Tabs))
	}
	if f.called("doc.save") == 0 {
		t.Fatal("modified document was not saved before deleting")
	}
	closes := f.called("doc.close")
	c.Work.Delete([]string{"src"}, true, nil)
	c.Settle(time.Second)
	if len(c.Tabs) != 1 || c.Tabs[0].Path != "index.php" {
		t.Fatalf("tabs after delete %+v", c.Tabs)
	}
	if f.called("doc.close") != closes {
		t.Fatal("the backend already closed the document; no doc.close expected")
	}
	if !strings.Contains(c.Status.Notice, "permanently") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
}

func TestClipboardPasteAndPaths(t *testing.T) {
	f := projectFake()
	f.answer("files.copy", map[string]any{"results": []map[string]any{{"path": "src/index.php"}}})
	f.answer("files.move", map[string]any{"results": []map[string]any{{"path": "src/index.php"}}})
	c := started(t, f)
	w := c.Work
	w.Paste("src") // empty clipboard: nothing
	w.CopyToClip([]string{"index.php"})
	w.Paste("src")
	c.Settle(time.Second)
	if f.called("files.copy") != 1 || len(w.Clip.Paths) != 1 {
		t.Fatalf("copy %d clip %+v", f.called("files.copy"), w.Clip)
	}
	w.CutToClip([]string{"src"})
	w.Paste("src/sub") // into itself
	c.Settle(100 * time.Millisecond)
	if f.called("files.move") != 0 || !strings.Contains(c.Status.Notice, "into itself") {
		t.Fatalf("move into itself: %q", c.Status.Notice)
	}
	w.CutToClip([]string{"index.php"})
	w.Paste("src")
	c.Settle(time.Second)
	var mp struct {
		Paths []string
		To    string
	}
	lastParams(t, f, "files.move", &mp)
	if mp.To != "src" || len(mp.Paths) != 1 || len(w.Clip.Paths) != 0 {
		t.Fatalf("move %+v clip %+v", mp, w.Clip)
	}
	if got := w.PathText([]string{"src/Mailer.php", ""}, PathAbsolute); got != filepath.Join(root, "src/Mailer.php")+"\n"+root {
		t.Fatalf("absolute %q", got)
	}
	if got := w.PathText([]string{"src/Mailer.php"}, PathName); got != "Mailer.php" {
		t.Fatalf("name %q", got)
	}
	if got := w.PathText([]string{"src/Mailer.php"}, PathRelative); got != "src/Mailer.php" {
		t.Fatalf("relative %q", got)
	}
}

// ---- events: tree refresh, renames ------------------------------------------------

func TestTreeFollowsFilesChanged(t *testing.T) {
	f := projectFake()
	srcChildren := []*FileNode{{Name: "Mailer.php", Path: "src/Mailer.php"}}
	f.on("files.list", func(raw json.RawMessage) (any, error) {
		var p struct{ Path string }
		_ = json.Unmarshal(raw, &p)
		switch p.Path {
		case "":
			return filesListResult{Tree: &FileNode{Dir: true, Children: []*FileNode{
				{Name: "index.php", Path: "index.php"}, {Name: "src", Path: "src", Dir: true, Collapsed: true}}}}, nil
		case "src":
			f.mu.Lock()
			ch := srcChildren
			f.mu.Unlock()
			return filesListResult{Tree: &FileNode{Dir: true, Children: ch}}, nil
		}
		return nil, api.Errorf(api.CodeNotFound, "nope")
	})
	c := started(t, f)
	c.LoadDir("src")
	c.Settle(time.Second)
	lists := f.called("files.list")
	gits := f.called("git.status")

	// A watcher change of file content does not reload directories.
	f.emit(api.TopicFilesChanged, api.FilesChangedEvent{Changes: []api.FileChange{{Kind: api.FileModified, Path: "src/Mailer.php", Source: api.FileSourceWatcher}}})
	c.Settle(time.Second)
	if f.called("files.list") != lists {
		t.Fatal("modified file reloaded the tree")
	}
	if f.called("git.status") == gits {
		t.Fatal("git status not refreshed")
	}

	f.mu.Lock()
	srcChildren = append(srcChildren, &FileNode{Name: "Mail.php", Path: "src/Mail.php"})
	f.mu.Unlock()
	f.emit(api.TopicFilesChanged, api.FilesChangedEvent{Changes: []api.FileChange{
		{Kind: api.FileCreated, Path: "src/Mail.php", Source: api.FileSourceWatcher},
		{Kind: api.FileDeleted, Path: "deep/unknown/x.php", Source: api.FileSourceWatcher},
	}})
	waitFor(t, c, "new tree entry", func() bool { return c.Tree.Node("src/Mail.php") != nil })
	if n := c.Tree.Node("src"); len(n.Children) != 2 {
		t.Fatalf("src children %d", len(n.Children))
	}
}

func TestTabsFollowDocRenamed(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	c.OpenFile("src/Mailer.php", nil)
	waitFor(t, c, "tab", func() bool { return len(c.Tabs) == 1 })
	c.Split(false) // the file is open in two groups
	from, to := filepath.Join(root, "src/Mailer.php"), filepath.Join(root, "lib/Mail.php")
	f.emit(api.TopicDocExternalConflict, api.DocExternalEvent{Path: "src/Mailer.php", File: from, Kind: "modified", Modified: true})
	c.Settle(200 * time.Millisecond)
	f.emit(api.TopicDocRenamed, api.DocRenamedEvent{From: "src/Mailer.php", To: "lib/Mail.php", FromFile: from, File: to, Language: "php"})
	c.Settle(200 * time.Millisecond)
	for _, tab := range c.allTabs() {
		if tab.File != to || tab.Path != "lib/Mail.php" {
			t.Fatalf("tab not renamed: %+v", tab)
		}
	}
	if c.Work.ExternalOf(to) == nil || c.Work.ExternalOf(from) != nil {
		t.Fatal("external state did not follow")
	}
	rn := c.Work.TakeRenames()
	if len(rn) != 1 || rn[0] != (Rename{From: from, To: to}) || len(c.Work.TakeRenames()) != 0 {
		t.Fatalf("renames %+v", rn)
	}
	if c.RecentFiles[0] != to {
		t.Fatalf("recent %v", c.RecentFiles)
	}
}

// ---- external changes -----------------------------------------------------------

func TestExternalConflictFlow(t *testing.T) {
	f := projectFake()
	var actions []string
	f.on("doc.resolveExternal", func(raw json.RawMessage) (any, error) {
		var p struct{ Path, Action string }
		_ = json.Unmarshal(raw, &p)
		actions = append(actions, p.Action)
		return DocState{Path: p.Path, Modified: p.Action == ResolveTakeDisk && false}, nil
	})
	f.on("doc.save", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeConflict, "changed on disk").WithData(api.ExternalChangeData{Reason: "externalChange", Path: "src/Mailer.php", Kind: "modified"})
	})
	c := started(t, f)
	file := filepath.Join(root, "src/Mailer.php")
	c.OpenFile(file, nil)
	waitFor(t, c, "tab", func() bool { return len(c.Tabs) == 1 })
	c.Tabs[0].Modified = true

	// An explicit save that is refused starts the conflict flow.
	c.Save(file)
	c.Settle(time.Second)
	e := c.Work.ExternalOf(file)
	if e == nil || e.Kind != "modified" || e.Source != "save" || !strings.Contains(c.Status.Notice, "Keep mine") {
		t.Fatalf("save conflict %+v notice %q", e, c.Status.Notice)
	}
	c.Work.ResolveExternal(file, ResolveKeepMine)
	c.Settle(time.Second)
	if c.Work.ExternalOf(file) != nil || c.Tabs[0].Modified {
		t.Fatalf("keepMine did not clear: %+v", c.Work.ExternalOf(file))
	}

	// A watcher conflict with a diff summary, resolved by taking the disk.
	f.emit(api.TopicDocExternalConflict, api.DocExternalEvent{Path: "src/Mailer.php", File: file, Kind: "modified", Modified: true, Source: "watcher",
		Diff: &api.ExternalDiff{Added: 2, Removed: 1}})
	c.Settle(200 * time.Millisecond)
	if e := c.Work.ExternalOf(file); e == nil || e.Added != 2 || e.Removed != 1 {
		t.Fatalf("watcher conflict %+v", e)
	}
	c.Work.ResolveExternal(file, ResolveTakeDisk)
	c.Settle(time.Second)
	if c.Work.ExternalOf(file) != nil || strings.Join(actions, ",") != "keepMine,takeDisk" {
		t.Fatalf("takeDisk: %v", actions)
	}

	// Deleted on disk: banner until the document is reloaded or closed.
	f.emit(api.TopicDocDeletedOnDisk, api.DocExternalEvent{Path: "src/Mailer.php", File: file, Kind: "deleted", RenamedTo: "lib/Mailer.php"})
	c.Settle(200 * time.Millisecond)
	if e := c.Work.ExternalOf(file); e == nil || !e.Deleted() || e.RenamedTo != "lib/Mailer.php" {
		t.Fatalf("deleted %+v", e)
	}
	c.Work.CloseDiscard(file)
	c.Settle(time.Second)
	if len(c.Tabs) != 0 || c.Work.ExternalOf(file) != nil {
		t.Fatalf("close discard: tabs %d", len(c.Tabs))
	}
	var cl map[string]any
	lastParams(t, f, "doc.close", &cl)
	if cl["discard"] != true {
		t.Fatalf("doc.close %v", cl)
	}

	// doc.reloaded clears a pending state silently.
	f.emit(api.TopicDocExternalConflict, api.DocExternalEvent{Path: "index.php", File: filepath.Join(root, "index.php"), Kind: "modified"})
	c.Settle(200 * time.Millisecond)
	f.emit(api.TopicDocReloaded, map[string]any{"path": "index.php", "source": "watcher"})
	c.Settle(200 * time.Millisecond)
	if c.Work.ExternalOf(filepath.Join(root, "index.php")) != nil {
		t.Fatal("doc.reloaded did not clear")
	}

	f.answer("watch.refresh", map[string]any{"external": []any{}})
	c.Work.Refresh()
	c.Settle(time.Second)
	if f.called("watch.refresh") != 1 {
		t.Fatal("watch.refresh not called")
	}
}

// ---- diff tabs ---------------------------------------------------------------------

func TestDiffTabLifecycle(t *testing.T) {
	f := projectFake()
	f.on("git.fileDiff", func(raw json.RawMessage) (any, error) {
		return map[string]any{"path": "src/Mailer.php", "left": map[string]any{"version": "HEAD", "text": "a\n", "exists": true},
			"right":  map[string]any{"version": "working", "text": "b\n", "exists": true},
			"blocks": []map[string]any{{"kind": "modified", "leftStart": 1, "leftEnd": 2, "rightStart": 1, "rightEnd": 2}}}, nil
	})
	f.on("doc.open", func(raw json.RawMessage) (any, error) {
		var p pathParams
		_ = json.Unmarshal(raw, &p)
		f.mu.Lock()
		f.docs[p.Path] = &fakeDoc{path: p.Path, buf: nil}
		f.mu.Unlock()
		return map[string]any{"path": p.Path, "generation": 7}, nil
	})
	c := started(t, f)
	spec := DiffSpec{Path: "src/Mailer.php", Base: VersionHead}
	dt := c.Work.OpenDiff(spec)
	waitFor(t, c, "diff loaded", func() bool { return dt.Version > 0 })
	tab := c.ActiveTab()
	if tab == nil || tab.Virtual != TabDiff || tab.Data != dt || !strings.Contains(tab.Title(), "HEAD ↔ local") {
		t.Fatalf("tab %+v", tab)
	}
	if dt.Doc == nil || dt.ownGen != 7 || !dt.Editable() || dt.Result == nil {
		t.Fatalf("diff tab %+v", dt)
	}
	var p map[string]any
	lastParams(t, f, "git.fileDiff", &p)
	if p["base"] != "HEAD" || p["target"] != "working" || p["path"] != filepath.Join(root, "src/Mailer.php") {
		t.Fatalf("fileDiff params %v", p)
	}

	// Reopening the same comparison activates the tab; white space reloads.
	c.OpenFile("index.php", nil)
	waitFor(t, c, "file tab", func() bool { return len(c.Tabs) == 2 })
	if again := c.Work.OpenDiff(spec); again != dt || c.ActiveTab().Data != dt || len(c.Tabs) != 2 {
		t.Fatal("same diff opened twice")
	}
	c.Settle(time.Second)
	c.Work.SetIgnoreWhitespace(dt, true)
	c.Settle(time.Second)
	lastParams(t, f, "git.fileDiff", &p)
	if p["ignoreWhitespace"] != true {
		t.Fatalf("ignore whitespace %v", p)
	}

	// Closing the tab closes the document the diff opened itself.
	closes := f.called("doc.close")
	c.CloseTab(c.Active)
	c.Work.CloseDiff(dt)
	c.Settle(time.Second)
	if f.called("doc.close") != closes+1 {
		t.Fatal("own document not closed")
	}
	lastParams(t, f, "doc.close", &p)
	if p["generation"] != float64(7) {
		t.Fatalf("doc.close %v", p)
	}
	if len(c.Work.Diffs()) != 0 {
		t.Fatal("diff not forgotten")
	}

	// A commit file diff compares with the parent (or the empty file).
	c.Work.CommitFileDiff("abcdef0123456789abcdef0123456789abcdef01", []string{"p1"}, "src/Mailer.php", "src/Old.php", false)
	c.Settle(time.Second)
	lastParams(t, f, "git.fileDiff", &p)
	if p["base"] != "p1" || p["target"] != "abcdef0123456789abcdef0123456789abcdef01" || p["basePath"] != "src/Old.php" {
		t.Fatalf("commit diff %v", p)
	}
	c.Work.CommitFileDiff("abc", nil, "src/New.php", "", true)
	c.Settle(time.Second)
	lastParams(t, f, "git.fileDiff", &p)
	if p["base"] != "empty" {
		t.Fatalf("root commit %v", p)
	}
}

func TestAnnotateAndHistories(t *testing.T) {
	old := BlameDelay
	BlameDelay = 10 * time.Millisecond
	t.Cleanup(func() { BlameDelay = old })
	f := projectFake()
	f.answer("git.blame", map[string]any{"lines": []map[string]any{{"line": 1, "hash": "h1"}},
		"commits": []map[string]any{{"hash": "h1", "author": "Ada", "path": "src/Mailer.php", "previous": "h0", "prevPath": "src/Old.php"}}})
	f.answer("git.fileDiff", map[string]any{"path": "src/Mailer.php"})
	f.answer("git.log", []any{})
	f.answer("git.diff", map[string]any{"raw": ""})
	f.on("git.fileHistory", func(raw json.RawMessage) (any, error) {
		var p struct{ Skip int }
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"commits": []map[string]any{{"hash": "c" + fmtInt(p.Skip), "status": "M", "path": "src/Mailer.php"}}, "more": p.Skip == 0}, nil
	})
	f.answer("history.list", []LocalRevision{{ID: 3, Label: "autosave"}, {ID: 2}})
	f.answer("history.content", map[string]any{"text": "old"})
	f.answer("files.read", map[string]any{"content": "new"})
	f.answer("history.restore", map[string]any{})
	c := started(t, f)
	file := filepath.Join(root, "src/Mailer.php")

	c.Work.ToggleAnnotate(file)
	waitFor(t, c, "blame", func() bool { return c.Work.Blame[file] != nil && c.Work.Blame[file].Result != nil })
	blames := f.called("git.blame")
	f.emit(api.TopicDocEdited, map[string]any{"file": file})
	waitFor(t, c, "blame after edit", func() bool { return f.called("git.blame") > blames })
	c.Work.ShowBlameCommit(file, "h1")
	c.Settle(time.Second)
	var p map[string]any
	lastParams(t, f, "git.fileDiff", &p)
	if p["base"] != "h0" || p["target"] != "h1" || p["basePath"] != "src/Old.php" {
		t.Fatalf("blame commit diff %v", p)
	}
	c.Work.ToggleAnnotate(file)
	if c.Work.Annotated(file) {
		t.Fatal("annotation not switched off")
	}

	ht := c.Work.OpenFileHistory("src/Mailer.php")
	waitFor(t, c, "history", func() bool { return len(ht.Entries) == 1 })
	c.Work.MoreHistory(ht)
	waitFor(t, c, "page 2", func() bool { return len(ht.Entries) == 2 })
	if ht.More || ht.Entries[1].Hash != "c1" || ht.Entries[0].Status != 'M' {
		t.Fatalf("history %+v", ht)
	}
	c.Work.HistoryDiff(ht, "c0", "p", "src/Mailer.php", "src/Old.php")
	waitFor(t, c, "history diff", func() bool { return ht.DiffVersion > 0 })

	lt := c.Work.OpenLocalHistory("src/Mailer.php", 0)
	waitFor(t, c, "local history", func() bool { return lt.DiffVersion > 0 })
	if lt.Selected != 3 || lt.RevText != "old" || lt.Current != "new" || RevisionLabel(lt.Revisions[0]) != "#3  autosave" {
		t.Fatalf("local history %+v", lt)
	}
	c.Work.RevertToLocalRevision(lt, 2)
	c.Settle(time.Second)
	var rp map[string]any
	lastParams(t, f, "history.restore", &rp)
	if rp["id"] != float64(2) || !strings.Contains(c.Status.Notice, "#2") {
		t.Fatalf("restore %v %q", rp, c.Status.Notice)
	}
}

// ---- remote and stash ---------------------------------------------------------------

func TestRemoteErrorsAreReadable(t *testing.T) {
	for kind, want := range map[string]string{
		"auth": "authentication", "network": "network", "diverged": "Rebase", "localChanges": "Autostash",
		"noUpstream": "upstream", "detached": "detached", "noRemote": "no remote", "repoNotFound": "not found",
	} {
		err := api.Errorf(api.CodeFailed, "pull failed").WithData(map[string]string{"kind": kind, "message": "m", "stderr": "fatal: x"})
		p := DescribeRemoteError("pull", err)
		if p.Kind != kind || !strings.Contains(p.Title, want) || !strings.Contains(p.Detail, "fatal: x") {
			t.Errorf("%s: %+v", kind, p)
		}
	}
	p := DescribeRemoteError("stash pop", api.Errorf(api.CodeConflict, "stale").WithData(map[string]string{"reason": "staleStash"}))
	if p.Kind != "staleStash" || !strings.Contains(p.Title, "reloaded") {
		t.Errorf("stale %+v", p)
	}
	if p := DescribeRemoteError("fetch", api.Errorf(api.CodeFailed, "boom")); p.Kind != "failed" || p.Title != "Fetch failed." {
		t.Errorf("plain %+v", p)
	}
}

func TestPullUpdateAndStashFlows(t *testing.T) {
	f := projectFake()
	f.answer("git.log", []any{})
	f.answer("git.branches", []any{})
	f.answer("git.diff", map[string]any{"raw": ""})
	f.on("git.pull", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeFailed, "pull failed").WithData(map[string]string{"kind": "auth", "message": "denied"})
	})
	f.answer("git.updateProject", map[string]any{"branch": "main", "remote": "origin", "remoteBranch": "main", "changed": []string{"a"}, "conflicts": []string{"src/Mailer.php"}})
	f.answer("git.fetch", map[string]any{"remotes": []string{"origin"}, "updated": []any{map[string]any{"ref": "refs/remotes/origin/main"}}})
	stashes := []StashEntry{{Index: 0, Ref: "stash@{0}", Hash: "s0", Message: "WIP"}, {Index: 1, Ref: "stash@{1}", Hash: "s1", Message: "old"}}
	f.answer("git.stashList", stashes)
	f.answer("git.stash", map[string]any{"created": true})
	f.answer("git.stashPop", map[string]any{"conflicts": []string{"index.php"}, "dropped": false})
	f.answer("git.stashDrop", stashes[1:])
	c := started(t, f)
	w := c.Work

	w.Fetch()
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "Fetched 1") || w.Remote.Busy != "" {
		t.Fatalf("fetch %q", c.Status.Notice)
	}
	w.Pull(PullOptions{Strategy: "rebase", Autostash: true})
	c.Settle(time.Second)
	var pp map[string]any
	lastParams(t, f, "git.pull", &pp)
	if pp["strategy"] != "rebase" || pp["autostash"] != true {
		t.Fatalf("pull params %v", pp)
	}
	if w.Remote.Problem == nil || w.Remote.Problem.Kind != "auth" || w.Remote.Pull.Strategy != "rebase" {
		t.Fatalf("pull problem %+v", w.Remote.Problem)
	}
	w.UpdateProject(PullOptions{Strategy: "merge"})
	c.Settle(time.Second)
	if w.Remote.Problem != nil || w.Remote.ConflictVersion != 1 || w.Remote.Conflicts[0] != "src/Mailer.php" {
		t.Fatalf("update conflicts %+v", w.Remote)
	}

	w.Stash(StashOptions{Message: " wip ", IncludeUntracked: true})
	c.Settle(time.Second)
	var sp map[string]any
	lastParams(t, f, "git.stash", &sp)
	if sp["message"] != "wip" || sp["includeUntracked"] != true || len(w.Remote.Stashes) != 2 {
		t.Fatalf("stash %v list %d", sp, len(w.Remote.Stashes))
	}
	w.StashApply(stashes[0], true)
	c.Settle(time.Second)
	lastParams(t, f, "git.stashPop", &sp)
	if sp["hash"] != "s0" || w.Remote.ConflictVersion != 2 || !strings.Contains(c.Status.Notice, "kept") {
		t.Fatalf("pop %v %q", sp, c.Status.Notice)
	}
	w.StashDrop(stashes[0])
	c.Settle(time.Second)
	if len(w.Remote.Stashes) != 1 || w.Remote.Stashes[0].Hash != "s1" {
		t.Fatalf("drop %+v", w.Remote.Stashes)
	}
}
