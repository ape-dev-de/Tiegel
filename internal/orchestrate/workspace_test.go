package orchestrate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitProject makes root a repository with the project in sub ("" =
// the repository root) and one commit.
func gitProject(t *testing.T, root, sub string) string {
	t.Helper()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	return filepath.Join(root, sub)
}

func TestWorktreeWorkspace(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	repo := t.TempDir()
	_ = writeFile(repo, "app/src/Calc.php", []byte(baseCalc))
	_ = writeFile(repo, "app/composer.json", []byte("{}"))
	_ = writeFile(repo, "README", []byte("r\n"))
	root := gitProject(t, repo, "app")
	// A local change of the user and an untracked file.
	_ = writeFile(root, "composer.json", []byte(`{"name":"x"}`))
	_ = writeFile(root, "notes.txt", []byte("mine"))

	if _, err := OpenWorktree(ctx, root, "p1", DirtyRefuse); !errors.Is(err, ErrDirty) {
		t.Fatal(err)
	}
	w, err := OpenWorktree(ctx, root, "p1", DirtySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if w.Dir() != filepath.Join(root, ".tiegel", "worktrees", "p1", "app") || w.Sub != "app" || !strings.Contains(w.Note, "snapshot") || !strings.Contains(w.Note, "untracked") {
		t.Fatalf("%s %q %q", w.Dir(), w.Sub, w.Note)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Dir(), "composer.json")); string(b) != `{"name":"x"}` {
		t.Fatal("snapshot not in the worktree:", string(b))
	}
	if st := git(t, repo, "status", "--porcelain"); strings.Contains(st, ".tiegel") || !strings.Contains(st, "composer.json") {
		t.Fatalf("status: %s", st)
	}
	info := w.Info()
	if info.Mode != "worktree" || info.Branch != "tiegel/plan-p1" || info.Base == "" {
		t.Fatalf("%+v", info)
	}
	// Agent edits in the worktree.
	_ = writeFile(w.Dir(), "src/Calc.php", []byte(strings.Replace(baseCalc, "return 0;", "return $a + $b;", 1)))
	_ = writeFile(w.Dir(), "tests/CalcTest.php", []byte(goodTest))
	cs, err := w.Changes(ctx)
	if err != nil || len(cs) != 2 || cs[0].Path != "src/Calc.php" || cs[0].Kind != api.ChangeModified || cs[1].Kind != api.ChangeCreated || !cs[1].Test {
		t.Fatalf("%+v %v", cs, err)
	}
	if err := w.Checkpoint(ctx, "iteration 1"); err != nil {
		t.Fatal(err)
	}
	if log := git(t, w.Root, "log", "--format=%an %s", "-1"); !strings.HasPrefix(log, "Tiegel") {
		t.Fatal(log)
	}
	// The base tree has the code before the change.
	base, err := w.BaseDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "src/Calc.php")); string(b) != baseCalc {
		t.Fatal("base:", string(b))
	}
	_ = writeFile(base, "tests/CalcTest.php", []byte(goodTest))
	if err := w.ResetBase(ctx); err != nil || fileExists(base, "tests/CalcTest.php") {
		t.Fatal("base not reset", err)
	}
	// Revert one file to the base.
	if err := w.Revert(ctx, "tests/CalcTest.php"); err != nil || fileExists(w.Dir(), "tests/CalcTest.php") {
		t.Fatal(err)
	}
	_ = writeFile(w.Dir(), "src/Calc.php", []byte("x"))
	if err := w.Revert(ctx, "src/Calc.php"); err != nil {
		t.Fatal(err)
	}
	_ = writeFile(w.Dir(), "src/Calc.php", []byte(strings.Replace(baseCalc, "return 0;", "return $a + $b;", 1)))
	// Reopen (resume).
	w2, err := OpenWorktree(ctx, root, "p1", DirtyRefuse)
	if err != nil || w2.BaseRev != w.BaseRev || !strings.Contains(w2.Note, "resumed") {
		t.Fatalf("%v %+v", err, w2)
	}
	// Apply onto the user's working copy (still dirty), no commit.
	head := git(t, repo, "rev-parse", "HEAD")
	if err := w.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src/Calc.php")); !strings.Contains(string(b), "return $a + $b") {
		t.Fatal("not applied")
	}
	if git(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("committed on the user's branch")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "notes.txt")); string(b) != "mine" {
		t.Fatal("untracked file touched")
	}
	if err := w.Remove(ctx, true); err != nil {
		t.Fatal(err)
	}
	if git(t, repo, "branch", "--list", "tiegel/plan-p1") == "" {
		t.Fatal("branch not kept")
	}
	if _, err := os.Stat(w.Root); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
	// Discard removes the branch too.
	w3, err := OpenWorktree(ctx, root, "p2", DirtyHead)
	if err != nil || !strings.Contains(w3.Note, "not part") {
		t.Fatal(err, w3.Note)
	}
	if err := w3.Remove(ctx, false); err != nil || git(t, repo, "branch", "--list", "tiegel/plan-p2") != "" {
		t.Fatal(err)
	}
}

func TestExecuteInWorktreeRedCheck(t *testing.T) {
	needGit(t)
	// QA's first test passes on the base code (it tests nothing new).
	replies := map[string]reply{
		"qa": func(ctx context.Context, tt *acptest.Turn, n int) any {
			_ = tt.WriteFile(ctx, "tests/CalcTest.php", goodTest)
			r := map[string]any{"summary": "ok", "tests": []map[string]any{{"case": "T1", "file": "tests/CalcTest.php", "tests": []string{"CalcTest::testAdds"}}}}
			if n > 0 {
				r["responses"] = []map[string]any{{"item": "Q2", "verdict": "accept"}}
			}
			return r
		},
	}
	h := newHarness(t, replies, Config{Subagents: true, MaxIterations: 3})
	gitProject(t, h.root, "")
	calls := 0
	h.runner.pass = func(dir string) bool {
		calls++
		src, _ := os.ReadFile(filepath.Join(dir, "src/Calc.php"))
		if strings.Contains(dir, "-base") {
			// First red check: the test passes on the base (bad); later
			// QA's fixed test fails there (good).
			return calls < 3
		}
		return strings.Contains(string(src), "return $a + $b")
	}
	_, st := h.planned()
	w, err := OpenWorktree(context.Background(), h.root, st.Doc.ID, DirtySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	e := execute(t, h, st, w)
	s := e.State()
	var red *api.PlanReviewItem
	for i := range s.Doc.Review {
		if s.Doc.Review[i].From == "gate:red" {
			red = &s.Doc.Review[i]
		}
	}
	if red == nil || !strings.Contains(red.Claim, "passes on the base code") || red.Status != api.ReviewResolved {
		t.Fatalf("%+v", red)
	}
	if s.Doc.Status != api.PlanDone || s.Workspace == nil || s.Workspace.Mode != "worktree" {
		t.Fatalf("%s %+v %+v", s.Doc.Status, s.Doc.Gates, s.Workspace)
	}
	// The agents worked in the worktree, the project is untouched.
	if h.read("src/Calc.php") != baseCalc || !fileExists(w.Dir(), "tests/CalcTest.php") {
		t.Fatal("edits outside the worktree")
	}
	if fileExists(w.Root+"-base", "tests/CalcTest.php") {
		t.Fatal("base tree not reset")
	}
	if err := w.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}
