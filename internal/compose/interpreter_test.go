package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

// fakeDocker writes a docker stand-in that logs its arguments to args.log,
// answers `ps` with ps.json and behaves like php for exec/run.
func fakeDocker(t *testing.T, psJSON string) (bin, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker needs a POSIX shell")
	}
	dir = t.TempDir()
	script := `#!/bin/sh
d=$(dirname "$0")
printf "%s\n" "$*" >> "$d/args.log"
for a in "$@"; do
  if [ "$a" = "ps" ]; then
    if [ -f "$d/ps.fail" ]; then echo "daemon down" >&2; exit 1; fi
    cat "$d/ps.json"; exit 0
  fi
done
# Skip to the service name "php", then past the php binary.
while [ $# -gt 0 ] && [ "$1" != "php" ]; do shift; done
shift
[ "$1" = "php" ] && shift
case "$1" in
  -m) printf '[PHP Modules]\nCore\nxdebug\n\n[Zend Modules]\nXdebug\n' ;;
  -r)
    case "$2" in
      *PHP_VERSION*) printf '8.3.12' ;;
      *ini_get*) printf 'xdebug.mode=debug\nxdebug.client_port=9003\n' ;;
    esac ;;
  *) echo "bad php args: $*" >&2; exit 2 ;;
esac
`
	bin = filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ps.json"), []byte(psJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func readLog(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "args.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

const psRunning = `{"Name":"multi-php-1","Service":"php","State":"running","Health":"","Status":"Up 2 minutes"}
{"Name":"multi-db-1","Service":"db","State":"exited","Status":"Exited (0)"}
`

func TestInterpreterMetadata(t *testing.T) {
	p := load(t, "multi", LoadOptions{Profiles: []string{"dev"}})
	c := NewInterpreter(p, "php")
	var _ interp.Interpreter = c
	if c.Kind() != interp.KindCompose || c.ID() != "compose:multi:php" {
		t.Errorf("Kind/ID = %s %s", c.Kind(), c.ID())
	}
	if c.Label() != "PHP (compose multi/php)" {
		t.Errorf("Label = %q", c.Label())
	}
	if r, ok := c.Mappings.ToRemote(filepath.Join(p.Dir, "a.php")); !ok || r != "/srv/a.php" {
		t.Errorf("mapping = %q %v", r, ok)
	}
	want := []string{"compose", "--project-directory", p.Dir, "-p", "multi",
		"-f", p.Files[0], "-f", p.Files[1], "--profile", "dev"}
	if got := c.BaseArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("BaseArgs = %v", got)
	}
	exec := c.ExecArgs(true, "-v")
	if got := strings.Join(exec[len(want):], " "); got != "exec -T php php -v" {
		t.Errorf("exec args = %q", got)
	}
	c.PHP = "/usr/local/bin/php"
	run := c.ExecArgs(false, "-v")
	if got := strings.Join(run[len(want):], " "); got != "run --rm -T --no-deps php /usr/local/bin/php -v" {
		t.Errorf("run args = %q", got)
	}
	c.RunDeps = true
	run = c.ExecArgs(false)
	if got := strings.Join(run[len(want):], " "); got != "run --rm -T php /usr/local/bin/php" {
		t.Errorf("run with deps = %q", got)
	}

	// Unknown service: no mappings, still usable.
	if u := NewInterpreter(p, "ghost"); len(u.Mappings) != 0 {
		t.Error("ghost has no mappings")
	}
	// Minimal interpreter without dir/project.
	bare := &Interpreter{Service: "s"}
	if got := bare.BaseArgs(); !reflect.DeepEqual(got, []string{"compose"}) {
		t.Errorf("bare BaseArgs = %v", got)
	}
}

func TestInterpreterAutoExecWhenRunning(t *testing.T) {
	bin, dir := fakeDocker(t, psRunning)
	p := load(t, "multi", LoadOptions{})
	c := NewInterpreter(p, "php")
	c.Docker = bin
	ctx := context.Background()

	v, err := c.Version(ctx)
	if err != nil || v != "8.3.12" {
		t.Fatalf("Version = %q %v", v, err)
	}
	if !strings.HasPrefix(c.Label(), "PHP 8.3.12 ") {
		t.Errorf("Label = %q", c.Label())
	}
	mods, err := c.Modules(ctx)
	if err != nil || !reflect.DeepEqual(mods, []string{"Core", "xdebug"}) {
		t.Errorf("Modules = %v %v", mods, err)
	}
	ini, err := c.Ini(ctx)
	if err != nil || ini["xdebug.mode"] != "debug" || ini["xdebug.client_port"] != "9003" {
		t.Errorf("Ini = %v %v", ini, err)
	}

	log := readLog(t, dir)
	// Each query: one ps + one exec.
	if len(log) != 6 {
		t.Fatalf("log = %v", log)
	}
	if !strings.Contains(log[0], " ps --all --format json") || !strings.Contains(log[1], " exec -T php php -r echo PHP_VERSION;") {
		t.Errorf("log = %v", log)
	}
}

func TestInterpreterAutoRunWhenStopped(t *testing.T) {
	bin, dir := fakeDocker(t, `[{"Name":"multi-php-1","Service":"php","State":"exited"}]`)
	p := load(t, "multi", LoadOptions{})
	c := NewInterpreter(p, "php")
	c.Docker = bin
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := readLog(t, dir)
	if len(log) != 2 || !strings.Contains(log[1], " run --rm -T --no-deps php php -r") {
		t.Errorf("log = %v", log)
	}
}

func TestInterpreterFixedModes(t *testing.T) {
	bin, dir := fakeDocker(t, "")
	p := load(t, "multi", LoadOptions{})
	c := NewInterpreter(p, "php")
	c.Docker = bin

	c.Mode = ExecAlways
	cmd, err := c.Command(context.Background(), "-m")
	if err != nil || cmd.Dir != p.Dir || cmd.Args[len(cmd.Args)-4] != "-T" {
		t.Fatalf("exec cmd = %v %v", cmd, err)
	}
	c.Mode = RunAlways
	if _, err := interp.Run(context.Background(), c, "-m"); err != nil {
		t.Fatal(err)
	}
	log := readLog(t, dir)
	if len(log) != 1 || !strings.Contains(log[0], " run --rm ") {
		t.Errorf("log = %v (no ps expected for fixed modes)", log)
	}
}

func TestInterpreterStatusErrors(t *testing.T) {
	bin, dir := fakeDocker(t, "not json")
	p := load(t, "multi", LoadOptions{})
	c := NewInterpreter(p, "php")
	c.Docker = bin
	ctx := context.Background()
	if _, err := c.Running(ctx); err == nil {
		t.Error("want parse error")
	}
	if err := os.WriteFile(filepath.Join(dir, "ps.fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(ctx); err == nil || !strings.Contains(err.Error(), "daemon down") {
		t.Errorf("Status err = %v", err)
	}
	if _, err := c.Command(ctx, "-v"); err == nil {
		t.Error("Command must fail when status is unknown")
	}
	if _, err := c.Version(ctx); err == nil {
		t.Error("Version must fail")
	}
}

func TestParsePS(t *testing.T) {
	ndjson, err := ParsePS([]byte(psRunning + "\n\n"))
	if err != nil || len(ndjson) != 2 || ndjson[0].Service != "php" || ndjson[1].State != "exited" {
		t.Errorf("ndjson = %+v %v", ndjson, err)
	}
	arr, err := ParsePS([]byte(` [{"Service":"php","State":"running","Health":"healthy"}]`))
	if err != nil || len(arr) != 1 || arr[0].Health != "healthy" {
		t.Errorf("array = %+v %v", arr, err)
	}
	if got, err := ParsePS([]byte("  \n")); err != nil || got != nil {
		t.Errorf("empty = %v %v", got, err)
	}
	if _, err := ParsePS([]byte("[{")); err == nil {
		t.Error("broken array must fail")
	}
	if _, err := ParsePS([]byte("{}\n{")); err == nil {
		t.Error("broken line must fail")
	}
}

// TestDockerIntegration talks to a real Docker daemon when one is available.
// It only runs `docker compose ps`, which needs no images and no network.
func TestDockerIntegration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
	dir := t.TempDir()
	write(t, dir, "compose.yaml", "services:\n  php:\n    image: php:8.3-cli\n    volumes:\n      - ./:/app\n")
	p, err := Load(ctx, dir, LoadOptions{Name: "tiegel-test-" + strings.ToLower(filepath.Base(dir))})
	if err != nil {
		t.Fatal(err)
	}
	c := NewInterpreter(p, "php")
	running, err := c.Running(ctx)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if running {
		t.Fatal("fresh project cannot be running")
	}
	cmd, err := c.Command(ctx, "-v")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), " run --rm ") {
		t.Errorf("stopped service must use run: %v", cmd.Args)
	}
}
