package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe output buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitOutput(t *testing.T, out *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %q in output:\n%s", want, out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatchCommand(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"human", "json"} {
		t.Run(mode, func(t *testing.T) {
			dir := project(t, map[string]string{"a.php": "<?php\n", ".git/HEAD": "ref: refs/heads/main\n", "node_modules/x.js": ""})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := &syncBuffer{}
			env := testEnv(t, nil, out, io.Discard)
			args := []string{"watch", "--project", dir, "--backend", "poll", "--exclude", "node_modules"}
			if mode == "json" {
				args = append(args, "--json")
			}
			done := make(chan int, 1)
			go func() { done <- Run(ctx, args, env) }()

			want := map[string][3]string{
				"human": {"watching ", "created  b.php", "git      .git/HEAD"},
				"json":  {`"topic":"watch.status"`, `"path":"b.php"`, `"topic":"git.changed"`},
			}[mode]
			waitOutput(t, out, want[0])
			if err := os.WriteFile(filepath.Join(dir, "b.php"), []byte("<?php\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			waitOutput(t, out, want[1])
			if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			waitOutput(t, out, want[2])
			cancel()
			select {
			case code := <-done:
				if code != ExitOK {
					t.Fatalf("exit %d\n%s", code, out.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("watch did not stop on cancel")
			}
			if mode == "json" {
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					var ev map[string]any
					if err := json.Unmarshal([]byte(line), &ev); err != nil || ev["topic"] == nil {
						t.Fatalf("not a JSON event line: %q", line)
					}
				}
			}
		})
	}
}

func TestWatchCommandBadBackend(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	var errOut bytes.Buffer
	code := Run(context.Background(), []string{"watch", "--project", dir, "--backend", "nope"}, testEnv(t, nil, io.Discard, &errOut))
	if code != ExitUsage || !strings.Contains(errOut.String(), "unknown backend") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}
