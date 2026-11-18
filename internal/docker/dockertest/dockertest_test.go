package dockertest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	Main()
	os.Exit(m.Run())
}

func run(t *testing.T, f *Fake, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(f.Binary, args...)
	cmd.Env = append(os.Environ(), f.Env...)
	out, err := cmd.Output()
	return string(out), err
}

func TestFake(t *testing.T) {
	f := New(t)
	if out, err := run(t, f, "context", "ls", "--format", "json"); err != nil || out != Contexts {
		t.Fatalf("contexts %q %v", out, err)
	}
	if out, err := run(t, f, "--context", "orbstack", "version", "--format", "json"); err != nil || !strings.Contains(out, "orbstack") {
		t.Fatalf("version %q %v", out, err)
	}
	f.Write(t, "down", "orbstack\n")
	if _, err := run(t, f, "--context", "orbstack", "version"); err == nil {
		t.Fatal("down context answered")
	}
	f.Write(t, "ps.json", `{"Service":"app","State":"running"}`)
	if out, _ := run(t, f, "compose", "-p", "x", "ps", "--format", "json"); !strings.Contains(out, "running") {
		t.Fatalf("ps %q", out)
	}
	f.Write(t, "php-version", "8.2.9\n")
	if out, _ := run(t, f, "compose", "exec", "-T", "app", "php", "-r", "echo PHP_VERSION;"); out != "8.2.9" {
		t.Fatalf("php %q", out)
	}
	if out, _ := run(t, f, "run", "--rm", "--entrypoint", "php", "img", "-m"); !strings.Contains(out, "xdebug") {
		t.Fatalf("modules %q", out)
	}
	if out, _ := run(t, f, "run", "--entrypoint", "php", "img", "-r", "ini_get('x')"); !strings.Contains(out, "xdebug.mode") {
		t.Fatalf("ini %q", out)
	}
	if out, _ := run(t, f, "run", "--entrypoint", "php", "img", "-r", "__TIEGEL_PROBE__"); !strings.Contains(out, `"8.2.9"`) {
		t.Fatalf("probe %q", out)
	}
	if _, err := run(t, f, "run", "--pull", "never", "--entrypoint", "php", "img", "-m"); err == nil {
		t.Fatal("pull never with missing image")
	}
	f.Write(t, "images", "img\n")
	if out, _ := run(t, f, "image", "ls", "-q", "img"); !strings.HasPrefix(out, "sha256:") {
		t.Fatalf("image %q", out)
	}
	if _, err := run(t, f, "compose", "-p", "x", "exec", "-T", "app", "sh", "-c", "kill", "sh", "7"); err != nil {
		t.Fatalf("compose exec helper: %v", err)
	}
	if _, err := run(t, f, "exec", "adminer", "sh", "-c", "kill", "sh", "7"); err != nil {
		t.Fatalf("exec helper: %v", err)
	}
	for _, bad := range [][]string{{}, {"bogus"}, {"exec", "adminer", "ls"}, {"run", "img"}, {"compose", "exec", "app", "php"}, {"compose", "exec", "php", "-r"}, {"compose", "exec", "php", "-r", "x"}} {
		if _, err := run(t, f, bad...); err == nil {
			t.Errorf("%v succeeded", bad)
		}
	}
	if !f.Called("image ls") || f.Called("never-called") || len(f.Calls()) < 10 {
		t.Fatalf("calls %v", f.Calls())
	}
	f.Remove("images")
	f.Remove("images")
	empty := &Fake{Dir: t.TempDir()}
	if empty.Calls() != nil {
		t.Fatal("calls without log")
	}
}
