//go:build !windows

package qa

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

// localShellImage returns a local image with a POSIX shell (never pulled):
// $TIEGEL_TEST_SH_IMAGE or one of a few common ones.
func localShellImage(ctx context.Context) string {
	cands := []string{os.Getenv("TIEGEL_TEST_SH_IMAGE"), "alpine:latest", "busybox:latest", "debian:bookworm-slim", "redis:7.4-alpine", "php:8.4-cli", "php:8.3-cli"}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if exec.CommandContext(ctx, "docker", "image", "inspect", c).Run() == nil {
			return c
		}
	}
	return ""
}

// fakeContainerPint formats only inside a container (/.dockerenv), so
// the test proves the run went through the Compose service.
const fakeContainerPint = `#!/bin/sh
for a in "$@"; do f="$a"; done
[ -f /.dockerenv ] || { echo "not in a container" >&2; exit 9; }
sed 's/array(1,2)/[1, 2]/' "$f" > "$f.out" && mv "$f.out" "$f"
`

// TestPintFormatOnSaveInCompose: format on save runs Pint in the
// project's Compose service (exec into the running container) and the
// result lands in the editor buffer and on disk. Real Pint is used when
// $TIEGEL_TEST_PINT_IMAGE names a local image with pint on its PATH,
// otherwise a fake vendor/bin/pint in a local shell image. Skipped
// without docker or without a suitable local image; nothing is pulled.
func TestPintFormatOnSaveInCompose(t *testing.T) {
	if testing.Short() {
		t.Skip("real docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if exec.CommandContext(ctx, "docker", "info").Run() != nil {
		t.Skip("docker engine not reachable")
	}
	image, real := os.Getenv("TIEGEL_TEST_PINT_IMAGE"), true
	if image == "" || exec.CommandContext(ctx, "docker", "image", "inspect", image).Run() != nil {
		image, real = localShellImage(ctx), false
	}
	if image == "" {
		t.Skip("no suitable local image (set TIEGEL_TEST_SH_IMAGE or TIEGEL_TEST_PINT_IMAGE)")
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	name := "tiegel-qa-test-" + hex.EncodeToString(b[:])
	// A local tag whose name suggests PHP, so that the service is offered
	// as PHP interpreter (removed again; the image itself stays).
	tag := name + "-php:test"
	if out, err := exec.CommandContext(ctx, "docker", "tag", image, tag).CombinedOutput(); err != nil {
		t.Skipf("docker tag: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", tag).Run() })
	mods := []module.Module{phpruntime.New(phpruntime.Options{Docker: "docker"})}
	for _, p := range All() {
		mods = append(mods, p)
	}
	a := modtest.NewApp(t, mods, nil)
	files := map[string]string{
		"compose.yaml": "name: " + name + "\nservices:\n  php:\n    image: " + tag + "\n    pull_policy: never\n" +
			"    environment:\n      PHP_QA_TEST: \"1\"\n" +
			"    entrypoint: [\"sleep\", \"600\"]\n    working_dir: /app\n    volumes:\n      - ./:/app\n",
		"a.php": source,
	}
	if !real {
		files["vendor/bin/pint"] = fakeContainerPint
	}
	dir := modtest.Project(t, a, files)
	if !real {
		if err := os.Chmod(filepath.Join(dir, "vendor", "bin", "pint"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	up := exec.CommandContext(ctx, "docker", "compose", "up", "-d", "--pull", "never")
	up.Dir = dir
	if out, err := up.CombinedOutput(); err != nil {
		t.Skipf("docker compose up: %v %s", err, out)
	}
	t.Cleanup(func() {
		down := exec.Command("docker", "compose", "down", "-t", "0")
		down.Dir = dir
		_ = down.Run()
	})

	var list []phpruntime.InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	id := ""
	for _, i := range list {
		if i.Kind == "compose" {
			id = i.ID
		}
	}
	if id == "" {
		t.Fatalf("no compose interpreter: %+v", list)
	}
	modtest.Call(t, a, "interp.select", phpruntime.InterpSelectParams{ID: id}, nil)
	if real {
		set(t, a, PintID+".path", "pint")
	}
	set(t, a, PintID+".formatOnSave", true)
	var st []api.QAToolInfo
	modtest.Call(t, a, "qa.list", nil, &st)
	for _, i := range st {
		if i.Plugin == PintID && !i.Available {
			t.Fatalf("pint not available: %+v", i)
		}
	}

	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "a.php", "at": map[string]int{"offset": len(source)}, "text": "\n"}, nil)
	rec := modtest.Record(t, a, api.TopicQAFinished)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	for _, e := range rec.Events(api.TopicQAFinished) {
		if ev := e.Payload.(api.QARunEvent); ev.Error != "" {
			t.Fatalf("format on save failed: %s", ev.Error)
		}
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "a.php"))
	if !strings.Contains(string(disk), "[1, 2]") || strings.Contains(string(disk), "array(") {
		t.Fatalf("not formatted in the container: %q", disk)
	}
	if got := docText(t, a, "a.php"); got != string(disk) {
		t.Fatalf("buffer %q, disk %q", got, disk)
	}
}
