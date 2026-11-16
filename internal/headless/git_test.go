package headless

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
}

// gitProject creates a repository with one commit and a bare remote.
func gitProject(t *testing.T) (dir, remote string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = project(t, map[string]string{"a.php": "<?php\n", "b.php": "<?php\n"})
	remote = filepath.Join(t.TempDir(), "remote.git")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.name", "T"}, {"config", "user.email", "t@example.org"},
		{"config", "commit.gpgsign", "false"}, {"config", "core.autocrlf", "false"}, {"add", "-A"}, {"commit", "-q", "-m", "init"},
		{"init", "-q", "--bare", "-b", "main", remote}, {"remote", "add", "origin", remote},
	} {
		gitIn(t, dir, args...)
	}
	return dir, remote
}

func TestGitCommands(t *testing.T) {
	t.Parallel()
	dir, _ := gitProject(t)
	if err := os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php\necho 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "git-diff", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "+echo 1;") {
		t.Fatalf("git-diff %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-diff", "a.php", "--json", "--project", dir)
	if code != ExitOK || !strings.Contains(out, `"hunks"`) {
		t.Fatalf("git-diff json %d %q", code, out)
	}
	if code, out, _ = run(t, "", "git-diff", "--staged", "--project", dir); code != ExitOK || out != "" {
		t.Fatalf("git-diff staged %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-commit", "--project", dir); code != ExitUsage {
		t.Fatalf("git-commit without message: %d", code)
	}
	code, out, errs := run(t, "", "git-commit", "-m", "echo", "a.php", "--push", "--project", dir)
	if code != ExitOK || len(strings.TrimSpace(strings.Split(out, "\n")[0])) != 40 || !strings.Contains(out, "pushed main to origin/main") {
		t.Fatalf("git-commit %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "git-commit", "--amend", "-m", "echo amended", "--json", "--project", dir); code != ExitOK || !strings.Contains(out, `"hash"`) {
		t.Fatalf("amend %d %q", code, out)
	}
	// The amended commit is not a fast-forward of the pushed one.
	code, _, errs = run(t, "", "git-push", "--project", dir)
	if code != ExitError || !strings.Contains(errs, "rejected") {
		t.Fatalf("git-push rejected %d %q", code, errs)
	}
	gitIn(t, dir, "add", "b.php")
	if err := os.WriteFile(filepath.Join(dir, "b.php"), []byte("<?php\n// b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "b.php")
	code, out, errs = run(t, "", "git-commit", "--message", "index", "--push", "--project", dir)
	if code != ExitError || !strings.Contains(errs, "push failed") || out == "" {
		t.Fatalf("index commit with failing push %d %q %q", code, out, errs)
	}
	code, out, _ = run(t, "", "git-push", "--remote", "origin", "--json", "--project", t.TempDir())
	if code != ExitError {
		t.Fatalf("push outside repo %d %q", code, out)
	}
}

func TestGitE2E(t *testing.T) {
	t.Parallel()
	dir, remote := gitProject(t)
	bin := buildBinary(t)
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php\necho 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e2e(t, bin, cfg, "", "--headless", "git-commit", "-m", "e2e", "a.php", "--push", "--json", "--project", dir)
	if code != 0 {
		t.Fatalf("git-commit: %d %s", code, errs)
	}
	var res struct {
		Hash   string `json:"hash"`
		Pushed bool   `json:"pushed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Pushed || res.Hash == "" {
		t.Fatalf("result %v %q", err, out)
	}
	cmd := exec.Command("git", "--git-dir", remote, "log", "-1", "--format=%s", "main")
	if b, err := cmd.Output(); err != nil || strings.TrimSpace(string(b)) != "e2e" {
		t.Fatalf("remote log %q %v", b, err)
	}
	code, out, _ = e2e(t, bin, cfg, "", "--headless", "git-diff", "--base", "HEAD~1", "--project", dir)
	if code != 0 || !strings.Contains(out, "+echo 2;") {
		t.Fatalf("git-diff %d %q", code, out)
	}
	code, _, errs = e2e(t, bin, cfg, "", "--headless", "modules", "disable", "git")
	if code != 0 {
		t.Fatalf("disable: %s", errs)
	}
	code, _, errs = e2e(t, bin, cfg, "", "--headless", "git-diff", "--project", dir)
	if code != 1 || !strings.Contains(errs, "module disabled") {
		t.Fatalf("disabled module: %d %q", code, errs)
	}
}
