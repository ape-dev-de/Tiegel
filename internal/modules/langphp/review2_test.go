package langphp

import (
	"context"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// TestStopInterruptsHangingStart: lsp.stop during an initialize that does
// not answer returns at once instead of waiting for the init timeout.
func TestStopInterruptsHangingStart(t *testing.T) {
	entered := make(chan struct{}, 4)
	hang := func(ctx context.Context, _ lsp.Config) (*lsp.Client, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m := New(Options{Connect: hang, Supervisor: func(o *supervisor.Options) { o.InitTimeout = time.Minute }})
	a := modtest.NewApp(t, withPlugins(m), nil)
	modtest.Project(t, a, map[string]string{"a.php": src})
	done := make(chan error, 1)
	go func() { done <- modtest.TryCall(a, "lsp.start", StartParams{Command: "hanging-lsp"}, nil) }()
	<-entered
	t0 := time.Now()
	var st Status
	modtest.Call(t, a, "lsp.stop", nil, &st)
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("lsp.stop took %s", d)
	}
	if st.State != supervisor.StateOff {
		t.Fatalf("after stop %+v", st)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("lsp.start still hangs")
	}
	if st := m.currentStatus(); st.State != supervisor.StateOff {
		t.Fatalf("final %+v", st)
	}
}

// TestRequestTimeoutSettingApplies: lang-php.requestTimeout changes reach
// the supervisor and the running server without a restart of Tiegel.
func TestRequestTimeoutSettingApplies(t *testing.T) {
	a, m, _, _ := setup(t)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake-lsp"}, nil)
	c := m.current().client
	if c.RequestTimeout() != 30*time.Second {
		t.Fatalf("initial %s", c.RequestTimeout())
	}
	if err := settings.Set(a.Settings(), SettingRequestTimeout, 5); err != nil {
		t.Fatal(err)
	}
	modtest.Eventually(t, "timeout applied", func() bool {
		return m.sup.RequestTimeout() == 5*time.Second && c.RequestTimeout() == 5*time.Second
	})
	if err := settings.Set(a.Settings(), SettingRequestTimeout, 0); err != nil {
		t.Fatal(err)
	}
	modtest.Eventually(t, "timeout off", func() bool { return c.RequestTimeout() == 0 })
}

// TestProjectSwitchKeepsAutoStartedServer: switching projects in the same
// window (project.closed + project.opened) ends with the server running
// for the new project, whatever order the handlers run in.
func TestProjectSwitchKeepsAutoStartedServer(t *testing.T) {
	a, _, f, dir, _ := pluginApp(t, Options{AutoStart: true}, "phpactor")
	lastRoot := func() string {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.cfgs) == 0 {
			return ""
		}
		return f.cfgs[len(f.cfgs)-1].RootDir
	}
	waitState(t, a, "auto start", func(s Status) bool { return s.Running && lastRoot() == dir })
	dirs := []string{dir, t.TempDir(), t.TempDir()}
	for i := 0; i < 15; i++ {
		next := dirs[(i+1)%len(dirs)]
		var info api.ProjectInfo
		modtest.Call(t, a, "project.open", api.PathParams{Path: next}, &info)
		waitState(t, a, "running after switch to "+info.Root, func(s Status) bool { return s.Running && lastRoot() == info.Root })
		// Settled: nothing stops it afterwards.
		time.Sleep(20 * time.Millisecond)
		var st Status
		modtest.Call(t, a, "lsp.status", nil, &st)
		if !st.Running || lastRoot() != info.Root {
			t.Fatalf("switch %d: %+v (root %s, want %s)", i, st, lastRoot(), info.Root)
		}
	}
}
