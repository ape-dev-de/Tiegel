package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/trash"
)

// evRecorder collects bus events.
type evRecorder struct {
	mu  sync.Mutex
	evs []bus.Event
}

func recordEvents(t *testing.T, a *App, pattern string) *evRecorder {
	r := &evRecorder{}
	t.Cleanup(a.Bus().Subscribe(pattern, func(e bus.Event) {
		r.mu.Lock()
		r.evs = append(r.evs, e)
		r.mu.Unlock()
	}))
	return r
}

// take drains the bus and returns (and forgets) the recorded events.
func (r *evRecorder) take(t *testing.T, a *App) []bus.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Bus().Drain(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.evs
	r.evs = nil
	return out
}

func topics(evs []bus.Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Topic)
	}
	return strings.Join(s, ",")
}

func changesOf(t *testing.T, evs []bus.Event) []FileChange {
	t.Helper()
	var out []FileChange
	for _, e := range evs {
		if ev, ok := e.Payload.(FilesChangedEvent); ok {
			out = append(out, ev.Changes...)
		}
	}
	return out
}

func fileProject(t *testing.T, files map[string]string) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return a, a.ProjectRoot()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists (%v)", path, err)
	}
}

func TestFilesNeedProject(t *testing.T) {
	a := newTestApp(t)
	for m, p := range map[string]any{
		"files.createFile": CreateFileParams{Path: "x"}, "files.createDir": PathParams{Path: "x"},
		"files.rename": RenameParams{Path: "x", NewName: "y"}, "files.delete": DeleteParams{Path: "x"},
		"files.reveal": PathParams{Path: "x"}, "files.createPhpClass": PHPClassParams{Name: "X"},
		"files.phpNamespace": PathParams{Path: "x"},
	} {
		wantCode(t, a, m, p, CodeNoProject)
	}
	wantCode(t, a, "files.move", TransferParams{Path: "x", To: "y"}, CodeNoProject)
}

func TestFilesCreate(t *testing.T) {
	a, root := fileProject(t, map[string]string{"a.php": "<?php\n"})
	rec := recordEvents(t, a, "files.*")
	var res FileOpResult
	call(t, a, "files.createFile", CreateFileParams{Path: "src/Deep/new.php", Content: "<?php // new\n", Open: true}, &res)
	if res.Path != "src/Deep/new.php" || res.Doc == nil || res.Doc.Path != "src/Deep/new.php" {
		t.Fatalf("result %+v", res)
	}
	if readFile(t, filepath.Join(root, "src", "Deep", "new.php")) != "<?php // new\n" {
		t.Fatal("content")
	}
	ch := changesOf(t, rec.take(t, a))
	if len(ch) != 3 || ch[0].Path != "src" || !ch[0].Dir || ch[1].Path != "src/Deep" || ch[2].Path != "src/Deep/new.php" || ch[2].Op != "created" {
		t.Fatalf("changes %+v", ch)
	}
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "a.php"}, CodeConflict)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "../escape.php"}, CodeInvalidParams)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "src/../../escape.php"}, CodeInvalidParams)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: filepath.Join(t.TempDir(), "abs.php")}, CodeInvalidParams)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: ""}, CodeInvalidParams)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "."}, CodeInvalidParams)
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "a\x00b"}, CodeInvalidParams)
	if runtime.GOOS == "windows" {
		wantCode(t, a, "files.createFile", CreateFileParams{Path: "con.txt"}, CodeInvalidParams)
	}
	gone(t, filepath.Join(filepath.Dir(root), "escape.php"))

	call(t, a, "files.createDir", PathParams{Path: "lib/x"}, &res)
	if !res.Dir || res.Path != "lib/x" {
		t.Fatalf("dir %+v", res)
	}
	if st, err := os.Stat(filepath.Join(root, "lib", "x")); err != nil || !st.IsDir() {
		t.Fatal("dir not created")
	}
	if ch := changesOf(t, rec.take(t, a)); len(ch) != 2 || ch[1].Path != "lib/x" {
		t.Fatalf("dir changes %+v", ch)
	}
	wantCode(t, a, "files.createDir", PathParams{Path: "lib/x"}, CodeConflict)
	wantCode(t, a, "files.createDir", PathParams{Path: "../out"}, CodeInvalidParams)
	// Absolute paths inside the project are fine.
	call(t, a, "files.createFile", CreateFileParams{Path: filepath.Join(root, "abs.txt")}, &res)
	if res.Path != "abs.txt" {
		t.Fatalf("abs %+v", res)
	}
}

func TestFilesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	a, root := fileProject(t, nil)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a, "files.createFile", CreateFileParams{Path: "link/x.php"}, CodeInvalidParams)
	wantCode(t, a, "files.createDir", PathParams{Path: "link/a/b"}, CodeInvalidParams)
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote outside the project")
	}
	// The link itself may be renamed or deleted (not its target).
	a.trash = os.RemoveAll
	call(t, a, "files.delete", DeleteParams{Path: "link"}, nil)
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("link target removed")
	}
}

func TestFilesRenameOpenDocuments(t *testing.T) {
	a, root := fileProject(t, map[string]string{
		"src/A.php":     "<?php class A {}\n",
		"src/sub/B.php": "<?php class B {}\n",
		"other.txt":     "text",
	})
	ctx := context.Background()
	call(t, a, "doc.open", DocOpenParams{Path: "src/A.php"}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "src/sub/B.php"}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "other.txt"}, nil)
	// A saved revision in Local History moves with the file.
	call(t, a, "doc.replace", DocReplaceParams{Path: "src/A.php", Text: "<?php class A { }\n"}, nil)
	call(t, a, "doc.save", PathParams{Path: "src/A.php"}, nil)
	// Unsaved changes are carried along (no data loss, no save).
	call(t, a, "doc.insert", DocInsertParams{Path: "src/sub/B.php", At: Pos{Line: 2}, Text: "// unsaved\n"}, nil)
	before, _ := a.Document("src/A.php")
	gen := before.Generation()

	rec := recordEvents(t, a, "*")
	var res FileOpResult
	call(t, a, "files.rename", RenameParams{Path: "src/A.php", NewName: "Renamed.php"}, &res)
	if res.Path != "src/Renamed.php" || res.From != "src/A.php" || len(res.Docs) != 1 || res.Docs[0] != "src/Renamed.php" {
		t.Fatalf("rename %+v", res)
	}
	evs := rec.take(t, a)
	var docTopics []string
	for _, e := range evs {
		if strings.HasPrefix(e.Topic, "doc.") || e.Topic == TopicFilesChanged {
			docTopics = append(docTopics, e.Topic)
		}
	}
	if got := strings.Join(docTopics, ","); got != "doc.renamed,doc.closed,doc.opened,files.changed" {
		t.Fatalf("events %s", got)
	}
	for _, e := range evs {
		switch p := e.Payload.(type) {
		case DocRenamedEvent:
			if p.From != "src/A.php" || p.To != "src/Renamed.php" || p.Generation != gen || p.Language != "php" {
				t.Fatalf("renamed %+v", p)
			}
		case DocCloseResult:
			if p.Path != "src/A.php" || p.RenamedTo != "src/Renamed.php" {
				t.Fatalf("closed %+v", p)
			}
		}
	}
	d, err := a.Document("src/Renamed.php")
	if err != nil || d != before || d.Path() != filepath.Join(root, "src", "Renamed.php") || d.Generation() != gen {
		t.Fatalf("document not re-homed: %v", err)
	}
	if _, err := a.Document("src/A.php"); ErrorCode(err) != CodeNotFound {
		t.Fatal("old path still open")
	}
	var revs []struct{ ID uint64 }
	call(t, a, "history.list", PathParams{Path: "src/Renamed.php"}, &revs)
	if len(revs) == 0 {
		t.Fatal("history not moved")
	}
	// Editing and saving continue on the new file.
	call(t, a, "doc.insert", DocInsertParams{Path: "src/Renamed.php", At: Pos{Offset: 0}, Text: "#"}, nil)
	call(t, a, "doc.save", PathParams{Path: "src/Renamed.php"}, nil)
	if !strings.HasPrefix(readFile(t, filepath.Join(root, "src", "Renamed.php")), "#<?php") {
		t.Fatal("save went elsewhere")
	}
	gone(t, filepath.Join(root, "src", "A.php"))

	// Renaming a directory moves the modified document below it; the
	// language follows the new extension.
	call(t, a, "files.rename", RenameParams{Path: "src", To: "lib/source"}, &res)
	if len(res.Docs) != 2 || !res.Dir {
		t.Fatalf("dir rename %+v", res)
	}
	var st DocState
	b, err := a.Document("lib/source/sub/B.php")
	if err != nil {
		t.Fatal(err)
	}
	if snap := b.Snapshot(); !snap.Modified || !strings.Contains(string(snap.Text), "// unsaved") {
		t.Fatal("unsaved changes lost")
	}
	call(t, a, "files.rename", RenameParams{Path: "other.txt", NewName: "other.php"}, nil)
	call(t, a, "doc.text", DocTextParams{Path: "other.php"}, nil)
	o, _ := a.Document("other.php")
	if o.Language() != "php" {
		t.Fatalf("language %q", o.Language())
	}
	_ = st
	_ = ctx

	// Errors.
	writeFile(t, filepath.Join(root, "x.txt"), "x")
	writeFile(t, filepath.Join(root, "y.txt"), "y")
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "y.txt"}, CodeConflict)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "a", To: "b"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "a/b"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: ".."}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", To: "../x.txt"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "missing", NewName: "z"}, CodeNotFound)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "x.txt"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: "lib", To: "lib/inner"}, CodeInvalidParams)
	wantCode(t, a, "files.rename", RenameParams{Path: ".", NewName: "z"}, CodeInvalidParams)
	// Overwrite replaces a file, closing its (unmodified) document; a
	// modified target blocks.
	call(t, a, "doc.open", DocOpenParams{Path: "y.txt"}, nil)
	call(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "y.txt", Overwrite: true}, nil)
	if readFile(t, filepath.Join(root, "y.txt")) != "x" {
		t.Fatal("not overwritten")
	}
	if _, err := a.Document("y.txt"); err != nil {
		// y.txt was closed; nothing re-opened it
	} else {
		t.Fatal("overwritten document still open")
	}
	writeFile(t, filepath.Join(root, "x.txt"), "x2")
	call(t, a, "doc.open", DocOpenParams{Path: "y.txt"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "y.txt", At: Pos{Offset: 0}, Text: "dirty"}, nil)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", NewName: "y.txt", Overwrite: true}, CodeConflict)
	wantCode(t, a, "files.rename", RenameParams{Path: "x.txt", To: "lib", Overwrite: true}, CodeConflict)
}

func TestFilesRenameCaseOnly(t *testing.T) {
	a, root := fileProject(t, map[string]string{"user.php": "<?php"})
	call(t, a, "doc.open", DocOpenParams{Path: "user.php"}, nil)
	call(t, a, "files.rename", RenameParams{Path: "user.php", NewName: "User.php"}, nil)
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "User.php" {
		t.Fatalf("entries %v", entries)
	}
	if _, err := a.Document("User.php"); err != nil {
		t.Fatal(err)
	}
}

func TestFilesMoveCopy(t *testing.T) {
	a, root := fileProject(t, map[string]string{
		"a.php": "a", "b.php": "b", "dir/c.php": "c", "dir/sub/d.php": "d", "target/.keep": "",
	})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	rec := recordEvents(t, a, TopicFilesChanged)
	var res FilesOpResult
	call(t, a, "files.move", TransferParams{Paths: []string{"a.php", "dir"}, To: "target"}, &res)
	if len(res.Results) != 2 || res.Results[0].Path != "target/a.php" || res.Results[1].Path != "target/dir" || len(res.Results[0].Docs) != 1 {
		t.Fatalf("move %+v", res)
	}
	if readFile(t, filepath.Join(root, "target", "dir", "sub", "d.php")) != "d" {
		t.Fatal("tree not moved")
	}
	if _, err := a.Document("target/a.php"); err != nil {
		t.Fatal(err)
	}
	ch := changesOf(t, rec.take(t, a))
	if len(ch) != 2 || ch[0].Op != "renamed" || ch[0].From != "a.php" || ch[1].From != "dir" || !ch[1].Dir {
		t.Fatalf("changes %+v", ch)
	}
	// One path to a new name, creating parents.
	call(t, a, "files.move", TransferParams{Path: "b.php", To: "new/place/b2.php"}, &res)
	if res.Results[0].Path != "new/place/b2.php" {
		t.Fatalf("move to path %+v", res)
	}
	if ch := changesOf(t, rec.take(t, a)); len(ch) != 3 || ch[0].Path != "new" || ch[1].Path != "new/place" || ch[2].Op != "renamed" {
		t.Fatalf("move to path changes %+v", ch)
	}
	wantCode(t, a, "files.move", TransferParams{Paths: []string{"target/a.php", "new"}, To: "nodir"}, CodeInvalidParams)
	wantCode(t, a, "files.move", TransferParams{To: "target"}, CodeInvalidParams)
	wantCode(t, a, "files.move", TransferParams{Path: "target", To: "target/dir"}, CodeInvalidParams)
	wantCode(t, a, "files.move", TransferParams{Path: "target/a.php", To: "../x"}, CodeInvalidParams)

	// Copy a file into a directory and a directory to a new path.
	call(t, a, "files.copy", TransferParams{Path: "target/a.php", To: "new"}, &res)
	if res.Results[0].Path != "new/a.php" || readFile(t, filepath.Join(root, "new", "a.php")) != "a" {
		t.Fatalf("copy %+v", res)
	}
	call(t, a, "files.copy", TransferParams{Path: "target/dir", To: "copied"}, &res)
	if !res.Results[0].Dir || readFile(t, filepath.Join(root, "copied", "sub", "d.php")) != "d" || readFile(t, filepath.Join(root, "target", "dir", "c.php")) != "c" {
		t.Fatalf("copy dir %+v", res)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("c.php", filepath.Join(root, "copied", "link.php")); err != nil {
			t.Fatal(err)
		}
		call(t, a, "files.copy", TransferParams{Path: "copied", To: "copied2"}, nil)
		if l, err := os.Readlink(filepath.Join(root, "copied2", "link.php")); err != nil || l != "c.php" {
			t.Fatalf("symlink copy %q %v", l, err)
		}
	}
	ch = changesOf(t, rec.take(t, a))
	if len(ch) < 2 || ch[0].Op != "created" || ch[0].Path != "new/a.php" {
		t.Fatalf("copy changes %+v", ch)
	}
	wantCode(t, a, "files.copy", TransferParams{Path: "target/a.php", To: "new"}, CodeConflict)
	wantCode(t, a, "files.copy", TransferParams{Path: "copied", To: "copied/inner"}, CodeInvalidParams)
	wantCode(t, a, "files.copy", TransferParams{Path: "copied", To: "new/a.php", Overwrite: true}, CodeConflict)
	wantCode(t, a, "files.copy", TransferParams{Path: "missing", To: "new"}, CodeNotFound)
	// Overwrite reloads an open, unmodified target.
	writeFile(t, filepath.Join(root, "src.txt"), "fresh")
	call(t, a, "doc.open", DocOpenParams{Path: "new/a.php"}, nil)
	call(t, a, "files.copy", TransferParams{Path: "src.txt", To: "new/a.php", Overwrite: true}, nil)
	var txt DocText
	call(t, a, "doc.text", DocTextParams{Path: "new/a.php"}, &txt)
	if txt.Text != "fresh" {
		t.Fatalf("open target not reloaded: %q", txt.Text)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "new/a.php", At: Pos{Offset: 0}, Text: "x"}, nil)
	wantCode(t, a, "files.copy", TransferParams{Path: "src.txt", To: "new/a.php", Overwrite: true}, CodeConflict)
}

func TestFilesDelete(t *testing.T) {
	a, root := fileProject(t, map[string]string{"a.php": "a", "dir/b.php": "b", "dir/c.php": "c", "keep.php": "k"})
	var trashed []string
	a.trash = func(p string) error {
		trashed = append(trashed, p)
		return os.RemoveAll(p)
	}
	call(t, a, "doc.open", DocOpenParams{Path: "dir/b.php"}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "dir/c.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "dir/c.php", At: Pos{Offset: 0}, Text: "dirty"}, nil)
	// An unsaved document blocks the whole request; nothing is deleted.
	err := tryCall(a, "files.delete", DeleteParams{Paths: []string{"a.php", "dir"}}, nil)
	if ErrorCode(err) != CodeConflict || !strings.Contains(err.Error(), "dir/c.php") {
		t.Fatalf("conflict %v", err)
	}
	if readFile(t, filepath.Join(root, "a.php")) != "a" || len(trashed) != 0 {
		t.Fatal("deleted despite conflict")
	}
	call(t, a, "doc.save", PathParams{Path: "dir/c.php"}, nil)

	rec := recordEvents(t, a, "*")
	var res DeleteResult
	call(t, a, "files.delete", DeleteParams{Paths: []string{"a.php", "dir"}}, &res)
	if !res.Trashed || len(res.Deleted) != 2 || len(res.Closed) != 2 || len(trashed) != 2 {
		t.Fatalf("delete %+v %v", res, trashed)
	}
	gone(t, filepath.Join(root, "dir"))
	if _, err := a.Document("dir/b.php"); ErrorCode(err) != CodeNotFound {
		t.Fatal("document of deleted file still open")
	}
	evs := rec.take(t, a)
	ch := changesOf(t, evs)
	if len(ch) != 2 || ch[0].Op != "deleted" || !ch[0].Trashed || !ch[1].Dir {
		t.Fatalf("changes %+v", ch)
	}
	if !strings.Contains(topics(evs), "doc.closed") {
		t.Fatalf("no doc.closed: %s", topics(evs))
	}
	// Local History survives a delete (restore remains possible).
	var revs []struct{ ID uint64 }
	call(t, a, "history.list", PathParams{Path: "dir/c.php"}, &revs)
	if len(revs) == 0 {
		t.Fatal("history lost")
	}

	// Without a trash only an explicit permanent delete works.
	a.trash = func(string) error { return trash.ErrUnavailable }
	wantCode(t, a, "files.delete", DeleteParams{Path: "keep.php"}, CodeUnavailable)
	if readFile(t, filepath.Join(root, "keep.php")) != "k" {
		t.Fatal("deleted without trash")
	}
	call(t, a, "files.delete", DeleteParams{Path: "keep.php", Permanent: true}, &res)
	if res.Trashed {
		t.Fatal("permanent reported as trashed")
	}
	gone(t, filepath.Join(root, "keep.php"))
	a.trash = func(string) error { return errors.New("boom") }
	writeFile(t, filepath.Join(root, "z.php"), "")
	wantCode(t, a, "files.delete", DeleteParams{Path: "z.php"}, CodeFailed)
	wantCode(t, a, "files.delete", DeleteParams{Path: "missing"}, CodeNotFound)
	wantCode(t, a, "files.delete", DeleteParams{}, CodeInvalidParams)
	wantCode(t, a, "files.delete", DeleteParams{Path: "."}, CodeInvalidParams)
	wantCode(t, a, "files.delete", DeleteParams{Path: root}, CodeInvalidParams)
	wantCode(t, a, "files.delete", DeleteParams{Path: "../x"}, CodeInvalidParams)
}

func TestFilesRevealAndCopyPath(t *testing.T) {
	a, root := fileProject(t, map[string]string{"src/a b.php": ""})
	var got []string
	a.start = func(name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	}
	var res RevealResult
	call(t, a, "files.reveal", PathParams{Path: "src/a b.php"}, &res)
	// newTestApp uses GOOS linux: the containing directory is opened.
	if len(got) != 2 || got[0] != "xdg-open" || got[1] != filepath.Join(root, "src") || res.Path != "src/a b.php" {
		t.Fatalf("reveal %v %+v", got, res)
	}
	call(t, a, "files.reveal", PathParams{Path: "."}, &res)
	if got[1] != root {
		t.Fatalf("reveal root %v", got)
	}
	if c := revealCommand("darwin", "/p/x", false); strings.Join(c, " ") != "open -R /p/x" {
		t.Fatal(c)
	}
	if c := revealCommand("windows", `C:\p\x`, false); strings.Join(c, " ") != `explorer /select,C:\p\x` {
		t.Fatal(c)
	}
	wantCode(t, a, "files.reveal", PathParams{Path: "missing"}, CodeNotFound)
	wantCode(t, a, "files.reveal", PathParams{Path: "../"}, CodeInvalidParams)
	a.start = func(string, ...string) error { return errors.New("exec: \"xdg-open\": " + "executable file not found") }
	wantCode(t, a, "files.reveal", PathParams{Path: "src"}, CodeFailed)

	var cp CopyPathResult
	call(t, a, "files.copyPath", CopyPathParams{Paths: []string{"src/a b.php", "src"}}, &cp)
	abs := filepath.Join(root, "src", "a b.php")
	if len(cp.Paths) != 2 || cp.Paths[0].Absolute != abs || cp.Paths[0].Relative != "src/a b.php" || cp.Paths[0].Name != "a b.php" ||
		!strings.HasPrefix(cp.Paths[0].URI, "file:///") || !strings.HasSuffix(cp.Paths[0].URI, "/src/a%20b.php") {
		t.Fatalf("copyPath %+v", cp)
	}
	if cp.Text != abs+"\n"+filepath.Join(root, "src") || cp.RelativeText != "src/a b.php\nsrc" {
		t.Fatalf("text %q / %q", cp.Text, cp.RelativeText)
	}
	outside := filepath.Join(t.TempDir(), "o.txt")
	call(t, a, "files.copyPath", CopyPathParams{Path: outside}, &cp)
	if cp.Paths[0].Relative != "" || cp.RelativeText != outside {
		t.Fatalf("outside %+v", cp)
	}
	wantCode(t, a, "files.copyPath", CopyPathParams{}, CodeInvalidParams)
}

func TestFilesPHPClass(t *testing.T) {
	a, root := fileProject(t, map[string]string{
		"composer.json": `{
			"autoload": {"psr-4": {"App\\": "src/", "Legacy\\": ["lib", "old/"]}},
			"autoload-dev": {"psr-4": {"App\\Tests\\": "tests/", "Dev\\": "src/"}}
		}`,
		"src/.keep": "",
	})
	var res PHPClassResult
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "src/Service/Mail", Name: "Mailer", Open: true}, &res)
	want := "<?php\n\ndeclare(strict_types=1);\n\nnamespace App\\Service\\Mail;\n\nfinal class Mailer\n{\n}\n"
	if got := readFile(t, filepath.Join(root, "src", "Service", "Mail", "Mailer.php")); got != want {
		t.Fatalf("file\n%s", got)
	}
	if res.Class != `App\Service\Mail\Mailer` || res.Namespace != `App\Service\Mail` || res.Doc == nil || res.Path != "src/Service/Mail/Mailer.php" {
		t.Fatalf("result %+v", res)
	}
	for _, tc := range []struct{ dir, name, kind, decl, ns string }{
		{"tests/Unit", "MailerTest", "abstract", "abstract class MailerTest", `App\Tests\Unit`},
		{"lib", "Contract", "interface", "interface Contract", "Legacy"},
		{"old/X", "Helper", "trait", "trait Helper", `Legacy\X`},
		{"src", "Status", "enum", "enum Status", "App"},
		{"", "Root", "", "final class Root", ""},
		{"scripts", "Tool", "class", "final class Tool", ""},
	} {
		call(t, a, "files.createPhpClass", PHPClassParams{Dir: tc.dir, Name: tc.name + ".php", Kind: tc.kind}, &res)
		got := readFile(t, res.File)
		if !strings.Contains(got, tc.decl+"\n{\n}\n") || res.Namespace != tc.ns || (tc.ns == "") == strings.Contains(got, "namespace") {
			t.Errorf("%s/%s: ns %q\n%s", tc.dir, tc.name, res.Namespace, got)
		}
	}
	empty := ""
	custom := `\My\Space\`
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "src", Name: "NoNs", Namespace: &empty}, &res)
	if strings.Contains(readFile(t, res.File), "namespace") {
		t.Fatal("explicit empty namespace ignored")
	}
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "src", Name: "Custom", Namespace: &custom}, &res)
	if res.Class != `My\Space\Custom` {
		t.Fatalf("custom %+v", res)
	}
	bad := `My\1x`
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Name: "X", Namespace: &bad}, CodeInvalidParams)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "src", Name: "Mailer", Kind: "struct"}, CodeInvalidParams)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "src", Name: "1Bad"}, CodeInvalidParams)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "src", Name: "Class"}, CodeInvalidParams)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "src/Service/Mail", Name: "Mailer"}, CodeConflict)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "src/my-dir", Name: "X"}, CodeInvalidParams)
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Dir: "../x", Name: "X"}, CodeInvalidParams)

	var ns NamespaceResult
	call(t, a, "files.phpNamespace", PathParams{Path: "src/Domain"}, &ns)
	if !ns.Found || ns.Namespace != `App\Domain` || ns.Mapping.Dir != "src" || ns.Mapping.Dev {
		t.Fatalf("namespace %+v", ns)
	}
	call(t, a, "files.phpNamespace", PathParams{}, &ns)
	if ns.Found || ns.Dir != "" {
		t.Fatalf("root namespace %+v", ns)
	}
	writeFile(t, filepath.Join(root, "composer.json"), "{broken")
	wantCode(t, a, "files.phpNamespace", PathParams{Path: "src"}, CodeFailed)
	if err := os.Remove(filepath.Join(root, "composer.json")); err != nil {
		t.Fatal(err)
	}
	call(t, a, "files.phpNamespace", PathParams{Path: "src"}, &ns)
	if ns.Found {
		t.Fatal("namespace without composer.json")
	}
}

func TestNamespaceFor(t *testing.T) {
	maps := []PSR4Mapping{{Prefix: "", Dir: "", Dev: false}, {Prefix: "App", Dir: "src"}}
	if r, _ := namespaceFor(maps, "src/A"); r.Namespace != `App\A` {
		t.Fatal(r)
	}
	if r, _ := namespaceFor(maps, "other/B"); r.Namespace != `other\B` || !r.Found {
		t.Fatal(r)
	}
	if r, _ := namespaceFor(maps, "srcx"); r.Namespace != "srcx" {
		t.Fatal(r)
	}
	if normDir("./src/") != "src" || normDir(".") != "" || normDir("") != "" {
		t.Fatal("normDir")
	}
}
