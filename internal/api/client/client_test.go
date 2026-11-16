package client_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/apitest"
	"github.com/ape-dev-de/tiegel/internal/api/client"
	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
)

// TestMain doubles as a stdio server for the Spawn tests.
func TestMain(m *testing.M) {
	if os.Getenv("TIEGEL_CLIENT_HELPER") == "serve" {
		os.Exit(helperServe())
	}
	os.Exit(m.Run())
}

func helperServe() int {
	dir, err := os.MkdirTemp("", "tgcfg")
	if err != nil {
		return 3
	}
	defer os.RemoveAll(dir)
	a, err := api.New(api.Options{ConfigDir: dir, AppVersion: "helper"})
	if err != nil {
		return 3
	}
	defer a.Close()
	if os.Getenv("TIEGEL_CLIENT_HELPER_HANG") != "" {
		// Ignore stdin EOF to exercise the kill path.
		time.Sleep(time.Minute)
	}
	if err := a.Serve(context.Background(), stdio{}, api.ServeOptions{}); err != nil {
		return 1
	}
	return 0
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return nil }

func TestSpawn(t *testing.T) {
	t.Setenv("TIEGEL_CLIENT_HELPER", "serve")
	ctx := context.Background()
	var stderr strings.Builder
	c, err := client.Spawn(ctx, []string{os.Args[0], "-test.run=^$"}, client.WithStderr(&stderr))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Auth(ctx, "anything") // no token configured: accepted
	if err != nil || !res.OK || res.Version != "helper" {
		t.Fatalf("auth: %+v %v", res, err)
	}
	vi, err := api.Call[api.VersionInfo](ctx, c, "app.version", struct{}{})
	if err != nil || vi.APIVersion != api.Version {
		t.Fatalf("version: %+v %v", vi, err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Call(ctx, "app.version", nil, nil); !errors.Is(err, client.ErrDisconnected) {
		t.Fatalf("after close: %v", err)
	}
	if c.Err() == nil {
		t.Fatal("Err after close")
	}
}

func TestSpawnKill(t *testing.T) {
	t.Setenv("TIEGEL_CLIENT_HELPER", "serve")
	t.Setenv("TIEGEL_CLIENT_HELPER_HANG", "1")
	c, err := client.Spawn(context.Background(), []string{os.Args[0], "-test.run=^$"}, client.WithExitTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = c.Close()
	if time.Since(start) > 10*time.Second {
		t.Fatal("close did not kill the server")
	}
}

func TestSpawnErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := client.Spawn(ctx, nil); err == nil {
		t.Fatal("empty command")
	}
	if _, err := client.Spawn(ctx, []string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing binary")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.Spawn(cctx, []string{"true"}); err == nil {
		t.Fatal("cancelled context")
	}
}

func TestCommands(t *testing.T) {
	got := strings.Join(client.WSLServeCommand("Ubuntu", "/usr/local/bin/tiegel", "--project", "/home/u/p"), " ")
	if got != "wsl.exe -d Ubuntu -- /usr/local/bin/tiegel --headless serve --project /home/u/p" {
		t.Fatal(got)
	}
	if got := strings.Join(client.ServeCommand("tiegel"), " "); got != "tiegel --headless serve" {
		t.Fatal(got)
	}
}

func TestDialAuth(t *testing.T) {
	a := apitest.NewApp(t, nil)
	s := apitest.NewServer(t, a, "unix")
	ctx := context.Background()

	if _, err := client.Dial(ctx, s.Addr(), "wrong"); api.ErrorCode(err) != api.CodeUnauthorized {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := client.Dial(ctx, "bogus", s.Token()); err == nil {
		t.Fatal("bad address")
	}
	if _, err := client.Dial(ctx, "unix:"+filepath.Join(t.TempDir(), "none.sock"), "x"); err == nil {
		t.Fatal("missing socket")
	}

	// Without auth: the first request fails and the connection closes.
	nc, err := net.Dial("unix", strings.TrimPrefix(s.Addr(), "unix:"))
	if err != nil {
		t.Fatal(err)
	}
	raw := client.New(nc)
	defer raw.Close()
	if err := raw.Call(ctx, "doc.list", nil, nil); api.ErrorCode(err) != api.CodeUnauthorized {
		t.Fatalf("unauthenticated: %v", err)
	}
	select {
	case <-raw.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection stays open after failed auth")
	}
	if !errors.Is(raw.Err(), client.ErrDisconnected) {
		t.Fatalf("Err: %v", raw.Err())
	}

	// Two clients with independent subscriptions.
	c1, err := client.Dial(ctx, s.Addr(), s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := client.Dial(ctx, s.Addr(), s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var r1, r2 recorder
	if _, err := c1.Subscribe(ctx, []string{"one"}, r1.add); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Subscribe(ctx, []string{"two"}, r2.add); err != nil {
		t.Fatal(err)
	}
	_ = c1.Call(ctx, "events.publish", api.PublishParams{Topic: "two"}, nil)
	_ = c1.Call(ctx, "events.publish", api.PublishParams{Topic: "one"}, nil)
	if got := r1.waitFor(t, 1); got[0].Topic != "one" {
		t.Fatalf("c1 got %v", got)
	}
	if got := r2.waitFor(t, 1); got[0].Topic != "two" {
		t.Fatalf("c2 got %v", got)
	}
}

func TestAuthTimeoutAndNotifications(t *testing.T) {
	a := apitest.NewApp(t, nil)
	srv, cli := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- a.Serve(context.Background(), srv, api.ServeOptions{Token: "tok", AuthTimeout: 50 * time.Millisecond})
	}()
	// A notification before auth is ignored, not answered.
	w := bufio.NewWriter(cli)
	_, _ = w.WriteString(`{"jsonrpc":"2.0","method":"events.publish","params":{"topic":"x"}}` + "\n")
	_ = w.Flush()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("auth timeout did not close the connection")
	}
	_ = cli.Close()
}

func TestAuthBadParams(t *testing.T) {
	a := apitest.NewApp(t, nil)
	srv, cli := net.Pipe()
	go func() { _ = a.Serve(context.Background(), srv, api.ServeOptions{Token: "tok"}) }()
	c := client.New(cli, client.WithFraming(jsonrpc2.LineFraming))
	defer c.Close()
	if err := c.Call(context.Background(), api.AuthMethod, map[string]any{"bogus": true}, nil); api.ErrorCode(err) != api.CodeInvalidParams {
		t.Fatalf("bad auth params: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection open after bad auth")
	}
}

func TestServerGoesAway(t *testing.T) {
	a := apitest.NewApp(t, nil)
	srv, cli := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Serve(ctx, srv, api.ServeOptions{}) }()
	c := client.New(cli)
	defer c.Close()
	var r recorder
	cancelSub, err := c.Subscribe(context.Background(), []string{"x"}, r.add)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client did not notice the server going away")
	}
	cancelSub() // no server: must not block
	if err := c.Call(context.Background(), "app.version", nil, nil); !errors.Is(err, client.ErrDisconnected) {
		t.Fatalf("call after disconnect: %v", err)
	}
	if _, err := c.Subscribe(context.Background(), []string{"y"}, nil); !errors.Is(err, client.ErrDisconnected) {
		t.Fatalf("subscribe after disconnect: %v", err)
	}
}
