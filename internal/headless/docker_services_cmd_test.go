package headless

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
)

func TestDockerServiceCommands(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"ID":"1","Name":"shop-web-1","Service":"web","State":"running","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"ID":"2","Name":"shop-app-1","Service":"app","State":"exited","Status":"Exited (0)"}
`)
	f.Write(t, "containers.json", `{"ID":"9","Names":"adminer","State":"running","Status":"Up","Ports":"9000/tcp"}`+"\n")
	f.Write(t, "logs.txt", "web-1  | GET / 200\napp-1  | PHP Warning\n")
	dir := project(t, map[string]string{
		"compose.yaml": "name: shop\nservices:\n  web:\n    image: nginx\n  app:\n    image: php:8.3-fpm\n",
	})
	var mu sync.Mutex
	var launched [][]string
	// run runs the CLI; with until it cancels once the output contains it.
	run := func(until string, args ...string) (int, string, string) {
		var out, errb syncBuffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if until != "" {
			go func() {
				for ctx.Err() == nil && !strings.Contains(out.String(), until) {
					time.Sleep(20 * time.Millisecond)
				}
				cancel()
			}()
		}
		env := testEnv(t, strings.NewReader(""), &out, &errb)
		env.Modules.Docker = dockermod.Options{Binary: f.Binary, Env: f.Env, NoLoginShell: true, GOOS: "linux",
			LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
			Launch: func(argv, _ []string) error {
				mu.Lock()
				launched = append(launched, argv)
				mu.Unlock()
				return nil
			}}
		code := Run(ctx, append(args, "--project", dir, "--config-dir", t.TempDir()), env)
		return code, out.String(), errb.String()
	}
	cli := func(args ...string) (int, string, string) { return run("", args...) }

	code, out, errOut := cli("docker", "ps", "--all")
	if code != 0 || !strings.Contains(out, "project shop (compose.yaml)") || !strings.Contains(out, "web\trunning (healthy)\thttp://localhost:8080") ||
		!strings.Contains(out, "app\tstopped") || !strings.Contains(out, "shop-app-1\texited\tExited (0)") ||
		!strings.Contains(out, "other containers") || !strings.Contains(out, "adminer\trunning\tUp\t9000/tcp") {
		t.Fatalf("ps %d %q %q", code, out, errOut)
	}
	if code, out, _ = cli("docker", "ps", "--json"); code != 0 || !strings.Contains(out, `"projects"`) {
		t.Fatalf("ps json %d %q", code, out)
	}
	if code, _, _ = cli("docker", "ps", "x"); code != ExitUsage {
		t.Fatal("ps with arg")
	}

	if code, out, _ = cli("docker", "restart", "web"); code != 0 || !strings.Contains(out, "service web: restart done") || !f.Called("restart web") {
		t.Fatalf("restart %d %q", code, out)
	}
	if code, out, _ = cli("docker", "up"); code != 0 || !strings.Contains(out, "project shop: up done") {
		t.Fatalf("up %d %q", code, out)
	}
	if code, out, _ = cli("docker", "stop", "adminer", "--container"); code != 0 || !strings.Contains(out, "container adminer: stop done") {
		t.Fatalf("stop container %d %q", code, out)
	}
	if code, _, _ = cli("docker", "down"); code != ExitUsage || f.Called("compose.yaml down") {
		t.Fatal("down without --yes")
	}
	if code, _, _ = cli("docker", "down", "web", "--yes"); code != ExitUsage {
		t.Fatal("down with service")
	}
	if code, out, _ = cli("docker", "down", "--yes"); code != 0 || !f.Called("compose.yaml down") {
		t.Fatalf("down %d %q", code, out)
	}
	f.Write(t, "fail", "start\n")
	if code, _, _ = cli("docker", "start", "app"); code != ExitError {
		t.Fatal("failing start")
	}

	if code, out, _ = cli("docker", "logs", "app", "--tail", "5", "--filter", "php"); code != 0 || out != "app-1 | PHP Warning\n" || !f.Called("logs --no-color --tail 5 app") {
		t.Fatalf("logs %d %q %v", code, out, f.Calls())
	}
	if code, _, _ = cli("docker", "logs", "--tail", "all", "--timestamps"); code != 0 || !f.Called("logs --no-color --timestamps") {
		t.Fatal("logs all")
	}
	if code, _, _ = cli("docker", "logs", "--tail", "x"); code != ExitUsage {
		t.Fatal("bad tail")
	}
	if code, _, _ = cli("docker", "logs", "--container"); code != ExitUsage {
		t.Fatal("container logs without name")
	}
	if code, out, _ = cli("docker", "logs", "adminer", "--container", "--tail", "0"); code != 0 || !strings.Contains(out, "GET / 200") {
		t.Fatalf("container logs %d %q", code, out)
	}
	// Follow until interrupted.
	f.Emit(t, "logs-next", "web-1  | later\n")
	code, out, _ = run("web-1 | later", "docker", "logs", "-f")
	if code != 0 || !strings.Contains(out, "web-1 | GET / 200") || !strings.Contains(out, "web-1 | later") {
		t.Fatalf("follow %d %q", code, out)
	}

	if code, out, _ = cli("docker", "shell", "app", "--shell", "bash"); code != 0 || !strings.Contains(out, "compose") || !strings.Contains(out, "exec app bash") ||
		!strings.Contains(out, "opened with: x-terminal-emulator -e") {
		t.Fatalf("shell %d %q", code, out)
	}
	if code, out, _ = cli("docker", "shell", "adminer", "--container"); code != 0 || !strings.Contains(out, "exec -it adminer sh") {
		t.Fatalf("container shell %d %q", code, out)
	}
	if code, _, _ = cli("docker", "shell"); code != ExitUsage {
		t.Fatal("shell without service")
	}
	if code, out, _ = cli("docker", "open", "http://localhost:8080"); code != 0 || out != "xdg-open http://localhost:8080\n" {
		t.Fatalf("open %d %q", code, out)
	}
	if code, _, _ = cli("docker", "open"); code != ExitUsage {
		t.Fatal("open without url")
	}
	if code, _, _ = cli("docker", "open", "file:///etc/passwd"); code == 0 {
		t.Fatal("file url opened")
	}
	mu.Lock()
	n := len(launched)
	mu.Unlock()
	if n != 3 {
		t.Fatalf("launched %v", launched)
	}

	// watch prints snapshots until the context ends.
	code, out, _ = run("project shop", "docker", "watch")
	if code != 0 || !strings.Contains(out, "--- (events)") || !strings.Contains(out, "project shop") {
		t.Fatalf("watch %d %q", code, out)
	}

	// Engine errors make ps exit 1.
	f.Write(t, "ps.json", "{broken")
	if code, out, _ = cli("docker", "ps"); code != ExitError || !strings.Contains(out, "error:") {
		t.Fatalf("ps error %d %q", code, out)
	}
}
