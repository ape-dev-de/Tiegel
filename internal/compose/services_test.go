package compose

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
)

const stackYAML = `services:
  web:
    image: nginx
    ports: ["8080:80"]
  app:
    image: php:8.3-fpm
  worker:
    image: php:8.3-cli
    profiles: [jobs]
`

func TestStackArgs(t *testing.T) {
	s := Stack{Project: "shop", Dir: "/p", Files: []string{"/p/compose.yaml"}, Profiles: []string{"dev"}, Client: docker.Client{Binary: "/bin/docker", Context: "orb"}}
	base := []string{"compose", "--project-directory", "/p", "-p", "shop", "-f", "/p/compose.yaml", "--profile", "dev"}
	if !reflect.DeepEqual(s.BaseArgs(), base) {
		t.Fatalf("base %v", s.BaseArgs())
	}
	for _, c := range []struct {
		action   string
		services []string
		tail     []string
	}{
		{ActionUp, nil, []string{"up", "--detach"}},
		{ActionUp, []string{"web"}, []string{"up", "--detach", "web"}},
		{ActionStop, []string{"web", "app"}, []string{"stop", "web", "app"}},
		{ActionStart, nil, []string{"start"}},
		{ActionRestart, []string{"app"}, []string{"restart", "app"}},
		{ActionDown, nil, []string{"down"}},
	} {
		got, err := s.ActionArgs(c.action, c.services...)
		if err != nil || !reflect.DeepEqual(got[len(base):], c.tail) {
			t.Errorf("%s %v: %v %v", c.action, c.services, got, err)
		}
	}
	for _, bad := range []struct {
		action   string
		services []string
	}{{"rm", nil}, {ActionDown, []string{"web"}}, {ActionStop, []string{"--all"}}, {ActionStop, []string{""}}, {ActionStop, []string{"a b"}}} {
		if _, err := s.ActionArgs(bad.action, bad.services...); err == nil {
			t.Errorf("%s %v accepted", bad.action, bad.services)
		}
	}
	logs, err := s.LogsArgs(LogOptions{Services: []string{"web"}, Tail: 50, Follow: true, Timestamps: true})
	if err != nil || !reflect.DeepEqual(logs[len(base):], []string{"logs", "--no-color", "--tail", "50", "--follow", "--timestamps", "web"}) {
		t.Fatalf("logs %v %v", logs, err)
	}
	logs, _ = s.LogsArgs(LogOptions{Tail: -1})
	if !reflect.DeepEqual(logs[len(base):], []string{"logs", "--no-color"}) {
		t.Fatalf("logs all %v", logs)
	}
	if _, err := s.LogsArgs(LogOptions{Services: []string{"-x"}}); err == nil {
		t.Fatal("bad service in logs")
	}
	ex, err := s.ExecArgs("app", false, "sh", "-c", "x")
	if err != nil || !reflect.DeepEqual(ex[len(base):], []string{"exec", "-T", "app", "sh", "-c", "x"}) {
		t.Fatalf("exec %v %v", ex, err)
	}
	if ex, _ = s.ExecArgs("app", true, "sh"); !reflect.DeepEqual(ex[len(base):], []string{"exec", "app", "sh"}) {
		t.Fatalf("exec tty %v", ex)
	}
	if _, err := s.ExecArgs("-x", true, "sh"); err == nil {
		t.Fatal("bad exec service")
	}
	if _, err := s.ExecArgs("app", true); err == nil {
		t.Fatal("exec without command")
	}
	sh, err := s.ShellArgs("app", "")
	if err != nil || sh[0] != "/bin/docker" || !reflect.DeepEqual(sh[1:3], []string{"--context", "orb"}) || !reflect.DeepEqual(sh[len(sh)-3:], []string{"exec", "app", "sh"}) {
		t.Fatalf("shell %v %v", sh, err)
	}
	if sh, _ := s.ShellArgs("app", "bash"); sh[len(sh)-1] != "bash" {
		t.Fatalf("bash %v", sh)
	}
	if _, err := s.ShellArgs("-x", ""); err == nil {
		t.Fatal("bad shell service")
	}
	// Names reach a terminal command line: only a safe charset (D308).
	for _, bad := range []string{"a&b", "a|b", "%PATH%", "a;b", `a"b`, "a b", "a^b", ".x", "_x", "a\nb"} {
		if _, err := s.ShellArgs(bad, ""); err == nil {
			t.Errorf("service %q accepted", bad)
		}
		if _, err := s.ShellArgs("app", bad); err == nil {
			t.Errorf("shell %q accepted", bad)
		}
	}
	for _, sh := range []string{"bash", "/bin/sh", "/usr/local/bin/zsh-5.9"} {
		if _, err := s.ShellArgs("my_app.v2-x", sh); err != nil {
			t.Errorf("shell %q: %v", sh, err)
		}
	}
	if _, err := s.ShellArgs("app", "//x"); err == nil {
		t.Error("shell //x accepted")
	}
	// The interpreter shares the base arguments.
	ip := &Interpreter{Project: "shop", Dir: "/p", Files: []string{"/p/compose.yaml"}, Profiles: []string{"dev"}}
	if !reflect.DeepEqual(ip.BaseArgs(), base) {
		t.Fatalf("interpreter base %v", ip.BaseArgs())
	}
}

func TestParseLogLine(t *testing.T) {
	for in, want := range map[string][2]string{
		"web-1  | GET / 200":    {"web-1", "GET / 200"},
		"web-1 | a | b":         {"web-1", "a | b"},
		"app-1  |":              {"app-1", ""},
		"plain line":            {"", "plain line"},
		"foo|bar":               {"", "foo|bar"},
		" | x":                  {"", " | x"},
		"two words | not a pfx": {"", "two words | not a pfx"},
	} {
		c, txt := ParseLogLine(in)
		if c != want[0] || txt != want[1] {
			t.Errorf("ParseLogLine(%q) = %q, %q; want %q", in, c, txt, want)
		}
	}
}

func TestMergeServices(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", stackYAML)
	p, err := Load(context.Background(), dir, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	states, err := ParsePS([]byte(`{"ID":"1","Name":"shop-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"},{"URL":"::","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"ID":"2","Name":"shop-web-2","Service":"web","State":"running","Health":"unhealthy","Publishers":[{"URL":"","TargetPort":443,"PublishedPort":0,"Protocol":""}]}
{"ID":"3","Name":"shop-app-1","Service":"app","State":"exited","ExitCode":137}
{"ID":"4","Name":"shop-old-1","Service":"old","State":"running","Image":"busybox"}
`))
	if err != nil {
		t.Fatal(err)
	}
	svcs := MergeServices(p, states, "")
	names := []string{}
	for _, s := range svcs {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"app", "web", "old"}) {
		t.Fatalf("names %v (worker is in an inactive profile)", names)
	}
	app, web, old := svcs[0], svcs[1], svcs[2]
	if app.State != StateStopped || app.Containers[0].ExitCode != 137 || !app.Defined || app.Image != "php:8.3-fpm" {
		t.Fatalf("app %+v", app)
	}
	if web.State != StateRunning || web.Health != "unhealthy" || len(web.Containers) != 2 || len(web.Ports) != 2 ||
		web.Ports[0].Target != 443 || web.Ports[1].URL != "http://localhost:8080" {
		t.Fatalf("web %+v", web)
	}
	if old.Defined || old.State != StateRunning || old.Image != "busybox" {
		t.Fatalf("old %+v", old)
	}
	// Without containers every defined service is missing.
	for _, s := range MergeServices(p, nil, "") {
		if s.State != StateMissing || len(s.Containers) != 0 {
			t.Fatalf("missing %+v", s)
		}
	}
	if got := MergeServices(nil, states[:1], ""); len(got) != 1 || got[0].Defined {
		t.Fatalf("nil project %+v", got)
	}
}

func TestStackRuns(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"Name":"shop-web-1","Service":"web","State":"running"}`+"\n")
	s := Stack{Project: "shop", Dir: t.TempDir(), Client: docker.Client{Binary: f.Binary, Env: f.Env, Context: "orbstack"}}
	ctx := context.Background()
	states, err := s.PS(ctx)
	if err != nil || len(states) != 1 || states[0].Name != "shop-web-1" {
		t.Fatalf("ps %+v %v", states, err)
	}
	out, err := s.Action(ctx, ActionRestart, "web")
	if err != nil || len(out) != 1 || !strings.Contains(out[0], "restart") {
		t.Fatalf("restart %v %v", out, err)
	}
	if !f.Called("--context orbstack compose", "-p shop restart web") {
		t.Fatalf("calls %v", f.Calls())
	}
	f.Write(t, "fail", "up\n")
	if _, err := s.Action(ctx, ActionUp); err == nil || !strings.Contains(err.Error(), "up failed") {
		t.Fatalf("up %v", err)
	}
	if _, err := s.Action(ctx, "nope"); err == nil {
		t.Fatal("unknown action ran")
	}
	f.Write(t, "ps.json", "{broken")
	if _, err := s.PS(ctx); err == nil {
		t.Fatal("broken ps")
	}
	if _, err := (Stack{Client: docker.Client{Binary: t.TempDir() + "/missing"}}).PS(ctx); err == nil {
		t.Fatal("missing docker")
	}
	_ = NewStack(&Project{Name: "x", Dir: "/d", Files: []string{"a"}}, docker.Client{})
}

// TestStackDockerSmoke talks to a real Docker engine when one is available
// (OrbStack, Docker Desktop, …). It only lists containers, reads logs of a
// project without containers and listens to events briefly: no image, no
// container, no network.
func TestStackDockerSmoke(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
	dir := t.TempDir()
	write(t, dir, "compose.yaml", stackYAML)
	p, err := Load(ctx, dir, LoadOptions{Name: "tiegel-smoke-" + strings.ToLower(filepath.Base(dir))})
	if err != nil {
		t.Fatal(err)
	}
	s := NewStack(p, docker.Client{})
	states, err := s.PS(ctx)
	if err != nil || len(states) != 0 {
		t.Fatalf("ps %v %v", states, err)
	}
	for _, svc := range MergeServices(p, states, "") {
		if svc.State != StateMissing {
			t.Fatalf("fresh service %+v", svc)
		}
	}
	args, _ := s.LogsArgs(LogOptions{Tail: 10})
	if err := docker.Stream(s.Client.Command(ctx, args...), nil); err != nil {
		t.Fatalf("logs: %v", err)
	}
	ectx, ecancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer ecancel()
	if err := s.Client.Events(ectx, p.Name, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("events: %v", err)
	}
	if _, err := s.Client.Containers(ctx, ""); err != nil {
		t.Fatalf("containers: %v", err)
	}
}
