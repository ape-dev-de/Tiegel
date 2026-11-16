package server_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/apitest"
	"github.com/ape-dev-de/tiegel/internal/api/client"
	"github.com/ape-dev-de/tiegel/internal/api/server"
)

func TestParseAddr(t *testing.T) {
	cases := []struct{ in, network, addr string }{
		{"unix:/tmp/a.sock", "unix", "/tmp/a.sock"},
		{"tcp:127.0.0.1:9000", "tcp", "127.0.0.1:9000"},
		{"/tmp/b.sock", "unix", "/tmp/b.sock"},
		{"./rel.sock", "unix", "./rel.sock"},
		{"localhost:1", "tcp", "localhost:1"},
		{" tcp:[::1]:0 ", "tcp", "[::1]:0"},
	}
	for _, c := range cases {
		n, a, err := server.ParseAddr(c.in)
		if err != nil || n != c.network || a != c.addr {
			t.Errorf("%q: %s %s %v", c.in, n, a, err)
		}
		if err == nil && server.FormatAddr(n, a) == "" {
			t.Error("format")
		}
	}
	for _, bad := range []string{"", "nothing", "unix:", "tcp:", "tcp:noport"} {
		if _, _, err := server.ParseAddr(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true,
		"": false, "0.0.0.0": false, "::": false, "192.168.1.2": false, "example.org": false,
	} {
		if got := server.IsLoopback(host); got != want {
			t.Errorf("%q: %v", host, got)
		}
	}
}

func TestTokenFile(t *testing.T) {
	tok, err := server.GenerateToken()
	if err != nil || len(tok) != 64 {
		t.Fatalf("token %q %v", tok, err)
	}
	p := filepath.Join(t.TempDir(), "sub", "token")
	if err := server.WriteTokenFile(p, tok); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	got, err := server.ReadTokenFile(p)
	if err != nil || got != tok {
		t.Fatalf("read %q %v", got, err)
	}
	if _, err := server.ReadTokenFile(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	_ = os.WriteFile(empty, []byte("\n"), 0o600)
	if _, err := server.ReadTokenFile(empty); err == nil {
		t.Fatal("empty file")
	}
	if err := server.WriteTokenFile(filepath.Join(empty, "x"), tok); err == nil {
		t.Fatal("dir is a file")
	}
}

func TestListenRules(t *testing.T) {
	dir := apitest.SocketDir(t)
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, f) }
	if _, err := server.Listen(server.Config{Addr: "tcp:0.0.0.0:0", Token: "t", Logf: logf}); err == nil || !strings.Contains(err.Error(), "allow-remote") {
		t.Fatalf("0.0.0.0 without AllowRemote: %v", err)
	}
	if _, err := server.Listen(server.Config{Addr: "tcp::0", Token: "t"}); err == nil {
		t.Fatal("empty host without AllowRemote")
	}
	if _, err := server.Listen(server.Config{Addr: "tcp:127.0.0.1:0"}); err == nil {
		t.Fatal("no token and no token file")
	}
	if _, err := server.Listen(server.Config{Addr: "x"}); err == nil {
		t.Fatal("bad address")
	}
	// Explicit token: no token file.
	s, err := server.Listen(server.Config{Addr: "tcp:localhost:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Token() != "secret" || s.TokenFile() != "" || !strings.HasPrefix(s.Addr(), "tcp:127.0.0.1:") && !strings.HasPrefix(s.Addr(), "tcp:[::1]:") {
		t.Fatalf("server %s %s %s", s.Addr(), s.Token(), s.TokenFile())
	}
	_ = s.Close()
	_ = s.Close()

	// Unix: not a socket, stale socket, socket in use.
	plain := filepath.Join(dir, "plain")
	_ = os.WriteFile(plain, nil, 0o600)
	if _, err := server.Listen(server.Config{Addr: "unix:" + plain, Token: "t"}); err == nil {
		t.Fatal("plain file accepted")
	}
	if runtime.GOOS == "windows" {
		return // AF_UNIX socket files are not reported as sockets by Lstat
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Listen(server.Config{Addr: "unix:" + sock, Token: "t"}); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("socket in use: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // leaves a stale socket file
	s, err = server.Listen(server.Config{Addr: "unix:" + sock, TokenFile: filepath.Join(dir, "tok")})
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(sock)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("socket mode %v %v", fi.Mode(), err)
		}
	}
	tok, err := server.ReadTokenFile(filepath.Join(dir, "tok"))
	if err != nil || tok != s.Token() {
		t.Fatalf("token file %q %v", tok, err)
	}
	_ = s.Close()
	if _, err := os.Stat(filepath.Join(dir, "tok")); !os.IsNotExist(err) {
		t.Fatal("token file not removed")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatal("socket not removed")
	}
}

func TestServeShutdown(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.txt": "x"})
	dir := apitest.SocketDir(t)
	var logged []string
	s, err := server.Listen(server.Config{
		Addr: "unix:" + filepath.Join(dir, "s.sock"), TokenFile: filepath.Join(dir, "token"),
		Logf: func(f string, args ...any) { logged = append(logged, f) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, a) }()

	c, err := client.Dial(context.Background(), s.Addr(), s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := api.Call[api.DocOpenResult](context.Background(), c, "doc.open", api.DocOpenParams{Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	cancel() // like SIGINT/SIGTERM in `serve --listen`
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client not disconnected on shutdown")
	}
	if _, err := os.Stat(filepath.Join(dir, "s.sock")); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
	// Serving a closed server returns at once.
	if err := s.Serve(context.Background(), a); err != nil {
		t.Fatalf("serve after close: %v", err)
	}
}
