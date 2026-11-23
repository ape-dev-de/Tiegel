package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/modules"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestMain(m *testing.M) {
	dockertest.Main()
	os.Exit(checkSyncCalls(m.Run()))
}

// newDockerEnv is newEnv with the fake docker and a compose project.
func newDockerEnv(t *testing.T, f *dockertest.Fake, launch func(argv, env []string) error) *env {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("name: shop\nservices:\n  web:\n    image: nginx\n  app:\n    image: php:8.3-fpm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	opts := api.Options{
		ConfigDir: t.TempDir(),
		Interp:    interp.DiscoverOptions{Dirs: []string{}},
		LSP:       servers.DetectOptions{PathEnv: filepath.Join(home, "none"), HomeDir: home, ComposerHome: home, NoSystemDirs: true},
	}
	opts.Extensions = append(opts.Extensions, modules.Installer(modules.Options{Docker: dockermod.Options{
		Binary: f.Binary, Env: f.Env, NoLoginShell: true, GOOS: "linux", Launch: launch,
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
	}}))
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctl := controller.New(controller.NewInProcess(a))
	v := NewView(ctl, "darwin")
	e := &env{t: t, app: a, root: root, ctl: ctl, v: v, h: uitest.NewHarness(windowSize)}
	t.Cleanup(func() {
		v.Close()
		ctl.Close()
		_ = a.Close()
	})
	ctl.Start(root)
	e.settle()
	return e
}

func engineCalls(f *dockertest.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if strings.Contains(c, " ps ") || strings.HasPrefix(c, "events") || strings.Contains(c, " events") {
			n++
		}
	}
	return n
}

func TestServicesToolWindow(t *testing.T) {
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"ID":"1","Name":"shop-web-1","Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"ID":"2","Name":"shop-app-1","Service":"app","State":"exited"}
`)
	f.Write(t, "logs.txt", "web-1  | GET / 200\napp-1  | PHP Warning\n")
	f.Write(t, "containers.json", `{"ID":"9","Names":"adminer","State":"running","Ports":"0.0.0.0:9000->8080/tcp"}`+"\n")
	var mu sync.Mutex
	var launched [][]string
	e := newDockerEnv(t, f, func(argv, _ []string) error {
		mu.Lock()
		launched = append(launched, argv)
		mu.Unlock()
		return nil
	})
	d := e.v.svcd
	s := e.ctl.Services

	// Closed window: no engine contact.
	time.Sleep(100 * time.Millisecond)
	if n := engineCalls(f); n != 0 {
		t.Fatalf("engine contacted while closed: %v", f.Calls())
	}

	// Cmd+8 opens the window, the watch starts and the tree fills.
	if a, ok := e.v.Registry.Action(ActServicesView); !ok || a.Mac != "Cmd+8" || a.PC != "Alt+8" {
		t.Fatalf("toggle action %+v %v", a, ok)
	}
	e.v.RunAction(ActServicesView)
	e.waitFor("snapshot", func() bool { return s.Loaded && len(s.Snapshot.Projects) == 1 })
	if !s.Visible() {
		t.Fatal("not visible")
	}
	e.frame()
	keys := map[string]bool{}
	for _, n := range d.nodes {
		keys[n.key] = true
	}
	if !keys["ctx"] || !keys["p/shop"] || !keys["p/shop/web"] || !keys["p/shop/app"] {
		t.Fatalf("nodes %+v", d.nodes)
	}
	if name, _ := d.v.Registry.ToolWindow(ToolServices); name.Badge == nil {
		t.Fatal("no badge")
	} else if b, k := name.Badge(); b != "1" || k != atoms.BadgeSuccess {
		t.Fatalf("badge %q %v", b, k)
	}

	// Expand the service: containers appear.
	d.rows["p/shop/web"].Expanded = true
	e.frame()
	found := false
	for _, n := range d.nodes {
		found = found || n.kind == svcContainer && n.container == "shop-web-1"
	}
	if !found {
		t.Fatalf("container row missing %+v", d.nodes)
	}

	// Toolbar acts on the selection.
	d.sel = "p/shop/web"
	if svc, ctr := d.target(); svc != "web" || ctr != "" {
		t.Fatalf("target %q %q", svc, ctr)
	}
	items := d.toolbarItems()
	if items[2].Disabled || !strings.Contains(items[5].Label, "Service web") {
		t.Fatalf("items %+v", items)
	}
	d.run(svcRestart)
	e.waitFor("restart", func() bool { return f.Called("restart web") && s.Busy == "" })
	d.run(svcLogs)
	e.waitFor("logs", func() bool { return len(s.Logs.Lines) == 2 && s.Logs.ID != "" })
	if !strings.Contains(d.logTitle(), "web – following") {
		t.Fatalf("title %q", d.logTitle())
	}
	e.frame()
	d.run(svcShell)
	e.waitFor("shell", func() bool { mu.Lock(); defer mu.Unlock(); return len(launched) == 1 })
	// Down asks first.
	d.run(svcDown)
	if !e.v.PopupOpen() || f.Called("compose.yaml down") {
		t.Fatal("down without confirmation")
	}
	e.v.ClosePopup()
	// Other containers.
	d.run(svcStandalone)
	e.waitFor("standalone", func() bool { return len(s.Snapshot.Standalone) == 1 })
	e.frame()
	d.sel = "o/9"
	if svc, ctr := d.target(); svc != "" || ctr != "adminer" {
		t.Fatalf("standalone target %q %q", svc, ctr)
	}
	d.run(svcRefresh)
	e.settle()
	renderServices(t, e)
	d.contextMenu()
	e.frame()

	// Hiding stops the watch and the log stream: no more engine contact.
	e.v.HideTool(ToolServices)
	e.frame()
	e.waitFor("hidden", func() bool { return !s.Visible() && s.Logs.ID == "" })
	e.ctl.Settle(time.Second)
	time.Sleep(200 * time.Millisecond)
	before := len(f.Calls())
	time.Sleep(400 * time.Millisecond)
	if after := len(f.Calls()); after != before {
		t.Fatalf("docker ran after hiding: %v", f.Calls()[before:])
	}
}

// renderServices draws the workbench with the Services window; with
// TIEGEL_SHOTS set it writes services-<theme>.png there.
func renderServices(t *testing.T, e *env) {
	t.Helper()
	dir := os.Getenv("TIEGEL_SHOTS")
	for _, mode := range []string{"dark", "light"} {
		e.ctl.Prefs.Theme = mode
		e.frame()
		img, err := uitest.Render(e.v.th, windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return e.v.Frame(gtx) })
		if err != nil {
			t.Logf("headless rendering unavailable: %v", err)
			return
		}
		if dir == "" {
			continue
		}
		out, err := os.Create(filepath.Join(dir, "services-"+mode+".png"))
		if err != nil {
			t.Fatal(err)
		}
		_ = png.Encode(out, img)
		_ = out.Close()
	}
}

func TestServicesHelpers(t *testing.T) {
	for _, c := range []struct{ state, health, want string }{
		{"running", "", "running"}, {"running", "unhealthy", "unhealthy"}, {"running", "starting", "starting"},
		{"missing", "", "not created"}, {"paused", "", "paused"}, {"exited", "", "exited"}, {"", "", ""},
	} {
		if got := stateBadge(c.state, c.health).Text; got != c.want {
			t.Errorf("%s/%s: %q", c.state, c.health, got)
		}
	}
	if got := lastLines([]string{"a", "b", "c", "d"}, 2); len(got) != 2 || got[0] != "c" {
		t.Fatalf("lastLines %v", got)
	}
}
