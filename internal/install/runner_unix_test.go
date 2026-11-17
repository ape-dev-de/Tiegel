//go:build unix

package install

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCancelKillsGrandchildren: cancelling a run whose wrapper started the
// real work in a grandchild (npm.cmd, composer.bat, shell scripts) ends the
// grandchild too and finishes at once, although the grandchild held the
// output pipes.
func TestCancelKillsGrandchildren(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	plan := Plan{Plugin: "lsp-x", Tool: "X", Available: true,
		Command: []string{"/bin/sh", "-c", "sleep 30 & echo $! > " + pidFile + "; echo started; wait"}}
	var rn Runner
	id, err := rn.Start(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 {
		if b, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(b), "\n") {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := rn.Cancel(id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := rn.Wait(ctx, id)
	if err != nil {
		t.Fatalf("cancelled run did not finish: %v", err)
	}
	if res.State != StateCancelled {
		t.Fatalf("result %+v", res)
	}
	for syscall.Kill(pid, 0) == nil {
		if ctx.Err() != nil {
			t.Fatalf("grandchild %d survived the cancel", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
