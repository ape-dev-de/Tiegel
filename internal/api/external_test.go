package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

const extTimeout = 10 * time.Second

// eventLog records bus events.
type eventLog struct {
	mu  sync.Mutex
	evs []bus.Event
}

func logEvents(t *testing.T, a *App, pattern string) *eventLog {
	l := &eventLog{}
	unsub := a.Bus().Subscribe(pattern, func(e bus.Event) {
		l.mu.Lock()
		l.evs = append(l.evs, e)
		l.mu.Unlock()
	})
	t.Cleanup(unsub)
	return l
}

func (l *eventLog) all(topic string) []any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []any
	for _, e := range l.evs {
		if e.Topic == topic {
			out = append(out, e.Payload)
		}
	}
	return out
}

// waitUntil polls cond with a deadline (no sleeps as synchronisation).
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(extTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func extBackends() []string {
	return []string{"auto", "poll"}
}

// newWatchApp opens a project with the watcher running (backend) and
// waits for its initial scan.
func newWatchApp(t *testing.T, backend string, files map[string]string) (*App, string) {
	t.Helper()
	home := t.TempDir()
	a, err := New(Options{
		ConfigDir:         filepath.Join(t.TempDir(), "config"),
		Interp:            interpOptions(nil),
		LSP:               servers.DetectOptions{HomeDir: home, PathEnv: "", ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
		ComposeEnv:        map[string]string{},
		GOOS:              "linux",
		AppVersion:        "test",
		Watch:             backend != "",
		WatchBackend:      backend,
		WatchPollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := openProject(t, a, files)
	if backend != "" {
		waitUntil(t, "watcher ready", func() bool { return a.watchStatus().Ready })
		if st := a.watchStatus(); st.Degraded != "" {
			t.Fatalf("watcher degraded: %+v", st)
		}
	}
	return a, root
}

func writeExt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDiskT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func docState(t *testing.T, a *App, path string) DocState {
	t.Helper()
	var list []DocState
	call(t, a, "doc.list", nil, &list)
	for _, d := range list {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("%s not open", path)
	return DocState{}
}

// errData decodes the data of an API error.
func errData(t *testing.T, err error) map[string]any {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("not an API error: %v", err)
	}
	b, _ := json.Marshal(e.Data)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestExternalChangeReloadsCleanDocument(t *testing.T) {
	for _, be := range extBackends() {
		t.Run(be, func(t *testing.T) {
			a, root := newWatchApp(t, be, map[string]string{"a.php": "<?php\necho 1;\necho 2;\n"})
			events := logEvents(t, a, "doc.*")
			call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
			writeExt(t, filepath.Join(root, "a.php"), "<?php\necho 1;\necho 'external';\n")
			waitUntil(t, "reload", func() bool { return docText(t, a, "a.php") == "<?php\necho 1;\necho 'external';\n" })
			waitUntil(t, "doc.reloaded", func() bool { return len(events.all(TopicDocReloaded)) == 1 })
			ev := events.all(TopicDocReloaded)[0].(DocReloadedEvent)
			if ev.Source != "watcher" || ev.Modified || ev.External != "" {
				t.Fatalf("reloaded event %+v", ev)
			}
			// The reload is one undo step and leaves the document saved.
			var u UndoResult
			call(t, a, "doc.undo", PathParams{Path: "a.php"}, &u)
			if !u.Done || docText(t, a, "a.php") != "<?php\necho 1;\necho 2;\n" || !u.Modified {
				t.Fatalf("undo after reload: %+v %q", u, docText(t, a, "a.php"))
			}
			if n := len(events.all(TopicDocExternalConflict)); n != 0 {
				t.Fatalf("%d conflict events for a clean document", n)
			}
		})
	}
}

func TestExternalChangeWhileDirty(t *testing.T) {
	for _, be := range extBackends() {
		t.Run(be, func(t *testing.T) {
			a, root := newWatchApp(t, be, map[string]string{"a.php": "<?php\none\ntwo\n"})
			file := filepath.Join(root, "a.php")
			events := logEvents(t, a, "doc.*")
			call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
			call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "<?php\none\nmine\n"}, nil)
			writeExt(t, file, "<?php\nzero\none\ntheirs\n")
			waitUntil(t, "conflict", func() bool { return len(events.all(TopicDocExternalConflict)) > 0 })
			ev := events.all(TopicDocExternalConflict)[0].(DocExternalEvent)
			if ev.Kind != ExternalModified || !ev.Modified || ev.Source != "watcher" || ev.Diff == nil || ev.Diff.Added != 2 || ev.Diff.Removed != 1 || ev.DiskSize != int64(len("<?php\nzero\none\ntheirs\n")) {
				t.Fatalf("conflict event %+v diff %+v", ev, ev.Diff)
			}
			if st := docState(t, a, "a.php"); st.External != ExternalModified || !st.Modified {
				t.Fatalf("state %+v", st)
			}
			if got := docText(t, a, "a.php"); got != "<?php\none\nmine\n" {
				t.Fatalf("buffer changed: %q", got)
			}

			// Autosave never overwrites the external change: background
			// triggers skip it, others report it.
			call(t, a, "autosave.trigger", AutosaveTriggerParams{Trigger: "idle"}, nil)
			call(t, a, "autosave.trigger", AutosaveTriggerParams{Trigger: "focusLost"}, nil)
			wantCode(t, a, "autosave.trigger", AutosaveTriggerParams{Trigger: "beforeCommit"}, CodeFailed)
			if got := readDiskT(t, file); got != "<?php\nzero\none\ntheirs\n" {
				t.Fatalf("autosave overwrote the external change: %q", got)
			}

			// doc.save refuses with -32004 / externalChange.
			err := tryCall(a, "doc.save", DocSaveParams{Path: "a.php"}, nil)
			if ErrorCode(err) != CodeConflict {
				t.Fatalf("save: %v", err)
			}
			if d := errData(t, err); d["reason"] != "externalChange" || d["kind"] != "modified" || d["path"] != "a.php" {
				t.Fatalf("error data %v", d)
			}
			wantCode(t, a, "doc.save", DocSaveParams{Path: "a.php", Force: true, Reload: true}, CodeInvalidParams)
			wantCode(t, a, "doc.resolveExternal", DocResolveExternalParams{Path: "a.php", Action: "merge"}, CodeInvalidParams)

			// keepMine writes the buffer.
			var res DocResolveResult
			call(t, a, "doc.resolveExternal", DocResolveExternalParams{Path: "a.php", Action: "keepMine"}, &res)
			if res.Modified || res.External != "" || readDiskT(t, file) != "<?php\none\nmine\n" {
				t.Fatalf("keepMine: %+v disk %q", res, readDiskT(t, file))
			}

			// takeDisk replaces the buffer (undoable).
			call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "#"}, nil)
			writeExt(t, file, "<?php\nfrom disk\n")
			waitUntil(t, "second conflict", func() bool { return docState(t, a, "a.php").External == ExternalModified })
			call(t, a, "doc.resolveExternal", DocResolveExternalParams{Path: "a.php", Action: "takeDisk"}, &res)
			if res.Modified || res.External != "" || docText(t, a, "a.php") != "<?php\nfrom disk\n" {
				t.Fatalf("takeDisk: %+v %q", res, docText(t, a, "a.php"))
			}
			call(t, a, "doc.undo", PathParams{Path: "a.php"}, nil)
			if got := docText(t, a, "a.php"); got != "#<?php\none\nmine\n" {
				t.Fatalf("undo takeDisk: %q", got)
			}
			// The disk state is known again: saving works without force.
			call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, nil)
			if got := readDiskT(t, file); got != "#<?php\none\nmine\n" {
				t.Fatalf("save after takeDisk+undo: %q", got)
			}
		})
	}
}

// Without a watcher (one-shot CLI), doc.save still detects the change.
func TestSaveRefusesExternalChangeWithoutWatcher(t *testing.T) {
	a, root := newWatchApp(t, "", map[string]string{"a.php": "<?php\n"})
	file := filepath.Join(root, "a.php")
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "<?php // mine\n"}, nil)
	// Same size as the original stamp would allow a stat-only check to
	// miss it; the content is compared.
	writeExt(t, file, "<?PHP\n")
	events := logEvents(t, a, TopicDocExternalConflict)
	wantCode(t, a, "doc.save", DocSaveParams{Path: "a.php"}, CodeConflict)
	waitUntil(t, "conflict event from save", func() bool { return len(events.all(TopicDocExternalConflict)) == 1 })
	if ev := events.all(TopicDocExternalConflict)[0].(DocExternalEvent); ev.Source != "save" {
		t.Fatalf("event %+v", ev)
	}
	// A second refused save does not repeat the event.
	wantCode(t, a, "doc.save", DocSaveParams{Path: "a.php"}, CodeConflict)
	call(t, a, "doc.save", DocSaveParams{Path: "a.php", Force: true}, nil)
	if got := readDiskT(t, file); got != "<?php // mine\n" {
		t.Fatalf("forced save: %q", got)
	}
	if n := len(events.all(TopicDocExternalConflict)); n != 1 {
		t.Fatalf("%d conflict events", n)
	}

	// If the disk already holds exactly the buffer content, it is no
	// conflict.
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "<?php // same\n"}, nil)
	writeExt(t, file, "<?php // same\n")
	call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, nil)

	// watch.refresh reconciles without a watcher: a clean document is
	// reloaded.
	writeExt(t, file, "<?php // refreshed\n")
	var rr WatchRefreshResult
	call(t, a, "watch.refresh", nil, &rr)
	if docText(t, a, "a.php") != "<?php // refreshed\n" || len(rr.External) != 0 {
		t.Fatalf("refresh: %q %+v", docText(t, a, "a.php"), rr)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "#"}, nil)
	writeExt(t, file, "<?php // refreshed again\n")
	call(t, a, "watch.refresh", nil, &rr)
	if len(rr.External) != 1 || rr.External[0].External != ExternalModified {
		t.Fatalf("refresh conflict: %+v", rr)
	}
}

// Tiegel's own writes never look external: saves (manual and autosave),
// Local History restore and module writes with ReloadDocument.
func TestSelfWriteNoConflict(t *testing.T) {
	for _, be := range extBackends() {
		t.Run(be, func(t *testing.T) {
			a, root := newWatchApp(t, be, map[string]string{"a.php": "<?php\n", "b.php": "<?php\n"})
			events := logEvents(t, a, "*")
			call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
			call(t, a, "doc.open", DocOpenParams{Path: "b.php"}, nil)
			var saved DocSaveResult
			for i := range 5 {
				call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: fmt.Sprintf("<?php // %d\n", i)}, nil)
				call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, &saved)
				call(t, a, "doc.replace", DocReplaceParams{Path: "b.php", Text: fmt.Sprintf("<?php // b%d\n", i)}, nil)
				call(t, a, "autosave.trigger", AutosaveTriggerParams{Trigger: "idle"}, nil)
			}
			// history.restore writes the file and reloads the document.
			call(t, a, "history.restore", RevisionParams{Path: "a.php", ID: saved.Revision - 1}, nil)
			// A module writes a file under the save lock and reloads.
			if err := a.Exclusive(func() error {
				writeExt(t, filepath.Join(root, "b.php"), "<?php // module\n")
				if !a.ReloadDocument("b.php") {
					t.Error("ReloadDocument: not reloaded")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if a.ReloadDocument("missing.php") {
				t.Fatal("ReloadDocument of a closed document")
			}
			// The watcher has seen everything up to the marker.
			writeExt(t, filepath.Join(root, "marker"), "m")
			waitUntil(t, "marker", func() bool {
				for _, p := range events.all(TopicFilesChanged) {
					for _, c := range p.(FilesChangedEvent).Changes {
						if c.Path == "marker" {
							return true
						}
					}
				}
				return false
			})
			for _, topic := range []string{TopicDocExternalConflict, TopicDocDeletedOnDisk} {
				if n := len(events.all(topic)); n != 0 {
					t.Fatalf("%d %s events for own writes: %+v", n, topic, events.all(topic))
				}
			}
			for _, p := range events.all(TopicDocReloaded) {
				if ev := p.(DocReloadedEvent); ev.Source == "watcher" {
					t.Fatalf("own write reloaded as external: %+v", ev)
				}
			}
			for _, path := range []string{"a.php", "b.php"} {
				if st := docState(t, a, path); st.External != "" || st.Modified {
					t.Fatalf("%s: %+v", path, st)
				}
			}
			// And the next save needs no force.
			call(t, a, "doc.replace", DocReplaceParams{Path: "b.php", Text: "<?php // after\n"}, nil)
			call(t, a, "doc.save", DocSaveParams{Path: "b.php"}, nil)
		})
	}
}

func TestDeletedAndRenamedOnDisk(t *testing.T) {
	for _, be := range extBackends() {
		t.Run(be, func(t *testing.T) {
			a, root := newWatchApp(t, be, map[string]string{"a.php": "<?php // a\n", "b.php": "<?php // b\n"})
			events := logEvents(t, a, "*")
			call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
			call(t, a, "doc.open", DocOpenParams{Path: "b.php"}, nil)
			call(t, a, "doc.replace", DocReplaceParams{Path: "b.php", Text: "<?php // b edited\n"}, nil)

			if err := os.Remove(filepath.Join(root, "a.php")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(root, "b.php"), filepath.Join(root, "c.php")); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, "deleted events", func() bool { return len(events.all(TopicDocDeletedOnDisk)) == 2 })
			byPath := map[string]DocExternalEvent{}
			for _, p := range events.all(TopicDocDeletedOnDisk) {
				ev := p.(DocExternalEvent)
				byPath[ev.Path] = ev
			}
			if ev := byPath["a.php"]; ev.Kind != ExternalDeleted || ev.Modified || ev.RenamedTo != "" {
				t.Fatalf("a.php %+v", ev)
			}
			if ev := byPath["b.php"]; ev.Kind != ExternalDeleted || !ev.Modified {
				t.Fatalf("b.php %+v", ev)
			}
			if watchFileIDs {
				if ev := byPath["b.php"]; ev.RenamedTo != "c.php" {
					t.Fatalf("rename target missing: %+v", ev)
				}
			}
			// Both stay open, flagged, with their content.
			for path, text := range map[string]string{"a.php": "<?php // a\n", "b.php": "<?php // b edited\n"} {
				if st := docState(t, a, path); st.External != ExternalDeleted {
					t.Fatalf("%s: %+v", path, st)
				}
				if got := docText(t, a, path); got != text {
					t.Fatalf("%s: %q", path, got)
				}
			}
			// files.changed reports the rename for file trees.
			waitUntil(t, "rename in files.changed", func() bool {
				for _, p := range events.all(TopicFilesChanged) {
					for _, c := range p.(FilesChangedEvent).Changes {
						if c.Kind == "renamed" && c.Path == "c.php" && c.OldPath == "b.php" || !watchFileIDs && c.Kind == "created" && c.Path == "c.php" {
							return true
						}
					}
				}
				return false
			})
			// Saving would recreate the file: refused without force.
			err := tryCall(a, "doc.save", DocSaveParams{Path: "b.php"}, nil)
			if d := errData(t, err); ErrorCode(err) != CodeConflict || d["kind"] != "deleted" {
				t.Fatalf("save deleted: %v %v", err, d)
			}
			wantCode(t, a, "doc.resolveExternal", DocResolveExternalParams{Path: "b.php", Action: "takeDisk"}, CodeNotFound)
			call(t, a, "doc.resolveExternal", DocResolveExternalParams{Path: "b.php", Action: "keepMine"}, nil)
			if got := readDiskT(t, filepath.Join(root, "b.php")); got != "<?php // b edited\n" {
				t.Fatalf("keepMine recreated %q", got)
			}
			// The file comes back with the same content: no conflict left.
			writeExt(t, filepath.Join(root, "a.php"), "<?php // a\n")
			waitUntil(t, "a.php restored", func() bool { return docState(t, a, "a.php").External == "" })
		})
	}
}

// watchFileIDs: renames are paired by inode (not on Windows, where the
// watcher pairs by size+mtime and these tests may see create+delete).
var watchFileIDs = filepath.Separator == '/'

func TestManyFilesAndDocuments(t *testing.T) {
	files := map[string]string{}
	for i := range 300 {
		files[fmt.Sprintf("src/d%d/f%d.php", i%15, i)] = "<?php\n"
	}
	a, root := newWatchApp(t, "auto", files)
	const open = 40
	for i := range open {
		call(t, a, "doc.open", DocOpenParams{Path: fmt.Sprintf("src/d%d/f%d.php", i%15, i)}, nil)
	}
	// Change every file externally at once.
	for i := range 300 {
		writeExt(t, filepath.Join(root, "src", fmt.Sprintf("d%d", i%15), fmt.Sprintf("f%d.php", i)), fmt.Sprintf("<?php // %d\n", i))
	}
	waitUntil(t, "all open documents reloaded", func() bool {
		for i := range open {
			if docText(t, a, fmt.Sprintf("src/d%d/f%d.php", i%15, i)) != fmt.Sprintf("<?php // %d\n", i) {
				return false
			}
		}
		return true
	})
	st := a.watchStatus()
	if !st.Running || st.Files != 300 || st.Backend == "" {
		t.Fatalf("status %+v", st)
	}
}

func TestWatcherGitChanged(t *testing.T) {
	a, root := newWatchApp(t, "auto", map[string]string{
		"a.php":                 "<?php\n",
		".git/HEAD":             "ref: refs/heads/main\n",
		".git/refs/heads/main":  "1\n",
		".git/objects/aa/bbbbb": "x",
	})
	events := logEvents(t, a, TopicGitChanged)
	writeExt(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/feature\n")
	waitUntil(t, "git.changed", func() bool { return len(events.all(TopicGitChanged)) > 0 })
	ev := events.all(TopicGitChanged)[0].(GitChangedEvent)
	if ev.Op != "external" || len(ev.Paths) != 1 || ev.Paths[0] != ".git/HEAD" {
		t.Fatalf("git.changed %+v", ev)
	}
}

func TestWatchMethods(t *testing.T) {
	a, root := newWatchApp(t, "", map[string]string{"a.php": "<?php\n", "vendor/x.php": "<?php\n"})
	var st WatchStatus
	call(t, a, "watch.status", nil, &st)
	if st.Running {
		t.Fatalf("watcher runs without Options.Watch: %+v", st)
	}
	wantCode(t, a, "watch.start", WatchStartParams{Backend: "inotify"}, CodeInvalidParams)
	call(t, a, "watch.start", WatchStartParams{Backend: "poll", Exclude: []string{"vendor"}}, &st)
	if !st.Running || st.Backend != "poll" {
		t.Fatalf("start %+v", st)
	}
	waitUntil(t, "ready", func() bool { return a.watchStatus().Ready })
	if st := a.watchStatus(); st.Files != 1 || st.Root != root {
		t.Fatalf("status %+v", st)
	}
	call(t, a, "watch.stop", nil, &st)
	if st.Running {
		t.Fatalf("stop %+v", st)
	}
	call(t, a, "project.close", nil, nil)
	wantCode(t, a, "watch.start", WatchStartParams{}, CodeNoProject)
	if _, err := New(Options{ConfigDir: t.TempDir(), WatchBackend: "x"}); err == nil {
		t.Fatal("New accepted an unknown watch backend")
	}
}

// The watch.exclude setting is used by default.
func TestWatchExcludeSetting(t *testing.T) {
	a, _ := newWatchApp(t, "", map[string]string{"a.php": "<?php\n", "var/cache/x": "x"})
	call(t, a, "settings.set", SettingsSetParams{Key: SettingWatchExclude, Scope: "project", Value: json.RawMessage(`["var/cache"]`)}, nil)
	call(t, a, "watch.start", WatchStartParams{Backend: "poll"}, nil)
	waitUntil(t, "ready", func() bool { return a.watchStatus().Ready })
	if st := a.watchStatus(); st.Files != 1 {
		t.Fatalf("exclude setting ignored: %+v", st)
	}
}

func TestReplaceMinimal(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"abc", "abc"},
		{"abc", "abXc"},
		{"", "new"},
		{"old", ""},
		{"line1\r\nline2\r\n", "line1\r\nline2\r\nline3\r\n"},
		{"a\r\nb", "a\nb"},
		{"x äöü y", "x äü y"},
		{"€€", "€£€"},
		{"same\nsame\n", "same\n"},
	} {
		b := buffer.New([]byte(tc.from))
		var edits []buffer.Edit
		b.OnEdit(func(e buffer.Edit) { edits = append(edits, e) })
		changed, err := replaceMinimal(b, []byte(tc.to))
		if err != nil || b.String() != tc.to || changed != (tc.from != tc.to) {
			t.Fatalf("%q→%q: %q %v %v", tc.from, tc.to, b.String(), changed, err)
		}
		for _, e := range edits {
			for _, s := range []string{string(e.Text), string(e.OldText)} {
				if !utf8Valid(s) {
					t.Fatalf("%q→%q: edit splits a character: %+v", tc.from, tc.to, e)
				}
			}
		}
		if changed {
			b.Undo()
			if b.String() != tc.from {
				t.Fatalf("undo %q→%q: %q", tc.from, tc.to, b.String())
			}
		}
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

// Tiegel's own file operations (files.rename with open documents, also
// a modified one, and a directory move) are no external changes.
func TestFileOpsAreNotExternal(t *testing.T) {
	for _, be := range extBackends() {
		t.Run(be, func(t *testing.T) {
			a, root := newWatchApp(t, be, map[string]string{"a.php": "<?php // a\n", "src/b.php": "<?php // b\n"})
			events := logEvents(t, a, "*")
			call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
			call(t, a, "doc.open", DocOpenParams{Path: "src/b.php"}, nil)
			call(t, a, "doc.replace", DocReplaceParams{Path: "src/b.php", Text: "<?php // b unsaved\n"}, nil)
			call(t, a, "files.rename", RenameParams{Path: "a.php", NewName: "renamed.php"}, nil)
			call(t, a, "files.rename", RenameParams{Path: "src", NewName: "lib"}, nil)
			writeExt(t, filepath.Join(root, "marker"), "m")
			waitUntil(t, "marker from the watcher", func() bool {
				for _, p := range events.all(TopicFilesChanged) {
					for _, c := range p.(FilesChangedEvent).Changes {
						if c.Path == "marker" && c.Source == FileSourceWatcher {
							return true
						}
					}
				}
				return false
			})
			for _, topic := range []string{TopicDocDeletedOnDisk, TopicDocExternalConflict} {
				if n := len(events.all(topic)); n != 0 {
					t.Fatalf("%s after own file operations: %+v", topic, events.all(topic))
				}
			}
			var sawTiegel bool
			for _, p := range events.all(TopicFilesChanged) {
				for _, c := range p.(FilesChangedEvent).Changes {
					sawTiegel = sawTiegel || c.Source == FileSourceTiegel && c.Kind == FileRenamed && c.Path == "renamed.php"
				}
			}
			if !sawTiegel {
				t.Fatal("files.rename did not publish a tiegel-sourced rename")
			}
			if st := docState(t, a, "lib/b.php"); st.External != "" || !st.Modified {
				t.Fatalf("moved document %+v", st)
			}
			call(t, a, "doc.save", DocSaveParams{Path: "lib/b.php"}, nil)
			call(t, a, "doc.replace", DocReplaceParams{Path: "renamed.php", Text: "<?php // a2\n"}, nil)
			call(t, a, "doc.save", DocSaveParams{Path: "renamed.php"}, nil)
		})
	}
}
