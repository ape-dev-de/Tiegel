// Package modtest holds test helpers for the module packages: an isolated
// App with modules installed, typed calls and an event recorder.
package modtest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Options returns api.Options that only touch temporary directories and do
// not look at the machine's PATH.
func Options(t testing.TB) api.Options {
	home := t.TempDir()
	return api.Options{
		ConfigDir:  filepath.Join(t.TempDir(), "config"),
		Interp:     interp.DiscoverOptions{Dirs: []string{}, Timeout: 5 * time.Second},
		LSP:        servers.DetectOptions{HomeDir: home, PathEnv: "", ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
		ComposeEnv: map[string]string{},
		GOOS:       "linux",
		AppVersion: "test",
	}
}

// NewApp creates an App with the given modules; mod may adjust the options
// first (e.g. interpreter directories).
func NewApp(t testing.TB, mods []module.Module, mod func(*api.Options)) *api.App {
	t.Helper()
	opts := Options(t)
	if mod != nil {
		mod(&opts)
	}
	opts.Extensions = append(opts.Extensions, func(a *api.App) error {
		for _, m := range mods {
			if err := a.AddModule(m); err != nil {
				return err
			}
		}
		return nil
	})
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// Call invokes method and decodes the result into out (if non-nil).
func Call(t testing.TB, a *api.App, method string, params, out any) {
	t.Helper()
	if err := TryCall(a, method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

// TryCall is Call returning the error.
func TryCall(a *api.App, method string, params, out any) error {
	res, err := a.CallJSON(context.Background(), method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	v := reflect.ValueOf(out).Elem()
	v.Set(reflect.Zero(v.Type()))
	return json.Unmarshal(b, out)
}

// WantCode asserts that method fails with code and returns the error.
func WantCode(t testing.TB, a *api.App, method string, params any, code int) error {
	t.Helper()
	err := TryCall(a, method, params, nil)
	if err == nil {
		t.Fatalf("%s: expected error code %d, got success", method, code)
	}
	if got := api.ErrorCode(err); got != code {
		t.Fatalf("%s: code = %d, want %d (%v)", method, got, code, err)
	}
	return err
}

// Project writes files into a new directory and opens it as project.
func Project(t testing.TB, a *api.App, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	for name, content := range files {
		WriteFile(t, filepath.Join(dir, name), content)
	}
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// WriteFile writes content, creating directories.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Recorder collects bus events.
type Recorder struct {
	mu     sync.Mutex
	events []bus.Event
	signal chan struct{}
}

// Record subscribes to pattern on a's bus until the test ends.
func Record(t testing.TB, a *api.App, pattern string) *Recorder {
	r := &Recorder{signal: make(chan struct{}, 1)}
	un := a.Bus().Subscribe(pattern, func(e bus.Event) {
		r.mu.Lock()
		r.events = append(r.events, e)
		r.mu.Unlock()
		select {
		case r.signal <- struct{}{}:
		default:
		}
	})
	t.Cleanup(un)
	return r
}

// Events returns the recorded events of topic ("" = all).
func (r *Recorder) Events(topic string) []bus.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []bus.Event
	for _, e := range r.events {
		if topic == "" || e.Topic == topic {
			out = append(out, e)
		}
	}
	return out
}

// Wait waits until an event of topic satisfies match (nil: any) and
// returns it.
func (r *Recorder) Wait(t testing.TB, topic string, match func(bus.Event) bool) bus.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		for _, e := range r.Events(topic) {
			if match == nil || match(e) {
				return e
			}
		}
		select {
		case <-r.signal:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timeout waiting for %s (got %d events)", topic, len(r.Events("")))
		}
	}
}

// Eventually polls cond until it is true or fails the test.
func Eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// FakePHP writes a shell script that behaves like `php` for the queries the
// interp package runs. ini lines are "key=value" separated by \n.
func FakePHP(t testing.TB, dir, name, version, ini string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake php scripts need a POSIX shell")
	}
	script := `#!/bin/sh
case "$1" in
  -m) printf '[PHP Modules]\nCore\njson\nxdebug\n' ;;
  -r)
    case "$2" in
      *PHP_VERSION*) printf '` + version + `' ;;
      *ini_get*) printf '` + strings.ReplaceAll(ini, "\n", `\n`) + `' ;;
      *) exit 3 ;;
    esac ;;
  *) exit 2 ;;
esac
`
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// NeedGit skips the test if git is not installed.
func NeedGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// Git runs git in dir with a fixed identity and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.org",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
