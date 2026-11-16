package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Regression tests for the backend review of 2026-11-16.

// #2: closing a tab and quickly reopening it let the late doc.close remove
// the reopened document, so its edits were never saved. doc.close now
// refuses unsaved changes without discard:true and checks the generation.
func TestDocCloseLateCloseKeepsReopenedDocument(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "<?php\n"})

	var first DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, &first)
	if first.Generation == 0 {
		t.Fatal("no generation")
	}
	// The tab is closed (doc.save + doc.close still pending) and reopened
	// at once: the document is still open, the reopened tab edits it.
	var again DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, &again)
	if !again.AlreadyOpen || again.Generation != first.Generation {
		t.Fatalf("reopen %+v", again)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 2}, Text: "echo 1;\n"}, nil)
	// Now the late doc.close of the old tab arrives.
	wantCode(t, a, "doc.close", DocCloseParams{Path: "a.php", Generation: first.Generation}, CodeConflict)
	if got := docText(t, a, "a.php"); got != "<?php\necho 1;\n" {
		t.Fatalf("edits of the reopened tab lost: %q", got)
	}
	call(t, a, "doc.save", PathParams{Path: "a.php"}, nil)
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); string(data) != "<?php\necho 1;\n" {
		t.Fatalf("disk %q", data)
	}

	// Close and reopen: a new generation; a stale close is refused even
	// without unsaved changes.
	var cr DocCloseResult
	call(t, a, "doc.close", DocCloseParams{Path: "a.php", Generation: first.Generation}, &cr)
	if cr.Discarded || cr.Generation != first.Generation {
		t.Fatalf("close %+v", cr)
	}
	var second DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, &second)
	if second.AlreadyOpen || second.Generation == first.Generation {
		t.Fatalf("second open %+v", second)
	}
	wantCode(t, a, "doc.close", DocCloseParams{Path: "a.php", Generation: first.Generation}, CodeConflict)
	call(t, a, "doc.close", DocCloseParams{Path: "a.php", Generation: second.Generation}, nil)
	wantCode(t, a, "doc.close", DocCloseParams{Path: "a.php"}, CodeNotFound)
}

// blockingFilter blocks the save pipeline of trigger until release is
// closed and reports on started when it begins blocking.
func blockingFilter(a *App, trigger autosave.Trigger) (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	var once bool
	autosave.BeforeSave.On(a.Hooks(), 0, func(_ context.Context, e autosave.SaveEvent) (autosave.SaveEvent, error) {
		if e.Trigger == trigger && !once {
			once = true
			close(started)
			<-release
		}
		return e, nil
	})
	return started, release
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

// #3: doc.save copied the text before taking the save lock. An older
// manual save then overwrote a newer autosave on disk while the buffer
// stayed marked as saved.
func TestManualSaveDoesNotOverwriteNewerAutosave(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "v0"})
	path := filepath.Join(root, "a.php")
	started, release := blockingFilter(a, autosave.TriggerIdle)

	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "v1"}, nil)

	// A0: an idle save holds the save lock (blocked in a filter).
	a0 := make(chan error, 1)
	go func() { a0 <- a.saver.SaveAll(context.Background(), autosave.TriggerIdle) }()
	<-started
	// A2: a focus-lost autosave queues for the lock first ...
	a2 := make(chan error, 1)
	go func() { a2 <- a.saver.SaveAll(context.Background(), autosave.TriggerFocusLost) }()
	time.Sleep(50 * time.Millisecond)
	// ... then the user presses Cmd+S while the content is v1 ...
	g1 := make(chan error, 1)
	go func() { g1 <- tryCall(a, "doc.save", PathParams{Path: "a.php"}, nil) }()
	time.Sleep(50 * time.Millisecond)
	// ... and keeps typing.
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "v2"}, nil)
	close(release)
	waitDone(t, a0)
	waitDone(t, a2)
	waitDone(t, g1)

	data, _ := os.ReadFile(path)
	var st []DocState
	call(t, a, "doc.list", nil, &st)
	if string(data) != "v2" && !st[0].Modified {
		t.Fatalf("data loss: disk %q but buffer %q is marked saved", data, docText(t, a, "a.php"))
	}
	if string(data) != "v2" || st[0].Modified {
		t.Fatalf("disk %q, modified %v; want v2 saved", data, st[0].Modified)
	}
}

// #11: saves addressed documents by path. A snapshot of a document that was
// closed with discard was still written, and after reopening, versions of
// the old instance were confused with the new one.
func TestSaveOfClosedInstanceIsSkipped(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "orig"})
	path := filepath.Join(root, "a.php")
	ds := docSet{a}

	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "discard me"}, nil)
	snap := ds.Dirty()
	if len(snap) != 1 || snap[0].Instance == 0 {
		t.Fatalf("dirty %+v", snap)
	}
	call(t, a, "doc.close", DocCloseParams{Path: "a.php", Discard: true}, nil)
	if err := ds.Save(context.Background(), snap[0]); !errors.Is(err, autosave.ErrDocGone) {
		t.Fatalf("save of closed instance: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "orig" {
		t.Fatalf("discarded content written: %q", data)
	}

	// Reopen and edit to the same version number: the old snapshot must
	// not mark the new instance clean.
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	var st DocState
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "new work"}, &st)
	if st.Version != snap[0].Version {
		t.Skipf("versions differ (%d vs %d); collision not reproducible", st.Version, snap[0].Version)
	}
	if err := ds.Save(context.Background(), snap[0]); !errors.Is(err, autosave.ErrDocGone) {
		t.Fatalf("save of old instance: %v", err)
	}
	var list []DocState
	call(t, a, "doc.list", nil, &list)
	if !list[0].Modified {
		t.Fatal("new instance marked clean by an old snapshot")
	}
	// SaveAll skips vanished documents silently.
	if err := a.saver.SaveAll(context.Background(), autosave.TriggerManual); err != nil {
		t.Fatal(err)
	}
}

// #12: history.restore wrote the file outside the save lock; a save in
// flight then overwrote the restored file while the buffer showed the
// restored content as saved.
func TestHistoryRestoreWaitsForSaveInFlight(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "v0"})
	path := filepath.Join(root, "a.php")
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "rev1"}, nil)
	var sr DocSaveResult
	call(t, a, "doc.save", PathParams{Path: "a.php"}, &sr)

	started, release := blockingFilter(a, autosave.TriggerIdle)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "typing"}, nil)
	a0 := make(chan error, 1)
	go func() { a0 <- a.saver.SaveAll(context.Background(), autosave.TriggerIdle) }()
	<-started
	restored := make(chan error, 1)
	go func() { restored <- tryCall(a, "history.restore", RevisionParams{Path: "a.php", ID: sr.Revision}, nil) }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	waitDone(t, a0)
	waitDone(t, restored)

	data, _ := os.ReadFile(path)
	buf := docText(t, a, "a.php")
	var list []DocState
	call(t, a, "doc.list", nil, &list)
	if !list[0].Modified && string(data) != buf {
		t.Fatalf("buffer %q marked saved but disk has %q", buf, data)
	}
	if string(data) != "rev1" || buf != "rev1" {
		t.Fatalf("disk %q buffer %q, want rev1", data, buf)
	}
}

// #5: when saving on close failed, the changes were silently dropped. Now
// they are preserved as recovery points in Local History, the error says
// so, and the hint survives a restart (history.recovery).
func TestRecoveryPointWhenSaveOnCloseFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions as non-root")
	}
	config := filepath.Join(t.TempDir(), "config")
	newApp := func() *App {
		home := t.TempDir()
		a, err := New(Options{
			ConfigDir: config, Interp: interpOptions(nil),
			LSP:        servers.DetectOptions{HomeDir: home, PathEnv: "", ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
			ComposeEnv: map[string]string{}, GOOS: "linux", AppVersion: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	writeFile(t, filepath.Join(sub, "a.php"), "<?php\n")

	a := newApp()
	call(t, a, "project.open", PathParams{Path: dir}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "ro/a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "ro/a.php", Text: "<?php // precious\n"}, nil)
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	err := tryCall(a, "autosave.trigger", AutosaveTriggerParams{Trigger: "close"}, nil)
	if ErrorCode(err) != CodeFailed || !strings.Contains(err.Error(), "history.recovery") {
		t.Fatalf("save on close: %v", err)
	}
	var pts []RecoveryPoint
	call(t, a, "history.recovery", nil, &pts)
	if len(pts) != 1 || pts[0].Path != "ro/a.php" || pts[0].Revision == 0 || pts[0].Error == "" {
		t.Fatalf("recovery %+v", pts)
	}
	var rc RevisionContent
	call(t, a, "history.content", RevisionParams{Path: "ro/a.php", ID: pts[0].Revision}, &rc)
	if rc.Text != "<?php // precious\n" || !strings.HasPrefix(rc.Revision.Label, "recovery:") {
		t.Fatalf("recovery content %+v", rc)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// Next start: the hint is still there until dismissed.
	b := newApp()
	t.Cleanup(func() { _ = b.Close() })
	call(t, b, "project.open", PathParams{Path: dir}, nil)
	call(t, b, "history.recovery", nil, &pts)
	if len(pts) != 1 || pts[0].Path != "ro/a.php" || pts[0].Reason != "save failed on close" {
		t.Fatalf("recovery after restart %+v", pts)
	}
	call(t, b, "history.recoveryDismiss", RecoveryDismissParams{Paths: []string{"ro/a.php"}}, &pts)
	if len(pts) != 0 {
		t.Fatalf("dismiss %+v", pts)
	}
	call(t, b, "history.recovery", nil, &pts)
	if len(pts) != 0 {
		t.Fatalf("after dismiss %+v", pts)
	}
}

// Closing the App with modified documents preserves them as well.
func TestRecoveryPointOnAppClose(t *testing.T) {
	a := newTestApp(t)
	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "<?php // unsaved\n"}, nil)
	pts := a.preserveUnsaved("test", errors.New("boom"))
	if len(pts) != 1 || pts[0].Reason != "test" || pts[0].Error != "boom" {
		t.Fatalf("preserve %+v", pts)
	}
	wantCode(t, a, "history.recoveryDismiss", RecoveryDismissParams{Paths: []string{""}}, CodeInvalidParams)
	call(t, a, "history.recoveryDismiss", RecoveryDismissParams{}, &pts)
	if len(pts) != 0 {
		t.Fatalf("dismiss all %+v", pts)
	}
	call(t, a, "project.close", nil, nil)
	wantCode(t, a, "history.recovery", nil, CodeNoProject)
}

// #9: a request with an object or float id was counted as pending, but the
// connection answers it as a parse error without id; serve then hung (the
// next message was never read and the input end never reached).
func TestServeInvalidRequestIDDoesNotHang(t *testing.T) {
	a := newTestApp(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":{"x":1},"method":"app.info"}`,
		`{"jsonrpc":"2.0","id":1.5,"method":"app.info"}`,
		`{"jsonrpc":"2.0","id":2,"method":"app.info"}`,
	}, "\n") + "\n"
	rwc := &bufRWC{Reader: strings.NewReader(in)}
	done := make(chan error, 1)
	go func() { done <- a.Serve(context.Background(), rwc, ServeOptions{}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve hangs after a request with an invalid id")
	}
	rwc.mu.Lock()
	out := rwc.out.String()
	rwc.mu.Unlock()
	if !strings.Contains(out, `"id":2,"result"`) || strings.Count(out, `"code":-32700`) != 2 {
		t.Fatalf("output:\n%s", out)
	}
}

// #14: a session whose client stops reading is cut off at the queue limit
// instead of growing without bound.
func TestSessionQueueLimit(t *testing.T) {
	a := newTestApp(t)
	block := make(chan struct{})
	defer close(block)
	sess := a.NewSession(func(Event) { <-block })
	overflow := make(chan struct{})
	sess.LimitQueue(5, func() { close(overflow) })
	sess.Subscribe("test.*")
	for i := 0; i < 50; i++ {
		a.Bus().Publish("test.event", i)
	}
	select {
	case <-overflow:
	case <-time.After(5 * time.Second):
		t.Fatal("no overflow")
	}
	if got := sess.Subscribe(); len(got) != 0 {
		t.Fatalf("subscriptions after overflow: %v", got)
	}
}
