// Package apitest provides test helpers for code written against
// api.Caller: an isolated App with a project, and the same App reachable
// through every Caller implementation (in-process, JSON-RPC over a pipe, a
// unix socket and loopback TCP with token authentication), so one test suite
// can run against all of them.
package apitest

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/client"
	"github.com/ape-dev-de/tiegel/internal/api/server"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// NewApp creates an App with an isolated config directory and opens a
// project containing files (relative path → content). It is closed when
// the test ends.
func NewApp(t testing.TB, files map[string]string) *api.App {
	t.Helper()
	home := t.TempDir()
	a, err := api.New(api.Options{
		ConfigDir:  filepath.Join(t.TempDir(), "config"),
		Interp:     interp.DiscoverOptions{Dirs: []string{}, Timeout: 5 * time.Second},
		LSP:        servers.DetectOptions{HomeDir: home, ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
		ComposeEnv: map[string]string{},
		GOOS:       "linux",
		AppVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return a
}

// Transport names returned by Callers.
const (
	InProcess = "inprocess"
	Pipe      = "pipe"
	Unix      = "unix"
	TCP       = "tcp"
)

// Callers returns a fresh Caller per transport, all talking to a. The
// connections and servers are closed when the test ends.
func Callers(t testing.TB, a *api.App) map[string]api.Caller {
	t.Helper()
	return map[string]api.Caller{
		InProcess: NewInProcess(t, a),
		Pipe:      NewPipe(t, a),
		Unix:      NewSocket(t, a, "unix"),
		TCP:       NewSocket(t, a, "tcp"),
	}
}

// NewInProcess returns an api.InProcess caller for a.
func NewInProcess(t testing.TB, a *api.App) api.Caller {
	c := api.NewInProcess(a)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// NewPipe serves a on one end of an in-memory pipe (concurrent mode, no
// token) and returns a client on the other end.
func NewPipe(t testing.TB, a *api.App) *client.Client {
	t.Helper()
	srv, cli := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Serve(ctx, srv, api.ServeOptions{Concurrent: true})
	}()
	c := client.New(cli)
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
		<-done
	})
	return c
}

// SocketDir returns a short temporary directory for unix sockets (macOS
// limits socket paths to 104 bytes, test temp dirs can be longer).
func SocketDir(t testing.TB) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "tg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// NewServer starts a socket server for a ("unix" or "tcp" on loopback) with
// a generated token and returns it.
func NewServer(t testing.TB, a *api.App, network string) *server.Server {
	t.Helper()
	dir := SocketDir(t)
	addr := "tcp:127.0.0.1:0"
	if network == "unix" {
		addr = "unix:" + filepath.Join(dir, "s.sock")
	}
	s, err := server.Listen(server.Config{
		Addr:      addr,
		TokenFile: filepath.Join(dir, "token"),
		Serve:     api.ServeOptions{Concurrent: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, a)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return s
}

// NewSocket starts a server (see NewServer) and returns an authenticated
// client.
func NewSocket(t testing.TB, a *api.App, network string) *client.Client {
	t.Helper()
	s := NewServer(t, a, network)
	c, err := client.Dial(context.Background(), s.Addr(), s.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
