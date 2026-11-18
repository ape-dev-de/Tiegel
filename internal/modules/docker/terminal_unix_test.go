//go:build unix

package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// localShellImage returns a local image with sh (never pulls), or skips.
func localShellImage(t *testing.T, bin string) string {
	t.Helper()
	cands := []string{"alpine:latest", "alpine:3", "busybox:latest", "debian:bookworm-slim"}
	if v := os.Getenv("TIEGEL_TEST_SH_IMAGE"); v != "" {
		cands = []string{v}
	}
	for _, img := range cands {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := exec.CommandContext(ctx, bin, "image", "inspect", img).Run()
		cancel()
		if err == nil {
			return img
		}
	}
	t.Skip("no local image with sh (set TIEGEL_TEST_SH_IMAGE)")
	return ""
}

// containerProcs returns the command lines of the processes in c.
func containerProcs(t *testing.T, bin, c string) string {
	t.Helper()
	out, err := exec.Command(bin, "exec", c, "sh", "-c",
		`for f in /proc/[0-9]*/cmdline; do tr '\0' ' ' <"$f" 2>/dev/null; echo; done`).Output()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	return string(out)
}

// TestRealContainerShellClose: closing the tab of a container shell ends
// the shell and its jobs inside the container, not only the local docker
// client (D370). Needs docker and a local image; never pulls.
func TestRealContainerShellClose(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	bin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("no docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = exec.CommandContext(ctx, bin, "version").Run()
	cancel()
	if err != nil {
		t.Skip("docker engine unreachable")
	}
	img := localShellImage(t, bin)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "tiegel-term-test-" + hex.EncodeToString(b)
	if out, err := exec.Command(bin, "run", "-d", "--rm", "--pull", "never", "--name", name, img, "sleep", "300").CombinedOutput(); err != nil {
		t.Skipf("cannot start a container: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(bin, "rm", "-f", name).Run() })

	a, _ := newApp(t, Options{Binary: bin, GOOS: "linux"})
	var ti terminal.Info
	modtest.Call(t, a, "terminal.open", api.TerminalOpenParams{Kind: TerminalKindContainer, Target: name, Dir: t.TempDir()}, &ti)
	waitRemote := func() terminal.Info {
		deadline := time.Now().Add(20 * time.Second)
		for {
			var l []terminal.Info
			modtest.Call(t, a, "terminal.list", nil, &l)
			if len(l) == 1 && l[0].RemotePID > 0 {
				return l[0]
			}
			if time.Now().After(deadline) {
				t.Fatalf("no remote pid: %+v", l)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	got := waitRemote()
	// Type only once the prompt is there (the line editor drops earlier
	// input).
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var txt api.TerminalText
		modtest.Call(t, a, "terminal.text", api.TerminalTextParams{ID: got.ID}, &txt)
		if strings.ContainsAny(txt.Text, "#$") {
			// busybox asks for the cursor position after the prompt and
			// drops input that arrives before the answer.
			time.Sleep(500 * time.Millisecond)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no prompt: %q", txt.Text)
		}
	}
	modtest.Call(t, a, "terminal.input", api.TerminalInputParams{ID: got.ID, Data: "sleep 777 & sleep 778"}, nil)
	modtest.Call(t, a, "terminal.input", api.TerminalInputParams{ID: got.ID, Key: &vt.Key{Name: "Enter"}}, nil)
	for deadline := time.Now().Add(20 * time.Second); !strings.Contains(containerProcs(t, bin, name), "sleep 778"); {
		if time.Now().After(deadline) {
			var txt api.TerminalText
			modtest.Call(t, a, "terminal.text", api.TerminalTextParams{ID: got.ID}, &txt)
			t.Fatalf("jobs did not start: %s\nscreen %q", containerProcs(t, bin, name), txt.Text)
		}
		time.Sleep(100 * time.Millisecond)
	}
	modtest.Call(t, a, "terminal.close", api.TerminalIDParams{ID: got.ID}, nil)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		procs := containerProcs(t, bin, name)
		if !strings.Contains(procs, "sleep 777") && !strings.Contains(procs, "sleep 778") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes left in the container:\n%s", procs)
		}
	}
}
