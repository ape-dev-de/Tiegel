package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/client"
	"github.com/ape-dev-de/tiegel/internal/api/server"
)

func socketDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "tgh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func readListenInfo(t *testing.T, r io.Reader) ListenInfo {
	t.Helper()
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		var info ListenInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil || info.Listen == "" {
			t.Fatalf("listen line %q: %v", line, err)
		}
		return info
	case <-time.After(20 * time.Second):
		t.Fatal("no listen line")
	}
	return ListenInfo{}
}

func TestServeListen(t *testing.T) {
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	sock := filepath.Join(socketDir(t), "s.sock")
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var errb lockedBytes
	go func() {
		done <- Run(ctx, []string{"serve", "--listen", "unix:" + sock, "--project", dir, "--concurrent"}, testEnv(t, nil, pw, &errb))
	}()
	info := readListenInfo(t, pr)
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if info.Listen != "unix:"+sock || info.TokenFile == "" {
		t.Fatalf("info %+v", info)
	}
	tok, err := server.ReadTokenFile(info.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(context.Background(), info.Listen, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := api.Call[api.DocOpenResult](context.Background(), c, "doc.open", api.DocOpenParams{Path: "a.php", Text: true})
	if err != nil || res.Text != "<?php\n" {
		t.Fatalf("doc.open %+v %v", res, err)
	}
	cancel() // SIGINT/SIGTERM in the real binary
	select {
	case code := <-done:
		if code != ExitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	if _, err := os.Stat(info.TokenFile); !os.IsNotExist(err) {
		t.Fatal("token file left behind")
	}
	if !strings.Contains(errb.String(), "listening on") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestServeListenErrors(t *testing.T) {
	if code, _, errs := run(t, "", "serve", "--token", "x"); code != ExitUsage || !strings.Contains(errs, "--listen") {
		t.Fatalf("token without listen: %d %q", code, errs)
	}
	if code, _, errs := run(t, "", "serve", "--listen", "tcp:0.0.0.0:0", "--token", "x"); code != ExitError || !strings.Contains(errs, "allow-remote") {
		t.Fatalf("0.0.0.0: %d %q", code, errs)
	}
	// Token from the environment: no token file is written.
	env := testEnv(t, nil, nil, nil)
	env.Getenv = func(k string) string {
		if k == server.TokenEnv {
			return "envtoken"
		}
		return ""
	}
	pr, pw := io.Pipe()
	env.Stdout = pw
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"serve", "--listen", "tcp:127.0.0.1:0"}, env) }()
	info := readListenInfo(t, pr)
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if info.TokenFile != "" {
		t.Fatalf("token file %q", info.TokenFile)
	}
	c, err := client.Dial(context.Background(), info.Listen, "envtoken")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	cancel()
	if code := <-done; code != ExitOK {
		t.Fatalf("exit %d", code)
	}
}

// lockedBytes is a goroutine-safe strings.Builder.
type lockedBytes struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *lockedBytes) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *lockedBytes) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

// E2E: the real binary as a stdio child (client.Spawn) and as a socket
// server stopped with SIGTERM.
func TestE2EServeTransports(t *testing.T) {
	bin := buildBinary(t)
	cfg := t.TempDir()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	t.Setenv("TIEGEL_CONFIG_DIR", cfg)

	c, err := client.Spawn(context.Background(), client.ServeCommand(bin, "--project", dir))
	if err != nil {
		t.Fatal(err)
	}
	txt, err := api.Call[api.DocOpenResult](context.Background(), c, "doc.open", api.DocOpenParams{Path: "a.php", Text: true})
	if err != nil || txt.Text != "<?php\n" {
		t.Fatalf("spawned doc.open %+v %v", txt, err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if runtime.GOOS == "windows" {
		return // no SIGTERM
	}
	sock := filepath.Join(socketDir(t), "e.sock")
	cmd := exec.Command(bin, "--headless", "serve", "--listen", "unix:"+sock)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	info := readListenInfo(t, stdout)
	tok, err := server.ReadTokenFile(info.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := client.Dial(context.Background(), info.Listen, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if err := rc.Call(context.Background(), "app.version", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	werr := make(chan error, 1)
	go func() { werr <- cmd.Wait() }()
	select {
	case err := <-werr:
		if err != nil {
			t.Fatalf("exit after SIGTERM: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("no shutdown on SIGTERM")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
	if _, err := os.Stat(info.TokenFile); !os.IsNotExist(err) {
		t.Fatal("token file left behind")
	}
}
