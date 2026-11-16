package headless

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

func runWith(t *testing.T, mod func(*Env), args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	env := testEnv(t, nil, &out, io.Discard)
	mod(&env)
	return Run(context.Background(), args, env), out.String()
}

func TestUpdateCheckCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			_, _ = w.Write([]byte(`{"tag_name":"v0.0.1","html_url":"https://example.org/old"}`))
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"https://example.org/r"}`))
	}))
	defer srv.Close()
	code, out := runWith(t, func(e *Env) { e.Options.UpdateSource = srv.URL + "/new" }, "update-check")
	if code != ExitOK || !strings.Contains(out, "update available: 99.0.0") {
		t.Fatalf("update-check %d %q", code, out)
	}
	code, out = runWith(t, func(e *Env) { e.Options.UpdateSource = srv.URL + "/old" }, "update-check", "--prereleases")
	if code != ExitOK || out == "" {
		t.Fatalf("update-check old %d %q", code, out)
	}
	if code, _ = runWith(t, func(e *Env) { e.Options.UpdateSource = "http://example.invalid/x" }, "update-check"); code != ExitError {
		t.Fatal("non-https remote source must fail")
	}
}

func TestInterpreterCommandsWithFakePHP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  -m) printf 'Core\\njson\\n' ;;\n  -r) case \"$2\" in *PHP_VERSION*) printf '8.3.1' ;; *) printf 'xdebug.mode=debug\\n' ;; esac ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "php"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	cfg := filepath.Join(t.TempDir(), "cfg")
	shared := func(e *Env) {
		e.Options.Interp = interp.DiscoverOptions{Dirs: []string{bin}}
		e.Options.ConfigDir = cfg
	}
	code, out := runWith(t, shared, "interpreters", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "8.3.1") {
		t.Fatalf("interpreters %q", out)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(bin, "php"))
	if err != nil {
		t.Fatal(err)
	}
	id := "local:" + real
	if code, out = runWith(t, shared, "interpreters", "select", id, "--project", dir); code != ExitOK || !strings.Contains(out, "selected") {
		t.Fatalf("select %d %q", code, out)
	}
	code, out = runWith(t, shared, "interpreters", "version", "--modules", "--ini", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "8.3.1") || !strings.Contains(out, "module\tjson") || !strings.Contains(out, "ini\txdebug.mode=debug") {
		t.Fatalf("version %d %q", code, out)
	}
	if _, out = runWith(t, shared, "interpreters", "--project", dir); !strings.Contains(out, "* local") {
		t.Fatalf("selected mark %q", out)
	}
}

func TestGitStatusCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "T"}, {"config", "user.email", "t@example.org"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	code, out := runWith(t, func(*Env) {}, "git-status", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "## main") || !strings.Contains(out, "?? a.php") {
		t.Fatalf("git-status %d %q", code, out)
	}
	if code, out = runWith(t, func(*Env) {}, "info", "--project", dir); code != ExitOK || !strings.Contains(out, "yes") {
		t.Fatalf("info %q", out)
	}
	if code, out = runWith(t, func(*Env) {}, "git-status", "--json", "--project", dir); code != ExitOK || !strings.Contains(out, `"files"`) {
		t.Fatalf("git-status json %q", out)
	}
}
