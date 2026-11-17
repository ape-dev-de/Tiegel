//go:build unix

package proctree

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startTree starts a shell that runs a grandchild (sleep) and prints its
// PID; it returns the command, its tree and the grandchild PID.
func startTree(t *testing.T) (*exec.Cmd, *Tree, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo $!; wait")
	tree := New(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree.Attach()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return cmd, tree, pid
}

// alive reports whether pid still runs (Unix).
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grandchild %d still alive", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKillEndsGrandchildren(t *testing.T) {
	cmd, tree, pid := startTree(t)
	if !alive(pid) {
		t.Fatal("grandchild not running")
	}
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	tree.Release()
	waitDead(t, pid)
	if err := tree.Kill(); err != nil { // released: no-op
		t.Fatal(err)
	}
	var nilTree *Tree
	if err := nilTree.Kill(); err != nil {
		t.Fatal(err)
	}
	nilTree.Release()
}

// TestKillAfterLeaderExited: grandchildren left behind by an exited
// wrapper are still reached through the process group.
func TestKillAfterLeaderExited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $!")
	tree := New(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if !alive(pid) {
		t.Fatal("grandchild not running")
	}
	tree.Attach()
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	waitDead(t, pid)
}
