package gitcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenAndInfo(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	info, err := tr.r.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "main" || !info.Unborn || info.Head != "" || info.Detached || info.State != StateClean {
		t.Errorf("unborn info: %+v", info)
	}
	tr.write("sub/dir/a.txt", "a\n")
	h := tr.commitAll("first")

	// Open from a subdirectory finds the root.
	r, err := Open(ctx, filepath.Join(tr.dir, "sub", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != tr.r.Root || r.GitDir != filepath.Join(r.Root, ".git") || r.CommonDir != r.GitDir {
		t.Errorf("repo: %+v", r)
	}
	info, err = r.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Head != h || info.Unborn || info.Branch != "main" {
		t.Errorf("info: %+v", info)
	}
	if got, _ := r.ResolveCommit(ctx, "main"); got != h {
		t.Errorf("ResolveCommit = %q", got)
	}
	if _, err := r.ResolveCommit(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveCommit(nope) = %v", err)
	}

	tr.git("checkout", "-q", "--detach", "HEAD")
	info, _ = r.Info(ctx)
	if !info.Detached || info.Branch != "" || info.Head != h {
		t.Errorf("detached: %+v", info)
	}

	rel, err := r.RelPath(filepath.Join(tr.dir, "sub", "dir", "a.txt"))
	if err != nil || rel != "sub/dir/a.txt" {
		t.Errorf("RelPath = %q, %v", rel, err)
	}
	rel, err = r.RelPath(filepath.Join(tr.dir, "sub", "new", "file.txt"))
	if err != nil || rel != "sub/new/file.txt" {
		t.Errorf("RelPath (missing file) = %q, %v", rel, err)
	}
	if _, err := r.RelPath(filepath.Dir(tr.dir)); err == nil {
		t.Error("RelPath outside root must fail")
	}
}

func TestOpenNotRepository(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	_, err := Open(ctxT(t), dir)
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("got %v", err)
	}
	if _, err := Open(ctxT(t), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir must fail")
	}
}

func TestOpenBare(t *testing.T) {
	tr := newTestRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	tr.git("init", "-q", "--bare", bare)
	if _, err := Open(ctxT(t), bare); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("bare: %v", err)
	}
}

func TestWorktreeCommonDir(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a", "a\n")
	tr.commitAll("a")
	wt := filepath.Join(t.TempDir(), "wt")
	tr.git("worktree", "add", "-q", wt, "-b", "side")
	r, err := Open(ctxT(t), wt)
	if err != nil {
		t.Fatal(err)
	}
	if r.CommonDir == r.GitDir || !strings.HasSuffix(filepath.ToSlash(r.GitDir), "worktrees/wt") {
		t.Errorf("worktree dirs: %+v", r)
	}
	info, err := r.Info(ctxT(t))
	if err != nil || info.Branch != "side" {
		t.Errorf("worktree info %+v %v", info, err)
	}
}

func TestRepoStates(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a", "a\n")
	tr.commitAll("a")
	cases := map[string]RepoState{
		"MERGE_HEAD":               StateMerging,
		"CHERRY_PICK_HEAD":         StateCherryPick,
		"REVERT_HEAD":              StateReverting,
		"BISECT_LOG":               StateBisecting,
		"rebase-apply/x":           StateRebasing,
		"rebase-apply/applying":    StateApplyingAM,
		"rebase-merge/x":           StateRebasing,
		"rebase-merge/interactive": StateRebaseInter,
	}
	for name, want := range cases {
		p := filepath.Join(tr.r.GitDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := tr.r.state(); got != want {
			t.Errorf("%s: state %q, want %q", name, got, want)
		}
		os.RemoveAll(filepath.Join(tr.r.GitDir, strings.Split(name, "/")[0]))
	}
	if got := tr.r.state(); got != StateClean {
		t.Errorf("clean: %q", got)
	}
}

func TestRunnerErrors(t *testing.T) {
	tr := newTestRepo(t)
	_, err := tr.r.run(ctxT(t), runOpts{}, "no-such-command")
	var gerr *Error
	if !errors.As(err, &gerr) || gerr.ExitCode == 0 || gerr.Stderr == "" {
		t.Fatalf("got %#v", err)
	}
	if !strings.HasPrefix(gerr.Error(), "git no-such-command:") {
		t.Errorf("Error() = %q", gerr.Error())
	}
	if gerr.Unwrap() == nil {
		t.Error("Unwrap nil")
	}
	if exitCode(nil) != -1 || stderrOf(nil) != "" {
		t.Error("helpers on nil")
	}
	e := &Error{Args: []string{"x"}, Err: errors.New("boom")}
	if e.Error() != "git x: boom" {
		t.Errorf("Error() = %q", e.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.r.run(ctx, runOpts{}, "status"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	time.Sleep(time.Millisecond)
	if _, err := tr.r.run(ctx2, runOpts{}, "status"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("deadline: %v", err)
	}
}

func TestBaseEnv(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	env := baseEnv([]string{"X_EXTRA=1"})
	seen := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := seen[k]; dup && k == "LC_ALL" {
			t.Errorf("duplicate %s", k)
		}
		seen[k] = v
	}
	if seen["LC_ALL"] != "C" || seen["GIT_TERMINAL_PROMPT"] != "0" || seen["X_EXTRA"] != "1" || seen["GIT_LITERAL_PATHSPECS"] != "1" {
		t.Errorf("env: %v", seen)
	}
}

func TestGitNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Open(context.Background(), "."); !errors.Is(err, ErrGitNotFound) {
		t.Fatalf("got %v", err)
	}
}
