package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitRemoteCommands(t *testing.T) {
	t.Parallel()
	dir, remote := gitProject(t)
	if code, _, errs := run(t, "", "git-pull", "--project", dir); code != ExitError || !strings.Contains(errs, "noUpstream") {
		t.Fatalf("pull without upstream %d %q", code, errs)
	}
	gitIn(t, dir, "push", "-q", "-u", "origin", "main")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, dir, "clone", "-q", "-b", "main", remote, other)
	for _, args := range [][]string{{"config", "user.name", "O"}, {"config", "user.email", "o@example.org"}, {"config", "commit.gpgsign", "false"}} {
		gitIn(t, other, args...)
	}
	push := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(other, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, other, "commit", "-q", "-am", file)
		gitIn(t, other, "push", "-q", "origin", "main")
	}

	code, out, errs := run(t, "", "git-fetch", "--prune", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "up to date") {
		t.Fatalf("fetch %d %q %q", code, out, errs)
	}
	push("a.php", "<?php\n// theirs\n")
	code, out, _ = run(t, "", "git-fetch", "--remote", "origin", "--json", "--project", dir)
	if code != ExitOK || !strings.Contains(out, `"refs/remotes/origin/main"`) {
		t.Fatalf("fetch json %d %q", code, out)
	}
	code, out, errs = run(t, "", "git-update", "--strategy", "ff-only", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "fast-forwarded main") || !strings.Contains(out, "a.php") {
		t.Fatalf("update %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "git-pull", "--project", dir); code != ExitOK || !strings.Contains(out, "up to date") {
		t.Fatalf("pull up to date %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-pull", "--strategy", "nope", "--project", dir); code != ExitUsage {
		t.Fatalf("bad strategy %d", code)
	}

	// Conflict: exit 1 with the conflicted file listed.
	push("b.php", "<?php\n// theirs\n")
	if err := os.WriteFile(filepath.Join(dir, "b.php"), []byte("<?php\n// ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "ours")
	code, out, _ = run(t, "", "git-pull", "--remote", "origin", "--branch", "main", "--project", dir)
	if code != ExitError || !strings.Contains(out, "U b.php") {
		t.Fatalf("pull conflict %d %q", code, out)
	}
	gitIn(t, dir, "merge", "--abort")
}

func TestGitStashCommand(t *testing.T) {
	t.Parallel()
	dir, _ := gitProject(t)
	if code, out, _ := run(t, "", "git-stash", "--project", dir); code != ExitOK || out != "" {
		t.Fatalf("empty list %d %q", code, out)
	}
	if code, out, _ := run(t, "", "git-stash", "push", "--project", dir); code != ExitOK || !strings.Contains(out, "no local changes") {
		t.Fatalf("nothing %d %q", code, out)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.php", "<?php\n// wip\n")
	write("new.php", "<?php\n")
	code, out, _ := run(t, "", "git-stash", "push", "-m", "wip", "--untracked", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "stash@{0}") || !strings.Contains(out, "wip") {
		t.Fatalf("push %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.php")); err == nil {
		t.Fatal("untracked file not stashed")
	}
	if code, out, _ = run(t, "", "git-stash", "list", "--json", "--project", dir); code != ExitOK || !strings.Contains(out, `"ref": "stash@{0}"`) {
		t.Fatalf("list %d %q", code, out)
	}
	if code, out, _ = run(t, "", "git-stash", "apply", "stash@{0}", "--project", dir); code != ExitOK || !strings.Contains(out, "applied") {
		t.Fatalf("apply %d %q", code, out)
	}
	gitIn(t, dir, "checkout", "--", "a.php")
	gitIn(t, dir, "clean", "-fdq")
	if code, out, _ = run(t, "", "git-stash", "pop", "0", "--restore-index", "--project", dir); code != ExitOK || !strings.Contains(out, "dropped") {
		t.Fatalf("pop %d %q", code, out)
	}
	// Conflicting pop: exit 1.
	gitIn(t, dir, "stash", "push", "-q")
	write("a.php", "<?php\n// committed\n")
	gitIn(t, dir, "commit", "-q", "-am", "c")
	if code, out, _ = run(t, "", "git-stash", "pop", "--project", dir); code != ExitError || !strings.Contains(out, "U a.php") {
		t.Fatalf("pop conflict %d %q", code, out)
	}
	gitIn(t, dir, "reset", "-q", "--hard")
	if code, out, _ = run(t, "", "git-stash", "drop", "0", "--project", dir); code != ExitOK || out != "" {
		t.Fatalf("drop %d %q", code, out)
	}
	for _, args := range [][]string{{"git-stash", "bogus"}, {"git-stash", "drop", "x"}, {"git-stash", "pop", "1", "2"}, {"git-stash", "list", "x"}} {
		if code, _, _ := run(t, "", append(args, "--project", dir)...); code != ExitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
	if code, _, _ := run(t, "", "git-stash", "drop", "--project", dir); code != ExitError {
		t.Errorf("drop missing: %d", code)
	}
}
