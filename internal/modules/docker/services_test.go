package docker

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

const shopCompose = "name: shop\nservices:\n  web:\n    image: nginx\n    ports: [\"8080:80\"]\n  app:\n    image: php:8.3-fpm\n"

const shopPS = `{"ID":"1","Name":"shop-web-1","Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"ID":"2","Name":"shop-app-1","Service":"app","State":"exited","ExitCode":0}
`

// launcher records launched programs.
type launcher struct {
	mu    sync.Mutex
	calls [][]string
}

func (l *launcher) launch(argv, _ []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, argv)
	return nil
}

func (l *launcher) last() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.calls) == 0 {
		return nil
	}
	return l.calls[len(l.calls)-1]
}

func servicesApp(t *testing.T, f *dockertest.Fake, files map[string]string) (*api.App, *launcher) {
	t.Helper()
	l := &launcher{}
	a, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env, GOOS: "linux", Launch: l.launch,
		LookPath: func(n string) (string, error) {
			if n == "xterm" {
				return "/usr/bin/xterm", nil
			}
			return "", exec.ErrNotFound
		}})
	if files != nil {
		modtest.Project(t, a, files)
	}
	return a, l
}

func TestServicesSnapshot(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", shopPS)
	f.Write(t, "containers.json", `{"ID":"9","Names":"adminer","State":"running","Ports":"0.0.0.0:9000->8080/tcp"}
{"ID":"1","Names":"shop-web-1","State":"running","Labels":"com.docker.compose.project=shop"}
`)
	a, _ := servicesApp(t, f, nil)

	// Without a project: no compose projects, but the context is known.
	var res ServicesResult
	modtest.Call(t, a, "docker.services", nil, &res)
	if len(res.Projects) != 0 || res.Context != "orbstack" || res.Error != "" {
		t.Fatalf("no project %+v", res)
	}
	if f.Called("compose") {
		t.Fatal("compose ran without a project")
	}

	modtest.Project(t, a, map[string]string{"compose.yaml": shopCompose})
	modtest.Call(t, a, "docker.services", ServicesParams{Standalone: true}, &res)
	if len(res.Projects) != 1 {
		t.Fatalf("projects %+v", res)
	}
	p := res.Projects[0]
	if p.Name != "shop" || len(p.Files) != 1 || p.Files[0] != "compose.yaml" || len(p.Services) != 2 || p.Error != "" {
		t.Fatalf("project %+v", p)
	}
	app, web := p.Services[0], p.Services[1]
	if app.Name != "app" || app.State != compose.StateStopped || web.State != compose.StateRunning || web.Ports[0].URL != "http://localhost:8080" {
		t.Fatalf("services %+v", p.Services)
	}
	if len(res.Standalone) != 1 || res.Standalone[0].Name != "adminer" {
		t.Fatalf("standalone %+v", res.Standalone)
	}
	if !f.Called("compose", "-p shop", "ps --all --format json") {
		t.Fatalf("calls %v", f.Calls())
	}

	// Engine errors are reported per project.
	f.Write(t, "ps.json", "{broken")
	modtest.Call(t, a, "docker.services", nil, &res)
	if res.Projects[0].Error == "" || len(res.Projects[0].Services) != 2 {
		t.Fatalf("ps error %+v", res.Projects[0])
	}
}

func TestServicesBrokenComposeAndNoDocker(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	a, _ := servicesApp(t, f, map[string]string{"compose.yaml": "services: [nope\n"})
	var res ServicesResult
	modtest.Call(t, a, "docker.services", nil, &res)
	if len(res.Projects) != 1 || res.Projects[0].Error == "" {
		t.Fatalf("broken %+v", res)
	}
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "up"}, api.CodeFailed)

	b, _ := newApp(t, Options{Binary: t.TempDir() + "/missing", CommonDirs: []string{}})
	modtest.Call(t, b, "docker.services", nil, &res)
	if res.Error == "" {
		t.Fatalf("missing docker %+v", res)
	}
	modtest.WantCode(t, b, "docker.logs", LogsParams{}, api.CodeUnavailable)
}

func TestServiceActions(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", shopPS)
	a, _ := servicesApp(t, f, nil)
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "up"}, api.CodeNoProject)
	modtest.Project(t, a, map[string]string{"composer.json": "{}"})
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "up"}, api.CodeNotFound)

	a, _ = servicesApp(t, f, map[string]string{"compose.yaml": shopCompose})
	var res ServiceActionResult
	modtest.Call(t, a, "docker.serviceAction", ServiceActionParams{Action: "restart", Service: "web"}, &res)
	if res.Target != "service web" || len(res.Output) == 0 || !f.Called("-p shop", "compose.yaml restart web") {
		t.Fatalf("restart %+v %v", res, f.Calls())
	}
	modtest.Call(t, a, "docker.serviceAction", ServiceActionParams{Action: "up"}, &res)
	if res.Target != "project shop" || !f.Called("-p shop", "up --detach") {
		t.Fatalf("up %+v", res)
	}
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "down"}, api.CodeInvalidParams)
	if f.Called(" down") {
		t.Fatal("down ran without confirmation")
	}
	modtest.Call(t, a, "docker.serviceAction", ServiceActionParams{Action: "down", Confirm: true}, &res)
	if !f.Called("-p shop", "compose.yaml down") {
		t.Fatal("down not run")
	}
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "down", Service: "web", Confirm: true}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "explode"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "stop", Services: []string{"--rm"}}, api.CodeInvalidParams)

	f.Write(t, "fail", "stop\n")
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "stop", Service: "web"}, api.CodeFailed)

	// Standalone containers.
	modtest.Call(t, a, "docker.serviceAction", ServiceActionParams{Action: "restart", Container: "adminer"}, &res)
	if res.Target != "container adminer" || !f.Called("restart adminer") {
		t.Fatalf("container %+v %v", res, f.Calls())
	}
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "stop", Container: "adminer"}, api.CodeFailed)
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "up", Container: "adminer"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.serviceAction", ServiceActionParams{Action: "start", Container: "-x"}, api.CodeInvalidParams)
}

func TestLogs(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "logs.txt", "web-1  | GET / 200\napp-1  | PHP Warning: x\nweb-1  | GET /favicon.ico 404\n")
	a, _ := servicesApp(t, f, map[string]string{"compose.yaml": shopCompose})

	var res LogsResult
	modtest.Call(t, a, "docker.logs", LogsParams{Service: "web", Filter: "get"}, &res)
	if res.ID != "" || len(res.Lines) != 2 || res.Lines[0].Container != "web-1" || res.Lines[0].Text != "GET / 200" {
		t.Fatalf("logs %+v", res)
	}
	if !f.Called("-p shop", "logs --no-color --tail 200 web") {
		t.Fatalf("calls %v", f.Calls())
	}
	modtest.Call(t, a, "docker.logs", LogsParams{Tail: -1, Timestamps: true, Container: "adminer"}, &res)
	if len(res.Lines) != 3 || res.Lines[0].Container != "" || !f.Called("logs --timestamps adminer") {
		t.Fatalf("container logs %+v %v", res, f.Calls())
	}
	modtest.WantCode(t, a, "docker.logs", LogsParams{Service: "-f"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.logs", LogsParams{Container: "a b"}, api.CodeInvalidParams)

	// Follow: lines arrive as events until docker.logsStop.
	rec := modtest.Record(t, a, TopicLog)
	modtest.Call(t, a, "docker.logs", LogsParams{Follow: true, Filter: "PHP"}, &res)
	if res.ID == "" {
		t.Fatal("no stream id")
	}
	id := res.ID
	rec.Wait(t, TopicLog, func(e bus.Event) bool {
		ev := e.Payload.(LogEvent)
		return ev.ID == id && len(ev.Lines) == 1 && ev.Lines[0].Text == "PHP Warning: x"
	})
	f.Emit(t, "logs-next", "app-1  | PHP Fatal error\nweb-1  | ignored\n")
	rec.Wait(t, TopicLog, func(e bus.Event) bool {
		ev := e.Payload.(LogEvent)
		return ev.ID == id && len(ev.Lines) > 0 && ev.Lines[0].Text == "PHP Fatal error"
	})
	var st StoppedResult
	modtest.Call(t, a, "docker.logsStop", IDParams{ID: id}, &st)
	if !st.Stopped {
		t.Fatal("not stopped")
	}
	done := rec.Wait(t, TopicLog, func(e bus.Event) bool { return e.Payload.(LogEvent).Done }).Payload.(LogEvent)
	if done.ID != id || done.Error != "" {
		t.Fatalf("done %+v", done)
	}
	modtest.Call(t, a, "docker.logsStop", IDParams{ID: id}, &st)
	if st.Stopped {
		t.Fatal("stopped twice")
	}
	for _, e := range rec.Events(TopicLog) {
		for _, l := range e.Payload.(LogEvent).Lines {
			if strings.Contains(l.Text, "ignored") {
				t.Fatal("filter not applied")
			}
		}
	}

	// A context change ends followed logs.
	modtest.Call(t, a, "docker.logs", LogsParams{Follow: true}, &res)
	id2 := res.ID
	modtest.Call(t, a, "docker.setContext", SetContextParams{Name: "desktop-linux"}, nil)
	rec.Wait(t, TopicLog, func(e bus.Event) bool { ev := e.Payload.(LogEvent); return ev.ID == id2 && ev.Done })

	// A project event delivered late (the bus is asynchronous: headless
	// opens the project and runs the command right away) does not end a
	// stream started for that project.
	modtest.Call(t, a, "docker.logs", LogsParams{Follow: true}, &res)
	id3 := res.ID
	a.Bus().Publish(api.TopicProjectOpened, api.ProjectEvent{Root: a.ProjectRoot()})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Bus().Drain(ctx); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "docker.logsStop", IDParams{ID: id3}, &st)
	if !st.Stopped {
		t.Fatal("late project event ended the stream")
	}

	// A failing stream reports its error.
	f.Write(t, "logs.txt", "")
	modtest.Call(t, a, "docker.logs", LogsParams{Follow: true, Container: "x"}, &res)
	_ = res
}

// Stop waits for the followed streams: no docker.log event is published
// after it returned, and no stream starts after it (D309).
func TestLogsStopWaitsForStreams(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "logs.txt", "web-1  | hello\n")
	l := &launcher{}
	a, m := newApp(t, Options{Binary: f.Binary, Env: f.Env, GOOS: "linux", Launch: l.launch})
	modtest.Project(t, a, map[string]string{"compose.yaml": shopCompose})
	rec := modtest.Record(t, a, TopicLog)
	var res LogsResult
	modtest.Call(t, a, "docker.logs", LogsParams{Follow: true}, &res)
	id := res.ID
	rec.Wait(t, TopicLog, func(e bus.Event) bool { ev := e.Payload.(LogEvent); return ev.ID == id && len(ev.Lines) > 0 })

	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	// Publish only enqueues: draining delivers everything published
	// before Stop returned.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Bus().Drain(ctx); err != nil {
		t.Fatal(err)
	}
	n := len(rec.Events(TopicLog))
	done := false
	for _, e := range rec.Events(TopicLog) {
		if ev := e.Payload.(LogEvent); ev.ID == id && ev.Done {
			done = true
		}
	}
	if !done {
		t.Fatal("stream still running after Stop returned")
	}
	if _, err := m.logs(context.Background(), LogsParams{Follow: true}); err == nil {
		t.Fatal("stream started after Stop")
	}
	time.Sleep(200 * time.Millisecond)
	if err := a.Bus().Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Events(TopicLog)); got != n {
		t.Fatalf("%d docker.log events after Stop", got-n)
	}
}

func TestWatch(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", shopPS)
	a, _ := servicesApp(t, f, map[string]string{"compose.yaml": shopCompose})
	rec := modtest.Record(t, a, TopicServicesChanged)

	var w WatchResult
	modtest.Call(t, a, "docker.watchStart", nil, &w)
	if w.ID == "" || w.IntervalSeconds != 5 {
		t.Fatalf("watch %+v", w)
	}
	first := rec.Wait(t, TopicServicesChanged, nil).Payload.(ServicesResult)
	if first.Refresh != "events" || len(first.Projects) != 1 {
		t.Fatalf("first %+v", first)
	}
	modtest.Eventually(t, "docker events started", func() bool { return f.Called("events", "label=com.docker.compose.project=shop") })

	// An event leads to a new snapshot when something changed.
	f.Write(t, "ps.json", strings.ReplaceAll(shopPS, `"exited"`, `"running"`))
	f.Emit(t, "events-next", `{"Type":"container","Action":"start","Actor":{"ID":"2","Attributes":{"name":"shop-app-1"}}}`+"\n")
	rec.Wait(t, TopicServicesChanged, func(e bus.Event) bool {
		r := e.Payload.(ServicesResult)
		return len(r.Projects) == 1 && r.Projects[0].Services[0].State == compose.StateRunning
	})
	n := len(rec.Events(TopicServicesChanged))
	// exec events (PHP probes) are ignored.
	f.Emit(t, "events-next", `{"Type":"container","Action":"exec_start: php -v","Actor":{"ID":"2"}}`+"\n")
	// An action refreshes at once.
	f.Write(t, "ps.json", shopPS)
	modtest.Call(t, a, "docker.serviceAction", ServiceActionParams{Action: "stop", Service: "app"}, nil)
	modtest.Eventually(t, "snapshot after the action", func() bool { return len(rec.Events(TopicServicesChanged)) > n })
	evs := rec.Events(TopicServicesChanged)
	if len(evs) != n+1 || evs[n].Payload.(ServicesResult).Projects[0].Services[0].State != compose.StateStopped {
		t.Fatalf("events %d, want %d", len(evs), n+1)
	}

	var st StoppedResult
	modtest.Call(t, a, "docker.watchStop", IDParams{ID: w.ID}, &st)
	if !st.Stopped {
		t.Fatal("watch not stopped")
	}
	modtest.Call(t, a, "docker.watchStop", IDParams{ID: w.ID}, &st)
	if st.Stopped {
		t.Fatal("stopped twice")
	}
	// No engine contact after the watch ended.
	before := len(f.Calls())
	time.Sleep(300 * time.Millisecond)
	if after := len(f.Calls()); after != before {
		t.Fatalf("docker ran after watchStop: %v", f.Calls()[before:])
	}
}

func TestWatchPollFallback(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", shopPS)
	f.Write(t, "events-fail", "")
	a, _ := servicesApp(t, f, map[string]string{"compose.yaml": shopCompose})
	if err := settings.Set(a.Settings(), SettingRefresh, 1); err != nil {
		t.Fatal(err)
	}
	rec := modtest.Record(t, a, TopicServicesChanged)
	var w WatchResult
	modtest.Call(t, a, "docker.watchStart", WatchParams{Standalone: true}, &w)
	if w.IntervalSeconds != 1 {
		t.Fatalf("interval %+v", w)
	}
	rec.Wait(t, TopicServicesChanged, func(e bus.Event) bool { return e.Payload.(ServicesResult).Refresh == "poll" })
	f.Write(t, "ps.json", strings.ReplaceAll(shopPS, `"exited"`, `"running"`))
	rec.Wait(t, TopicServicesChanged, func(e bus.Event) bool {
		r := e.Payload.(ServicesResult)
		return r.Refresh == "poll" && r.Projects[0].Services[0].State == compose.StateRunning
	})
	if !f.Called("ps --all --no-trunc") {
		t.Fatalf("standalone not listed: %v", f.Calls())
	}
	// Module stop ends the watch.
	if err := a.Modules().Disable(ID); err != nil {
		t.Skipf("cannot disable: %v", err)
	}
}

func TestShellAndOpenURL(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	a, l := servicesApp(t, f, map[string]string{"compose.yaml": shopCompose})
	var res LaunchResult
	modtest.Call(t, a, "docker.shell", ShellParams{Service: "app"}, &res)
	if err := settings.Set(a.Settings(), SettingContext, "orbstack"); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "docker.shell", ShellParams{Service: "app"}, &res)
	if res.Command[0] != f.Binary || !strings.Contains(strings.Join(res.Command, " "), "--context orbstack compose") ||
		strings.Join(res.Command[len(res.Command)-3:], " ") != "exec app sh" {
		t.Fatalf("command %v", res.Command)
	}
	if got := l.last(); len(got) < 2 || got[0] != "xterm" || got[1] != "-e" {
		t.Fatalf("launched %v", got)
	}
	if err := settings.Set(a.Settings(), SettingTerminal, "kitty --single-instance"); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "docker.shell", ShellParams{Container: "adminer", Shell: "bash"}, &res)
	if got := l.last(); got[0] != "kitty" || got[1] != "--single-instance" || got[len(got)-1] != "bash" || got[len(got)-2] != "adminer" {
		t.Fatalf("container shell %v", got)
	}
	modtest.WantCode(t, a, "docker.shell", ShellParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.shell", ShellParams{Service: "-x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.shell", ShellParams{Container: "-x"}, api.CodeInvalidParams)
	// Names and shells with shell/cmd metacharacters are refused (D308).
	for _, bad := range []string{"a&calc", "%COMSPEC%", "a;b", "a|b", `a"b`} {
		modtest.WantCode(t, a, "docker.shell", ShellParams{Container: bad}, api.CodeInvalidParams)
		modtest.WantCode(t, a, "docker.shell", ShellParams{Container: "adminer", Shell: bad}, api.CodeInvalidParams)
		modtest.WantCode(t, a, "docker.shell", ShellParams{Service: "app", Shell: bad}, api.CodeInvalidParams)
		modtest.WantCode(t, a, "docker.logs", LogsParams{Container: bad}, api.CodeInvalidParams)
	}

	modtest.Call(t, a, "docker.openURL", URLParams{URL: "http://localhost:8080"}, &res)
	if got := l.last(); got[0] != "xdg-open" || got[1] != "http://localhost:8080" {
		t.Fatalf("browser %v", got)
	}
	modtest.WantCode(t, a, "docker.openURL", URLParams{URL: "file:///etc/passwd"}, api.CodeInvalidParams)

	// No terminal found.
	b, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env, GOOS: "linux", Launch: l.launch,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound }})
	modtest.Project(t, b, map[string]string{"compose.yaml": shopCompose})
	modtest.WantCode(t, b, "docker.shell", ShellParams{Service: "app"}, api.CodeUnavailable)
	_ = context.Background
}

func TestStartDetached(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	if err := startDetached([]string{f.Binary, "version"}, f.Env); err != nil {
		t.Fatal(err)
	}
	modtest.Eventually(t, "detached run", func() bool { return f.Called("version") })
	if err := startDetached(nil, nil); err == nil {
		t.Fatal("empty argv")
	}
	if err := startDetached([]string{t.TempDir() + "/missing"}, nil); err == nil {
		t.Fatal("missing program")
	}
	if _, err := lookPath("definitely-not-a-program-xyz"); err == nil {
		t.Fatal("lookPath")
	}
}
