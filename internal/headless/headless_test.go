package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/modules"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// run executes the CLI in-process with an isolated config dir.
func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return runCtx(t, context.Background(), stdin, args...)
}

func runCtx(t *testing.T, ctx context.Context, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(ctx, args, testEnv(t, strings.NewReader(stdin), &out, &errb))
	return code, out.String(), errb.String()
}

func testEnv(t *testing.T, in io.Reader, out, errw io.Writer) Env {
	home := t.TempDir()
	return Env{
		Stdin: in, Stdout: out, Stderr: errw,
		Getenv: func(string) string { return "" },
		Options: api.Options{
			ConfigDir:  filepath.Join(t.TempDir(), "cfg"),
			AppVersion: "test",
			Interp:     interp.DiscoverOptions{Dirs: []string{}},
			LSP:        servers.DetectOptions{HomeDir: home, NoSystemDirs: true},
			GOOS:       "darwin",
		},
		// Never ask the developer's login shell for docker's PATH.
		Modules: modules.Options{Docker: dockermod.Options{NoLoginShell: true}},
	}
}

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	p, err := parseArgs([]string{"--headless", "files", "--json", "--project=/x", "sub", "--depth", "2", "--", "--literal"})
	if err != nil {
		t.Fatal(err)
	}
	if p.cmd != "files" || strings.Join(p.args, ",") != "sub,--literal" || !p.has("json") || p.value("project") != "/x" || p.value("depth") != "2" {
		t.Fatalf("%+v", p)
	}
	for _, bad := range [][]string{{"--nope"}, {"files", "--depth"}, {"--json=1"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
	if n, err := (&parsed{flags: map[string][]string{"depth": {"x"}}}).intValue("depth"); err == nil || n != 0 {
		t.Fatal("intValue should fail")
	}
}

func TestHelpAndUsage(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, "")
	if code != ExitOK || !strings.Contains(out, "Commands:") || !strings.Contains(out, "serve") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "help", "files"); code != ExitOK || !strings.Contains(out, "--tree") {
		t.Fatalf("help files: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "files", "--help"); code != ExitOK {
		t.Fatal("--help")
	}
	if code, _, errs := run(t, "", "help", "nope"); code != ExitUsage || errs == "" {
		t.Fatal("help nope")
	}
	if code, _, errs := run(t, "", "frobnicate"); code != ExitUsage || !strings.Contains(errs, "unknown command") {
		t.Fatalf("unknown command: %d %q", code, errs)
	}
	if code, _, _ := run(t, "", "lang"); code != ExitUsage {
		t.Fatal("missing args")
	}
	if code, _, _ := run(t, "", "--bogus"); code != ExitUsage {
		t.Fatal("bad flag")
	}
	if code, out, _ := run(t, "", "--version"); code != ExitOK || !strings.Contains(out, "api") {
		t.Fatalf("version: %q", out)
	}
	if code, out, _ := run(t, "", "version", "--json"); code != ExitOK || !strings.Contains(out, `"apiVersion"`) {
		t.Fatalf("version json: %q", out)
	}
}

func TestInfoFilesLangRead(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		".gitignore": "*.log\n", "a.php": "<?php echo 1;\n", "x.log": "", "vendor/v.php": "", "src/B.php": "<?php",
	})
	code, out, errs := run(t, "", "info", "--json", "--project", dir)
	if code != ExitOK {
		t.Fatalf("info: %d %s", code, errs)
	}
	var info struct {
		App     api.AppInfo     `json:"app"`
		Project api.ProjectInfo `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil || !info.Project.Open || info.App.Version != "test" {
		t.Fatalf("info json %q %v", out, err)
	}
	if code, out, _ := run(t, "", "info", "--project", dir); code != ExitOK || !strings.Contains(out, "Project:") || !strings.Contains(out, "Git:") {
		t.Fatalf("info human %q", out)
	}
	if code, _, errs := run(t, "", "info", "--project", filepath.Join(dir, "missing")); code != ExitError || errs == "" {
		t.Fatal("missing project should fail")
	}

	_, out, _ = run(t, "", "files", "--project", dir)
	if out != "src/\nsrc/B.php\nvendor/  [library]\n.gitignore\na.php\n" {
		t.Fatalf("files %q", out)
	}
	_, out, _ = run(t, "", "files", "--project", dir, "--tree", "--ignored")
	for _, want := range []string{"vendor/  [library]", "  B.php", "x.log  [ignored]"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree missing %q:\n%s", want, out)
		}
	}
	if code, _, _ := run(t, "", "files", "--project", dir, "--depth", "x"); code != ExitUsage {
		t.Fatal("bad depth")
	}
	if code, out, _ := run(t, "", "files", "--project", dir, "--json"); code != ExitOK || !strings.Contains(out, `"tree"`) {
		t.Fatal("files json")
	}

	_, out, _ = run(t, "", "lang", filepath.Join(dir, "a.php"))
	if out != "php\n" {
		t.Fatalf("lang %q", out)
	}
	_, out, _ = run(t, "", "lang", filepath.Join(dir, "a.php"), "c.json")
	if !strings.Contains(out, "c.json\tjson") {
		t.Fatalf("lang multi %q", out)
	}
	if _, out, _ = run(t, "", "lang", "--json", "c.yml"); !strings.Contains(out, `"yaml"`) {
		t.Fatalf("lang json %q", out)
	}
	if _, out, _ = run(t, "", "read", filepath.Join(dir, "a.php")); out != "<?php echo 1;\n" {
		t.Fatalf("read %q", out)
	}
	if _, out, _ = run(t, "", "read", "--json", filepath.Join(dir, "a.php")); !strings.Contains(out, `"encoding": "utf-8"`) {
		t.Fatalf("read json %q", out)
	}
	if code, _, _ := run(t, "", "read", filepath.Join(dir, "nope")); code != ExitError {
		t.Fatal("read missing")
	}
}

func TestSearchCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n$foo = 1;\n"})
	f := filepath.Join(dir, "a.php")
	code, out, _ := run(t, "", "search", f, `\$\w+`, "--regex")
	if code != ExitOK || out != f+":2:1: $foo = 1;\n" {
		t.Fatalf("search %d %q", code, out)
	}
	if code, _, _ := run(t, "", "search", f, "nothing"); code != ExitError {
		t.Fatal("no match should exit 1")
	}
	if code, _, _ := run(t, "", "search", f, "x", "--limit", "z"); code != ExitUsage {
		t.Fatal("bad limit")
	}
	if code, _, _ := run(t, "", "search", f, "(", "--regex"); code != ExitUsage {
		t.Fatal("bad regex is a usage error")
	}
}

func TestCallCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	params, _ := json.Marshal(map[string]string{"path": filepath.Join(dir, "a.php")}) // escapes \ on Windows
	code, out, _ := run(t, "", "call", "files.detectLanguage", string(params))
	if code != ExitOK || !strings.Contains(out, `"language": "php"`) {
		t.Fatalf("call %d %q", code, out)
	}
	code, out, _ = run(t, `{"path":"x.json"}`, "call", "files.detectLanguage", "-")
	if code != ExitOK || !strings.Contains(out, `"json"`) {
		t.Fatalf("call stdin %d %q", code, out)
	}
	if code, _, _ := run(t, "", "call", "app.info"); code != ExitOK {
		t.Fatal("call without params")
	}
	if code, _, _ := run(t, "", "call", "nope.nope"); code != ExitUsage {
		t.Fatal("unknown method")
	}
	if code, _, _ := run(t, "", "call", "app.info", "{bad"); code != ExitUsage {
		t.Fatal("bad json")
	}
	if code, _, _ := run(t, "", "call", "files.list"); code != ExitError {
		t.Fatal("no project")
	}
	if code, out, _ := run(t, "", "methods"); code != ExitOK || !strings.Contains(out, "doc.insert") {
		t.Fatal("methods")
	}
	if code, out, _ := run(t, "", "methods", "--json"); code != ExitOK || !strings.Contains(out, `"name"`) {
		t.Fatal("methods json")
	}
}

func TestRuntimeCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"compose.yaml":  "services:\n  php:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
		"composer.json": `{"require":{"php":"^8.0"}}`,
		"e.php":         "<?php\nenum A { case B; }\n",
	})
	_, out, _ := run(t, "", "compose", "--project", dir)
	for _, want := range []string{"php (score", "map ", "/var/www/html"} {
		if !strings.Contains(out, want) {
			t.Errorf("compose missing %q:\n%s", want, out)
		}
	}
	if _, out, _ = run(t, "", "compose", "--json", "--project", dir); !strings.Contains(out, `"candidates"`) {
		t.Fatalf("compose json %q", out)
	}
	if _, out, _ = run(t, "", "compose", "--project", t.TempDir()); !strings.Contains(out, "no compose file") {
		t.Fatalf("no compose %q", out)
	}
	_, out, _ = run(t, "", "interpreters", "--project", dir)
	if !strings.Contains(out, "compose") {
		t.Fatalf("interpreters %q", out)
	}
	if _, out, _ = run(t, "", "interpreters", "--project", t.TempDir()); !strings.Contains(out, "no PHP interpreter") {
		t.Fatalf("no interpreters %q", out)
	}
	if code, _, _ := run(t, "", "interpreters", "select"); code != ExitUsage {
		t.Fatal("select without id")
	}
	if code, _, _ := run(t, "", "interpreters", "frob"); code != ExitUsage {
		t.Fatal("unknown sub command")
	}
	if code, _, _ := run(t, "", "interpreters", "select", "nope", "--project", dir); code != ExitError {
		t.Fatal("unknown interpreter")
	}
	if code, _, _ := run(t, "", "interpreters", "version", "--project", dir); code != ExitUsage {
		t.Fatal("version without selection")
	}
	if _, out, _ = run(t, "", "lsp-servers", "--project", dir); !strings.Contains(out, "no PHP language server") {
		t.Fatalf("lsp %q", out)
	}
	if _, out, _ = run(t, "", "modules"); !regexp.MustCompile(`debug-xdebug +running +php-runtime`).MatchString(out) || !regexp.MustCompile(`lsp-intelephense +off`).MatchString(out) {
		t.Fatalf("modules %q", out)
	}
	if code, _, _ := run(t, "", "modules", "enable"); code != ExitUsage {
		t.Fatal("modules usage")
	}
	if code, _, _ := run(t, "", "modules", "enable", "nope"); code != ExitError {
		t.Fatal("unknown module")
	}
	if _, out, _ = run(t, "", "php-version", "--project", dir); !strings.HasPrefix(out, "8.0.0") {
		t.Fatalf("php-version %q", out)
	}
	if syntax.TreeSitter {
		code, out, _ := run(t, "", "php-check", "e.php", "--project", dir)
		if code != ExitError || !strings.Contains(out, "e.php:2:") {
			t.Fatalf("php-check %d %q", code, out)
		}
		if code, _, _ := run(t, "", "php-check", "e.php", "--php", "8.1", "--project", dir); code != ExitOK {
			t.Fatal("php-check 8.1 should pass")
		}
	}
	if code, _, _ := run(t, "", "git-status", "--project", dir); code != ExitError {
		t.Fatal("git-status outside repo")
	}
}

func TestHistoryCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	env := testEnv(t, nil, io.Discard, io.Discard)
	// Create two revisions through the API using the same config dir.
	a, err := api.New(env.Options)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.OpenProject(ctx, dir); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"<?php // 1\n", "<?php // 2\n"} {
		for _, step := range []struct {
			m string
			p any
		}{
			{"doc.open", map[string]any{"path": "a.php"}},
			{"doc.replace", map[string]any{"path": "a.php", "text": text}},
			{"doc.save", map[string]any{"path": "a.php"}},
		} {
			if _, err := a.CallJSON(ctx, step.m, step.p); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = a.Close()

	hist := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := Run(ctx, append([]string{"history", "a.php", "--project", dir}, args...), Env{Stdout: &out, Stderr: io.Discard, Getenv: func(string) string { return "" }, Options: env.Options})
		return code, out.String()
	}
	code, out := hist()
	if code != ExitOK || !strings.Contains(out, "#2") || !strings.Contains(out, "#1") {
		t.Fatalf("history %d %q", code, out)
	}
	if _, out = hist("--show", "1"); out != "<?php // 1\n" {
		t.Fatalf("show %q", out)
	}
	if _, out = hist("--show", "1", "--json"); !strings.Contains(out, `"revision"`) {
		t.Fatalf("show json %q", out)
	}
	if code, out = hist("--restore", "1"); code != ExitOK || !strings.Contains(out, "restored") {
		t.Fatalf("restore %d %q", code, out)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(data) != "<?php // 1\n" {
		t.Fatalf("disk %q", data)
	}
	if code, _ = hist("--show", "x"); code != ExitUsage {
		t.Fatal("bad id")
	}
	if code, _ = hist("--restore", "x"); code != ExitUsage {
		t.Fatal("bad restore id")
	}
	if code, _ = hist("--show", "99"); code != ExitError {
		t.Fatal("missing revision")
	}
	var out2 bytes.Buffer
	Run(ctx, []string{"history", "other.php", "--project", dir}, Env{Stdout: &out2, Getenv: func(string) string { return "" }, Options: env.Options})
	if !strings.Contains(out2.String(), "no history") {
		t.Fatalf("empty history %q", out2.String())
	}
}

func TestServeCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	in := `{"jsonrpc":"2.0","id":1,"method":"doc.open","params":{"path":"a.php"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"doc.insert","params":{"path":"a.php","at":{"line":2},"text":"echo 1;\n"}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"doc.save","params":{"path":"a.php"}}` + "\n"
	code, out, errs := run(t, in, "serve", "--project", dir)
	if code != ExitOK || strings.Count(out, `"result"`) != 3 {
		t.Fatalf("serve %d %q %q", code, out, errs)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(data) != "<?php\necho 1;\n" {
		t.Fatalf("disk %q", data)
	}
	lsp := "Content-Length: 46\r\n\r\n" + `{"jsonrpc":"2.0","id":1,"method":"app.info"}  `
	if code, out, _ := run(t, lsp, "serve", "--framing", "lsp", "--concurrent"); code != ExitOK || !strings.HasPrefix(out, "Content-Length:") {
		t.Fatalf("lsp framing %d %q", code, out)
	}
	if code, _, _ := run(t, "", "serve", "--framing", "xml"); code != ExitUsage {
		t.Fatal("bad framing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"serve"}, testEnv(t, pr, io.Discard, io.Discard))
	}()
	cancel()
	if code := <-done; code != ExitOK {
		t.Fatalf("interrupted serve: %d", code)
	}
}

// lockedBuffer is a goroutine-safe output buffer.
type lockedBuffer struct {
	ch chan string
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.ch <- string(p)
	return len(p), nil
}

func TestDebugListenCommand(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if code, _, _ := run(t, "", "debug-listen", "--map", "bad"); code != ExitUsage {
		t.Fatal("bad mapping")
	}

	out := &lockedBuffer{ch: make(chan string, 100)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"debug-listen", "--json", "--addr", addr, "--map", "/host=/remote"}, testEnv(t, nil, out, io.Discard))
	}()
	select {
	case line := <-out.ch:
		if !strings.Contains(line, `"debug.listening"`) {
			t.Fatalf("first line %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no listening event")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close() // closed before init: the server just drops it
	cancel()
	if code := <-done; code != ExitOK {
		t.Fatalf("exit %d", code)
	}

	// Without --json, events are "topic payload" lines; a busy port fails.
	ln, _ = net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if code, _, errs := run(t, "", "debug-listen", "--addr", ln.Addr().String()); code != ExitError || errs == "" {
		t.Fatalf("busy port: %d %q", code, errs)
	}
	_ = bufio.NewReader
}

// --- End-to-end: build the real binary and drive it. ---

// exeName adds the executable suffix of the OS: Windows only starts files
// with an extension from PATHEXT, and go build -o keeps the name as given.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// builtBinary is a binary built at most once per test run and shared by
// all tests of the package (building per test cost a link of the whole
// IDE each time).
type builtBinary struct {
	once sync.Once
	path string
	out  []byte
	err  error
}

var (
	tiegelBin, fakeLSPBin builtBinary

	buildDirOnce sync.Once
	buildDir     string
	buildDirErr  error
)

func TestMain(m *testing.M) {
	dockertest.Main() // the test binary doubles as fake docker
	runHelperMain()   // … and as the program of the run tests
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// buildOnce builds pkg (with extra go build flags) into a shared temporary
// directory on first use and returns the binary path.
func buildOnce(t *testing.T, b *builtBinary, name, pkg string, flags ...string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("E2E tests need go build")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not found")
	}
	b.once.Do(func() {
		buildDirOnce.Do(func() { buildDir, buildDirErr = os.MkdirTemp("", "tiegel-headless-test-") })
		if b.err = buildDirErr; b.err != nil {
			return
		}
		b.path = filepath.Join(buildDir, exeName(name))
		args := append(append([]string{"build"}, flags...), "-o", b.path, pkg)
		b.out, b.err = exec.Command(goBin, args...).CombinedOutput()
	})
	if b.err != nil {
		t.Fatalf("go build %s: %v\n%s", name, b.err, b.out)
	}
	return b.path
}

func buildBinary(t *testing.T) string {
	t.Helper()
	// The E2E tests only drive --headless: build without the GUI (D84), so
	// they need no Gio toolchain (cgo on macOS/Linux) and link faster.
	return buildOnce(t, &tiegelBin, "tiegel", "github.com/ape-dev-de/tiegel/cmd/tiegel", "-tags", "headless")
}

func e2e(t *testing.T, bin, cfg, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "TIEGEL_CONFIG_DIR="+cfg)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func TestE2E(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	cfg := t.TempDir()
	dir := project(t, map[string]string{"a.php": "<?php\n", ".gitignore": "*.tmp\n", "x.tmp": "", "src/B.php": "<?php"})

	code, out, errs := e2e(t, bin, cfg, "", "--headless", "info", "--json", "--project", dir)
	if code != 0 {
		t.Fatalf("info: %d %s", code, errs)
	}
	var info struct {
		App     api.AppInfo     `json:"app"`
		Project api.ProjectInfo `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("info json: %v\n%s", err, out)
	}
	if !info.Project.Open || info.App.ConfigDir != cfg || info.App.APIVersion != api.Version {
		t.Fatalf("info %+v", info)
	}

	code, out, _ = e2e(t, bin, cfg, "", "--headless", "files", "--project", dir)
	if code != 0 || out != "src/\nsrc/B.php\n.gitignore\na.php\n" {
		t.Fatalf("files %d %q", code, out)
	}

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"topics":["doc.saved"]}}`,
		`{"jsonrpc":"2.0","id":2,"method":"doc.open","params":{"path":"a.php"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"doc.insert","params":{"path":"a.php","at":{"line":2},"text":"echo 'e2e';\n"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"doc.save","params":{"path":"a.php"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"history.list","params":{"path":"a.php"}}`,
	}, "\n") + "\n"
	code, out, errs = e2e(t, bin, cfg, in, "--headless", "serve", "--project", dir)
	if code != 0 {
		t.Fatalf("serve: %d %s", code, errs)
	}
	responses := map[float64]map[string]any{}
	sawSaved := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q", line)
		}
		if id, ok := m["id"].(float64); ok {
			responses[id] = m
		} else if m["method"] == "event" {
			sawSaved = sawSaved || m["params"].(map[string]any)["topic"] == "doc.saved"
		}
	}
	for id := 1.0; id <= 5; id++ {
		if responses[id] == nil || responses[id]["error"] != nil {
			t.Fatalf("response %v: %v\n%s", id, responses[id], out)
		}
	}
	if revs := responses[5]["result"].([]any); len(revs) != 1 {
		t.Fatalf("history %v", revs)
	}
	if !sawSaved {
		t.Fatalf("no doc.saved event:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(data) != "<?php\necho 'e2e';\n" {
		t.Fatalf("disk %q", data)
	}

	code, out, _ = e2e(t, bin, cfg, "", "--headless", "call", "doc.text", `{"path":"a.php"}`, "--project", dir)
	if code != 1 {
		t.Fatalf("doc.text on a fresh process must fail (not open): %d %q", code, out)
	}
	code, out, _ = e2e(t, bin, cfg, "", "--headless", "history", "a.php", "--json", "--project", dir)
	if code != 0 || !strings.Contains(out, `"label"`) {
		t.Fatalf("history persisted across processes: %d %q", code, out)
	}
	if code, _, _ := e2e(t, bin, cfg, "", "--headless", "nope"); code != 2 {
		t.Fatalf("usage exit code %d", code)
	}
	if code, out, _ := e2e(t, bin, cfg, "", "--version"); code != 0 || out == "" {
		t.Fatalf("--version %d %q", code, out)
	}
	// The binary is headless-only (buildBinary): starting the GUI explains
	// how to use it instead of failing obscurely.
	if code, _, errs := e2e(t, bin, cfg, "", dir); code != 2 || !strings.Contains(errs, "--headless") {
		t.Fatalf("gui in a headless-only build %d %q", code, errs)
	}
	if code, _, _ := e2e(t, bin, cfg, "", "--frob"); code != 2 {
		t.Fatal("gui bad flag")
	}
}
