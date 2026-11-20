package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// divergedProject has main and feature with conflicting changes of a.php.
func divergedProject(t *testing.T) string {
	t.Helper()
	dir, _ := gitProject(t)
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "a.php"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	write("<?php\necho 'feature';\n")
	gitIn(t, dir, "commit", "-q", "-am", "feature")
	gitIn(t, dir, "checkout", "-q", "main")
	write("<?php\necho 'main';\n")
	gitIn(t, dir, "commit", "-q", "-am", "main")
	return dir
}

func TestGitMergeCommand(t *testing.T) {
	t.Parallel()
	dir := divergedProject(t)
	for _, args := range [][]string{{"git-merge"}, {"git-merge", "feature", "--abort"}, {"git-merge", "feature", "--no-ff", "--ff-only"}} {
		if code, _, _ := run(t, "", append(args, "--project", dir)...); code != ExitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	code, out, errs := run(t, "", "git-merge", "feature", "--project", dir)
	if code != ExitError || !strings.Contains(out, "merge stopped with conflicts:\n  a.php") || !strings.Contains(errs, "--continue") {
		t.Fatalf("conflict %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "git-merge", "--abort", "--project", dir); code != ExitOK || out != "merge aborted\n" {
		t.Fatalf("abort %d %q", code, out)
	}
	run(t, "", "git-merge", "feature", "--project", dir)
	if err := os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "a.php")
	if code, out, errs = run(t, "", "git-merge", "--continue", "--json", "--project", dir); code != ExitOK || !strings.Contains(out, `"state": ""`) {
		t.Fatalf("continue %d %q %q", code, out, errs)
	}
	if code, out, _ = run(t, "", "git-merge", "feature", "--project", dir); code != ExitOK || out != "already up to date\n" {
		t.Fatalf("up to date %d %q", code, out)
	}
}

func TestGitRebaseCommand(t *testing.T) {
	t.Parallel()
	dir := divergedProject(t)
	gitIn(t, dir, "checkout", "-q", "feature")
	if code, _, _ := run(t, "", "git-rebase", "--project", dir); code != ExitUsage {
		t.Fatalf("no args: exit %d", code)
	}
	code, out, _ := run(t, "", "git-rebase", "main", "--project", dir)
	if code != ExitError || !strings.Contains(out, "rebase stopped with conflicts") {
		t.Fatalf("conflict %d %q", code, out)
	}
	if code, _, errs := run(t, "", "git-rebase", "--abort", "--project", dir); code != ExitOK {
		t.Fatalf("abort %d %q", code, errs)
	}
	if code, _, errs := run(t, "", "git-rebase", "--continue", "--project", dir); code != ExitError || !strings.Contains(errs, "no operation in progress") {
		t.Fatalf("continue without rebase %d %q", code, errs)
	}
}
