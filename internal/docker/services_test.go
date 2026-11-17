package docker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
)

func TestPortURLAndParsePorts(t *testing.T) {
	cases := []struct {
		ip, proto, host string
		pub             int
		want            string
	}{
		{"0.0.0.0", "tcp", "", 8080, "http://localhost:8080"},
		{"::", "tcp", "", 8080, "http://localhost:8080"},
		{"127.0.0.1", "tcp", "", 80, "http://127.0.0.1:80"},
		{"::1", "tcp", "", 80, "http://[::1]:80"},
		{"", "tcp", "dev.box", 443, "http://dev.box:443"},
		{"0.0.0.0", "udp", "", 53, ""},
		{"0.0.0.0", "tcp", "", 0, ""},
	}
	for _, c := range cases {
		if got := docker.PortURL(c.ip, c.pub, c.proto, c.host); got != c.want {
			t.Errorf("PortURL(%q,%d,%q,%q) = %q, want %q", c.ip, c.pub, c.proto, c.host, got, c.want)
		}
	}
	ports := docker.ParsePorts("0.0.0.0:8080->80/tcp, :::8080->80/tcp, 9000/tcp, 127.0.0.1:5000-5001->6000-6001/tcp, 0.0.0.0:53->53/udp, bogus, x->y/tcp", "")
	want := []docker.Port{
		{Target: 9000, Protocol: "tcp"},
		{IP: "0.0.0.0", Published: 53, Target: 53, Protocol: "udp"},
		{IP: "127.0.0.1", Published: 5000, Target: 6000, Protocol: "tcp", URL: "http://127.0.0.1:5000"},
		{IP: "127.0.0.1", Published: 5001, Target: 6001, Protocol: "tcp", URL: "http://127.0.0.1:5001"},
		{IP: "0.0.0.0", Published: 8080, Target: 80, Protocol: "tcp", URL: "http://localhost:8080"},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports\n%+v\nwant\n%+v", ports, want)
	}
	if got := docker.ParsePorts("", ""); len(got) != 0 {
		t.Fatalf("empty %v", got)
	}
}

func TestParseContainersAndLabels(t *testing.T) {
	lines := `{"ID":"abc","Names":"web,alias","Image":"nginx","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:8080->80/tcp","Labels":"com.docker.compose.project=shop,com.docker.compose.service=web"}
{"ID":"def","Names":"adminer","Image":"adminer","State":"exited","Status":"Exited (0)","Ports":"","Labels":""}
`
	list, err := docker.ParseContainers([]byte(lines), "remote.host")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %+v", err, list)
	}
	if a := list[0]; a.Name != "adminer" || a.ComposeProject != "" || a.State != "exited" {
		t.Fatalf("sorted %+v", a)
	}
	if w := list[1]; w.Name != "web" || w.ComposeProject != "shop" || w.ComposeService != "web" || len(w.Ports) != 1 || w.Ports[0].URL != "http://remote.host:8080" {
		t.Fatalf("web %+v", w)
	}
	arr, err := docker.ParseContainers([]byte(`[{"ID":"x","Names":"x"}]`), "")
	if err != nil || len(arr) != 1 {
		t.Fatalf("array %v %v", arr, err)
	}
	if _, err := docker.ParseContainers([]byte("{nope"), ""); err == nil {
		t.Fatal("bad line accepted")
	}
	if _, err := docker.ParseContainers([]byte("[nope"), ""); err == nil {
		t.Fatal("bad array accepted")
	}
	if l := docker.ParseLabels("a=1,b=x=y,,c"); l["a"] != "1" || l["b"] != "x=y" || l["c"] != "" || len(l) != 3 {
		t.Fatalf("labels %v", l)
	}
}

func TestLinkHost(t *testing.T) {
	for in, want := range map[string]string{
		"unix:///var/run/docker.sock": "localhost",
		"tcp://10.0.0.5:2376":         "10.0.0.5",
		"ssh://me@build.example.com":  "build.example.com",
		"ssh://me@build:22/path":      "build",
		"tcp://[fd00::1]:2376":        "fd00::1",
		"npipe:////./pipe/docker":     "localhost",
	} {
		if got := docker.LinkHost(in); got != want {
			t.Errorf("LinkHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEventAndRelevant(t *testing.T) {
	e, err := docker.ParseEvent([]byte(`{"status":"start","id":"abc","Type":"container","Action":"start","Actor":{"ID":"abc","Attributes":{"name":"shop-web-1","com.docker.compose.project":"shop","com.docker.compose.service":"web"}}}`))
	if err != nil || e.Action != "start" || e.ID != "abc" || e.Project != "shop" || e.Service != "web" || e.Name != "shop-web-1" || !e.Relevant() {
		t.Fatalf("%+v %v", e, err)
	}
	old, err := docker.ParseEvent([]byte(`{"status":"die","id":"x"}`))
	if err != nil || old.Action != "die" || old.ID != "x" {
		t.Fatalf("old format %+v %v", old, err)
	}
	for _, a := range []string{"exec_create: php -v", "exec_start: sh", "exec_die", "attach", "resize", "top", ""} {
		if (docker.Event{Action: a}).Relevant() {
			t.Errorf("%q relevant", a)
		}
	}
	if !(docker.Event{Action: "health_status: unhealthy"}).Relevant() {
		t.Error("health not relevant")
	}
	if _, err := docker.ParseEvent([]byte("nope")); err == nil {
		t.Fatal("bad event accepted")
	}
	if got := docker.EventsArgs("shop"); !reflect.DeepEqual(got[len(got)-2:], []string{"--filter", "label=com.docker.compose.project=shop"}) {
		t.Fatalf("args %v", got)
	}
	if got := docker.EventsArgs(""); len(got) != 5 {
		t.Fatalf("args %v", got)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "logs.txt", "web-1  | hello\nweb-1  | world\n")
	c := client(f)
	var mu sync.Mutex
	var got []docker.Line
	err := docker.Stream(c.Command(context.Background(), "compose", "logs"), func(l docker.Line) {
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	})
	if err != nil || len(got) != 2 || got[0].Text != "web-1  | hello" || got[0].Stream != "stdout" {
		t.Fatalf("%v %+v", err, got)
	}
	// A failing command reports its last stderr line.
	f.Write(t, "fail", "stop\n")
	var lines []docker.Line
	err = docker.Stream(c.Command(context.Background(), "stop", "x"), func(l docker.Line) { lines = append(lines, l) })
	if err == nil || !strings.Contains(err.Error(), "stop failed") || len(lines) != 1 || lines[0].Stream != "stderr" {
		t.Fatalf("fail %v %+v", err, lines)
	}
	// Cancelling a follow kills it.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	first := make(chan struct{}, 1)
	go func() {
		done <- docker.Stream(c.Command(ctx, "compose", "logs", "--follow"), func(docker.Line) {
			select {
			case first <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("no output")
	}
	f.Emit(t, "logs-next", "web-1  | more\n")
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("follow not cancelled")
	}
	// Start failure.
	if err := docker.Stream(docker.Client{Binary: filepath.Join(t.TempDir(), "missing")}.Command(context.Background(), "ps"), nil); err == nil {
		t.Fatal("missing binary started")
	}
}

func TestContainersAndEvents(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "containers.json", `{"ID":"abc","Names":"web","State":"running","Ports":"0.0.0.0:80->80/tcp"}`+"\n")
	c := client(f)
	list, err := c.Containers(context.Background(), "")
	if err != nil || len(list) != 1 || list[0].Ports[0].URL != "http://localhost:80" {
		t.Fatalf("%v %+v", err, list)
	}
	f.Write(t, "events.json", `{"Type":"container","Action":"start","Actor":{"ID":"a","Attributes":{"name":"web"}}}`+"\nnot json\n")
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan docker.Event, 4)
	done := make(chan error, 1)
	go func() { done <- c.Events(ctx, "shop", func(e docker.Event) { got <- e }) }()
	select {
	case e := <-got:
		if e.Name != "web" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	f.Emit(t, "events-next", `{"Type":"container","Action":"die","Actor":{"ID":"a"}}`+"\n")
	select {
	case e := <-got:
		if e.Action != "die" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no second event")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("done %v", err)
	}
	if !f.Called("events", "label=com.docker.compose.project=shop") {
		t.Fatalf("calls %v", f.Calls())
	}
	f.Write(t, "events-fail", "")
	if err := c.Events(context.Background(), "", nil); err == nil {
		t.Fatal("failing events")
	}
}

func TestTerminalCommand(t *testing.T) {
	argv := []string{"/usr/bin/docker", "--context", "orb stack", "compose", "exec", "app", "sh"}
	none := func(string) (string, error) { return "", os.ErrNotExist }
	only := func(name string) func(string) (string, error) {
		return func(n string) (string, error) {
			if n == name {
				return "/usr/bin/" + n, nil
			}
			return "", os.ErrNotExist
		}
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	got, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "linux", Getenv: env(nil), LookPath: only("gnome-terminal")}, argv)
	if err != nil || !reflect.DeepEqual(got[:2], []string{"gnome-terminal", "--"}) || got[len(got)-1] != "sh" {
		t.Fatalf("gnome %v %v", got, err)
	}
	got, _ = docker.TerminalCommand(docker.TerminalOptions{GOOS: "linux", Getenv: env(map[string]string{"TERMINAL": "foot"}), LookPath: none}, argv)
	if !reflect.DeepEqual(got[:2], []string{"foot", "-e"}) {
		t.Fatalf("$TERMINAL %v", got)
	}
	got, _ = docker.TerminalCommand(docker.TerminalOptions{GOOS: "linux", Terminal: "wezterm start --", LookPath: none}, argv)
	if !reflect.DeepEqual(got[:3], []string{"wezterm", "start", "--"}) || len(got) != 3+len(argv) {
		t.Fatalf("setting %v", got)
	}
	if _, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "linux", Getenv: env(nil), LookPath: none}, argv); !errors.Is(err, docker.ErrNoTerminal) {
		t.Fatalf("none %v", err)
	}
	// Windows: cmd.exe only, with one pre-quoted line (D307).
	for _, lp := range []func(string) (string, error){only("wt"), none} {
		got, err = docker.TerminalCommand(docker.TerminalOptions{GOOS: "windows", LookPath: lp}, argv)
		if err != nil || !docker.WindowsRawCmd(got) || got[len(got)-1] != `""/usr/bin/docker" --context ^"orb stack^" compose exec app sh"` {
			t.Fatalf("cmd %q %v", got, err)
		}
	}
	if docker.WindowsRawCmd(argv) || docker.WindowsRawCmd([]string{"cmd", "/d", "/v:on", "/s", "/k", "x"}) {
		t.Fatal("raw cmd misdetected")
	}
	if _, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "windows"}, []string{`C:\100%\docker.exe`}); err == nil {
		t.Fatal("program with % accepted")
	}
	got, _ = docker.TerminalCommand(docker.TerminalOptions{GOOS: "windows", Terminal: `C:\Apps\WT.exe`}, []string{"docker", "exec", "a;b"})
	if !reflect.DeepEqual(got, []string{`C:\Apps\WT.exe`, "docker", "exec", `a\;b`}) {
		t.Fatalf("wt setting %q", got)
	}
	got, _ = docker.TerminalCommand(docker.TerminalOptions{GOOS: "windows", Terminal: "alacritty -e"}, argv)
	if got[0] != "alacritty" {
		t.Fatalf("windows setting %v", got)
	}
	if _, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "linux"}, nil); err == nil {
		t.Fatal("empty argv")
	}

	dir := t.TempDir()
	got, err = docker.TerminalCommand(docker.TerminalOptions{GOOS: "darwin", TempDir: dir, Terminal: "iTerm", Env: []string{"PATH=/opt/a b:/usr/bin", "bad key=x", "NOEQ"}}, argv)
	if err != nil || !reflect.DeepEqual(got[:3], []string{"open", "-a", "iTerm"}) {
		t.Fatalf("darwin %v %v", got, err)
	}
	script, err := os.ReadFile(got[3])
	if err != nil {
		t.Fatal(err)
	}
	s := string(script)
	if !strings.HasPrefix(s, "#!/bin/sh\nrm -f \"$0\"\n") || !strings.Contains(s, "export PATH='/opt/a b:/usr/bin'\n") ||
		!strings.Contains(s, "exec /usr/bin/docker --context 'orb stack' compose exec app sh\n") || strings.Contains(s, "bad key") {
		t.Fatalf("script %q", s)
	}
	got, _ = docker.TerminalCommand(docker.TerminalOptions{GOOS: "darwin", TempDir: dir}, argv)
	if got[2] != "Terminal" {
		t.Fatalf("default app %v", got)
	}
	if _, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "darwin", TempDir: filepath.Join(dir, "missing")}, argv); err == nil {
		t.Fatal("unwritable temp dir")
	}
	if q := docker.ShellQuote("it's"); q != `'it'\''s'` {
		t.Fatalf("quote %s", q)
	}
	if q := docker.ShellQuote(""); q != "''" {
		t.Fatalf("quote empty %s", q)
	}
}

func TestBrowserCommand(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open", "windows": "rundll32", "linux": "xdg-open"} {
		got, err := docker.BrowserCommand(goos, "http://localhost:8080/x")
		if err != nil || got[0] != want || got[len(got)-1] != "http://localhost:8080/x" {
			t.Errorf("%s: %v %v", goos, got, err)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "http://", "::nope", "localhost:80"} {
		if _, err := docker.BrowserCommand("linux", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
