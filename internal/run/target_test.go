package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// fakeBin creates an executable named name in a new directory.
func fakeBin(t *testing.T, name string) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	file = filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func TestSpecBasics(t *testing.T) {
	if err := (Spec{}).Validate(); err == nil {
		t.Fatal("empty spec valid")
	}
	if err := (Spec{Program: "x", Env: []string{"NOEQ"}}).Validate(); err == nil {
		t.Fatal("bad env valid")
	}
	if (Spec{PHP: true}).EffectiveInterpreter() != InterpreterProject || (Spec{Program: "x"}).EffectiveInterpreter() != InterpreterHost ||
		(Spec{Program: "x", Interpreter: "local:/php"}).EffectiveInterpreter() != "local:/php" {
		t.Fatal("effective interpreter")
	}
	if d := (Spec{PHP: true, Args: []string{"a b.php"}}).Display(); d != "php 'a b.php'" {
		t.Fatal(d)
	}
	if _, err := Prepare(context.Background(), Spec{}, Target{}); err == nil {
		t.Fatal("prepare invalid spec")
	}
}

func TestPrepareHostAndLocal(t *testing.T) {
	bin, composer := fakeBin(t, "composer")
	root := t.TempDir()
	tgt := Target{ProjectDir: root, PathEnv: bin}
	l, err := Prepare(context.Background(), Spec{Program: "composer", Args: []string{"install"}, Dir: "sub", Env: []string{"FOO=1"}}, tgt)
	if err != nil {
		t.Fatal(err)
	}
	if l.Cmd.Path != composer || l.Cmd.Dir != filepath.Join(root, "sub") || l.Display != "composer install" || l.Where != InterpreterHost {
		t.Fatalf("host %+v %s %s", l, l.Cmd.Path, l.Cmd.Dir)
	}
	if envValue(l.Cmd.Env, "FOO") != "1" || envValue(l.Cmd.Env, "PATH") != bin {
		t.Fatalf("env %v", l.Cmd.Env)
	}
	if _, err := Prepare(context.Background(), Spec{Program: "nope-not-there"}, tgt); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing program %v", err)
	}
	// Host PHP is "php" from PATH.
	if _, err := Prepare(context.Background(), Spec{PHP: true}, tgt); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("host php %v", err)
	}

	// A local interpreter runs its php; programs see its directory first
	// in PATH.
	phpDir, php := fakeBin(t, "php")
	if r, err := filepath.EvalSymlinks(phpDir); err == nil {
		phpDir = r
	}
	loc := interp.NewLocal(php)
	l, err = Prepare(context.Background(), Spec{PHP: true, Args: []string{"x.php"}}, Target{Interp: loc, ProjectDir: root, PathEnv: bin})
	if err != nil || l.Cmd.Path != loc.Path || !slices.Equal(l.Cmd.Args[1:], []string{"x.php"}) || l.Cmd.Dir != root {
		t.Fatalf("local php %+v %v", l, err)
	}
	l, err = Prepare(context.Background(), Spec{Program: "composer"}, Target{Interp: loc, ProjectDir: root, PathEnv: bin})
	if err != nil || l.Cmd.Path != composer || !strings.HasPrefix(envValue(l.Cmd.Env, "PATH"), phpDir+string(os.PathListSeparator)) {
		t.Fatalf("local program %+v %v", l, err)
	}
}

func TestPrepareCompose(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	ci := &compose.Interpreter{Project: "demo", Dir: root, Files: []string{"compose.yaml"}, Service: "php", Mode: compose.ExecAlways,
		Docker: "/usr/bin/docker", Context: "orbstack", Mappings: compose.Mappings{}.Set(root, "/var/www")}
	spec := Spec{PHP: true, Args: []string{filepath.Join(root, "bin", "x.php"), "-v"}, Dir: filepath.Join(root, "bin"), Env: []string{"APP_ENV=test"}}
	l, err := Prepare(context.Background(), spec, Target{Interp: ci, ProjectDir: root})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/docker", "--context", "orbstack", "compose", "--project-directory", root, "-p", "demo", "-f", "compose.yaml",
		"exec", "-T", "-e", "APP_ENV=test", "-w", "/var/www/bin", "php", "sh", "-c", `echo "` + pidMarker + `$$" >&2; exec "$@"`, "sh", "php", "/var/www/bin/x.php", "-v"}
	if !slices.Equal(l.Cmd.Args, want) {
		t.Fatalf("exec args\n got %q\nwant %q", l.Cmd.Args, want)
	}
	if l.PIDMarker != pidMarker || l.StopRemote == nil || l.Cmd.Dir != root || len(l.Mappings) != 1 {
		t.Fatalf("exec launch %+v", l)
	}
	if err := l.StopRemote(context.Background(), ""); err != nil {
		t.Fatalf("stop without pid: %v", err)
	}

	// Plain (installation plans): no stop helper.
	l, err = Prepare(context.Background(), Spec{Program: "composer", Args: []string{"require", "x"}, Dir: root}, Target{Interp: ci, ProjectDir: root, Plain: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Cmd.Args[len(l.Cmd.Args)-6:]; !slices.Equal(got, []string{"-w", "/var/www", "php", "composer", "require", "x"}) || l.PIDMarker != "" {
		t.Fatalf("plain %q", l.Cmd.Args)
	}

	// run --rm with a named container and --no-deps.
	run := *ci
	run.Mode = compose.RunAlways
	l, err = Prepare(context.Background(), Spec{Program: "vendor/bin/phpunit"}, Target{Interp: &run, ProjectDir: root})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(l.Cmd.Args, " ")
	if !strings.Contains(args, " run --rm -T --name tiegel-run-") || !strings.Contains(args, "--no-deps -w /var/www php vendor/bin/phpunit") || l.StopRemote == nil {
		t.Fatalf("run args %s", args)
	}
}

func TestPrepareImageAndWSL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	d := docker.NewImageInterpreter("php:8.3-cli", "/app", root, docker.Client{Binary: "docker"}).WithoutPull()
	l, err := Prepare(context.Background(), Spec{Program: "composer", Args: []string{"test"}, Env: []string{"A=1"}}, Target{Interp: d, ProjectDir: root})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(l.Cmd.Args, " ")
	if !strings.HasPrefix(args, "docker run --rm -i --name tiegel-run-") ||
		!strings.HasSuffix(args, "--pull never -v "+root+":/app -w /app -e A=1 --entrypoint composer php:8.3-cli test") || l.StopRemote == nil {
		t.Fatalf("image args %s", args)
	}
	l, err = Prepare(context.Background(), Spec{PHP: true, Args: []string{"-v"}}, Target{Interp: d, ProjectDir: root})
	if err != nil || !strings.Contains(strings.Join(l.Cmd.Args, " "), "--entrypoint php php:8.3-cli -v") {
		t.Fatalf("image php %v %v", l.Cmd.Args, err)
	}

	w := &interp.WSL{Distro: "Ubuntu", Path: "/usr/bin/php", Program: "wsl.exe"}
	l, err = Prepare(context.Background(), Spec{PHP: true, Args: []string{"x.php"}, Env: []string{"A=1"}}, Target{Interp: w, ProjectDir: `C:\proj`})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(l.Cmd.Args, " ")
	if !strings.HasPrefix(got, "wsl.exe -d Ubuntu ") || !strings.HasSuffix(got, " -- env A=1 /usr/bin/php x.php") ||
		runtime.GOOS == "windows" && !strings.Contains(got, " --cd /mnt/c/proj ") {
		t.Fatalf("wsl %q", l.Cmd.Args)
	}
	if _, err := Prepare(context.Background(), Spec{PHP: true}, Target{Interp: &interp.WSL{}}); err == nil {
		t.Fatal("wsl without distro")
	}
}

// phpOnly is an interpreter of an unknown kind.
type phpOnly struct{ php string }

func (phpOnly) ID() string                                { return "other:x" }
func (phpOnly) Label() string                             { return "Other" }
func (phpOnly) Kind() interp.Kind                         { return "other" }
func (phpOnly) Version(context.Context) (string, error)   { return "", nil }
func (phpOnly) Modules(context.Context) ([]string, error) { return nil, nil }
func (phpOnly) Ini(context.Context) (interp.IniValues, error) {
	return nil, nil
}
func (p phpOnly) Command(_ context.Context, args ...string) (*exec.Cmd, error) {
	if p.php == "" {
		return nil, errors.New("broken")
	}
	return exec.Command(p.php, args...), nil
}

func TestPrepareOtherInterpreter(t *testing.T) {
	root := t.TempDir()
	l, err := Prepare(context.Background(), Spec{PHP: true, Args: []string{"a"}, Env: []string{"X=1"}}, Target{Interp: phpOnly{php: "php7"}, ProjectDir: root})
	if err != nil || l.Cmd.Dir != root || envValue(l.Cmd.Env, "X") != "1" || l.Where != "Other" {
		t.Fatalf("other %+v %v", l, err)
	}
	if _, err := Prepare(context.Background(), Spec{Program: "composer"}, Target{Interp: phpOnly{php: "php7"}}); err == nil {
		t.Fatal("program on php-only interpreter")
	}
	if _, err := Prepare(context.Background(), Spec{PHP: true}, Target{Interp: phpOnly{}}); err == nil {
		t.Fatal("broken interpreter")
	}
}

func TestHelpers(t *testing.T) {
	m := compose.Mappings{}.Set(filepath.Join(string(filepath.Separator)+"h", "p"), "/app")
	in := []string{"-v", filepath.Join(string(filepath.Separator)+"h", "p", "x.php"), "rel.php"}
	if runtime.GOOS != "windows" {
		if got := remoteArgs(in, m); !slices.Equal(got, []string{"-v", "/app/x.php", "rel.php"}) {
			t.Fatalf("remote args %q", got)
		}
	}
	if remoteDir("", m) != "" || remoteDir(t.TempDir(), m) != "" {
		t.Fatal("remote dir of unmapped")
	}
	if a, b := containerName(), containerName(); a == b || !strings.HasPrefix(a, "tiegel-run-") {
		t.Fatal("container names")
	}
	if got := mergeEnv([]string{"A=1"}, nil); !slices.Equal(got, []string{"A=1"}) {
		t.Fatal("merge nil")
	}
}
