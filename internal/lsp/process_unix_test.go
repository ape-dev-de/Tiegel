//go:build unix

package lsp

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestKillEndsServerTree: killing a server started through a wrapper
// (intelephense.cmd, phpactor.bat, shell scripts) also ends the real
// server running as grandchild.
func TestKillEndsServerTree(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	pids := make(chan int, 1)
	p, err := startProcess("/bin/sh", []string{"-c", "sleep 30 & echo $! >&2; wait"}, t.TempDir(), nil,
		slog.New(slog.DiscardHandler), func(line string) {
			if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				pids <- n
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	select {
	case pid = <-pids:
	case <-time.After(5 * time.Second):
		t.Fatal("grandchild did not start")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	p.kill()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the kill", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
