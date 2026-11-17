package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestMain(m *testing.M) {
	dockertest.Main()
	os.Exit(m.Run())
}

func newApp(t *testing.T, opts Options) (*api.App, *Module) {
	t.Helper()
	opts.NoLoginShell = true
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	m := New(opts)
	return modtest.NewApp(t, []module.Module{m}, nil), m
}

func TestSettingSpecs(t *testing.T) {
	specs := New(Options{}).SettingSpecs()
	if len(specs) != 5 {
		t.Fatalf("specs %+v", specs)
	}
	for _, s := range specs {
		if err := s.Validate(); err != nil || !strings.HasPrefix(s.Key, ID+".") {
			t.Errorf("%s: %v", s.Key, err)
		}
	}
	var _ module.SettingsProvider = New(Options{})
}

func TestContextsAndSelection(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	a, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env})
	rec := modtest.Record(t, a, api.TopicDockerContextChanged)

	var res ContextsResult
	modtest.Call(t, a, "docker.contexts", nil, &res)
	if len(res.Contexts) != 3 || res.Effective != "orbstack" || res.Source != "docker" || res.Selected != "" ||
		res.Binary.Path != f.Binary || res.Binary.Source != "option" {
		t.Fatalf("contexts %+v", res)
	}

	// Without a project the choice is global.
	var sc SetContextResult
	modtest.Call(t, a, "docker.setContext", SetContextParams{Name: "desktop-linux"}, &sc)
	if sc.Selected != "desktop-linux" || sc.Scope != "global" || sc.Source != "global" {
		t.Fatalf("global %+v", sc)
	}
	ev := rec.Wait(t, api.TopicDockerContextChanged, nil).Payload.(api.DockerContextChangedEvent)
	if ev.Context != "desktop-linux" || ev.Scope != "global" || ev.Previous != "" {
		t.Fatalf("event %+v", ev)
	}

	// A project overrides the global default.
	modtest.Project(t, a, map[string]string{"composer.json": "{}"})
	modtest.Call(t, a, "docker.setContext", SetContextParams{Name: "orbstack"}, &sc)
	if sc.Selected != "orbstack" || sc.Source != "project" || sc.Scope != "project" {
		t.Fatalf("project %+v", sc)
	}
	modtest.Call(t, a, "docker.contexts", nil, &res)
	if res.Selected != "orbstack" || res.Source != "project" || res.Effective != "orbstack" {
		t.Fatalf("after project %+v", res)
	}
	q, err := api.HookDockerEndpoint.Apply(context.Background(), a.Hooks(), api.DockerEndpointQuery{})
	if err != nil || q.Binary != f.Binary || q.Context != "orbstack" || len(q.Env) < 2 || !strings.Contains(strings.ToUpper(q.Env[0]), "PATH=") {
		t.Fatalf("hook %+v %v", q, err)
	}
	if q2, _ := api.HookDockerEndpoint.Apply(context.Background(), a.Hooks(), api.DockerEndpointQuery{Binary: "/mine"}); q2.Binary != "/mine" {
		t.Fatal("hook overrode a filled query")
	}
	modtest.Call(t, a, "docker.setContext", SetContextParams{Clear: true}, &sc)
	if sc.Selected != "desktop-linux" || sc.Source != "global" {
		t.Fatalf("clear project %+v", sc)
	}
	modtest.Call(t, a, "docker.setContext", SetContextParams{Clear: true, Global: true}, &sc)
	if sc.Selected != "" || sc.Source != "docker" {
		t.Fatalf("clear global %+v", sc)
	}
	modtest.WantCode(t, a, "docker.setContext", SetContextParams{Name: "nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "docker.setContext", SetContextParams{}, api.CodeInvalidParams)

	// Settings written elsewhere (settings dialog) are announced too.
	if err := settings.Set(a.ProjectSettings(), SettingContext, "default"); err != nil {
		t.Fatal(err)
	}
	rec.Wait(t, api.TopicDockerContextChanged, func(e bus.Event) bool {
		return e.Payload.(api.DockerContextChangedEvent).Context == "default"
	})
}

func TestStatus(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	a, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env})
	var st StatusResult
	modtest.Call(t, a, "docker.status", nil, &st)
	if !st.Reachable || st.Context != "orbstack" || st.Provider != "orbstack" || st.Version == nil || st.Version.Server != "27.3.1" ||
		!strings.Contains(st.Endpoint, ".orbstack") || st.Source != "docker" {
		t.Fatalf("status %+v", st)
	}
	f.Write(t, "down", "desktop-linux\n")
	modtest.Call(t, a, "docker.status", StatusParams{Context: "desktop-linux"}, &st)
	if st.Reachable || st.Provider != "docker-desktop" || !strings.Contains(st.Error, "Cannot connect") || st.Source != "param" {
		t.Fatalf("down %+v", st)
	}
	modtest.Call(t, a, "docker.status", StatusParams{Context: "nope"}, &st)
	if st.Reachable || !strings.Contains(st.Error, "unknown Docker context") {
		t.Fatalf("unknown %+v", st)
	}
	if !f.Called("--context orbstack version --format json") {
		t.Fatalf("calls %v", f.Calls())
	}
}

func TestDockerHost(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "contexts.json", `{"Current":true,"DockerEndpoint":"tcp://10.1.2.3:2376","Name":"default"}`+"\n")
	f.Write(t, "version.json", `{"Client":{"Version":"27"},"Server":{"Version":"27","Platform":{"Name":"Podman Engine"}}}`)
	env := map[string]string{"DOCKER_HOST": "tcp://10.1.2.3:2376"}
	a, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env, Getenv: func(k string) string { return env[k] }})
	var st StatusResult
	modtest.Call(t, a, "docker.status", nil, &st)
	if st.DockerHost != "tcp://10.1.2.3:2376" || !st.Remote || st.Endpoint != st.DockerHost || st.Provider != "podman" {
		t.Fatalf("docker host %+v", st)
	}
	var res ContextsResult
	modtest.Call(t, a, "docker.contexts", nil, &res)
	if res.DockerHost == "" || res.Effective != "default" || !res.Contexts[0].Remote {
		t.Fatalf("contexts %+v", res)
	}
}

func TestBinaryPathAndMissing(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	missing := filepath.Join(t.TempDir(), "docker")
	a, _ := newApp(t, Options{Binary: missing, Env: f.Env})
	modtest.WantCode(t, a, "docker.contexts", nil, api.CodeUnavailable)
	var st StatusResult
	modtest.Call(t, a, "docker.status", nil, &st)
	if st.Reachable || !strings.Contains(st.Error, "not found") || st.Binary.Path != "" {
		t.Fatalf("missing %+v", st)
	}
	// Hook without binary: only the context.
	q, _ := api.HookDockerEndpoint.Apply(context.Background(), a.Hooks(), api.DockerEndpointQuery{})
	if q.Binary != "" {
		t.Fatalf("hook %+v", q)
	}
	modtest.WantCode(t, a, "docker.setContext", SetContextParams{Name: "orbstack"}, api.CodeUnavailable)

	var b dk.Binary
	modtest.Call(t, a, "docker.setPath", SetPathParams{Path: f.Binary}, &b)
	if b.Path != f.Binary || b.Source != dk.SourceSetting {
		t.Fatalf("setPath %+v", b)
	}
	if got := settings.Get(a.Settings(), SettingPath, ""); got != f.Binary {
		t.Fatalf("stored %q", got)
	}
	var res ContextsResult
	modtest.Call(t, a, "docker.contexts", nil, &res)
	if res.Binary.Source != dk.SourceSetting || len(res.Contexts) != 3 {
		t.Fatalf("contexts %+v", res)
	}
	modtest.WantCode(t, a, "docker.setPath", SetPathParams{Path: missing}, api.CodeNotFound)
	modtest.WantCode(t, a, "docker.setPath", SetPathParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "docker.setPath", SetPathParams{Clear: true}, api.CodeUnavailable)
	if settings.Get(a.Settings(), SettingPath, "") != "" {
		t.Fatal("not cleared")
	}
}

func TestContextsListFailure(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "contexts.json", "not json")
	a, _ := newApp(t, Options{Binary: f.Binary, Env: f.Env})
	modtest.WantCode(t, a, "docker.contexts", nil, api.CodeUnavailable)
	// status still pings when the listing fails.
	var st StatusResult
	modtest.Call(t, a, "docker.status", nil, &st)
	if !st.Reachable {
		t.Fatalf("status %+v", st)
	}
}

func TestTimeoutSetting(t *testing.T) {
	a, m := newApp(t, Options{Binary: "/nonexistent"})
	if m.timeout().Seconds() != 5 {
		t.Fatal(m.timeout())
	}
	_ = settings.Set(a.Settings(), SettingTimeout, 0)
	if m.timeout().Seconds() != 1 {
		t.Fatal(m.timeout())
	}
	if m.getenv("PATH") != "" {
		t.Fatal("Getenv option ignored")
	}
	if New(Options{}).getenv("TIEGEL_SURELY_UNSET_VAR") != "" {
		t.Fatal("os getenv")
	}
}

func TestLoginShellPath(t *testing.T) {
	t.Parallel()
	asked := 0
	m := New(Options{LoginPath: func(context.Context) (string, error) { asked++; return t.TempDir(), nil }, CommonDirs: []string{}})
	_ = modtest.NewApp(t, []module.Module{m}, nil)
	// Whether the process PATH has docker depends on the machine; the
	// result is cached either way.
	b1, err1 := m.binary(context.Background())
	b2, err2 := m.binary(context.Background())
	if b1 != b2 || (err1 == nil) != (err2 == nil) || asked > 1 {
		t.Fatalf("cache %+v %+v asked %d", b1, b2, asked)
	}
}
