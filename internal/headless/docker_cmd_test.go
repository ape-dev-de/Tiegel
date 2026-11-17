package headless

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
)

// runWithFakeDocker runs the CLI with the fake docker.
func runWithFakeDocker(t *testing.T, f *dockertest.Fake, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := testEnv(t, strings.NewReader(""), &out, &errb)
	env.Modules.Docker = dockermod.Options{Binary: f.Binary, Env: f.Env, NoLoginShell: true}
	code := Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func TestDockerCommands(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	dir := project(t, map[string]string{
		"compose.yaml":  "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
		"composer.json": `{"require":{"php":"^8.1"}}`,
	})
	cfg := t.TempDir()
	at := []string{"--project", dir, "--config-dir", cfg}
	cli := func(args ...string) (int, string, string) { return runWithFakeDocker(t, f, append(args, at...)...) }

	code, out, errOut := cli("docker")
	if code != 0 || !strings.Contains(out, "* orbstack\torbstack") || !strings.Contains(out, "desktop-linux\tdocker-desktop") || !strings.Contains(out, "[docker]") {
		t.Fatalf("contexts %d %q %q", code, out, errOut)
	}
	if code, out, _ = cli("docker", "use", "desktop-linux"); code != 0 || !strings.Contains(out, "context desktop-linux [project]") {
		t.Fatalf("use %d %q", code, out)
	}
	if code, out, _ = cli("docker", "contexts", "--json"); code != 0 || !strings.Contains(out, `"selected": "desktop-linux"`) {
		t.Fatalf("persisted %d %q", code, out)
	}
	if code, out, _ = cli("docker", "use", "orbstack", "--global"); code != 0 || !strings.Contains(out, "desktop-linux [project]") {
		t.Fatalf("global keeps project override %d %q", code, out)
	}
	if code, out, _ = cli("docker", "use", "--clear"); code != 0 || !strings.Contains(out, "orbstack [global]") {
		t.Fatalf("clear %d %q", code, out)
	}
	if code, _, _ = cli("docker", "use", "nope"); code != ExitError {
		t.Fatal("unknown context accepted")
	}
	if code, _, _ = cli("docker", "use"); code != ExitUsage {
		t.Fatal("use without name")
	}
	if code, _, _ = cli("docker", "contexts", "x"); code != ExitUsage {
		t.Fatal("contexts with arg")
	}
	if code, _, _ = cli("docker", "what"); code != ExitUsage {
		t.Fatal("unknown subcommand")
	}

	if code, out, _ = cli("docker", "status"); code != 0 || !strings.Contains(out, "reachable") || !strings.Contains(out, "server 27.3.1") {
		t.Fatalf("status %d %q", code, out)
	}
	f.Write(t, "down", "desktop-linux\n")
	if code, out, _ = cli("docker", "status", "desktop-linux"); code != ExitError || !strings.Contains(out, "unreachable") {
		t.Fatalf("down %d %q", code, out)
	}
	if code, out, _ = cli("docker", "path"); code != 0 || !strings.Contains(out, "\toption") {
		t.Fatalf("path %d %q", code, out)
	}
	if code, out, _ = cli("docker", "path", f.Binary); code != 0 || !strings.Contains(out, "\tsetting") {
		t.Fatalf("set path %d %q", code, out)
	}
	if code, out, _ = cli("docker", "path", "--clear"); code != 0 || !strings.Contains(out, "\toption") {
		t.Fatalf("clear path %d %q", code, out)
	}

	// Compose interpreter with context and state.
	f.Write(t, "ps.json", `{"Name":"p-app-1","Service":"app","State":"running","Status":"Up 1 minute"}`+"\n")
	code, out, _ = cli("interpreters", "--json")
	if code != 0 || !strings.Contains(out, `"context": "orbstack"`) {
		t.Fatalf("interpreters %d %q", code, out)
	}
	id := "compose:" + strings.ToLower(lastElem(dir)) + ":app"
	if code, out, _ = cli("interp-status", id); code != 0 || !strings.Contains(out, "service app running") || !strings.Contains(out, "context orbstack") {
		t.Fatalf("interp-status %d %q (id %s)", code, out, id)
	}
	if code, out, _ = cli("interp-probe", id); code != 0 || !strings.Contains(out, "xdebug 3.3.2") || !strings.Contains(out, "ini+ /usr/local/etc/php/conf.d") {
		t.Fatalf("probe %d %q", code, out)
	}
	if code, out, _ = cli("interp-probe", id); code != 0 || !strings.Contains(out, "(cached") {
		t.Fatalf("cached probe %d %q", code, out)
	}

	// Image interpreters.
	if code, out, _ = cli("interp-image"); code != 0 || !strings.Contains(out, "no image interpreters") {
		t.Fatalf("images %d %q", code, out)
	}
	if code, out, _ = cli("interp-image", "add", "php:8.3-cli", "/srv"); code != 0 || !strings.Contains(out, "docker:php:8.3-cli\tphp:8.3-cli\t/srv") {
		t.Fatalf("add %d %q", code, out)
	}
	if code, _, _ = cli("interpreters", "select", "docker:php:8.3-cli"); code != 0 {
		t.Fatal("select image")
	}
	if code, out, _ = cli("interp-status"); code != 0 || !strings.Contains(out, "present=false") {
		t.Fatalf("image status %d %q", code, out)
	}
	if code, _, _ = cli("interp-probe"); code != ExitError {
		t.Fatal("probe pulled automatically")
	}
	if code, out, _ = cli("interp-probe", "--start", "--refresh"); code != 0 || !strings.Contains(out, "php 8.3.1 cli Linux") {
		t.Fatalf("explicit probe %d %q", code, out)
	}
	if code, out, _ = cli("mappings"); code != 0 || !strings.Contains(out, "/srv\tdocker") {
		t.Fatalf("mappings %d %q", code, out)
	}
	if code, out, _ = cli("interp-image", "remove", "php:8.3-cli"); code != 0 || !strings.Contains(out, "no image interpreters") {
		t.Fatalf("remove %d %q", code, out)
	}
	if code, _, _ = cli("interp-image", "frobnicate"); code != ExitUsage {
		t.Fatal("bad interp-image usage")
	}
	if code, _, _ = cli("interp-image", "remove", "nope"); code != ExitError {
		t.Fatal("remove unknown")
	}
}

func lastElem(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	return p[strings.LastIndex(p, "/")+1:]
}
