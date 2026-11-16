package modtest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

func TestHelpers(t *testing.T) {
	a := NewApp(t, nil, func(o *api.Options) { o.AppVersion = "x" })
	rec := Record(t, a, "*")
	dir := Project(t, a, map[string]string{"sub/a.txt": "hi"})
	rec.Wait(t, api.TopicProjectOpened, func(e bus.Event) bool { return e.Payload.(api.ProjectEvent).Root == dir })
	if len(rec.Events("")) == 0 || len(rec.Events("nope")) != 0 {
		t.Fatal("events")
	}
	var info api.AppInfo
	Call(t, a, "app.info", nil, &info)
	if info.Version != "x" {
		t.Fatal(info)
	}
	if err := WantCode(t, a, "nope.x", nil, api.CodeMethodNotFound); err == nil {
		t.Fatal("no error")
	}
	if err := TryCall(a, "app.info", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := TryCall(a, "app.info", func() {}, nil); err == nil {
		t.Fatal("unencodable params")
	}
	n := 0
	Eventually(t, "counter", func() bool { n++; return n > 2 })

	php := FakePHP(t, t.TempDir(), "php", "8.3.1", "xdebug.mode=debug\n")
	out, err := exec.Command(php, "-r", "echo PHP_VERSION;").Output()
	if err != nil || string(out) != "8.3.1" {
		t.Fatal(string(out), err)
	}
	if out, _ := exec.Command(php, "-r", "ini_get").Output(); !strings.Contains(string(out), "xdebug.mode=debug") {
		t.Fatal(string(out))
	}
	if _, err := exec.LookPath("git"); err == nil {
		NeedGit(t)
		repo := t.TempDir()
		Git(t, repo, "init", "-q")
		if out := Git(t, repo, "rev-parse", "--git-dir"); strings.TrimSpace(out) != ".git" {
			t.Fatal(out)
		}
	}
	WriteFile(t, filepath.Join(t.TempDir(), "x", "y"), "z")
}
