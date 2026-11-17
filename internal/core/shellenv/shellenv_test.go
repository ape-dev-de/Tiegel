package shellenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoginPathParsesAndCaches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell on windows")
	}
	calls := 0
	r := New("/bin/fake", func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "/bin/fake" || args[0] != "-l" {
			t.Fatalf("unexpected call %s %v", name, args)
		}
		return []byte("motd noise\n" + marker + "/a:/b" + marker), nil
	})
	for i := 0; i < 2; i++ {
		p, err := r.LoginPath(context.Background())
		if err != nil || p != "/a:/b" {
			t.Fatalf("got %q, %v", p, err)
		}
	}
	if calls != 1 {
		t.Fatalf("shell called %d times, want 1", calls)
	}
}

func TestLoginPathErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell on windows")
	}
	r := New("/bin/fake", func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("boom") })
	if _, err := r.LoginPath(context.Background()); err == nil {
		t.Fatal("want error")
	}
	r = New("/bin/fake", func(context.Context, string, ...string) ([]byte, error) { return []byte("no marker"), nil })
	if _, err := r.LoginPath(context.Background()); err == nil {
		t.Fatal("want error for missing marker")
	}
	t.Setenv("PATH", "/x")
	if got := r.Path(context.Background()); got != "/x" {
		t.Fatalf("Path fallback = %q", got)
	}
}

func TestMergeDedup(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := Merge("/a"+sep+"/b", "/b"+sep+"/c"+sep, "")
	if got != strings.Join([]string{"/a", "/b", "/c"}, sep) {
		t.Fatalf("Merge = %q", got)
	}
}

func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	name := "tool"
	file := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(file, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := LookPath(name, filepath.Join(dir, "missing")+string(os.PathListSeparator)+dir)
	if err != nil || p != file {
		t.Fatalf("LookPath = %q, %v", p, err)
	}
	if _, err := LookPath("nope", dir); err == nil {
		t.Fatal("want not found")
	}
}

func TestRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	p, err := New("/bin/sh", nil).LoginPath(context.Background())
	if err != nil {
		t.Skipf("login shell unavailable: %v", err)
	}
	if p == "" {
		t.Fatal("empty PATH from real shell")
	}
}
