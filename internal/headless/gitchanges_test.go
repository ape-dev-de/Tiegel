package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitChangesAndDiffTab(t *testing.T) {
	t.Parallel()
	dir, _ := gitProject(t)
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.php", "<?php\necho 'a';\n")
	write("src/Mail/Mailer.php", "<?php\nclass Mailer {}\n")
	gitIn(t, dir, "add", "src/Mail/Mailer.php")
	write("notes.md", "todo\n")

	code, out, errs := run(t, "", "git-changes", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "Changes  2 files [main]") || !strings.Contains(out, "src/Mail  1 file") ||
		!strings.Contains(out, "added       src/Mail/Mailer.php") || !strings.Contains(out, "Unversioned Files  1 file") {
		t.Fatalf("git-changes %d %q %q", code, out, errs)
	}
	code, out, _ = run(t, "", "git-changes", "--group-by", "none", "--json", "--project", dir)
	var res struct {
		Order []string `json:"order"`
	}
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || strings.Join(res.Order, ",") != "a.php,src/Mail/Mailer.php,notes.md" {
		t.Fatalf("git-changes json %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-changes", "--group-by", "bogus", "--project", dir); code == ExitOK {
		t.Fatal("bad grouping accepted")
	}

	code, out, errs = run(t, "", "git-diff-tab", "a.php", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "a.php (HEAD ↔ local)  [2/3]  prev: src/Mail/Mailer.php  next: notes.md") || !strings.Contains(out, "+echo 'a';") {
		t.Fatalf("git-diff-tab %d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", "git-diff-tab", "a.php", "--prev", "--mode", "staged", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "Mailer.php (empty ↔ index)  [1/3]") || !strings.Contains(out, "+class Mailer {}") {
		t.Fatalf("git-diff-tab next %d %q %q", code, out, errs)
	}
	code, out, _ = run(t, "", "git-diff-tab", "a.php", "--next", "--json", "--project", dir)
	if code != ExitOK || !strings.Contains(out, `"path": "notes.md"`) || !strings.Contains(out, `"base": "empty"`) {
		t.Fatalf("git-diff-tab next json %d %q", code, out)
	}
	if code, _, _ = run(t, "", "git-diff-tab", "a.php", "--next", "--prev", "--project", dir); code != ExitUsage {
		t.Fatalf("next+prev: %d", code)
	}
	if code, _, _ = run(t, "", "git-diff-tab", "b.php", "--project", dir); code == ExitOK {
		t.Fatal("unchanged file accepted")
	}
	// Sign-off reaches the commit.
	code, _, errs = run(t, "", "git-commit", "-m", "signed", "--signoff", "--no-verify", "a.php", "--project", dir)
	if code != ExitOK {
		t.Fatalf("git-commit signoff %d %q", code, errs)
	}
	code, out, _ = run(t, "", "git-diff-tab", "src/Mail/Mailer.php", "--mode", "unstaged", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "(index ↔ local)") || !strings.Contains(out, "identical") {
		t.Fatalf("unstaged %d %q", code, out)
	}
}
