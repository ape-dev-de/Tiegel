package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHistoryCommands(t *testing.T) {
	t.Parallel()
	dir, _ := gitProject(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.php", "<?php\n$a = 1;\n$b = 2;\n")
	gitIn(t, dir, "commit", "-q", "-am", "second")
	gitIn(t, dir, "mv", "a.php", "c.php")
	gitIn(t, dir, "commit", "-q", "-m", "rename")
	write("c.php", "<?php\n$a = 10;\n$b = 2;\n$c = 3;\n")

	code, out, errs := run(t, "", "git-file-diff", "c.php", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "@@ modified left 2 right 2 @@\n-$a = 1;\n+$a = 10;") ||
		!strings.Contains(out, "@@ added left before 4 right 4 @@\n+$c = 3;") || !strings.Contains(out, "2 difference(s)") {
		t.Fatalf("git-file-diff %d %q %q", code, out, errs)
	}
	code, out, _ = run(t, "", "git-file-diff", "c.php", "--base", "HEAD", "--target", "HEAD", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "identical") {
		t.Fatalf("identical %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-file-diff", "c.php", "--base", "HEAD~1", "--base-path", "a.php", "--target", "HEAD", "--json", "--project", dir)
	if code != ExitOK || !strings.Contains(out, `"identical": true`) || !strings.Contains(out, `"path": "a.php"`) {
		t.Fatalf("rename json %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-file-diff", "--project", dir); code != ExitUsage {
		t.Fatalf("missing file: %d", code)
	}

	code, out, _ = run(t, "", "git-history", "c.php", "--project", dir)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != ExitOK || len(lines) != 3 || !strings.Contains(lines[0], "a.php → c.php rename") || !strings.Contains(lines[2], "init") {
		t.Fatalf("git-history %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-history", "c.php", "--max", "1", "--skip", "1", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "second") || !strings.Contains(out, "more: --skip 2") {
		t.Fatalf("git-history page %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-history", "c.php", "--max", "x", "--project", dir); code != ExitUsage {
		t.Fatalf("bad --max: %d", code)
	}

	code, out, _ = run(t, "", "git-blame", "c.php", "--project", dir)
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if code != ExitOK || len(lines) != 4 || !strings.HasPrefix(lines[1], "00000000 (Not committed") || !strings.Contains(lines[1], "$a = 10;") ||
		!strings.Contains(lines[2], "(T ") || !strings.HasSuffix(lines[2], "$b = 2;") {
		t.Fatalf("git-blame %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-blame", "c.php", "--rev", "HEAD", "--json", "--project", dir)
	if code != ExitOK || !strings.Contains(out, `"summary": "second"`) {
		t.Fatalf("git-blame json %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-blame", "a.php", "--rev", "HEAD~1", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "$b = 2;") {
		t.Fatalf("git-blame rev %d %q", code, out)
	}

	code, out, _ = run(t, "", "git-compare", "HEAD~1", "HEAD", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "R\ta.php → c.php") {
		t.Fatalf("git-compare %d %q", code, out)
	}
	code, out, _ = run(t, "", "git-compare", "HEAD", "--project", dir)
	if code != ExitOK || strings.TrimSpace(out) != "M\tc.php" {
		t.Fatalf("git-compare work tree %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-compare", "nope", "--project", dir); code != ExitError {
		t.Fatalf("bad rev: %d", code)
	}
}
