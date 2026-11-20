package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// projectDir creates a project directory with one PHP file.
func projectDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(dir, name+".php"), "<?php\n")
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir
}

type projEvents struct {
	mu     sync.Mutex
	topics []string
}

func (l *projEvents) add(e Event) {
	l.mu.Lock()
	l.topics = append(l.topics, e.Topic+" "+string(e.Payload))
	l.mu.Unlock()
}

func (l *projEvents) has(t *testing.T, topic, contains string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, s := range l.topics {
			if strings.HasPrefix(s, topic+" ") && strings.Contains(s, contains) {
				l.mu.Unlock()
				return true
			}
		}
		l.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestProjectsLifecycle(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	w, err := a.EnableWorkspace(WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w2, err := a.EnableWorkspace(WorkspaceOptions{}); err != nil || w2 != w || a.Workspace() != w || w.Host() != a || w.Manager() == nil {
		t.Fatal("EnableWorkspace not idempotent", err)
	}
	log := &projEvents{}
	s := a.NewSession(log.add)
	defer s.Close()
	s.Subscribe("projects.*", "doc.*", "project.*")

	var list ProjectsListResult
	call(t, a, "projects.list", nil, &list)
	if len(list.Projects) != 0 || list.Active != "" {
		t.Fatalf("%+v", list)
	}
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	var ps ProjectSession
	call(t, a, "projects.open", PathParams{Path: dirA}, &ps)
	if ps.State != workspace.StateActive || ps.Root != dirA || a.ProjectRoot() != dirA {
		t.Fatalf("first project not on the host: %+v %s", ps, a.ProjectRoot())
	}
	call(t, a, "doc.open", DocOpenParams{Path: "alpha.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "alpha.php", At: Pos{Line: 1}, Text: "echo 1;\n"}, nil)

	call(t, a, "projects.open", PathParams{Path: dirB}, &ps)
	appB, err := w.App("beta")
	if err != nil || appB == nil || appB == a || w.ActiveApp() != appB {
		t.Fatalf("beta app %v %v", appB, err)
	}
	// The host forwards to the active project, through both call paths.
	var info ProjectInfo
	call(t, a, "project.info", nil, &info)
	if info.Root != dirB {
		t.Fatalf("project.info not forwarded: %+v", info)
	}
	if _, err := s.Call(ctx, "doc.open", json.RawMessage(`{"path":"beta.php"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := appB.Document(filepath.Join(dirB, "beta.php")); err != nil {
		t.Fatal("doc not opened in beta's app:", err)
	}
	// A local caller of the host stays with alpha.
	local := NewLocalInProcess(a)
	defer local.Close()
	if err := local.Call(ctx, "project.info", nil, &info); err != nil || info.Root != dirA {
		t.Fatalf("local caller: %+v %v", info, err)
	}
	localLog := &projEvents{}
	if _, err := local.Subscribe(ctx, []string{"doc.*"}, localLog.add); err != nil {
		t.Fatal(err)
	}
	var dr DocOpenResult
	if err := local.Call(ctx, "doc.open", DocOpenParams{Path: "alpha.php"}, &dr); err != nil || !dr.AlreadyOpen {
		t.Fatalf("local doc.open: %+v %v", dr, err)
	}
	inproc := NewInProcess(a)
	defer inproc.Close()
	if err := inproc.Call(ctx, "project.info", nil, &info); err != nil || info.Root != dirB {
		t.Fatalf("forwarding caller: %+v %v", info, err)
	}
	var ai AppInfo
	call(t, a, "app.info", nil, &ai)
	if ai.Project != dirB {
		t.Fatalf("app.info project %q", ai.Project)
	}
	// Events of the active project and of the workspace reach the host's
	// clients.
	if !log.has(t, "projects.activated", `"beta"`) || !log.has(t, "projects.backgrounded", `"alpha"`) || !log.has(t, "doc.opened", "beta.php") {
		t.Fatalf("events: %v", log.topics)
	}
	call(t, a, "projects.list", nil, &list)
	if len(list.Projects) != 2 || list.Active != dirB || list.Projects[0].State != workspace.StateBackground {
		t.Fatalf("%+v", list)
	}

	// Hibernate alpha (background): its document is saved, the host is
	// released.
	call(t, a, "projects.hibernate", ProjectsHibernateParams{Name: "alpha", ClientState: json.RawMessage(`{"tabs":["alpha.php"]}`)}, &ps)
	if ps.State != workspace.StateHibernated || len(ps.Docs) != 1 || ps.Docs[0] != filepath.Join(dirA, "alpha.php") {
		t.Fatalf("%+v", ps)
	}
	if b, _ := os.ReadFile(filepath.Join(dirA, "alpha.php")); !strings.Contains(string(b), "echo 1;") {
		t.Fatalf("not saved: %q", b)
	}
	if a.ProjectRoot() != "" {
		t.Fatal("host still holds alpha")
	}
	if app, _ := w.App("alpha"); app != nil {
		t.Fatal("hibernated project has an app")
	}
	// Switching resumes it (on the free host) with its documents.
	call(t, a, "projects.switch", ProjectsNameParams{Name: "alpha"}, &ps)
	if ps.State != workspace.StateActive || string(ps.ClientState) != `{"tabs":["alpha.php"]}` || a.ProjectRoot() != dirA {
		t.Fatalf("%+v", ps)
	}
	if _, err := a.Document(filepath.Join(dirA, "alpha.php")); err != nil {
		t.Fatal("document not restored:", err)
	}
	// beta (background, in its own app) hibernates and resumes in a new
	// app.
	call(t, a, "projects.hibernate", ProjectsHibernateParams{Name: dirB}, &ps)
	call(t, a, "projects.resume", ProjectsNameParams{Name: "beta"}, &ps)
	if ps.State != workspace.StateBackground {
		t.Fatalf("%+v", ps)
	}
	if app, _ := w.App("beta"); app == nil || app == appB || app == a {
		t.Fatal("beta not resumed in a new app")
	} else if _, err := app.Document(filepath.Join(dirB, "beta.php")); err != nil {
		t.Fatal("beta's document not restored:", err)
	}

	// Legacy project.open replaces the client's project.
	dirC := projectDir(t, "gamma")
	call(t, a, "project.open", PathParams{Path: dirC}, &info)
	if info.Root != dirC || a.ProjectRoot() != dirC {
		t.Fatalf("legacy open: %+v", info)
	}
	call(t, a, "projects.list", nil, &list)
	if len(list.Projects) != 2 || list.Active != dirC {
		t.Fatalf("alpha not replaced: %+v", list)
	}
	call(t, a, "project.open", PathParams{Path: dirC}, &info) // same project
	call(t, a, "project.close", nil, nil)
	if _, ok := w.Manager().Active(); ok || a.ProjectRoot() != "" {
		t.Fatal("legacy close left the project")
	}
	call(t, a, "project.close", nil, nil) // nothing open

	wantCode(t, a, "projects.switch", ProjectsNameParams{Name: "nope"}, CodeNotFound)
	wantCode(t, a, "projects.open", PathParams{}, CodeInvalidParams)
	wantCode(t, a, "projects.open", PathParams{Path: filepath.Join(dirA, "alpha.php")}, CodeInvalidParams)
	wantCode(t, a, "project.open", PathParams{}, CodeInvalidParams)
	var closed ProjectsListResult
	call(t, a, "projects.close", ProjectsCloseParams{Name: "beta"}, &closed)
	if len(closed.Projects) != 0 {
		t.Fatalf("%+v", closed)
	}
	wantCode(t, a, "projects.close", ProjectsCloseParams{Name: "beta"}, CodeNotFound)
	if !log.has(t, "projects.closed", `"beta"`) {
		t.Fatalf("events: %v", log.topics)
	}
}

func TestProjectsBusyAndUnsaved(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	// A project open before the workspace is adopted.
	call(t, a, "project.open", PathParams{Path: dirA}, nil)
	call(t, a, "projects.open", PathParams{Path: dirB}, nil)
	var list ProjectsListResult
	call(t, a, "projects.list", nil, &list)
	if len(list.Projects) != 2 || list.Projects[0].Root != dirA {
		t.Fatalf("%+v", list)
	}
	// A running process in beta (active, forwarded) blocks hibernation.
	var info run.Info
	call(t, a, "run.start", RunStartParams{Spec: &run.Spec{Name: "sleeper", Program: self, Env: []string{runHelperEnv + "=sleep"}}}, &info)
	var busy ProjectsBusyResult
	call(t, a, "projects.busy", ProjectsNameParams{Name: "beta"}, &busy)
	if len(busy.Busy) != 1 || busy.Busy[0] != "run: sleeper" {
		t.Fatalf("%+v", busy)
	}
	call(t, a, "projects.busy", ProjectsNameParams{Name: "alpha"}, &busy)
	if len(busy.Busy) != 0 {
		t.Fatalf("%+v", busy)
	}
	err := tryCall(a, "projects.hibernate", ProjectsHibernateParams{Name: "beta"}, nil)
	if ErrorCode(err) != CodeConflict {
		t.Fatal(err)
	}
	wantCode(t, a, "projects.close", ProjectsCloseParams{Name: "beta"}, CodeConflict)
	wantCode(t, a, "projects.busy", ProjectsNameParams{Name: "nope"}, CodeNotFound)
	var ps ProjectSession
	call(t, a, "projects.hibernate", ProjectsHibernateParams{Name: "beta", Force: true}, &ps)
	if ps.State != workspace.StateHibernated {
		t.Fatalf("%+v", ps)
	}
	// With beta hibernated no project is active: the host answers.
	var pi ProjectInfo
	call(t, a, "project.info", nil, &pi)
	if pi.Root != dirA {
		t.Fatalf("%+v", pi)
	}

	// A document changed on disk and in the editor cannot be saved.
	call(t, a, "projects.switch", ProjectsNameParams{Name: "alpha"}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "alpha.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "alpha.php", At: Pos{Line: 1}, Text: "echo 1;\n"}, nil)
	writeFile(t, filepath.Join(dirA, "alpha.php"), "<?php // changed elsewhere\n")
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(dirA, "alpha.php"), future, future)
	if err := tryCall(a, "projects.hibernate", ProjectsHibernateParams{Name: "alpha"}, nil); ErrorCode(err) != CodeConflict {
		t.Fatalf("unsaved: %v", err)
	}
	call(t, a, "projects.hibernate", ProjectsHibernateParams{Name: "alpha", Force: true}, nil)
	var rec []RecoveryPoint
	call(t, a, "projects.switch", ProjectsNameParams{Name: "alpha"}, nil)
	call(t, a, "history.recovery", nil, &rec)
	if len(rec) == 0 {
		t.Fatal("forced hibernation lost the unsaved change")
	}
}

// testClock is a manual workspace.Clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
	fns []func()
}

type testTimer struct{}

func (testTimer) Stop() bool { return true }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) AfterFunc(_ time.Duration, f func()) workspace.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fns = append(c.fns, f)
	return testTimer{}
}

// fire runs all timers armed so far.
func (c *testClock) fire() {
	c.mu.Lock()
	fns := c.fns
	c.fns = nil
	c.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func (c *testClock) armed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.fns)
}

func TestProjectsAutoHibernateSetting(t *testing.T) {
	a := newTestApp(t)
	clock := &testClock{now: time.Now()}
	var created []*App
	w, err := a.EnableWorkspace(WorkspaceOptions{Clock: clock, NewApp: func() (*App, error) {
		o := a.opts
		o.GlobalSettings = a.Settings()
		c, err := New(o)
		created = append(created, c)
		return c, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	call(t, a, "projects.open", PathParams{Path: dirA}, nil)
	call(t, a, "projects.open", PathParams{Path: dirB}, nil)
	if len(created) != 1 || clock.armed() != 0 {
		t.Fatalf("apps %d, timers %d", len(created), clock.armed())
	}
	call(t, a, "settings.set", map[string]any{"key": KeyAutoHibernate, "scope": "global", "value": 5}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for w.Manager().AutoHibernate() != 5*time.Minute && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	var list ProjectsListResult
	call(t, a, "projects.list", nil, &list)
	if list.AutoHibernateMinutes != 5 || list.Projects[0].AutoHibernateAt == nil {
		t.Fatalf("%+v", list)
	}
	clock.fire()
	if i, _ := w.Manager().Get("alpha"); i.State != workspace.StateHibernated {
		t.Fatalf("%+v", i)
	}
	// The child App of a member answers projects.* too.
	var fromChild ProjectsListResult
	call(t, created[0], "projects.list", nil, &fromChild)
	if len(fromChild.Projects) != 2 {
		t.Fatalf("%+v", fromChild)
	}
	if _, err := created[0].EnableWorkspace(WorkspaceOptions{}); err == nil {
		t.Fatal("member became a host")
	}
}

func TestProjectsNewAppFailure(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.EnableWorkspace(WorkspaceOptions{NewApp: func() (*App, error) { return nil, os.ErrPermission }}); err != nil {
		t.Fatal(err)
	}
	call(t, a, "projects.open", PathParams{Path: projectDir(t, "alpha")}, nil)
	wantCode(t, a, "projects.open", PathParams{Path: projectDir(t, "beta")}, CodeFailed)
	wantCode(t, a, "projects.open", PathParams{Path: filepath.Join(t.TempDir(), "missing")}, CodeNotFound)
}

func TestProjectsAPIError(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{nil, 0},
		{workspace.ErrAmbiguous, CodeInvalidParams},
		{workspace.ErrUnsaved, CodeConflict},
		{workspace.ErrClosed, CodeUnavailable},
		{os.ErrClosed, CodeFailed},
	} {
		err := apiError(c.err)
		if c.err == nil {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if ErrorCode(err) != c.code {
			t.Fatalf("%v: %d", c.err, ErrorCode(err))
		}
	}
}

func TestProjectsHostCloseKeepsUnsaved(t *testing.T) {
	a := newTestApp(t)
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	call(t, a, "projects.open", PathParams{Path: dirA}, nil)
	call(t, a, "projects.open", PathParams{Path: dirB}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "beta.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "beta.php", At: Pos{Line: 1}, Text: "echo 1;\n"}, nil)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dirB, "beta.php")); string(b) != "<?php\n" {
		t.Fatalf("saved on exit: %q", b)
	}
	if _, err := a.Workspace().Open(context.Background(), dirA); ErrorCode(err) != CodeUnavailable {
		t.Fatalf("open after close: %v", err)
	}
}
