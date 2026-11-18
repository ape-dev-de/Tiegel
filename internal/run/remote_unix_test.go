//go:build !windows

package run

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// gone reports whether process pid ended (a zombie counts as ended).
func gone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, after, _ := strings.Cut(string(data), ") ")
	return strings.HasPrefix(after, "Z")
}

func waitGone(t *testing.T, what string, pid int) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if gone(pid) {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("%s %d still running", what, pid)
}

// ownGroup reports whether the wrapper can give the program its own
// process group on the host (setsid exists; the fake's shell is no
// group leader).
func ownGroup() bool {
	_, err := exec.LookPath("setsid")
	return err == nil && runtime.GOOS == "linux"
}

// pidLines returns the "pid N" and "child N" values of run id.
func pidLines(t *testing.T, m *Manager, id string) (pid, child int) {
	t.Helper()
	pg, err := m.Output(id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range pg.Lines {
		if v, ok := strings.CutPrefix(l.Text, "pid "); ok {
			pid, _ = strconv.Atoi(v)
		}
		if v, ok := strings.CutPrefix(l.Text, "child "); ok {
			child, _ = strconv.Atoi(v)
		}
	}
	if pid == 0 || child == 0 {
		t.Fatalf("no pids in %+v", pg.Lines)
	}
	return pid, child
}

// stopViaFake starts the pidtree helper through target, stops it and
// checks that the process (and with a group, its child) ended. It
// returns the logged calls and the program's PID.
func stopViaFake(t *testing.T, tgt Target, spec Spec, log string) ([][]string, int) {
	t.Helper()
	m := &Manager{}
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, info.ID, "ready")
	pid, child := pidLines(t, m, info.ID)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err := m.Stop(info.ID); err != nil {
		t.Fatal(err)
	}
	done := wait(t, m, info.ID)
	if done.State != StateStopped {
		t.Fatalf("state %+v", done)
	}
	waitGone(t, "program", pid)
	if ownGroup() {
		waitGone(t, "child", child)
	}
	pg, _ := m.Output(info.ID, 0, 0)
	for _, l := range pg.Lines {
		if strings.Contains(l.Text, "failed") {
			t.Fatalf("stop reported an error: %q", l.Text)
		}
	}
	return fakeCalls(t, log), pid
}

// TestComposeStopRunsShellKill: stopping an exec run signals the process
// group inside the container with the shell's kill builtin (images like
// php:*-cli have no kill program), not with `kill` as a program.
func TestComposeStopRunsShellKill(t *testing.T) {
	self, _ := os.Executable()
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	ci := &compose.Interpreter{Project: "demo", Dir: root, Files: []string{"compose.yaml"}, Service: "php", Mode: compose.ExecAlways,
		Docker: self, Env: []string{helperEnv + "=fakedocker", fakeLogEnv + "=" + log}}
	spec := Spec{Name: "tree", Program: self, Env: []string{helperEnv + "=pidtree"}}
	calls, pid := stopViaFake(t, Target{Interp: ci, ProjectDir: root}, spec, log)
	want := []string{"compose", "--project-directory", root, "-p", "demo", "-f", "compose.yaml",
		"exec", "-T", "php", "sh", "-c", stopScript, "sh", strconv.Itoa(pid)}
	if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Fatalf("no stop call %q in\n%q", want, calls)
	}
}

// TestWSLStopRemote: a WSL run reports its PID and is stopped inside the
// distribution (killing wsl.exe alone leaves the Linux process).
func TestWSLStopRemote(t *testing.T) {
	self, _ := os.Executable()
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv(helperEnv, "fakewsl") // wsl.exe inherits the environment
	t.Setenv(fakeLogEnv, log)
	w := &interp.WSL{Distro: "Ubuntu", Program: self}
	spec := Spec{Name: "tree", Program: self, Env: []string{helperEnv + "=pidtree"}}
	calls, pid := stopViaFake(t, Target{Interp: w, ProjectDir: t.TempDir()}, spec, log)
	want := []string{"-d", "Ubuntu", "--", "sh", "-c", stopScript, "sh", strconv.Itoa(pid)}
	if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Fatalf("no stop call %q in\n%q", want, calls)
	}
}

// TestStopScriptEscalates: a program that ignores TERM is killed after
// the grace period.
func TestStopScriptEscalates(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the grace period")
	}
	c := exec.Command("sh", "-c", `trap "" TERM; echo ready; while :; do sleep 1; done`)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL); _ = c.Wait() })
	if l, _ := bufio.NewReader(out).ReadString('\n'); l != "ready\n" {
		t.Fatalf("line %q", l)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if b, err := exec.CommandContext(ctx, "sh", "-c", stopScript, "sh", strconv.Itoa(c.Process.Pid)).CombinedOutput(); err != nil {
		t.Fatalf("stop script: %v %s", err, b)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("TERM-ignoring program not killed")
	}
}

// dockerCLI runs the docker CLI with a timeout.
func dockerCLI(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

// localImage returns a Debian based image that is present locally ("" if
// none; tests never pull).
func localImage() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return ""
	}
	ctx := context.Background()
	if out, err := dockerCLI(ctx, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}", "--filter", "reference=php:*-cli"); err == nil {
		if f := strings.Fields(string(out)); len(f) > 0 {
			return f[0]
		}
	}
	if _, err := dockerCLI(ctx, "image", "inspect", "debian:bookworm-slim"); err == nil {
		return "debian:bookworm-slim"
	}
	return ""
}

// TestRealDockerStopScripts runs the wrapper and stop scripts in a real
// Debian based container (dash, no kill program): the stop ends the
// program and its child.
func TestRealDockerStopScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("real docker")
	}
	image := localImage()
	if image == "" {
		t.Skip("docker or a local php:*-cli / debian:bookworm-slim image not available")
	}
	ctx := context.Background()
	name := containerName()
	if out, err := dockerCLI(ctx, "run", "-d", "--rm", "--init", "--pull", "never", "--name", name, "--entrypoint", "sleep", image, "300"); err != nil {
		t.Skipf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = dockerCLI(context.Background(), "rm", "-f", name) })
	// The old stop command fails here: there is no kill program.
	if _, err := dockerCLI(ctx, "exec", name, "kill", "-0", "1"); err == nil {
		t.Log("image has a kill program")
	}

	c := exec.Command("docker", "exec", name, "sh", "-c", wrapScript, "sh", "sh", "-c", `sleep 300 & echo "child $!"; wait`)
	stdout, _ := c.StdoutPipe()
	stderr, _ := c.StderrPipe()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	marker, _ := bufio.NewReader(stderr).ReadString('\n')
	pid, ok := strings.CutPrefix(strings.TrimSpace(marker), strings.TrimSpace(pidMarker))
	pid = strings.TrimSpace(pid)
	if !ok || pid == "" {
		t.Fatalf("marker %q", marker)
	}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	child, ok := strings.CutPrefix(strings.TrimSpace(line), "child ")
	if !ok {
		t.Fatalf("child line %q", line)
	}
	start := time.Now()
	if out, err := dockerCLI(ctx, "exec", name, "sh", "-c", stopScript, "sh", pid); err != nil {
		t.Fatalf("stop: %v %s", err, out)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("docker exec did not end after the stop")
	}
	out, err := dockerCLI(ctx, "exec", name, "sh", "-c",
		`for p in "$@"; do s=; read -r _ _ s _ 2>/dev/null </proc/$p/stat; if [ -n "$s" ] && [ "$s" != Z ]; then echo "alive $p"; fi; done`, "sh", pid, child)
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("after stop (%v): %s %v", time.Since(start), out, err)
	}
}
