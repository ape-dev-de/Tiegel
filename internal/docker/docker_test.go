package docker_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

func TestMain(m *testing.M) {
	dockertest.Main()
	os.Exit(m.Run())
}

func client(f *dockertest.Fake) docker.Client {
	return docker.Client{Binary: f.Binary, Env: f.Env}
}

func TestClientArgsAndEnv(t *testing.T) {
	c := docker.Client{}
	if c.Bin() != "docker" || !reflect.DeepEqual(c.Args("ps"), []string{"ps"}) {
		t.Fatalf("zero client %q %v", c.Bin(), c.Args("ps"))
	}
	c = docker.Client{Binary: "/x/docker", Context: "orbstack", Env: []string{"A=1"}}
	if got := c.Args("compose", "ps"); !reflect.DeepEqual(got, []string{"--context", "orbstack", "compose", "ps"}) {
		t.Fatalf("args %v", got)
	}
	cmd := c.Command(context.Background(), "ps")
	if cmd.Args[0] != "/x/docker" || cmd.Env[len(cmd.Env)-1] != "A=1" {
		t.Fatalf("cmd %v env tail %v", cmd.Args, cmd.Env[len(cmd.Env)-1])
	}
	if c.WithContext("").Context != "" || c.Context != "orbstack" {
		t.Fatal("WithContext must copy")
	}
	merged := docker.MergeEnv([]string{"PATH=/a", "HOME=/h"}, []string{"PATH=/b"})
	if !reflect.DeepEqual(merged, []string{"HOME=/h", "PATH=/b"}) {
		t.Fatalf("merge %v", merged)
	}
}

func TestContexts(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	c := client(f).WithContext("ignored-for-ls")
	list, err := c.Contexts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("contexts %+v", list)
	}
	cur, ok := docker.CurrentContext(list)
	if !ok || cur.Name != "orbstack" || cur.Provider != docker.ProviderOrbStack {
		t.Fatalf("current %+v", cur)
	}
	if dd, _ := docker.FindContext(list, "desktop-linux"); dd.Provider != docker.ProviderDockerDesktop {
		t.Fatalf("desktop %+v", dd)
	}
	if def, _ := docker.FindContext(list, "default"); def.Provider != docker.ProviderEngine || def.Remote {
		t.Fatalf("default %+v", def)
	}
	if _, ok := docker.FindContext(list, "nope"); ok {
		t.Fatal("found unknown context")
	}
	if f.Called("--context", "context ls") {
		t.Fatal("context ls must not pass --context")
	}
	f.Write(t, "contexts.json", "garbage")
	if _, err := c.Contexts(context.Background()); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestParseContextsArray(t *testing.T) {
	list, err := docker.ParseContexts([]byte(`[{"Name":"colima","DockerEndpoint":"unix:///Users/me/.colima/default/docker.sock","Current":true},{"Name":"prod","DockerEndpoint":"ssh://deploy@prod.example"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Provider != docker.ProviderColima || !list[1].Remote || list[1].Provider != docker.ProviderRemote {
		t.Fatalf("list %+v", list)
	}
	if l, err := docker.ParseContexts(nil); err != nil || len(l) != 0 {
		t.Fatalf("empty %v %v", l, err)
	}
	if _, err := docker.ParseContexts([]byte("[")); err == nil {
		t.Fatal("broken array accepted")
	}
}

func TestDetectProvider(t *testing.T) {
	cases := []struct {
		name, endpoint string
		want           docker.Provider
	}{
		{"orbstack", "unix:///Users/me/.orbstack/run/docker.sock", docker.ProviderOrbStack},
		{"desktop-linux", "unix:///Users/me/.docker/run/docker.sock", docker.ProviderDockerDesktop},
		{"default", "npipe:////./pipe/dockerDesktopLinuxEngine", docker.ProviderDockerDesktop},
		{"colima", "unix:///Users/me/.colima/default/docker.sock", docker.ProviderColima},
		{"rancher-desktop", "unix:///Users/me/.rd/docker.sock", docker.ProviderRancher},
		{"podman", "unix:///run/user/1000/podman/podman.sock", docker.ProviderPodman},
		{"default", "unix:///var/run/docker.sock", docker.ProviderEngine},
		{"remote", "tcp://10.0.0.5:2376", docker.ProviderRemote},
		{"local-tcp", "tcp://127.0.0.1:2375", docker.ProviderUnknown},
		{"x", "", docker.ProviderUnknown},
	}
	for _, c := range cases {
		if got := docker.DetectProvider(c.name, c.endpoint); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.name, c.endpoint, got, c.want)
		}
	}
	if docker.IsRemote("unix:///x") || docker.IsRemote("tcp://localhost:2375") || !docker.IsRemote("ssh://me@[::2]") {
		t.Error("IsRemote")
	}
}

func TestPing(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	ctx := context.Background()
	v, err := client(f).WithContext("orbstack").Ping(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.Client != "27.3.1" || v.Server != "27.3.1" || v.OS != "linux" || docker.ProviderFromVersion(v, "") != docker.ProviderOrbStack {
		t.Fatalf("version %+v", v)
	}
	f.Write(t, "down", "desktop-linux\n")
	v, err = client(f).WithContext("desktop-linux").Ping(ctx, time.Second*5)
	if err == nil || !strings.Contains(err.Error(), "Cannot connect") || v.Client != "27.3.1" || v.Server != "" {
		t.Fatalf("down: %+v %v", v, err)
	}
	f.Write(t, "slow", "")
	start := time.Now()
	if _, err := client(f).Ping(ctx, 200*time.Millisecond); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("timeout not applied")
	}
	f.Remove("slow")
	f.Write(t, "version.json", `{"Client":{"Version":"1"},"Server":null}`)
	if _, err := client(f).Ping(ctx, 0); err == nil {
		t.Fatal("missing server accepted")
	}
}

func TestParseVersion(t *testing.T) {
	v, err := docker.ParseVersion([]byte("WARNING: noise\n" + `{"Client":{"Version":"26.1.0"},"Server":{"Platform":{"Name":"Docker Desktop 4.34.2 (167172)"},"Version":"27.2.0","KernelVersion":"6.10.4-linuxkit"}}`))
	if err != nil || v.Platform == "" || docker.ProviderFromVersion(v, "") != docker.ProviderDockerDesktop {
		t.Fatalf("%+v %v", v, err)
	}
	if p := docker.ProviderFromVersion(docker.VersionInfo{Platform: "Podman Engine"}, ""); p != docker.ProviderPodman {
		t.Fatal(p)
	}
	if p := docker.ProviderFromVersion(docker.VersionInfo{}, docker.ProviderColima); p != docker.ProviderColima {
		t.Fatal(p)
	}
	if _, err := docker.ParseVersion([]byte("nope")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name = "docker.exe"
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()

	b, err := docker.Find(ctx, docker.FindOptions{Configured: bin})
	if err != nil || b.Path != bin || b.Source != docker.SourceSetting {
		t.Fatalf("configured %+v %v", b, err)
	}
	if _, err := docker.Find(ctx, docker.FindOptions{Configured: filepath.Join(empty, name)}); !errors.Is(err, docker.ErrNotFound) {
		t.Fatalf("missing configured: %v", err)
	}
	b, err = docker.Find(ctx, docker.FindOptions{ProcessPath: dir})
	if err != nil || b.Source != docker.SourcePath {
		t.Fatalf("path %+v %v", b, err)
	}
	b, err = docker.Find(ctx, docker.FindOptions{Configured: "docker", ProcessPath: dir})
	if err != nil || b.Source != docker.SourceSetting {
		t.Fatalf("configured name %+v %v", b, err)
	}
	asked := false
	login := func(context.Context) (string, error) { asked = true; return dir, nil }
	b, err = docker.Find(ctx, docker.FindOptions{ProcessPath: empty, LoginPath: login, CommonDirs: []string{}})
	if err != nil || b.Source != docker.SourceLoginShell || !asked {
		t.Fatalf("login %+v %v", b, err)
	}
	b, err = docker.Find(ctx, docker.FindOptions{ProcessPath: empty, LoginPath: func(context.Context) (string, error) { return "", errors.New("no shell") }, CommonDirs: []string{dir}})
	if err != nil || b.Source != docker.SourceCommon {
		t.Fatalf("common %+v %v", b, err)
	}
	if _, err := docker.Find(ctx, docker.FindOptions{ProcessPath: empty, CommonDirs: []string{empty}}); !errors.Is(err, docker.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	env := docker.EnvPath(bin, "/usr/bin", dir)
	k, v, _ := strings.Cut(env, "=")
	if !strings.EqualFold(k, "PATH") || !strings.HasPrefix(v, dir) || strings.Count(v, dir) != 1 {
		t.Fatalf("EnvPath %q", env)
	}
}

func TestCommonDirs(t *testing.T) {
	mac := strings.Join(docker.CommonDirs("darwin", "/Users/me"), " ")
	for _, want := range []string{"/opt/homebrew/bin", filepath.Join("/Users/me", ".orbstack", "bin"), "/Applications/Docker.app/Contents/Resources/bin"} {
		if !strings.Contains(mac, want) {
			t.Errorf("darwin dirs lack %s: %s", want, mac)
		}
	}
	if l := docker.CommonDirs("linux", ""); len(l) == 0 || l[0] != "/usr/bin" {
		t.Errorf("linux %v", l)
	}
	if w := strings.Join(docker.CommonDirs("windows", `C:\Users\me`), " "); !strings.Contains(w, "Docker") {
		t.Errorf("windows %s", w)
	}
}

func TestImageSpec(t *testing.T) {
	for spec, want := range map[string][2]string{
		"php:8.3-cli":               {"php:8.3-cli", "/app"},
		" php:8.3-cli = /var/www/ ": {"php:8.3-cli", "/var/www"},
		"ghcr.io/x/php@sha256:abc":  {"ghcr.io/x/php@sha256:abc", "/app"},
	} {
		image, remote, err := docker.ParseImageSpec(spec)
		if err != nil || image != want[0] || remote != want[1] {
			t.Errorf("%q: %q %q %v", spec, image, remote, err)
		}
	}
	for _, bad := range []string{"", "=/app", "php x", "php=relative"} {
		if _, _, err := docker.ParseImageSpec(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if docker.FormatImageSpec("php", "/app") != "php" || docker.FormatImageSpec("php", "/srv") != "php=/srv" {
		t.Error("FormatImageSpec")
	}
}

func TestImageInterpreter(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	ctx := context.Background()
	host := t.TempDir()
	d := docker.NewImageInterpreter("php:8.3-cli", "", host, client(f).WithContext("orbstack"))
	if d.ID() != "docker:php:8.3-cli" || d.Kind() != interp.KindDocker || d.RemoteDir() != "/app" || !strings.Contains(d.Label(), "docker php:8.3-cli") {
		t.Fatalf("meta %s %s %s", d.ID(), d.Kind(), d.Label())
	}
	if m := d.Mappings(); len(m) != 1 || m[0].Host != host || m[0].Remote != "/app" {
		t.Fatalf("mappings %+v", m)
	}
	want := []string{"run", "--rm", "-i", "-v", host + ":/app", "-w", "/app", "--entrypoint", "php", "php:8.3-cli", "-v"}
	if got := d.RunArgs("-v"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args %v", got)
	}
	v, err := d.Version(ctx)
	if err != nil || v != "8.3.1" || !strings.HasPrefix(d.Label(), "PHP 8.3.1") {
		t.Fatalf("version %q %v", v, err)
	}
	if !f.Called("--context orbstack run --rm -i -v") {
		t.Fatalf("calls %v", f.Calls())
	}
	if mods, err := d.Modules(ctx); err != nil || len(mods) != 3 {
		t.Fatalf("modules %v %v", mods, err)
	}
	if ini, err := d.Ini(ctx); err != nil || ini["xdebug.mode"] != "debug" {
		t.Fatalf("ini %v %v", ini, err)
	}
	info, err := interp.Probe(ctx, d)
	if err != nil || info.Xdebug != "3.3.2" {
		t.Fatalf("probe %+v %v", info, err)
	}
	cmd, _ := d.Command(ctx, "-v")
	if cmd.Dir != host {
		t.Fatalf("dir %q", cmd.Dir)
	}

	// Without pull: the image must exist locally.
	np := d.WithoutPull()
	if d.NoPull || !np.NoPull || !strings.Contains(strings.Join(np.RunArgs(), " "), "--pull never") {
		t.Fatal("WithoutPull")
	}
	if _, err := np.Version(ctx); err == nil || !strings.Contains(err.Error(), "Unable to find image") {
		t.Fatalf("no-pull run: %v", err)
	}
	if ok, err := np.Client.ImageExists(ctx, "php:8.3-cli"); ok || err != nil {
		t.Fatalf("exists %v %v", ok, err)
	}
	f.Write(t, "images", "php:8.3-cli\n")
	if ok, err := np.Client.ImageExists(ctx, "php:8.3-cli"); !ok || err != nil {
		t.Fatalf("exists %v %v", ok, err)
	}
	if _, err := np.Version(ctx); err != nil {
		t.Fatal(err)
	}
	bare := &docker.ImageInterpreter{Image: "php", PHP: "php8"}
	if got := bare.RunArgs(); !reflect.DeepEqual(got, []string{"run", "--rm", "-i", "--entrypoint", "php8", "php"}) || bare.Mappings() != nil {
		t.Fatalf("bare %v", got)
	}
}

func TestOutputErrors(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	if _, err := client(f).Output(context.Background(), "bogus"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("stderr not in error: %v", err)
	}
	missing := docker.Client{Binary: filepath.Join(t.TempDir(), "docker")}
	if _, err := missing.Output(context.Background(), "ps"); err == nil {
		t.Fatal("missing binary")
	}
}

// TestRealDocker talks to the docker CLI on PATH (OrbStack, Docker Desktop,
// …). It only lists contexts and pings the current one; skipped without
// docker or in -short mode.
func TestRealDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	bin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not installed")
	}
	c := docker.Client{Binary: bin}
	ctx := context.Background()
	list, err := c.Contexts(ctx)
	if err != nil {
		t.Skipf("docker context ls: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("no contexts")
	}
	v, err := c.Ping(ctx, 10*time.Second)
	if err != nil {
		t.Skipf("engine not reachable: %v", err)
	}
	if v.Client == "" || v.Server == "" {
		t.Fatalf("version %+v", v)
	}
	cur, _ := docker.CurrentContext(list)
	t.Logf("context %s (%s), engine %s, provider %s", cur.Name, cur.Endpoint, v.Server, docker.ProviderFromVersion(v, cur.Provider))
}
