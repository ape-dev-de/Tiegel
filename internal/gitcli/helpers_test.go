package gitcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate tests from the developer's git configuration.
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_AUTHOR_DATE", "")
	os.Unsetenv("GIT_DIR")
	os.Unsetenv("GIT_WORK_TREE")
	os.Unsetenv("GIT_INDEX_FILE")
	os.Exit(m.Run())
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// testRepo is a temporary repository for tests.
type testRepo struct {
	t   *testing.T
	dir string
	r   *Repo
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	tr := &testRepo{t: t, dir: dir}
	tr.git("init", "-q", "-b", "main")
	tr.git("config", "user.name", "Test User")
	tr.git("config", "user.email", "test@example.com")
	tr.git("config", "commit.gpgsign", "false")
	tr.git("config", "core.autocrlf", "false")
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tr.r = r
	return tr
}

// git runs a git command in the repo and returns trimmed stdout.
func (tr *testRepo) git(args ...string) string {
	tr.t.Helper()
	out, err := runGit(context.Background(), tr.dir, runOpts{}, args...)
	if err != nil {
		tr.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// gitErr runs git and returns the error.
func (tr *testRepo) gitErr(args ...string) error {
	_, err := runGit(context.Background(), tr.dir, runOpts{}, args...)
	return err
}

func (tr *testRepo) write(path, content string) {
	tr.t.Helper()
	p := filepath.Join(tr.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *testRepo) read(path string) string {
	tr.t.Helper()
	b, err := os.ReadFile(filepath.Join(tr.dir, filepath.FromSlash(path)))
	if err != nil {
		tr.t.Fatal(err)
	}
	return string(b)
}

func (tr *testRepo) commitAll(msg string) string {
	tr.t.Helper()
	tr.git("add", "-A")
	tr.git("commit", "-q", "-m", msg)
	return tr.git("rev-parse", "HEAD")
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}
