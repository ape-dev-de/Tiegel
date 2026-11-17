package shellenv

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCancelledFirstCallerDoesNotPoison: the cached query must not use the
// first caller's (cancelled) context.
func TestCancelledFirstCallerDoesNotPoison(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell on windows")
	}
	r := New("/bin/fake", func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []byte(marker + "/a" + marker), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := r.LoginPath(ctx); err != nil || p != "/a" {
		t.Fatalf("cancelled first caller: %q, %v", p, err)
	}
	if p, err := r.LoginPath(context.Background()); err != nil || p != "/a" {
		t.Fatalf("later caller: %q, %v", p, err)
	}
}

// TestFishJoinsPathList: fish's $PATH is a list and must be joined with ':'.
func TestFishJoinsPathList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell on windows")
	}
	var script string
	r := New("/opt/homebrew/bin/fish", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		script = args[len(args)-1]
		return []byte(marker + "/a:/b" + marker), nil
	})
	if _, err := r.LoginPath(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "string join : $PATH") {
		t.Fatalf("fish script %q", script)
	}
	r = New("/bin/zsh", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		script = args[len(args)-1]
		return []byte(marker + "/a" + marker), nil
	})
	if _, err := r.LoginPath(context.Background()); err != nil || !strings.Contains(script, `"$PATH"`) {
		t.Fatalf("posix script %q, %v", script, err)
	}
}

// TestExecRunnerBackgroundJob: a background job holding stdout does not
// keep the runner waiting after the shell exited.
func TestExecRunnerBackgroundJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t0 := time.Now()
	out, _ := execRunner(ctx, "/bin/sh", "-c", "sleep 8 & echo hi")
	if d := time.Since(t0); d > 4*time.Second {
		t.Fatalf("runner waited %s for the background job", d)
	}
	if !strings.Contains(string(out), "hi") {
		t.Logf("output %q", out)
	}
}
